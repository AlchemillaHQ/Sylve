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

	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
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

func TestDeleteEpairFailsClosedWhenOnlyPeerExists(t *testing.T) {
	called := false
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{{Name: "aaadx_net4b"}}, nil
		},
		func(string, ...string) (string, error) {
			called = true
			return "", nil
		},
	)

	err := (&Service{}).DeleteEpair("aaadx_net4")
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairStateConflict) {
		t.Fatalf("DeleteEpair error = %v", err)
	}
	if called {
		t.Fatal("peer without an ownership sentinel triggered ifconfig")
	}
}

func TestEnsureEpairCreatesMissingPair(t *testing.T) {
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

	if err := (&Service{}).EnsureEpair("aaadx_net4"); err != nil {
		t.Fatalf("EnsureEpair: %v", err)
	}
	want := []string{
		"/sbin/ifconfig epair create",
		"/sbin/ifconfig epair0a name aaadx_net4a",
		"/sbin/ifconfig epair0b name aaadx_net4b",
		"/sbin/ifconfig aaadx_net4a group sylve",
		"/sbin/ifconfig aaadx_net4b group sylve",
	}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("EnsureEpair commands = %v, want %v", commands, want)
	}
}

func TestEnsureEpairReusesCompleteManagedPair(t *testing.T) {
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

	if err := (&Service{}).EnsureEpair("aaadx_net4"); err != nil {
		t.Fatalf("EnsureEpair: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("complete pair triggered commands: %v", commands)
	}
}

func TestEnsureEpairRejectsUnmanagedExpectedPair(t *testing.T) {
	called := false
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{
				{Name: "aaadx_net4a"},
				{Name: "aaadx_net4b"},
			}, nil
		},
		func(string, ...string) (string, error) {
			called = true
			return "", nil
		},
	)

	err := (&Service{}).EnsureEpair("aaadx_net4")
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairOwnershipConflict) {
		t.Fatalf("EnsureEpair error = %v", err)
	}
	if called {
		t.Fatal("unmanaged pair triggered ifconfig")
	}
}

func TestEnsureEpairFailsClosedWhenPeerIsNotHostVisible(t *testing.T) {
	called := false
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{{Name: "aaadx_net4a", Groups: []string{sylveEpairGroup}}}, nil
		},
		func(string, ...string) (string, error) {
			called = true
			return "", nil
		},
	)

	err := (&Service{}).EnsureEpair("aaadx_net4")
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairStateConflict) {
		t.Fatalf("EnsureEpair error = %v", err)
	}
	if called {
		t.Fatal("partial pair triggered ifconfig")
	}
}

func TestEnsureEpairFailsClosedWhenHostSideIsMissing(t *testing.T) {
	called := false
	setEpairTestHooks(t,
		func() ([]*iface.Interface, error) {
			return []*iface.Interface{{Name: "aaadx_net4b"}}, nil
		},
		func(string, ...string) (string, error) {
			called = true
			return "", nil
		},
	)

	err := (&Service{}).EnsureEpair("aaadx_net4")
	if err == nil || !errors.Is(err, networkServiceInterfaces.ErrEpairStateConflict) {
		t.Fatalf("EnsureEpair error = %v", err)
	}
	if called {
		t.Fatal("partial pair triggered ifconfig")
	}
}
