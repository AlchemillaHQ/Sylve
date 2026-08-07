package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)


func carpIntPtr(v int) *int {
	return &v
}

func TestValidateCarpConfig(t *testing.T) {
	tests := []struct {
		name        string
		jailType    jailModels.JailType
		dhcp        bool
		carp        bool
		vhid        *int
		advskew     *int
		password    string
		wantErr     string
	}{
		{name: "disabled_skips_validation", carp: false},
		{name: "linux_jail_rejected", jailType: jailModels.JailTypeLinux, carp: true, vhid: carpIntPtr(1), password: "pw", wantErr: "carp_only_supported_on_freebsd_jails"},
		{name: "dhcp_conflict", jailType: jailModels.JailTypeFreeBSD, dhcp: true, carp: true, vhid: carpIntPtr(1), password: "pw", wantErr: "cannot_enable_carp_with_dhcp"},
		{name: "missing_vhid", jailType: jailModels.JailTypeFreeBSD, carp: true, password: "pw", wantErr: "invalid_carp_vhid"},
		{name: "vhid_too_low", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(0), password: "pw", wantErr: "invalid_carp_vhid"},
		{name: "vhid_too_high", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(256), password: "pw", wantErr: "invalid_carp_vhid"},
		{name: "advskew_too_high", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(1), advskew: carpIntPtr(255), password: "pw", wantErr: "invalid_carp_advskew"},
		{name: "advskew_negative", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(1), advskew: carpIntPtr(-1), password: "pw", wantErr: "invalid_carp_advskew"},
		{name: "missing_password", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(1), wantErr: "carp_password_required"},
		{name: "valid", jailType: jailModels.JailTypeFreeBSD, carp: true, vhid: carpIntPtr(1), advskew: carpIntPtr(0), password: "pw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCarpConfig(tt.jailType, tt.dhcp, tt.carp, tt.vhid, tt.advskew, tt.password)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestAddNetworkStoresCarpFieldsOnNetworkRecord(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)

	fakeNetwork := &jailNetworkValidationFakeNetworkService{entries: map[uint]string{}}
	svc := &Service{DB: db, NetworkService: fakeNetwork}

	// Pre-seed allow.raw_sockets so AddNetwork doesn't need to rewrite jail.conf
	// on disk (that path is covered separately by the SyncNetwork rendering test).
	jail := jailModels.Jail{
		CTID:           9403,
		Name:           "jail-carp-store",
		Type:           jailModels.JailTypeFreeBSD,
		AllowedOptions: []string{"allow.raw_sockets"},
	}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	sw := networkModels.StandardSwitch{Name: "sw-carp-store", BridgeName: "vm-sw-carp-store"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed standard switch: %v", err)
	}

	carp := true
	req := jailServiceInterfaces.AddJailNetworkRequest{
		CTID:         jail.CTID,
		Name:         "net-carp-store",
		SwitchName:   sw.Name,
		CARP:         &carp,
		CARPVHID:     carpIntPtr(11),
		CARPAdvSkew:  carpIntPtr(20),
		CARPPassword: "s3cr3t",
		CARPIPv4Raw:  "10.90.3.10/24",
	}

	// SyncNetwork (called at the end of AddNetwork) needs jail config/hook
	// files on disk to fully succeed; that's exercised by the SyncNetwork
	// rendering test below, so the error here is ignored and we only assert
	// on what AddNetwork persists before it gets there.
	_ = svc.AddNetwork(req)

	var networks []jailModels.Network
	if err := db.Where("jid = ?", jail.ID).Find(&networks).Error; err != nil {
		t.Fatalf("failed to load jail networks: %v", err)
	}
	if len(networks) != 1 {
		t.Fatalf("expected 1 network, got %d", len(networks))
	}

	stored := networks[0]
	if !stored.CARP {
		t.Fatal("expected carp to be true")
	}
	if stored.CARPVHID == nil || *stored.CARPVHID != 11 {
		t.Fatalf("expected CARPVHID 11, got %v", stored.CARPVHID)
	}
	if stored.CARPAdvSkew == nil || *stored.CARPAdvSkew != 20 {
		t.Fatalf("expected CARPAdvSkew 20, got %v", stored.CARPAdvSkew)
	}
	if stored.CARPPassword != "s3cr3t" {
		t.Fatalf("expected CARPPassword to be stored, got %q", stored.CARPPassword)
	}
	if stored.CARPIPv4ID == nil {
		t.Fatal("expected CARPIPv4ID to be set")
	}

	var entry networkModels.ObjectEntry
	if err := db.Where("object_id = ?", *stored.CARPIPv4ID).First(&entry).Error; err != nil {
		t.Fatalf("failed to load carp ipv4 entry: %v", err)
	}
	if entry.Value != "10.90.3.10/24" {
		t.Fatalf("expected carp ipv4 entry 10.90.3.10/24, got %q", entry.Value)
	}
}

func TestSyncNetworkRendersCarpAliasLine(t *testing.T) {
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)

	fakeNetwork := &jailNetworkValidationFakeNetworkService{
		entries: map[uint]string{
			5: "10.50.0.100/24",
		},
	}
	svc := &Service{
		DB:             testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{}),
		NetworkService: fakeNetwork,
		ctidHashByCTID: make(map[uint]string),
	}

	ctid := uint(9404)
	mountPoint := t.TempDir()
	rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")
	if err := os.MkdirAll(filepath.Dir(rcConfPath), 0755); err != nil {
		t.Fatalf("create jail etc directory: %v", err)
	}
	if err := os.WriteFile(rcConfPath, []byte(""), 0644); err != nil {
		t.Fatalf("write rc.conf: %v", err)
	}

	jailDir := filepath.Join(dataPath, "jails", fmt.Sprintf("%d", ctid))
	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctid))
	jailConfig := fmt.Sprintf("carphash {\n\tpath = \"%s\";\n\tvnet;\n}\n", mountPoint)
	if err := os.MkdirAll(filepath.Dir(jailConfigPath), 0755); err != nil {
		t.Fatalf("create jail config directory: %v", err)
	}
	if err := os.WriteFile(jailConfigPath, []byte(jailConfig), 0644); err != nil {
		t.Fatalf("write jail config: %v", err)
	}

	scriptsDir := filepath.Join(jailDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("create scripts directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "pre-start.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write pre-start hook: %v", err)
	}

	vhid := 12
	advskew := 20
	carpIPv4ID := uint(5)

	err := svc.SyncNetwork(ctid, jailModels.Jail{
		CTID: ctid,
		Type: jailModels.JailTypeFreeBSD,
		Networks: []jailModels.Network{{
			ID:           7,
			SwitchID:     1,
			CARP:         true,
			CARPVHID:     &vhid,
			CARPAdvSkew:  &advskew,
			CARPPassword: "s3cr3t",
			CARPIPv4ID:   &carpIPv4ID,
		}},
	})
	if err != nil {
		t.Fatalf("SyncNetwork returned error: %v", err)
	}

	gotRCConf, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("read rc.conf: %v", err)
	}

	ctidHash := svc.GetCTIDHash(ctid)
	wantLine := fmt.Sprintf(`ifconfig_%s_net7b_alias0="vhid 12 pass s3cr3t advskew 20 alias 10.50.0.100 netmask 255.255.255.0"`, ctidHash)
	if !strings.Contains(string(gotRCConf), wantLine) {
		t.Fatalf("rc.conf missing expected CARP alias line, got:\n%s\nwant substring:\n%s", gotRCConf, wantLine)
	}
}
