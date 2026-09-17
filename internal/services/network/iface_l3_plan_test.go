// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"net"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

func mustParseIP(t *testing.T, value string) net.IP {
	t.Helper()

	ip := net.ParseIP(value)
	if ip == nil {
		t.Fatalf("failed to parse IP %q", value)
	}
	return ip
}

func stubHostInterfaceL3Get(t *testing.T, interfaces map[string]*iface.Interface) {
	t.Helper()

	originalGet := syncIfaceGet
	originalRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = originalGet
		syncRunCommand = originalRun
	})

	syncIfaceGet = func(name string) (*iface.Interface, error) {
		obj, ok := interfaces[name]
		if !ok {
			return nil, errInterfaceNotFound(name)
		}
		if obj != nil && obj.Driver == "" && strings.HasPrefix(obj.Name, "em") {
			obj.Driver = "em"
		}
		return obj, nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}
}

func errInterfaceNotFound(name string) error {
	return &interfaceMissingError{name: name}
}

type interfaceMissingError struct {
	name string
}

func (e *interfaceMissingError) Error() string {
	return "interface " + e.name + " does not exist"
}

func requestWithAddresses(addresses ...string) networkServiceInterfaces.HostInterfaceL3UpdateRequest {
	inputs := make([]networkServiceInterfaces.HostInterfaceL3AddressInput, 0, len(addresses))
	for _, address := range addresses {
		inputs = append(inputs, networkServiceInterfaces.HostInterfaceL3AddressInput{Address: address})
	}
	return networkServiceInterfaces.HostInterfaceL3UpdateRequest{Addresses: inputs}
}

func TestPlanHostInterfaceL3FirstAddressUsesConfiguredPrefix(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
	})

	change, err := svc.planHostInterfaceL3Change("em0", nil, requestWithAddresses("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Addresses) != 1 {
		t.Fatalf("expected one planned address, got %+v", change.Plan.Addresses)
	}
	if change.Plan.Addresses[0].PrefixLength != 24 || change.Plan.Addresses[0].Alias {
		t.Fatalf("expected a plain /24 first address, got %+v", change.Plan.Addresses[0])
	}
	if len(change.Intended.Addresses) != 1 || change.Intended.Addresses[0].Address != "10.0.0.5/24" {
		t.Fatalf("unexpected intended applied state %+v", change.Intended.Addresses)
	}
}

func TestPlanHostInterfaceL3SecondAddressInPrefixBecomesAlias(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
	})

	change, err := svc.planHostInterfaceL3Change("em0", nil, requestWithAddresses("10.0.0.5/24", "10.0.0.6/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Addresses) != 2 {
		t.Fatalf("expected two planned addresses, got %+v", change.Plan.Addresses)
	}
	if change.Plan.Addresses[0].PrefixLength != 24 || change.Plan.Addresses[0].Alias {
		t.Fatalf("expected the first address to own the prefix, got %+v", change.Plan.Addresses[0])
	}
	if change.Plan.Addresses[1].PrefixLength != 32 || !change.Plan.Addresses[1].Alias {
		t.Fatalf("expected the second address to be a /32 alias, got %+v", change.Plan.Addresses[1])
	}
}

func TestPlanHostInterfaceL3ForeignPrefixOwnerForcesAlias(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {
			Name:  "em0",
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv4: []iface.IPv4{{
				IP:      mustParseIP(t, "10.0.0.1"),
				Netmask: "255.255.255.0",
			}},
		},
	})

	change, err := svc.planHostInterfaceL3Change("em0", nil, requestWithAddresses("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Addresses) != 1 {
		t.Fatalf("expected one planned address, got %+v", change.Plan.Addresses)
	}
	if change.Plan.Addresses[0].PrefixLength != 32 || !change.Plan.Addresses[0].Alias {
		t.Fatalf("expected a /32 alias beside a foreign owner, got %+v", change.Plan.Addresses[0])
	}
}

