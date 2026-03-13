// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

type syncStubSet struct {
	ifaceGet     func(string) (*iface.Interface, error)
	createBridge func(networkModels.StandardSwitch) error
	editBridge   func(networkModels.StandardSwitch, networkModels.StandardSwitch) error
	deleteBridge func(networkModels.StandardSwitch) error
	runCommand   func(string, ...string) (string, error)
}

func stubSyncFunctions(t *testing.T, stubs syncStubSet) {
	t.Helper()

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origDelete := syncDeleteBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncDeleteBridge = origDelete
		syncRunCommand = origRun
	})

	if stubs.ifaceGet != nil {
		syncIfaceGet = stubs.ifaceGet
	}
	if stubs.createBridge != nil {
		syncCreateBridge = stubs.createBridge
	}
	if stubs.editBridge != nil {
		syncEditBridge = stubs.editBridge
	}
	if stubs.deleteBridge != nil {
		syncDeleteBridge = stubs.deleteBridge
	}
	if stubs.runCommand != nil {
		syncRunCommand = stubs.runCommand
	}
}

func TestNewStandardSwitchRejectsInvalidMTU(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	err := svc.NewStandardSwitch(
		"switch-invalid-mtu",
		90000,
		0,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		false,
		false,
		false,
		false,
		false,
	)
	if err == nil {
		t.Fatal("expected invalid_mtu error, got nil")
	}
	if err.Error() != "invalid_mtu" {
		t.Fatalf("expected invalid_mtu error, got %q", err.Error())
	}
}

func TestNewStandardSwitchRejectsInvalidVLAN(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	err := svc.NewStandardSwitch(
		"switch-invalid-vlan",
		1500,
		5000,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		false,
		false,
		false,
		false,
		false,
	)
	if err == nil {
		t.Fatal("expected invalid_vlan error, got nil")
	}
	if err.Error() != "invalid_vlan" {
		t.Fatalf("expected invalid_vlan error, got %q", err.Error())
	}
}

func TestNewStandardSwitchRejectsPortOverlapDeterministically(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	existing := networkModels.StandardSwitch{
		Name:       "existing",
		BridgeName: "vm-existing",
		VLAN:       10,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("failed to seed existing switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{
		Name:     "em0",
		SwitchID: existing.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed existing port: %v", err)
	}

	err := svc.NewStandardSwitch(
		"candidate",
		1500,
		10,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		false,
		false,
		false,
		false,
		false,
	)
	if err == nil {
		t.Fatal("expected port_overlap error, got nil")
	}

	want := `port_overlap: em0 (used by switch "existing" vlan=10)`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error to contain %q, got %q", want, err.Error())
	}
}

func TestSyncStandardSwitchesSyncCreatesWhenBridgeMissing(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s1", BridgeName: "vm-s1"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	createCalls := 0
	editCalls := 0

	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return nil, errors.New("interface not found")
	}
	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		createCalls++
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		editCalls++
		return nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("expected create bridge call once, got %d", createCalls)
	}
	if editCalls != 0 {
		t.Fatalf("expected no edit bridge calls, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncReconcilesInPlaceWhenBridgeExists(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s2", BridgeName: "vm-s2"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	createCalls := 0
	editCalls := 0

	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name}, nil
	}
	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		createCalls++
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		editCalls++
		return nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("expected no create bridge calls, got %d", createCalls)
	}
	if editCalls != 1 {
		t.Fatalf("expected edit bridge call once, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncPreservesNonDBMembers(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s3", BridgeName: "vm-s3"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	getCalls := 0
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		getCalls++
		switch getCalls {
		case 1:
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
					{Name: "tap0"},
				},
			}, nil
		default:
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		}
	}

	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		return nil
	}

	var seenAddMember bool
	var seenBringUp bool
	syncRunCommand = func(command string, args ...string) (string, error) {
		full := append([]string{command}, args...)
		if strings.Join(full, " ") == "/sbin/ifconfig vm-s3 addm tap0 up" {
			seenAddMember = true
		}
		if strings.Join(full, " ") == "/sbin/ifconfig tap0 up" {
			seenBringUp = true
		}
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}

	if !seenAddMember {
		t.Fatalf("expected non-db member reattach command, got getCalls=%d", getCalls)
	}
	if !seenBringUp {
		t.Fatal("expected non-db member bring-up command")
	}
}

