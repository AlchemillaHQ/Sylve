// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// tempDir is t.TempDir for trees Sylve may leave system-immutable files in, such as a jail's
// .sylve; the cleanup clears the flags first so TempDir can remove them.
func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("chflags", "-R", "noschg", dir).Run() })
	return dir
}

func metadataTestRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("setting system flags needs root")
	}
	return tempDir(t)
}

func writeMetadataOrSkip(t *testing.T, mountPoint string, data string) {
	t.Helper()
	if err := WriteJailMetadata(mountPoint, []byte(data)); err != nil {
		if errors.Is(err, syscall.EOPNOTSUPP) {
			t.Skipf("file system has no flags: %v", err)
		}
		t.Fatalf("write metadata: %v", err)
	}
}

func TestWriteJailMetadataLeavesItImmutable(t *testing.T) {
	mountPoint := metadataTestRoot(t)
	path := filepath.Join(mountPoint, ".sylve", "jail.json")

	writeMetadataOrSkip(t, mountPoint, "v1")
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !isImmutable(dir) || !isImmutable(first) {
		t.Fatal(".sylve or jail.json isn't immutable")
	}
	if err := os.WriteFile(path, []byte("guest"), 0o644); err == nil {
		t.Fatal("wrote to an immutable jail.json")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "extra"), nil, 0o644); err == nil {
		t.Fatal("created a file in an immutable .sylve")
	}

	writeMetadataOrSkip(t, mountPoint, "v1")
	if again, err := os.Lstat(path); err != nil || !os.SameFile(first, again) {
		t.Fatalf("unchanged metadata was rewritten: %v", err)
	}

	writeMetadataOrSkip(t, mountPoint, "v2")
	if data, err := os.ReadFile(path); err != nil || string(data) != "v2" {
		t.Fatalf("metadata = %q, %v; want v2", data, err)
	}
	if info, err := os.Lstat(path); err != nil || !isImmutable(info) {
		t.Fatalf("rewritten jail.json isn't immutable: %v", err)
	}
}

func TestWriteJailMetadataReplacesWhatTheJailPutThere(t *testing.T) {
	mountPoint := metadataTestRoot(t)
	elsewhere := filepath.Join(mountPoint, "tmp")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(mountPoint, ".sylve")); err != nil {
		t.Fatal(err)
	}

	writeMetadataOrSkip(t, mountPoint, "v1")
	if info, err := os.Lstat(filepath.Join(mountPoint, ".sylve")); err != nil || !info.IsDir() {
		t.Fatalf(".sylve isn't a real directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "jail.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata followed the jail's symlink: %v", err)
	}
}

func TestWriteJailMetadataDropsLegacyHostConfig(t *testing.T) {
	mountPoint := metadataTestRoot(t)
	legacy := filepath.Join(mountPoint, ".sylve", "host-config", "scripts")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "pre-start.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeMetadataOrSkip(t, mountPoint, "v1")
	if _, err := os.Lstat(filepath.Join(mountPoint, ".sylve", "host-config")); !os.IsNotExist(err) {
		t.Fatalf("legacy host-config is still there: %v", err)
	}
}
