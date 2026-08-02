// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/remoteexec"
)

func TestSaveSSHKeyWritesTrimmedKeyWithTrailingNewline(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("failed to create ssh key dir: %v", err)
	}

	keyPath, err := SaveSSHKey(42, "  test-key-data  ")
	if err != nil {
		t.Fatalf("SaveSSHKey failed: %v", err)
	}

	expectedPath := filepath.Join(SSHKeyDirectory, "target-42_id")
	if keyPath != expectedPath {
		t.Fatalf("expected key path %q, got %q", expectedPath, keyPath)
	}

	content, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read written key file: %v", err)
	}
	if string(content) != "test-key-data\n" {
		t.Fatalf("expected trimmed key content with newline, got %q", string(content))
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("failed to stat key file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected key file mode 0600, got %o", info.Mode().Perm())
	}
}

func TestBuildSSHArgsDoesNotInventIdentityForPasswordlessTarget(t *testing.T) {
	t.Parallel()

	service := &Service{}
	withoutKey := service.buildSSHArgs(&clusterModels.BackupTarget{ID: 42, SSHHost: "root@localhost"})
	for _, arg := range withoutKey {
		if arg == "-i" {
			t.Fatalf("target without configured key received identity flag: %v", withoutKey)
		}
	}

	withKey := service.buildSSHArgs(&clusterModels.BackupTarget{
		ID:         42,
		SSHHost:    "root@localhost",
		SSHKeyPath: "/configured/key",
	})
	foundIdentity := false
	for i := 0; i+1 < len(withKey); i++ {
		if withKey[i] == "-i" && withKey[i+1] == "/configured/key" {
			foundIdentity = true
			break
		}
	}
	if !foundIdentity {
		t.Fatalf("configured key was omitted: %v", withKey)
	}
}

func TestRunTargetSSHUsesEncodedRemoteCommands(t *testing.T) {
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	if err := os.WriteFile(sshPath, []byte("#!/bin/sh\nfor arg do remote=$arg; done\nexec /bin/sh -c \"$remote\"\n"), 0755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	service := &Service{}
	target := &clusterModels.BackupTarget{
		SSHHost:    "root@localhost",
		SSHPort:    22,
		BackupRoot: "tank/backups",
	}
	output, err := service.runTargetSSH(
		context.Background(),
		target,
		"/usr/bin/printf",
		"<%s>",
		"value; $(printf injected)",
	)
	if err != nil {
		t.Fatalf("run target command: %v", err)
	}
	if output != "<value; $(printf injected)>" {
		t.Fatalf("command output = %q", output)
	}

	output, err = service.runTargetDatasetScript(
		context.Background(),
		target,
		"tank/backups/vm",
		"value='script value'\nprintf '%s' \"$value\"",
	)
	if err != nil {
		t.Fatalf("run target script: %v", err)
	}
	if output != "script value" {
		t.Fatalf("script output = %q", output)
	}
}

func TestRemoteCommandLogFieldsRedactCommandData(t *testing.T) {
	tests := []struct {
		name        string
		argv        []string
		wantKind    string
		wantDataset string
	}{
		{
			name:        "snapshot token",
			argv:        []string{"zfs", "destroy", "tank/backups/vm@ha_secret-token"},
			wantKind:    "zfs.destroy",
			wantDataset: "tank/backups/vm",
		},
		{
			name:        "staging token",
			argv:        []string{"zfs", "recv", "tank/backups/vm_gen-secret-token/disk"},
			wantKind:    "zfs.recv",
			wantDataset: "tank/backups/vm/disk",
		},
		{
			name:     "metadata path",
			argv:     []string{"cat", "/mounted/.sylve/vm.json"},
			wantKind: "metadata.read",
		},
		{
			name:     "unknown command",
			argv:     []string{"custom-secret-token", "private-key"},
			wantKind: "remote",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, dataset := remoteCommandLogFields(test.argv)
			if kind != test.wantKind || dataset != test.wantDataset {
				t.Fatalf("log fields = (%q, %q), want (%q, %q)", kind, dataset, test.wantKind, test.wantDataset)
			}
		})
	}
}

