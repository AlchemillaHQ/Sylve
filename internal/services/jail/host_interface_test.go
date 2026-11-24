// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func (f *jailNetworkSyncFixture) addHostInterfaceNetwork(t *testing.T, network jailModels.Network) jailModels.Network {
	t.Helper()
	network.JailID = f.jail.ID
	network.SwitchType = networkAttachment.SwitchTypeHostInterface
	if err := f.db.Create(&network).Error; err != nil {
		t.Fatalf("create host interface network: %v", err)
	}
	f.jail.Networks = append(f.jail.Networks, network)
	return network
}

func TestSyncNetworkRendersHostInterfaceAttachment(t *testing.T) {
	for _, test := range []struct {
		jailType jailModels.JailType
		ctID     uint
	}{
		{jailModels.JailTypeFreeBSD, 7301},
		{jailModels.JailTypeLinux, 7302},
	} {
		t.Run(string(test.jailType), func(t *testing.T) {
			fixture := newJailNetworkSyncFixture(t, test.jailType, test.ctID)
			macID := fixture.addObject(t, "Mac", "02:00:00:00:73:01")
			lan := fixture.addNetwork(t, jailModels.Network{Name: "lan", MacID: &macID})
			ipv4ID := fixture.addObject(t, "Network", "192.0.2.10/24")
			gatewayID := fixture.addObject(t, "Host", "192.0.2.1")
			fixture.addHostInterfaceNetwork(t, jailModels.Network{
				Name: "wan", HostInterface: "ixl0.10", IPv4ID: &ipv4ID, IPv4GwID: &gatewayID, DefaultGateway: true,
			})

			if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
				t.Fatalf("sync host interface network: %v", err)
			}
			config, err := fixture.service.GetJailConfig(fixture.jail.CTID)
			if err != nil {
				t.Fatalf("read jail config: %v", err)
			}
			hash := fixture.service.GetCTIDHash(fixture.jail.CTID)
			lanEpairB := fmt.Sprintf("%s_net%db", hash, lan.ID)
			for _, line := range []string{
				"vnet;",
				`vnet.interface += "ixl0.10";`,
				fmt.Sprintf(`vnet.interface += "%s";`, lanEpairB),
			} {
				if got := strings.Count(config, line); got != 1 {
					t.Fatalf("jail config line %q count = %d, want 1:\n%s", line, got, config)
				}
			}

			preStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "pre-start")
			if err != nil {
				t.Fatalf("get pre-start hook: %v", err)
			}
			preStart, err := os.ReadFile(preStartPath)
			if err != nil {
				t.Fatalf("read pre-start hook: %v", err)
			}
			if strings.Contains(string(preStart), "ixl0.10") {
				t.Fatalf("pre-start hook touches the host interface:\n%s", preStart)
			}

			if test.jailType == jailModels.JailTypeLinux {
				postStartPath, err := fixture.service.GetHookScriptPath(fixture.jail.CTID, "post-start")
				if err != nil {
					t.Fatalf("get post-start hook: %v", err)
				}
				postStart, err := os.ReadFile(postStartPath)
				if err != nil {
					t.Fatalf("read post-start hook: %v", err)
				}
				for _, line := range []string{
					fmt.Sprintf("ifconfig -j %s ixl0.10 inet 192.0.2.10/24", hash),
					fmt.Sprintf("route -j %s add default 192.0.2.1", hash),
				} {
					if got := strings.Count(string(postStart), line); got != 1 {
						t.Fatalf("post-start line %q count = %d, want 1:\n%s", line, got, postStart)
					}
				}
				return
			}

			rcConf, err := os.ReadFile(filepath.Join(fixture.mountPoint, "etc", "rc.conf"))
			if err != nil {
				t.Fatalf("read rc.conf: %v", err)
			}
			for _, line := range []string{
				`# ixl0.10: Sylve network "wan" (host interface)`,
				`ifconfig_ixl0_10="inet 192.0.2.10 netmask 255.255.255.0"`,
				`defaultrouter="192.0.2.1"`,
			} {
				if got := strings.Count(string(rcConf), line); got != 1 {
					t.Fatalf("rc.conf line %q count = %d, want 1:\n%s", line, got, rcConf)
				}
			}
		})
	}
}

