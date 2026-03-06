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
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const replicationJobQueueName = "zelta-replication-run"

const (
	defaultReplicationPruneKeepLast  = 64
	defaultReplicationLineageKeepOld = 2
	replicationOrphanCleanupInterval = 5 * time.Minute

	replicationEventStatusRunning   = "running"
	replicationEventStatusDemoting  = "demoting"
	replicationEventStatusPromoting = "promoting"
	replicationEventStatusActive    = "active"
	replicationEventStatusSuccess   = "success"
	replicationEventStatusFailed    = "failed"
)

type replicationJobPayload struct {
	PolicyID uint `json:"policy_id"`
}

type ReplicationEventProgress struct {
	Event           *clusterModels.ReplicationEvent `json:"event"`
	MovedBytes      *uint64                         `json:"movedBytes"`
	TotalBytes      *uint64                         `json:"totalBytes"`
	ProgressPercent *float64                        `json:"progressPercent"`
}

var errReplicationPolicyTransitionAlreadyRunning = errors.New("replication_policy_transition_already_running")

func isReplicationPolicyTransitionRunningError(err error) bool {
	return errors.Is(err, errReplicationPolicyTransitionAlreadyRunning)
}

func (s *Service) registerReplicationJob() {
	db.QueueRegisterJSON(replicationJobQueueName, func(ctx context.Context, payload replicationJobPayload) error {
		if payload.PolicyID == 0 {
			return fmt.Errorf("invalid_policy_id_in_queue_payload")
		}

		policy, err := s.Cluster.GetReplicationPolicyByID(payload.PolicyID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_not_found")
			return err
		}

		if err := s.runReplicationPolicy(ctx, policy); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_failed")
			return err
		}
		return nil
	})
}

func (s *Service) EnqueueReplicationPolicyRun(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}
	if !s.acquireReplication(policyID) {
		return fmt.Errorf("replication_policy_already_running")
	}
	s.releaseReplication(policyID)

	if ownershipErr := s.validateLocalReplicationPolicyLease(policy); ownershipErr != nil {
		return fmt.Errorf("replication_policy_local_ownership_invalid: %w", ownershipErr)
	}

	return db.EnqueueJSON(ctx, replicationJobQueueName, replicationJobPayload{PolicyID: policyID})
}

func (s *Service) StartReplicationScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastSSHSync := time.Time{}
	lastOrphanCleanup := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.Cluster != nil && time.Since(lastSSHSync) > 30*time.Second {
				if err := s.Cluster.EnsureAndPublishLocalSSHIdentity(); err != nil {
					logger.L.Warn().Err(err).Msg("cluster_ssh_identity_sync_failed")
				}
				lastSSHSync = time.Now()
			}

			if err := s.selfFenceExpiredLeases(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("replication_self_fence_check_failed")
			}

			if err := s.runReplicationSchedulerTick(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("replication_scheduler_tick_failed")
			}

			if time.Since(lastOrphanCleanup) > replicationOrphanCleanupInterval {
				if err := s.runOrphanReplicationSnapshotCleanupTick(ctx); err != nil {
					logger.L.Warn().Err(err).Msg("replication_orphan_snapshot_cleanup_tick_failed")
				}
				lastOrphanCleanup = time.Now()
			}

			if s.Cluster != nil && s.Cluster.Raft != nil && s.Cluster.Raft.State() == raft.Leader {
				if err := s.runTransitionRecoveryTick(ctx); err != nil {
					logger.L.Warn().Err(err).Msg("replication_transition_recovery_tick_failed")
				}

				if err := s.runFailoverControllerTick(ctx); err != nil {
					logger.L.Warn().Err(err).Msg("replication_failover_tick_failed")
				}
			}
		}
	}
}

func transitionStateInProgress(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup,
		clusterModels.ReplicationTransitionStatePromoting:
		return true
	default:
		return false
	}
}

func transitionDemoteAckRequired(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return true
	}
	return !strings.Contains(reason, "node_down_failover")
}

func transitionPayloadFromPolicy(policy *clusterModels.ReplicationPolicy) clusterModels.ReplicationPolicyTransition {
	if policy == nil {
		return clusterModels.ReplicationPolicyTransition{}
	}
	return clusterModels.ReplicationPolicyTransition{
		State:        policy.TransitionState,
		RunID:        policy.TransitionRunID,
		Reason:       policy.TransitionReason,
		SourceNodeID: policy.TransitionSourceNodeID,
		TargetNodeID: policy.TransitionTargetNodeID,
		OwnerEpoch:   policy.TransitionOwnerEpoch,
		RequestedAt:  policy.TransitionRequestedAt,
		DemotedAt:    policy.TransitionDemotedAt,
		CatchupAt:    policy.TransitionCatchupAt,
		PromotedAt:   policy.TransitionPromotedAt,
		CompletedAt:  policy.TransitionCompletedAt,
		Error:        policy.TransitionError,
	}
}

func (s *Service) failPolicyTransition(policy *clusterModels.ReplicationPolicy, transitionErr error) error {
	if policy == nil || policy.ID == 0 || s.Cluster == nil {
		return transitionErr
	}

	transition := transitionPayloadFromPolicy(policy)
	transition.State = clusterModels.ReplicationTransitionStateFailed
	now := time.Now().UTC()
	transition.CompletedAt = &now
	if transitionErr != nil {
		transition.Error = transitionErr.Error()
	} else {
		transition.Error = "transition_failed"
	}

	if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition, false); err != nil {
		if transitionErr != nil {
			return fmt.Errorf("%v; transition_checkpoint_persist_failed: %v", transitionErr, err)
		}
		return fmt.Errorf("transition_checkpoint_persist_failed: %w", err)
	}

	policy.TransitionState = clusterModels.ReplicationTransitionStateFailed
	policy.TransitionCompletedAt = &now
	policy.TransitionError = transition.Error
	return transitionErr
}

func (s *Service) resumePromotingTransition(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy_transition_input")
	}

	targetNodeID := strings.TrimSpace(policy.TransitionTargetNodeID)
	if targetNodeID == "" {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_transition_target_missing"))
	}

	ownerNodeID := replicationPolicyOwnerNode(policy)
	if ownerNodeID != targetNodeID {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_transition_owner_target_mismatch"))
	}

	targetOnline, targetOnlineErr := s.isClusterNodeOnline(targetNodeID)
	if targetOnlineErr != nil {
		return s.failPolicyTransition(policy, targetOnlineErr)
	}
	if !targetOnline {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_target_node_offline"))
	}

	var activateErr error
	if targetNodeID == strings.TrimSpace(s.Cluster.LocalNodeID()) {
		activateErr = s.ActivateReplicationPolicy(ctx, policy.ID)
	} else {
		activateErr = s.forwardActivateReplicationPolicy(targetNodeID, policy.ID)
	}
	if activateErr != nil {
		return s.failPolicyTransition(policy, activateErr)
	}

	transition := transitionPayloadFromPolicy(policy)
	now := time.Now().UTC()
	if transition.PromotedAt == nil {
		transition.PromotedAt = &now
	}
	transition.State = clusterModels.ReplicationTransitionStateCompleted
	transition.CompletedAt = &now
	transition.OwnerEpoch = replicationPolicyOwnerEpoch(policy)
	transition.Error = ""
	if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition, false); err != nil {
		return err
	}

	policy.TransitionState = clusterModels.ReplicationTransitionStateCompleted
	policy.TransitionPromotedAt = transition.PromotedAt
	policy.TransitionCompletedAt = &now
	policy.TransitionOwnerEpoch = transition.OwnerEpoch
	policy.TransitionError = ""
	return nil
}

func (s *Service) resumePolicyTransition(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy_transition_input")
	}

	state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
	if !transitionStateInProgress(state) {
		return nil
	}

	targetNodeID := strings.TrimSpace(policy.TransitionTargetNodeID)
	if targetNodeID == "" {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_transition_target_missing"))
	}

	reason := strings.TrimSpace(policy.TransitionReason)
	if reason == "" {
		reason = "transition_recovery"
	}

	switch state {
	case clusterModels.ReplicationTransitionStatePromoting:
		return s.resumePromotingTransition(ctx, policy)
	case clusterModels.ReplicationTransitionStateDemoting, clusterModels.ReplicationTransitionStateCatchup:
		ownerNodeID := replicationPolicyOwnerNode(policy)
		if ownerNodeID == targetNodeID {
			transition := transitionPayloadFromPolicy(policy)
			transition.State = clusterModels.ReplicationTransitionStatePromoting
			transition.Error = ""
			if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition, false); err != nil {
				return err
			}
			policy.TransitionState = clusterModels.ReplicationTransitionStatePromoting
			policy.TransitionError = ""
			return s.resumePromotingTransition(ctx, policy)
		}

		return s.runPolicyOwnershipTransition(
			ctx,
			policy,
			targetNodeID,
			reason+"_resume",
			transitionDemoteAckRequired(reason),
		)
	default:
		return nil
	}
}

func (s *Service) runTransitionRecoveryTick(ctx context.Context) error {
	if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return nil
	}

	policies, err := s.Cluster.ListReplicationPolicies()
	if err != nil {
		return err
	}

	for i := range policies {
		policy := policies[i]
		if !transitionStateInProgress(policy.TransitionState) {
			continue
		}
		if !s.acquirePolicyTransition(policy.ID) {
			continue
		}

		resumeErr := s.resumePolicyTransition(ctx, &policy)
		s.releasePolicyTransition(policy.ID)
		if resumeErr != nil {
			logger.L.Warn().
				Err(resumeErr).
				Uint("policy_id", policy.ID).
				Str("transition_state", policy.TransitionState).
				Str("transition_target_node_id", strings.TrimSpace(policy.TransitionTargetNodeID)).
				Msg("replication_transition_recovery_failed")
		}
	}

	return nil
}

