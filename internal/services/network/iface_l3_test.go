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
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

func stubHostInterfaceL3Interfaces(t *testing.T, interfaces []*iface.Interface) {
	t.Helper()

	original := hostInterfaceL3ListInterfaces
	t.Cleanup(func() {
		hostInterfaceL3ListInterfaces = original
	})
	hostInterfaceL3ListInterfaces = func() ([]*iface.Interface, error) {
		return interfaces, nil
	}
}

func hostInterfaceL3TestDB(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	return newNetworkServiceForTest(t,
		&networkModels.HostInterfaceL3{},
		&networkModels.HostInterfaceL3Address{},
		&networkModels.PendingApply{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.DHCPRange{},
	)
}

func containsConflict(entry networkServiceInterfaces.HostInterfaceL3Entry, code string) bool {
	for _, conflict := range entry.Conflicts {
		if conflict == code {
			return true
		}
	}
	return false
}

func entryByInterface(
	t *testing.T,
	entries []networkServiceInterfaces.HostInterfaceL3Entry,
	name string,
) networkServiceInterfaces.HostInterfaceL3Entry {
	t.Helper()

	for _, entry := range entries {
		if entry.Interface == name {
			return entry
		}
	}
	t.Fatalf("expected an entry for interface %q", name)
	return networkServiceInterfaces.HostInterfaceL3Entry{}
}

func TestGetHostInterfaceL3ReturnsEmptyList(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})

	list, err := svc.GetHostInterfaceL3()
	if err != nil {
		t.Fatalf("GetHostInterfaceL3: %v", err)
	}
	if len(list.Rows) != 0 || len(list.Targets) != 0 {
		t.Fatalf("expected an empty list, got %d rows and %d targets", len(list.Rows), len(list.Targets))
	}
}

func TestGetHostInterfaceL3ReportsVLANDriftManagedAddressesAndPendingReservations(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	row := networkModels.HostInterfaceL3{
		Interface:  "em0.100",
		VLANParent: "em0",
		VLANTag:    100,
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
				Family: "inet", Address: "10.0.0.5/24",
			}},
		},
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed Host IP row: %v", err)
	}
	pending := networkModels.PendingApply{
		ID:        "pending-parent",
		Interface: "em1.200",
		Kind:      networkModels.PendingApplyKindInterface,
		Phase:     networkModels.PendingApplyPhaseApplied,
		RuntimeSnapshot: networkModels.HostInterfaceL3Baseline{
			VLANParent: "em1",
		},
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("seed pending operation: %v", err)
	}
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{
		{Name: "em0", Ether: "aa:aa:aa:aa:aa:aa", Driver: "em"},
		{Name: "em0.100", Ether: "aa:aa:aa:aa:aa:aa", VLANParent: "em0", VLANTag: 200},
		{Name: "em1", Ether: "bb:bb:bb:bb:bb:bb", Driver: "em"},
		{Name: "em1.200", Ether: "bb:bb:bb:bb:bb:bb", VLANParent: "em1", VLANTag: 200},
	})

	list, err := svc.GetHostInterfaceL3()
	if err != nil {
		t.Fatalf("GetHostInterfaceL3: %v", err)
	}
	entry := entryByInterface(t, list.Rows, "em0.100")
	if !containsConflict(entry, networkServiceInterfaces.HostInterfaceL3ConflictVLANIdentity) {
		t.Fatalf("expected VLAN identity conflict, got %v", entry.Conflicts)
	}
	if len(entry.ManagedAddresses) != 1 || entry.ManagedAddresses[0].Address != "10.0.0.5/24" {
		t.Fatalf("expected managed address state, got %+v", entry.ManagedAddresses)
	}
	for _, name := range []string{"em1", "em1.200"} {
		found := false
		for _, target := range list.Targets {
			if target.Interface == name {
				found = true
				if target.Reason != networkServiceInterfaces.HostInterfaceL3ConflictPending {
					t.Fatalf("expected %s to be pending-reserved, got %+v", name, target)
				}
			}
		}
		if !found {
			t.Fatalf("expected target %s", name)
		}
	}
}

