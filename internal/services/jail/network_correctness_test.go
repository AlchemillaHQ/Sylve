// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type jailNetworkSyncFixture struct {
	service     *Service
	db          *gorm.DB
	jail        jailModels.Jail
	mountPoint  string
	switchID    uint
	network     *jailNetworkValidationFakeNetworkService
	objectIndex int
}

func newJailNetworkSyncFixture(t *testing.T, jailType jailModels.JailType, ctID uint) *jailNetworkSyncFixture {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", tempDir(t))
	mountPoint := tempDir(t)
	if err := os.MkdirAll(filepath.Join(mountPoint, "etc"), 0o755); err != nil {
		t.Fatalf("create jail etc directory: %v", err)
	}

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)
	switchRow := networkModels.ManualSwitch{Name: fmt.Sprintf("sync-switch-%d", ctID), Bridge: fmt.Sprintf("vm-sync-%d", ctID)}
	if err := db.Create(&switchRow).Error; err != nil {
		t.Fatalf("create manual switch: %v", err)
	}
	jail := jailModels.Jail{CTID: ctID, Name: fmt.Sprintf("sync-jail-%d", ctID), Type: jailType}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	network := &jailNetworkValidationFakeNetworkService{entries: map[uint]string{}}
	service := &Service{DB: db, NetworkService: network, ctidHashByCTID: make(map[uint]string)}
	originalInspect := jailInspectBridgeVLAN
	jailInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{}, nil
	}
	t.Cleanup(func() { jailInspectBridgeVLAN = originalInspect })
	attachJailRootTestFixture(t, service, db, jail.ID, ctID, mountPoint)
	cfg, err := service.CreateJailConfig(jail, mountPoint)
	if err != nil {
		t.Fatalf("create structural jail config: %v", err)
	}
	if err := service.SaveJailConfig(ctID, cfg); err != nil {
		t.Fatalf("save structural jail config: %v", err)
	}

	return &jailNetworkSyncFixture{
		service:    service,
		db:         db,
		jail:       jail,
		mountPoint: mountPoint,
		switchID:   switchRow.ID,
		network:    network,
	}
}

func (f *jailNetworkSyncFixture) addObject(t *testing.T, objectType, value string) uint {
	t.Helper()
	f.objectIndex++
	object := networkModels.Object{
		Name: fmt.Sprintf("sync-object-%d-%d", f.jail.CTID, f.objectIndex),
		Type: objectType,
	}
	if err := f.db.Create(&object).Error; err != nil {
		t.Fatalf("create network object: %v", err)
	}
	if err := f.db.Create(&networkModels.ObjectEntry{ObjectID: object.ID, Value: value}).Error; err != nil {
		t.Fatalf("create network object entry: %v", err)
	}
	f.network.entries[object.ID] = value
	return object.ID
}

func (f *jailNetworkSyncFixture) addNetwork(t *testing.T, network jailModels.Network) jailModels.Network {
	t.Helper()
	network.JailID = f.jail.ID
	network.SwitchID = f.switchID
	network.SwitchType = "manual"
	if err := f.db.Create(&network).Error; err != nil {
		t.Fatalf("create jail network: %v", err)
	}
	f.jail.Networks = append(f.jail.Networks, network)
	return network
}

func TestValidateJailNetworkVLANPolicy(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.ManualSwitch{})
	defaultVLAN := 10
	filtered := networkModels.ManualSwitch{Name: "filtered-policy", Bridge: "bridge-policy-filtered"}
	unfiltered := networkModels.ManualSwitch{Name: "plain-policy", Bridge: "bridge-policy-plain"}
	if err := db.Create(&filtered).Error; err != nil {
		t.Fatalf("create filtered switch: %v", err)
	}
	if err := db.Create(&unfiltered).Error; err != nil {
		t.Fatalf("create unfiltered switch: %v", err)
	}
	originalInspect := jailInspectBridgeVLAN
	jailInspectBridgeVLAN = func(bridge string) (bridgevlan.BridgeState, error) {
		if bridge == filtered.Bridge {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
		}
		return bridgevlan.BridgeState{}, nil
	}
	t.Cleanup(func() { jailInspectBridgeVLAN = originalInspect })

	t.Run("normalizes access policy", func(t *testing.T) {
		accessVLAN := 20
		network := jailModels.Network{
			SwitchID: filtered.ID, SwitchType: "manual",
			VLANPolicy: bridgevlan.PortPolicy{Mode: " ACCESS ", UntaggedVLAN: &accessVLAN},
		}
		_, config, err := ResolveDesiredNetworkAttachment(db, &network)
		if err != nil {
			t.Fatalf("validate access policy: %v", err)
		}
		if !config.VLANFiltering || config.DefaultAccessVLAN == nil || *config.DefaultAccessVLAN != defaultVLAN {
			t.Fatalf("switch VLAN config = %#v", config)
		}
		if network.VLANPolicy.Mode != bridgevlan.ModeAccess || network.VLANPolicy.UntaggedVLAN == nil || *network.VLANPolicy.UntaggedVLAN != accessVLAN || network.VLANPolicy.TaggedVLANs == nil {
			t.Fatalf("normalized access policy = %#v", network.VLANPolicy)
		}
	})

	t.Run("normalizes trunk policy", func(t *testing.T) {
		nativeVLAN := 11
		network := jailModels.Network{
			SwitchID: filtered.ID, SwitchType: "manual",
			VLANPolicy: bridgevlan.PortPolicy{
				Mode: bridgevlan.ModeTrunk, UntaggedVLAN: &nativeVLAN, TaggedVLANs: []int{30, 20, 30},
			},
		}
		if _, _, err := ResolveDesiredNetworkAttachment(db, &network); err != nil {
			t.Fatalf("validate trunk policy: %v", err)
		}
		if got := fmt.Sprint(network.VLANPolicy.TaggedVLANs); got != "[20 30]" {
			t.Fatalf("normalized tagged VLANs = %s, want [20 30]", got)
		}
	})

	t.Run("requires policy on filtered switch", func(t *testing.T) {
		network := jailModels.Network{SwitchID: filtered.ID, SwitchType: "manual"}
		_, _, err := ResolveDesiredNetworkAttachment(db, &network)
		if err == nil || !strings.Contains(err.Error(), "filtered_switch_vlan_policy_required") {
			t.Fatalf("expected required policy error, got %v", err)
		}
	})

	t.Run("rejects policy on unfiltered switch", func(t *testing.T) {
		accessVLAN := 20
		network := jailModels.Network{
			SwitchID: unfiltered.ID, SwitchType: "manual",
			VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
		}
		_, _, err := ResolveDesiredNetworkAttachment(db, &network)
		if err == nil || !strings.Contains(err.Error(), "vlan_policy_requires_filtered_switch") {
			t.Fatalf("expected unfiltered switch policy error, got %v", err)
		}
	})

	t.Run("accepts empty policy on unfiltered switch", func(t *testing.T) {
		network := jailModels.Network{SwitchID: unfiltered.ID, SwitchType: "manual"}
		if _, _, err := ResolveDesiredNetworkAttachment(db, &network); err != nil {
			t.Fatalf("validate empty policy: %v", err)
		}
	})
}