func replicationGuestKey(guestType string, guestID uint) string {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if guestType == "" || guestID == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", guestType, guestID)
}

func parseReplicationGuestKey(key string) (string, uint, bool) {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return "", 0, false
	}

	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}

	guestType := strings.TrimSpace(parts[0])
	if guestType != clusterModels.ReplicationGuestTypeJail && guestType != clusterModels.ReplicationGuestTypeVM {
		return "", 0, false
	}

	parsedID, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || parsedID == 0 {
		return "", 0, false
	}

	return guestType, uint(parsedID), true
}

func isHAReplicationSnapshotShortName(snapshotName string) bool {
	value := strings.ToLower(strings.TrimSpace(snapshotName))
	if value == "" {
		return false
	}
	return strings.HasPrefix(value, "ha_")
}

func buildOrphanHAReplicationPruneCandidates(snapshots []SnapshotInfo) []string {
	if len(snapshots) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(snapshots))
	out := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		fullName := strings.TrimSpace(snapshot.Name)
		if !isValidZFSSnapshotName(fullName) {
			continue
		}
		if !isHAReplicationSnapshotShortName(snapshotShortName(snapshot)) {
			continue
		}
		if _, ok := seen[fullName]; ok {
			continue
		}
		seen[fullName] = struct{}{}
		out = append(out, fullName)
	}

	sort.Strings(out)
	return out
}

func (s *Service) runOrphanReplicationSnapshotCleanupTick(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Select("guest_type", "guest_id").Find(&policies).Error; err != nil {
		return err
	}

	protectedGuests := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		key := replicationGuestKey(policy.GuestType, policy.GuestID)
		if key != "" {
			protectedGuests[key] = struct{}{}
		}
	}

	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return err
	}

	orphanGuests := make(map[string]struct{})
	for _, dataset := range datasets {
		guestType, guestID := inferRestoreDatasetKind(dataset)
		if guestType != clusterModels.ReplicationGuestTypeJail && guestType != clusterModels.ReplicationGuestTypeVM {
			continue
		}

		key := replicationGuestKey(guestType, guestID)
		if key == "" {
			continue
		}
		if _, protected := protectedGuests[key]; protected {
			continue
		}
		orphanGuests[key] = struct{}{}
	}

	for key := range orphanGuests {
		guestType, guestID, ok := parseReplicationGuestKey(key)
		if !ok {
			continue
		}

		roots, findErr := s.findLocalGuestDatasets(ctx, guestType, guestID)
		if findErr != nil {
			logger.L.Warn().
				Str("guest_type", guestType).
				Uint("guest_id", guestID).
				Err(findErr).
				Msg("replication_orphan_cleanup_list_guest_roots_failed")
			continue
		}

		for _, root := range roots {
			snapshots, snapErr := s.listLocalSnapshotsForDataset(ctx, root)
			if snapErr != nil {
				logger.L.Warn().
					Str("guest_type", guestType).
					Uint("guest_id", guestID).
					Str("dataset", root).
					Err(snapErr).
					Msg("replication_orphan_cleanup_list_snapshots_failed")
				continue
			}

			pruneCandidates := buildOrphanHAReplicationPruneCandidates(snapshots)
			if len(pruneCandidates) == 0 {
				continue
			}

			if destroyErr := s.DestroySnapshots(ctx, pruneCandidates); destroyErr != nil {
				logger.L.Warn().
					Str("guest_type", guestType).
					Uint("guest_id", guestID).
					Str("dataset", root).
					Int("snapshots", len(pruneCandidates)).
					Err(destroyErr).
					Msg("replication_orphan_cleanup_destroy_snapshots_failed")
				continue
			}

			logger.L.Info().
				Str("guest_type", guestType).
				Uint("guest_id", guestID).
				Str("dataset", root).
				Int("snapshots_deleted", len(pruneCandidates)).
				Msg("replication_orphan_snapshots_cleaned")
		}
	}

	return nil
}

func (s *Service) runReplicationSchedulerTick(ctx context.Context) error {
	if s.DB == nil || s.Cluster == nil {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Preload("Targets").Where("enabled = ? AND COALESCE(cron_expr, '') != ''", true).Find(&policies).Error; err != nil {
		return err
	}

	now := time.Now().UTC()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	for i := range policies {
		policy := policies[i]
		runnerNodeID := s.replicationRunnerNodeID(&policy)
		if runnerNodeID != "" && localNodeID != "" && runnerNodeID != localNodeID {
			continue
		}
		if runnerNodeID == "" && s.Cluster.Raft != nil && s.Cluster.Raft.State() != raft.Leader {
			continue
		}
		if ownershipErr := s.validateLocalReplicationPolicyLease(&policy); ownershipErr != nil {
			logger.L.Warn().
				Err(ownershipErr).
				Uint("policy_id", policy.ID).
				Msg("replication_policy_scheduler_skip_invalid_local_ownership")
			continue
		}

		nextAt, err := nextRunTime(policy.CronExpr, now)
		if err != nil {
			_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(map[string]any{
				"last_status": "failed",
				"last_error":  "invalid_cron_expr",
				"next_run_at": nil,
			}).Error
			continue
		}

		if policy.NextRunAt == nil {
			_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Update("next_run_at", nextAt).Error
			continue
		}

		if now.Before(*policy.NextRunAt) {
			continue
		}

		if err := s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Update("next_run_at", nextAt).Error; err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("failed_to_update_replication_policy_next_run")
			continue
		}

		enqueueCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := db.EnqueueJSON(enqueueCtx, replicationJobQueueName, replicationJobPayload{PolicyID: policy.ID}); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("failed_to_enqueue_replication_policy")
		}
		cancel()
	}

	return nil
}

func (s *Service) replicationRunnerNodeID(policy *clusterModels.ReplicationPolicy) string {
	if policy == nil {
		return ""
	}
	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned {
		return strings.TrimSpace(policy.SourceNodeID)
	}
	return strings.TrimSpace(policy.ActiveNodeID)
}

func replicationPolicyOwnerNode(policy *clusterModels.ReplicationPolicy) string {
	if policy == nil {
		return ""
	}
	owner := strings.TrimSpace(policy.ActiveNodeID)
	if owner == "" {
		owner = strings.TrimSpace(policy.SourceNodeID)
	}
	return owner
}

func replicationPolicyOwnerEpoch(policy *clusterModels.ReplicationPolicy) uint64 {
	if policy == nil {
		return 0
	}
	return policy.OwnerEpoch
}

func (s *Service) validateLocalReplicationPolicyLease(policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}

	policyOwner := replicationPolicyOwnerNode(policy)
	if policyOwner == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if policyOwner != localNodeID {
		return fmt.Errorf("replication_policy_not_owned_by_local_node")
	}

	expectedEpoch := replicationPolicyOwnerEpoch(policy)
	if expectedEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_missing")
	}

	lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("replication_lease_missing")
		}
		return fmt.Errorf("replication_lease_lookup_failed: %w", err)
	}
	if lease == nil {
		return fmt.Errorf("replication_lease_missing")
	}

	leaseOwner := strings.TrimSpace(lease.OwnerNodeID)
	if leaseOwner == "" {
		return fmt.Errorf("replication_lease_owner_missing")
	}
	if leaseOwner != localNodeID {
		return fmt.Errorf("replication_lease_owner_mismatch")
	}
	if lease.OwnerEpoch == 0 {
		return fmt.Errorf("replication_lease_owner_epoch_missing")
	}
	if lease.OwnerEpoch != expectedEpoch {
		return fmt.Errorf("replication_lease_epoch_mismatch")
	}
	if time.Now().UTC().After(lease.ExpiresAt) {
		return fmt.Errorf("replication_lease_expired")
	}

	return nil
}

func (s *Service) fenceReplicationGuestDatasets(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	reason string,
) error {
	if policy == nil || policy.ID == 0 {
		return nil
	}

	datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return nil
	}

	var fenceErr error
	for _, dataset := range datasets {
		ds, getErr := s.getLocalDataset(ctx, dataset)
		if getErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, getErr)
			continue
		}
		if ds == nil {
			continue
		}
		if setErr := ds.SetProperties(ctx, "readonly", "on"); setErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, setErr)
			continue
		}

		logger.L.Info().
			Uint("policy_id", policy.ID).
			Str("dataset", dataset).
			Str("reason", strings.TrimSpace(reason)).
			Msg("replication_dataset_self_fenced_readonly")
	}

	return fenceErr
}

func appendReplicationFenceDatasetError(baseErr error, dataset string, datasetErr error) error {
	if datasetErr == nil {
		return baseErr
	}
	if baseErr == nil {
		return fmt.Errorf("fence_dataset_%s_failed: %w", dataset, datasetErr)
	}
	return fmt.Errorf("%v; fence_dataset_%s_failed: %v", baseErr, dataset, datasetErr)
}

