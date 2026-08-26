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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	BackupTargetReadinessTTL               = 10 * time.Minute
	backupTargetValidationReceiptClockSkew = 2 * time.Minute
	backupTargetValidationTimeout          = 65 * time.Second
	backupTargetValidationEndpoint         = "/api/intra-cluster/backup-target-validation"
)

type BackupTargetValidationRequest struct {
	ExpectedNodeID          string `json:"expectedNodeId"`
	MinimumRaftAppliedIndex uint64 `json:"minimumRaftAppliedIndex,omitempty"`
	TargetID                uint   `json:"targetId"`
	TargetFingerprint       string `json:"targetFingerprint"`
}

type BackupTargetValidationRejectedError struct {
	NodeID string
	Reason string
}

func (e *BackupTargetValidationRejectedError) Error() string {
	if e == nil {
		return "backup_target_validation_rejected"
	}
	return fmt.Sprintf(
		"backup_target_validation_rejected: node_id=%s: %s",
		strings.TrimSpace(e.NodeID),
		strings.TrimSpace(e.Reason),
	)
}

func normalizeBackupTargetValidationRequest(request BackupTargetValidationRequest) BackupTargetValidationRequest {
	request.ExpectedNodeID = strings.TrimSpace(request.ExpectedNodeID)
	request.TargetFingerprint = strings.ToLower(strings.TrimSpace(request.TargetFingerprint))
	return request
}

func (s *Service) SetBackupTargetValidator(
	validator func(context.Context, *clusterModels.BackupTarget) error,
) {
	if s == nil {
		return
	}
	s.backupTargetValidator = validator
}