func TestIsInterfaceMissingError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{err: nil, want: false},
		{err: fmt.Errorf("interface not found"), want: true},
		{err: fmt.Errorf("does not exist"), want: true},
		{err: fmt.Errorf("no such interface"), want: true},
		{err: fmt.Errorf("permission denied"), want: false},
	}

	for _, tt := range tests {
		if got := isInterfaceMissingError(tt.err); got != tt.want {
			t.Fatalf("isInterfaceMissingError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestSyncStandardSwitchesSyncReturnsUnexpectedIfaceError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s4", BridgeName: "vm-s4"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	createCalls := 0
	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return nil, errors.New("permission denied")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: get vm-s4: permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("expected no create calls, got %d", createCalls)
	}
	if editCalls != 0 {
		t.Fatalf("expected no edit calls, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncReturnsCreateError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s5", BridgeName: "vm-s5"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return errors.New("create failed")
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: failed_to_create vm-s5: create failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncReturnsEditError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s6", BridgeName: "vm-s6"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return errors.New("edit failed")
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: failed_to_reconcile vm-s6: edit failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncSkipsReattachWhenAlreadyPresent(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s7", BridgeName: "vm-s7"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	runCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
					{Name: "tap0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			runCalls++
			return "", nil
		},
	})

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if runCalls != 0 {
		t.Fatalf("expected no reattach commands when member already present, got %d", runCalls)
	}
}

func TestSyncStandardSwitchesSyncTreatsVLANSubinterfaceAsDBMember(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s8", BridgeName: "vm-s8", VLAN: 10}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0.10"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0.10"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}

	for _, cmd := range commands {
		if strings.Contains(cmd, "em0.10") {
			t.Fatalf("expected no reattach commands for DB VLAN member, got %q", cmd)
		}
	}

	var sawTapAttach bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-s8 addm tap0 up" {
			sawTapAttach = true
		}
	}
	if !sawTapAttach {
		t.Fatalf("expected non-db tap member to be reattached, commands: %v", commands)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorWhenPostReconcileLookupFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s9", BridgeName: "vm-s9"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return nil, errors.New("lookup failed")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: get vm-s9 after reconcile: lookup failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorOnMemberReattachFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s10", BridgeName: "vm-s10"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			if full == "/sbin/ifconfig vm-s10 addm tap0 up" {
				return "", errors.New("addm failed")
			}
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: add member tap0 to vm-s10: addm failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorOnMemberBringUpFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s11", BridgeName: "vm-s11"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			if full == "/sbin/ifconfig tap0 up" {
				return "", errors.New("up failed")
			}
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: bring up member tap0: up failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesCreateActionCallsCreateBridge(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)

	var got networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		createBridge: func(sw networkModels.StandardSwitch) error {
			got = sw
			return nil
		},
	})

	input := &networkModels.StandardSwitch{Name: "create-test", BridgeName: "vm-create-test"}
	if err := svc.SyncStandardSwitches(input, "create"); err != nil {
		t.Fatalf("expected create sync success, got %v", err)
	}
	if got.BridgeName != "vm-create-test" {
		t.Fatalf("expected create bridge to be called with vm-create-test, got %q", got.BridgeName)
	}
}

func TestSyncStandardSwitchesDeleteActionCallsDeleteBridge(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)

	var got networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		deleteBridge: func(sw networkModels.StandardSwitch) error {
			got = sw
			return nil
		},
	})

	input := &networkModels.StandardSwitch{Name: "delete-test", BridgeName: "vm-delete-test"}
	if err := svc.SyncStandardSwitches(input, "delete"); err != nil {
		t.Fatalf("expected delete sync success, got %v", err)
	}
	if got.BridgeName != "vm-delete-test" {
		t.Fatalf("expected delete bridge to be called with vm-delete-test, got %q", got.BridgeName)
	}
}

func TestSyncStandardSwitchesEditActionSwitchNotFound(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t, &networkModels.StandardSwitch{}, &networkModels.NetworkPort{})

	stubSyncFunctions(t, syncStubSet{
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			t.Fatal("edit bridge should not be called when switch is missing")
			return nil
		},
	})

	input := &networkModels.StandardSwitch{ID: 42, Name: "missing", BridgeName: "vm-missing"}
	err := svc.SyncStandardSwitches(input, "edit")
	if err == nil {
		t.Fatal("expected switch_not_found error, got nil")
	}
	if err.Error() != "switch_not_found" {
		t.Fatalf("expected switch_not_found, got %q", err.Error())
	}
}

func TestSyncStandardSwitchesEditActionLoadsCurrentSwitchAndPorts(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{}, &networkModels.NetworkPort{})

	current := networkModels.StandardSwitch{
		Name:       "current",
		BridgeName: "vm-current",
		MTU:        1500,
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: current.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	previous := networkModels.StandardSwitch{
		ID:         current.ID,
		Name:       current.Name,
		BridgeName: current.BridgeName,
		MTU:        1400,
	}

	var gotOld networkModels.StandardSwitch
	var gotNew networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			gotOld = oldSw
			gotNew = newSw
			return nil
		},
	})

	if err := svc.SyncStandardSwitches(&previous, "edit"); err != nil {
		t.Fatalf("expected edit sync success, got %v", err)
	}
	if gotOld.MTU != 1400 {
		t.Fatalf("expected old switch MTU 1400, got %d", gotOld.MTU)
	}
	if gotNew.MTU != 1500 {
		t.Fatalf("expected new switch MTU 1500 from DB, got %d", gotNew.MTU)
	}
	if len(gotNew.Ports) != 1 || gotNew.Ports[0].Name != "em0" {
		t.Fatalf("expected DB preloaded ports, got %+v", gotNew.Ports)
	}
}