func (s *Service) runReplicationPolicy(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	if !s.acquireReplication(policy.ID) {
		return fmt.Errorf("replication_policy_already_running")
	}
	defer s.releaseReplication(policy.ID)

	localNodeID := ""
	if s.Cluster != nil {
		localNodeID = strings.TrimSpace(s.Cluster.LocalNodeID())
	}

	runner := s.replicationRunnerNodeID(policy)
	if runner != "" && localNodeID != "" && runner != localNodeID {
		return fmt.Errorf("policy_runner_mismatch")
	}

	if ownershipErr := s.validateLocalReplicationPolicyLease(policy); ownershipErr != nil {
		runErr := fmt.Errorf("replication_policy_local_ownership_invalid: %w", ownershipErr)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	sourceDatasets, err := s.replicationSourceDatasets(ctx, policy)
	if err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}
	if len(sourceDatasets) == 0 {
		runErr := fmt.Errorf("no_source_datasets_found")
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	identities, err := s.Cluster.ListClusterSSHIdentities()
	if err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}
	identityByNode := make(map[string]clusterModels.ClusterSSHIdentity, len(identities))
	for _, identity := range identities {
		identityByNode[strings.TrimSpace(identity.NodeUUID)] = identity
	}

	nodes, _ := s.Cluster.Nodes()
	statusByNode := make(map[string]string, len(nodes))
	for _, node := range nodes {
		statusByNode[strings.TrimSpace(node.NodeUUID)] = strings.TrimSpace(strings.ToLower(node.Status))
	}

	event := clusterModels.ReplicationEvent{
		PolicyID:     &policy.ID,
		EventType:    "replication",
		Status:       replicationEventStatusRunning,
		SourceNodeID: localNodeID,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		StartedAt:    time.Now().UTC(),
		Message:      "replication_run_started",
	}
	if err := s.DB.Create(&event).Error; err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		runErr := fmt.Errorf("cluster_ssh_private_key_path_failed: %w", err)
		s.finalizeReplicationEvent(&event, runErr)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	targets := append([]clusterModels.ReplicationPolicyTarget{}, policy.Targets...)
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Weight == targets[j].Weight {
			return targets[i].NodeID < targets[j].NodeID
		}
		return targets[i].Weight > targets[j].Weight
	})

	var runErr error
	eligibleTargets := 0
	skippedOffline := 0
	skippedNoIdentity := 0
	attemptedTransfers := 0
	for _, target := range targets {
		targetNodeID := strings.TrimSpace(target.NodeID)
		if targetNodeID == "" || targetNodeID == localNodeID {
			continue
		}
		if status, ok := statusByNode[targetNodeID]; ok && status != "online" {
			skippedOffline++
			continue
		}

		identity, ok := identityByNode[targetNodeID]
		if !ok {
			skippedNoIdentity++
			continue
		}
		eligibleTargets++

		for _, sourceDataset := range sourceDatasets {
			attemptedTransfers++
			backupRoot, destSuffix := splitDatasetForTarget(sourceDataset)
			targetSpec := &clusterModels.BackupTarget{
				SSHHost:    fmt.Sprintf("%s@%s", strings.TrimSpace(identity.SSHUser), strings.TrimSpace(identity.SSHHost)),
				SSHPort:    identity.SSHPort,
				SSHKeyPath: privateKeyPath,
				BackupRoot: backupRoot,
				Enabled:    true,
			}
			event.TargetNodeID = targetNodeID
			event.Message = fmt.Sprintf("replicating_%s_to_%s", sourceDataset, targetNodeID)
			_ = s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
				"target_node_id": targetNodeID,
				"message":        event.Message,
			}).Error

			out, err := s.replicateWithEventProgress(ctx, targetSpec, sourceDataset, destSuffix, event.ID)
			if strings.TrimSpace(out) != "" {
				_ = s.AppendReplicationEventOutput(event.ID, out)
			}
			if err != nil {
				if isReplicationTargetModifiedError(err) {
					_ = s.AppendReplicationEventOutput(event.ID, "target_dataset_diverged_attempting_zelta_rotate")
					rotateOut, rotateErr := s.RotateWithTargetAndPrefix(ctx, targetSpec, sourceDataset, destSuffix, "ha")
					if strings.TrimSpace(rotateOut) != "" {
						_ = s.AppendReplicationEventOutput(event.ID, rotateOut)
					}
					if rotateErr != nil {
						runErr = fmt.Errorf(
							"replication_to_target_%s_failed_after_diverged_target_rotate_failed: %w (original: %v)",
							targetNodeID,
							rotateErr,
							err,
						)
						break
					}

					retryOut, retryErr := s.replicateWithEventProgress(ctx, targetSpec, sourceDataset, destSuffix, event.ID)
					if strings.TrimSpace(retryOut) != "" {
						_ = s.AppendReplicationEventOutput(event.ID, retryOut)
					}
					if retryErr != nil {
						runErr = fmt.Errorf(
							"replication_to_target_%s_failed_after_diverged_target_rotate: %w (original: %v)",
							targetNodeID,
							retryErr,
							err,
						)
						break
					}
				} else {
					runErr = fmt.Errorf("replication_to_target_%s_failed: %w", targetNodeID, err)
					break
				}
			}

			if retentionErr := s.applyReplicationRetention(ctx, targetSpec, sourceDataset, destSuffix, event.ID); retentionErr != nil {
				logger.L.Warn().
					Err(retentionErr).
					Uint("policy_id", policy.ID).
					Str("source_dataset", sourceDataset).
					Str("target_node_id", targetNodeID).
					Msg("replication_retention_post_run_failed")
				_ = s.AppendReplicationEventOutput(event.ID, fmt.Sprintf("replication_retention_warning: %v", retentionErr))
			}
		}

		if runErr != nil {
			break
		}
	}

	if runErr == nil {
		if eligibleTargets == 0 {
			runErr = fmt.Errorf("no_eligible_replication_targets (offline=%d missing_identity=%d)", skippedOffline, skippedNoIdentity)
		} else if attemptedTransfers == 0 {
			runErr = fmt.Errorf("no_replication_transfers_executed")
		}
	}

	s.finalizeReplicationEvent(&event, runErr)
	s.updateReplicationPolicyResult(policy, runErr)

	return runErr
}

func splitDatasetForTarget(dataset string) (string, string) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return "zroot", "sylve"
	}

	idx := strings.Index(dataset, "/")
	if idx <= 0 || idx >= len(dataset)-1 {
		return dataset, ""
	}
	return dataset[:idx], dataset[idx+1:]
}

func targetDatasetPath(root, suffix string) string {
	root = normalizeDatasetPath(root)
	suffix = normalizeDatasetPath(suffix)
	if root == "" {
		return suffix
	}
	if suffix == "" {
		return root
	}
	return root + "/" + suffix
}

func (s *Service) applyReplicationRetention(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	eventID uint,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	retentionErrors := make([]string, 0)

	pruneCandidates, pruneOutput, pruneErr := s.PruneCandidatesWithTarget(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		defaultReplicationPruneKeepLast,
	)
	if strings.TrimSpace(pruneOutput) != "" {
		_ = s.AppendReplicationEventOutput(eventID, pruneOutput)
	}
	if pruneErr != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("source_prune_scan_failed: %v", pruneErr))
	} else if len(pruneCandidates) > 0 {
		if err := s.DestroySnapshots(ctx, pruneCandidates); err != nil {
			retentionErrors = append(retentionErrors, fmt.Sprintf("source_prune_destroy_failed: %v", err))
		} else {
			_ = s.AppendReplicationEventOutput(eventID, fmt.Sprintf("source_prune_completed: %d", len(pruneCandidates)))
		}
	}

	targetPruneCandidates, targetPruneOutput, targetPruneErr := s.PruneTargetCandidatesWithSource(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		defaultReplicationPruneKeepLast,
	)
	if strings.TrimSpace(targetPruneOutput) != "" {
		_ = s.AppendReplicationEventOutput(eventID, targetPruneOutput)
	}
	if targetPruneErr != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("target_prune_scan_failed: %v", targetPruneErr))
	} else if len(targetPruneCandidates) > 0 {
		if err := s.DestroyTargetSnapshotsByName(ctx, target, targetPruneCandidates); err != nil {
			retentionErrors = append(retentionErrors, fmt.Sprintf("target_prune_destroy_failed: %v", err))
		} else {
			_ = s.AppendReplicationEventOutput(eventID, fmt.Sprintf("target_prune_completed: %d", len(targetPruneCandidates)))
		}
	}

	if err := s.trimLocalReplicationLineageDatasets(ctx, sourceDataset, defaultReplicationLineageKeepOld); err != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("local_lineage_trim_failed: %v", err))
	}

	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	if err := s.trimRemoteReplicationLineageDatasets(ctx, target, targetDataset, defaultReplicationLineageKeepOld); err != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("target_lineage_trim_failed: %v", err))
	}

	if len(retentionErrors) > 0 {
		return errors.New(strings.Join(retentionErrors, "; "))
	}

	return nil
}

func (s *Service) trimLocalReplicationLineageDatasets(
	ctx context.Context,
	rootDataset string,
	keepOutOfBand int,
) error {
	lineageDatasets, err := s.listLocalReplicationLineageDatasets(ctx, rootDataset)
	if err != nil {
		return err
	}

	staleDatasets := staleReplicationLineageDatasets(rootDataset, lineageDatasets, keepOutOfBand)
	for _, dataset := range staleDatasets {
		if err := s.destroyLocalDatasetWithRetry(ctx, dataset, true, 5, 500*time.Millisecond); err != nil {
			return fmt.Errorf("destroy_local_lineage_dataset_%s_failed: %w", dataset, err)
		}
	}

	return nil
}

func (s *Service) trimRemoteReplicationLineageDatasets(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset string,
	keepOutOfBand int,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}

	lineageDatasets, err := s.listRemoteLineageDatasets(ctx, target, remoteDataset)
	if err != nil {
		return err
	}

	staleDatasets := staleReplicationLineageDatasets(remoteDataset, lineageDatasets, keepOutOfBand)
	for _, dataset := range staleDatasets {
		script := fmt.Sprintf(
			`set -eu
ds=%q
if zfs list -H "$ds" >/dev/null 2>&1; then
  zfs destroy -r -f "$ds"
fi`,
			dataset,
		)
		if _, err := s.runTargetSSH(ctx, target, "sh", "-c", script); err != nil {
			return fmt.Errorf("destroy_remote_lineage_dataset_%s_failed: %w", dataset, err)
		}
	}

	return nil
}