func TestConfigureJailFilteredMembersAppliesPolicyWhileDown(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7289)
	defaultVLAN := 10
	bridgeName := fmt.Sprintf("vm-sync-%d", fixture.jail.CTID)
	jailInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
	}
	accessVLAN := 20
	network := fixture.addNetwork(t, jailModels.Network{
		Name: "filtered",
		VLANPolicy: bridgevlan.PortPolicy{
			Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN, TaggedVLANs: []int{},
		},
	})
	epairA := fmt.Sprintf("%s_net%da", fixture.service.GetCTIDHash(fixture.jail.CTID), network.ID)

	originalDown := jailSetFilteredMemberDown
	originalConfigure := jailConfigureFilteredMember
	originalPrivate := jailSetMemberPrivate
	calls := make([]string, 0, 3)
	jailSetFilteredMemberDown = func(member string) error {
		if member != epairA {
			t.Fatalf("lowered member = %q, want %q", member, epairA)
		}
		calls = append(calls, "down:"+member)
		return nil
	}
	jailConfigureFilteredMember = func(
		bridge, member string, expectedDefault *int, policy bridgevlan.PortPolicy,
	) error {
		if bridge != bridgeName || member != epairA {
			t.Fatalf("configured target = %s/%s, want %s/%s", bridge, member, bridgeName, epairA)
		}
		if expectedDefault == nil || *expectedDefault != defaultVLAN {
			t.Fatalf("default access VLAN = %v, want %d", expectedDefault, defaultVLAN)
		}
		if policy.Mode != bridgevlan.ModeAccess || policy.UntaggedVLAN == nil || *policy.UntaggedVLAN != accessVLAN {
			t.Fatalf("configured policy = %#v", policy)
		}
		calls = append(calls, "configure:"+member)
		return nil
	}
	jailSetMemberPrivate = func(bridge, member string, private bool) error {
		if bridge != bridgeName || member != epairA || private {
			t.Fatalf("private target = %s/%s private=%t", bridge, member, private)
		}
		calls = append(calls, "private:"+member)
		return nil
	}
	t.Cleanup(func() {
		jailSetFilteredMemberDown = originalDown
		jailConfigureFilteredMember = originalConfigure
		jailSetMemberPrivate = originalPrivate
	})

	if err := fixture.service.configureJailFilteredMembers(fixture.jail); err != nil {
		t.Fatalf("configure filtered jail members: %v", err)
	}
	want := []string{"down:" + epairA, "configure:" + epairA, "private:" + epairA}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("operation order = %v, want %v", calls, want)
	}
}

func TestSyncNetworkFilteredHookOnlyActivatesPreparedMember(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7290)
	defaultVLAN := 10
	jailInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
	}
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:90")
	accessVLAN := 20
	network := fixture.addNetwork(t, jailModels.Network{
		Name: "filtered", MacID: &macID,
		VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN, TaggedVLANs: []int{}},
	})

	originalValidate := jailValidateFilteredBridge
	jailValidateFilteredBridge = func(bridge string, expected *int) (bridgevlan.BridgeState, error) {
		if bridge != fmt.Sprintf("vm-sync-%d", fixture.jail.CTID) || expected == nil || *expected != defaultVLAN {
			t.Fatalf("runtime validation = bridge %q, default VLAN %v", bridge, expected)
		}
		return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
	}
	t.Cleanup(func() { jailValidateFilteredBridge = originalValidate })

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync filtered jail network: %v", err)
	}
	preStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "pre-start")
	if err != nil {
		t.Fatalf("get pre-start hook: %v", err)
	}
	contentBytes, err := os.ReadFile(preStartPath)
	if err != nil {
		t.Fatalf("read pre-start hook: %v", err)
	}
	content := string(contentBytes)
	epairA := fmt.Sprintf("%s_net%da", fixture.service.GetCTIDHash(fixture.jail.CTID), network.ID)
	downIndex := strings.Index(content, fmt.Sprintf("ifconfig %s ether ", epairA))
	upIndex := strings.Index(content, fmt.Sprintf("ifconfig %s up\n", epairA))
	if downIndex < 0 || upIndex < 0 || downIndex >= upIndex {
		t.Fatalf("filtered hook does not keep the member down until activation:\n%s", content)
	}
	if strings.Contains(content, "bridge-vlan-member") {
		t.Fatalf("filtered hook still invokes the removed helper command:\n%s", content)
	}
	if strings.Contains(content, fmt.Sprintf("ifconfig vm-test addm %s", epairA)) {
		t.Fatalf("filtered hook contains an unconfigured bridge attachment:\n%s", content)
	}
}