func TestTargetRemoteCommandArgsPreserveStreamingWithoutExposingArguments(t *testing.T) {
	service := &Service{}
	target := &clusterModels.BackupTarget{
		ID:         42,
		SSHHost:    "root@backup.example",
		SSHPort:    22,
		BackupRoot: "tank/backups",
	}
	command, err := remoteexec.NewCommand(
		"zfs",
		"recv",
		"-o",
		"sylve:run-id=secret-token",
		"tank/backups/vm_gen-secret-token",
	)
	if err != nil {
		t.Fatalf("new remote command: %v", err)
	}
	args, err := service.targetRemoteCommandArgs(
		target,
		command,
		true,
		"zfs.recv",
		"tank/backups/vm_gen-secret-token",
	)
	if err != nil {
		t.Fatalf("target remote command args: %v", err)
	}
	for _, arg := range args {
		if arg == "-n" {
			t.Fatal("streaming receiver retained ssh stdin suppression")
		}
		if strings.Contains(arg, "secret-token") || strings.Contains(arg, "sylve:run-id") {
			t.Fatalf("remote argument was not encoded: %q", arg)
		}
	}
	if len(args) < 2 || args[len(args)-2] != "root@backup.example" || !strings.HasPrefix(args[len(args)-1], "/bin/sh -c ") {
		t.Fatalf("unexpected ssh invocation: %v", args)
	}
}

func TestTemporarySSHKeyIsNotRemovedAsOrphan(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("failed to create ssh key dir: %v", err)
	}

	keyPath, err := SaveTemporarySSHKey("  temporary-key-data  ")
	if err != nil {
		t.Fatalf("SaveTemporarySSHKey failed: %v", err)
	}
	if isManagedSSHKeyName(filepath.Base(keyPath)) {
		t.Fatalf("temporary key must not use a managed target key name: %q", keyPath)
	}
	oldStylePath := filepath.Join(SSHKeyDirectory, "target-164690_id")
	if err := os.WriteFile(oldStylePath, []byte("old-temporary-key\n"), 0600); err != nil {
		t.Fatalf("failed to create old-style temporary key: %v", err)
	}

	s := &Service{}
	if err := s.cleanupOrphanTargetSSHKeys(nil); err != nil {
		t.Fatalf("cleanupOrphanTargetSSHKeys failed: %v", err)
	}
	if _, err := os.Stat(oldStylePath); !os.IsNotExist(err) {
		t.Fatalf("expected old-style temporary key to be removed as an orphan, stat err=%v", err)
	}
	content, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("temporary key was removed by orphan cleanup: %v", err)
	}
	if string(content) != "temporary-key-data\n" {
		t.Fatalf("unexpected temporary key content: %q", string(content))
	}

	RemoveTemporarySSHKey(keyPath)
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("expected temporary key to be removed, stat err=%v", err)
	}
}

func TestPrepareBackupTargetValidationCandidateStagesManagedKey(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("create key dir: %v", err)
	}
	canonical := filepath.Join(SSHKeyDirectory, "target-42_id")
	if err := os.WriteFile(canonical, []byte("committed-key\n"), 0600); err != nil {
		t.Fatalf("write committed key: %v", err)
	}
	target := &clusterModels.BackupTarget{
		ID: 42, SSHKey: "replacement-key", SSHHost: "root@backup", BackupRoot: "tank/backups",
	}

	candidate, cleanup, err := prepareBackupTargetValidationCandidate(target)
	if err != nil {
		t.Fatalf("prepare candidate: %v", err)
	}
	if target.SSHKey != "replacement-key" || target.SSHKeyPath != "" {
		t.Fatalf("original candidate mutated: %+v", target)
	}
	if candidate.ID != target.ID || candidate.SSHKey != "" ||
		!strings.HasPrefix(filepath.Base(candidate.SSHKeyPath), ".target-validation-") {
		t.Fatalf("staged candidate: %+v", candidate)
	}
	stagedPath := candidate.SSHKeyPath
	service := &Service{}
	sshArgs := service.buildSSHArgs(candidate)
	committedArgs := service.buildSSHArgs(target)
	foundIdentity := false
	for i := 0; i+1 < len(sshArgs); i++ {
		if sshArgs[i] == "-i" && sshArgs[i+1] == stagedPath {
			foundIdentity = true
			break
		}
	}
	if !foundIdentity {
		t.Fatalf("staged identity omitted from SSH args: %v", sshArgs)
	}
	controlPath := func(args []string) string {
		for _, arg := range args {
			if strings.HasPrefix(arg, "ControlPath=") {
				return arg
			}
		}
		return ""
	}
	if controlPath(sshArgs) == "" || controlPath(sshArgs) == controlPath(committedArgs) {
		t.Fatalf("staged validation reused committed SSH control path: staged=%v committed=%v", sshArgs, committedArgs)
	}
	if content, err := os.ReadFile(stagedPath); err != nil || string(content) != "replacement-key\n" {
		t.Fatalf("staged key content=%q err=%v", string(content), err)
	}
	if info, err := os.Stat(stagedPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("staged key mode=%v err=%v", info, err)
	}
	if content, err := os.ReadFile(canonical); err != nil || string(content) != "committed-key\n" {
		t.Fatalf("committed key changed before commit: content=%q err=%v", string(content), err)
	}

	cleanup()
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged key not removed: %v", err)
	}
	if content, err := os.ReadFile(canonical); err != nil || string(content) != "committed-key\n" {
		t.Fatalf("cleanup changed committed key: content=%q err=%v", string(content), err)
	}
}