func (s *Service) listLocalReplicationLineageDatasets(ctx context.Context, rootDataset string) ([]string, error) {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil, fmt.Errorf("root_dataset_required")
	}

	parent := rootDataset
	leaf := rootDataset
	if idx := strings.LastIndex(rootDataset, "/"); idx > 0 {
		parent = rootDataset[:idx]
		leaf = rootDataset[idx+1:]
	}
	baseLeaf := replicationLineageBaseLeaf(leaf)

	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	results := make([]string, 0)
	add := func(dataset string) {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			return
		}
		if _, ok := seen[dataset]; ok {
			return
		}
		seen[dataset] = struct{}{}
		results = append(results, dataset)
	}

	for _, dataset := range datasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			continue
		}
		if dataset == rootDataset {
			add(dataset)
			continue
		}
		if !strings.HasPrefix(dataset, parent+"/") {
			continue
		}
		if datasetDepth(dataset) != datasetDepth(rootDataset) {
			continue
		}

		suffix := strings.TrimPrefix(dataset, parent+"/")
		switch {
		case suffix == baseLeaf:
			add(dataset)
		case strings.HasPrefix(suffix, baseLeaf+"_gen-"):
			add(dataset)
		}
	}

	if len(results) == 0 {
		return []string{rootDataset}, nil
	}

	return results, nil
}

func staleReplicationLineageDatasets(rootDataset string, lineageDatasets []string, keepOutOfBand int) []string {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" || len(lineageDatasets) == 0 {
		return nil
	}

	if keepOutOfBand < 0 {
		keepOutOfBand = 0
	}

	rootLeaf := rootDataset
	if idx := strings.LastIndex(rootDataset, "/"); idx >= 0 && idx+1 < len(rootDataset) {
		rootLeaf = rootDataset[idx+1:]
	}
	baseLeaf := replicationLineageBaseLeaf(rootLeaf)

	outOfBand := make([]string, 0)
	for _, dataset := range lineageDatasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" || dataset == rootDataset {
			continue
		}

		leaf := dataset
		if idx := strings.LastIndex(dataset, "/"); idx >= 0 && idx+1 < len(dataset) {
			leaf = dataset[idx+1:]
		}

		if strings.HasPrefix(leaf, baseLeaf+"_gen-") {
			outOfBand = append(outOfBand, dataset)
		}
	}

	if len(outOfBand) <= keepOutOfBand {
		return nil
	}

	sort.SliceStable(outOfBand, func(i, j int) bool {
		return outOfBand[i] > outOfBand[j]
	})

	return outOfBand[keepOutOfBand:]
}

func replicationLineageBaseLeaf(leaf string) string {
	leaf = strings.TrimSpace(leaf)
	if idx := strings.Index(leaf, "_gen-"); idx > 0 {
		return leaf[:idx]
	}
	return leaf
}

func isReplicationTargetModifiedError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "destination") &&
		strings.Contains(lower, "has been modified")
}

func (s *Service) replicateWithEventProgress(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	eventID uint,
) (string, error) {
	return s.replicateWithTargetAndPrefixStreaming(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		"ha",
		func(line string) {
			if err := s.AppendReplicationEventOutput(eventID, line); err != nil {
				logger.L.Warn().Uint("event_id", eventID).Err(err).Msg("append_replication_event_output_failed")
			}
		},
	)
}

func (s *Service) replicateWithTargetAndPrefix(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	snapPrefix string,
) (string, error) {
	return s.replicateWithTargetAndPrefixStreaming(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		snapPrefix,
		nil,
	)
}

func (s *Service) replicateWithTargetAndPrefixStreaming(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	snapPrefix string,
	onLine func(string),
) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")
	snapshotName := zeltaSnapshotName(strings.TrimSpace(snapPrefix))
	if strings.TrimSpace(snapPrefix) == "" {
		snapshotName = zeltaSnapshotName("ha")
	}

	return runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		onLine,
		"backup",
		"--json",
		"--snap-name",
		snapshotName,
		sourceDataset,
		zeltaEndpoint,
	)
}

func (s *Service) replicationSourceDatasets(ctx context.Context, policy *clusterModels.ReplicationPolicy) ([]string, error) {
	if policy == nil {
		return nil, fmt.Errorf("policy_required")
	}

	driver, err := s.replicationGuestDriver(policy.GuestType)
	if err != nil {
		return nil, err
	}
	return driver.sourceDatasets(ctx, policy.GuestID)
}

func (s *Service) resolveJailReplicationSourceDataset(ctID uint) (string, error) {
	if ctID == 0 {
		return "", fmt.Errorf("invalid_jail_ctid")
	}

	var jail jailModels.Jail
	if err := s.DB.Preload("Storages").Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		return "", err
	}

	pool := ""
	for _, storage := range jail.Storages {
		if storage.IsBase {
			pool = strings.TrimSpace(storage.Pool)
			break
		}
	}
	if pool == "" && len(jail.Storages) > 0 {
		pool = strings.TrimSpace(jail.Storages[0].Pool)
	}
	if pool == "" {
		return "", fmt.Errorf("jail_pool_not_found")
	}

	return fmt.Sprintf("%s/sylve/jails/%d", pool, ctID), nil
}

func (s *Service) updateReplicationPolicyResult(policy *clusterModels.ReplicationPolicy, runErr error) {
	if policy == nil || policy.ID == 0 {
		return
	}

	now := time.Now().UTC()
	next := (*time.Time)(nil)
	if policy.Enabled {
		if n, err := nextRunTime(policy.CronExpr, now); err == nil {
			next = &n
		}
	}

	updates := map[string]any{
		"last_run_at": now,
		"last_status": "success",
		"last_error":  "",
		"next_run_at": next,
	}
	if runErr != nil {
		updates["last_status"] = "failed"
		updates["last_error"] = runErr.Error()
	}

	_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(updates).Error
}

func (s *Service) finalizeReplicationEvent(event *clusterModels.ReplicationEvent, runErr error) {
	if event == nil || event.ID == 0 {
		return
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	if runErr != nil {
		event.Status = replicationEventStatusFailed
		event.Error = runErr.Error()
		event.Message = "replication_run_failed"
	} else {
		event.Status = replicationEventStatusSuccess
		event.Error = ""
		event.Message = "replication_run_completed"
	}

	_ = s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
		"status":       event.Status,
		"error":        event.Error,
		"message":      event.Message,
		"completed_at": event.CompletedAt,
	}).Error
}

func (s *Service) AppendReplicationEventOutput(eventID uint, chunk string) error {
	chunk = strings.TrimSpace(chunk)
	if eventID == 0 || chunk == "" {
		return nil
	}
	return s.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("id = ?", eventID).
		Update("output", gorm.Expr("COALESCE(output, '') || ?", chunk+"\n")).Error
}

func (s *Service) GetReplicationEventProgress(_ context.Context, id uint) (*ReplicationEventProgress, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_event_id")
	}

	var event clusterModels.ReplicationEvent
	if err := s.DB.First(&event, id).Error; err != nil {
		return nil, err
	}

	out := &ReplicationEventProgress{
		Event:      &event,
		TotalBytes: parseTotalBytesFromOutput(event.Output),
		MovedBytes: parseMovedBytesFromOutput(event.Output),
	}

	if out.TotalBytes != nil && out.MovedBytes != nil && *out.TotalBytes > 0 {
		pct := (float64(*out.MovedBytes) / float64(*out.TotalBytes)) * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		out.ProgressPercent = &pct
	}

	return out, nil
}

func (s *Service) acquireReplication(policyID uint) bool {
	s.replicationMu.Lock()
	defer s.replicationMu.Unlock()
	if _, exists := s.runningReplication[policyID]; exists {
		return false
	}
	s.runningReplication[policyID] = struct{}{}
	return true
}

func (s *Service) releaseReplication(policyID uint) {
	s.replicationMu.Lock()
	defer s.replicationMu.Unlock()
	delete(s.runningReplication, policyID)
}

func (s *Service) acquirePolicyTransition(policyID uint) bool {
	if policyID == 0 {
		return false
	}

	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if _, exists := s.runningTransitions[policyID]; exists {
		return false
	}
	s.runningTransitions[policyID] = struct{}{}
	return true
}

func (s *Service) releasePolicyTransition(policyID uint) {
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	delete(s.runningTransitions, policyID)
}