func (s *Service) validateBackupTargetConnectivityLocalWith(
	ctx context.Context,
	request BackupTargetValidationRequest,
	validator func(context.Context, *clusterModels.BackupTarget) error,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	request = normalizeBackupTargetValidationRequest(request)
	if s == nil || s.DB == nil {
		return clusterModels.BackupTargetNodeReadinessUpdate{}, fmt.Errorf("backup_target_validation_service_unavailable")
	}
	update := clusterModels.BackupTargetNodeReadinessUpdate{
		TargetID:          request.TargetID,
		NodeID:            strings.TrimSpace(s.guestIdentityInventoryLocalNodeID()),
		TargetFingerprint: request.TargetFingerprint,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.ExpectedNodeID == "" {
		return update, fmt.Errorf("backup_target_validation_node_id_required")
	}
	if update.NodeID == "" {
		return update, fmt.Errorf("backup_target_validation_local_node_id_unavailable")
	}
	if request.ExpectedNodeID != update.NodeID {
		return update, fmt.Errorf(
			"backup_target_validation_identity_mismatch: expected=%s actual=%s",
			request.ExpectedNodeID,
			update.NodeID,
		)
	}
	if request.TargetID == 0 || request.TargetFingerprint == "" {
		return update, fmt.Errorf("backup_target_validation_scope_invalid")
	}
	appliedIndex, err := s.waitForBackupJobValidationAppliedIndex(ctx, request.MinimumRaftAppliedIndex)
	if err != nil {
		return update, err
	}
	update.RaftAppliedIndex = appliedIndex

	var target clusterModels.BackupTarget
	result := s.DB.WithContext(ctx).Where("id = ?", request.TargetID).Limit(1).Find(&target)
	if result.Error != nil {
		return update, result.Error
	}
	if result.RowsAffected == 0 {
		return update, fmt.Errorf("backup_target_not_found")
	}
	if actual := clusterModels.BackupTargetConnectivityFingerprint(&target); actual != request.TargetFingerprint {
		return update, fmt.Errorf(
			"backup_target_validation_fingerprint_mismatch: expected=%s actual=%s",
			request.TargetFingerprint,
			actual,
		)
	}
	if validator == nil {
		return update, fmt.Errorf("backup_target_validation_service_unavailable")
	}

	validationErr := validator(ctx, &target)
	verifiedAt := time.Now().UTC()
	update.LastVerifiedAt = verifiedAt
	if validationErr != nil {
		update.ValidationSucceeded = false
		update.LastError = validationErr.Error()
		return update, nil
	}
	readyUntil := verifiedAt.Add(BackupTargetReadinessTTL)
	update.ValidationSucceeded = true
	update.ReadyUntil = &readyUntil
	return update, nil
}

func (s *Service) ValidateBackupTargetConnectivityLocal(
	ctx context.Context,
	request BackupTargetValidationRequest,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	if s == nil {
		return clusterModels.BackupTargetNodeReadinessUpdate{}, fmt.Errorf("backup_target_validation_service_unavailable")
	}
	return s.validateBackupTargetConnectivityLocalWith(ctx, request, s.backupTargetValidator)
}

func validateBackupTargetReadinessReceipt(
	request BackupTargetValidationRequest,
	update *clusterModels.BackupTargetNodeReadinessUpdate,
) error {
	request = normalizeBackupTargetValidationRequest(request)
	if update == nil {
		return fmt.Errorf("backup_target_validation_receipt_missing")
	}
	if strings.TrimSpace(update.NodeID) != request.ExpectedNodeID {
		return fmt.Errorf(
			"backup_target_validation_identity_mismatch: expected=%s actual=%s",
			request.ExpectedNodeID,
			strings.TrimSpace(update.NodeID),
		)
	}
	if update.TargetID != request.TargetID ||
		strings.ToLower(strings.TrimSpace(update.TargetFingerprint)) != request.TargetFingerprint {
		return fmt.Errorf("backup_target_validation_scope_mismatch")
	}
	if update.RaftAppliedIndex < request.MinimumRaftAppliedIndex {
		return fmt.Errorf(
			"backup_target_validation_raft_state_stale: minimum=%d actual=%d",
			request.MinimumRaftAppliedIndex,
			update.RaftAppliedIndex,
		)
	}
	if update.LastVerifiedAt.IsZero() {
		return fmt.Errorf("backup_target_validation_timestamp_missing")
	}
	now := time.Now().UTC()
	verifiedAt := update.LastVerifiedAt.UTC()
	if verifiedAt.Before(now.Add(-backupTargetValidationReceiptClockSkew)) ||
		verifiedAt.After(now.Add(backupTargetValidationReceiptClockSkew)) {
		return fmt.Errorf("backup_target_validation_timestamp_stale")
	}
	if update.ValidationSucceeded {
		if update.ReadyUntil == nil || !update.ReadyUntil.After(update.LastVerifiedAt) ||
			update.ReadyUntil.Sub(update.LastVerifiedAt) != BackupTargetReadinessTTL {
			return fmt.Errorf("backup_target_validation_expiry_invalid")
		}
		return nil
	}
	if strings.TrimSpace(update.LastError) == "" {
		return fmt.Errorf("backup_target_validation_failure_reason_missing")
	}
	return nil
}

func stampRemoteBackupTargetReadinessReceipt(update *clusterModels.BackupTargetNodeReadinessUpdate) {
	if update == nil {
		return
	}
	verifiedAt := time.Now().UTC()
	update.LastVerifiedAt = verifiedAt
	if update.ValidationSucceeded {
		readyUntil := verifiedAt.Add(BackupTargetReadinessTTL)
		update.ReadyUntil = &readyUntil
	} else {
		update.ReadyUntil = nil
	}
}

func backupTargetReadinessOutcome(update *clusterModels.BackupTargetNodeReadinessUpdate) error {
	if update == nil || update.ValidationSucceeded {
		return nil
	}
	return &BackupTargetValidationRejectedError{
		NodeID: update.NodeID,
		Reason: update.LastError,
	}
}

func (s *Service) fetchBackupTargetValidation(
	ctx context.Context,
	nodeID, endpoint string,
	request BackupTargetValidationRequest,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	var update clusterModels.BackupTargetNodeReadinessUpdate
	if s == nil || s.AuthService == nil {
		return update, fmt.Errorf("backup_target_validation_auth_service_unavailable")
	}
	localNodeID := s.guestIdentityInventoryLocalNodeID()
	clusterToken, err := s.AuthService.CreateInternalClusterJWT(localNodeID)
	if err != nil {
		return update, fmt.Errorf("backup_target_validation_cluster_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return update, fmt.Errorf("backup_target_validation_marshal_failed: %w", err)
	}
	body, statusCode, err := utils.HTTPPostJSONWithTimeoutContext(
		ctx,
		fmt.Sprintf("https://%s%s", endpoint, backupTargetValidationEndpoint),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
		backupTargetValidationTimeout,
	)
	if err != nil {
		return update, fmt.Errorf(
			"backup_target_validation_request_failed: node_id=%s status=%d: %w",
			nodeID,
			statusCode,
			err,
		)
	}
	var response internal.APIResponse[clusterModels.BackupTargetNodeReadinessUpdate]
	if err := json.Unmarshal(body, &response); err != nil {
		return update, fmt.Errorf("backup_target_validation_decode_failed: node_id=%s: %w", nodeID, err)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Status), "success") {
		return update, fmt.Errorf(
			"backup_target_validation_non_success: node_id=%s message=%s error=%s",
			nodeID,
			strings.TrimSpace(response.Message),
			strings.TrimSpace(response.Error),
		)
	}
	stampRemoteBackupTargetReadinessReceipt(&response.Data)
	return response.Data, nil
}

func (s *Service) UpdateBackupTargetNodeReadiness(
	update clusterModels.BackupTargetNodeReadinessUpdate,
	bypassRaft bool,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_target_readiness_service_unavailable")
	}
	if bypassRaft || s.Raft == nil {
		return clusterModels.ApplyBackupTargetNodeReadinessUpdateTxn(s.DB, &update)
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("backup_target_readiness_marshal_failed: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_target_readiness", Action: "update", Data: data,
	})
}