func TestSyncNetworkPrivateStandardSwitchIsolatesMemberBeforeActivation(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7293)
	switchRow := networkModels.StandardSwitch{
		Name:       "private-standard",
		BridgeName: "vm-private",
		Private:    true,
	}
	if err := fixture.db.Create(&switchRow).Error; err != nil {
		t.Fatalf("create private Standard Switch: %v", err)
	}
	mac := "02:00:00:00:72:93"
	macID := fixture.addObject(t, "Mac", mac)
	network := jailModels.Network{
		JailID:     fixture.jail.ID,
		Name:       "private",
		SwitchID:   switchRow.ID,
		SwitchType: "standard",
		MacID:      &macID,
	}
	if err := fixture.db.Create(&network).Error; err != nil {
		t.Fatalf("create private jail network: %v", err)
	}
	fixture.jail.Networks = append(fixture.jail.Networks, network)

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync private jail network: %v", err)
	}
	preStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "pre-start")
	if err != nil {
		t.Fatalf("get pre-start hook: %v", err)
	}
	contentBytes, err := os.ReadFile(preStartPath)
	if err != nil {
		t.Fatalf("read pre-start hook: %v", err)
	}
	content := string(contentBytes)
	epairA := fmt.Sprintf("%s_net%da", fixture.service.GetCTIDHash(fixture.jail.CTID), network.ID)
	previousMAC, err := utils.PreviousMAC(mac)
	if err != nil {
		t.Fatalf("derive host epair MAC: %v", err)
	}
	commands := []string{
		fmt.Sprintf("ifconfig %s ether %s down", epairA, previousMAC),
		fmt.Sprintf("ifconfig %s addm %s", switchRow.BridgeName, epairA),
		fmt.Sprintf("ifconfig %s private %s", switchRow.BridgeName, epairA),
		fmt.Sprintf("ifconfig %s up", epairA),
	}
	previousIndex := -1
	for _, command := range commands {
		index := strings.Index(content, command)
		if index < 0 {
			t.Fatalf("pre-start hook is missing %q:\n%s", command, content)
		}
		if index <= previousIndex {
			t.Fatalf("pre-start hook command %q is out of order:\n%s", command, content)
		}
		previousIndex = index
	}
}

func TestSyncNetworkObservesManualSwitchRuntimeVLANMode(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7291)
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:91")
	fixture.addNetwork(t, jailModels.Network{Name: "plain", MacID: &macID})
	jailInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{VLANFiltering: true}, nil
	}

	err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail)
	if err == nil || !strings.Contains(err.Error(), "filtered_switch_vlan_policy_required") {
		t.Fatalf("expected live filtered mode to require a policy, got %v", err)
	}
}

func TestLoadJailNetworkRejectsCrossJailMembership(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)
	first := jailModels.Jail{CTID: 7101, Name: "first", Type: jailModels.JailTypeFreeBSD}
	second := jailModels.Jail{CTID: 7102, Name: "second", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("seed first jail: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed second jail: %v", err)
	}
	switchRow := networkModels.StandardSwitch{Name: "membership-switch", BridgeName: "vm-membership"}
	if err := db.Create(&switchRow).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}
	network := jailModels.Network{JailID: second.ID, Name: "second-net", SwitchID: switchRow.ID, SwitchType: "standard"}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("seed network: %v", err)
	}

	_, err := loadJailNetwork(db, first.ID, network.ID)
	if err == nil || !strings.Contains(err.Error(), "network_not_found") {
		t.Fatalf("expected scoped network_not_found, got %v", err)
	}
}

