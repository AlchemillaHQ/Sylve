// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package pkgmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestHostBranch(t *testing.T) {
	for version, want := range map[string]string{"15.0": "15.0", "15.1p4": "15.1", "16.snap20261019120000": "16.snap", "15.snap20261019120000_1": "15.snap"} {
		got, err := HostBranch(version)
		if err != nil || got != want {
			t.Fatalf("%s: %s, %v", version, got, err)
		}
	}
	for _, version := range []string{"14.3", "15.1.rc1", "15.snap1", "16.0-CURRENT", "", "15.1/../../"} {
		if _, err := HostBranch(version); err == nil {
			t.Fatalf("accepted %q", version)
		}
	}
}

func TestHostTargetIsExplicit(t *testing.T) {
	c := NewHostClient(nil)
	args, err := c.args("upgrade", "-U", "-n")
	if err != nil || slices.Contains(args, "-j") || slices.Contains(args, "-c") || !slices.Contains(args, "IGNORE_OSVERSION=true") || !slices.Contains(args, "AUTOMERGE=true") {
		t.Fatalf("host arguments: %v, %v", args, err)
	}
	c.Scope = ScopeApplications
	if _, err := c.args("query"); err == nil {
		t.Fatal("host accepted application scope")
	}
	c = &Client{Target: Target{Name: "jail", JID: 1, Root: "/jail"}, Scope: ScopeHost}
	if _, err := c.args("query"); err == nil {
		t.Fatal("jail accepted host scope")
	}
	for _, target := range []Target{{Name: "host", Root: "/"}, {Host: true, Root: "/", Name: "host", JID: 1}, {Host: true, Name: "host", Root: "/jail"}, {Host: true, Name: "host", Root: "/", Offline: true}} {
		if target.validate() == nil {
			t.Fatalf("accepted unsafe target: %+v", target)
		}
	}
}

func TestHostPlanBranchGuards(t *testing.T) {
	c := NewHostClient(nil)
	c.HostSystem = &HostInfo{Branch: "16.snap"}
	for _, change := range []Change{
		{Action: "upgrade", Name: "FreeBSD-kernel-generic", From: "16.snap20261018120000", To: "16.snap20261019120000"},
		{Action: "upgrade", Name: "drm-kmod", From: "1", To: "2"},
	} {
		if err := c.validateHostChanges(Plan{Changes: []Change{change}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []Change{
		{Action: "upgrade", Name: "FreeBSD-runtime", From: "16.snap20261018120000", To: "16.0"},
		{Action: "upgrade", Name: "FreeBSD-runtime", From: "16.snap20261018120000", To: "17.snap20261019120000"},
		{Action: "remove", Name: "FreeBSD-kernel-generic"},
		{Action: "downgrade", Name: "sylve"},
		{Action: "remove", Name: "pkg"},
	} {
		if err := c.validateHostChanges(Plan{Changes: []Change{change}}); err == nil {
			t.Fatalf("accepted %+v", change)
		}
	}
}

func TestHostPreservesStandaloneExecutable(t *testing.T) {
	c := NewHostClient(nil)
	c.PreserveExecutable = "/usr/local/sbin/sylve"
	c.RunCommand = func(context.Context, []string) (Result, error) { return Result{Stdout: "/usr/local/sbin/sylve\n"}, nil }
	if _, err := c.packageFiles(t.Context(), "other-package", ""); err == nil || !strings.Contains(err.Error(), "standalone_sylve_package_conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
	c.RunCommand = func(context.Context, []string) (Result, error) { return Result{Stdout: "/boot/kernel/kernel\n"}, nil }
	if _, err := c.packageFiles(t.Context(), "FreeBSD-kernel-generic", ""); err != nil {
		t.Fatal(err)
	}
}

func TestHostPackageManagerPreviewIsSeparate(t *testing.T) {
	c := NewHostClient(nil)
	c.HostSystem = &HostInfo{Branch: "16.snap", PackageManager: "pkg"}
	var commands [][]string
	c.RunCommand = func(_ context.Context, args []string) (Result, error) {
		commands = append(commands, args)
		if args[len(args)-1] != "pkg" {
			t.Fatal("full preview ran before package-manager upgrade")
		}
		return Result{Stdout: "The following 1 package(s) will be affected (of 1 checked):\n\nInstalled packages to be UPGRADED:\n\tpkg: 2.8.4 -> 2.8.5 [ports]\n\nNumber of packages to be upgraded: 1\n"}, nil
	}
	plan, err := c.HostPreview(t.Context(), []Repository{{Name: "ports", Enabled: true}}, nil)
	if err != nil || len(plan.Changes) != 1 || len(commands) != 1 || !slices.Equal(c.HostSelection, []string{"pkg"}) {
		t.Fatalf("preview: %+v %v %v", plan, c.HostSelection, err)
	}
}

func TestPreviewWithoutSelectionRequiresHostScope(t *testing.T) {
	for _, scope := range []Scope{ScopeApplications, ScopeSystem, ScopeHost} {
		client := NewHostClient(nil)
		client.Scope = scope
		called := false
		client.RunCommand = func(context.Context, []string) (Result, error) {
			called = true
			return Result{Stdout: "Your packages are up to date.\n"}, nil
		}
		_, err := client.Preview(t.Context(), nil, nil)
		if scope == ScopeHost {
			if err != nil || !called {
				t.Fatalf("host preview: called=%v err=%v", called, err)
			}
		} else if err == nil || called {
			t.Fatalf("accepted unscoped jail preview: scope=%s called=%v err=%v", scope, called, err)
		}
	}
}

func TestPackageVersionProbePreservesJailPinAndBinaryTrust(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("pkg binaries must be root-owned")
	}
	for _, version := range []string{SupportedVersion, "2.8.5", "2.7.9", "3.0.0"} {
		t.Run(version, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "pkg-static")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0755); err != nil {
				t.Fatal(err)
			}
			got, err := probeVersion(binary)
			if err != nil || got != version {
				t.Fatalf("version probe: %q %v", got, err)
			}
			if err = Probe(binary); (err == nil) != (version == SupportedVersion) {
				t.Fatalf("jail version pin changed for %s: %v", version, err)
			}
			if err = os.Chmod(binary, 0777); err != nil {
				t.Fatal(err)
			}
			if _, err = probeVersion(binary); err == nil || err.Error() != "pkg_static_not_trusted" {
				t.Fatalf("untrusted binary accepted: %v", err)
			}
		})
	}
}