func TestSyncNetworkHostInterfaceOnlyJailIsVNET(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7303)
	fixture.addHostInterfaceNetwork(t, jailModels.Network{Name: "wan", HostInterface: "ixl0", DHCP: true})

	if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
		t.Fatalf("sync host interface only jail: %v", err)
	}
	config, err := fixture.service.GetJailConfig(fixture.jail.CTID)
	if err != nil {
		t.Fatalf("read jail config: %v", err)
	}
	if !strings.Contains(config, "vnet;") || !strings.Contains(config, `vnet.interface += "ixl0";`) {
		t.Fatalf("host interface only jail is not a vnet jail:\n%s", config)
	}
	if strings.Contains(config, "ip4=") || strings.Contains(config, "ip6=") {
		t.Fatalf("vnet jail carries IP restrictions the kernel rejects:\n%s", config)
	}
	rcConf, err := os.ReadFile(filepath.Join(fixture.mountPoint, "etc", "rc.conf"))
	if err != nil {
		t.Fatalf("read rc.conf: %v", err)
	}
	if !strings.Contains(string(rcConf), `ifconfig_ixl0="SYNCDHCP"`) {
		t.Fatalf("missing DHCP line for host interface:\n%s", rcConf)
	}

	names, err := fixture.service.jailEpairNames(fixture.jail)
	if err != nil {
		t.Fatalf("list jail epairs: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("host interface network produced epairs %v", names)
	}
}

func TestSyncNetworkKeepsManualRCConfForUnaddressedHostInterface(t *testing.T) {
	fixture := newJailNetworkSyncFixture(t, jailModels.JailTypeFreeBSD, 7304)
	fixture.addHostInterfaceNetwork(t, jailModels.Network{Name: "wan", HostInterface: "ixl0.10"})
	rcConfPath := filepath.Join(fixture.mountPoint, "etc", "rc.conf")
	manual := []string{
		`ifconfig_ixl0_10="inet 198.51.100.2/24"`,
		`ifconfig_ixl0_10_ipv6="inet6 accept_rtadv"`,
		`defaultrouter="198.51.100.1"`,
		`rtsold_enable="YES"`,
	}
	if err := os.WriteFile(rcConfPath, []byte(`hostname="router"`+"\n"+strings.Join(manual, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("seed rc.conf: %v", err)
	}

	for range 2 {
		if err := fixture.service.SyncNetwork(fixture.jail.CTID, fixture.jail); err != nil {
			t.Fatalf("sync unaddressed host interface: %v", err)
		}
	}
	rcConf, err := os.ReadFile(rcConfPath)
	if err != nil {
		t.Fatalf("read rc.conf: %v", err)
	}
	for _, line := range manual {
		if got := strings.Count(string(rcConf), line); got != 1 {
			t.Fatalf("manual line %q count = %d, want 1:\n%s", line, got, rcConf)
		}
	}
	block := string(rcConf)[strings.Index(string(rcConf), jailNetworkRCConfStart):]
	for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
		if line != jailNetworkRCConfStart && line != jailNetworkRCConfEnd && !strings.HasPrefix(line, "#") {
			t.Fatalf("Sylve wrote %q for an unaddressed host interface:\n%s", line, rcConf)
		}
	}
}

func seedHostInterfaceJail(t *testing.T, svc *Service, ctID uint, name string) jailModels.Jail {
	t.Helper()
	jail := jailModels.Jail{CTID: ctID, Name: name, Type: jailModels.JailTypeFreeBSD}
	if err := svc.DB.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	return jail
}

func seedHostInterfaceClaim(t *testing.T, svc *Service, jail jailModels.Jail, hostInterface string) jailModels.Network {
	t.Helper()
	network := jailModels.Network{
		JailID: jail.ID, Name: "wan-" + hostInterface,
		SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: hostInterface,
	}
	if err := svc.DB.Create(&network).Error; err != nil {
		t.Fatalf("seed host interface claim: %v", err)
	}
	return network
}

func TestAddNetworkHostInterfaceValidation(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	untagged := 10
	for _, test := range []struct {
		name string
		req  jailServiceInterfaces.AddJailNetworkRequest
		want string
	}{
		{"switch and interface", jailServiceInterfaces.AddJailNetworkRequest{SwitchName: "LAN", HostInterface: "ixl0"}, "switch_and_host_interface_are_mutually_exclusive"},
		{"neither", jailServiceInterfaces.AddJailNetworkRequest{}, "switch_name_required"},
		{"mac", jailServiceInterfaces.AddJailNetworkRequest{HostInterface: "ixl0", MACRaw: "02:00:00:00:00:01"}, "host_interface_mac_unsupported"},
		{"invalid name", jailServiceInterfaces.AddJailNetworkRequest{HostInterface: "ixl0;reboot"}, "invalid_host_interface_name"},
		{"missing", jailServiceInterfaces.AddJailNetworkRequest{HostInterface: "ixl9"}, "host_interface_not_found"},
		{"bridge member", jailServiceInterfaces.AddJailNetworkRequest{HostInterface: "em0"}, "host_interface_bridge_member"},
		{"claimed", jailServiceInterfaces.AddJailNetworkRequest{HostInterface: "ixl1"}, "host_interface_used_by_jail"},
		{"vlan policy", jailServiceInterfaces.AddJailNetworkRequest{
			HostInterface: "ixl0",
			VLANPolicy:    &bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &untagged},
		}, "jail_network_host_interface_vlan_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, db, network := newJailNetworkValidationService(t)
			network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{
				{Interface: "em0", Reason: "host_interface_bridge_member"},
				{Interface: "ixl0", Eligible: true},
				{Interface: "ixl1", Eligible: true},
			}
			jail := seedHostInterfaceJail(t, svc, 9301, "host-interface-add")
			other := seedHostInterfaceJail(t, svc, 9302, "host-interface-other")
			seedHostInterfaceClaim(t, svc, other, "ixl1")

			test.req.Name = "wan"
			_, err := svc.AddNetwork(jail.CTID, test.req)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AddNetwork error = %v, want %s", err, test.want)
			}
			var count int64
			if err := db.Model(&jailModels.Network{}).Where("jid = ?", jail.ID).Count(&count).Error; err != nil {
				t.Fatalf("count jail networks: %v", err)
			}
			if count != 0 {
				t.Fatalf("rejected add persisted %d networks", count)
			}
		})
	}
}

