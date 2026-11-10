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

func TestFSMDispatcherEncryptionKeyDeleteLeavesTombstone(t *testing.T) {
	db := newClusterModelTestDB(t, &EncryptionKey{}, &EncryptionKeyTombstone{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	for _, key := range []EncryptionKey{
		{UUID: "zroot_sylve", KeyData: "copied-user-key", KeyFormat: "passphrase"},
		{UUID: "changed", KeyData: "current-key", KeyFormat: "passphrase"},
	} {
		if err := db.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
	}
	remove := func(uuid, keyData string) {
		t.Helper()
		data, _ := json.Marshal(EncryptionKeyRemoval{UUID: uuid, KeyHash: EncryptionKeyHash(keyData)})
		if err := applyFSMCommand(t, fsm, Command{Type: "encryption_key", Action: "delete", Data: data}); err != nil {
			t.Fatalf("delete %s: %v", uuid, err)
		}
	}
	remove("zroot_sylve", "copied-user-key")
	remove("changed", "the-key-it-had-before")
	remove("missing", "anything")

	var keys []EncryptionKey
	if err := db.Order("uuid").Find(&keys).Error; err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].UUID != "changed" {
		t.Fatalf("keys after delete = %+v, want only the changed key", keys)
	}
	var tombstones []EncryptionKeyTombstone
	if err := db.Find(&tombstones).Error; err != nil {
		t.Fatal(err)
	}
	if len(tombstones) != 1 || tombstones[0].UUID != "zroot_sylve" ||
		tombstones[0].KeyHash != EncryptionKeyHash("copied-user-key") {
		t.Fatalf("tombstones = %+v, want one for zroot_sylve", tombstones)
	}
}
