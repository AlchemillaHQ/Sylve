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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/logger"
)

var EncryptionKeyDirectory = "/etc/zfs/keys"

func (s *Service) ReconcileEncryptionKeys() error {
	keys, err := s.Cluster.ListEncryptionKeys()
	if err != nil {
		return fmt.Errorf("list_encryption_keys_for_reconcile_failed: %w", err)
	}

	var materialized int
	for _, key := range keys {
		keyPath := filepath.Join(EncryptionKeyDirectory, key.UUID)
		if _, statErr := os.Stat(keyPath); statErr == nil {
			continue
		}

		if err := os.WriteFile(keyPath, []byte(key.KeyData), 0600); err != nil {
			logger.L.Warn().Err(err).Str("uuid", key.UUID).Msg("reconcile_encryption_key_write_failed")
			continue
		}
		materialized++
	}

	if materialized > 0 {
		logger.L.Info().Int("count", materialized).Msg("reconciled_encryption_keys")
	}

	return nil
}

func (s *Service) AutoDiscoverAndRegisterKeys(ctx context.Context) {
	datasets, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		logger.L.Warn().Err(err).Msg("auto_discover_encryption_keys_list_failed")
		return
	}

	for _, ds := range datasets {
		if !ds.IsEncrypted() {
			continue
		}

		keylocProp, err := ds.GetProperty(ctx, "keylocation")
		if err != nil {
			logger.L.Warn().Err(err).Str("dataset", ds.Name).Msg("auto_discover_get_keylocation_failed")
			continue
		}

		keyloc := strings.TrimSpace(keylocProp.Value)

		// Inherited encryption: child shares parent's key, which is already
		// registered under the encryption root. Nothing to do.
		if keyloc == "" || keyloc == "none" {
			continue
		}

		if !strings.HasPrefix(keyloc, "file://") {
			// "prompt" is expected for encrypted datasets received via
			// zfs send -w (e.g. backup targets). Try the cluster key
			// store — if a match is found the keylocation is upgraded
			// to file:// automatically.
			if keyloc == "prompt" {
				if _, err := s.ensureEncryptionKeyForDataset(ctx, ds); err != nil {
					logger.L.Debug().Str("dataset", ds.Name).Str("keylocation", keyloc).
						Msg("auto_discover_prompt_key_not_in_cluster")
				}
				continue
			}

			logger.L.Warn().Str("dataset", ds.Name).Str("keylocation", keyloc).
				Msg("auto_discover_unexpected_keylocation")
			continue
		}

		keyPath := strings.TrimPrefix(keyloc, "file://")
		uuid := filepath.Base(keyPath)

		keyData, readErr := os.ReadFile(keyPath)
		if readErr != nil {
			logger.L.Warn().Err(readErr).Str("dataset", ds.Name).Str("keyPath", keyPath).
				Msg("auto_discover_read_key_file_failed")
			continue
		}

		keyfmtProp, err := ds.GetProperty(ctx, "keyformat")
		keyFormat := "passphrase"
		if err == nil {
			keyFormat = strings.TrimSpace(keyfmtProp.Value)
			if keyFormat == "" || keyFormat == "none" {
				keyFormat = "passphrase"
			}
		}

		if err := s.Cluster.ForwardEncryptionKeyToLeader(uuid, string(keyData), keyFormat); err != nil {
			if strings.Contains(err.Error(), "leader_unknown") {
				logger.L.Debug().Str("uuid", uuid).Msg("auto_discover_skip_no_leader")
			} else {
				logger.L.Warn().Err(err).Str("uuid", uuid).Msg("auto_discover_forward_key_failed")
			}
		}
	}
}

func (s *Service) EnsureEncryptionKeyFile(uuid string) error {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return fmt.Errorf("encryption_key_uuid_required")
	}

	keyPath := filepath.Join(EncryptionKeyDirectory, uuid)
	if _, err := os.Stat(keyPath); err == nil {
		return nil
	}

	key, err := s.Cluster.GetEncryptionKeyByUUID(uuid)
	if err != nil {
		return fmt.Errorf("encryption_key_not_found_in_cluster_store: %s", uuid)
	}

	if err := os.WriteFile(keyPath, []byte(key.KeyData), 0600); err != nil {
		return fmt.Errorf("write_encryption_key_file_failed: %w", err)
	}

	return nil
}

func (s *Service) ensureEncryptionKeyForDataset(ctx context.Context, ds *gzfs.Dataset) (keyLoaded bool, err error) {
	keylocProp, err := ds.GetProperty(ctx, "keylocation")
	if err != nil {
		return false, fmt.Errorf("get_keylocation_failed: %w", err)
	}

	keyloc := strings.TrimSpace(keylocProp.Value)
	if keyloc == "" || keyloc == "none" {
		return false, nil
	}

	if strings.HasPrefix(keyloc, "file://") {
		uuid := filepath.Base(strings.TrimPrefix(keyloc, "file://"))
		if err := s.EnsureEncryptionKeyFile(uuid); err != nil {
			return false, fmt.Errorf("encryption_key_not_found_in_cluster_store: %s", uuid)
		}
		if err := ds.LoadKey(ctx, true); err != nil {
			return false, fmt.Errorf("load_key_failed: %w", err)
		}
		return true, nil
	}

	// keylocation is "prompt" — the original key file wasn't available on
	// the server that received this dataset (e.g. a backup target). Try each
	// key in the cluster store until one loads successfully.
	keys, listErr := s.Cluster.ListEncryptionKeys()
	if listErr != nil {
		logger.L.Warn().Err(listErr).Str("dataset", ds.Name).
			Msg("prompt_keylocation_cannot_list_keys")
		return false, nil
	}

	for _, key := range keys {
		if err := ds.LoadKeyWithPassphrase(ctx, key.KeyData, true); err != nil {
			continue
		}

		if writeErr := s.EnsureEncryptionKeyFile(key.UUID); writeErr != nil {
			logger.L.Warn().Err(writeErr).Str("uuid", key.UUID).
				Str("dataset", ds.Name).Msg("prompt_key_loaded_but_file_write_failed")
		} else {
			_ = ds.SetProperties(ctx, "keylocation", "file://"+filepath.Join(EncryptionKeyDirectory, key.UUID))
		}

		logger.L.Info().Str("dataset", ds.Name).Str("uuid", key.UUID).
			Msg("encrypted_dataset_key_loaded_via_prompt_fallback")
		return true, nil
	}

	logger.L.Warn().Str("dataset", ds.Name).
		Msg("encrypted_dataset_requires_manual_key_load_run_zfs_load_key")
	return false, nil
}