func TestPlanHostInterfaceL3RemovesStaleManagedAddress(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {
			Name:  "em0",
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv4: []iface.IPv4{{
				IP:      mustParseIP(t, "10.0.0.5"),
				Netmask: "255.255.255.0",
			}},
		},
	})

	current := &networkModels.HostInterfaceL3{
		Interface: "em0",
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
				Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24,
			}},
		},
	}

	change, err := svc.planHostInterfaceL3Change("em0", current, requestWithAddresses())
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Remove) != 1 || change.Plan.Remove[0].Address != "10.0.0.5/24" {
		t.Fatalf("expected the stale address to be removed, got %+v", change.Plan.Remove)
	}
	if len(change.Plan.Addresses) != 0 || len(change.Intended.Addresses) != 0 {
		t.Fatalf("expected no additions, got %+v", change.Plan.Addresses)
	}
}

func TestPlanHostInterfaceL3PrefixChangeRemovesThenAdds(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {
			Name:  "em0",
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv4: []iface.IPv4{{
				IP:      mustParseIP(t, "10.0.0.5"),
				Netmask: "255.255.255.255",
			}},
		},
	})

	current := &networkModels.HostInterfaceL3{
		Interface: "em0",
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
				Family: "inet", Address: "10.0.0.5/32", PrefixLength: 32,
			}},
		},
	}

	change, err := svc.planHostInterfaceL3Change("em0", current, requestWithAddresses("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Remove) != 1 {
		t.Fatalf("expected the /32 to be removed, got %+v", change.Plan.Remove)
	}
	if len(change.Plan.Addresses) != 1 || change.Plan.Addresses[0].PrefixLength != 24 {
		t.Fatalf("expected a /24 re-add, got %+v", change.Plan.Addresses)
	}
}

func TestPlanHostInterfaceL3ReAddsMissingManagedAddressWithoutDuplicating(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
	})

	current := &networkModels.HostInterfaceL3{
		Interface: "em0",
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
				Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24,
			}},
		},
	}

	change, err := svc.planHostInterfaceL3Change("em0", current, requestWithAddresses("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Addresses) != 1 {
		t.Fatalf("expected the missing managed address to be re-added, got %+v", change.Plan.Addresses)
	}
	if len(change.Intended.Addresses) != 1 {
		t.Fatalf("expected exactly one intended address, got %+v", change.Intended.Addresses)
	}
}

func TestPlanHostInterfaceL3KeepsPolicyAppliedAliasesOnReapply(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {
			Name:  "em0",
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv4: []iface.IPv4{
				{IP: mustParseIP(t, "10.0.0.5"), Netmask: "255.255.255.0"},
				{IP: mustParseIP(t, "10.0.0.6"), Netmask: "255.255.255.255"},
			},
		},
	})

	current := &networkModels.HostInterfaceL3{
		Interface: "em0",
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{
				{Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24},
				{Family: "inet", Address: "10.0.0.6/32", PrefixLength: 32, Alias: true},
			},
		},
	}

	change, err := svc.planHostInterfaceL3Change("em0", current, requestWithAddresses("10.0.0.5/24", "10.0.0.6/24"))
	if err != nil {
		t.Fatalf("planHostInterfaceL3Change: %v", err)
	}
	if len(change.Plan.Remove) != 0 {
		t.Fatalf("expected no removals for policy-applied aliases, got %+v", change.Plan.Remove)
	}
	if len(change.Plan.Addresses) != 0 {
		t.Fatalf("expected no re-additions, got %+v", change.Plan.Addresses)
	}
	if len(change.Intended.Addresses) != 2 {
		t.Fatalf("expected both addresses to stay owned, got %+v", change.Intended.Addresses)
	}
	if change.Intended.Addresses[1].Address != "10.0.0.6/32" {
		t.Fatalf("expected the /32 alias to keep its applied mask, got %+v", change.Intended.Addresses[1])
	}
}

