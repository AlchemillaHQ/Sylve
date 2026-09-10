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
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	mountPoint := t.TempDir()
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

func TestJailNetworkRCConfReplacementPreservesUnmanagedConfiguration(t *testing.T) {
	original := strings.Join([]string{
		`hostname="jail.example"`,
		`ifconfig_em0="DHCP"`,
		jailNetworkRCConfStart,
		`ifconfig_old="SYNCDHCP"`,
		jailNetworkRCConfEnd,
		`sshd_enable="YES"`,
		"",
	}, "\n")
	updated := replaceJailNetworkRCConfBlock(original, "abcde", []string{`ifconfig_new="SYNCDHCP"`})
	for _, expected := range []string{`hostname="jail.example"`, `ifconfig_em0="DHCP"`, `sshd_enable="YES"`, `ifconfig_new="SYNCDHCP"`} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("expected %q to be preserved in %q", expected, updated)
		}
	}
	if strings.Contains(updated, `ifconfig_old="SYNCDHCP"`) {
		t.Fatalf("old managed block remained in %q", updated)
	}
}

func TestJailNetworkRCConfReplacementRepairsReportedDHCPDuplicate(t *testing.T) {
	dhcpLine := `ifconfig_aaaep_net2b="SYNCDHCP"`
	original := strings.Join([]string{
		dhcpLine,
		jailNetworkRCConfStart,
		dhcpLine,
		jailNetworkRCConfEnd,
		"",
	}, "\n")

	updated := replaceJailNetworkRCConfBlock(original, "aaaep", []string{dhcpLine})
	if got := strings.Count(updated, dhcpLine); got != 1 {
		t.Fatalf("expected one DHCP assignment, got %d:\n%s", got, updated)
	}
	if got := strings.Count(updated, jailNetworkRCConfStart); got != 1 {
		t.Fatalf("expected one managed block, got %d:\n%s", got, updated)
	}
	if second := replaceJailNetworkRCConfBlock(updated, "aaaep", []string{dhcpLine}); second != updated {
		t.Fatalf("expected reconciliation to be idempotent:\nfirst:\n%s\nsecond:\n%s", updated, second)
	}
}

func TestJailNetworkRCConfReplacementKeepsOneLinePerDHCPInterface(t *testing.T) {
	first := `ifconfig_aaaep_net2b="SYNCDHCP"`
	second := `ifconfig_aaaep_net7b="SYNCDHCP"`
	original := strings.Join([]string{
		first,
		jailNetworkRCConfStart,
		first,
		second,
		jailNetworkRCConfEnd,
		"",
	}, "\n")

	updated := replaceJailNetworkRCConfBlock(original, "aaaep", []string{first, second})
	for _, expected := range []string{first, second} {
		if got := strings.Count(updated, expected); got != 1 {
			t.Fatalf("expected DHCP assignment %q once, got %d:\n%s", expected, got, updated)
		}
	}
	if got := strings.Count(updated, jailNetworkRCConfStart); got != 1 {
		t.Fatalf("expected one managed block, got %d:\n%s", got, updated)
	}
}

func TestJailNetworkRCConfReplacementMigratesStaticConfiguration(t *testing.T) {
	oldAddress := `ifconfig_aaaep_net2b="inet 192.0.2.10 netmask 255.255.255.0"`
	oldGateway := `defaultrouter="192.0.2.1"`
	newAddress := `ifconfig_aaaep_net2b="inet 198.51.100.10 netmask 255.255.255.0"`
	newGateway := `defaultrouter="198.51.100.1"`
	original := strings.Join([]string{
		`hostname="jail.example"`,
		oldAddress,
		oldGateway,
		jailNetworkRCConfStart,
		oldAddress,
		oldGateway,
		jailNetworkRCConfEnd,
		`sshd_enable="YES"`,
		"",
	}, "\n")

	updated := replaceJailNetworkRCConfBlock(original, "aaaep", []string{newAddress, newGateway})
	for _, stale := range []string{oldAddress, oldGateway} {
		if strings.Contains(updated, stale) {
			t.Fatalf("stale managed line %q remained:\n%s", stale, updated)
		}
	}
	for _, expected := range []string{`hostname="jail.example"`, `sshd_enable="YES"`, newAddress, newGateway} {
		if got := strings.Count(updated, expected); got != 1 {
			t.Fatalf("expected %q once, got %d:\n%s", expected, got, updated)
		}
	}
}

func TestJailNetworkRCConfReplacementDeduplicatesGlobalSLAACSetting(t *testing.T) {
	first := `ifconfig_aaaep_net2b_ipv6="inet6 accept_rtadv"`
	second := `ifconfig_aaaep_net7b_ipv6="inet6 accept_rtadv"`
	rtsold := `rtsold_enable="YES"`
	updated := replaceJailNetworkRCConfBlock("", "aaaep", []string{first, rtsold, second, rtsold})

	for _, expected := range []string{first, second, rtsold} {
		if got := strings.Count(updated, expected); got != 1 {
			t.Fatalf("expected %q once, got %d:\n%s", expected, got, updated)
		}
	}
}

func TestJailNetworkRCConfReplacementRemovesMarkerlessSylveInterface(t *testing.T) {
	original := strings.Join([]string{
		`ifconfig_aaaep_net2b="SYNCDHCP"`,
		`ifconfig_em0="DHCP"`,
		`sshd_enable="YES"`,
		"",
	}, "\n")
	updated := replaceJailNetworkRCConfBlock(original, "aaaep", nil)

	if strings.Contains(updated, `ifconfig_aaaep_net2b=`) {
		t.Fatalf("markerless Sylve interface assignment remained:\n%s", updated)
	}
	for _, expected := range []string{`ifconfig_em0="DHCP"`, `sshd_enable="YES"`} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("unmanaged line %q was removed:\n%s", expected, updated)
		}
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
