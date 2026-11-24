// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"net"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

func claimHostInterfaceForJail(t *testing.T, svc *Service, name string) {
	t.Helper()
	if err := svc.DB.Create(&jailModels.Network{
		JailID: 1, Name: "wan", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: name,
	}).Error; err != nil {
		t.Fatalf("seed jail host interface claim: %v", err)
	}
}

func TestJailHostInterfaceTargetsEligibilityRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&networkModels.StaticRoute{},
	)
	if err := db.Create(&networkModels.NetworkPort{Name: "em2", SwitchID: 1}).Error; err != nil {
		t.Fatalf("seed switch port: %v", err)
	}
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em5"}).Error; err != nil {
		t.Fatalf("seed host IP row: %v", err)
	}
	if err := db.Create(&networkModels.StaticRoute{
		Name: "via-em6", Enabled: true, DestinationType: "network", Destination: "198.51.100.0/24",
		Family: "inet", NextHopMode: "interface", Interface: "em6",
	}).Error; err != nil {
		t.Fatalf("seed static route: %v", err)
	}
	originalConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{WANInterfaces: []string{"em7"}}
	t.Cleanup(func() { config.ParsedConfig = originalConfig })

	ether := "02:00:00:00:00:01"
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{
		{Name: "ixl0", Ether: ether},
		{Name: "ixl1", Ether: ether},
		{Name: "ixl1.20", Ether: ether, Groups: []string{"vlan"}, VLANParent: "ixl1", VLANTag: 20},
		{Name: "ixl2", LaggPort: true},
		{Name: "lagg0", Ether: ether, Groups: []string{"lagg"}},
		{Name: "bridge0", Ether: ether, Groups: []string{"bridge"}, BridgeMembers: []iface.BridgeMember{{Name: "em1"}}},
		{Name: "em1", Ether: ether},
		{Name: "em2", Ether: ether},
		{Name: "em3", Ether: ether, IPv4: []iface.IPv4{{IP: net.ParseIP("192.0.2.5")}}},
		{Name: "em4", Ether: ether, IPv6: []iface.IPv6{{IP: net.ParseIP("fe80::1")}}},
		{Name: "em5", Ether: ether},
		{Name: "em6", Ether: ether},
		{Name: "em7", Ether: ether},
		{Name: "em8", Ether: ether, IPv6: []iface.IPv6{{IP: net.ParseIP("2001:db8::8")}}},
		{Name: "aaaep_net1a", Ether: ether, Groups: []string{"epair", "sylve"}},
		{Name: "lo0", Groups: []string{"lo"}},
	})

	targets, err := svc.JailHostInterfaceTargets()
	if err != nil {
		t.Fatalf("JailHostInterfaceTargets: %v", err)
	}
	reasons := make(map[string]string, len(targets))
	for _, target := range targets {
		if target.Eligible != (target.Reason == "") {
			t.Fatalf("target %+v has an inconsistent eligibility flag", target)
		}
		reasons[target.Interface] = target.Reason
	}
	for name, want := range map[string]string{
		"ixl0":        "",
		"ixl1":        "host_interface_vlan_parent",
		"ixl1.20":     "",
		"ixl2":        "host_interface_lagg_member",
		"lagg0":       "",
		"bridge0":     "host_interface_unsupported_type",
		"em1":         "host_interface_bridge_member",
		"em2":         "host_interface_switch_port",
		"em3":         "host_interface_has_addresses",
		"em4":         "",
		"em5":         "host_interface_host_ip_configured",
		"em6":         "host_interface_in_use_by_static_route",
		"em7":         "host_interface_in_use_by_container_nat",
		"em8":         "host_interface_has_addresses",
		"aaaep_net1a": "host_interface_unsupported_type",
		"lo0":         "host_interface_unsupported_type",
	} {
		got, ok := reasons[name]
		if !ok {
			t.Errorf("%s: missing from targets", name)
			continue
		}
		if got != want {
			t.Errorf("%s: reason = %q, want %q", name, got, want)
		}
	}
}

func TestJailClaimedInterfaceCannotBecomeSwitchPortOrHostIP(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.DHCPRange{},
	)
	claimHostInterfaceForJail(t, svc, "ixl0")
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{
		{Name: "ixl0", Ether: "02:00:00:00:00:01", Driver: "ixl"},
		{Name: "ixl1", Ether: "02:00:00:00:00:02", Driver: "ixl"},
	})

	_, err := svc.validateStandardSwitchInput(0, "vm-claimed", standardSwitchInput{
		mtu:         1500,
		dhcp:        true,
		disableIPv6: true,
		ports:       []string{"ixl0"},
		macSource:   createTestStandardSwitchMACSource(t, svc),
	})
	if StandardSwitchErrorCode(err) != "standard_switch_port_used_by_jail" {
		t.Fatalf("switch port error = %v, code = %q", err, StandardSwitchErrorCode(err))
	}

	err = svc.hostInterfaceL3MembershipGuardWithInterfaces("ixl0", nil)
	if err == nil || !errors.Is(err, ErrHostInterfaceL3Conflict) {
		t.Fatalf("host IP guard error = %v", err)
	}

	list, err := svc.GetHostInterfaceL3()
	if err != nil {
		t.Fatalf("GetHostInterfaceL3: %v", err)
	}
	for _, target := range list.Targets {
		switch target.Interface {
		case "ixl0":
			if target.Eligible || target.Reason != "host_interface_l3_used_by_jail" {
				t.Fatalf("claimed interface Host IP target = %+v", target)
			}
		case "ixl1":
			if !target.Eligible {
				t.Fatalf("unclaimed interface Host IP target = %+v", target)
			}
		}
	}
}
