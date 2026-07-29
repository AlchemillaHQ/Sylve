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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	BackupJobSourceClassificationDataset      = "dataset"
	BackupJobSourceClassificationManagedVM    = "managed_vm"
	BackupJobSourceClassificationManagedJail  = "managed_jail"
	BackupJobSourceClassificationManagedScope = "managed_scope"

	backupJobValidationTimeout = 8 * time.Second
)

// BackupJobPlacementAuthorization is supplied only by internal migration or
// failover recovery paths. User create/update requests always use the zero
// value and cannot bypass an active replicated guest transition.
type BackupJobPlacementAuthorization struct {
	GuestOperationToken string
	TransitionRunID     string
}

type BackupJobSafetyValidationRequest struct {
	ExpectedNodeID          string `json:"expectedNodeId"`
	MinimumRaftAppliedIndex uint64 `json:"minimumRaftAppliedIndex,omitempty"`
	Mode                    string `json:"mode"`
	SourceDataset           string `json:"sourceDataset"`
	JailRootDataset         string `json:"jailRootDataset"`
	Recursive               bool   `json:"recursive"`
}

type BackupJobSafetyValidationResult struct {
	NodeID           string `json:"nodeId"`
	RaftAppliedIndex uint64 `json:"raftAppliedIndex,omitempty"`
	Valid            bool   `json:"valid"`
	ValidationError  string `json:"validationError,omitempty"`

	Mode            string `json:"mode"`
	SourceDataset   string `json:"sourceDataset"`
	JailRootDataset string `json:"jailRootDataset"`
	Classification  string `json:"classification"`
	ManagedScope    bool   `json:"managedScope"`
	GuestType       string `json:"guestType,omitempty"`
	GuestID         uint   `json:"guestId,omitempty"`
	FriendlySource  string `json:"friendlySource"`
	OwnerNodeID     string `json:"ownerNodeId,omitempty"`
	OwnerEpoch      uint64 `json:"ownerEpoch,omitempty"`

	PlacementFence *clusterModels.BackupJobPlacementFence `json:"placementFence,omitempty"`
}

func normalizeBackupJobSafetyValidationRequest(request BackupJobSafetyValidationRequest) BackupJobSafetyValidationRequest {
	request.ExpectedNodeID = strings.TrimSpace(request.ExpectedNodeID)
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.SourceDataset = normalizeManagedGuestDatasetPath(request.SourceDataset)
	request.JailRootDataset = normalizeManagedGuestDatasetPath(request.JailRootDataset)
	return request
}

func backupJobValidationRequest(job *clusterModels.BackupJob, expectedNodeID string, minimumIndex uint64) BackupJobSafetyValidationRequest {
	request := BackupJobSafetyValidationRequest{
		ExpectedNodeID:          strings.TrimSpace(expectedNodeID),
		MinimumRaftAppliedIndex: minimumIndex,
	}
	if job != nil {
		request.Mode = job.Mode
		request.SourceDataset = job.SourceDataset
		request.JailRootDataset = job.JailRootDataset
		request.Recursive = job.Recursive
	}
	return normalizeBackupJobSafetyValidationRequest(request)
}

func backupJobValidationIdentity(request BackupJobSafetyValidationRequest) (string, uint) {
	switch request.Mode {
	case clusterModels.BackupJobModeVM:
		id, ok := canonicalManagedGuestRootID(request.SourceDataset, "virtual-machines")
		if ok {
			return clusterModels.BackupJobModeVM, id
		}
	case clusterModels.BackupJobModeJail:
		id, ok := canonicalManagedGuestRootID(request.JailRootDataset, "jails")
		if ok {
			return clusterModels.BackupJobModeJail, id
		}
	}
	return "", 0
}

func backupJobValidationClassification(request BackupJobSafetyValidationRequest, validationErr error) (string, bool) {
	switch request.Mode {
	case clusterModels.BackupJobModeVM:
		return BackupJobSourceClassificationManagedVM, true
	case clusterModels.BackupJobModeJail:
		return BackupJobSourceClassificationManagedJail, true
	default:
		if isReservedManagedBackupScope(request.SourceDataset) ||
			(validationErr != nil && strings.Contains(validationErr.Error(), "managed_guest")) {
			return BackupJobSourceClassificationManagedScope, true
		}
		return BackupJobSourceClassificationDataset, false
	}
}

