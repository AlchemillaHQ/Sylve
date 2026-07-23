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

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func setEpairTestHooks(t *testing.T, list func() ([]*iface.Interface, error), run func(string, ...string) (string, error)) {
	t.Helper()
	originalList := epairInterfaceList
	originalRun := epairRunCommand
	epairInterfaceList = list
	epairRunCommand = run
	t.Cleanup(func() {
		epairInterfaceList = originalList
		epairRunCommand = originalRun
	})
}

func TestCreateEpairMarksBothInterfacesManaged(t *testing.T) {
	var commands []string
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) { return nil, nil },
		func(command string, args ...string) (string, error) {
			commands = append(commands, command+" "+strings.Join(args, " "))
			if len(args) == 2 && args[0] == "epair" && args[1] == "create" {
				return "epair0a\n", nil
			}
			return "", nil
		},
	)

	if err := (&Service{}).CreateEpair("abcde_net1"); err != nil {
		t.Fatalf("CreateEpair: %v", err)
	}
	want := []string{
		"/sbin/ifconfig epair create",
		"/sbin/ifconfig epair0a name abcde_net1a",
		"/sbin/ifconfig epair0b name abcde_net1b",
		"/sbin/ifconfig abcde_net1a group sylve",
		"/sbin/ifconfig abcde_net1b group sylve",
	}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestDeleteEpairRefusesUnmanagedInterface(t *testing.T) {
	called := false
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{{Name: "aaadx_net4a"}}, nil
		},
		func(string, ...string) (string, error) {
			called = true
			return "", nil
		},
	)

	err := (&Service{}).DeleteEpair("aaadx_net4")
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairOwnershipConflict) || !strings.Contains(err.Error(), "refusing to delete unmanaged epair") {
		t.Fatalf("DeleteEpair error = %v", err)
	}
	if called {
		t.Fatal("DeleteEpair invoked ifconfig for an unmanaged interface")
	}
}

func TestDeleteEpairUsesMarkedHostSideWhenPeerIsUnmarked(t *testing.T) {
	var commands []string
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{
				{Name: "aaadx_net4a", Groups: []string{sylveEpairGroup}},
				{Name: "aaadx_net4b"},
			}, nil
		},
		func(command string, args ...string) (string, error) {
			commands = append(commands, command+" "+strings.Join(args, " "))
			return "", nil
		},
	)

	if err := (&Service{}).DeleteEpair("aaadx_net4"); err != nil {
		t.Fatalf("DeleteEpair: %v", err)
	}
	if fmt.Sprint(commands) != fmt.Sprint([]string{"/sbin/ifconfig aaadx_net4a destroy"}) {
		t.Fatalf("DeleteEpair commands = %v, want host-side destroy", commands)
	}
}

func TestSyncEpairsLeavesUnmanagedHostPairsAlone(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t, &jailModels.Jail{}, &jailModels.Network{})
	var commands []string
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{
				{Name: "aaadx_net4a"},
				{Name: "aaadx_net4b"},
			}, nil
		},
		func(command string, args ...string) (string, error) {
			commands = append(commands, command+" "+strings.Join(args, " "))
			return "", nil
		},
	)

	if err := svc.SyncEpairs(false); err != nil {
		t.Fatalf("SyncEpairs: %v", err)
	}
	if len(commands) != 1 || commands[0] != "/usr/sbin/jls path" {
		t.Fatalf("SyncEpairs commands = %v, want only jls", commands)
	}
}

func TestSyncEpairsRejectsUnmanagedExpectedPair(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &jailModels.Jail{}, &jailModels.Network{}, &networkModels.ManualSwitch{})
	manual := networkModels.ManualSwitch{Name: "test-switch", Bridge: "test-bridge"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual switch: %v", err)
	}
	jail := jailModels.Jail{CTID: 42, Name: "test-jail"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	network := jailModels.Network{JailID: jail.ID, Name: "initial", SwitchID: manual.ID, SwitchType: "manual"}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("create jail network: %v", err)
	}
	name := utils.HashIntToNLetters(int(jail.CTID), 5) + "_net" + fmt.Sprint(network.ID) + "a"
	var commands []string
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{{Name: name}}, nil
		},
		func(command string, args ...string) (string, error) {
			commands = append(commands, command+" "+strings.Join(args, " "))
			return "", nil
		},
	)

	err := svc.SyncEpairs(false)
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairOwnershipConflict) || !strings.Contains(err.Error(), "refusing to adopt unmanaged epair") {
		t.Fatalf("SyncEpairs error = %v", err)
	}
	if fmt.Sprint(commands) != fmt.Sprint([]string{"/usr/sbin/jls path"}) {
		t.Fatalf("SyncEpairs commands = %v, want only jls", commands)
	}
}

func TestSyncEpairsAcceptsUnmarkedVNETPeer(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &jailModels.Jail{}, &jailModels.Network{}, &networkModels.ManualSwitch{})
	manual := networkModels.ManualSwitch{Name: "test-switch", Bridge: "test-bridge"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual switch: %v", err)
	}
	jail := jailModels.Jail{CTID: 42, Name: "test-jail"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	network := jailModels.Network{JailID: jail.ID, Name: "initial", SwitchID: manual.ID, SwitchType: "manual"}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("create jail network: %v", err)
	}
	base := utils.HashIntToNLetters(int(jail.CTID), 5) + "_net" + fmt.Sprint(network.ID)
	var commands []string
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{
				{Name: base + "a", Groups: []string{sylveEpairGroup}},
				{Name: base + "b"},
			}, nil
		},
		func(command string, args ...string) (string, error) {
			commands = append(commands, command+" "+strings.Join(args, " "))
			return "", nil
		},
	)

	if err := svc.SyncEpairs(false); err != nil {
		t.Fatalf("SyncEpairs: %v", err)
	}
	if fmt.Sprint(commands) != fmt.Sprint([]string{"/usr/sbin/jls path"}) {
		t.Fatalf("SyncEpairs commands = %v, want only jls", commands)
	}
}
