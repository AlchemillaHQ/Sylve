// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestGetEncryptionKeyByUUID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.EncryptionKey{})
	s := withFSM(&Service{DB: db})

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
	s := withFSM(&Service{DB: db})

	_, err := s.GetEncryptionKeyByUUID("  ")
	if err == nil {
		t.Fatal("expected error for empty UUID")
	}
}

func TestPublishEncryptionKeyStandalone(t *testing.T) {
	s := newStandaloneTestService(t, &clusterModels.EncryptionKey{})

	if err := s.PublishEncryptionKey(t.Context(), "local-key", "local-key-data-minimum-32-bytes", ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := s.PublishEncryptionKey(t.Context(), "local-key", "updated-local-key-data-32bytes", "passphrase"); err != nil {
		t.Fatalf("republish: %v", err)
	}
	var keys []clusterModels.EncryptionKey
	if err := s.DB.Find(&keys).Error; err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].KeyData != "updated-local-key-data-32bytes" || keys[0].KeyFormat != "passphrase" {
		t.Fatalf("stored keys = %+v", keys)
	}
	if err := s.PublishEncryptionKey(t.Context(), "  ", "some-data-32bytes-long-enough", "passphrase"); err == nil {
		t.Fatal("expected an error for an empty UUID")
	}
}

func TestPublishEncryptionKeyNeedsRaftWhenClustered(t *testing.T) {
	s := newStandaloneTestService(t, &clusterModels.Cluster{}, &clusterModels.EncryptionKey{})
	if err := s.DB.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	err := s.PublishEncryptionKey(t.Context(), "test-uuid", "data-32-bytes-minimum-for-key", "passphrase")
	if !errors.Is(err, raft.ErrRaftShutdown) {
		t.Fatalf("publish without Raft = %v, want cluster_consensus_unavailable", err)
	}
	var count int64
	if err := s.DB.Model(&clusterModels.EncryptionKey{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("clustered node wrote the key locally: count=%d err=%v", count, err)
	}
}