func backupJobFriendlySourceWithDB(db *gorm.DB, request BackupJobSafetyValidationRequest, guestID uint) (string, error) {
	switch request.Mode {
	case clusterModels.BackupJobModeDataset:
		return request.SourceDataset, nil
	case clusterModels.BackupJobModeVM:
		var row struct{ Name string }
		result := db.Model(&vmModels.VM{}).Select("name").Where("rid = ?", guestID).Limit(1).Scan(&row)
		if result.Error != nil {
			return "", result.Error
		}
		if name := strings.TrimSpace(row.Name); name != "" {
			return name, nil
		}
		return request.SourceDataset, nil
	case clusterModels.BackupJobModeJail:
		var row struct{ Name string }
		result := db.Model(&jailModels.Jail{}).Select("name").Where("ct_id = ?", guestID).Limit(1).Scan(&row)
		if result.Error != nil {
			return "", result.Error
		}
		if name := strings.TrimSpace(row.Name); name != "" {
			return name, nil
		}
		return request.JailRootDataset, nil
	default:
		return "", fmt.Errorf("invalid_backup_job_mode")
	}
}

func backupJobPlacementOwner(fence *clusterModels.BackupJobPlacementFence, localNodeID string) (string, uint64) {
	if fence == nil || !fence.PolicyPresent {
		return strings.TrimSpace(localNodeID), 0
	}
	owner := strings.TrimSpace(fence.PolicyActiveNodeID)
	if owner == "" {
		owner = strings.TrimSpace(fence.PolicySourceNodeID)
	}
	return owner, fence.PolicyOwnerEpoch
}