func TestSylvePortsEpochCannotHideApplicationDowngrade(t *testing.T) {
	for _, test := range []struct {
		target  string
		allowed bool
	}{{"0.3.0,99", false}, {"0.3.0_100", false}, {"0.3.1_1", true}, {"0.3.2,1", true}, {"not-a-version", false}} {
		if sylvePackageUpgradeAllowed("0.3.1", test.target) != test.allowed {
			t.Fatalf("incorrect decision for %s", test.target)
		}
	}
	for _, version := range []string{"2.7.9", "2.8.3", "3.0.0", "2.9.-1", "invalid"} {
		if supportedHostPkgVersion(version) {
			t.Fatalf("accepted pkg %s", version)
		}
	}
}

func TestHostResultMustMatchEveryApprovedChange(t *testing.T) {
	plan := Plan{Changes: []Change{{Name: "example", Action: "upgrade", From: "1", To: "2"}, {Name: "obsolete", Action: "remove", From: "1"}}}
	if err := ValidateHostResult(plan, []Package{{Name: "example", Version: "2"}}); err != nil {
		t.Fatal(err)
	}
	for _, inventory := range [][]Package{nil, {{Name: "example", Version: "1"}}, {{Name: "example", Version: "2"}, {Name: "obsolete", Version: "1"}}} {
		if err := ValidateHostResult(plan, inventory); err == nil {
			t.Fatalf("accepted incomplete result: %+v", inventory)
		}
	}
}