func (s *Service) runFailoverControllerTick(ctx context.Context) error {
	if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return nil
	}

	nodes, err := s.Cluster.Nodes()
	if err != nil {
		return err
	}
	nodeByID := make(map[string]clusterModels.ClusterNode, len(nodes))
	for _, node := range nodes {
		nodeByID[strings.TrimSpace(node.NodeUUID)] = node
	}

	policies, err := s.Cluster.ListReplicationPolicies()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for i := range policies {
		policy := policies[i]
		if !policy.Enabled {
			continue
		}

		owner := replicationPolicyOwnerNode(&policy)
		if owner == "" {
			logger.L.Warn().Uint("policy_id", policy.ID).Msg("replication_policy_owner_missing")
			continue
		}
		ownerEpoch := replicationPolicyOwnerEpoch(&policy)
		if ownerEpoch == 0 {
			logger.L.Warn().Uint("policy_id", policy.ID).Msg("replication_policy_owner_epoch_missing")
			continue
		}

		node, ok := nodeByID[owner]
		status := "offline"
		if ok {
			status = strings.ToLower(strings.TrimSpace(node.Status))
		}

		if status == "online" {
			s.downMisses[policy.ID] = 0
			lease := clusterModels.ReplicationLease{
				PolicyID:    policy.ID,
				GuestType:   policy.GuestType,
				GuestID:     policy.GuestID,
				OwnerNodeID: owner,
				OwnerEpoch:  ownerEpoch,
				ExpiresAt:   now.Add(10 * time.Second),
				Version:     uint64(now.UnixNano()),
				LastReason:  "leader_renew",
				LastActor:   s.Cluster.LocalNodeID(),
			}
			if err := s.Cluster.UpsertReplicationLease(lease, false); err != nil {
				logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("replication_lease_renew_failed")
			}

			if policy.FailbackMode == clusterModels.ReplicationFailbackAuto &&
				strings.TrimSpace(policy.SourceNodeID) != "" &&
				strings.TrimSpace(policy.SourceNodeID) != owner {
				sourceNode, ok := nodeByID[strings.TrimSpace(policy.SourceNodeID)]
				if ok && strings.ToLower(strings.TrimSpace(sourceNode.Status)) == "online" {
					if err := s.failoverPolicyToNode(ctx, &policy, strings.TrimSpace(policy.SourceNodeID), "auto_failback", true); err != nil {
						if isReplicationPolicyTransitionRunningError(err) {
							logger.L.Debug().Uint("policy_id", policy.ID).Msg("auto_failback_transition_already_running")
							continue
						}
						logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("auto_failback_failed")
					}
				}
			}
			continue
		}

		s.downMisses[policy.ID]++
		if s.downMisses[policy.ID] < 3 {
			continue
		}

		targetNodeID, selectErr := s.selectFailoverTarget(&policy, owner, nodeByID)
		if selectErr != nil {
			_, _ = s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
				PolicyID:     &policy.ID,
				EventType:    "failover",
				Status:       replicationEventStatusFailed,
				Message:      "no_healthy_failover_target",
				Error:        selectErr.Error(),
				SourceNodeID: owner,
				GuestType:    policy.GuestType,
				GuestID:      policy.GuestID,
				StartedAt:    now,
				CompletedAt:  &now,
			}, false)
			continue
		}

		if err := s.failoverPolicyToNode(ctx, &policy, targetNodeID, "node_down_failover", false); err != nil {
			if isReplicationPolicyTransitionRunningError(err) {
				logger.L.Debug().
					Uint("policy_id", policy.ID).
					Str("target", targetNodeID).
					Msg("policy_failover_transition_already_running")
				continue
			}
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Str("target", targetNodeID).Msg("policy_failover_failed")
			continue
		}

		s.downMisses[policy.ID] = 0
	}

	return nil
}

func (s *Service) selectFailoverTarget(policy *clusterModels.ReplicationPolicy, currentOwner string, nodes map[string]clusterModels.ClusterNode) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("policy_required")
	}

	targets := append([]clusterModels.ReplicationPolicyTarget{}, policy.Targets...)
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Weight == targets[j].Weight {
			ni := nodes[strings.TrimSpace(targets[i].NodeID)]
			nj := nodes[strings.TrimSpace(targets[j].NodeID)]
			li := ni.CPUUsage + ni.MemoryUsage + ni.DiskUsage
			lj := nj.CPUUsage + nj.MemoryUsage + nj.DiskUsage
			if li == lj {
				return targets[i].NodeID < targets[j].NodeID
			}
			return li < lj
		}
		return targets[i].Weight > targets[j].Weight
	})

	for _, target := range targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" || nodeID == currentOwner {
			continue
		}
		node, ok := nodes[nodeID]
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(node.Status)) != "online" {
			continue
		}
		return nodeID, nil
	}

	return "", fmt.Errorf("no_healthy_target_nodes")
}

func (s *Service) isClusterNodeOnline(nodeID string) (bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false, fmt.Errorf("replication_target_node_required")
	}
	if s.Cluster == nil {
		return false, fmt.Errorf("cluster_service_unavailable")
	}

	nodes, err := s.Cluster.Nodes()
	if err != nil {
		return false, err
	}
	for _, node := range nodes {
		if strings.TrimSpace(node.NodeUUID) != nodeID {
			continue
		}
		return strings.ToLower(strings.TrimSpace(node.Status)) == "online", nil
	}

	return false, fmt.Errorf("replication_target_node_not_found")
}

func (s *Service) failoverPolicyToNode(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	reason string,
	requireDemoteAck bool,
) error {
	if policy == nil || targetNodeID == "" {
		return fmt.Errorf("invalid_failover_input")
	}
	if !s.acquirePolicyTransition(policy.ID) {
		return errReplicationPolicyTransitionAlreadyRunning
	}
	defer s.releasePolicyTransition(policy.ID)

	return s.runPolicyOwnershipTransition(ctx, policy, targetNodeID, reason, requireDemoteAck)
}

