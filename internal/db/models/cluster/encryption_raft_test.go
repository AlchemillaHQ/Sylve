// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"testing"
)

func TestFSMDispatcherEncryptionKeyUpsert(t *testing.T) {
	db := newClusterModelTestDB(t, &EncryptionKey{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	key := EncryptionKey{
		UUID:      "abc-def-ghijkl",
		KeyData:   "test-passphrase-minimum-32-bytes-long",
		KeyFormat: "passphrase",
	}

	data, _ := json.Marshal(key)
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "upsert",
		Data:   data,
	}); err != nil {
		t.Fatalf("encryption key upsert apply failed: %v", err)
	}

	var stored EncryptionKey
	if err := db.Where("uuid = ?", "abc-def-ghijkl").First(&stored).Error; err != nil {
		t.Fatalf("failed to fetch upserted encryption key: %v", err)
	}
	if stored.KeyData != "test-passphrase-minimum-32-bytes-long" {
		t.Fatalf("stored key data mismatch: got %q", stored.KeyData)
	}
	if stored.KeyFormat != "passphrase" {
		t.Fatalf("stored key format mismatch: got %q", stored.KeyFormat)
	}
}

func TestFSMDispatcherEncryptionKeyUpsertUpdatesExisting(t *testing.T) {
	db := newClusterModelTestDB(t, &EncryptionKey{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	key1 := EncryptionKey{
		UUID:      "key-to-update",
		KeyData:   "first-passphrase-value-for-testing",
		KeyFormat: "passphrase",
	}
	data1, _ := json.Marshal(key1)
	applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "upsert",
		Data:   data1,
	})

	key2 := EncryptionKey{
		UUID:      "key-to-update",
		KeyData:   "updated-passphrase-value-32bytes",
		KeyFormat: "passphrase",
	}
	data2, _ := json.Marshal(key2)
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "upsert",
		Data:   data2,
	}); err != nil {
		t.Fatalf("encryption key re-upsert apply failed: %v", err)
	}

	var count int64
	if err := db.Model(&EncryptionKey{}).Where("uuid = ?", "key-to-update").Count(&count).Error; err != nil {
		t.Fatalf("failed to count encryption keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after re-upsert, got %d", count)
	}

	var stored EncryptionKey
	db.Where("uuid = ?", "key-to-update").First(&stored)
	if stored.KeyData != "updated-passphrase-value-32bytes" {
		t.Fatalf("expected updated key data, got %q", stored.KeyData)
	}
}

func TestFSMDispatcherEncryptionKeyDelete(t *testing.T) {
	db := newClusterModelTestDB(t, &EncryptionKey{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	key := EncryptionKey{
		UUID:      "delete-me-key",
		KeyData:   "to-be-deleted-passphrase-32bytes",
		KeyFormat: "passphrase",
	}
	data, _ := json.Marshal(key)
	applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "upsert",
		Data:   data,
	})

	deletePayload, _ := json.Marshal(struct {
		UUID string `json:"uuid"`
	}{UUID: "delete-me-key"})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "delete",
		Data:   deletePayload,
	}); err != nil {
		t.Fatalf("encryption key delete apply failed: %v", err)
	}

	var count int64
	if err := db.Model(&EncryptionKey{}).Where("uuid = ?", "delete-me-key").Count(&count).Error; err != nil {
		t.Fatalf("failed to count encryption keys after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", count)
	}
}

func TestFSMDispatcherEncryptionKeyDeleteEmptyUUID(t *testing.T) {
	db := newClusterModelTestDB(t, &EncryptionKey{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	deletePayload, _ := json.Marshal(struct {
		UUID string `json:"uuid"`
	}{UUID: "  "})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "encryption_key",
		Action: "delete",
		Data:   deletePayload,
	}); err != nil {
		t.Fatalf("encryption key delete with empty uuid should be no-op, got: %v", err)
	}
}