func TestReplaceJailNetworkRCConfBlock(t *testing.T) {
	const (
		hostname = `hostname="jail.example"`
		sshd     = `sshd_enable="YES"`
		em0      = `ifconfig_em0="DHCP"`
		dhcp2    = `ifconfig_aaaep_net2b="SYNCDHCP"`
		dhcp7    = `ifconfig_aaaep_net7b="SYNCDHCP"`
		oldAddr  = `ifconfig_aaaep_net2b="inet 192.0.2.10 netmask 255.255.255.0"`
		oldGW    = `defaultrouter="192.0.2.1"`
		newAddr  = `ifconfig_aaaep_net2b="inet 198.51.100.10 netmask 255.255.255.0"`
		newGW    = `defaultrouter="198.51.100.1"`
		slaac2   = `ifconfig_aaaep_net2b_ipv6="inet6 accept_rtadv"`
		slaac7   = `ifconfig_aaaep_net7b_ipv6="inet6 accept_rtadv"`
		rtsold   = `rtsold_enable="YES"`
	)
	block := func(lines ...string) []string {
		return append(append([]string{jailNetworkRCConfStart}, lines...), jailNetworkRCConfEnd)
	}
	tests := []struct {
		name     string
		original []string
		lines    []string
		once     []string // must appear exactly once
		absent   []string
	}{
		{
			name:     "preserves unmanaged configuration",
			original: slices.Concat([]string{hostname, em0}, block(`ifconfig_old="SYNCDHCP"`), []string{sshd}),
			lines:    []string{`ifconfig_new="SYNCDHCP"`},
			once:     []string{hostname, em0, sshd, `ifconfig_new="SYNCDHCP"`},
			absent:   []string{`ifconfig_old="SYNCDHCP"`},
		},
		{
			name:     "repairs reported dhcp duplicate",
			original: slices.Concat([]string{dhcp2}, block(dhcp2)),
			lines:    []string{dhcp2},
			once:     []string{dhcp2, jailNetworkRCConfStart},
		},
		{
			name:     "keeps one line per dhcp interface",
			original: slices.Concat([]string{dhcp2}, block(dhcp2, dhcp7)),
			lines:    []string{dhcp2, dhcp7},
			once:     []string{dhcp2, dhcp7, jailNetworkRCConfStart},
		},
		{
			name:     "migrates static configuration",
			original: slices.Concat([]string{hostname, oldAddr, oldGW}, block(oldAddr, oldGW), []string{sshd}),
			lines:    []string{newAddr, newGW},
			once:     []string{hostname, sshd, newAddr, newGW},
			absent:   []string{oldAddr, oldGW},
		},
		{
			name:  "deduplicates global slaac setting",
			lines: []string{slaac2, rtsold, slaac7, rtsold},
			once:  []string{slaac2, slaac7, rtsold},
		},
		{
			name:     "removes markerless sylve interface",
			original: []string{dhcp2, em0, sshd},
			once:     []string{em0, sshd},
			absent:   []string{`ifconfig_aaaep_net2b=`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated := replaceJailNetworkRCConfBlock(strings.Join(append(tt.original, ""), "\n"), "aaaep", tt.lines)
			for _, want := range tt.once {
				if got := strings.Count(updated, want); got != 1 {
					t.Fatalf("expected %q once, got %d:\n%s", want, got, updated)
				}
			}
			for _, stale := range tt.absent {
				if strings.Contains(updated, stale) {
					t.Fatalf("stale line %q remained:\n%s", stale, updated)
				}
			}
			if again := replaceJailNetworkRCConfBlock(updated, "aaaep", tt.lines); again != updated {
				t.Fatalf("expected reconciliation to be idempotent:\nfirst:\n%s\nsecond:\n%s", updated, again)
			}
		})
	}
}

func TestSyncNetworkRendersMultipleFreeBSDDHCPInterfacesOnce(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7201)
	firstMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:01")
	secondMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:02")
	first := fixture.addNetwork(t, jailModels.Network{Name: "dhcp-1", MacID: &firstMAC, DHCP: true, DefaultGateway: true})
	second := fixture.addNetwork(t, jailModels.Network{Name: "dhcp-2", MacID: &secondMAC, DHCP: true})

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync multiple DHCP interfaces: %v", err)
	}
	rcConfPath := filepath.Join(fixture.mountPoint, "etc", "rc.conf")
	firstRCConf, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("read DHCP rc.conf: %v", err)
	}
	firstConfig, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("read DHCP jail config: %v", err)
	}
	preStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "pre-start")
	if err != nil {
		t.Fatalf("get DHCP pre-start hook: %v", err)
	}
	firstPreStart, err := os.ReadFile(preStartPath)
	if err != nil {
		t.Fatalf("read DHCP pre-start hook: %v", err)
	}

	hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
	for _, network := range []jailModels.Network{first, second} {
		epairB := fmt.Sprintf("%s_net%db", hash, network.ID)
		rcLine := fmt.Sprintf(`ifconfig_%s="SYNCDHCP"`, epairB)
		if got := strings.Count(string(firstRCConf), rcLine); got != 1 {
			t.Fatalf("DHCP line %q count = %d, want 1:\n%s", rcLine, got, firstRCConf)
		}
		interfaceLine := fmt.Sprintf(`vnet.interface += "%s";`, epairB)
		if got := strings.Count(firstConfig, interfaceLine); got != 1 {
			t.Fatalf("jail interface line %q count = %d, want 1:\n%s", interfaceLine, got, firstConfig)
		}
		if got := strings.Count(string(firstPreStart), "# Setup Network Interface "+epairB); got != 1 {
			t.Fatalf("pre-start setup for %s count = %d, want 1:\n%s", epairB, got, firstPreStart)
		}
	}
	if got := strings.Count(string(firstRCConf), jailNetworkRCConfStart); got != 1 {
		t.Fatalf("managed rc.conf block count = %d, want 1:\n%s", got, firstRCConf)
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("repeat multiple DHCP sync: %v", err)
	}
	secondRCConf, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("reread DHCP rc.conf: %v", err)
	}
	secondConfig, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("reread DHCP jail config: %v", err)
	}
	secondPreStart, err := os.ReadFile(preStartPath)
	if err != nil {
		t.Fatalf("reread DHCP pre-start hook: %v", err)
	}
	if string(secondRCConf) != string(firstRCConf) {
		t.Fatalf("repeating DHCP network sync changed rc.conf:\nfirst:\n%s\nsecond:\n%s", firstRCConf, secondRCConf)
	}
	if secondConfig != firstConfig {
		t.Fatalf("repeating DHCP network sync changed jail config:\nfirst:\n%s\nsecond:\n%s", firstConfig, secondConfig)
	}
	if string(secondPreStart) != string(firstPreStart) {
		t.Fatalf("repeating DHCP network sync changed pre-start hook:\nfirst:\n%s\nsecond:\n%s", firstPreStart, secondPreStart)
	}
}

