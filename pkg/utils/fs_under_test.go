// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUnderHelpersDontFollowSymlinksOutOfTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "jail")
	host := filepath.Join(dir, "host")
	for _, d := range []string{root, host} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(host, "secret")
	if err := os.WriteFile(secret, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../host/secret", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	escaped := filepath.Join(root, "etc", "secret")
	if err := WriteFileUnder(root, escaped, []byte("guest"), 0o644); err == nil {
		t.Error("write through an absolute symlink succeeded")
	}
	if err := MkdirAllUnder(root, filepath.Join(root, "etc", "sub"), 0o755); err == nil {
		t.Error("mkdir through an absolute symlink succeeded")
	}
	if err := RemoveUnder(root, escaped); err == nil {
		t.Error("remove through an absolute symlink succeeded")
	}
	if _, err := ReadFileUnder(root, escaped); err == nil {
		t.Error("read through an absolute symlink succeeded")
	}
	if _, err := ReadFileUnder(root, filepath.Join(root, "link")); err == nil {
		t.Error("read through a relative symlink out of the root succeeded")
	}
	if _, err := ReadFileUnder(root, secret); err == nil {
		t.Error("read of a path outside the root succeeded")
	}

	if data, err := os.ReadFile(secret); err != nil || string(data) != "host" {
		t.Fatalf("host file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(host, "sub")); !os.IsNotExist(err) {
		t.Fatalf("directory created outside the root: %v", err)
	}
}

func TestUnderHelpersWorkInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "usr", "local", "sylve", "scripts", "start.sh")
	if err := MkdirAllUnder(root, filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileUnder(root, path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := LstatUnder(root, path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("stat = %v, %v; want mode 0755", info, err)
	}
	if data, err := ReadFileUnder(root, path); err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if err := RemoveUnder(root, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LstatUnder(root, path); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

func TestReadFileUnderRefusesAFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "rc.conf")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFileUnder(root, fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read blocked on a FIFO")
	}
}