func (s *Service) runPolicyOwnershipTransition(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	reason string,
	requireDemoteAck bool,
) error {
	if policy == nil || targetNodeID == "" {
		return fmt.Errorf("invalid_policy_transition_input")
	}

	previousOwner := replicationPolicyOwnerNode(policy)
	currentEpoch := replicationPolicyOwnerEpoch(policy)
	if currentEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_missing")
	}
	if currentEpoch == math.MaxUint64 {
		return fmt.Errorf("replication_policy_owner_epoch_exhausted")
	}
	nextEpoch := currentEpoch + 1

	eventStartedAt := time.Now().UTC()
	eventID, _ := s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
		PolicyID:     &policy.ID,
		EventType:    "failover",
		Status:       replicationEventStatusDemoting,
		Message:      reason + "_demoting",
		SourceNodeID: previousOwner,
		TargetNodeID: targetNodeID,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		StartedAt:    eventStartedAt,
	}, false)
	updateTransitionEvent := func(status, message string, transitionErr error, completed bool) {
		if eventID == 0 {
			return
		}

		event := clusterModels.ReplicationEvent{
			ID:           eventID,
			PolicyID:     &policy.ID,
			EventType:    "failover",
			Status:       status,
			Message:      message,
			SourceNodeID: previousOwner,
			TargetNodeID: targetNodeID,
			GuestType:    policy.GuestType,
			GuestID:      policy.GuestID,
			StartedAt:    eventStartedAt,
		}
		if transitionErr != nil {
			event.Error = transitionErr.Error()
		}
		if completed {
			completedAt := time.Now().UTC()
			event.CompletedAt = &completedAt
		}
		_, _ = s.Cluster.CreateOrUpdateReplicationEvent(event, false)
	}

	transition := clusterModels.ReplicationPolicyTransition{
		State:        clusterModels.ReplicationTransitionStateDemoting,
		RunID:        fmt.Sprintf("%d-%s", policy.ID, compactNowToken()),
		Reason:       reason,
		SourceNodeID: previousOwner,
		TargetNodeID: targetNodeID,
		OwnerEpoch:   currentEpoch,
		RequestedAt:  &eventStartedAt,
	}
	persistTransition := func() error {
		return s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition, false)
	}
	appendStepError := func(base error, label string, detail error) error {
		if detail == nil {
			return base
		}
		if base == nil {
			return fmt.Errorf("%s: %w", label, detail)
		}
		return fmt.Errorf("%v; %s: %v", base, label, detail)
	}
	activateOnNode := func(nodeID string) error {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return fmt.Errorf("replication_target_node_required")
		}
		if nodeID == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			return s.ActivateReplicationPolicy(ctx, policy.ID)
		}
		return s.forwardActivateReplicationPolicy(nodeID, policy.ID)
	}
	recoverPreviousOwnerIfDemoted := func(baseErr error) error {
		if transition.DemotedAt == nil {
			return baseErr
		}
		if strings.TrimSpace(previousOwner) == "" {
			return appendStepError(baseErr, "previous_owner_reactivate_failed", fmt.Errorf("replication_previous_owner_missing"))
		}
		return appendStepError(baseErr, "previous_owner_reactivate_failed", activateOnNode(previousOwner))
	}
	rollbackToPreviousOwner := func() error {
		rollbackOwner := strings.TrimSpace(previousOwner)
		if rollbackOwner == "" || rollbackOwner == strings.TrimSpace(targetNodeID) {
			return fmt.Errorf("replication_previous_owner_missing")
		}
		if nextEpoch == math.MaxUint64 {
			return fmt.Errorf("replication_policy_owner_epoch_exhausted")
		}
		rollbackEpoch := nextEpoch + 1

		rollbackReq := s.replicationPolicyToReq(policy)
		if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
			rollbackReq.SourceNodeID = rollbackOwner
			policy.SourceNodeID = rollbackOwner
		}
		rollbackReq.ActiveNodeID = rollbackOwner
		rollbackReq.OwnerEpoch = rollbackEpoch
		if err := s.Cluster.ProposeReplicationPolicyUpdate(policy.ID, rollbackReq, false); err != nil {
			return err
		}

		rollbackLease := clusterModels.ReplicationLease{
			PolicyID:    policy.ID,
			GuestType:   policy.GuestType,
			GuestID:     policy.GuestID,
			OwnerNodeID: rollbackOwner,
			OwnerEpoch:  rollbackEpoch,
			ExpiresAt:   time.Now().UTC().Add(10 * time.Second),
			Version:     uint64(time.Now().UTC().UnixNano()),
			LastReason:  reason + "_rollback",
			LastActor:   s.Cluster.LocalNodeID(),
		}
		if err := s.Cluster.UpsertReplicationLease(rollbackLease, false); err != nil {
			return err
		}

		policy.ActiveNodeID = rollbackOwner
		policy.OwnerEpoch = rollbackEpoch
		transition.OwnerEpoch = rollbackEpoch
		return activateOnNode(rollbackOwner)
	}
	markTransitionFailed := func(transitionErr error) {
		now := time.Now().UTC()
		transition.State = clusterModels.ReplicationTransitionStateFailed
		transition.CompletedAt = &now
		if transitionErr != nil {
			transition.Error = transitionErr.Error()
		} else {
			transition.Error = "transition_failed"
		}
		if err := persistTransition(); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Msg("replication_policy_transition_checkpoint_persist_failed")
		}
	}

	if err := persistTransition(); err != nil {
		updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", err, true)
		return err
	}

	if requireDemoteAck {
		updateTransitionEvent(replicationEventStatusDemoting, reason+"_demote_requested", nil, false)

		var demoteErr error
		if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			demoteErr = s.DemoteReplicationPolicy(ctx, policy.ID, currentEpoch)
		} else {
			demoteErr = s.forwardDemoteReplicationPolicy(previousOwner, policy.ID, currentEpoch)
		}
		if demoteErr != nil {
			markTransitionFailed(demoteErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_demote_failed", demoteErr, true)
			return demoteErr
		}

		updateTransitionEvent(replicationEventStatusDemoting, reason+"_demote_ack", nil, false)
		demotedAt := time.Now().UTC()
		transition.DemotedAt = &demotedAt
		transition.Error = ""
		if err := persistTransition(); err != nil {
			effectiveErr := recoverPreviousOwnerIfDemoted(err)
			markTransitionFailed(effectiveErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", effectiveErr, true)
			return effectiveErr
		}

		updateTransitionEvent(replicationEventStatusDemoting, reason+"_catchup_requested", nil, false)
		transition.State = clusterModels.ReplicationTransitionStateCatchup
		transition.Error = ""
		if err := persistTransition(); err != nil {
			effectiveErr := recoverPreviousOwnerIfDemoted(err)
			markTransitionFailed(effectiveErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", effectiveErr, true)
			return effectiveErr
		}

		var catchupErr error
		if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			catchupErr = s.CatchupReplicationPolicyToNode(ctx, policy.ID, targetNodeID, currentEpoch)
		} else {
			catchupErr = s.forwardCatchupReplicationPolicy(previousOwner, policy.ID, targetNodeID, currentEpoch)
		}
		if catchupErr != nil {
			effectiveErr := recoverPreviousOwnerIfDemoted(catchupErr)
			markTransitionFailed(effectiveErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_catchup_failed", effectiveErr, true)
			return effectiveErr
		}
		updateTransitionEvent(replicationEventStatusDemoting, reason+"_catchup_synced", nil, false)
		catchupAt := time.Now().UTC()
		transition.CatchupAt = &catchupAt
		transition.Error = ""
		if err := persistTransition(); err != nil {
			effectiveErr := recoverPreviousOwnerIfDemoted(err)
			markTransitionFailed(effectiveErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", effectiveErr, true)
			return effectiveErr
		}
	} else {
		transitionErr := fmt.Errorf("unsafe_failover_blocked_without_demote_and_catchup")
		markTransitionFailed(transitionErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_blocked_without_demote_or_catchup", transitionErr, true)
		return transitionErr
	}

	targetOnline, targetOnlineErr := s.isClusterNodeOnline(targetNodeID)
	if targetOnlineErr != nil {
		effectiveErr := recoverPreviousOwnerIfDemoted(targetOnlineErr)
		markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_target_health_check_failed", effectiveErr, true)
		return effectiveErr
	}
	if !targetOnline {
		effectiveErr := recoverPreviousOwnerIfDemoted(fmt.Errorf("replication_target_node_offline"))
		markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_target_offline_before_promote", effectiveErr, true)
		return effectiveErr
	}

	req := s.replicationPolicyToReq(policy)

	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
		req.SourceNodeID = targetNodeID
	}
	req.ActiveNodeID = targetNodeID
	req.OwnerEpoch = nextEpoch

	policy.ActiveNodeID = targetNodeID
	policy.OwnerEpoch = nextEpoch
	if err := s.Cluster.ProposeReplicationPolicyUpdate(policy.ID, req, false); err != nil {
		markTransitionFailed(err)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_demoting_failed", err, true)
		return err
	}

	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: targetNodeID,
		OwnerEpoch:  nextEpoch,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Second),
		Version:     uint64(time.Now().UTC().UnixNano()),
		LastReason:  reason,
		LastActor:   s.Cluster.LocalNodeID(),
	}
	if err := s.Cluster.UpsertReplicationLease(lease, false); err != nil {
		markTransitionFailed(err)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_demoting_failed", err, true)
		return err
	}

	updateTransitionEvent(replicationEventStatusPromoting, reason+"_promoting", nil, false)
	transition.State = clusterModels.ReplicationTransitionStatePromoting
	transition.OwnerEpoch = nextEpoch
	transition.Error = ""
	if err := persistTransition(); err != nil {
		markTransitionFailed(err)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", err, true)
		return err
	}

	targetOnline, targetOnlineErr = s.isClusterNodeOnline(targetNodeID)
	if targetOnlineErr != nil {
		effectiveErr := targetOnlineErr
		previousOwnerOnline, previousOwnerOnlineErr := s.isClusterNodeOnline(previousOwner)
		if previousOwnerOnlineErr == nil && previousOwnerOnline {
			if rollbackErr := rollbackToPreviousOwner(); rollbackErr != nil {
				effectiveErr = appendStepError(effectiveErr, "rollback_failed", rollbackErr)
			} else {
				effectiveErr = fmt.Errorf("%v; rollback_succeeded", effectiveErr)
			}
		}
		markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_target_health_check_failed", effectiveErr, true)
		return effectiveErr
	}
	if !targetOnline {
		effectiveErr := fmt.Errorf("replication_target_node_offline")
		previousOwnerOnline, previousOwnerOnlineErr := s.isClusterNodeOnline(previousOwner)
		if previousOwnerOnlineErr == nil && previousOwnerOnline {
			if rollbackErr := rollbackToPreviousOwner(); rollbackErr != nil {
				effectiveErr = appendStepError(effectiveErr, "rollback_failed", rollbackErr)
			} else {
				effectiveErr = fmt.Errorf("%v; rollback_succeeded", effectiveErr)
			}
		}
		markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_target_offline_during_promote", effectiveErr, true)
		return effectiveErr
	}

	activateErr := activateOnNode(targetNodeID)

	if activateErr != nil {
		effectiveErr := activateErr
		previousOwnerOnline, previousOwnerOnlineErr := s.isClusterNodeOnline(previousOwner)
		if previousOwnerOnlineErr == nil && previousOwnerOnline {
			if rollbackErr := rollbackToPreviousOwner(); rollbackErr != nil {
				effectiveErr = appendStepError(effectiveErr, "rollback_failed", rollbackErr)
			} else {
				effectiveErr = fmt.Errorf("%v; rollback_succeeded", effectiveErr)
			}
		}
		markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_promoting_failed", effectiveErr, true)
		return effectiveErr
	}

	updateTransitionEvent(replicationEventStatusActive, reason+"_active", nil, true)
	now := time.Now().UTC()
	transition.State = clusterModels.ReplicationTransitionStateCompleted
	transition.PromotedAt = &now
	transition.CompletedAt = &now
	transition.OwnerEpoch = nextEpoch
	transition.Error = ""
	if err := persistTransition(); err != nil {
		updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", err, true)
		return err
	}

	return nil
}

func (s *Service) forwardActivateReplicationPolicy(nodeID string, policyID uint) error {
	return s.forwardReplicationPolicyControl(nodeID, "activate", map[string]any{
		"policyId": policyID,
	})
}

func (s *Service) forwardDemoteReplicationPolicy(nodeID string, policyID uint, ownerEpoch uint64) error {
	return s.forwardReplicationPolicyControl(nodeID, "demote", map[string]any{
		"policyId":   policyID,
		"ownerEpoch": ownerEpoch,
	})
}

func (s *Service) forwardCatchupReplicationPolicy(
	nodeID string,
	policyID uint,
	targetNodeID string,
	ownerEpoch uint64,
) error {
	return s.forwardReplicationPolicyControl(nodeID, "catchup", map[string]any{
		"policyId":     policyID,
		"targetNodeId": targetNodeID,
		"ownerEpoch":   ownerEpoch,
	})
}

func (s *Service) forwardCleanupReplicationPolicyDelete(nodeID string, policyID uint) error {
	return s.forwardReplicationPolicyControl(nodeID, "cleanup-policy-delete", map[string]any{
		"policyId": policyID,
	})
}

func (s *Service) forwardReplicationPolicyControl(nodeID string, action string, payload map[string]any) error {
	targetAPI, err := s.resolveReplicationNodeAPI(nodeID)
	if err != nil {
		return err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.Cluster.AuthService.CreateInternalClusterJWT(hostname, "")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/intra-cluster/%s", targetAPI, strings.TrimSpace(action))
	return utils.HTTPPostJSON(url, payload, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
}

func (s *Service) CleanupReplicationPolicyDeleteBestEffort(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	nodesSet := map[string]struct{}{}
	nodes := make([]string, 0, len(policy.Targets)+3)
	addNode := func(nodeID string) {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return
		}
		if _, exists := nodesSet[nodeID]; exists {
			return
		}
		nodesSet[nodeID] = struct{}{}
		nodes = append(nodes, nodeID)
	}

	addNode(policy.SourceNodeID)
	addNode(policy.ActiveNodeID)
	for _, target := range policy.Targets {
		addNode(target.NodeID)
	}
	if localNodeID != "" {
		addNode(localNodeID)
	}

	sort.Strings(nodes)
	cleanupErrs := make([]string, 0)
	for _, nodeID := range nodes {
		var cleanupErr error
		if localNodeID != "" && nodeID == localNodeID {
			cleanupErr = s.CleanupReplicationPolicyDeleteLocalBestEffort(ctx, policyID)
		} else {
			cleanupErr = s.forwardCleanupReplicationPolicyDelete(nodeID, policyID)
		}
		if cleanupErr == nil {
			continue
		}

		logger.L.Warn().
			Uint("policy_id", policyID).
			Str("node_id", nodeID).
			Err(cleanupErr).
			Msg("replication_policy_delete_cleanup_node_failed")
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%s: %v", nodeID, cleanupErr))
	}

	if len(cleanupErrs) > 0 {
		return fmt.Errorf("replication_policy_delete_cleanup_partial_failure: %s", strings.Join(cleanupErrs, "; "))
	}

	return nil
}

func (s *Service) CleanupReplicationPolicyDeleteLocalBestEffort(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	ownerNodeID := replicationPolicyOwnerNode(policy)
	if ownerNodeID == "" {
		return nil
	}
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}

	datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return nil
	}

	cleanupErrs := make([]string, 0)

	// Never remove the active owner's primary dataset during policy delete.
	if localNodeID == ownerNodeID {
		for _, dataset := range datasets {
			if err := s.trimLocalReplicationLineageDatasets(ctx, dataset, 0); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("trim_owner_lineage_%s_failed: %v", dataset, err))
			}
		}
		if len(cleanupErrs) > 0 {
			return fmt.Errorf("replication_policy_delete_local_cleanup_failed: %s", strings.Join(cleanupErrs, "; "))
		}
		return nil
	}

	driver, driverErr := s.replicationGuestDriver(policy.GuestType)
	if driverErr != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("replication_guest_driver_failed: %v", driverErr))
	} else if demoteErr := driver.demote(ctx, policy.GuestID); demoteErr != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("demote_before_cleanup_failed: %v", demoteErr))
	}

	for _, dataset := range datasets {
		if err := s.destroyLocalDatasetWithRetry(ctx, dataset, true, 20, 500*time.Millisecond); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("destroy_local_replica_dataset_%s_failed: %v", dataset, err))
			continue
		}
	}

	switch strings.TrimSpace(policy.GuestType) {
	case clusterModels.ReplicationGuestTypeJail:
		if _, retireErr := s.retireStaleNonOwnerJailMetadata(ctx, policy.GuestID, localNodeID, ownerNodeID); retireErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("retire_stale_jail_metadata_failed: %v", retireErr))
		}
	case clusterModels.ReplicationGuestTypeVM:
		if _, retireErr := s.retireStaleNonOwnerVMMetadata(ctx, policy.GuestID, localNodeID, ownerNodeID); retireErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("retire_stale_vm_metadata_failed: %v", retireErr))
		}
	}

	if len(cleanupErrs) > 0 {
		return fmt.Errorf("replication_policy_delete_local_cleanup_failed: %s", strings.Join(cleanupErrs, "; "))
	}

	return nil
}

