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
	for version, want := range map[string]string{"15.0": "15.0", "15.1p4": "15.1", "16.snap20261019120000": "16.snap", "15.snap20261019120000_1": "15.snap", "15.1.a1.20261019120000": "15.1", "15.1.b2.20261019120000": "15.1", "15.1.rc1.20261019120000_1": "15.1"} {
		got, err := HostBranch(version)
		if err != nil || got != want {
			t.Fatalf("%s: %s, %v", version, got, err)
		}
	}
	for _, version := range []string{"15.1.rc1", "15.1.x1.20261019120000", "15.snap1", "16.0-CURRENT", "", "15.1/../../"} {
		if _, err := HostBranch(version); err == nil {
			t.Fatalf("accepted %q", version)
		}
	}
}

func TestHostTargetIsExplicit(t *testing.T) {
	c := NewHostClient(nil)
	args, err := c.args("upgrade", "-U", "-n")
	if err != nil || slices.Contains(args, "-j") || slices.Contains(args, "-c") || !slices.Contains(args, "IGNORE_OSVERSION=true") || !slices.Contains(args, "AUTOMERGE=true") || slices.Contains(args, "PKG_ENV=SYLVE=1") {
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

func TestPackageVersionProbeRangeAndBinaryTrust(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("pkg binaries must be root-owned")
	}
	for _, version := range []string{"2.8.4", "2.8.5", "2.7.9", "3.0.0"} {
		t.Run(version, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "pkg-static")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0755); err != nil {
				t.Fatal(err)
			}
			got, err := probeVersion(binary)
			if err != nil || got != version {
				t.Fatalf("version probe: %q %v", got, err)
			}
			if err = Probe(binary); (err == nil) != (version == "2.8.4" || version == "2.8.5") {
				t.Fatalf("version %s: %v", version, err)
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
		if supportedPkgVersion(version) {
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

func TestInspectHostRequiresConsistentPkgbase(t *testing.T) {
	arch := runtime.GOARCH
	if arch == "arm64" {
		arch = "aarch64"
	}
	abi := "FreeBSD:16:" + arch
	for _, test := range []struct {
		name      string
		inventory func([]Package)
		unowned   bool
		running   uint32
		want      string
	}{
		{name: "snapshot with registered kernel"},
		{name: "mixed branches", inventory: func(p []Package) { p[1].Version = "16.0" }, want: "host_base_branch_mixed"},
		{name: "uname outside pkgbase", unowned: true, want: "host_updates_require_pkgbase"},
		{name: "runtime repository not configured", inventory: func(p []Package) { p[0].Repository = "FreeBSD-base-old" }, want: "host_base_repository_unavailable"},
		{name: "running kernel from another major", running: 1500068, want: "host_kernel_major_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := []Package{
				{Name: "FreeBSD-runtime", Origin: "base/FreeBSD-runtime", Version: "16.snap20261019120000", Repository: "base"},
				{Name: "FreeBSD-clibs", Origin: "base/FreeBSD-clibs", Version: "16.snap20261018120000", Repository: "base"},
				{Name: "FreeBSD-kernel-generic", Origin: "base/FreeBSD-kernel-generic", Version: "16.snap20261019120000", Repository: "base"},
				{Name: "pkg", Origin: "ports-mgmt/pkg", Version: "2.8.4", Repository: "ports"},
				{Name: "FreeBSD-runtime-backup-libraries", Origin: "base/FreeBSD-runtime-backup", Version: "20261019120000"},
			}
			if test.inventory != nil {
				test.inventory(inventory)
			}
			running := uint32(1600001)
			if test.running != 0 {
				running = test.running
			}
			c := NewHostClient(nil)
			c.HostOSVersion = func() (uint32, error) { return running, nil }
			c.RunCommand = func(_ context.Context, args []string) (Result, error) {
				if slices.Contains(args, "which") {
					if test.unowned {
						return Result{ExitCode: 1}, nil
					}
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
			if test.want != "" {
				if err == nil || err.Error() != test.want {
					t.Fatalf("expected %s, got %+v: %v", test.want, info, err)
				}
				return
			}
			if err != nil || info.Branch != "16.snap" || info.ABI != abi || info.KernelOSVersion != 1600002 || info.PackageManager != "pkg" {
				t.Fatalf("snapshot inspection: %+v %v", info, err)
			}
		})
	}
}

func TestHostCandidateArchivesMatchTheRunningBase(t *testing.T) {
	const ports = "\tFreeBSD:15:*\t/usr/local\tFreeBSD_version\t"
	base := Change{Action: "upgrade", Name: "FreeBSD-runtime", From: "15.1p1", To: "15.1p2", Repository: "base"}
	app := Change{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}
	sylve := Change{Action: "upgrade", Name: "sylve", From: "0.3.1", To: "0.3.2", Repository: "ports"}
	for _, test := range []struct {
		name     string
		changes  []Change
		metadata map[string]string
		preserve string
		want     string
	}{
		{name: "application for the running base", changes: []Change{app}, metadata: map[string]string{"app": "misc/app" + ports + "1501000"}},
		{name: "application for the base update", changes: []Change{base, app}, metadata: map[string]string{
			"FreeBSD-runtime": "base/FreeBSD-runtime\tFreeBSD:15:amd64\t/\tFreeBSD_version\t1501500",
			"app":             "misc/app" + ports + "1501500",
		}},
		{name: "other architecture", changes: []Change{app}, metadata: map[string]string{"app": "misc/app\tFreeBSD:15:aarch64\t/usr/local\tFreeBSD_version\t1501000"}, want: "host_package_abi_changed"},
		{name: "newer minor release", changes: []Change{app}, metadata: map[string]string{"app": "misc/app" + ports + "1502000"}, want: "host_package_requires_newer_release"},
		{name: "newer patch level", changes: []Change{app}, metadata: map[string]string{"app": "misc/app" + ports + "1501500"}, want: "host_package_requires_newer_base"},
		{name: "base archive from another major", changes: []Change{base}, metadata: map[string]string{"FreeBSD-runtime": "base/FreeBSD-runtime\tFreeBSD:15:amd64\t/\tFreeBSD_version\t1600000"}, want: "host_base_branch_changed"},
		{name: "sylve epoch downgrade", changes: []Change{{Action: "upgrade", Name: "sylve", From: "0.3.1", To: "0.3.0,1", Repository: "ports"}}, metadata: map[string]string{"sylve": "sysutils/sylve" + ports + "1501000"}, want: "sylve_downgrade_forbidden"},
		{name: "standalone sylve", changes: []Change{sylve}, metadata: map[string]string{"sylve": "sysutils/sylve" + ports + "1501000"}, preserve: "/usr/local/bin/sylve", want: "standalone_sylve_package_conflict"},
		{name: "pkg 3", changes: []Change{{Action: "upgrade", Name: "pkg", From: "2.8.4", To: "3.0.0", Repository: "ports"}}, metadata: map[string]string{"pkg": "ports-mgmt/pkg" + ports + "1501000"}, want: "pkg_version_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// file:// repositories are read in place, so nothing is fetched into the host's /var/cache/pkg.
			dir := t.TempDir()
			versions := map[string]string{}
			for _, change := range test.changes {
				versions[change.Name] = change.To
				if err := os.MkdirAll(filepath.Join(dir, "All"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "All", change.Name+".pkg"), []byte(archiveTestContents), 0644); err != nil {
					t.Fatal(err)
				}
			}
			client := NewHostClient(nil)
			client.HostSystem = &HostInfo{ABI: "FreeBSD:15:amd64", Branch: "15.1", Repository: "base", OSVersion: 1501000, KernelOSVersion: 1501000}
			client.SylveVersion, client.PreserveExecutable = "0.3.1", test.preserve
			client.RunCommand = func(_ context.Context, args []string) (Result, error) {
				switch {
				case slices.Contains(args, "search"):
					for name, version := range versions {
						if args[len(args)-1] == name+"-"+version {
							return Result{Stdout: archiveTestMetadata(name, version, "All/"+name+".pkg")}, nil
						}
					}
				case slices.Contains(args, "query"):
					name := strings.TrimSuffix(filepath.Base(args[len(args)-2]), ".pkg")
					if args[len(args)-1] == "%Fp" {
						return Result{Stdout: "/usr/local/share/" + name + "\n"}, nil
					}
					return Result{Stdout: name + "\t" + versions[name] + "\t" + test.metadata[name] + "\n"}, nil
				}
				t.Fatalf("unexpected command: %v", args)
				return Result{}, nil
			}
			repos := []Repository{{Name: "ports", URL: "file://" + dir}, {Name: "base", URL: "file://" + dir}}
			err := client.VerifyCandidates(t.Context(), Plan{Changes: test.changes}, repos)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.HasPrefix(err.Error(), test.want)) {
				t.Fatalf("expected %q: %v", test.want, err)
			}
		})
	}
}
