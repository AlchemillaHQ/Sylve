// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

// The periodic key scan sends every key it finds; keys the store already holds
// unchanged aren't proposed again.
func (s *Service) PublishEncryptionKey(ctx context.Context, uuid, keyData, keyFormat string) error {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return fmt.Errorf("encryption_key_uuid_required")
	}
	if strings.TrimSpace(keyData) == "" {
		return fmt.Errorf("encryption_key_data_required")
	}
	if strings.TrimSpace(keyFormat) == "" {
		keyFormat = "passphrase"
	}
	var existing clusterModels.EncryptionKey
	if err := s.DB.Where("uuid = ?", uuid).Limit(1).Find(&existing).Error; err == nil &&
		existing.ID != 0 && existing.KeyData == keyData && existing.KeyFormat == keyFormat {
		return nil
	}
	return s.Propose(ctx, "encryption_key", "upsert", clusterModels.EncryptionKey{
		UUID: uuid, KeyData: keyData, KeyFormat: keyFormat,
	})
}

func (s *Service) DeleteEncryptionKey(ctx context.Context, uuid, keyHash string) error {
	return s.Propose(ctx, "encryption_key", "delete", clusterModels.EncryptionKeyRemoval{
		UUID: strings.TrimSpace(uuid), KeyHash: keyHash,
	})
}

func (s *Service) ListEncryptionKeys() ([]clusterModels.EncryptionKey, error) {
	var keys []clusterModels.EncryptionKey
	if err := s.DB.Order("id ASC").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list_encryption_keys_failed: %w", err)
	}
	return keys, nil
}

func (s *Service) GetEncryptionKeyByUUID(uuid string) (*clusterModels.EncryptionKey, error) {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, fmt.Errorf("encryption_key_uuid_required")
	}

	var key clusterModels.EncryptionKey
	if err := s.DB.Where("uuid = ?", uuid).First(&key).Error; err != nil {
		return nil, fmt.Errorf("encryption_key_not_found: %s", uuid)
	}
	return &key, nil
}