func TestSyncNetworkPreservesQuotedInheritedAddressSyntax(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7204)
	fixture.jail.InheritIPv4 = true
	fixture.jail.InheritIPv6 = true

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync inherited network configuration: %v", err)
	}
	config, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("read inherited jail config: %v", err)
	}
	for _, line := range []string{`ip4="inherit";`, `ip6="inherit";`} {
		if got := strings.Count(config, line); got != 1 {
			t.Fatalf("inherited line %q count = %d, want 1:\n%s", line, got, config)
		}
	}
}

func TestSyncNetworkRepairsFreeBSDStaticConfiguration(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7202)
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:03")
	ipv4ID := fixture.addObject(t, "Network", "192.0.2.10/24")
	ipv4GatewayID := fixture.addObject(t, "Host", "192.0.2.1")
	ipv6ID := fixture.addObject(t, "Network", "2001:db8::10/64")
	ipv6GatewayID := fixture.addObject(t, "Host", "2001:db8::1")
	network := fixture.addNetwork(t, jailModels.Network{
		Name:           "static",
		MacID:          &macID,
		IPv4ID:         &ipv4ID,
		IPv4GwID:       &ipv4GatewayID,
		IPv6ID:         &ipv6ID,
		IPv6GwID:       &ipv6GatewayID,
		DefaultGateway: true,
	})

	hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
	epairB := fmt.Sprintf("%s_net%db", hash, network.ID)
	expected := []string{
		fmt.Sprintf(`ifconfig_%s="inet 192.0.2.10 netmask 255.255.255.0"`, epairB),
		`defaultrouter="192.0.2.1"`,
		fmt.Sprintf(`ifconfig_%s_ipv6="inet6 2001:db8::10/64"`, epairB),
		`ipv6_defaultrouter="2001:db8::1"`,
	}
	legacyDuplicate := strings.Join([]string{
		`hostname="static.example"`,
		expected[0],
		expected[1],
		jailNetworkRCConfStart,
		expected[0],
		expected[1],
		expected[2],
		expected[3],
		jailNetworkRCConfEnd,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(fixture.mountPoint, "etc", "rc.conf"), []byte(legacyDuplicate), 0o644); err != nil {
		t.Fatalf("seed duplicate static rc.conf: %v", err)
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync static interface: %v", err)
	}
	rcConf, err := os.ReadFile(filepath.Join(fixture.mountPoint, "etc", "rc.conf"))
	if err != nil {
		t.Fatalf("read static rc.conf: %v", err)
	}
	for _, line := range expected {
		if got := strings.Count(string(rcConf), line); got != 1 {
			t.Fatalf("static line %q count = %d, want 1:\n%s", line, got, rcConf)
		}
	}
	if got := strings.Count(string(rcConf), `hostname="static.example"`); got != 1 {
		t.Fatalf("unmanaged hostname count = %d, want 1:\n%s", got, rcConf)
	}
}

func TestIPv4GatewayOffLink(t *testing.T) {
	tests := []struct {
		cidr, gateway string
		want          bool
	}{
		{"192.0.2.10/32", "192.0.2.1", true},
		{"192.0.2.10/24", "192.0.2.1", false},
		{"192.0.2.10/30", "192.0.2.1", true},
		{"192.0.2.10/32", "192.0.2.10", false},
		{"192.0.2.10", "192.0.2.1", false},
		{"192.0.2.10/32", "not-an-ip", false},
	}
	for _, tt := range tests {
		if got := ipv4GatewayOffLink(tt.cidr, tt.gateway); got != tt.want {
			t.Errorf("ipv4GatewayOffLink(%q, %q) = %v, want %v", tt.cidr, tt.gateway, got, tt.want)
		}
	}
}

