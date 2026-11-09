// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJailFileSnapshotsStayInsideTheJailRoot(t *testing.T) {
	dir := t.TempDir()
	mountPoint := filepath.Join(dir, "jail")
	host := filepath.Join(dir, "host")
	for _, d := range []string{filepath.Join(mountPoint, "etc"), host} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hostConf := filepath.Join(host, "1.conf")
	rcConf := filepath.Join(mountPoint, "etc", "rc.conf")
	for path, data := range map[string]string{hostConf: "conf", rcConf: "rc"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snapshots, err := captureJailFiles(mountPoint, []string{hostConf, rcConf})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(mountPoint, "etc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(mountPoint, "etc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostConf, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := restoreJailFiles(snapshots); err == nil {
		t.Fatal("restore wrote through a symlink out of the jail root")
	}
	if data, _ := os.ReadFile(hostConf); string(data) != "conf" {
		t.Fatalf("host file not restored: %q", data)
	}
	if _, err := os.Stat(filepath.Join(host, "rc.conf")); !os.IsNotExist(err) {
		t.Fatalf("rc.conf was written on the host: %v", err)
	}
	if _, err := captureJailFiles(mountPoint, []string{rcConf}); err == nil {
		t.Fatal("capture read through a symlink out of the jail root")
	}
}

func TestJailFileSnapshotsKeepAGuestSymlinkAsALink(t *testing.T) {
	dir := t.TempDir()
	mountPoint := filepath.Join(dir, "jail")
	if err := os.MkdirAll(filepath.Join(mountPoint, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostTarget := filepath.Join(dir, "stub-resolv.conf")
	if err := os.WriteFile(hostTarget, []byte("host"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolv := filepath.Join(mountPoint, "etc", "resolv.conf")
	if err := os.Symlink(hostTarget, resolv); err != nil {
		t.Fatal(err)
	}

	snapshots, err := captureJailFiles(mountPoint, []string{resolv})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := os.Remove(resolv); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolv, []byte("nameserver 192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restoreJailFiles(snapshots); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if link, err := os.Readlink(resolv); err != nil || link != hostTarget {
		t.Fatalf("link = %q, %v; want %q", link, err, hostTarget)
	}
	if data, _ := os.ReadFile(hostTarget); string(data) != "host" {
		t.Fatalf("host target changed: %q", data)
	}
}