func TestPrepareBackupTargetValidationCandidateReportsStagingFailure(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(SSHKeyDirectory, []byte("file"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	_, _, err := prepareBackupTargetValidationCandidate(&clusterModels.BackupTarget{SSHKey: "key"})
	if err == nil || !strings.Contains(err.Error(), "stage_backup_target_ssh_key_failed") {
		t.Fatalf("staging error=%v", err)
	}
}

func TestRemoveSSHKeyRemovesTargetKeyPath(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")

	keyPath := filepath.Join(SSHKeyDirectory, "target-77_id")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		t.Fatalf("failed to create ssh key parent dir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key\n"), 0600); err != nil {
		t.Fatalf("failed to write test key file: %v", err)
	}

	s := &Service{}
	s.RemoveSSHKey(77)

	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("expected key file to be removed, stat err=%v", err)
	}
}

func TestRemoveSSHKeyRemovesAllUnleasedVersions(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("create key dir: %v", err)
	}
	s := &Service{}
	leased := clusterModels.BackupTarget{ID: 77, SSHKey: "old-key"}
	release, err := s.acquireBackupTargetSSHKey(&leased)
	if err != nil {
		t.Fatalf("acquire leased key: %v", err)
	}
	current := clusterModels.BackupTarget{ID: 77, SSHKey: "new-key"}
	if err := s.MaterializeBackupTargetSSHKey(&current); err != nil {
		t.Fatalf("materialize current key: %v", err)
	}
	canonical := filepath.Join(SSHKeyDirectory, "target-77_id")
	if err := ensureSSHKeyFileAtPath(canonical, "legacy-key"); err != nil {
		t.Fatalf("materialize canonical key: %v", err)
	}
	other := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(78, "other-key"))
	if err := ensureSSHKeyFileAtPath(other, "other-key"); err != nil {
		t.Fatalf("materialize other key: %v", err)
	}

	s.RemoveSSHKey(77)
	if _, err := os.Stat(leased.SSHKeyPath); err != nil {
		t.Fatalf("leased version removed: %v", err)
	}
	for _, removed := range []string{current.SSHKeyPath, canonical} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("unleased target version retained path=%s err=%v", removed, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other target key removed: %v", err)
	}
	release()
	s.RemoveSSHKey(77)
	if _, err := os.Stat(leased.SSHKeyPath); !os.IsNotExist(err) {
		t.Fatalf("released version retained: %v", err)
	}
}

