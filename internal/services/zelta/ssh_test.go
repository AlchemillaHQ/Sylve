// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
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

		expectedPath := filepath.Join(SSHKeyDirectory, "target-7_id")
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

		expectedPath := filepath.Join(SSHKeyDirectory, "target-12345_id")
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

	t.Run("existing key path writes key to explicit path", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "keys", "target-explicit")
		target := &clusterModels.BackupTarget{
			ID:         9,
			SSHKeyPath: keyPath,
			SSHKey:     "  explicit-key  ",
		}

		s := &Service{}
		if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
			t.Fatalf("ensureBackupTargetSSHKeyMaterialized failed: %v", err)
		}

		content, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("failed to read explicit key path: %v", err)
		}
		if string(content) != "explicit-key\n" {
			t.Fatalf("expected explicit key content with newline, got %q", string(content))
		}
	})
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

func TestParseZFSPoolNameFromDataset(t *testing.T) {
	tests := []struct {
		name    string
		dataset string
		want    string
	}{
		{
			name:    "empty",
			dataset: "",
			want:    "",
		},
		{
			name:    "whitespace",
			dataset: "   ",
			want:    "",
		},
		{
			name:    "pool only",
			dataset: "tank",
			want:    "tank",
		},
		{
			name:    "pool with child dataset",
			dataset: "tank/backups",
			want:    "tank",
		},
		{
			name:    "trimmed pool and dataset",
			dataset: "  tank/backup/root  ",
			want:    "tank",
		},
		{
			name:    "leading slash remains invalid but parsed verbatim",
			dataset: "/broken",
			want:    "/broken",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseZFSPoolNameFromDataset(tc.dataset)
			if got != tc.want {
				t.Fatalf("expected pool %q, got %q", tc.want, got)
			}
		})
	}
}