func (s *Service) DemoteReplicationPolicy(ctx context.Context, policyID uint, expectedOwnerEpoch uint64) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}

	policyOwner := replicationPolicyOwnerNode(policy)
	if policyOwner == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if policyOwner != localNodeID {
		return fmt.Errorf("replication_policy_not_owned_by_local_node")
	}

	currentEpoch := replicationPolicyOwnerEpoch(policy)
	if expectedOwnerEpoch > 0 && currentEpoch != expectedOwnerEpoch {
		return fmt.Errorf("replication_policy_owner_epoch_mismatch")
	}

	driver, err := s.replicationGuestDriver(policy.GuestType)
	if err != nil {
		return err
	}
	if err := driver.demote(ctx, policy.GuestID); err != nil {
		return err
	}

	return nil
}

func (s *Service) CatchupReplicationPolicyToNode(
	ctx context.Context,
	policyID uint,
	targetNodeID string,
	expectedOwnerEpoch uint64,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID == "" {
		return fmt.Errorf("replication_target_node_required")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}

	policyOwner := replicationPolicyOwnerNode(policy)
	if policyOwner == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if policyOwner != localNodeID {
		return fmt.Errorf("replication_policy_not_owned_by_local_node")
	}

	currentEpoch := replicationPolicyOwnerEpoch(policy)
	if expectedOwnerEpoch > 0 && currentEpoch != expectedOwnerEpoch {
		return fmt.Errorf("replication_policy_owner_epoch_mismatch")
	}

	nodes, err := s.Cluster.Nodes()
	if err == nil {
		for _, node := range nodes {
			if strings.TrimSpace(node.NodeUUID) != targetNodeID {
				continue
			}
			if strings.ToLower(strings.TrimSpace(node.Status)) != "online" {
				return fmt.Errorf("replication_target_node_offline")
			}
			break
		}
	}

	sourceDatasets, err := s.replicationSourceDatasets(ctx, policy)
	if err != nil {
		return err
	}
	if len(sourceDatasets) == 0 {
		return fmt.Errorf("no_source_datasets_found")
	}

	identities, err := s.Cluster.ListClusterSSHIdentities()
	if err != nil {
		return err
	}
	var identity *clusterModels.ClusterSSHIdentity
	for i := range identities {
		nodeID := strings.TrimSpace(identities[i].NodeUUID)
		if nodeID == targetNodeID {
			identity = &identities[i]
			break
		}
	}
	if identity == nil {
		return fmt.Errorf("replication_target_identity_missing")
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_private_key_path_failed: %w", err)
	}

	targetHost := strings.TrimSpace(identity.SSHHost)
	if targetHost == "" {
		return fmt.Errorf("replication_target_identity_host_missing")
	}
	targetUser := strings.TrimSpace(identity.SSHUser)
	if targetUser == "" {
		targetUser = "root"
	}

	for _, sourceDataset := range sourceDatasets {
		backupRoot, destSuffix := splitDatasetForTarget(sourceDataset)
		targetSpec := &clusterModels.BackupTarget{
			SSHHost:    fmt.Sprintf("%s@%s", targetUser, targetHost),
			SSHPort:    identity.SSHPort,
			SSHKeyPath: privateKeyPath,
			BackupRoot: backupRoot,
			Enabled:    true,
		}

		out, runErr := s.replicateWithTargetAndPrefix(ctx, targetSpec, sourceDataset, destSuffix, "ha")
		if strings.TrimSpace(out) != "" {
			logger.L.Debug().
				Uint("policy_id", policyID).
				Str("target_node_id", targetNodeID).
				Str("source_dataset", sourceDataset).
				Str("output", out).
				Msg("replication_catchup_output")
		}
		if runErr == nil {
			continue
		}
		if !isReplicationTargetModifiedError(runErr) {
			return fmt.Errorf("replication_catchup_to_target_%s_failed: %w", targetNodeID, runErr)
		}

		rotateOut, rotateErr := s.RotateWithTargetAndPrefix(ctx, targetSpec, sourceDataset, destSuffix, "ha")
		if strings.TrimSpace(rotateOut) != "" {
			logger.L.Debug().
				Uint("policy_id", policyID).
				Str("target_node_id", targetNodeID).
				Str("source_dataset", sourceDataset).
				Str("output", rotateOut).
				Msg("replication_catchup_rotate_output")
		}
		if rotateErr != nil {
			return fmt.Errorf(
				"replication_catchup_to_target_%s_failed_after_diverged_target_rotate_failed: %w (original: %v)",
				targetNodeID,
				rotateErr,
				runErr,
			)
		}

		retryOut, retryErr := s.replicateWithTargetAndPrefix(ctx, targetSpec, sourceDataset, destSuffix, "ha")
		if strings.TrimSpace(retryOut) != "" {
			logger.L.Debug().
				Uint("policy_id", policyID).
				Str("target_node_id", targetNodeID).
				Str("source_dataset", sourceDataset).
				Str("output", retryOut).
				Msg("replication_catchup_retry_output")
		}
		if retryErr != nil {
			return fmt.Errorf(
				"replication_catchup_to_target_%s_failed_after_diverged_target_rotate: %w (original: %v)",
				targetNodeID,
				retryErr,
				runErr,
			)
		}
	}

	return nil
}

func (s *Service) resolveReplicationNodeAPI(nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("replication_target_node_required")
	}

	nodes, err := s.Cluster.Nodes()
	if err == nil {
		for _, node := range nodes {
			if strings.TrimSpace(node.NodeUUID) == nodeID && strings.TrimSpace(node.API) != "" {
				return strings.TrimSpace(node.API), nil
			}
		}
	}

	if s.Cluster.Raft != nil {
		fut := s.Cluster.Raft.GetConfiguration()
		if fut.Error() == nil {
			for _, server := range fut.Configuration().Servers {
				if string(server.ID) != nodeID {
					continue
				}
				host, _, splitErr := net.SplitHostPort(string(server.Address))
				if splitErr != nil {
					host = string(server.Address)
				}
				host = strings.TrimSpace(host)
				if host == "" {
					continue
				}
				return net.JoinHostPort(host, strconv.Itoa(config.ParsedConfig.Port)), nil
			}
		}
	}

	return "", fmt.Errorf("replication_target_node_not_found")
}

func (s *Service) replicationPolicyToReq(policy *clusterModels.ReplicationPolicy) clusterServiceInterfaces.ReplicationPolicyReq {
	req := clusterServiceInterfaces.ReplicationPolicyReq{
		Name:         policy.Name,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		SourceNodeID: policy.SourceNodeID,
		OwnerEpoch:   replicationPolicyOwnerEpoch(policy),
		SourceMode:   policy.SourceMode,
		FailbackMode: policy.FailbackMode,
		CronExpr:     policy.CronExpr,
		Enabled:      &policy.Enabled,
		Targets:      make([]clusterServiceInterfaces.ReplicationPolicyTargetReq, 0, len(policy.Targets)),
	}

	for _, target := range policy.Targets {
		req.Targets = append(req.Targets, clusterServiceInterfaces.ReplicationPolicyTargetReq{
			NodeID: target.NodeID,
			Weight: target.Weight,
		})
	}

	return req
}

