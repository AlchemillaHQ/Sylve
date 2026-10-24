// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package pkgmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIntegrationHostPkgbaseApproval(t *testing.T) {
	if testing.Short() || os.Geteuid() != 0 {
		t.Skip("requires root on FreeBSD; uses an isolated chroot")
	}
	if err := ProbeHost(); err != nil {
		t.Skip(err)
	}
	version, err := hostOSVersion()
	if err != nil {
		t.Fatal(err)
	}
	major := version / 100000
	if major < 15 {
		t.Skip("requires FreeBSD 15 or newer")
	}
	directory := t.TempDir()
	root := filepath.Join(directory, "root")
	write := func(t *testing.T, path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, binary string, args ...string) string {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Env = Environment()
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %s %v: %v\n%s", binary, args, err, output)
		}
		return string(output)
	}
	for _, path := range []string{"var/db/pkg", "var/cache/pkg", "etc/pkg", "usr/local/etc/pkg/repos", "usr/local/sbin", "tmp", "dev"} {
		if err = os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"bin/sh", "usr/local/sbin/pkg-static"} {
		source := DefaultBinary
		if path == "bin/sh" {
			source = "/rescue/sh"
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, path), data)
		if err = os.Chmod(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "dev/null"), nil)
	write(t, filepath.Join(root, "etc/passwd"), []byte("root:*:0:0:root:/root:/bin/sh\nnobody:*:65534:65534:Unprivileged:/nonexistent:/bin/sh\n"))
	write(t, filepath.Join(root, "etc/group"), []byte("wheel:*:0:root\n"))
	write(t, filepath.Join(root, "etc/master.passwd"), []byte("root:*:0:0::0:0:root:/root:/bin/sh\nnobody:*:65534:65534::0:0:Unprivileged:/nonexistent:/bin/sh\n"))
	run(t, "/usr/sbin/pwd_mkdb", "-d", filepath.Join(root, "etc"), filepath.Join(root, "etc/master.passwd"))
	config := filepath.Join(directory, "build-pkg.conf")
	write(t, config, []byte(fmt.Sprintf("PKG_DBDIR: %q\nPKG_CACHEDIR: %q\nREPOS_DIR: []\n", filepath.Join(directory, "build-db"), filepath.Join(directory, "build-cache"))))
	oldVersion := fmt.Sprintf("%d.snap20261018000000", major)
	newVersion := fmt.Sprintf("%d.snap20261019000000", major)
	abi := strings.TrimSpace(run(t, DefaultBinary, "config", "ABI"))
	names := []string{"FreeBSD-runtime", "FreeBSD-clibs", "FreeBSD-kernel-generic", "host-fixture-app", "pkg"}
	appContent := ""
	create := func(t *testing.T, name, repository string, updated bool) {
		t.Helper()
		pkgVersion, origin, prefix := oldVersion, "base/"+name, "/"
		file := "/usr/share/host-update-fixture/" + name
		if updated {
			pkgVersion = newVersion
		}
		switch name {
		case "FreeBSD-runtime":
			file = "/usr/bin/uname"
		case "FreeBSD-kernel-generic":
			file = "/boot/kernel/kernel"
		case "host-fixture-app":
			pkgVersion, origin, prefix = "1", "misc/host-fixture-app", "/usr/local"
			if updated {
				pkgVersion = "2"
			}
		case "pkg":
			pkgVersion, origin, prefix = "2.8.4", "ports-mgmt/pkg", "/usr/local"
			if updated {
				pkgVersion = "2.8.5"
			}
		}
		stage := filepath.Join(directory, "stage-"+repository+"-"+name)
		write(t, filepath.Join(stage, file), []byte(pkgVersion))
		if name == "host-fixture-app" {
			write(t, filepath.Join(stage, file), []byte(pkgVersion+appContent))
		}
		if name == "FreeBSD-runtime" {
			data, err := os.ReadFile("/usr/bin/uname")
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(stage, file), data)
		}
		manifest := map[string]any{"name": name, "version": pkgVersion, "origin": origin, "abi": abi, "prefix": prefix, "comment": "Isolated host-update fixture", "desc": "No real system components", "maintainer": "test@example.invalid", "www": "https://example.invalid", "files": map[string]string{file: "-"}, "annotations": map[string]string{"FreeBSD_version": strconv.FormatUint(uint64(version), 10)}}
		if name == "FreeBSD-runtime" {
			value := "vendor=initial\n"
			if updated {
				value = "vendor=updated\n"
				manifest["scripts"] = map[string]string{"post-install": "printf executed > /var/db/pkg/host-update-script"}
			}
			write(t, filepath.Join(stage, "etc/host-update-fixture.conf"), []byte(value))
			manifest["files"].(map[string]string)["/etc/host-update-fixture.conf"] = "-"
			manifest["config"] = []string{"/etc/host-update-fixture.conf"}
		}
		raw, _ := json.Marshal(manifest)
		manifestPath := filepath.Join(directory, "manifest-"+repository+"-"+name+".json")
		write(t, manifestPath, raw)
		archiveDir := filepath.Join(root, repository, "All")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			t.Fatal(err)
		}
		run(t, DefaultBinary, "-C", config, "create", "-M", manifestPath, "-r", stage, "-o", archiveDir)
	}
	for _, name := range names {
		create(t, name, "initial", false)
		create(t, name, "updated", true)
	}
	for _, repository := range []string{"initial", "updated"} {
		run(t, DefaultBinary, "-C", config, "repo", filepath.Join(root, repository))
	}
	repoFile := filepath.Join(root, "usr/local/etc/pkg/repos/fixture.conf")
	write(t, repoFile, []byte(`fixture: {url: "file:///initial", enabled: yes, signature_type: "none"}`))
	args := append([]string{"-c", root, "-o", "IGNORE_OSVERSION=true", "install", "-y", "-r", "fixture"}, names...)
	run(t, DefaultBinary, args...)
	write(t, filepath.Join(root, "etc/host-update-fixture.conf"), []byte("vendor=local\n"))
	write(t, repoFile, []byte(`fixture: {url: "file:///updated", enabled: yes, signature_type: "none"}`))
	wrapper := filepath.Join(directory, "fixture-pkg")
	write(t, wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -c %q \"$@\"\n", DefaultBinary, root)))
	if err = os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	client := NewHostClient(&log)
	client.Binary = wrapper
	inventory, err := client.Inventory(t.Context())
	if err != nil {
		t.Fatalf("inventory: %v\n%s", err, &log)
	}
	info, err := client.InspectHost(t.Context(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	client.HostSystem = &info
	repositories, _, err := client.Repositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Refresh(t.Context(), repositories); err != nil {
		t.Fatal(err)
	}
	manager, err := client.HostPreview(t.Context(), repositories, nil)
	if err != nil || len(manager.Changes) != 1 || manager.Changes[0].Name != "pkg" || !slices.Equal(client.HostSelection, []string{"pkg"}) {
		t.Fatalf("pkg-first preview: %+v %v\n%s", manager, err, &log)
	}
	if err = client.Apply(t.Context(), UpgradeArgs(repositories, client.HostSelection, false), manager); err != nil {
		t.Fatalf("pkg-first application: %v\n%s", err, &log)
	}
	inventory, err = client.Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range inventory {
		if pkg.Name == "pkg" && pkg.Version != "2.8.5" {
			t.Fatal("package manager was not updated")
		}
		if pkg.Name == "FreeBSD-runtime" && pkg.Version != oldVersion {
			t.Fatal("package-manager stage unexpectedly updated the base")
		}
	}
	plan, err := client.HostPreview(t.Context(), repositories, nil)
	if err != nil || len(plan.Changes) != 4 {
		t.Fatalf("full-host preview: %+v %v\n%s", plan, err, &log)
	}
	newReader := func(onFetch func()) *Client {
		reader := &Client{Target: Target{JID: 1, Name: "fixture", Root: root}, Scope: ScopeSystem, System: &SystemInfo{Repository: "fixture"}, Log: &log}
		reader.RunCommand = func(ctx context.Context, args []string) (Result, error) {
			if len(args) < 2 || args[0] != "-j" || args[1] != "1" {
				return Result{}, fmt.Errorf("unexpected fixture target: %v", args)
			}
			if slices.Contains(args, "fetch") {
				onFetch()
			}
			command := exec.CommandContext(ctx, DefaultBinary, append([]string{"-c", root}, args[2:]...)...)
			command.Env = Environment()
			output, err := command.CombinedOutput()
			if err != nil {
				return Result{}, fmt.Errorf("fixture pkg: %w: %s", err, output)
			}
			return Result{Stdout: string(output)}, nil
		}
		return reader
	}
	t.Run("file repository inspection", func(t *testing.T) {
		fetches, inspected := 0, 0
		reader := newReader(func() { fetches++ })
		if err := reader.inspectCandidates(t.Context(), plan, repositories, func(change Change, archive string) error {
			if !strings.HasPrefix(archive, "/updated/") {
				return fmt.Errorf("file repository was copied unnecessarily: %s", archive)
			}
			metadata, err := reader.hostMetadata(t.Context(), change.Name, archive)
			if err != nil || metadata.version != change.To {
				return fmt.Errorf("archive identity mismatch: %+v %v", metadata, err)
			}
			inspected++
			return nil
		}); err != nil {
			t.Fatalf("batched inspection: %v\n%s", err, &log)
		}
		if fetches != 0 || inspected != len(plan.Changes) {
			t.Fatalf("expected in-place inspection without fetching: fetches=%d inspected=%d", fetches, inspected)
		}
	})
	var offline atomic.Bool
	var archivesMissing atomic.Bool
	var resumed atomic.Bool
	var requests, downloads atomic.Int64
	files := http.FileServer(http.Dir(filepath.Join(root, "updated")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Range") != "" {
			resumed.Store(true)
		}
		if strings.HasSuffix(r.URL.Path, ".pkg") && (strings.HasPrefix(r.URL.Path, "/All/") || strings.HasPrefix(r.URL.Path, "/Hashed/")) {
			downloads.Add(1)
			if archivesMissing.Load() {
				http.NotFound(w, r)
				return
			}
		}
		if offline.Load() {
			http.Error(w, "fixture mirror offline", http.StatusServiceUnavailable)
			return
		}
		r.Header.Del("If-Modified-Since")
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	write(t, repoFile, []byte(fmt.Sprintf(`fixture: {url: %q, enabled: yes, signature_type: "none"}`, server.URL)))
	repositories, _, err = client.Repositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Refresh(t.Context(), repositories); err != nil {
		t.Fatal(err)
	}
	t.Run("catalog-only previews do not request missing archives", func(t *testing.T) {
		archivesMissing.Store(true)
		defer archivesMissing.Store(false)
		beforeRequests, beforeDownloads := requests.Load(), downloads.Load()
		for _, selection := range [][]string{nil, {"host-fixture-app"}} {
			preview, err := client.HostPreview(t.Context(), repositories, selection)
			if err != nil {
				t.Fatalf("preview depended on archive availability: %v\n%s", err, &log)
			}
			if err = client.ValidateInstalled(preview, inventory); err != nil {
				t.Fatal(err)
			}
			want := len(plan.Changes)
			if len(selection) > 0 {
				want = 1
			}
			if len(preview.Changes) != want || requests.Load() != beforeRequests || downloads.Load() != beforeDownloads {
				t.Fatalf("preview fetched packages or changed the transaction: %+v requests=%d downloads=%d", preview, requests.Load()-beforeRequests, downloads.Load()-beforeDownloads)
			}
		}
		current, err := client.Inventory(t.Context())
		if err != nil || InventoryDigest(current) != InventoryDigest(inventory) {
			t.Fatalf("preview changed installed packages: %+v %v", current, err)
		}
	})
	if !t.Run("persistent checksum validated archive cache", func(t *testing.T) {
		archives := map[string]string{}
		inspect := func(wantFetches int) {
			t.Helper()
			fetches, inspected := 0, 0
			reader := newReader(func() { fetches++ })
			if err := reader.inspectCandidates(t.Context(), plan, repositories, func(change Change, archive string) error {
				if filepath.Dir(archive) != "/var/cache/pkg" {
					return fmt.Errorf("not using the native pkg cache: %s", archive)
				}
				metadata, err := reader.hostMetadata(t.Context(), change.Name, archive)
				if err != nil || metadata.version != change.To {
					return fmt.Errorf("cached archive identity mismatch: %+v %v", metadata, err)
				}
				archives[change.Name] = archive
				inspected++
				return nil
			}); err != nil {
				t.Fatalf("cache inspection: %v\n%s", err, &log)
			}
			if fetches != wantFetches || inspected != len(plan.Changes) {
				t.Fatalf("expected %d batched fetches and an inspection per archive: fetches=%d inspected=%d", wantFetches, fetches, inspected)
			}
		}
		inspect(1)
		if downloads.Load() != int64(len(plan.Changes)) {
			t.Fatalf("initial fetch downloaded %d archives instead of %d", downloads.Load(), len(plan.Changes))
		}
		beforeRequests := requests.Load()
		offline.Store(true)
		inspect(0)
		if requests.Load() != beforeRequests {
			t.Fatal("repeat verification contacted the offline mirror despite a complete cache")
		}
		offline.Store(false)
		beforeDownloads := downloads.Load()
		if err := os.Remove(filepath.Join(root, archives["host-fixture-app"])); err != nil {
			t.Fatal(err)
		}
		inspect(1)
		if downloads.Load()-beforeDownloads != 1 {
			t.Fatal("a missing archive caused unchanged packages to be downloaded again")
		}
		beforeDownloads = downloads.Load()
		archive := filepath.Join(root, archives["host-fixture-app"])
		contents, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		write(t, archive, contents[:len(contents)/2])
		inspect(1)
		if downloads.Load()-beforeDownloads != 1 || !resumed.Load() {
			t.Fatal("incomplete cached archive was not resumed independently")
		}
		beforeDownloads = downloads.Load()
		contents[len(contents)/2] ^= 0xff
		write(t, archive, contents)
		inspect(1)
		if downloads.Load()-beforeDownloads != 1 {
			t.Fatal("corrupt cached bytes were not refetched independently")
		}
		beforeDownloads = downloads.Load()
		previous := archives["host-fixture-app"]
		appContent = " republished with new bytes"
		create(t, "host-fixture-app", "updated", true)
		run(t, DefaultBinary, "-C", config, "repo", filepath.Join(root, "updated"))
		if err := client.Refresh(t.Context(), repositories); err != nil {
			t.Fatal(err)
		}
		inspect(1)
		if downloads.Load()-beforeDownloads != 1 || archives["host-fixture-app"] == previous {
			t.Fatal("a changed catalog checksum reused the previous archive or refetched unchanged packages")
		}
		offline.Store(true)
	}) {
		t.Fatal("archive cache verification failed")
	}
	beforeRequests := requests.Load()
	before := InventoryDigest(inventory)
	tampered := plan
	tampered.Changes = slices.Clone(plan.Changes)
	for i := range tampered.Changes {
		if tampered.Changes[i].Name == "host-fixture-app" {
			tampered.Changes[i].To = "999"
		}
	}
	if err = client.Apply(t.Context(), UpgradeArgs(repositories, nil, false), tampered); err == nil || !strings.Contains(err.Error(), "package_plan_changed") {
		t.Fatalf("changed approval accepted: %v\n%s", err, &log)
	}
	inventory, err = client.Inventory(t.Context())
	if err != nil || InventoryDigest(inventory) != before {
		t.Fatalf("rejected transaction changed inventory: %v", err)
	}
	if err = client.Apply(t.Context(), UpgradeArgs(repositories, nil, false), plan); err != nil {
		t.Fatalf("approved base/kernel application: %v\n%s", err, &log)
	}
	if requests.Load() != beforeRequests {
		t.Fatal("installation contacted the mirror instead of consuming the verified cache")
	}
	kernel, err := os.ReadFile(filepath.Join(root, "boot/kernel/kernel"))
	if err != nil || string(kernel) != newVersion {
		t.Fatalf("fixture kernel not updated: %q %v", kernel, err)
	}
	local, err := os.ReadFile(filepath.Join(root, "etc/host-update-fixture.conf"))
	if err != nil || string(local) != "vendor=local\n" {
		t.Fatalf("local configuration overwritten: %q %v", local, err)
	}
	if _, err = os.Stat(filepath.Join(root, "etc/host-update-fixture.conf.pkgnew")); err != nil {
		t.Fatalf("configuration conflict not retained: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(root, "var/db/pkg/host-update-script"))
	if err != nil || string(marker) != "executed" {
		t.Fatalf("pkgbase scripts were disabled: %q %v", marker, err)
	}
	t.Log("Real pkg cache reuse, corruption recovery, changed-digest invalidation, offline installation, self-update, kernel/base transaction, confirmation rejection, configuration conflict and package script verified in an isolated chroot.")
}
