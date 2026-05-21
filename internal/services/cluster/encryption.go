// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm/clause"
)

func (s *Service) ProposeEncryptionKeyUpsert(uuid, keyData, keyFormat string, bypassRaft bool) error {
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

	key := clusterModels.EncryptionKey{
		UUID:      uuid,
		KeyData:   keyData,
		KeyFormat: keyFormat,
	}

	if bypassRaft || s.Raft == nil {
		return s.UpsertEncryptionKeyLocally(uuid, keyData, keyFormat)
	}

	data, err := json.Marshal(key)
	if err != nil {
		return fmt.Errorf("marshal_encryption_key_failed: %w", err)
	}

	cmd := clusterModels.Command{Type: "encryption_key", Action: "upsert", Data: data}
	return s.applyRaftCommand(cmd)
}

func (s *Service) ProposeEncryptionKeyDelete(uuid string, bypassRaft bool) error {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil
	}

	if bypassRaft || s.Raft == nil {
		return s.DB.Where("uuid = ?", uuid).Delete(&clusterModels.EncryptionKey{}).Error
	}

	payload := struct {
		UUID string `json:"uuid"`
	}{UUID: uuid}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal_delete_payload_failed: %w", err)
	}

	cmd := clusterModels.Command{Type: "encryption_key", Action: "delete", Data: data}
	return s.applyRaftCommand(cmd)
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

func (s *Service) UpsertEncryptionKeyLocally(uuid, keyData, keyFormat string) error {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return fmt.Errorf("encryption_key_uuid_required")
	}
	if strings.TrimSpace(keyFormat) == "" {
		keyFormat = "passphrase"
	}

	key := clusterModels.EncryptionKey{
		UUID:      uuid,
		KeyData:   keyData,
		KeyFormat: keyFormat,
	}

	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{"key_data", "key_format"}),
	}).Create(&key).Error
}

func (s *Service) ForwardEncryptionKeyToLeader(uuid, keyData, keyFormat string) error {
	if s.Raft == nil || s.Raft.State() == raft.Leader {
		return s.ProposeEncryptionKeyUpsert(uuid, keyData, keyFormat, false)
	}

	leaderAddr, _ := s.Raft.LeaderWithID()
	if leaderAddr == "" {
		return fmt.Errorf("leader_unknown")
	}

	host, _, err := net.SplitHostPort(string(leaderAddr))
	if err != nil {
		host = string(leaderAddr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("leader_host_unknown")
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.AuthService.CreateInternalClusterJWT(hostname, "")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	payload := struct {
		UUID      string `json:"uuid"`
		KeyData   string `json:"keyData"`
		KeyFormat string `json:"keyFormat"`
	}{UUID: uuid, KeyData: keyData, KeyFormat: keyFormat}

	url := fmt.Sprintf("https://%s:%d/api/intra-cluster/encryption-key/discover", host, ClusterEmbeddedHTTPSPort)
	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	// Best-effort forwarding: if it fails, the key will be discovered on
	// the next reconcile cycle or when this node becomes leader.
	if err := utils.HTTPPostJSON(url, payload, headers); err != nil {
		logger.L.Warn().Err(err).Str("uuid", uuid).Msg("forward_encryption_key_http_failed")
	}
	return nil
}

func (s *Service) ForwardEncryptionKeyDeleteToLeader(uuid string) {
	if s.Raft == nil || s.Raft.State() == raft.Leader {
		_ = s.ProposeEncryptionKeyDelete(uuid, false)
		return
	}

	leaderAddr, _ := s.Raft.LeaderWithID()
	if leaderAddr == "" {
		return
	}

	host, _, err := net.SplitHostPort(string(leaderAddr))
	if err != nil {
		host = string(leaderAddr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.AuthService.CreateInternalClusterJWT(hostname, "")
	if err != nil {
		return
	}

	payload := struct {
		UUID string `json:"uuid"`
	}{UUID: uuid}

	url := fmt.Sprintf("https://%s:%d/api/intra-cluster/encryption-key/delete", host, ClusterEmbeddedHTTPSPort)
	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	if err := utils.HTTPPostJSON(url, payload, headers); err != nil {
		logger.L.Warn().Err(err).Str("uuid", uuid).Msg("forward_encryption_key_delete_http_failed")
	}
}
