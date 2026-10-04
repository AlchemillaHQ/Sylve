// SPDX-License-Identifier: BSD-2-Clause

package startup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestBackupUnmanagedISCSIConfigUsesMode0600(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "iscsi.conf")
	backup := filepath.Join(dir, "iscsi.conf.pre-sylve")
	const content = "chapiname = user;\nchapsecret = secretpassw0rd;\n"

	if err := os.WriteFile(source, []byte(content), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := backupUnmanagedISCSIConfig(source, backup); err != nil {
		t.Fatalf("backupUnmanagedISCSIConfig: %v", err)
	}

	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != content {
		t.Fatalf("backup content = %q, want %q", got, content)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
}

func TestBackupUnmanagedISCSIConfigDoesNotOverwriteExistingBackup(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ctl.conf")
	backup := filepath.Join(dir, "ctl.conf.pre-sylve")
	if err := os.WriteFile(source, []byte("new unmanaged config"), 0600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(backup, []byte("original backup"), 0644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := backupUnmanagedISCSIConfig(source, backup); err != nil {
		t.Fatalf("backupUnmanagedISCSIConfig: %v", err)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != "original backup" {
		t.Fatalf("existing backup was overwritten: %q", got)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("existing backup mode = %o, want 600", info.Mode().Perm())
	}
}

func TestBackupUnmanagedISCSIConfigSkipsManagedSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ctl.conf")
	backup := filepath.Join(dir, "ctl.conf.pre-sylve")
	if err := os.WriteFile(source, []byte(iscsiConfigMarker+"\n"), 0600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := backupUnmanagedISCSIConfig(source, backup); err != nil {
		t.Fatalf("backupUnmanagedISCSIConfig: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("managed source unexpectedly backed up: %v", err)
	}
}

func TestBackupISCSIConfigRequiresMarkerAtStart(t *testing.T) {
	dir := t.TempDir()
	source, backup := filepath.Join(dir, "ctl.conf"), filepath.Join(dir, "ctl.conf.pre-sylve")
	content := "# unmanaged file\n" + iscsiConfigMarker + "\n"
	if err := os.WriteFile(source, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := backupUnmanagedISCSIConfig(source, backup); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(backup)
	if err != nil || string(got) != content {
		t.Fatal("embedded marker skipped unmanaged backup")
	}
}

func TestISCSIInitiatorStartupHasSharedDeadline(t *testing.T) {
	t.Cleanup(utils.SetCommandWithContextForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "sleep 5 & wait")
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := ensureISCSIInitiatorDaemon(ctx)
	if err == nil || strings.Contains(err.Error(), "sleep") || time.Since(start) > time.Second {
		t.Fatalf("unbounded or unsafe startup result: %v", err)
	}
}
