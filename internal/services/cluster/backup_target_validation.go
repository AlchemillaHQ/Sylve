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
	"net"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
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
	HostKeyRevision         uint64 `json:"hostKeyRevision,omitempty"`
	EnrollHostKey           bool   `json:"enrollHostKey,omitempty"`
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

func (s *Service) SetBackupTargetHostKeyEnroller(enroller func(context.Context, *clusterModels.BackupTarget) error) {
	if s == nil {
		return
	}
	s.backupTargetHostKeyEnroller = enroller
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
	trust, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB.WithContext(ctx), &target)
	if err != nil {
		return update, err
	}
	if request.HostKeyRevision == 0 || request.HostKeyRevision != trust.Revision {
		return update, fmt.Errorf("backup_target_host_key_revision_conflict")
	}
	target.SSHHostKeyRevision = trust.Revision
	if request.EnrollHostKey && trust.PublicKey == "" {
		if s.backupTargetHostKeyEnroller == nil {
			return update, fmt.Errorf("backup_target_validation_service_unavailable")
		}
		if err := s.backupTargetHostKeyEnroller(ctx, &target); err != nil {
			update.LastVerifiedAt, update.LastError = time.Now().UTC(), err.Error()
			update.HostKeyRevision = request.HostKeyRevision
			return update, nil
		}
		trust, err = clusterModels.GetBackupTargetSSHHostTrust(s.DB.WithContext(ctx), &target)
		if err != nil {
			return update, err
		}
		if trust.Revision != request.HostKeyRevision+1 || trust.PublicKey == "" {
			return update, fmt.Errorf("backup_target_host_key_revision_conflict")
		}
	}
	update.HostKeyRevision = trust.Revision
	target.SSHHostKey, target.SSHHostKeyRevision = trust.PublicKey, trust.Revision

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
	if request.HostKeyRevision == 0 || (update.HostKeyRevision != request.HostKeyRevision && (!request.EnrollHostKey || update.HostKeyRevision != request.HostKeyRevision+1)) {
		return fmt.Errorf("backup_target_host_key_revision_conflict")
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
	if err := s.requireRuntimeWriteAuthority(bypassRaft); err != nil {
		return err
	}
	if bypassRaft || s.Raft == nil {
		return clusterModels.ApplyBackupTargetNodeReadinessUpdateV2Txn(s.DB, &update)
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("backup_target_readiness_marshal_failed: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_target_readiness", Action: "update_v2", Data: data,
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
	if err := s.InitializeBackupTargetHostTrust(); err != nil {
		return update, err
	}
	update, err := s.checkBackupTargetOnNode(ctx, targetID, nodeID, localValidator, true)
	if err != nil {
		return update, err
	}
	if err := s.UpdateBackupTargetNodeReadiness(update, s.Raft == nil); err != nil {
		return update, err
	}
	return update, backupTargetReadinessOutcome(&update)
}

func (s *Service) CheckBackupTargetOnNode(
	ctx context.Context,
	targetID uint,
	nodeID string,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	update, err := s.checkBackupTargetOnNode(ctx, targetID, nodeID, s.backupTargetValidator)
	if err != nil {
		return update, err
	}
	return update, backupTargetReadinessOutcome(&update)
}

func (s *Service) CheckGuestBackupTargetsForMigration(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("migration_backup_target_preflight_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	targetNodeID = strings.TrimSpace(targetNodeID)
	if !clusterModels.ValidGuestIdentityKind(guestType) || guestID == 0 ||
		guestID > clusterModels.GuestIdentityMaxID || targetNodeID == "" {
		return fmt.Errorf("migration_backup_target_preflight_invalid")
	}

	var jobs []clusterModels.BackupJob
	if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&jobs).Error; err != nil {
		return fmt.Errorf("migration_backup_target_preflight_job_lookup_failed: %w", err)
	}
	targetSet := make(map[uint]struct{})
	for i := range jobs {
		jobGuestType, jobGuestID := clusterModels.BackupJobGuestIdentity(&jobs[i])
		if jobGuestType != guestType || jobGuestID != guestID {
			continue
		}
		if jobs[i].TargetID == 0 {
			return fmt.Errorf("migration_backup_target_preflight_failed: job=%q target_id_required", jobs[i].Name)
		}
		targetSet[jobs[i].TargetID] = struct{}{}
	}
	if len(targetSet) == 0 {
		return nil
	}

	targetIDs := make([]uint, 0, len(targetSet))
	for targetID := range targetSet {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Slice(targetIDs, func(i, j int) bool { return targetIDs[i] < targetIDs[j] })

	failures := make([]string, 0)
	for _, targetID := range targetIDs {
		var target clusterModels.BackupTarget
		result := s.DB.WithContext(ctx).Where("id = ?", targetID).Limit(1).Find(&target)
		if result.Error != nil {
			return fmt.Errorf("migration_backup_target_preflight_target_lookup_failed: target_id=%d: %w", targetID, result.Error)
		}
		if result.RowsAffected == 0 {
			failures = append(failures, fmt.Sprintf("target_id=%d backup_target_not_found", targetID))
			continue
		}
		label := fmt.Sprintf("target=%q target_id=%d node_id=%s", target.Name, target.ID, targetNodeID)
		if !target.Enabled {
			failures = append(failures, fmt.Sprintf("%s: backup_target_disabled", label))
			continue
		}
		if _, err := s.CheckBackupTargetOnNode(ctx, target.ID, targetNodeID); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", label, err))
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("migration_backup_target_preflight_failed: %s", strings.Join(failures, " | "))
	}
	return nil
}

func (s *Service) checkBackupTargetOnNode(
	ctx context.Context,
	targetID uint,
	nodeID string,
	localValidator func(context.Context, *clusterModels.BackupTarget) error,
	enrollment ...bool,
) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
	var update clusterModels.BackupTargetNodeReadinessUpdate
	if s == nil || s.DB == nil {
		return update, fmt.Errorf("backup_target_validation_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
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
	enroll := len(enrollment) > 0 && enrollment[0]
	trust, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB.WithContext(ctx), &target)
	if err != nil {
		return update, err
	}
	hostRevision := trust.Revision
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
			HostKeyRevision: hostRevision, EnrollHostKey: enroll,
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
		return update, nil
	}
	if nodeID == "" {
		return update, fmt.Errorf("backup_target_validation_node_id_required")
	}
	server, local, err := s.backupJobRunnerVoter(nodeID)
	if err != nil {
		return update, err
	}
	request := BackupTargetValidationRequest{
		HostKeyRevision: hostRevision, EnrollHostKey: enroll,
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
	return update, nil
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
		if target.HostKey != nil {
			status.ConfigurationCurrent = status.ConfigurationCurrent && row.HostKeyRevision == target.HostKey.Revision && target.HostKey.State == "trusted"
		}
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
	if s == nil {
		return fmt.Errorf("backup_target_validation_service_unavailable")
	}
	if s.Raft == nil {
		if err := s.requireRuntimeWriteAuthority(true); err != nil {
			return err
		}
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
	if err := s.attachBackupTargetHostTrust(copyTarget); err != nil {
		return nil, err
	}
	if err := s.attachBackupTargetReadiness(copyTarget); err != nil {
		return nil, err
	}
	return copyTarget[0].Readiness, nil
}

func isBackupTargetValidationRejected(err error) bool {
	var rejected *BackupTargetValidationRejectedError
	return errors.As(err, &rejected)
}

type BackupTargetHostKeyInstallResult struct {
	Revision     uint64 `json:"revision"`
	AppliedIndex uint64 `json:"appliedIndex"`
	Fingerprint  string `json:"fingerprint"`
}

type BackupTargetHostKeyResetResult struct {
	clusterModels.BackupTargetSSHHostKeyStatus
	TargetID       uint   `json:"targetId"`
	OldFingerprint string `json:"oldFingerprint"`
}

func (s *Service) InitializeBackupTargetHostTrust() error {
	if s == nil || s.DB == nil || !s.DB.Migrator().HasTable(&clusterModels.BackupTargetSSHHostTrust{}) {
		return fmt.Errorf("backup_target_host_key_state_unavailable")
	}
	if s.Raft != nil && s.Raft.State() != raft.Leader {
		return nil
	}
	var options clusterModels.ClusterOption
	if err := s.DB.Where("id = ?", 1).Limit(1).Find(&options).Error; err != nil {
		return fmt.Errorf("backup_target_host_key_state_unavailable: %w", err)
	}
	if options.SSHHostTrustInitialized {
		return nil
	}
	return s.applyBackupTargetHostTrust("initialize_all_v1", clusterModels.BackupTargetSSHHostTrustChange{OccurredAt: time.Now().UTC()})
}

func (s *Service) applyBackupTargetHostTrust(action string, change clusterModels.BackupTargetSSHHostTrustChange) error {
	if s.Raft == nil {
		if err := s.requireRuntimeWriteAuthority(true); err != nil {
			return err
		}
		if action == "initialize_all_v1" {
			return clusterModels.InitializeBackupTargetSSHHostTrustTxn(s.DB, change.OccurredAt)
		}
		return clusterModels.ApplyBackupTargetSSHHostTrustTxn(s.DB, action, &change)
	}
	if s.Raft.State() != raft.Leader {
		return raft.ErrNotLeader
	}
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	return s.applyRaftCommand(clusterModels.Command{Type: "backup_target_ssh_host_trust", Action: action, Data: data})
}

func (s *Service) InstallBackupTargetHostKey(ctx context.Context, change clusterModels.BackupTargetSSHHostTrustChange) error {
	if change.ExpectedRevision == 0 {
		return fmt.Errorf("backup_target_host_key_revision_required")
	}
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if s.Raft != nil && s.Raft.State() != raft.Leader {
		leader, _ := s.Raft.LeaderWithID()
		host, _, err := net.SplitHostPort(string(leader))
		if err != nil || host == "" || s.AuthService == nil {
			return fmt.Errorf("backup_target_host_key_leader_unavailable")
		}
		token, err := s.AuthService.CreateInternalClusterJWT(s.guestIdentityInventoryLocalNodeID())
		if err != nil {
			return err
		}
		payload, err := json.Marshal(change)
		if err != nil {
			return err
		}
		body, status, err := utils.HTTPPostJSONWithTimeoutContext(ctx,
			"https://"+net.JoinHostPort(host, fmt.Sprint(ClusterEmbeddedHTTPSPort))+"/api/intra-cluster/backup-target-host-key",
			payload, map[string]string{"Content-Type": "application/json", "X-Cluster-Token": "Bearer " + token}, 30*time.Second)
		if err != nil {
			return fmt.Errorf("backup_target_host_key_install_failed: status=%d: %w", status, err)
		}
		var response internal.APIResponse[BackupTargetHostKeyInstallResult]
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		if response.Status != "success" {
			return fmt.Errorf("backup_target_host_key_install_failed: %s", response.Error)
		}
		fingerprint, keyErr := remoteexec.SSHHostKeyFingerprint(change.PublicKey)
		if keyErr != nil || response.Data.Revision != change.ExpectedRevision+1 || response.Data.Fingerprint != fingerprint || response.Data.AppliedIndex == 0 {
			return fmt.Errorf("backup_target_host_key_install_receipt_invalid")
		}
		_, err = s.WaitForReplicatedStateAppliedIndex(ctx, response.Data.AppliedIndex)
		return err
	}
	change.OccurredAt = time.Now().UTC()
	if err := s.applyBackupTargetHostTrust("install_v1", change); err != nil {
		return err
	}
	fingerprint, _ := remoteexec.SSHHostKeyFingerprint(change.PublicKey)
	logger.L.Info().Uint("target_id", change.TargetID).Str("fingerprint", fingerprint).
		Uint64("host_key_revision", change.ExpectedRevision+1).Msg("backup_target_host_key_installed")
	return nil
}

func (s *Service) ResetBackupTargetHostKey(ctx context.Context, targetID uint, expectedRevision uint64) (*BackupTargetHostKeyResetResult, error) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}
	target, err := s.GetBackupTargetByID(targetID)
	if err != nil {
		return nil, err
	}
	trust, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB.WithContext(ctx), target)
	if err != nil {
		return nil, err
	}
	if trust.Revision != expectedRevision {
		return nil, fmt.Errorf("backup_target_host_key_reset_stale: the host-key state changed; refresh the page, then review the target again")
	}
	if err := s.applyBackupTargetHostTrust("reset_v1", clusterModels.BackupTargetSSHHostTrustChange{
		TargetID: targetID, EndpointFingerprint: trust.EndpointFingerprint,
		ExpectedRevision: expectedRevision, OccurredAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	status := clusterModels.BackupTargetSSHHostKeyStatus{State: "unlearned", Revision: expectedRevision + 1}
	logger.L.Info().Uint("target_id", targetID).Str("old_fingerprint", trust.Status().Fingerprint).
		Uint64("host_key_revision", status.Revision).Msg("backup_target_host_key_reset")
	return &BackupTargetHostKeyResetResult{BackupTargetSSHHostKeyStatus: status, TargetID: targetID, OldFingerprint: trust.Status().Fingerprint}, nil
}

func (s *Service) attachBackupTargetHostTrust(targets []clusterModels.BackupTarget) error {
	if !s.DB.Migrator().HasTable(&clusterModels.BackupTargetSSHHostTrust{}) {
		return fmt.Errorf("backup_target_host_key_state_unavailable")
	}
	for i := range targets {
		trust, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB, &targets[i])
		if err != nil {
			return err
		}
		status := trust.Status()
		targets[i].HostKey = &status
	}
	return nil
}