func TestSyncNetworkAddsHostRouteForFreeBSDSlash32Gateway(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7206)
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:07")
	ipv4ID := fixture.addObject(t, "Network", "192.0.2.10/32")
	ipv4GatewayID := fixture.addObject(t, "Host", "192.0.2.1")
	network := fixture.addNetwork(t, jailModels.Network{
		Name:           "slash32",
		MacID:          &macID,
		IPv4ID:         &ipv4ID,
		IPv4GwID:       &ipv4GatewayID,
		DefaultGateway: true,
	})
	rcConfPath := filepath.Join(fixture.mountPoint, "etc", "rc.conf")
	if err := os.WriteFile(rcConfPath, []byte("static_routes=\"user\"\nroute_user=\"-net 198.51.100.0/24 192.0.2.1\"\n"), 0o644); err != nil {
		t.Fatalf("seed rc.conf: %v", err)
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync /32 interface: %v", err)
	}
	rcConf, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("read /32 rc.conf: %v", err)
	}
	epairB := fmt.Sprintf("%s_net%db", fixture.service.GetCTIDHash(fixture.jail.CTID), network.ID)
	routeName := fmt.Sprintf("sylve_gw%d", network.ID)
	ordered := []string{
		`static_routes="user"`,
		fmt.Sprintf(`ifconfig_%s="inet 192.0.2.10 netmask 255.255.255.255"`, epairB),
		fmt.Sprintf(`static_routes="${static_routes} %s"`, routeName),
		fmt.Sprintf(`route_%s="-host 192.0.2.1 -interface %s"`, routeName, epairB),
		`defaultrouter="192.0.2.1"`,
	}
	last := -1
	for _, line := range ordered {
		if got := strings.Count(string(rcConf), line); got != 1 {
			t.Fatalf("/32 line %q count = %d, want 1:\n%s", line, got, rcConf)
		}
		index := strings.Index(string(rcConf), line)
		if index < last {
			t.Fatalf("/32 line %q is out of order:\n%s", line, rcConf)
		}
		last = index
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("repeat /32 sync: %v", err)
	}
	repeated, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("reread /32 rc.conf: %v", err)
	}
	if string(repeated) != string(rcConf) {
		t.Fatalf("repeating /32 sync changed rc.conf:\nfirst:\n%s\nsecond:\n%s", rcConf, repeated)
	}

	fixture.network.entries[ipv4ID] = "192.0.2.10/24"
	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync /24 interface: %v", err)
	}
	onLink, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("read /24 rc.conf: %v", err)
	}
	if strings.Contains(string(onLink), routeName) {
		t.Fatalf("on-link gateway kept the /32 host route:\n%s", onLink)
	}
	for _, line := range []string{`static_routes="user"`, `defaultrouter="192.0.2.1"`} {
		if got := strings.Count(string(onLink), line); got != 1 {
			t.Fatalf("/24 line %q count = %d, want 1:\n%s", line, got, onLink)
		}
	}
}

func TestSyncNetworkAddsHostRouteForLinuxSlash32Gateway(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeLinux, 7207)
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:08")
	ipv4ID := fixture.addObject(t, "Network", "192.0.2.20/32")
	ipv4GatewayID := fixture.addObject(t, "Host", "192.0.2.1")
	network := fixture.addNetwork(t, jailModels.Network{
		Name:           "slash32",
		MacID:          &macID,
		IPv4ID:         &ipv4ID,
		IPv4GwID:       &ipv4GatewayID,
		DefaultGateway: true,
	})

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync Linux /32 interface: %v", err)
	}
	postStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "post-start")
	if err != nil {
		t.Fatalf("get Linux post-start hook: %v", err)
	}
	postStart, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatalf("read Linux post-start hook: %v", err)
	}
	hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
	epairB := fmt.Sprintf("%s_net%db", hash, network.ID)
	hostRoute := fmt.Sprintf("route -j %s add -host 192.0.2.1 -interface %s", hash, epairB)
	defaultRoute := fmt.Sprintf("route -j %s add default 192.0.2.1", hash)
	for _, command := range []string{hostRoute, defaultRoute} {
		if got := strings.Count(string(postStart), command); got != 1 {
			t.Fatalf("Linux /32 command %q count = %d, want 1:\n%s", command, got, postStart)
		}
	}
	if strings.Index(string(postStart), hostRoute) > strings.Index(string(postStart), defaultRoute) {
		t.Fatalf("Linux host route must precede the default route:\n%s", postStart)
	}
}

func TestSyncNetworkRendersMultipleLinuxStaticInterfaces(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeLinux, 7203)
	firstMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:04")
	firstIPv4 := fixture.addObject(t, "Network", "198.51.100.10/24")
	firstIPv4Gateway := fixture.addObject(t, "Host", "198.51.100.1")
	firstIPv6 := fixture.addObject(t, "Network", "2001:db8:1::10/64")
	firstIPv6Gateway := fixture.addObject(t, "Host", "2001:db8:1::1")
	secondMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:05")
	secondIPv4 := fixture.addObject(t, "Network", "203.0.113.10/24")
	first := fixture.addNetwork(t, jailModels.Network{
		Name:           "static-1",
		MacID:          &firstMAC,
		IPv4ID:         &firstIPv4,
		IPv4GwID:       &firstIPv4Gateway,
		IPv6ID:         &firstIPv6,
		IPv6GwID:       &firstIPv6Gateway,
		DefaultGateway: true,
	})
	second := fixture.addNetwork(t, jailModels.Network{Name: "static-2", MacID: &secondMAC, IPv4ID: &secondIPv4})

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync Linux static interfaces: %v", err)
	}
	postStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "post-start")
	if err != nil {
		t.Fatalf("get Linux post-start hook: %v", err)
	}
	firstPostStart, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatalf("read Linux post-start hook: %v", err)
	}
	hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
	firstEpairB := fmt.Sprintf("%s_net%db", hash, first.ID)
	secondEpairB := fmt.Sprintf("%s_net%db", hash, second.ID)
	expected := []string{
		fmt.Sprintf("ifconfig -j %s %s inet 198.51.100.10/24", hash, firstEpairB),
		fmt.Sprintf("route -j %s add default 198.51.100.1", hash),
		fmt.Sprintf("ifconfig -j %s %s inet6 2001:db8:1::10/64", hash, firstEpairB),
		fmt.Sprintf("route -6 -j %s add default 2001:db8:1::1", hash),
		fmt.Sprintf("ifconfig -j %s %s inet 203.0.113.10/24", hash, secondEpairB),
	}
	for _, line := range expected {
		if got := strings.Count(string(firstPostStart), line); got != 1 {
			t.Fatalf("Linux static command %q count = %d, want 1:\n%s", line, got, firstPostStart)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.mountPoint, "etc", "rc.conf")); !os.IsNotExist(err) {
		t.Fatalf("Linux network sync unexpectedly created rc.conf: %v", err)
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("repeat Linux static sync: %v", err)
	}
	secondPostStart, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatalf("reread Linux post-start hook: %v", err)
	}
	if string(secondPostStart) != string(firstPostStart) {
		t.Fatalf("repeating Linux static network sync changed post-start hook:\nfirst:\n%s\nsecond:\n%s", firstPostStart, secondPostStart)
	}
}