func (s *Service) waitForBackupJobValidationAppliedIndex(ctx context.Context, minimum uint64) (uint64, error) {
	if minimum == 0 {
		if s != nil && s.Raft != nil {
			return s.Raft.AppliedIndex(), nil
		}
		return 0, nil
	}
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return 0, fmt.Errorf("backup_runner_raft_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		applied := s.Raft.AppliedIndex()
		if applied >= minimum {
			return applied, nil
		}
		select {
		case <-ctx.Done():
			return applied, fmt.Errorf("backup_runner_raft_catchup_failed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// ValidateBackupJobSafetyLocal evaluates runner-local durable inventory in one
// database transaction and returns a typed receipt bound to this node.
func (s *Service) ValidateBackupJobSafetyLocal(
	ctx context.Context,
	request BackupJobSafetyValidationRequest,
) (BackupJobSafetyValidationResult, error) {
	request = normalizeBackupJobSafetyValidationRequest(request)
	result := BackupJobSafetyValidationResult{
		Mode:            request.Mode,
		SourceDataset:   request.SourceDataset,
		JailRootDataset: request.JailRootDataset,
	}
	if s == nil || s.DB == nil {
		return result, fmt.Errorf("backup_runner_validation_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	localNodeID := s.guestIdentityInventoryLocalNodeID()
	if localNodeID == "" {
		return result, fmt.Errorf("backup_runner_local_node_id_unavailable")
	}
	result.NodeID = localNodeID
	if request.ExpectedNodeID == "" || request.ExpectedNodeID != localNodeID {
		result.ValidationError = fmt.Sprintf(
			"backup_runner_identity_mismatch: expected=%s actual=%s",
			request.ExpectedNodeID,
			localNodeID,
		)
		result.Classification, result.ManagedScope = backupJobValidationClassification(request, errors.New(result.ValidationError))
		return result, nil
	}

	applied, err := s.waitForBackupJobValidationAppliedIndex(ctx, request.MinimumRaftAppliedIndex)
	if err != nil {
		return result, err
	}
	result.RaftAppliedIndex = applied
	result.GuestType, result.GuestID = backupJobValidationIdentity(request)

	var validationErr error
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job := &clusterModels.BackupJob{
			RunnerNodeID:    localNodeID,
			Mode:            request.Mode,
			SourceDataset:   request.SourceDataset,
			JailRootDataset: request.JailRootDataset,
			Recursive:       request.Recursive,
		}
		validationErr = (&Service{DB: tx}).ValidateBackupJobSafety(ctx, job)
		result.Classification, result.ManagedScope = backupJobValidationClassification(request, validationErr)
		if validationErr != nil {
			return nil
		}

		friendly, err := backupJobFriendlySourceWithDB(tx, request, result.GuestID)
		if err != nil {
			return fmt.Errorf("backup_runner_friendly_source_failed: %w", err)
		}
		result.FriendlySource = friendly

		if result.GuestType != "" && result.GuestID != 0 {
			fence, err := clusterModels.LoadBackupJobPlacementFence(
				tx,
				result.GuestType,
				result.GuestID,
				localNodeID,
			)
			if err != nil {
				return err
			}
			result.PlacementFence = &fence
			result.OwnerNodeID, result.OwnerEpoch = backupJobPlacementOwner(&fence, localNodeID)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("backup_runner_validation_canceled: %w", err)
	}
	if validationErr != nil {
		result.ValidationError = validationErr.Error()
		return result, nil
	}
	result.Valid = true
	return result, nil
}

func (s *Service) backupJobRunnerVoter(nodeID string) (raft.Server, bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return raft.Server{}, false, fmt.Errorf("backup_runner_node_id_required")
	}
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return raft.Server{}, false, fmt.Errorf("backup_runner_raft_unavailable")
	}
	localNodeID := s.guestIdentityInventoryLocalNodeID()
	if localNodeID == "" {
		return raft.Server{}, false, fmt.Errorf("backup_runner_local_node_id_unavailable")
	}

	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return raft.Server{}, false, fmt.Errorf("backup_runner_raft_configuration_failed: %w", err)
	}
	matches := make([]raft.Server, 0, 1)
	localPresent := false
	for _, server := range future.Configuration().Servers {
		serverID := strings.TrimSpace(string(server.ID))
		if serverID == localNodeID {
			localPresent = true
		}
		if serverID == nodeID {
			matches = append(matches, server)
		}
	}
	if !localPresent {
		return raft.Server{}, false, fmt.Errorf("backup_runner_local_node_not_in_raft_configuration")
	}
	if len(matches) != 1 {
		return raft.Server{}, false, fmt.Errorf("backup_runner_not_raft_member: node_id=%s", nodeID)
	}
	server := matches[0]
	if server.Suffrage != raft.Voter {
		return raft.Server{}, false, fmt.Errorf("backup_runner_not_raft_voter: node_id=%s", nodeID)
	}
	return server, nodeID == localNodeID, nil
}

func (s *Service) backupJobValidationAPI(nodeID string, address raft.ServerAddress) (string, error) {
	var endpoint string
	var err error
	if s.backupJobValidationAPIForNode != nil {
		endpoint, err = s.backupJobValidationAPIForNode(nodeID, address)
		if err != nil {
			return "", fmt.Errorf("backup_runner_api_resolve_failed: node_id=%s: %w", nodeID, err)
		}
	} else {
		host := strings.TrimSpace(raftAddressHost(string(address)))
		if host == "" {
			return "", fmt.Errorf("backup_runner_api_resolve_failed: node_id=%s: empty_raft_address", nodeID)
		}
		endpoint = ClusterAPIHost(host)
	}
	endpoint, err = normalizeGuestIdentityInventoryAPIEndpoint(endpoint)
	if err != nil {
		return "", fmt.Errorf("backup_runner_api_resolve_failed: node_id=%s: %w", nodeID, err)
	}
	return endpoint, nil
}

func (s *Service) fetchBackupJobSafetyValidation(
	ctx context.Context,
	nodeID, endpoint string,
	request BackupJobSafetyValidationRequest,
) (BackupJobSafetyValidationResult, error) {
	var result BackupJobSafetyValidationResult
	if s == nil || s.AuthService == nil {
		return result, fmt.Errorf("backup_runner_auth_service_unavailable")
	}
	localNodeID := s.guestIdentityInventoryLocalNodeID()
	clusterToken, err := s.AuthService.CreateInternalClusterJWT(localNodeID, "")
	if err != nil {
		return result, fmt.Errorf("backup_runner_cluster_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return result, fmt.Errorf("backup_runner_validation_marshal_failed: %w", err)
	}
	body, statusCode, err := utils.HTTPPostJSONWithTimeoutContext(
		ctx,
		fmt.Sprintf("https://%s/api/intra-cluster/backup-job-safety-validation", endpoint),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
		backupJobValidationTimeout,
	)
	if err != nil {
		return result, fmt.Errorf(
			"backup_runner_validation_request_failed: node_id=%s status=%d: %w",
			nodeID,
			statusCode,
			err,
		)
	}

	var response internal.APIResponse[BackupJobSafetyValidationResult]
	if err := json.Unmarshal(body, &response); err != nil {
		return result, fmt.Errorf("backup_runner_validation_decode_failed: node_id=%s: %w", nodeID, err)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Status), "success") {
		return result, fmt.Errorf(
			"backup_runner_validation_non_success: node_id=%s message=%s error=%s",
			nodeID,
			strings.TrimSpace(response.Message),
			strings.TrimSpace(response.Error),
		)
	}
	return response.Data, nil
}

func validateBackupJobSafetyReceipt(
	request BackupJobSafetyValidationRequest,
	result BackupJobSafetyValidationResult,
) error {
	if strings.TrimSpace(result.NodeID) != request.ExpectedNodeID {
		return fmt.Errorf(
			"backup_runner_identity_mismatch: expected=%s actual=%s",
			request.ExpectedNodeID,
			strings.TrimSpace(result.NodeID),
		)
	}
	if result.RaftAppliedIndex < request.MinimumRaftAppliedIndex {
		return fmt.Errorf(
			"backup_runner_raft_state_stale: minimum=%d actual=%d",
			request.MinimumRaftAppliedIndex,
			result.RaftAppliedIndex,
		)
	}
	if strings.ToLower(strings.TrimSpace(result.Mode)) != request.Mode ||
		normalizeManagedGuestDatasetPath(result.SourceDataset) != request.SourceDataset ||
		normalizeManagedGuestDatasetPath(result.JailRootDataset) != request.JailRootDataset {
		return fmt.Errorf("backup_runner_validation_scope_mismatch")
	}
	if !result.Valid {
		validationErr := strings.TrimSpace(result.ValidationError)
		if validationErr == "" {
			validationErr = "backup_runner_validation_rejected"
		}
		return errors.New(validationErr)
	}

	expectedGuestType, expectedGuestID := backupJobValidationIdentity(request)
	if result.GuestType != expectedGuestType || result.GuestID != expectedGuestID {
		return fmt.Errorf("backup_runner_validation_guest_identity_mismatch")
	}
	expectedClassification, expectedManaged := backupJobValidationClassification(request, nil)
	if strings.TrimSpace(result.Classification) != expectedClassification || result.ManagedScope != expectedManaged {
		return fmt.Errorf("backup_runner_validation_classification_mismatch")
	}
	if expectedGuestType != "" {
		if result.PlacementFence == nil {
			return fmt.Errorf("backup_runner_validation_placement_missing")
		}
		if result.PlacementFence.GuestType != expectedGuestType ||
			result.PlacementFence.GuestID != expectedGuestID ||
			strings.TrimSpace(result.PlacementFence.RunnerNodeID) != request.ExpectedNodeID {
			return fmt.Errorf("backup_runner_validation_placement_identity_mismatch")
		}
	} else if result.PlacementFence != nil {
		return fmt.Errorf("backup_runner_validation_unexpected_placement")
	}
	return nil
}

func authorizeBackupJobPlacement(
	fence *clusterModels.BackupJobPlacementFence,
	authorization BackupJobPlacementAuthorization,
	requireRunnerOwnership bool,
) error {
	if fence == nil {
		return nil
	}
	authorization.GuestOperationToken = strings.TrimSpace(authorization.GuestOperationToken)
	authorization.TransitionRunID = strings.TrimSpace(authorization.TransitionRunID)

	if fence.OperationPresent {
		if authorization.GuestOperationToken == "" || authorization.GuestOperationToken != fence.OperationToken {
			return fmt.Errorf("backup_job_guest_operation_in_progress: %s", fence.Operation)
		}
	} else if authorization.GuestOperationToken != "" {
		return fmt.Errorf("backup_job_guest_operation_authorization_stale")
	}

	transitioning := fence.PolicyPresent && replicationPolicyTransitionInProgress(fence.PolicyTransitionState)
	if transitioning {
		if authorization.TransitionRunID == "" || authorization.TransitionRunID != fence.PolicyTransitionRunID {
			return fmt.Errorf("backup_job_guest_transition_in_progress: %s", fence.PolicyTransitionState)
		}
	} else if authorization.TransitionRunID != "" {
		return fmt.Errorf("backup_job_guest_transition_authorization_stale")
	}

	if requireRunnerOwnership && fence.PolicyPresent && fence.PolicyEnabled {
		owner := strings.TrimSpace(fence.PolicyActiveNodeID)
		if owner == "" {
			owner = strings.TrimSpace(fence.PolicySourceNodeID)
		}
		if owner == "" || owner != strings.TrimSpace(fence.RunnerNodeID) {
			return fmt.Errorf("backup_runner_not_guest_owner")
		}
		if fence.PolicyOwnerEpoch == 0 {
			return fmt.Errorf("backup_runner_owner_epoch_missing")
		}
	}
	return nil
}

func (s *Service) backupJobPreviousPlacementFence(
	ctx context.Context,
	jobID uint,
	authorization BackupJobPlacementAuthorization,
) (*clusterModels.BackupJobPlacementFence, error) {
	if s == nil || s.DB == nil || jobID == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var existing clusterModels.BackupJob
	result := s.DB.WithContext(ctx).Where("id = ?", jobID).Limit(1).Find(&existing)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	request := backupJobValidationRequest(&existing, existing.RunnerNodeID, 0)
	guestType, guestID := backupJobValidationIdentity(request)
	if guestType == "" || guestID == 0 || strings.TrimSpace(existing.RunnerNodeID) == "" {
		return nil, nil
	}

	fence, err := clusterModels.LoadBackupJobPlacementFence(
		s.DB.WithContext(ctx),
		guestType,
		guestID,
		existing.RunnerNodeID,
	)
	if err != nil {
		return nil, err
	}
	if err := authorizeBackupJobPlacement(&fence, authorization, false); err != nil {
		return nil, err
	}
	return &fence, nil
}

func (s *Service) validateBackupJobOnRunner(
	ctx context.Context,
	job *clusterModels.BackupJob,
	bypassRaft bool,
	authorization BackupJobPlacementAuthorization,
) (BackupJobSafetyValidationResult, *clusterModels.BackupJobPlacementFence, error) {
	var empty BackupJobSafetyValidationResult
	if job == nil {
		return empty, nil, fmt.Errorf("backup_job_required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runnerNodeID := strings.TrimSpace(job.RunnerNodeID)
	if runnerNodeID == "" {
		return empty, nil, fmt.Errorf("backup_runner_node_id_required")
	}

	if bypassRaft || s.Raft == nil {
		request := backupJobValidationRequest(job, runnerNodeID, 0)
		result, err := s.ValidateBackupJobSafetyLocal(ctx, request)
		if err != nil {
			return empty, nil, err
		}
		if err := validateBackupJobSafetyReceipt(request, result); err != nil {
			return empty, nil, err
		}
		if err := authorizeBackupJobPlacement(result.PlacementFence, authorization, true); err != nil {
			return empty, nil, err
		}
		return result, result.PlacementFence, nil
	}

	server, local, err := s.backupJobRunnerVoter(runnerNodeID)
	if err != nil {
		return empty, nil, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		minimumIndex := s.Raft.AppliedIndex()
		request := backupJobValidationRequest(job, runnerNodeID, minimumIndex)
		var result BackupJobSafetyValidationResult
		if local {
			result, err = s.ValidateBackupJobSafetyLocal(ctx, request)
		} else {
			endpoint, resolveErr := s.backupJobValidationAPI(runnerNodeID, server.Address)
			if resolveErr != nil {
				return empty, nil, resolveErr
			}
			result, err = s.fetchBackupJobSafetyValidation(ctx, runnerNodeID, endpoint, request)
		}
		if err != nil {
			return empty, nil, err
		}
		if err := validateBackupJobSafetyReceipt(request, result); err != nil {
			return empty, nil, err
		}

		var leaderFence *clusterModels.BackupJobPlacementFence
		if result.GuestType != "" && result.GuestID != 0 {
			loaded, loadErr := clusterModels.LoadBackupJobPlacementFence(
				s.DB.WithContext(ctx),
				result.GuestType,
				result.GuestID,
				runnerNodeID,
			)
			if loadErr != nil {
				return empty, nil, loadErr
			}
			leaderFence = &loaded
			if result.PlacementFence == nil ||
				!clusterModels.BackupJobPlacementFencesEqual(*result.PlacementFence, loaded) {
				if attempt < 2 {
					continue
				}
				return empty, nil, fmt.Errorf("backup_runner_placement_state_mismatch")
			}
		}
		if err := authorizeBackupJobPlacement(leaderFence, authorization, true); err != nil {
			return empty, nil, err
		}
		return result, leaderFence, nil
	}
	return empty, nil, fmt.Errorf("backup_runner_validation_failed")
}