func TestGetHostInterfaceL3AnnotatesLiveStateAndConflicts(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)

	rows := []networkModels.HostInterfaceL3{
		{Interface: "em0"},
		{Interface: "em1"},
		{Interface: "em2", IdentityMAC: "aa:aa:aa:aa:aa:aa"},
		{Interface: "em3"},
		{Interface: "em4"},
		{Interface: "em5", VLANParent: "em0", VLANTag: 100},
		{Interface: "em6", VLANParent: "em7", VLANTag: 200},
		{Interface: "em9"},
		{Interface: "em8", IdentityMAC: "ab:ab:ab:ab:ab:ab"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed Host Interface L3 rows: %v", err)
	}

	addresses := []networkModels.HostInterfaceL3Address{
		{InterfaceL3ID: rows[7].ID, Family: "inet", Address: "10.0.1.5", PrefixLength: 32, Ordering: 2},
		{InterfaceL3ID: rows[7].ID, Family: "inet", Address: "10.0.0.5", PrefixLength: 24, Ordering: 1},
	}
	if err := db.Create(&addresses).Error; err != nil {
		t.Fatalf("seed Host Interface L3 addresses: %v", err)
	}

	standardSwitch := networkModels.StandardSwitch{Name: "sw1", BridgeName: "vm-sw1"}
	if err := db.Create(&standardSwitch).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em3", SwitchID: standardSwitch.ID}).Error; err != nil {
		t.Fatalf("seed network port: %v", err)
	}

	stubHostInterfaceL3Interfaces(t, []*iface.Interface{
		{Name: "em0", Ether: "00:00:00:00:00:00"},
		{Name: "em2", Ether: "bb:bb:bb:bb:bb:bb"},
		{Name: "em3", Ether: "cc:cc:cc:cc:cc:cc"},
		{Name: "em4", Ether: "dd:dd:dd:dd:dd:dd"},
		{Name: "em5", Ether: "ee:ee:ee:ee:ee:ee", VLANParent: "em0", VLANTag: 100},
		{Name: "em9", Ether: "ff:ff:ff:ff:ff:ff"},
		{Name: "em8"},
		{Name: "vm-sw1", Groups: []string{"bridge"}, BridgeMembers: []iface.BridgeMember{{Name: "em4"}}},
	})

	result, err := svc.GetHostInterfaceL3()
	if err != nil {
		t.Fatalf("GetHostInterfaceL3: %v", err)
	}
	entries := result.Rows
	if len(entries) != len(rows) {
		t.Fatalf("expected %d entries, got %d", len(rows), len(entries))
	}

	clean := entryByInterface(t, entries, "em9")
	if !clean.Present || len(clean.Conflicts) != 0 {
		t.Fatalf("expected em9 present without conflicts, got present=%v conflicts=%v", clean.Present, clean.Conflicts)
	}
	if len(clean.Addresses) != 2 || clean.Addresses[0].Address != "10.0.0.5" {
		t.Fatalf("expected ordered addresses on em9, got %+v", clean.Addresses)
	}

	missing := entryByInterface(t, entries, "em1")
	if missing.Present || !containsConflict(missing, networkServiceInterfaces.HostInterfaceL3ConflictMissingInterface) {
		t.Fatalf("expected em1 missing conflict, got present=%v conflicts=%v", missing.Present, missing.Conflicts)
	}

	mismatch := entryByInterface(t, entries, "em2")
	if !mismatch.Present || mismatch.LiveMAC != "bb:bb:bb:bb:bb:bb" ||
		!containsConflict(mismatch, networkServiceInterfaces.HostInterfaceL3ConflictIdentityMismatch) {
		t.Fatalf("expected em2 identity mismatch, got %+v", mismatch)
	}

	missingMAC := entryByInterface(t, entries, "em8")
	if !missingMAC.Present ||
		!containsConflict(missingMAC, networkServiceInterfaces.HostInterfaceL3ConflictIdentityMismatch) {
		t.Fatalf("expected em8 identity mismatch without a live MAC, got %+v", missingMAC)
	}

	port := entryByInterface(t, entries, "em3")
	if !containsConflict(port, networkServiceInterfaces.HostInterfaceL3ConflictStandardSwitchPort) {
		t.Fatalf("expected em3 standard switch port conflict, got %v", port.Conflicts)
	}

	member := entryByInterface(t, entries, "em4")
	if !containsConflict(member, networkServiceInterfaces.HostInterfaceL3ConflictBridgeMember) {
		t.Fatalf("expected em4 bridge member conflict, got %v", member.Conflicts)
	}

	parent := entryByInterface(t, entries, "em0")
	if !containsConflict(parent, networkServiceInterfaces.HostInterfaceL3ConflictParentHasChildren) {
		t.Fatalf("expected em0 parent-with-children conflict, got %v", parent.Conflicts)
	}

	child := entryByInterface(t, entries, "em5")
	if !child.Present || !containsConflict(child, networkServiceInterfaces.HostInterfaceL3ConflictParentHasHostIP) {
		t.Fatalf("expected em5 parent-has-host-ip conflict, got present=%v conflicts=%v", child.Present, child.Conflicts)
	}

	orphanChild := entryByInterface(t, entries, "em6")
	if !containsConflict(orphanChild, networkServiceInterfaces.HostInterfaceL3ConflictVLANParentMissing) {
		t.Fatalf("expected em6 missing-parent conflict, got %v", orphanChild.Conflicts)
	}
}

func TestGetHostInterfaceL3PropagatesInventoryFailure(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em0"}).Error; err != nil {
		t.Fatalf("seed Host Interface L3 row: %v", err)
	}

	original := hostInterfaceL3ListInterfaces
	t.Cleanup(func() {
		hostInterfaceL3ListInterfaces = original
	})
	hostInterfaceL3ListInterfaces = func() ([]*iface.Interface, error) {
		return nil, errors.New("iface list failed")
	}

	if _, err := svc.GetHostInterfaceL3(); err == nil {
		t.Fatal("expected inventory failure to propagate")
	}
}

func TestGetHostInterfaceL3AllowsSiblingVLANTargets(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	if err := db.Create(&networkModels.HostInterfaceL3{
		Interface: "em0.100", VLANParent: "em0", VLANTag: 100,
	}).Error; err != nil {
		t.Fatalf("seed Host Interface L3 row: %v", err)
	}
	interfaces := []*iface.Interface{
		{Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", Driver: "em"},
		{Name: "em0.100", Ether: "aa:bb:cc:dd:ee:ff", VLANParent: "em0", VLANTag: 100},
		{Name: "em0.200", Ether: "aa:bb:cc:dd:ee:ff", VLANParent: "em0", VLANTag: 200},
	}
	stubHostInterfaceL3Interfaces(t, interfaces)

	list, err := svc.GetHostInterfaceL3()
	if err != nil {
		t.Fatalf("GetHostInterfaceL3: %v", err)
	}
	for _, target := range list.Targets {
		if target.Interface == "em0.200" {
			if !target.Eligible || target.Reason != "" {
				t.Fatalf("expected sibling VLAN target to remain eligible, got %+v", target)
			}
			return
		}
	}
	t.Fatal("expected a target for em0.200")
}