func (s *Service) ValidateBackupTargetOnNode(
	ctx context.Context,
	targetID uint,
	nodeID string,
	localValidator func(context.Context, *clusterModels.BackupTarget) error,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	var update clusterModels.BackupTargetNodeReadinessUpdate
	if s == nil || s.DB == nil {
		return update, fmt.Errorf("backup_target_validation_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Raft != nil {
		if s.Raft.State() != raft.Leader {
			return update, fmt.Errorf("not_leader")
		}
		if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
			return update, fmt.Errorf("backup_target_validation_leader_barrier_failed: %w", err)
		}
	}
	var target clusterModels.BackupTarget
	targetResult := s.DB.WithContext(ctx).Where("id = ?", targetID).Limit(1).Find(&target)
	if targetResult.Error != nil {
		return update, targetResult.Error
	}
	if targetResult.RowsAffected == 0 {
		return update, fmt.Errorf("backup_target_not_found")
	}
	fingerprint := clusterModels.BackupTargetConnectivityFingerprint(&target)
	nodeID = strings.TrimSpace(nodeID)
	localNodeID := s.guestIdentityInventoryLocalNodeID()
	if s.Raft == nil {
		if nodeID == "" {
			nodeID = localNodeID
		}
		if nodeID == "" {
			nodeID = "local"
			localNodeID = nodeID
		}
		request := BackupTargetValidationRequest{
			ExpectedNodeID: nodeID, TargetID: target.ID, TargetFingerprint: fingerprint,
		}
		if strings.TrimSpace(s.NodeID) == "" && s.guestIdentityInventoryLocalNodeID() == "" {
			s.NodeID = localNodeID
		}
		update, err := s.validateBackupTargetConnectivityLocalWith(ctx, request, localValidator)
		if err != nil {
			return update, err
		}
		if err := validateBackupTargetReadinessReceipt(request, &update); err != nil {
			return update, err
		}
		if err := s.UpdateBackupTargetNodeReadiness(update, true); err != nil {
			return update, err
		}
		return update, backupTargetReadinessOutcome(&update)
	}
	if nodeID == "" {
		return update, fmt.Errorf("backup_target_validation_node_id_required")
	}
	server, local, err := s.backupJobRunnerVoter(nodeID)
	if err != nil {
		return update, err
	}
	request := BackupTargetValidationRequest{
		ExpectedNodeID:          nodeID,
		MinimumRaftAppliedIndex: s.Raft.AppliedIndex(),
		TargetID:                target.ID,
		TargetFingerprint:       fingerprint,
	}
	if local {
		update, err = s.validateBackupTargetConnectivityLocalWith(ctx, request, localValidator)
	} else {
		endpoint, resolveErr := s.backupTargetValidationAPI(nodeID, server.Address)
		if resolveErr != nil {
			return update, resolveErr
		}
		update, err = s.fetchBackupTargetValidation(ctx, nodeID, endpoint, request)
	}
	if err != nil {
		return update, err
	}
	if err := validateBackupTargetReadinessReceipt(request, &update); err != nil {
		return update, err
	}
	if err := s.UpdateBackupTargetNodeReadiness(update, false); err != nil {
		return update, err
	}
	return update, backupTargetReadinessOutcome(&update)
}

func (s *Service) backupTargetValidationAPI(nodeID string, address raft.ServerAddress) (string, error) {
	if s != nil && s.backupTargetValidationAPIForNode != nil {
		endpoint, err := s.backupTargetValidationAPIForNode(nodeID, address)
		if err != nil {
			return "", fmt.Errorf("backup_target_validation_api_resolve_failed: node_id=%s: %w", nodeID, err)
		}
		return normalizeGuestIdentityInventoryAPIEndpoint(endpoint)
	}
	return s.backupJobValidationAPI(nodeID, address)
}

func (s *Service) currentBackupTargetVoterIDs() ([]string, error) {
	if s == nil {
		return nil, fmt.Errorf("backup_target_readiness_service_unavailable")
	}
	if s.Raft == nil {
		nodeID := s.guestIdentityInventoryLocalNodeID()
		if nodeID == "" {
			nodeID = "local"
		}
		return []string{nodeID}, nil
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("backup_target_readiness_raft_configuration_failed: %w", err)
	}
	voters := make([]string, 0, len(future.Configuration().Servers))
	seen := make(map[string]struct{}, len(future.Configuration().Servers))
	for _, server := range future.Configuration().Servers {
		if server.Suffrage != raft.Voter {
			continue
		}
		nodeID := strings.TrimSpace(string(server.ID))
		if nodeID == "" {
			return nil, fmt.Errorf("backup_target_readiness_empty_voter_id")
		}
		if _, exists := seen[nodeID]; exists {
			return nil, fmt.Errorf("backup_target_readiness_duplicate_voter_id: %s", nodeID)
		}
		seen[nodeID] = struct{}{}
		voters = append(voters, nodeID)
	}
	sort.Strings(voters)
	return voters, nil
}