func TestHostInterfaceFakeDetectsCallsInsideTransaction(t *testing.T) {
	svc, db, network := newJailNetworkValidationService(t)
	network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{{Interface: "ixl0", Eligible: true}}

	tx := db.Begin()
	defer tx.Rollback()
	if err := tx.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("hold the pooled connection: %v", err)
	}
	if err := svc.ensureHostInterfaceEligible("ixl0"); err == nil {
		t.Fatal("eligibility check inside a transaction should starve the single-connection pool")
	}
}

func TestAddNetworkHostInterfaceSucceedsWithSingleConnectionPool(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	svc, db, network := newJailNetworkValidationService(t)
	network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{{Interface: "ixl0", Eligible: true}}
	jail := seedHostInterfaceJail(t, svc, 9308, "host-interface-pool")

	// The config snapshot step fails without jail files on disk; by then the
	// eligibility check and the transaction have both run.
	_, err := svc.AddNetwork(jail.CTID, jailServiceInterfaces.AddJailNetworkRequest{Name: "wan", HostInterface: "ixl0"})
	if err != nil && strings.Contains(err.Error(), "deadline") {
		t.Fatalf("AddNetwork starved the connection pool: %v", err)
	}
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("database unusable after AddNetwork: %v", err)
	}
}

func TestEditNetworkRejectsHostInterfaceTypeChangesAndMAC(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	svc, _, _ := newJailNetworkValidationService(t)
	jail := seedHostInterfaceJail(t, svc, 9303, "host-interface-edit")
	hostNetwork := seedHostInterfaceClaim(t, svc, jail, "ixl0")
	manual := networkModels.ManualSwitch{Name: "LAN", Bridge: "vm-host-interface-edit"}
	if err := svc.DB.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}
	switchNetwork := jailModels.Network{JailID: jail.ID, Name: "lan", SwitchID: manual.ID, SwitchType: "manual"}
	if err := svc.DB.Create(&switchNetwork).Error; err != nil {
		t.Fatalf("seed switch network: %v", err)
	}

	switchName := "LAN"
	hostInterface := "ixl1"
	mac := "02:00:00:00:00:02"
	for _, test := range []struct {
		name      string
		networkID uint
		req       jailServiceInterfaces.EditJailNetworkRequest
		want      string
	}{
		{"host to switch", hostNetwork.ID, jailServiceInterfaces.EditJailNetworkRequest{SwitchName: &switchName}, "jail_network_attachment_type_change_unsupported"},
		{"switch to host", switchNetwork.ID, jailServiceInterfaces.EditJailNetworkRequest{HostInterface: &hostInterface}, "jail_network_attachment_type_change_unsupported"},
		{"host mac", hostNetwork.ID, jailServiceInterfaces.EditJailNetworkRequest{MACRaw: &mac}, "host_interface_mac_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.EditNetwork(jail.CTID, test.networkID, test.req)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("EditNetwork error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestListHostInterfacesMarksJailClaims(t *testing.T) {
	svc, _, network := newJailNetworkValidationService(t)
	network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{
		{Interface: "em0", Reason: "host_interface_has_addresses"},
		{Interface: "ixl0", Eligible: true},
		{Interface: "ixl2", Eligible: true},
	}
	stopped := seedHostInterfaceJail(t, svc, 9304, "router-stopped")
	running := seedHostInterfaceJail(t, svc, 9305, "router-running")
	seedHostInterfaceClaim(t, svc, stopped, "ixl0")
	seedHostInterfaceClaim(t, svc, running, "ixl1")

	targets, err := svc.ListHostInterfaces()
	if err != nil {
		t.Fatalf("list host interfaces: %v", err)
	}
	want := []jailServiceInterfaces.HostInterfaceTarget{
		{Interface: "em0", Reason: "host_interface_has_addresses"},
		{Interface: "ixl0", Reason: "host_interface_used_by_jail", JailCTID: 9304, JailName: "router-stopped"},
		{Interface: "ixl1", Reason: "host_interface_used_by_jail", JailCTID: 9305, JailName: "router-running"},
		{Interface: "ixl2", Eligible: true},
	}
	if !slices.Equal(targets, want) {
		t.Fatalf("host interfaces = %+v\nwant %+v", targets, want)
	}
}

func TestKeepAvailableHostInterfaceNetworksOnRollback(t *testing.T) {
	svc, _, network := newJailNetworkValidationService(t)
	network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{
		{Interface: "ixl0", Eligible: true},
		{Interface: "ixl1", Eligible: true},
		{Interface: "em0", Reason: "host_interface_switch_port"},
	}
	jail := seedHostInterfaceJail(t, svc, 9306, "router")
	other := seedHostInterfaceJail(t, svc, 9307, "other")
	seedHostInterfaceClaim(t, svc, jail, "ixl0")
	seedHostInterfaceClaim(t, svc, other, "ixl1")

	attachment := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
		SwitchType: networkAttachment.SwitchTypeHostInterface, SwitchName: "ixl0",
	}
	snapshotNetworks := []jailModels.Network{
		{Name: "lan", SwitchType: "manual", SwitchID: 1},
		{Name: "wan", Attachment: &attachment},
		{Name: "dmz", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "ixl1"},
		{Name: "mgmt", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "em0"},
		{Name: "gone", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "ixl9"},
	}

	kept, warnings := svc.keepAvailableHostInterfaceNetworks(jail.ID, snapshotNetworks)
	if len(kept) != 2 || kept[0].Name != "lan" || kept[1].Name != "wan" || kept[1].HostInterface != "ixl0" {
		t.Fatalf("kept networks = %+v", kept)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want one each for ixl1, em0 and ixl9", warnings)
	}
	for index, name := range []string{"ixl1", "em0", "ixl9"} {
		if !strings.Contains(warnings[index], name) {
			t.Fatalf("warning %d = %q, want it to name %s", index, warnings[index], name)
		}
	}
}

func TestEnsureJailHostInterfacesReadyRechecksHost(t *testing.T) {
	svc, _, network := newJailNetworkValidationService(t)
	network.hostInterfaces = []networkServiceInterfaces.JailHostInterfaceTarget{
		{Interface: "ixl0", Reason: "host_interface_bridge_member"},
	}
	jail := jailModels.Jail{Networks: []jailModels.Network{
		{Name: "lan", SwitchType: "manual", SwitchID: 1},
		{Name: "wan", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "ixl0"},
	}}
	err := svc.ensureJailHostInterfacesReady(jail)
	if err == nil || !strings.Contains(err.Error(), "host_interface_bridge_member: ixl0") {
		t.Fatalf("start check error = %v", err)
	}

	network.hostInterfaces[0] = networkServiceInterfaces.JailHostInterfaceTarget{Interface: "ixl0", Eligible: true}
	if err := svc.ensureJailHostInterfacesReady(jail); err != nil {
		t.Fatalf("start check with an eligible interface: %v", err)
	}
}

func TestSplitHostInterfaceNetworksReadsMetadataAttachments(t *testing.T) {
	attachment := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
		SwitchType: networkAttachment.SwitchTypeHostInterface, SwitchName: "ixl0",
	}
	kept, dropped := SplitHostInterfaceNetworks([]jailModels.Network{
		{Name: "lan", SwitchType: "standard", SwitchID: 3},
		{Name: "wan", Attachment: &attachment},
		{Name: "dmz", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "ixl1"},
	})
	if len(kept) != 1 || kept[0].Name != "lan" {
		t.Fatalf("kept = %+v", kept)
	}
	if !slices.Equal(dropped, []string{"ixl0", "ixl1"}) {
		t.Fatalf("dropped = %v", dropped)
	}
}