func (s *Service) waitForLocalReplicationOwnership(ctx context.Context, policyID uint, timeout time.Duration) error {
	if policyID == 0 || s.Cluster == nil {
		return nil
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	deadline := time.Now().UTC().Add(timeout)
	for {
		policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
		if err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
		} else {
			expectedOwner := replicationPolicyOwnerNode(policy)
			expectedEpoch := replicationPolicyOwnerEpoch(policy)
			if expectedEpoch == 0 {
				return fmt.Errorf("replication_policy_owner_epoch_missing")
			}

			if expectedOwner == localNodeID {
				var lease clusterModels.ReplicationLease
				leaseErr := s.DB.Where("policy_id = ?", policyID).First(&lease).Error
				if leaseErr != nil {
					if leaseErr != gorm.ErrRecordNotFound {
						return leaseErr
					}
				} else {
					leaseEpoch := lease.OwnerEpoch
					if strings.TrimSpace(lease.OwnerNodeID) == localNodeID &&
						leaseEpoch == expectedEpoch &&
						time.Now().UTC().Before(lease.ExpiresAt) {
						return nil
					}
				}
			}
		}

		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if time.Now().UTC().After(deadline) {
			return fmt.Errorf("replication_activation_ownership_not_ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Service) ActivateReplicationPolicy(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	if err := s.waitForLocalReplicationOwnership(ctx, policyID, 10*time.Second); err != nil {
		return err
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	driver, err := s.replicationGuestDriver(policy.GuestType)
	if err != nil {
		return err
	}
	return driver.activate(ctx, policy.GuestID)
}

func (s *Service) activateReplicationJail(ctx context.Context, ctID uint) error {
	if err := s.stopLocalJailIfPresent(ctID); err != nil {
		return err
	}

	dataset, err := s.findLocalGuestDataset(ctx, clusterModels.ReplicationGuestTypeJail, ctID)
	if err != nil {
		return err
	}
	if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
		return err
	}
	if dataset == "" {
		return fmt.Errorf("jail_dataset_not_found")
	}
	if err := s.reconcileRestoredJailFromDatasetWithOptions(ctx, dataset, true); err != nil {
		return err
	}

	return s.Jail.JailAction(int(ctID), "start")
}

func (s *Service) activateReplicationVM(ctx context.Context, rid uint) error {
	if err := s.stopVMIfPresent(rid); err != nil {
		return err
	}

	dataset, err := s.findLocalGuestDataset(ctx, clusterModels.ReplicationGuestTypeVM, rid)
	if err != nil {
		return err
	}
	if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
		return err
	}
	if dataset == "" {
		return fmt.Errorf("vm_dataset_not_found")
	}
	if err := s.reconcileRestoredVMFromDatasetWithOptions(ctx, dataset, true); err != nil {
		return err
	}
	vm, err := s.findVMByRID(rid)
	if err != nil {
		return err
	}
	if vm == nil {
		return fmt.Errorf("vm_definition_not_found_after_reconcile")
	}

	return s.VM.LvVMAction(*vm, "start")
}

func (s *Service) stopLocalJailIfPresent(ctID uint) error {
	if ctID == 0 || s.Jail == nil {
		return nil
	}

	var jail jailModels.Jail
	if err := s.DB.Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if jail.StoppedAt != nil && (jail.StartedAt == nil || !jail.StoppedAt.Before(*jail.StartedAt)) {
		return nil
	}

	if err := s.Jail.JailAction(int(ctID), "stop"); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "failed to find jail") ||
			strings.Contains(lower, "not found") ||
			strings.Contains(lower, "no such process") {
			return nil
		}
		return err
	}

	return nil
}

func (s *Service) retireStaleNonOwnerJailMetadata(ctx context.Context, ctID uint, localNodeID string, expectedOwner string) (bool, error) {
	if ctID == 0 {
		return false, nil
	}

	localNodeID = strings.TrimSpace(localNodeID)
	expectedOwner = strings.TrimSpace(expectedOwner)
	if localNodeID == "" || expectedOwner == "" || localNodeID == expectedOwner {
		return false, nil
	}

	var jail jailModels.Jail
	if err := s.DB.Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}

	if err := s.Jail.DeleteJail(ctx, ctID, false, false); err != nil {
		return false, fmt.Errorf("retire_stale_non_owner_jail_metadata_failed: %w", err)
	}

	logger.L.Info().
		Uint("ctid", ctID).
		Uint("jail_id", jail.ID).
		Str("local_node", localNodeID).
		Str("expected_owner", expectedOwner).
		Msg("retired_stale_non_owner_jail_metadata")
	return true, nil
}

func (s *Service) retireStaleNonOwnerVMMetadata(ctx context.Context, rid uint, localNodeID string, expectedOwner string) (bool, error) {
	if rid == 0 || s.VM == nil {
		return false, nil
	}

	localNodeID = strings.TrimSpace(localNodeID)
	expectedOwner = strings.TrimSpace(expectedOwner)
	if localNodeID == "" || expectedOwner == "" || localNodeID == expectedOwner {
		return false, nil
	}

	vm, err := s.findVMByRID(rid)
	if err != nil {
		return false, err
	}
	if vm == nil {
		return false, nil
	}

	if err := s.VM.RetireVMLocalMetadata(rid, false); err != nil {
		return false, fmt.Errorf("retire_stale_non_owner_vm_metadata_failed: %w", err)
	}

	logger.L.Info().
		Uint("rid", rid).
		Uint("vm_id", vm.ID).
		Str("local_node", localNodeID).
		Str("expected_owner", expectedOwner).
		Msg("retired_stale_non_owner_vm_metadata")
	return true, nil
}

func (s *Service) prepareReplicatedDatasetForActivation(ctx context.Context, rootDataset string) error {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil
	}

	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return err
	}

	seen := map[string]struct{}{
		rootDataset: {},
	}
	subtree := []string{rootDataset}
	prefix := rootDataset + "/"

	for _, candidate := range datasets {
		ds := normalizeDatasetPath(candidate)
		if ds == "" || ds == rootDataset {
			continue
		}
		if !strings.HasPrefix(ds, prefix) {
			continue
		}
		if _, ok := seen[ds]; ok {
			continue
		}
		seen[ds] = struct{}{}
		subtree = append(subtree, ds)
	}

	sort.SliceStable(subtree, func(i, j int) bool {
		di := strings.Count(subtree[i], "/")
		dj := strings.Count(subtree[j], "/")
		if di == dj {
			return subtree[i] < subtree[j]
		}
		return di < dj
	})

	for idx, dataset := range subtree {
		ds, err := s.getLocalDataset(ctx, dataset)
		if err != nil {
			return fmt.Errorf("failed_to_open_replication_dataset_%s: %w", dataset, err)
		}
		if ds == nil {
			continue
		}

		if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
			return fmt.Errorf("failed_to_set_replication_dataset_properties_%s: %w", dataset, err)
		}

		if idx == 0 {
			if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", "mountpoint", dataset); err != nil {
				return fmt.Errorf("failed_to_inherit_replication_dataset_mountpoint_%s: %w", dataset, err)
			}
		}

		if err := ds.Mount(ctx, false); err != nil {
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, "already mounted") {
				return fmt.Errorf("failed_to_mount_replication_dataset_%s: %w", dataset, err)
			}
		}
	}

	return nil
}

func (s *Service) findLocalGuestDataset(ctx context.Context, guestType string, guestID uint) (string, error) {
	candidates, err := s.findLocalGuestDatasets(ctx, guestType, guestID)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", nil
	}
	return candidates[0], nil
}

func (s *Service) findLocalGuestDatasets(ctx context.Context, guestType string, guestID uint) ([]string, error) {
	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return nil, err
	}
	backupRoots := s.listEnabledBackupRoots()

	seen := map[string]struct{}{}
	candidates := make([]string, 0)
	for _, dataset := range datasets {
		kind, id := inferRestoreDatasetKind(dataset)
		if kind != guestType || id != guestID {
			continue
		}

		normalized := normalizeDatasetPath(dataset)
		if kind == clusterModels.ReplicationGuestTypeVM {
			root := vmDatasetRoot(normalized)
			if root != "" {
				normalized = root
			}
		}
		if datasetWithinAnyRoot(normalized, backupRoots) {
			continue
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}

	sort.Strings(candidates)
	return candidates, nil
}

func (s *Service) selfFenceExpiredLeases(ctx context.Context) error {
	if s.Cluster == nil {
		return nil
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		return err
	}

	for _, policy := range policies {
		expectedOwner := replicationPolicyOwnerNode(&policy)
		if expectedOwner == "" {
			continue
		}
		expectedOwner = strings.TrimSpace(expectedOwner)

		fenceReason := replicationFenceReasonPolicyOwnerMismatch
		if expectedOwner == localNodeID {
			expectedEpoch := replicationPolicyOwnerEpoch(&policy)
			if expectedEpoch == 0 {
				logger.L.Warn().
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Str("guest_type", strings.TrimSpace(policy.GuestType)).
					Msg("replication_self_fence_local_owner_epoch_missing")
				continue
			}

			lease, leaseLookupErr := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
			if leaseLookupErr != nil {
				if errors.Is(leaseLookupErr, gorm.ErrRecordNotFound) {
					// Lease renewal is asynchronous; avoid fencing local owner on transient lease absence.
					continue
				}
				logger.L.Warn().
					Err(leaseLookupErr).
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Str("guest_type", strings.TrimSpace(policy.GuestType)).
					Msg("replication_self_fence_local_owner_lease_lookup_failed")
				continue
			}
			if lease == nil {
				continue
			}
			if strings.TrimSpace(lease.OwnerNodeID) == localNodeID &&
				lease.OwnerEpoch == expectedEpoch &&
				time.Now().UTC().Before(lease.ExpiresAt) {
				continue
			}

			fenceReason = replicationFenceReasonOwnerLeaseInvalid
		}

		driver, err := s.replicationGuestDriver(policy.GuestType)
		if err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Str("guest_type", strings.TrimSpace(policy.GuestType)).
				Msg("replication_self_fence_invalid_guest_type")
			continue
		}
		driver.selfFence(ctx, policy.ID, policy.GuestID, localNodeID, expectedOwner, fenceReason)
		if err := s.fenceReplicationGuestDatasets(ctx, &policy, fenceReason); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Str("guest_type", strings.TrimSpace(policy.GuestType)).
				Str("reason", fenceReason).
				Msg("replication_self_fence_dataset_fencing_failed")
		}
	}

	return nil
}
