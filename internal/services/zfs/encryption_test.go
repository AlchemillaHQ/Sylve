// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestIsEncryptionRequested(t *testing.T) {
	tests := []struct {
		name     string
		props    map[string]string
		expected bool
	}{
		{"nil map", nil, false},
		{"empty map", map[string]string{}, false},
		{"encryption off", map[string]string{"encryption": "off"}, false},
		{"encryption empty", map[string]string{"encryption": " "}, false},
		{"encryption missing", map[string]string{"compression": "lz4"}, false},
		{"encryption on", map[string]string{"encryption": "on"}, true},
		{"encryption aes-256-gcm", map[string]string{"encryption": "aes-256-gcm"}, true},
		{"encryption aes-128-ccm", map[string]string{"encryption": "aes-128-ccm"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEncryptionRequested(tt.props)
			if result != tt.expected {
				t.Errorf("isEncryptionRequested(%v) = %v, want %v", tt.props, result, tt.expected)
			}
		})
	}
}

func TestEncryptionKeyCreatedHook(t *testing.T) {
	keyDir := t.TempDir()
	var called bool
	var hookedUUID, hookedData, hookedFormat string

	origHook := EncryptionKeyCreatedHook
	EncryptionKeyCreatedHook = func(uuid, keyData, keyFormat string) {
		called = true
		hookedUUID = uuid
		hookedData = keyData
		hookedFormat = keyFormat
	}
	t.Cleanup(func() { EncryptionKeyCreatedHook = origHook })

	t.Run("hook nil skips registration", func(t *testing.T) {
		EncryptionKeyCreatedHook = nil
		EncryptionKeyCreatedHook = func(uuid, keyData, keyFormat string) {
			called = true
			hookedUUID = uuid
			hookedData = keyData
			hookedFormat = keyFormat
		}
	})

	t.Run("hook called with correct data", func(t *testing.T) {
		called = false
		uuid := "test-hook-uuid-12345"
		keyData := "test-hook-passphrase-minimum-32-bytes"
		keyPath := filepath.Join(keyDir, uuid)
		if err := os.WriteFile(keyPath, []byte(keyData), 0600); err != nil {
			t.Fatalf("failed to write test key file: %v", err)
		}

		EncryptionKeyCreatedHook(uuid, keyData, "passphrase")

		if !called {
			t.Fatal("hook was not called")
		}
		if hookedUUID != uuid {
			t.Errorf("expected uuid %q, got %q", uuid, hookedUUID)
		}
		if hookedData != keyData {
			t.Errorf("expected key data %q, got %q", keyData, hookedData)
		}
		if hookedFormat != "passphrase" {
			t.Errorf("expected key format passphrase, got %q", hookedFormat)
		}
	})
}

func TestEncryptionKeyDeletedHook(t *testing.T) {
	var called bool
	var deletedUUID string

	origHook := EncryptionKeyDeletedHook
	EncryptionKeyDeletedHook = func(uuid string) {
		called = true
		deletedUUID = uuid
	}
	t.Cleanup(func() { EncryptionKeyDeletedHook = origHook })

	t.Run("hook nil is safe", func(t *testing.T) {
		called = false
		saved := EncryptionKeyDeletedHook
		EncryptionKeyDeletedHook = nil
		cleanupEncryptionKeyForDataset(&gzfs.Dataset{
			Name: "tank/plain",
			Properties: map[string]gzfs.ZFSProperty{
				"keylocation": {Value: "none"},
			},
		})
		EncryptionKeyDeletedHook = saved
	})

	t.Run("hook called", func(t *testing.T) {
		called = false
		EncryptionKeyDeletedHook("delete-me-uuid")
		if !called {
			t.Fatal("hook was not called")
		}
		if deletedUUID != "delete-me-uuid" {
			t.Errorf("expected uuid 'delete-me-uuid', got %q", deletedUUID)
		}
	})
}

func TestExtractUUIDFromDataset(t *testing.T) {
	t.Run("encrypted with file keylocation", func(t *testing.T) {
		ds := &gzfs.Dataset{
			Name: "tank/enc",
			Properties: map[string]gzfs.ZFSProperty{
				"keylocation": {Value: "file:///etc/zfs/keys/abc-def-ghi"},
			},
		}
		uuid := extractUUIDFromDataset(ds)
		if uuid != "abc-def-ghi" {
			t.Errorf("expected uuid 'abc-def-ghi', got %q", uuid)
		}
	})

	t.Run("unencrypted returns empty", func(t *testing.T) {
		ds := &gzfs.Dataset{
			Name: "tank/plain",
			Properties: map[string]gzfs.ZFSProperty{
				"keylocation": {Value: "none"},
			},
		}
		uuid := extractUUIDFromDataset(ds)
		if uuid != "" {
			t.Errorf("expected empty uuid, got %q", uuid)
		}
	})

	t.Run("prompt keylocation returns empty", func(t *testing.T) {
		ds := &gzfs.Dataset{
			Name: "tank/prompt",
			Properties: map[string]gzfs.ZFSProperty{
				"keylocation": {Value: "prompt"},
			},
		}
		uuid := extractUUIDFromDataset(ds)
		if uuid != "" {
			t.Errorf("expected empty uuid, got %q", uuid)
		}
	})
}
