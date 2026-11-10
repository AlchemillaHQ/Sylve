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
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

var EncryptionKeyDirectory = "/etc/zfs/keys"

var sylveKeyNamePattern = regexp.MustCompile(
	`^(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|restore-[0-9a-f]{24})$`,
)

func sylveKeyName(name string) bool {
	return sylveKeyNamePattern.MatchString(name)
}

func sylveKeyFile(keylocation string) (string, bool) {
	path, ok := strings.CutPrefix(strings.TrimSpace(keylocation), "file://")
	if !ok || filepath.Dir(path) != EncryptionKeyDirectory || !sylveKeyName(filepath.Base(path)) {
		return "", false
	}
	return filepath.Base(path), true
}

func writeKeyFileIfMissing(path, data string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = file.WriteString(data)
	return errors.Join(err, file.Close())
}

func (s *Service) ReconcileEncryptionKeys() error {
	keys, err := s.Cluster.ListEncryptionKeys()
	if err != nil {
		return fmt.Errorf("list_encryption_keys_for_reconcile_failed: %w", err)
	}

	var materialized int
	for _, key := range keys {
		if !sylveKeyName(key.UUID) {
			continue
		}
		keyPath := filepath.Join(EncryptionKeyDirectory, key.UUID)
		existing, err := os.ReadFile(keyPath)
		if err == nil {
			if string(existing) != key.KeyData {
				logger.L.Warn().Str("path", keyPath).Msg("encryption_key_file_differs_from_cluster_key")
			}
			continue
		}
		if err := writeKeyFileIfMissing(keyPath, key.KeyData); err != nil {
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
	copies := s.copiedEncryptionKeys()

	for _, ds := range datasets {
		if !ds.IsEncrypted() {
			continue
		}

		keylocProp, err := ds.GetProperty(ctx, "keylocation")
		if err != nil {
			logger.L.Warn().Err(err).Str("dataset", ds.Name).Msg("auto_discover_get_keylocation_failed")
			continue
		}
		uuid, ok := sylveKeyFile(keylocProp.Value)
		if !ok {
			s.dropCopiedEncryptionKey(ctx, keylocProp.Value, copies)
			continue
		}

		keyPath := filepath.Join(EncryptionKeyDirectory, uuid)
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

		if err := s.Cluster.PublishEncryptionKey(ctx, uuid, string(keyData), keyFormat); err != nil {
			if errors.Is(err, raft.ErrRaftShutdown) {
				logger.L.Debug().Str("uuid", uuid).Msg("auto_discover_skip_no_leader")
			} else {
				logger.L.Warn().Err(err).Str("uuid", uuid).Msg("auto_discover_forward_key_failed")
			}
		}
	}
}

func (s *Service) copiedEncryptionKeys() map[string]string {
	keys, err := s.Cluster.ListEncryptionKeys()
	if err != nil {
		logger.L.Warn().Err(err).Msg("list_encryption_keys_failed")
		return nil
	}
	copies := make(map[string]string)
	for _, key := range keys {
		if !sylveKeyName(key.UUID) {
			copies[key.UUID] = key.KeyData
		}
	}
	return copies
}

func (s *Service) dropCopiedEncryptionKey(ctx context.Context, keylocation string, copies map[string]string) {
	path, ok := strings.CutPrefix(strings.TrimSpace(keylocation), "file://")
	if !ok || filepath.Dir(path) == EncryptionKeyDirectory {
		return
	}
	name := filepath.Base(path)
	keyData, copied := copies[name]
	if !copied {
		return
	}
	if original, err := os.ReadFile(path); err != nil || string(original) != keyData {
		return
	}
	delete(copies, name)
	if err := s.Cluster.DeleteEncryptionKey(ctx, name, clusterModels.EncryptionKeyHash(keyData)); err != nil {
		logger.L.Debug().Err(err).Str("uuid", name).Msg("drop_copied_encryption_key_deferred")
		return
	}
	logger.L.Info().Str("uuid", name).Msg("copied_encryption_key_dropped")
}

func (s *Service) RemoveCopiedKeyFiles(ctx context.Context) {
	var tombstones []clusterModels.EncryptionKeyTombstone
	if err := s.DB.Find(&tombstones).Error; err != nil || len(tombstones) == 0 {
		return
	}
	datasets, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		logger.L.Warn().Err(err).Msg("copied_encryption_key_dataset_list_failed")
		return
	}
	for _, tombstone := range tombstones {
		path := filepath.Join(EncryptionKeyDirectory, tombstone.UUID)
		data, err := os.ReadFile(path)
		if err == nil && clusterModels.EncryptionKeyHash(string(data)) == tombstone.KeyHash {
			if !s.releaseKeyFile(ctx, datasets, path) {
				continue
			}
			if err := os.Remove(path); err != nil {
				logger.L.Warn().Err(err).Str("path", path).Msg("copied_encryption_key_file_remove_failed")
				continue
			}
			logger.L.Info().Str("path", path).Msg("copied_encryption_key_file_removed")
		}
		if err := s.DB.Delete(&tombstone).Error; err != nil {
			logger.L.Warn().Err(err).Str("uuid", tombstone.UUID).Msg("encryption_key_tombstone_delete_failed")
		}
	}
}

func (s *Service) releaseKeyFile(ctx context.Context, datasets []*gzfs.Dataset, path string) bool {
	free := true
	for _, ds := range datasets {
		if !ds.IsEncrypted() {
			continue
		}
		prop, err := ds.GetProperty(ctx, "keylocation")
		if err != nil || strings.TrimSpace(prop.Value) != "file://"+path {
			continue
		}
		if loaded, err := s.ensureEncryptionKeyForDataset(ctx, ds); err == nil && loaded && s.sameKeyAsParent(ctx, ds.Name, path) {
			if _, err := utils.RunCommandWithContext(ctx, "zfs", "change-key", "-i", ds.Name); err == nil {
				logger.L.Info().Str("dataset", ds.Name).Msg("dataset_moved_to_parent_encryption_key")
				continue
			}
		}
		logger.L.Warn().Str("dataset", ds.Name).Str("path", path).Msg("copied_encryption_key_file_still_in_use")
		free = false
	}
	return free
}

func (s *Service) EnsureEncryptionKeyFile(uuid string) error {
	uuid = strings.TrimSpace(uuid)
	if !sylveKeyName(uuid) {
		return fmt.Errorf("encryption_key_name_invalid: %q", uuid)
	}

	keyPath := filepath.Join(EncryptionKeyDirectory, uuid)
	if _, err := os.Stat(keyPath); err == nil {
		return nil
	}

	key, err := s.Cluster.GetEncryptionKeyByUUID(uuid)
	if err != nil {
		return fmt.Errorf("encryption_key_not_found_in_cluster_store: %s", uuid)
	}

	if err := writeKeyFileIfMissing(keyPath, key.KeyData); err != nil {
		return fmt.Errorf("write_encryption_key_file_failed: %w", err)
	}

	return nil
}

func (s *Service) ensureEncryptionKeyForDataset(ctx context.Context, ds *gzfs.Dataset) (keyLoaded bool, err error) {
	if ds == nil {
		return false, fmt.Errorf("encrypted_dataset_required")
	}

	encProps, err := ds.GetEncryptionProperties(ctx)
	if err != nil {
		return false, fmt.Errorf("get_encryption_properties_failed: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(encProps.KeyStatus), "available") {
		return true, nil
	}

	encryptionRoot := strings.TrimSpace(encProps.EncryptionRoot)
	if encryptionRoot != "" && encryptionRoot != "-" && encryptionRoot != ds.Name {
		root, rootErr := s.getLocalDataset(ctx, encryptionRoot)
		if rootErr != nil {
			return false, fmt.Errorf("get_encryption_root_%s_failed: %w", encryptionRoot, rootErr)
		}
		if root == nil {
			return false, fmt.Errorf("encryption_root_not_found: %s", encryptionRoot)
		}
		return s.ensureEncryptionKeyForDataset(ctx, root)
	}

	keyloc := strings.TrimSpace(encProps.KeyLocation)
	if !strings.HasPrefix(keyloc, "file://") {
		return false, nil
	}
	if uuid, ok := sylveKeyFile(keyloc); ok {
		if err := s.EnsureEncryptionKeyFile(uuid); err != nil {
			return false, err
		}
	}
	if err := ds.LoadKey(ctx, false); err != nil {
		return false, fmt.Errorf("load_key_failed: %w", err)
	}
	return encryptionKeyIsAvailable(ctx, ds)
}

func encryptionKeyIsAvailable(ctx context.Context, ds *gzfs.Dataset) (bool, error) {
	props, err := ds.GetEncryptionProperties(ctx)
	if err != nil {
		return false, fmt.Errorf("verify_encryption_key_status_failed: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(props.KeyStatus), "available"), nil
}

var errEncryptionKeyUnavailable = errors.New("encryption_key_unavailable")

type receivedKeys struct {
	inherit     bool
	keylocation string
	passphrase  string
}

func (s *Service) unlockReceivedDataset(ctx context.Context, ds *gzfs.Dataset, keys receivedKeys) error {
	props, err := ds.GetEncryptionProperties(ctx)
	if err != nil {
		return fmt.Errorf("get_encryption_properties_failed: %w", err)
	}
	if root := strings.TrimSpace(props.EncryptionRoot); root != "" && root != "-" && root != ds.Name {
		loaded, err := s.ensureEncryptionKeyForDataset(ctx, ds)
		if err == nil && !loaded {
			err = errEncryptionKeyUnavailable
		}
		return err
	}

	_, parentLoaded, parentKeylocation := s.parentEncryption(ctx, ds.Name)
	loaded := strings.EqualFold(strings.TrimSpace(props.KeyStatus), "available")
	bootKeylocation := ""
	if !loaded {
		if bootKeylocation, err = s.loadReceivedKey(ctx, ds, keys, parentKeylocation); err != nil {
			return err
		}
	}
	if keys.inherit && parentLoaded && bootKeylocation != "" && bootKeylocation == parentKeylocation {
		if _, err := utils.RunCommandWithContext(ctx, "zfs", "change-key", "-i", ds.Name); err != nil {
			return fmt.Errorf("inherit_parent_encryption_key_failed: %w", err)
		}
		return nil
	}
	if loaded || strings.TrimSpace(props.KeyLocation) != "prompt" {
		return nil
	}
	if bootKeylocation == "" {
		s.appendRestoreOutput(ctx, fmt.Sprintf(
			"encryption_key_not_kept: %s unlocks with the restore passphrase only; after a reboot load it with zfs load-key",
			ds.Name,
		))
		return nil
	}
	return ds.SetProperties(ctx, "keylocation", bootKeylocation)
}

func (s *Service) loadReceivedKey(
	ctx context.Context,
	ds *gzfs.Dataset,
	keys receivedKeys,
	parentKeylocation string,
) (string, error) {
	for _, location := range []string{keys.keylocation, parentKeylocation} {
		if !strings.HasPrefix(location, "file://") {
			continue
		}
		if uuid, ok := sylveKeyFile(location); ok {
			_ = s.EnsureEncryptionKeyFile(uuid)
		}
		if _, err := utils.RunCommandWithContext(ctx, "zfs", "load-key", "-L", location, ds.Name); err == nil {
			return location, nil
		}
	}
	if keys.passphrase != "" && ds.LoadKeyWithPassphrase(ctx, keys.passphrase, false) == nil {
		return "", nil
	}

	stored, err := s.Cluster.ListEncryptionKeys()
	if err != nil {
		return "", fmt.Errorf("list_encryption_keys_failed: %w", err)
	}
	for _, key := range stored {
		if !sylveKeyName(key.UUID) || ds.LoadKeyWithPassphrase(ctx, key.KeyData, false) != nil {
			continue
		}
		if err := s.EnsureEncryptionKeyFile(key.UUID); err != nil {
			logger.L.Warn().Err(err).Str("uuid", key.UUID).Str("dataset", ds.Name).
				Msg("received_dataset_key_file_write_failed")
			return "", nil
		}
		return "file://" + filepath.Join(EncryptionKeyDirectory, key.UUID), nil
	}
	return "", errEncryptionKeyUnavailable
}

func (s *Service) sameKeyAsParent(ctx context.Context, name, path string) bool {
	_, loaded, keylocation := s.parentEncryption(ctx, name)
	parentPath, ok := strings.CutPrefix(keylocation, "file://")
	if !loaded || !ok {
		return false
	}
	parentKey, err := os.ReadFile(parentPath)
	if err != nil {
		return false
	}
	key, err := os.ReadFile(path)
	return err == nil && string(key) == string(parentKey)
}

func (s *Service) parentEncryption(ctx context.Context, name string) (encrypted, loaded bool, keylocation string) {
	idx := strings.LastIndex(name, "/")
	if idx <= 0 {
		return false, false, ""
	}
	parent, err := s.getLocalDataset(ctx, name[:idx])
	if err != nil || parent == nil || !parent.IsEncrypted() {
		return false, false, ""
	}
	props, err := parent.GetEncryptionProperties(ctx)
	if err != nil {
		return false, false, ""
	}
	keylocation = props.KeyLocation
	if root := strings.TrimSpace(props.EncryptionRoot); root != "" && root != "-" && root != parent.Name {
		if rootDataset, err := s.getLocalDataset(ctx, root); err == nil && rootDataset != nil {
			if rootProps, err := rootDataset.GetEncryptionProperties(ctx); err == nil {
				keylocation = rootProps.KeyLocation
			}
		}
	}
	return true, strings.EqualFold(strings.TrimSpace(props.KeyStatus), "available"), keylocation
}

// A lagging follower may not have the key yet when this returns.
func (s *Service) RegisterRestoreEncryptionKey(keyData, keyFormat string) error {
	if strings.TrimSpace(keyData) == "" {
		return nil
	}

	keyFormat = strings.TrimSpace(keyFormat)
	if keyFormat == "" {
		keyFormat = "passphrase"
	}
	digest := sha256.Sum256([]byte(keyFormat + "\x00" + keyData))
	uuid := fmt.Sprintf("restore-%x", digest[:12])

	if err := s.Cluster.PublishEncryptionKey(context.Background(), uuid, keyData, keyFormat); err != nil {
		return fmt.Errorf("store_restore_encryption_key_failed: %w", err)
	}
	return nil
}