func TestEnsureBackupTargetSSHKeyMaterialized(t *testing.T) {
	t.Run("nil target returns error", func(t *testing.T) {
		s := &Service{}
		err := s.ensureBackupTargetSSHKeyMaterialized(nil)
		if err == nil || !strings.Contains(err.Error(), "backup_target_required") {
			t.Fatalf("expected backup_target_required error, got %v", err)
		}
	})

	t.Run("empty key is no-op", func(t *testing.T) {
		target := &clusterModels.BackupTarget{
			ID:         1,
			SSHKeyPath: "   /tmp/some-key-path   ",
			SSHKey:     "   ",
		}

		s := &Service{}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("ensureBackupTargetSSHKeyMaterialized failed: %v", err)
		}

		if target.SSHKeyPath != "/tmp/some-key-path" {
			t.Fatalf("expected trimmed key path to remain, got %q", target.SSHKeyPath)
		}
	})

	t.Run("missing key path derives canonical path without persisting", func(t *testing.T) {
		resetZeltaTestGlobals(t)
		SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
			t.Fatalf("failed to create ssh key dir: %v", err)
		}

		db := newZeltaServiceTestDB(t, &clusterModels.BackupTarget{})
		if err := db.Create(&clusterModels.BackupTarget{
			ID:         7,
			Name:       "target-seven",
			SSHHost:    "user@host",
			SSHPort:    22,
			BackupRoot: "tank/backups-seven",
			Enabled:    true,
		}).Error; err != nil {
			t.Fatalf("failed to seed backup target: %v", err)
		}

		target := &clusterModels.BackupTarget{
			ID:         7,
			SSHKeyPath: "   ",
			SSHKey:     "  private-key-material  ",
		}
		s := &Service{DB: db}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("ensureBackupTargetSSHKeyMaterialized failed: %v", err)
		}

		expectedPath := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(7, "private-key-material"))
		if target.SSHKeyPath != expectedPath {
			t.Fatalf("expected generated key path %q, got %q", expectedPath, target.SSHKeyPath)
		}

		content, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("failed reading generated key path: %v", err)
		}
		if string(content) != "private-key-material\n" {
			t.Fatalf("unexpected generated key content: %q", string(content))
		}
		if err := os.Remove(expectedPath); err != nil {
			t.Fatalf("remove follower-local managed key: %v", err)
		}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("rematerialize missing managed key: %v", err)
		}
		if content, err = os.ReadFile(expectedPath); err != nil || string(content) != "private-key-material\n" {
			t.Fatalf("rematerialized content=%q err=%v", string(content), err)
		}

		var persisted clusterModels.BackupTarget
		if err := db.First(&persisted, 7).Error; err != nil {
			t.Fatalf("failed to fetch persisted target: %v", err)
		}
		if strings.TrimSpace(persisted.SSHKeyPath) != "" {
			t.Fatalf("expected ssh_key_path to remain unpersisted, got %q", persisted.SSHKeyPath)
		}
	})

	t.Run("stale managed key path is ignored in favor of canonical", func(t *testing.T) {
		resetZeltaTestGlobals(t)
		SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
			t.Fatalf("failed to create ssh key dir: %v", err)
		}

		target := &clusterModels.BackupTarget{
			ID:         12345,
			SSHKeyPath: filepath.Join(SSHKeyDirectory, "target-999_id"),
			SSHKey:     "  drifted-key  ",
		}
		s := &Service{}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("ensureBackupTargetSSHKeyMaterialized failed: %v", err)
		}

		expectedPath := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(12345, "drifted-key"))
		if target.SSHKeyPath != expectedPath {
			t.Fatalf("expected canonical key path %q, got %q", expectedPath, target.SSHKeyPath)
		}
		if _, err := os.Stat(expectedPath); err != nil {
			t.Fatalf("expected canonical key file to exist: %v", err)
		}
		if _, err := os.Stat(filepath.Join(SSHKeyDirectory, "target-999_id")); !os.IsNotExist(err) {
			t.Fatalf("expected stale key file not to be written, stat err=%v", err)
		}
	})

	t.Run("managed material ignores an old node-local path", func(t *testing.T) {
		resetZeltaTestGlobals(t)
		SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		oldPath := filepath.Join(t.TempDir(), "keys", "target-explicit")
		target := &clusterModels.BackupTarget{
			ID: 9, SSHKeyPath: oldPath, SSHKey: "  explicit-key  ",
		}

		s := &Service{}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("ensureBackupTargetSSHKeyMaterialized failed: %v", err)
		}
		versioned := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(9, "explicit-key"))
		content, err := os.ReadFile(versioned)
		if err != nil || string(content) != "explicit-key\n" || target.SSHKeyPath != versioned {
			t.Fatalf("versioned managed key path=%q content=%q err=%v", target.SSHKeyPath, string(content), err)
		}
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("old node-local path was written: %v", err)
		}
	})
}

func TestFailedBackupTargetKeyActivationLeavesCommittedVersionComplete(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("create key dir: %v", err)
	}
	s := &Service{}
	oldTarget := clusterModels.BackupTarget{ID: 43, SSHKey: "old-key"}
	if err := s.MaterializeBackupTargetSSHKey(&oldTarget); err != nil {
		t.Fatalf("materialize old key: %v", err)
	}
	newPath := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(43, "new-key"))
	if err := os.Mkdir(newPath, 0700); err != nil {
		t.Fatalf("create activation obstruction: %v", err)
	}
	newTarget := clusterModels.BackupTarget{ID: 43, SSHKey: "new-key"}
	if err := s.MaterializeBackupTargetSSHKey(&newTarget); err == nil || !strings.Contains(err.Error(), "activate_ssh_key") {
		t.Fatalf("activation error=%v", err)
	}
	if content, err := os.ReadFile(oldTarget.SSHKeyPath); err != nil || string(content) != "old-key\n" {
		t.Fatalf("old committed content=%q err=%v", string(content), err)
	}
	if matches, err := filepath.Glob(filepath.Join(SSHKeyDirectory, ".target-key-materialization-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary materialization leak matches=%v err=%v", matches, err)
	}
}

