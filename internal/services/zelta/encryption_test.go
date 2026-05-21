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
	"github.com/alchemillahq/sylve/internal/services/cluster"
)

func setEncryptionKeyDir(t *testing.T, dir string) {
	orig := EncryptionKeyDirectory
	EncryptionKeyDirectory = dir
	t.Cleanup(func() { EncryptionKeyDirectory = orig })
}

func TestReconcileEncryptionKeys(t *testing.T) {
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)

	db := newZeltaServiceTestDB(t, &clusterModels.EncryptionKey{})
	clusterSvc := &cluster.Service{DB: db}
	s := &Service{Cluster: clusterSvc}

	t.Run("empty key store succeeds", func(t *testing.T) {
		if err := s.ReconcileEncryptionKeys(); err != nil {
			t.Fatalf("ReconcileEncryptionKeys on empty keys failed: %v", err)
		}
	})

	t.Run("materializes missing key file", func(t *testing.T) {
		clusterSvc.UpsertEncryptionKeyLocally("reconcile-uuid", "reconcile-key-data-32bytes-ok", "passphrase")

		keyPath := filepath.Join(keyDir, "reconcile-uuid")
		os.Remove(keyPath)

		if err := s.ReconcileEncryptionKeys(); err != nil {
			t.Fatalf("ReconcileEncryptionKeys failed: %v", err)
		}

		data, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("key file not materialized: %v", err)
		}
		if string(data) != "reconcile-key-data-32bytes-ok" {
			t.Fatalf("key file content mismatch: %q", string(data))
		}
	})

	t.Run("skips existing key file", func(t *testing.T) {
		clusterSvc.UpsertEncryptionKeyLocally("skip-uuid", "skip-key-data-32bytes-longer", "passphrase")

		keyPath := filepath.Join(keyDir, "skip-uuid")
		os.WriteFile(keyPath, []byte("existing-content-32bytes-that"), 0600)

		if err := s.ReconcileEncryptionKeys(); err != nil {
			t.Fatalf("ReconcileEncryptionKeys failed: %v", err)
		}

		data, _ := os.ReadFile(keyPath)
		if string(data) != "existing-content-32bytes-that" {
			t.Fatalf("existing key file was overwritten: %q", string(data))
		}
	})
}

func TestEnsureEncryptionKeyFile(t *testing.T) {
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)

	db := newZeltaServiceTestDB(t, &clusterModels.EncryptionKey{})
	clusterSvc := &cluster.Service{DB: db}
	s := &Service{Cluster: clusterSvc}

	t.Run("empty uuid errors", func(t *testing.T) {
		if err := s.EnsureEncryptionKeyFile("  "); err == nil {
			t.Fatal("expected error for empty UUID")
		}
	})

	t.Run("key not found in store", func(t *testing.T) {
		err := s.EnsureEncryptionKeyFile("nonexistent-uuid")
		if err == nil {
			t.Fatal("expected error for nonexistent key")
		}
		if !strings.Contains(err.Error(), "encryption_key_not_found_in_cluster_store") {
			t.Fatalf("expected not_found error, got: %v", err)
		}
	})

	t.Run("materializes missing key file", func(t *testing.T) {
		clusterSvc.UpsertEncryptionKeyLocally("ensure-me-uuid", "ensure-key-data-32bytes-long", "passphrase")

		keyPath := filepath.Join(keyDir, "ensure-me-uuid")
		os.Remove(keyPath)

		if err := s.EnsureEncryptionKeyFile("ensure-me-uuid"); err != nil {
			t.Fatalf("EnsureEncryptionKeyFile failed: %v", err)
		}

		data, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("failed to read written key file: %v", err)
		}
		if string(data) != "ensure-key-data-32bytes-long" {
			t.Fatalf("key file content mismatch: %q", string(data))
		}
	})

	t.Run("already existing file is no-op", func(t *testing.T) {
		clusterSvc.UpsertEncryptionKeyLocally("exists-uuid", "already-there-key-data-32b", "passphrase")

		keyPath := filepath.Join(keyDir, "exists-uuid")
		os.WriteFile(keyPath, []byte("already-there-key-data-32b"), 0600)

		if err := s.EnsureEncryptionKeyFile("exists-uuid"); err != nil {
			t.Fatalf("EnsureEncryptionKeyFile failed for existing file: %v", err)
		}
	})
}
