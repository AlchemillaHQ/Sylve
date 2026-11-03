// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestGetEncryptionKeyByUUID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db}

	if err := db.Create(&clusterModels.EncryptionKey{
		UUID:      "find-me-uuid",
		KeyData:   "look-me-up-passphrase-32bytes",
		KeyFormat: "passphrase",
	}).Error; err != nil {
		t.Fatalf("failed to seed encryption key: %v", err)
	}

	key, err := s.GetEncryptionKeyByUUID("find-me-uuid")
	if err != nil {
		t.Fatalf("GetEncryptionKeyByUUID failed: %v", err)
	}
	if key.KeyData != "look-me-up-passphrase-32bytes" {
		t.Fatalf("unexpected key data: %q", key.KeyData)
	}
	if key.KeyFormat != "passphrase" {
		t.Fatalf("unexpected key format: %q", key.KeyFormat)
	}

	_, err = s.GetEncryptionKeyByUUID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestGetEncryptionKeyByUUIDEmpty(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db}

	_, err := s.GetEncryptionKeyByUUID("  ")
	if err == nil {
		t.Fatal("expected error for empty UUID")
	}
}

func TestUpsertEncryptionKeyLocally(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db}

	t.Run("insert", func(t *testing.T) {
		if err := s.UpsertEncryptionKeyLocally("local-key", "local-key-data-minimum-32-bytes", "passphrase"); err != nil {
			t.Fatalf("UpsertEncryptionKeyLocally failed: %v", err)
		}

		var stored clusterModels.EncryptionKey
		if err := db.Where("uuid = ?", "local-key").First(&stored).Error; err != nil {
			t.Fatalf("failed to fetch locally upserted key: %v", err)
		}
		if stored.KeyData != "local-key-data-minimum-32-bytes" {
			t.Fatalf("unexpected key data: %q", stored.KeyData)
		}
	})

	t.Run("re-upsert updates", func(t *testing.T) {
		if err := s.UpsertEncryptionKeyLocally("local-key", "updated-local-key-data-32bytes", "passphrase"); err != nil {
			t.Fatalf("UpsertEncryptionKeyLocally re-upsert failed: %v", err)
		}

		var count int64
		db.Model(&clusterModels.EncryptionKey{}).Where("uuid = ?", "local-key").Count(&count)
		if count != 1 {
			t.Fatalf("expected 1 row after re-upsert, got %d", count)
		}

		var stored clusterModels.EncryptionKey
		db.Where("uuid = ?", "local-key").First(&stored)
		if stored.KeyData != "updated-local-key-data-32bytes" {
			t.Fatalf("expected updated key data, got %q", stored.KeyData)
		}
	})
}

func TestUpsertEncryptionKeyLocallyEmptyUUID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db}

	if err := s.UpsertEncryptionKeyLocally("  ", "some-data-32bytes-long-enough", "passphrase"); err == nil {
		t.Fatal("expected error for empty UUID")
	}
}

func TestProposeEncryptionKeyUpsertBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db}

	if err := s.ProposeEncryptionKeyUpsert("bypass-key", "bypass-key-data-minimum-32-bytes", "passphrase", true); err != nil {
		t.Fatalf("ProposeEncryptionKeyUpsert bypass failed: %v", err)
	}

	var stored clusterModels.EncryptionKey
	if err := db.Where("uuid = ?", "bypass-key").First(&stored).Error; err != nil {
		t.Fatalf("failed to fetch bypass-created key: %v", err)
	}
	if stored.KeyData != "bypass-key-data-minimum-32-bytes" {
		t.Fatalf("unexpected key data: %q", stored.KeyData)
	}
}

func TestForwardEncryptionKeyToLeaderRaftNil(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := &Service{DB: db, Raft: nil}

	err := s.ForwardEncryptionKeyToLeader("test-uuid", "data-32-bytes-minimum-for-key", "passphrase")
	if err != nil {
		t.Fatal("expected local upsert when Raft is nil (bypass), got error:", err)
	}

	var key clusterModels.EncryptionKey
	if err := db.Where("uuid = ?", "test-uuid").First(&key).Error; err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
}