func TestSyncNetworkPreservesLinuxUserStartHooks(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeLinux, 7205)
	macID := fixture.addObject(t, "Mac", "02:00:00:00:72:06")
	ipv4ID := fixture.addObject(t, "Network", "192.0.2.25/24")
	network := fixture.addNetwork(t, jailModels.Network{Name: "static", MacID: &macID, IPv4ID: &ipv4ID})

	startContent := "#!/bin/sh\necho user-start\n"
	startHostPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "start")
	if err != nil {
		t.Fatalf("get Linux start hook: %v", err)
	}
	if err := os.WriteFile(startHostPath, []byte(startContent), 0o755); err != nil {
		t.Fatalf("seed Linux host start hook: %v", err)
	}
	startJailPath := filepath.Join(fixture.mountPoint, "usr", "local", "sylve", "scripts", "start.sh")
	if err := os.WriteFile(startJailPath, []byte(startContent), 0o755); err != nil {
		t.Fatalf("seed Linux in-jail start hook: %v", err)
	}

	postStartContent := "#!/bin/sh\necho user-post-start\n"
	postStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "post-start")
	if err != nil {
		t.Fatalf("get Linux post-start hook: %v", err)
	}
	if err := os.WriteFile(postStartPath, []byte(postStartContent), 0o755); err != nil {
		t.Fatalf("seed Linux post-start hook: %v", err)
	}

	config, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("read structural Linux config: %v", err)
	}
	config, err = fixture.service.AppendToConfig(fixture.jail.CTID, config, fmt.Sprintf(
		"\texec.start += \"/usr/local/sylve/scripts/start.sh\";\n\texec.poststart += \"%s\";\n",
		postStartPath,
	))
	if err != nil {
		t.Fatalf("wire Linux user hooks: %v", err)
	}
	if err := fixture.service.SaveJailConfig(fixture.jail.CTID, config); err != nil {
		t.Fatalf("save Linux user hook config: %v", err)
	}

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync Linux network with user hooks: %v", err)
	}
	updatedConfig, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("read synced Linux config: %v", err)
	}
	for _, line := range []string{
		`exec.start += "/usr/local/sylve/scripts/start.sh";`,
		fmt.Sprintf(`exec.poststart += "%s";`, postStartPath),
	} {
		if got := strings.Count(updatedConfig, line); got != 1 {
			t.Fatalf("Linux user hook reference %q count = %d, want 1:\n%s", line, got, updatedConfig)
		}
	}
	updatedStart, err := os.ReadFile(startHostPath)
	if err != nil {
		t.Fatalf("read synced Linux start hook: %v", err)
	}
	if string(updatedStart) != startContent {
		t.Fatalf("Linux user start hook changed:\nwant:\n%s\ngot:\n%s", startContent, updatedStart)
	}
	updatedPostStart, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatalf("read synced Linux post-start hook: %v", err)
	}
	epairB := fmt.Sprintf("%s_net%db", fixture.service.GetCTIDHash(fixture.jail.CTID), network.ID)
	for _, command := range []string{"echo user-post-start", "ifconfig -j " + fixture.service.GetCTIDHash(fixture.jail.CTID) + " " + epairB + " inet 192.0.2.25/24"} {
		if got := strings.Count(string(updatedPostStart), command); got != 1 {
			t.Fatalf("Linux post-start command %q count = %d, want 1:\n%s", command, got, updatedPostStart)
		}
	}
}