func backupTargetReadinessStatuses(
	target clusterModels.BackupTarget,
	rows []clusterModels.BackupTargetNodeReadiness,
	voterIDs []string,
	now time.Time,
) []clusterModels.BackupTargetNodeReadinessStatus {
	voters := make(map[string]struct{}, len(voterIDs))
	for _, nodeID := range voterIDs {
		voters[nodeID] = struct{}{}
	}
	byNode := make(map[string]clusterModels.BackupTargetNodeReadiness, len(rows))
	for _, row := range rows {
		if row.TargetID == target.ID {
			byNode[strings.TrimSpace(row.NodeID)] = row
		}
	}
	nodeIDs := make([]string, 0, len(voters)+len(byNode))
	for nodeID := range voters {
		nodeIDs = append(nodeIDs, nodeID)
	}
	for nodeID := range byNode {
		if _, exists := voters[nodeID]; !exists {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	sort.Strings(nodeIDs)
	fingerprint := clusterModels.BackupTargetConnectivityFingerprint(&target)
	statuses := make([]clusterModels.BackupTargetNodeReadinessStatus, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		row, exists := byNode[nodeID]
		_, currentVoter := voters[nodeID]
		status := clusterModels.BackupTargetNodeReadinessStatus{
			TargetID: target.ID, NodeID: nodeID, CurrentVoter: currentVoter,
			ConfigurationCurrent: true,
		}
		if !exists {
			status.LastError = "backup_target_not_validated_on_node"
			statuses = append(statuses, status)
			continue
		}
		verifiedAt := row.LastVerifiedAt.UTC()
		status.ValidationSucceeded = row.ValidationSucceeded
		status.LastVerifiedAt = &verifiedAt
		status.ReadyUntil = row.ReadyUntil
		status.LastError = row.LastError
		status.Revision = row.Revision
		status.ConfigurationCurrent = strings.TrimSpace(row.TargetFingerprint) == fingerprint
		status.Expired = row.ValidationSucceeded && (row.ReadyUntil == nil || !row.ReadyUntil.After(now))
		status.Ready = row.ValidationSucceeded && currentVoter && status.ConfigurationCurrent && !status.Expired
		if !currentVoter && status.LastError == "" {
			status.LastError = "backup_target_validation_node_not_current_voter"
		} else if !status.ConfigurationCurrent && status.LastError == "" {
			status.LastError = "backup_target_configuration_changed"
		} else if status.Expired && status.LastError == "" {
			status.LastError = "backup_target_readiness_expired"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (s *Service) attachBackupTargetReadiness(targets []clusterModels.BackupTarget) error {
	if len(targets) == 0 || s == nil || s.DB == nil ||
		!s.DB.Migrator().HasTable(&clusterModels.BackupTargetNodeReadiness{}) {
		return nil
	}
	ids := make([]uint, 0, len(targets))
	for i := range targets {
		ids = append(ids, targets[i].ID)
	}
	var rows []clusterModels.BackupTargetNodeReadiness
	if err := s.DB.Where("target_id IN ?", ids).
		Order("target_id ASC, node_id ASC").Find(&rows).Error; err != nil {
		return err
	}
	voters, err := s.currentBackupTargetVoterIDs()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range targets {
		targets[i].Readiness = backupTargetReadinessStatuses(targets[i], rows, voters, now)
	}
	return nil
}

func (s *Service) requireBackupTargetReadinessBarrier() error {
	if s == nil || s.Raft == nil {
		return nil
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("backup_target_readiness_barrier_failed: %w", err)
	}
	return nil
}

func (s *Service) BackupTargetReadiness(targetID uint) ([]clusterModels.BackupTargetNodeReadinessStatus, error) {
	if targetID == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}
	target, err := s.GetBackupTargetByID(targetID)
	if err != nil {
		return nil, err
	}
	copyTarget := []clusterModels.BackupTarget{*target}
	if err := s.attachBackupTargetReadiness(copyTarget); err != nil {
		return nil, err
	}
	return copyTarget[0].Readiness, nil
}

func isBackupTargetValidationRejected(err error) bool {
	var rejected *BackupTargetValidationRejectedError
	return errors.As(err, &rejected)
}
