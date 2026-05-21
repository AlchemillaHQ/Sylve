// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
)

// EncryptionKeyCreatedHook is called after a new encrypted dataset is created.
// The UUID is the key file name (deterministic UUID from dataset name + passphrase),
// keyData is the passphrase content, and keyFormat is the encryption key format.
var EncryptionKeyCreatedHook func(uuid, keyData, keyFormat string)

func registerEncryptionKey(ctx context.Context, ds *gzfs.Dataset) error {
	if EncryptionKeyCreatedHook == nil {
		return nil
	}

	keylocProp, err := ds.GetProperty(ctx, "keylocation")
	if err != nil {
		return fmt.Errorf("get_keylocation_failed: %w", err)
	}

	keyloc := strings.TrimSpace(keylocProp.Value)
	if keyloc == "" || keyloc == "none" || !strings.HasPrefix(keyloc, "file://") {
		return fmt.Errorf("unexpected_keylocation: %s", keyloc)
	}

	keyPath := strings.TrimPrefix(keyloc, "file://")
	uuid := filepath.Base(keyPath)

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read_key_file_failed: %w", err)
	}

	keyfmtProp, err := ds.GetProperty(ctx, "keyformat")
	keyFormat := "passphrase"
	if err == nil {
		keyFormat = strings.TrimSpace(keyfmtProp.Value)
		if keyFormat == "" || keyFormat == "none" {
			keyFormat = "passphrase"
		}
	}

	EncryptionKeyCreatedHook(uuid, string(keyData), keyFormat)
	return nil
}

// extractUUIDFromDataset returns the UUID from the dataset's keylocation property.
// Returns empty string if the dataset is not encrypted or keylocation is unexpected.
func extractUUIDFromDataset(ds *gzfs.Dataset) string {
	if ds == nil || ds.Properties == nil {
		return ""
	}

	prop, ok := ds.Properties["keylocation"]
	if !ok {
		return ""
	}

	keyloc := strings.TrimSpace(prop.Value)
	if keyloc == "" || keyloc == "none" || !strings.HasPrefix(keyloc, "file://") {
		return ""
	}

	return filepath.Base(strings.TrimPrefix(keyloc, "file://"))
}

func cleanupEncryptionKeyForDataset(ds *gzfs.Dataset) {
	uuid := extractUUIDFromDataset(ds)
	if uuid == "" {
		return
	}

	keyPath := filepath.Join("/etc/zfs/keys", uuid)
	_ = os.Remove(keyPath)

	// Intentionally do NOT delete from the cluster key store.
	// The key may still be needed for future restores from backups.
}

func isEncryptionRequested(props map[string]string) bool {
	enc, ok := props["encryption"]
	if !ok {
		return false
	}
	v := strings.TrimSpace(enc)
	return v != "" && v != "off"
}