func TestHostBaseUpdatesMustNotSilentlySkipLockedSystemPackages(t *testing.T) {
	c := NewHostClient(nil)
	c.HostSystem = &HostInfo{Branch: "15.1"}
	base := Plan{Changes: []Change{{Name: "FreeBSD-runtime", Action: "upgrade", From: "15.1p1", To: "15.1p2"}}}
	inventory := []Package{{Name: "FreeBSD-runtime", Version: "15.1p1", Origin: "base/FreeBSD-runtime"}, {Name: "sylve", Origin: "sysutils/sylve", Locked: true}}
	if err := c.ValidateInstalled(base, inventory); err != nil {
		t.Fatalf("unrelated Sylve lock blocked base update: %v", err)
	}
	inventory = append(inventory, Package{Name: "FreeBSD-kernel-generic", Origin: "base/FreeBSD-kernel-generic", Locked: true})
	if err := c.ValidateInstalled(base, inventory); err == nil || !strings.HasPrefix(err.Error(), "host_base_package_locked") {
		t.Fatalf("partial base update accepted: %v", err)
	}
}

func TestHostSelectedPreviewsRejectSystemDependenciesWithoutArchiveInspection(t *testing.T) {
	for _, name := range []string{"FreeBSD-runtime", "FreeBSD-kernel-generic", "drm-kmod", "library"} {
		t.Run(name, func(t *testing.T) {
			client := NewHostClient(nil)
			client.HostSystem = &HostInfo{Branch: "16.snap"}
			client.HostSelection = []string{"app"}
			version := "2"
			if strings.HasPrefix(name, "FreeBSD-") {
				version = "16.snap20261019000000"
			}
			plan := Plan{Changes: []Change{{Name: "app", Action: "upgrade", From: "1", To: "2"}, {Name: name, Action: "install", To: version}}}
			inventory := []Package{{Name: "app", Version: "1", Origin: "misc/app"}}
			err := client.ValidateInstalled(plan, inventory)
			if name == "library" {
				if err != nil {
					t.Fatalf("ordinary dependency was blocked: %v", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), "host_base_selection_requires_all") {
				t.Fatalf("system dependency was allowed in a selected-package preview: %v", err)
			}
		})
	}
}

func TestInspectHostSupportsSnapshotVersionsAndRegisteredKernel(t *testing.T) {
	arch := runtime.GOARCH
	if arch == "arm64" {
		arch = "aarch64"
	}
	abi := "FreeBSD:16:" + arch
	c := NewHostClient(nil)
	c.HostOSVersion = func() (uint32, error) { return 1600001, nil }
	inventory := []Package{
		{Name: "FreeBSD-runtime", Origin: "base/FreeBSD-runtime", Version: "16.snap20261019120000", Repository: "base"},
		{Name: "FreeBSD-clibs", Origin: "base/FreeBSD-clibs", Version: "16.snap20261018120000", Repository: "base"},
		{Name: "FreeBSD-kernel-generic", Origin: "base/FreeBSD-kernel-generic", Version: "16.snap20261019120000", Repository: "base"},
		{Name: "pkg", Origin: "ports-mgmt/pkg", Version: "2.8.4", Repository: "ports"},
	}
	c.RunCommand = func(_ context.Context, args []string) (Result, error) {
		if slices.Contains(args, "which") {
			return Result{Stdout: "base/FreeBSD-runtime\n"}, nil
		}
		if slices.Contains(args, "-vv") {
			return Result{Stdout: "Repositories:\n  base: {\n    url: \"pkg+https://pkg.freebsd.org/" + abi + "/base_latest\",\n    enabled: yes\n  }\n  ports: {\n    url: \"pkg+https://pkg.freebsd.org/" + abi + "/latest\",\n    enabled: yes\n  }"}, nil
		}
		if slices.Contains(args, "query") {
			name := args[len(args)-1]
			osversion := 1600001
			if name == "FreeBSD-kernel-generic" {
				osversion = 1600002
			}
			return Result{Stdout: fmt.Sprintf("%s\t16.snap20261019120000\tbase/%s\t%s\t/\tFreeBSD_version\t%d\n", name, name, abi, osversion)}, nil
		}
		return Result{}, fmt.Errorf("unexpected command: %v", args)
	}
	info, err := c.InspectHost(t.Context(), inventory)
	if err != nil || info.Branch != "16.snap" || info.ABI != abi || info.KernelOSVersion != 1600002 || info.PackageManager != "pkg" {
		t.Fatalf("snapshot inspection: %+v %v", info, err)
	}
	inventory[1].Version = "16.0"
	if _, err = c.InspectHost(t.Context(), inventory); err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("accepted mixed branch: %v", err)
	}
}