func TestPlanHostInterfaceL3RejectsIdentityMismatch(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {Name: "em0", Ether: "bb:bb:bb:bb:bb:bb", MTU: 1500},
	})

	current := &networkModels.HostInterfaceL3{
		Interface:   "em0",
		IdentityMAC: "aa:aa:aa:aa:aa:aa",
	}

	_, err := svc.planHostInterfaceL3Change("em0", current, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_identity_mismatch" {
		t.Fatalf("expected identity mismatch refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3RejectsInterfaceWithoutDriver(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"virt0": {Name: "virt0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
	})

	_, err := svc.planHostInterfaceL3Change("virt0", nil, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_ineligible_no_driver" {
		t.Fatalf("expected the no-driver allowlist refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3RejectsMemberOfUnregisteredBridge(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{
		{Name: "bridge0", Groups: []string{"bridge"}, BridgeMembers: []iface.BridgeMember{{Name: "em5"}}},
	})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em5":     {Name: "em5", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
		"bridge0": {Name: "bridge0", Groups: []string{"bridge"}, BridgeMembers: []iface.BridgeMember{{Name: "em5"}}},
	})

	_, err := svc.planHostInterfaceL3Change("em5", nil, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_bridge_member" {
		t.Fatalf("expected the unregistered bridge membership refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3EnforcesIPv6FloorWithLiveIPv6(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {
			Name:  "em0",
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv6: []iface.IPv6{{
				IP:           mustParseIP(t, "2001:db8::5"),
				PrefixLength: 64,
				AutoConf:     true,
			}},
		},
	})

	mtu := uint(900)
	req := requestWithAddresses("10.0.0.5/24")
	req.MTU = &mtu

	_, err := svc.planHostInterfaceL3Change("em0", nil, req)
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_mtu_below_ipv6_floor" {
		t.Fatalf("expected the live IPv6 floor refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3RejectsIneligibleAndBusyInterfaces(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"bridge0": {Name: "bridge0", Ether: "aa:bb:cc:dd:ee:ff", Groups: []string{"bridge"}, MTU: 1500},
		"em4": {
			Name:          "em4",
			Ether:         "aa:bb:cc:dd:ee:ff",
			MTU:           1500,
			BridgeMembers: nil,
		},
		"vm-sw1": {Name: "vm-sw1", Groups: []string{"bridge"}, BridgeMembers: []iface.BridgeMember{{Name: "em4"}}},
	})

	if err := db.Create(&networkModels.StandardSwitch{Name: "sw1", BridgeName: "vm-sw1"}).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}

	_, err := svc.planHostInterfaceL3Change("bridge0", nil, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_ineligible_bridge" {
		t.Fatalf("expected bridge ineligibility, got %v", err)
	}

	_, err = svc.planHostInterfaceL3Change("em4", nil, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_bridge_member" {
		t.Fatalf("expected bridge membership refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3RejectsChildAboveParentMTU(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0":   {Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
		"em0.5": {Name: "em0.5", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500, VLANParent: "em0", VLANTag: 5},
	})

	mtu := uint(9000)
	req := requestWithAddresses("10.0.0.5/24")
	req.MTU = &mtu

	_, err := svc.planHostInterfaceL3Change("em0.5", nil, req)
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_child_mtu_above_parent" {
		t.Fatalf("expected parent MTU refusal, got %v", err)
	}
}

func TestPlanHostInterfaceL3RejectsDuplicateAcrossRows(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	stubHostInterfaceL3Get(t, map[string]*iface.Interface{
		"em0": {Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
		"em1": {Name: "em1", Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500},
	})

	other := networkModels.HostInterfaceL3{Interface: "em1"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("seed other row: %v", err)
	}
	if err := db.Create(&networkModels.HostInterfaceL3Address{
		InterfaceL3ID: other.ID, Family: "inet", Address: "10.0.0.5", PrefixLength: 24,
	}).Error; err != nil {
		t.Fatalf("seed other address: %v", err)
	}

	_, err := svc.planHostInterfaceL3Change("em0", nil, requestWithAddresses("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_duplicate_address" {
		t.Fatalf("expected duplicate address refusal, got %v", err)
	}
}