func TestVersionedBackupTargetKeysRemainImmutableWhileLeased(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("create key dir: %v", err)
	}
	s := &Service{}
	oldTarget := clusterModels.BackupTarget{ID: 44, SSHHost: "root@backup", SSHKey: "old-key"}
	releaseOld, err := s.acquireBackupTargetSSHKey(&oldTarget)
	if err != nil {
		t.Fatalf("acquire old key: %v", err)
	}
	newTarget := clusterModels.BackupTarget{ID: 44, SSHHost: "root@backup", SSHKey: "new-key"}
	if err := s.MaterializeBackupTargetSSHKey(&newTarget); err != nil {
		t.Fatalf("materialize new key: %v", err)
	}
	if oldTarget.SSHKeyPath == newTarget.SSHKeyPath {
		t.Fatalf("old and new versions share path %q", oldTarget.SSHKeyPath)
	}
	if sshControlPath(&oldTarget, oldTarget.SSHKeyPath) == sshControlPath(&newTarget, newTarget.SSHKeyPath) {
		t.Fatal("old and new versions share SSH control path")
	}
	if content, err := os.ReadFile(oldTarget.SSHKeyPath); err != nil || string(content) != "old-key\n" {
		t.Fatalf("old content=%q err=%v", string(content), err)
	}
	if content, err := os.ReadFile(newTarget.SSHKeyPath); err != nil || string(content) != "new-key\n" {
		t.Fatalf("new content=%q err=%v", string(content), err)
	}
	if err := s.cleanupOrphanTargetSSHKeys([]clusterModels.BackupTarget{newTarget}); err != nil {
		t.Fatalf("cleanup while leased: %v", err)
	}
	if _, err := os.Stat(oldTarget.SSHKeyPath); err != nil {
		t.Fatalf("leased old version removed: %v", err)
	}
	releaseOld()
	if err := s.cleanupOrphanTargetSSHKeys([]clusterModels.BackupTarget{newTarget}); err != nil {
		t.Fatalf("cleanup after release: %v", err)
	}
	if _, err := os.Stat(oldTarget.SSHKeyPath); !os.IsNotExist(err) {
		t.Fatalf("released old version retained: %v", err)
	}
}

func TestTargetSSHKeyPath(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("failed to create ssh key dir: %v", err)
	}
	s := &Service{}
	canonical := filepath.Join(SSHKeyDirectory, "target-555_id")

	t.Run("empty stored path derives canonical", func(t *testing.T) {
		got, err := s.targetSSHKeyPath(&clusterModels.BackupTarget{ID: 555})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != canonical {
			t.Fatalf("expected %q, got %q", canonical, got)
		}
	})

	t.Run("stale managed in-dir path derives canonical", func(t *testing.T) {
		got, err := s.targetSSHKeyPath(&clusterModels.BackupTarget{
			ID:         555,
			SSHKeyPath: filepath.Join(SSHKeyDirectory, "target-646079_id"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != canonical {
			t.Fatalf("expected canonical %q, got %q", canonical, got)
		}
	})

	t.Run("managed material derives immutable version", func(t *testing.T) {
		got, err := s.targetSSHKeyPath(&clusterModels.BackupTarget{ID: 555, SSHKey: "managed-key"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := filepath.Join(SSHKeyDirectory, managedBackupTargetSSHKeyName(555, "managed-key"))
		if got != expected {
			t.Fatalf("expected version %q, got %q", expected, got)
		}
	})

	t.Run("external out-of-dir path honored", func(t *testing.T) {
		external := filepath.Join(t.TempDir(), "id_ed25519")
		got, err := s.targetSSHKeyPath(&clusterModels.BackupTarget{
			ID:         555,
			SSHKeyPath: external,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != external {
			t.Fatalf("expected external %q, got %q", external, got)
		}
	})

	t.Run("transient target honors stored path", func(t *testing.T) {
		stored := filepath.Join(SSHKeyDirectory, "validate-abc.tmp")
		got, err := s.targetSSHKeyPath(&clusterModels.BackupTarget{
			ID:         0,
			SSHKeyPath: stored,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != stored {
			t.Fatalf("expected stored %q, got %q", stored, got)
		}
	})
}