func TestJailNetworkLabelNeutralizesShellAndMarkerCharacters(t *testing.T) {
	for name, want := range map[string]string{
		"vlan10":                             "vlan10",
		"  LAN 1  ":                          "LAN 1",
		"réseau-é":                           "réseau-é",
		"a'b\"c$(touch /tmp/x)`id`":          "a_b_c__touch /tmp/x__id_",
		"x\n# <<< Sylve-Managed Network <<<": "x__ ___ Sylve-Managed Network ___",
	} {
		if got := jailNetworkLabel(name); got != want {
			t.Errorf("jailNetworkLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSyncNetworkLabelsJailInterfacesWithNetworkNames(t *testing.T) {
	const hostileName = "x'; touch /tmp/pwned; '\n### End Sylve-Managed Network ###\n" + jailNetworkRCConfEnd

	for _, test := range []struct {
		jailType jailModels.JailType
		ctID     uint
	}{
		{jailModels.JailTypeFreeBSD, 7208},
		{jailModels.JailTypeLinux, 7209},
	} {
		t.Run(string(test.jailType), func(t *testing.T) {
			fixture := newJailNetworkSyncFixture(t, test.jailType, test.ctID)
			firstMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:10")
			secondMAC := fixture.addObject(t, "Mac", "02:00:00:00:72:11")
			freeBSD := test.jailType == jailModels.JailTypeFreeBSD
			named := fixture.addNetwork(t, jailModels.Network{Name: "vlan10", MacID: &firstMAC, SLAAC: freeBSD})
			hostile := fixture.addNetwork(t, jailModels.Network{Name: hostileName, MacID: &secondMAC})

			if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
				t.Fatalf("sync labelled networks: %v", err)
			}
			preStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "pre-start")
			if err != nil {
				t.Fatalf("get pre-start hook: %v", err)
			}
			preStart, err := os.ReadFile(preStartPath)
			if err != nil {
				t.Fatalf("read pre-start hook: %v", err)
			}
			rcConfPath := filepath.Join(fixture.mountPoint, "etc", "rc.conf")
			rcConf, rcErr := os.ReadFile(rcConfPath)

			hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
			namedEpairB := fmt.Sprintf("%s_net%db", hash, named.ID)
			hostileEpairB := fmt.Sprintf("%s_net%db", hash, hostile.ID)
			hostileLabel := jailNetworkLabel(hostileName)
			for _, line := range []string{
				fmt.Sprintf("ifconfig %s descr 'vlan10'\n", namedEpairB),
				fmt.Sprintf("ifconfig %s descr '%s'\n", hostileEpairB, hostileLabel),
			} {
				if got := strings.Count(string(preStart), line); got != 1 {
					t.Fatalf("pre-start line %q count = %d, want 1:\n%s", line, got, preStart)
				}
			}
			if got := strings.Count(string(preStart), "### End Sylve-Managed Network ###"); got != 1 {
				t.Fatalf("network name forged a hook marker (count %d):\n%s", got, preStart)
			}
			for _, line := range strings.Split(string(preStart), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "touch") {
					t.Fatalf("network name injected a command into pre-start:\n%s", preStart)
				}
			}

			if !freeBSD {
				if !os.IsNotExist(rcErr) {
					t.Fatalf("Linux network sync unexpectedly created rc.conf: %v", rcErr)
				}
				return
			}
			if rcErr != nil {
				t.Fatalf("read rc.conf: %v", rcErr)
			}
			switchName := fmt.Sprintf("sync-switch-%d", fixture.jail.CTID)
			namedComment := fmt.Sprintf(`# %s: Sylve network "vlan10" on switch "%s"`, namedEpairB, switchName)
			hostileComment := fmt.Sprintf(`# %s: Sylve network "%s" on switch "%s"`, hostileEpairB, hostileLabel, switchName)
			for _, line := range []string{namedComment, hostileComment} {
				if got := strings.Count(string(rcConf), line+"\n"); got != 1 {
					t.Fatalf("rc.conf comment %q count = %d, want 1:\n%s", line, got, rcConf)
				}
			}
			slaacLine := fmt.Sprintf(`ifconfig_%s_ipv6="inet6 accept_rtadv"`, namedEpairB)
			commentIndex := strings.Index(string(rcConf), namedComment)
			if slaacIndex := strings.Index(string(rcConf), slaacLine); slaacIndex < 0 || commentIndex > slaacIndex {
				t.Fatalf("expected %q to precede %q:\n%s", namedComment, slaacLine, rcConf)
			}
			if got := strings.Count(string(rcConf), jailNetworkRCConfEnd); got != 1 {
				t.Fatalf("network name forged an rc.conf marker (count %d):\n%s", got, rcConf)
			}

			if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
				t.Fatalf("repeat labelled network sync: %v", err)
			}
			repeatedRCConf, err := os.ReadFile(rcConfPath)
			if err != nil {
				t.Fatalf("reread rc.conf: %v", err)
			}
			if string(repeatedRCConf) != string(rcConf) {
				t.Fatalf("repeating sync changed rc.conf:\nfirst:\n%s\nsecond:\n%s", rcConf, repeatedRCConf)
			}
		})
	}
}

func TestJailNetworkHookCleanupPreservesNonNetworkLines(t *testing.T) {
	svc := &Service{}
	original := strings.Join([]string{
		"#!/bin/sh",
		"rctl -a jail:test:memoryuse:deny=1G",
		"### Start Sylve-Managed Network ###",
		"ifconfig epair0a up",
		"### End Sylve-Managed Network ###",
		"cpuset -l 0 -j test",
		"",
	}, "\n")
	cleaned := svc.RemoveSylveNetworkFromHook(original)
	if strings.Contains(cleaned, "ifconfig epair0a up") {
		t.Fatalf("managed network command remained in %q", cleaned)
	}
	for _, expected := range []string{"rctl -a jail:test:memoryuse:deny=1G", "cpuset -l 0 -j test"} {
		if !strings.Contains(cleaned, expected) {
			t.Fatalf("expected non-network hook line %q to remain in %q", expected, cleaned)
		}
	}
}

func TestNormalizeJailNetworkValueRejectsWrongFamiliesAndNonEthernetMAC(t *testing.T) {
	for _, test := range []struct {
		role  jailNetworkObjectRole
		value string
	}{
		{role: jailNetworkIPv4GW, value: "2001:db8::1"},
		{role: jailNetworkIPv6GW, value: "192.0.2.1"},
		{role: jailNetworkMAC, value: "01:02:03:04:05:06:07:08"},
	} {
		if _, err := normalizeJailNetworkValue(test.role, test.value); err == nil {
			t.Fatalf("expected %s value %q to be rejected", test.role, test.value)
		}
	}
}
