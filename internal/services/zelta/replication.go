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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	replicationJobQueueName         = "zelta-replication-run"
	replicationFailoverJobQueueName = "zelta-replication-failover"
)

const (
	defaultReplicationPruneKeepLast   = 64
	defaultReplicationLineageKeepOld  = 2
	replicationOrphanCleanupInterval  = 5 * time.Minute
	replicationReceiptCleanupInterval = 5 * time.Minute
	replicationFailoverDownMissLimit  = 3
	replicationFailbackHitLimit       = 3
	replicationCrashRestartLimit      = 3
	replicationLeaseTTL               = 20 * time.Second
	replicationLowPoolCapacityPercent = 90

	replicationEventStatusRunning   = "running"
	replicationEventStatusDemoting  = "demoting"
	replicationEventStatusPromoting = "promoting"
	replicationEventStatusActive    = "active"
	replicationEventStatusSuccess   = "success"
	replicationEventStatusFailed    = "failed"
	replicationEventStatusDegraded  = "degraded"

	replicationFailoverRequestSafe  = "safe"
	replicationFailoverRequestForce = "force"

	replicationControlDefaultTimeout = 30 * time.Second
	replicationControlCatchupTimeout = 2 * time.Hour
	replicationControlReceiptTimeout = 10 * time.Second

	replicationControlForwardAttempts = 3
	replicationControlForwardBackoff  = 500 * time.Millisecond

	replicationReceiptForwardAttempts = 3
	replicationReceiptForwardBackoff  = 500 * time.Millisecond
)

type replicationJobPayload struct {
	PolicyID uint `json:"policy_id"`
}

type replicationFailoverJobPayload struct {
	PolicyID         uint   `json:"policy_id"`
	TargetNodeID     string `json:"target_node_id"`
	Mode             string `json:"mode"`
	ConfirmDataLoss  bool   `json:"confirm_data_loss"`
	MovePinnedSource bool   `json:"move_pinned_source"`
}

type ReplicationEventProgress struct {
	Event           *clusterModels.ReplicationEvent `json:"event"`
	MovedBytes      *uint64                         `json:"movedBytes"`
	TotalBytes      *uint64                         `json:"totalBytes"`
	ProgressPercent *float64                        `json:"progressPercent"`
}

var errReplicationPolicyTransitionAlreadyRunning = errors.New("replication_policy_transition_already_running")

type replicationTransitionOptions struct {
	AllowUnsafe          bool
	MovePinnedSource     bool
	TriggerValidationRun bool
}

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
			if len(clusterService.ParseReplicationHAIneligibleReasons(err)) > 0 {
				logger.L.Warn().
					Err(err).
					Uint("policy_id", payload.PolicyID).
					Msg("queued_replication_policy_blocked_ha_constraints")
				return nil
			}
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_failed")
			return err
		}
		return nil
	})
}

func (s *Service) registerReplicationFailoverJob() {
	db.QueueRegisterJSON(replicationFailoverJobQueueName, func(ctx context.Context, payload replicationFailoverJobPayload) error {
		if payload.PolicyID == 0 {
			return fmt.Errorf("invalid_policy_id_in_failover_queue_payload")
		}

		err := s.RequestReplicationPolicyFailover(
			ctx,
			payload.PolicyID,
			strings.TrimSpace(payload.TargetNodeID),
			payload.Mode,
			payload.ConfirmDataLoss,
			payload.MovePinnedSource,
		)
		if err != nil {
			if isReplicationPolicyTransitionRunningError(err) {
				logger.L.Debug().
					Uint("policy_id", payload.PolicyID).
					Str("target_node_id", strings.TrimSpace(payload.TargetNodeID)).
					Msg("queued_failover_transition_already_running")
				return nil
			}
			logger.L.Warn().
				Err(err).
				Uint("policy_id", payload.PolicyID).
				Str("target_node_id", strings.TrimSpace(payload.TargetNodeID)).
				Str("mode", strings.TrimSpace(payload.Mode)).
				Msg("queued_failover_policy_failed")
			return err
		}

		return nil
	})
}

func (s *Service) EnqueueReplicationPolicyRun(ctx context.Context, policyID uint) error {
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

	haEval := s.Cluster.EvaluateReplicationPolicyHA(policy)
	if !haEval.Eligible {
		return replicationPolicyHAError(haEval)
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
	lastReceiptCleanup := time.Time{}
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

			if err := s.recoverCrashedReplicationGuests(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("replication_crash_recovery_failed")
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

			if time.Since(lastReceiptCleanup) > replicationReceiptCleanupInterval {
				if err := s.pruneLocalReplicationReceipts(); err != nil {
					logger.L.Warn().Err(err).Msg("replication_receipt_prune_tick_failed")
				}
				lastReceiptCleanup = time.Now()
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
	if strings.Contains(reason, "force") {
		return false
	}
	return !strings.Contains(reason, "node_down_failover")
}

func transitionAllowUnsafe(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	return strings.Contains(reason, "force")
}

func policyFailoverMode(policy *clusterModels.ReplicationPolicy) string {
	if policy == nil {
		return clusterModels.ReplicationFailoverManual
	}
	mode := strings.ToLower(strings.TrimSpace(policy.FailoverMode))
	switch mode {
	case clusterModels.ReplicationFailoverAutoSafe,
		clusterModels.ReplicationFailoverAutoForce,
		clusterModels.ReplicationFailoverManual:
		return mode
	default:
		return clusterModels.ReplicationFailoverManual
	}
}

func replicationPolicyHAError(eval clusterService.ReplicationPolicyHAEvaluation) error {
	if eval.Eligible {
		return nil
	}
	return clusterService.NewReplicationHAIneligibleError(eval.Reasons)
}

func projectedPolicyTopologyAfterFailover(
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	movePinnedSource bool,
) (string, string) {
	targetNodeID = strings.TrimSpace(targetNodeID)
	if policy == nil {
		return "", targetNodeID
	}

	sourceNodeID := strings.TrimSpace(policy.SourceNodeID)
	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
		sourceNodeID = targetNodeID
	} else if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned && movePinnedSource {
		sourceNodeID = targetNodeID
	}
	return sourceNodeID, targetNodeID
}

func replicationFailoverRequestMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case replicationFailoverRequestForce:
		return replicationFailoverRequestForce
	default:
		return replicationFailoverRequestSafe
	}
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

	if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition); err != nil {
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
	if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition); err != nil {
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
			if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition); err != nil {
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
			replicationTransitionOptions{
				AllowUnsafe:          transitionAllowUnsafe(reason),
				MovePinnedSource:     false,
				TriggerValidationRun: true,
			},
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
	snapshotName = strings.TrimSpace(snapshotName)
	snapshotName = strings.TrimPrefix(snapshotName, "@")
	if snapshotName == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(snapshotName), "ha_")
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

		haEval := s.Cluster.EvaluateReplicationPolicyHA(&policy)
		haErr := replicationPolicyHAError(haEval)

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
			updates := map[string]any{
				"next_run_at": nextAt,
			}
			if haErr != nil {
				updates["last_status"] = "blocked"
				updates["last_error"] = haErr.Error()
				updates["last_run_at"] = now
			}
			_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(updates).Error
			continue
		}

		if now.Before(*policy.NextRunAt) {
			continue
		}

		if haErr != nil {
			if err := s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(map[string]any{
				"last_run_at": now,
				"last_status": "blocked",
				"last_error":  haErr.Error(),
				"next_run_at": nextAt,
			}).Error; err != nil {
				logger.L.Warn().
					Err(err).
					Uint("policy_id", policy.ID).
					Msg("failed_to_mark_replication_policy_ha_blocked")
			}
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
	activeNodeID := strings.TrimSpace(policy.ActiveNodeID)
	if activeNodeID != "" {
		return activeNodeID
	}
	return strings.TrimSpace(policy.SourceNodeID)
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

		readonlyProp, propErr := ds.GetProperty(ctx, "readonly")
		if propErr == nil && strings.EqualFold(strings.TrimSpace(readonlyProp.Value), "on") {
			// Already fenced; avoid repeated noisy logs every self-fence tick.
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
	if s.Cluster == nil {
		runErr := fmt.Errorf("cluster_service_unavailable")
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}
	if !s.acquireReplication(policy.ID) {
		return fmt.Errorf("replication_policy_already_running")
	}
	defer s.releaseReplication(policy.ID)

	if ok, holder := s.acquireWorkloadOperation(
		policy.GuestType,
		policy.GuestID,
		fmt.Sprintf("replication_policy:%d", policy.ID),
	); !ok {
		runErr := fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			strings.ToLower(strings.TrimSpace(policy.GuestType)),
			policy.GuestID,
		)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}
	defer s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)

	localNodeID := ""
	if s.Cluster != nil {
		localNodeID = strings.TrimSpace(s.Cluster.LocalNodeID())
	}

	runner := s.replicationRunnerNodeID(policy)
	if runner != "" && localNodeID != "" && runner != localNodeID {
		return fmt.Errorf("policy_runner_mismatch")
	}

	haEval := s.Cluster.EvaluateReplicationPolicyHA(policy)
	if !haEval.Eligible {
		runErr := replicationPolicyHAError(haEval)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
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
	failedTargets := make([]string, 0)
	succeededTargets := 0

	// Re-verify the lease immediately before starting transfers.
	// The lease may have expired or been transferred since the
	// initial check at the top of runReplicationPolicy.
	if ownershipErr := s.validateLocalReplicationPolicyLease(policy); ownershipErr != nil {
		runErr = fmt.Errorf("pre_transfer_lease_check_failed: %w", ownershipErr)
		s.finalizeReplicationEvent(&event, runErr)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

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
			targetDataset := targetDatasetPath(targetSpec.BackupRoot, destSuffix)
			attemptAt := time.Now().UTC()
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
			attemptErr := error(nil)
			if err != nil {
				if isReplicationResumeStateError(err) {
					_ = s.AppendReplicationEventOutput(event.ID, "target_resumable_receive_state_detected_attempting_abort")
					abortOut, abortErr := s.abortTargetResumableReceiveState(ctx, targetSpec, destSuffix)
					if strings.TrimSpace(abortOut) != "" {
						_ = s.AppendReplicationEventOutput(event.ID, abortOut)
					}
					if abortErr != nil {
						attemptErr = fmt.Errorf(
							"replication_to_target_%s_failed_after_resume_abort_failed: %w (original: %v)",
							targetNodeID,
							abortErr,
							err,
						)
					} else {
						retryOut, retryErr := s.replicateWithEventProgress(ctx, targetSpec, sourceDataset, destSuffix, event.ID)
						if strings.TrimSpace(retryOut) != "" {
							_ = s.AppendReplicationEventOutput(event.ID, retryOut)
						}
						if retryErr != nil {
							attemptErr = fmt.Errorf(
								"replication_to_target_%s_failed_after_resume_abort_retry: %w (original: %v)",
								targetNodeID,
								retryErr,
								err,
							)
						}
					}
				} else if isReplicationTargetModifiedError(err) {
					attemptErr = fmt.Errorf(
						"replication_to_target_%s_failed_diverged_target_unrecoverable: %w",
						targetNodeID,
						err,
					)
				} else {
					attemptErr = fmt.Errorf("replication_to_target_%s_failed: %w", targetNodeID, err)
				}
			}

			if attemptErr == nil {
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

			receiptStatus := "success"
			receiptMessage := "replication_delivery_succeeded"
			receiptError := ""
			if attemptErr != nil {
				receiptStatus = "failed"
				receiptMessage = "replication_delivery_failed"
				receiptError = attemptErr.Error()
			}
			if receiptErr := s.emitReplicationReceiptBestEffort(
				targetNodeID,
				clusterModels.ReplicationReceipt{
					PolicyID:          policy.ID,
					GuestType:         policy.GuestType,
					GuestID:           policy.GuestID,
					SourceNodeID:      localNodeID,
					TargetNodeID:      targetNodeID,
					Status:            receiptStatus,
					Message:           receiptMessage,
					Error:             receiptError,
					LastAttemptAt:     attemptAt,
					LastSourceDataset: sourceDataset,
					LastTargetDataset: targetDataset,
				},
			); receiptErr != nil {
				logger.L.Warn().
					Err(receiptErr).
					Uint("policy_id", policy.ID).
					Str("source_dataset", sourceDataset).
					Str("target_node_id", targetNodeID).
					Msg("replication_receipt_emit_failed")
			}

			if attemptErr != nil {
				runErr = attemptErr
			}
		}

		if runErr != nil {
			// Track this target as failed but continue to remaining
			// targets. One bad target shouldn't block healthy ones.
			logger.L.Warn().
				Err(runErr).
				Uint("policy_id", policy.ID).
				Str("target_node_id", targetNodeID).
				Msg("replication_target_failed_continuing")
			failedTargets = append(failedTargets, targetNodeID)
			runErr = nil
		} else {
			succeededTargets++
		}
	}

	if runErr == nil {
		if eligibleTargets == 0 {
			runErr = fmt.Errorf("no_eligible_replication_targets (offline=%d missing_identity=%d)", skippedOffline, skippedNoIdentity)
		} else if attemptedTransfers == 0 {
			runErr = fmt.Errorf("no_replication_transfers_executed")
		}
	}

	if succeededTargets == 0 && len(failedTargets) > 0 {
		logger.L.Error().
			Uint("policy_id", policy.ID).
			Strs("failed_targets", failedTargets).
			Msg("replication_all_targets_failed")
		runErr = fmt.Errorf("replication_all_targets_failed:%d_targets", len(failedTargets))
	}

	// If some targets succeeded and some failed, surface a degraded
	// status so the UI can show a warning rather than hiding failures
	// behind "success". The per-target receipts still carry details.
	if succeededTargets > 0 && len(failedTargets) > 0 {
		logger.L.Warn().
			Uint("policy_id", policy.ID).
			Strs("failed_targets", failedTargets).
			Int("succeeded", succeededTargets).
			Int("failed", len(failedTargets)).
			Msg("replication_partial_success")
		runErr = fmt.Errorf("replication_degraded:%d_succeeded_%d_failed", succeededTargets, len(failedTargets))
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

	if err := s.retainReplicationSnapshots(ctx, target, sourceDataset, destSuffix, defaultReplicationPruneKeepLast); err != nil {
		_ = s.AppendReplicationEventOutput(eventID, fmt.Sprintf("replication_prune_warning: %v", err))
		logger.L.Warn().
			Err(err).
			Uint("event_id", eventID).
			Str("source_dataset", sourceDataset).
			Msg("replication_prune_failed")
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

func isReplicationResumeStateError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "cannot receive resume stream") &&
		strings.Contains(lower, "partially-complete state")
}

func isReplicationResumeAbortNoopError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no such process") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "no resumable receive state")
}

func (s *Service) abortTargetResumableReceiveState(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	destSuffix string,
) (string, error) {
	if target == nil {
		return "", fmt.Errorf("replication_target_required")
	}

	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	if targetDataset == "" {
		return "", fmt.Errorf("replication_target_dataset_required")
	}

	output, err := s.runTargetSSH(ctx, target, "zfs", "receive", "-A", targetDataset)
	if err != nil && !isReplicationResumeAbortNoopError(err) {
		return output, err
	}

	return output, nil
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
	return s.replicationZFSSend(ctx, target, sourceDataset, destSuffix, snapPrefix, onLine)
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

	lastStatus := "success"
	lastError := ""
	if runErr != nil {
		if strings.Contains(runErr.Error(), "replication_degraded") {
			lastStatus = "degraded"
		} else if len(clusterService.ParseReplicationHAIneligibleReasons(runErr)) > 0 {
			lastStatus = "blocked"
		} else {
			lastStatus = "failed"
		}
		lastError = runErr.Error()
	}

	if s.syncReplicationPolicyRuntimeState(policy.ID, now, next, lastStatus, lastError) {
		return
	}

	_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(map[string]any{
		"last_run_at": now,
		"last_status": lastStatus,
		"last_error":  lastError,
		"next_run_at": next,
	}).Error
}

func (s *Service) syncReplicationPolicyRuntimeState(policyID uint, lastRunAt time.Time, nextRunAt *time.Time, lastStatus, lastError string) bool {
	if s == nil || s.Cluster == nil {
		return false
	}

	update := clusterService.ReplicationPolicyRuntimeState{
		ID:         policyID,
		LastRunAt:  &lastRunAt,
		LastStatus: lastStatus,
		LastError:  lastError,
		NextRunAt:  nextRunAt,
	}
	bypassRaft := s.Cluster.Raft == nil
	if err := s.Cluster.ProposeReplicationPolicyStateUpdate(update, bypassRaft); err == nil {
		return true
	} else if !bypassRaft && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		forwardErr := s.forwardReplicationPolicyStateToLeader(update)
		if forwardErr == nil {
			return true
		}
		logger.L.Warn().Err(forwardErr).Uint("policy_id", policyID).Msg("failed_to_forward_replication_policy_state_to_leader")
	} else {
		logger.L.Warn().Err(err).Uint("policy_id", policyID).Msg("failed_to_sync_replication_policy_state_cluster_wide")
	}

	return false
}

func (s *Service) forwardReplicationPolicyStateToLeader(update clusterService.ReplicationPolicyRuntimeState) error {
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Cluster.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	_, leaderID := s.Cluster.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	if leaderNodeID == "" {
		return fmt.Errorf("leader_unknown")
	}

	payload := map[string]any{
		"id":         update.ID,
		"lastRunAt":  update.LastRunAt,
		"lastStatus": update.LastStatus,
		"lastError":  update.LastError,
		"nextRunAt":  update.NextRunAt,
	}

	return s.forwardReplicationPolicyControl(leaderNodeID, "replication-policy-state", payload, 5*time.Second)
}

func (s *Service) finalizeReplicationEvent(event *clusterModels.ReplicationEvent, runErr error) {
	if event == nil || event.ID == 0 {
		return
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	if runErr != nil {
		if strings.Contains(runErr.Error(), "replication_degraded") {
			event.Status = replicationEventStatusDegraded
			event.Message = "replication_run_degraded"
		} else {
			event.Status = replicationEventStatusFailed
			event.Message = "replication_run_failed"
		}
		event.Error = runErr.Error()
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

	if event.PolicyID != nil && s.TelemetryDB != nil {
		auditStatus := "success"
		errMsg := ""
		if runErr != nil {
			auditStatus = "failed"
			errMsg = runErr.Error()
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_run", *event.PolicyID, auditStatus, errMsg, map[string]any{
			"eventId": event.ID,
			"status":  auditStatus,
			"error":   errMsg,
		})
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("replication_event_finalized_%d", event.ID))
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

func (s *Service) IsPolicyTransitionRunning(policyID uint) bool {
	s.transitionMu.Lock()
	_, exists := s.runningTransitions[policyID]
	s.transitionMu.Unlock()
	return exists
}

const (
	badgerKeyCrashMisses    = "repl:crash:"
	badgerKeyDownMisses     = "repl:down:"
	badgerKeyFailbackHits   = "repl:failback:"
	badgerCounterTTL        = 86400
)

func badgerCrashKey(policyID uint) string    { return fmt.Sprintf("%s%d", badgerKeyCrashMisses, policyID) }
func badgerDownKey(policyID uint) string     { return fmt.Sprintf("%s%d", badgerKeyDownMisses, policyID) }

func badgerCounterGet(key string) uint64 {
	val, ok := db.GetValue(key)
	if !ok || len(val) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(val)
}

func badgerCounterSet(key string, val uint64) {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, val)
	if err := db.SetValue(key, b, badgerCounterTTL); err != nil {
		logger.L.Warn().Err(err).Str("key", key).Msg("badger_counter_set_failed")
	}
}

func badgerCounterIncr(key string, max uint64) uint64 {
	val := badgerCounterGet(key)
	val++
	if val > max {
		val = max
	}
	badgerCounterSet(key, val)
	return val
}

func badgerCounterDelete(key string) {
	if err := db.SetValue(key, nil, 0); err != nil {
		logger.L.Warn().Err(err).Str("key", key).Msg("badger_counter_delete_failed")
	}
}

func (s *Service) crashMissesReset(policyID uint) {
	badgerCounterSet(badgerCrashKey(policyID), 0)
}

func (s *Service) crashMissesIncr(policyID uint, max uint64) uint64 {
	return badgerCounterIncr(badgerCrashKey(policyID), max)
}

func (s *Service) downMissesReset(policyID uint) {
	badgerCounterSet(badgerDownKey(policyID), 0)
}

func (s *Service) downMissesIncr(policyID uint, max uint64) uint64 {
	return badgerCounterIncr(badgerDownKey(policyID), max)
}

func (s *Service) downMissesSet(policyID uint, val uint64) {
	badgerCounterSet(badgerDownKey(policyID), val)
}

func (s *Service) failbackHitsReset(policyID uint) {
	badgerCounterSet(fmt.Sprintf("%s%d", badgerKeyFailbackHits, policyID), 0)
}

func (s *Service) failbackHitsIncr(policyID uint) uint64 {
	return badgerCounterIncr(fmt.Sprintf("%s%d", badgerKeyFailbackHits, policyID), uint64(replicationFailbackHitLimit+1))
}

func (s *Service) replicationCountersDelete(policyID uint) {
	badgerCounterDelete(badgerCrashKey(policyID))
	badgerCounterDelete(badgerDownKey(policyID))
	badgerCounterDelete(fmt.Sprintf("%s%d", badgerKeyFailbackHits, policyID))
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
	var renewals []clusterModels.ReplicationLease
	for i := range policies {
		policy := policies[i]
		if !policy.Enabled {
			continue
		}

		haEval := s.Cluster.EvaluateReplicationPolicyHA(&policy)
		if !haEval.Eligible {
			logger.L.Debug().
				Uint("policy_id", policy.ID).
				Str("reasons", strings.Join(haEval.Reasons, ",")).
				Msg("replication_policy_failover_controller_blocked_by_ha_constraints")
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
			s.downMissesReset(policy.ID)
			renewals = append(renewals, clusterModels.ReplicationLease{
				PolicyID:    policy.ID,
				GuestType:   policy.GuestType,
				GuestID:     policy.GuestID,
				OwnerNodeID: owner,
				OwnerEpoch:  ownerEpoch,
				ExpiresAt:   now.Add(replicationLeaseTTL),
				Version:     uint64(now.UnixNano()),
				LastReason:  "leader_renew",
				LastActor:   s.Cluster.LocalNodeID(),
			})

			if policy.FailbackMode == clusterModels.ReplicationFailbackAuto &&
				strings.TrimSpace(policy.SourceNodeID) != "" &&
				strings.TrimSpace(policy.SourceNodeID) != owner {
				sourceOnline := false
				if sourceNode, ok := nodeByID[strings.TrimSpace(policy.SourceNodeID)]; ok {
					sourceOnline = strings.ToLower(strings.TrimSpace(sourceNode.Status)) == "online"
				}
				if sourceOnline {
					fbVal := s.failbackHitsIncr(policy.ID)
					if fbVal < uint64(replicationFailbackHitLimit) {
						continue
					}
					if err := s.failoverPolicyToNode(
						ctx,
						&policy,
						strings.TrimSpace(policy.SourceNodeID),
						"auto_failback",
						true,
						replicationTransitionOptions{
							AllowUnsafe:          false,
							MovePinnedSource:     false,
							TriggerValidationRun: true,
						},
					); err != nil {
						if isReplicationPolicyTransitionRunningError(err) {
							logger.L.Debug().Uint("policy_id", policy.ID).Msg("auto_failback_transition_already_running")
							continue
						}
						logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("auto_failback_failed")
					}
					s.failbackHitsReset(policy.ID)
				} else {
					s.failbackHitsReset(policy.ID)
				}
			}
			continue
		}

		downVal := s.downMissesIncr(policy.ID, uint64(replicationFailoverDownMissLimit+1))
		if downVal < uint64(replicationFailoverDownMissLimit) {
			continue
		}

		failoverMode := policyFailoverMode(&policy)
		if failoverMode == clusterModels.ReplicationFailoverManual {
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

		if failoverMode == clusterModels.ReplicationFailoverAutoSafe {
			_, _ = s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
				PolicyID:     &policy.ID,
				EventType:    "failover",
				Status:       replicationEventStatusFailed,
				Message:      "node_down_auto_safe_blocked_owner_unreachable",
				Error:        "safe_failover_requires_owner_reachable",
				SourceNodeID: owner,
				TargetNodeID: targetNodeID,
				GuestType:    policy.GuestType,
				GuestID:      policy.GuestID,
				StartedAt:    now,
				CompletedAt:  &now,
			}, false)
			s.downMissesSet(policy.ID, uint64(replicationFailoverDownMissLimit-1))
			continue
		}

		reason := "node_down_auto_safe"
		requireDemoteAck := true
		options := replicationTransitionOptions{
			AllowUnsafe:          false,
			MovePinnedSource:     false,
			TriggerValidationRun: true,
		}
		if failoverMode == clusterModels.ReplicationFailoverAutoForce {
			quorumOK, quorumErr := s.hasFailoverQuorum(nodeByID)
			if quorumErr != nil {
				logger.L.Warn().
					Err(quorumErr).
					Uint("policy_id", policy.ID).
					Msg("policy_failover_quorum_check_failed")
				continue
			}
			if !quorumOK {
				_, _ = s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
					PolicyID:     &policy.ID,
					EventType:    "failover",
					Status:       replicationEventStatusFailed,
					Message:      "node_down_auto_force_blocked_no_quorum",
					Error:        "force_failover_requires_quorum",
					SourceNodeID: owner,
					TargetNodeID: targetNodeID,
					GuestType:    policy.GuestType,
					GuestID:      policy.GuestID,
					StartedAt:    now,
					CompletedAt:  &now,
				}, false)
				continue
			}
			reason = "node_down_auto_force"
			requireDemoteAck = false
			options.AllowUnsafe = true
		}

		if err := s.failoverPolicyToNode(ctx, &policy, targetNodeID, reason, requireDemoteAck, options); err != nil {
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

		s.downMissesReset(policy.ID)
	}

	if len(renewals) > 0 {
		if err := s.Cluster.UpsertReplicationLeasesBatch(renewals); err != nil {
			time.Sleep(1 * time.Second)
			if retryErr := s.Cluster.UpsertReplicationLeasesBatch(renewals); retryErr != nil {
				logger.L.Warn().Err(retryErr).Int("count", len(renewals)).Msg("replication_lease_batch_renew_failed")
			}
		}
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

func nodeOnlineByID(nodeByID map[string]clusterModels.ClusterNode, nodeID string) bool {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false
	}
	node, ok := nodeByID[nodeID]
	if !ok {
		return false
	}
	return strings.ToLower(strings.TrimSpace(node.Status)) == "online"
}

func replicationPolicyHasTargetNode(policy *clusterModels.ReplicationPolicy, nodeID string) bool {
	if policy == nil {
		return false
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false
	}
	for _, target := range policy.Targets {
		if strings.TrimSpace(target.NodeID) == nodeID {
			return true
		}
	}
	return false
}

func (s *Service) hasFailoverQuorum(nodeByID map[string]clusterModels.ClusterNode) (bool, error) {
	if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return false, fmt.Errorf("not_leader")
	}
	if err := s.Cluster.Raft.VerifyLeader().Error(); err != nil {
		return false, err
	}

	cfgFuture := s.Cluster.Raft.GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		return false, err
	}

	totalVoters := 0
	onlineVoters := 0
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	for _, server := range cfgFuture.Configuration().Servers {
		if server.Suffrage != raft.Voter {
			continue
		}
		totalVoters++
		serverID := strings.TrimSpace(string(server.ID))
		if serverID == "" {
			continue
		}
		if nodeOnlineByID(nodeByID, serverID) || (localNodeID != "" && serverID == localNodeID) {
			onlineVoters++
		}
	}
	if totalVoters == 0 {
		return false, fmt.Errorf("raft_voter_set_empty")
	}
	required := (totalVoters / 2) + 1
	return onlineVoters >= required, nil
}

func (s *Service) EnqueueReplicationPolicyFailover(
	policyID uint,
	targetNodeID string,
	mode string,
	confirmDataLoss bool,
	movePinnedSource bool,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Cluster.Raft != nil && s.Cluster.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	requestMode := replicationFailoverRequestMode(mode)
	if requestMode == replicationFailoverRequestForce && !confirmDataLoss {
		return fmt.Errorf("confirm_data_loss_required_for_force_failover")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}
	if policy == nil {
		return fmt.Errorf("replication_policy_not_found")
	}
	baseEval := s.Cluster.EvaluateReplicationPolicyHA(policy)
	if !baseEval.Eligible {
		return replicationPolicyHAError(baseEval)
	}

	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID != "" {
		projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
			policy,
			targetNodeID,
			movePinnedSource,
		)
		transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
			policy,
			projectedSourceNodeID,
			projectedActiveNodeID,
		)
		if !transitionEval.Eligible {
			return replicationPolicyHAError(transitionEval)
		}
	}

	enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return db.EnqueueJSON(enqueueCtx, replicationFailoverJobQueueName, replicationFailoverJobPayload{
		PolicyID:         policyID,
		TargetNodeID:     targetNodeID,
		Mode:             requestMode,
		ConfirmDataLoss:  confirmDataLoss,
		MovePinnedSource: movePinnedSource,
	})
}

func (s *Service) RequestReplicationPolicyFailover(
	ctx context.Context,
	policyID uint,
	targetNodeID string,
	mode string,
	confirmDataLoss bool,
	movePinnedSource bool,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Cluster.Raft != nil && s.Cluster.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	requestMode := replicationFailoverRequestMode(mode)
	if requestMode == replicationFailoverRequestForce && !confirmDataLoss {
		return fmt.Errorf("confirm_data_loss_required_for_force_failover")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}
	if policy == nil {
		return fmt.Errorf("replication_policy_not_found")
	}
	baseEval := s.Cluster.EvaluateReplicationPolicyHA(policy)
	if !baseEval.Eligible {
		return replicationPolicyHAError(baseEval)
	}

	nodes, err := s.Cluster.Nodes()
	if err != nil {
		return err
	}
	nodeByID := make(map[string]clusterModels.ClusterNode, len(nodes))
	for _, node := range nodes {
		nodeByID[strings.TrimSpace(node.NodeUUID)] = node
	}

	ownerNodeID := replicationPolicyOwnerNode(policy)
	if ownerNodeID == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if requestMode == replicationFailoverRequestSafe && !nodeOnlineByID(nodeByID, ownerNodeID) {
		return fmt.Errorf("safe_failover_requires_online_owner_use_force_for_owner_down")
	}

	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID == "" {
		selectedTarget, selectErr := s.selectFailoverTarget(policy, ownerNodeID, nodeByID)
		if selectErr != nil {
			return selectErr
		}
		targetNodeID = selectedTarget
	}
	if targetNodeID == ownerNodeID {
		return fmt.Errorf("replication_target_same_as_owner")
	}
	if !replicationPolicyHasTargetNode(policy, targetNodeID) {
		return fmt.Errorf("replication_target_not_configured_for_policy")
	}
	if !nodeOnlineByID(nodeByID, targetNodeID) {
		return fmt.Errorf("replication_target_node_offline")
	}

	projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
		policy,
		targetNodeID,
		movePinnedSource,
	)
	transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
		policy,
		projectedSourceNodeID,
		projectedActiveNodeID,
	)
	if !transitionEval.Eligible {
		return replicationPolicyHAError(transitionEval)
	}

	options := replicationTransitionOptions{
		AllowUnsafe:          requestMode == replicationFailoverRequestForce,
		MovePinnedSource:     movePinnedSource,
		TriggerValidationRun: true,
	}
	requireDemoteAck := requestMode == replicationFailoverRequestSafe
	reason := "manual_failover"
	if requestMode == replicationFailoverRequestForce {
		quorumOK, quorumErr := s.hasFailoverQuorum(nodeByID)
		if quorumErr != nil {
			return fmt.Errorf("force_failover_quorum_check_failed: %w", quorumErr)
		}
		if !quorumOK {
			return fmt.Errorf("force_failover_requires_quorum")
		}
		reason = "manual_force_failover"
	}

	return s.failoverPolicyToNode(ctx, policy, targetNodeID, reason, requireDemoteAck, options)
}

func (s *Service) failoverPolicyToNode(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	reason string,
	requireDemoteAck bool,
	options replicationTransitionOptions,
) error {
	if policy == nil || targetNodeID == "" {
		return fmt.Errorf("invalid_failover_input")
	}
	if !s.acquirePolicyTransition(policy.ID) {
		return errReplicationPolicyTransitionAlreadyRunning
	}
	defer s.releasePolicyTransition(policy.ID)

	return s.runPolicyOwnershipTransition(ctx, policy, targetNodeID, reason, requireDemoteAck, options)
}

func (s *Service) runPolicyOwnershipTransition(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	reason string,
	requireDemoteAck bool,
	options replicationTransitionOptions,
) error {
	if policy == nil || targetNodeID == "" {
		return fmt.Errorf("invalid_policy_transition_input")
	}

	baseEval := s.Cluster.EvaluateReplicationPolicyHA(policy)
	if !baseEval.Eligible {
		return replicationPolicyHAError(baseEval)
	}

	projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
		policy,
		targetNodeID,
		options.MovePinnedSource,
	)
	transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
		policy,
		projectedSourceNodeID,
		projectedActiveNodeID,
	)
	if !transitionEval.Eligible {
		return replicationPolicyHAError(transitionEval)
	}

	previousOwner := replicationPolicyOwnerNode(policy)
	previousSourceNodeID := strings.TrimSpace(policy.SourceNodeID)
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

			if s.TelemetryDB != nil {
				auditStatus := "success"
				errMsg := ""
				if transitionErr != nil {
					auditStatus = "failed"
					errMsg = transitionErr.Error()
				}
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_failover", policy.ID, auditStatus, errMsg, map[string]any{
					"eventId": eventID,
					"status":  auditStatus,
					"error":   errMsg,
				})
			}
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
		return s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition)
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
		} else if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned && previousSourceNodeID != "" {
			rollbackReq.SourceNodeID = previousSourceNodeID
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
			ExpiresAt:   time.Now().UTC().Add(replicationLeaseTTL),
			Version:     uint64(time.Now().UTC().UnixNano()),
			LastReason:  reason + "_rollback",
			LastActor:   s.Cluster.LocalNodeID(),
		}
		if err := s.Cluster.UpsertReplicationLease(rollbackLease, false); err != nil {
			return err
		}

		policy.SourceNodeID = rollbackReq.SourceNodeID
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
		if !options.AllowUnsafe {
			transitionErr := fmt.Errorf("unsafe_failover_blocked_without_demote_and_catchup")
			markTransitionFailed(transitionErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_blocked_without_demote_or_catchup", transitionErr, true)
			return transitionErr
		}
		updateTransitionEvent(replicationEventStatusDemoting, reason+"_unsafe_force_allowed", nil, false)
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
	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned && options.MovePinnedSource {
		req.SourceNodeID = targetNodeID
	}
	req.ActiveNodeID = targetNodeID
	req.OwnerEpoch = nextEpoch

	if err := s.Cluster.ProposeReplicationPolicyUpdate(policy.ID, req, false); err != nil {
		markTransitionFailed(err)
		updateTransitionEvent(replicationEventStatusFailed, reason+"_demoting_failed", err, true)
		return err
	}

	policy.SourceNodeID = req.SourceNodeID
	policy.ActiveNodeID = targetNodeID
	policy.OwnerEpoch = nextEpoch

	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: targetNodeID,
		OwnerEpoch:  nextEpoch,
		ExpiresAt:   time.Now().UTC().Add(replicationLeaseTTL),
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

	if err := s.rebindReplicationGuestBackupJobRunners(policy, targetNodeID); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("policy_id", policy.ID).
			Str("target_node_id", strings.TrimSpace(targetNodeID)).
			Msg("replication_backup_job_runner_rebind_failed")
	}

	if options.TriggerValidationRun {
		if err := s.enqueueReplicationValidationRun(ctx, policy.ID, targetNodeID); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Str("target_node_id", strings.TrimSpace(targetNodeID)).
				Msg("replication_post_transition_validation_enqueue_failed")
		}
	}

	return nil
}

func (s *Service) forwardActivateReplicationPolicy(nodeID string, policyID uint) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "activate", map[string]any{
		"policyId": policyID,
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardDemoteReplicationPolicy(nodeID string, policyID uint, ownerEpoch uint64) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "demote", map[string]any{
		"policyId":   policyID,
		"ownerEpoch": ownerEpoch,
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardCatchupReplicationPolicy(
	nodeID string,
	policyID uint,
	targetNodeID string,
	ownerEpoch uint64,
) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "catchup", map[string]any{
		"policyId":     policyID,
		"targetNodeId": targetNodeID,
		"ownerEpoch":   ownerEpoch,
	}, replicationControlCatchupTimeout)
}

func (s *Service) forwardRunReplicationPolicy(nodeID string, policyID uint) error {
	return s.forwardReplicationPolicyControl(nodeID, "run", map[string]any{
		"policyId": policyID,
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardCleanupReplicationPolicyDelete(nodeID string, policyID uint) error {
	return s.forwardReplicationPolicyControl(nodeID, "cleanup-policy-delete", map[string]any{
		"policyId": policyID,
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardReplicationReceipt(nodeID string, receipt clusterModels.ReplicationReceipt) error {
	payload := map[string]any{
		"policyId":          receipt.PolicyID,
		"guestType":         strings.TrimSpace(receipt.GuestType),
		"guestId":           receipt.GuestID,
		"sourceNodeId":      strings.TrimSpace(receipt.SourceNodeID),
		"targetNodeId":      strings.TrimSpace(receipt.TargetNodeID),
		"status":            strings.TrimSpace(receipt.Status),
		"message":           strings.TrimSpace(receipt.Message),
		"error":             strings.TrimSpace(receipt.Error),
		"lastAttemptAt":     receipt.LastAttemptAt,
		"lastSourceDataset": strings.TrimSpace(receipt.LastSourceDataset),
		"lastTargetDataset": strings.TrimSpace(receipt.LastTargetDataset),
	}
	if receipt.LastSuccessAt != nil {
		payload["lastSuccessAt"] = receipt.LastSuccessAt
	}

	return s.forwardReplicationPolicyControl(nodeID, "replication-receipt", payload, replicationControlReceiptTimeout)
}

func backupJobGuestIdentity(job *clusterModels.BackupJob) (string, uint) {
	if job == nil {
		return "", 0
	}

	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	if mode != clusterModels.BackupJobModeJail && mode != clusterModels.BackupJobModeVM {
		return "", 0
	}

	kind, guestID := inferRestoreDatasetKind(strings.TrimSpace(job.JailRootDataset))
	if guestID == 0 {
		kind, guestID = inferRestoreDatasetKind(strings.TrimSpace(job.SourceDataset))
	}
	if guestID == 0 {
		return "", 0
	}
	if kind != clusterModels.BackupJobModeJail && kind != clusterModels.BackupJobModeVM {
		return "", 0
	}

	return kind, guestID
}

func backupJobToReqWithRunner(job *clusterModels.BackupJob, runnerNodeID string) clusterServiceInterfaces.BackupJobReq {
	enabled := false
	if job != nil {
		enabled = job.Enabled
	}

	req := clusterServiceInterfaces.BackupJobReq{
		RunnerNodeID: strings.TrimSpace(runnerNodeID),
		Enabled:      &enabled,
	}
	if job == nil {
		return req
	}

	req.Name = strings.TrimSpace(job.Name)
	req.TargetID = job.TargetID
	req.Mode = strings.TrimSpace(job.Mode)
	req.SourceDataset = strings.TrimSpace(job.SourceDataset)
	req.JailRootDataset = strings.TrimSpace(job.JailRootDataset)
	req.PruneKeepLast = job.PruneKeepLast
	req.PruneTarget = job.PruneTarget
	req.StopBeforeBackup = job.StopBeforeBackup
	req.CronExpr = strings.TrimSpace(job.CronExpr)
	return req
}

func (s *Service) rebindReplicationGuestBackupJobRunners(policy *clusterModels.ReplicationPolicy, runnerNodeID string) error {
	if s == nil || s.Cluster == nil || policy == nil || policy.ID == 0 {
		return nil
	}

	runnerNodeID = strings.TrimSpace(runnerNodeID)
	if runnerNodeID == "" {
		return nil
	}

	policyGuestType := strings.ToLower(strings.TrimSpace(policy.GuestType))
	policyGuestID := policy.GuestID
	if policyGuestType == "" || policyGuestID == 0 {
		return nil
	}

	jobs, err := s.Cluster.ListBackupJobs(0)
	if err != nil {
		return fmt.Errorf("list_backup_jobs_failed: %w", err)
	}

	updateErrs := make([]string, 0)
	for i := range jobs {
		job := jobs[i]
		jobGuestType, jobGuestID := backupJobGuestIdentity(&job)
		if jobGuestType != policyGuestType || jobGuestID != policyGuestID {
			continue
		}
		if strings.TrimSpace(job.RunnerNodeID) == runnerNodeID {
			continue
		}

		req := backupJobToReqWithRunner(&job, runnerNodeID)
		if err := s.Cluster.ProposeBackupJobUpdate(job.ID, req, false); err != nil {
			updateErrs = append(updateErrs, fmt.Sprintf("job_%d_update_failed: %v", job.ID, err))
			continue
		}

		logger.L.Info().
			Uint("policy_id", policy.ID).
			Uint("job_id", job.ID).
			Str("guest_type", policyGuestType).
			Uint("guest_id", policyGuestID).
			Str("runner_node_id", runnerNodeID).
			Msg("replication_backup_job_runner_rebound")
	}

	if len(updateErrs) > 0 {
		return fmt.Errorf("backup_job_runner_rebind_partial_failure: %s", strings.Join(updateErrs, "; "))
	}
	return nil
}

func (s *Service) emitReplicationReceiptBestEffort(nodeID string, receipt clusterModels.ReplicationReceipt) error {
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return fmt.Errorf("replication_target_node_required")
	}
	if receipt.PolicyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	receipt.TargetNodeID = nodeID
	if receipt.LastAttemptAt.IsZero() {
		receipt.LastAttemptAt = time.Now().UTC()
	}
	if strings.TrimSpace(strings.ToLower(receipt.Status)) == "success" && receipt.LastSuccessAt == nil {
		lastSuccessAt := receipt.LastAttemptAt
		receipt.LastSuccessAt = &lastSuccessAt
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID != "" && localNodeID == nodeID {
		return s.Cluster.UpsertLocalReplicationReceipt(receipt)
	}

	var lastErr error
	for attempt := 0; attempt < replicationReceiptForwardAttempts; attempt++ {
		if err := s.forwardReplicationReceipt(nodeID, receipt); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(replicationReceiptForwardBackoff * time.Duration(attempt+1))
	}

	return lastErr
}

func (s *Service) enqueueReplicationValidationRun(ctx context.Context, policyID uint, targetNodeID string) error {
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

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID != "" && targetNodeID == localNodeID {
		return s.EnqueueReplicationPolicyRun(ctx, policyID)
	}
	return s.forwardRunReplicationPolicy(targetNodeID, policyID)
}

func (s *Service) forwardReplicationPolicyControlWithRetry(
	nodeID string,
	action string,
	payload map[string]any,
	timeout time.Duration,
) error {
	var lastErr error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err := s.forwardReplicationPolicyControl(nodeID, action, payload, timeout); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < replicationControlForwardAttempts-1 {
			time.Sleep(replicationControlForwardBackoff * time.Duration(attempt+1))
		}
	}
	return lastErr
}

func (s *Service) forwardReplicationPolicyControl(nodeID string, action string, payload map[string]any, timeout time.Duration) error {
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
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal_replication_control_payload_failed: %w", err)
	}
	if timeout <= 0 {
		timeout = replicationControlDefaultTimeout
	}

	_, statusCode, err := utils.HTTPPostJSONWithTimeout(url, body, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}, timeout)
	if err != nil {
		return fmt.Errorf("replication_control_%s_failed_status_%d: %w", strings.TrimSpace(action), statusCode, err)
	}
	return nil
}

func (s *Service) pruneLocalReplicationReceipts() error {
	if s == nil || s.Cluster == nil {
		return nil
	}
	return s.Cluster.PruneLocalReplicationReceipts(strings.TrimSpace(s.Cluster.LocalNodeID()))
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

	cleanupErrs := make([]string, 0)
	if err := s.Cluster.DeleteLocalReplicationReceiptsByPolicy(policyID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("delete_local_replication_receipts_failed: %v", err))
	}

	s.replicationCountersDelete(policyID)

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		if len(cleanupErrs) > 0 {
			return fmt.Errorf("replication_policy_delete_local_cleanup_failed: %s; policy_lookup_failed: %v", strings.Join(cleanupErrs, "; "), err)
		}
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
		if err := s.destroyLocalDatasetIncludingDependentsWithRetry(ctx, dataset, 20, 500*time.Millisecond); err != nil {
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

	if ok, holder := s.acquireWorkloadOperation(
		policy.GuestType,
		policy.GuestID,
		fmt.Sprintf("replication_demote:%d", policy.ID),
	); !ok {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			strings.ToLower(strings.TrimSpace(policy.GuestType)),
			policy.GuestID,
		)
	}
	defer s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)

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

	if ok, holder := s.acquireWorkloadOperation(
		policy.GuestType,
		policy.GuestID,
		fmt.Sprintf("replication_catchup:%d", policy.ID),
	); !ok {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			strings.ToLower(strings.TrimSpace(policy.GuestType)),
			policy.GuestID,
		)
	}
	defer s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)

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
		if isReplicationResumeStateError(runErr) {
			abortOut, abortErr := s.abortTargetResumableReceiveState(ctx, targetSpec, destSuffix)
			if strings.TrimSpace(abortOut) != "" {
				logger.L.Debug().
					Uint("policy_id", policyID).
					Str("target_node_id", targetNodeID).
					Str("source_dataset", sourceDataset).
					Str("output", abortOut).
					Msg("replication_catchup_resume_abort_output")
			}
			if abortErr != nil {
				return fmt.Errorf(
					"replication_catchup_to_target_%s_failed_after_resume_abort_failed: %w (original: %v)",
					targetNodeID,
					abortErr,
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
					Msg("replication_catchup_resume_retry_output")
			}
			if retryErr != nil {
				return fmt.Errorf(
					"replication_catchup_to_target_%s_failed_after_resume_abort_retry: %w (original: %v)",
					targetNodeID,
					retryErr,
					runErr,
				)
			}
			continue
		}
		if isReplicationTargetModifiedError(runErr) {
			return fmt.Errorf(
				"replication_catchup_to_target_%s_failed_diverged_target_unrecoverable: %w",
				targetNodeID,
				runErr,
			)
		}
		return fmt.Errorf("replication_catchup_to_target_%s_failed: %w", targetNodeID, runErr)
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
				return net.JoinHostPort(host, strconv.Itoa(clusterService.ClusterEmbeddedHTTPSPort)), nil
			}
		}
	}

	return "", fmt.Errorf("replication_target_node_not_found")
}

func (s *Service) replicationPolicyToReq(policy *clusterModels.ReplicationPolicy) clusterServiceInterfaces.ReplicationPolicyReq {
	req := clusterServiceInterfaces.ReplicationPolicyReq{
		Name:         policy.Name,
		Description:  policy.Description,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		SourceNodeID: policy.SourceNodeID,
		OwnerEpoch:   replicationPolicyOwnerEpoch(policy),
		SourceMode:   policy.SourceMode,
		FailbackMode: policy.FailbackMode,
		FailoverMode: policy.FailoverMode,
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

	if ok, holder := s.acquireWorkloadOperation(
		policy.GuestType,
		policy.GuestID,
		fmt.Sprintf("replication_activate:%d", policy.ID),
	); !ok {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			strings.ToLower(strings.TrimSpace(policy.GuestType)),
			policy.GuestID,
		)
	}
	defer s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)

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

	if err := s.Jail.ForceStopJail(ctID); err != nil {
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

func (s *Service) forceKillReplicationJail(ctx context.Context, ctID uint) error {
	if s.Jail == nil {
		return nil
	}

	if err := s.stopLocalJailIfPresent(ctID); err != nil {
		return err
	}

	if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", ctID).Update("intentionally_stopped", false).Error; err != nil {
		return fmt.Errorf("force_kill_jail_reset_intentionally_stopped_failed: %w", err)
	}

	return nil
}

func (s *Service) forceKillReplicationVM(ctx context.Context, rid uint) error {
	if s.VM == nil {
		return nil
	}

	vm, err := s.findVMByRID(rid)
	if err != nil {
		return err
	}
	if vm == nil {
		return nil
	}

	isShutOff, err := s.VM.IsDomainShutOff(rid)
	if err != nil && !isVMDomainNotFoundError(err) {
		return fmt.Errorf("force_kill_vm_state_check_failed: %w", err)
	}
	if isShutOff || isVMDomainNotFoundError(err) {
		return nil
	}

	if err := s.VM.ForceStopVM(rid); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not running") || isVMDomainNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("force_kill_vm_stop_failed: %w", err)
	}

	if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", rid).Update("intentionally_stopped", false).Error; err != nil {
		return fmt.Errorf("force_kill_vm_reset_intentionally_stopped_failed: %w", err)
	}

	return nil
}

func (s *Service) unfenceReplicationGuestDatasetsIfNeeded(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.GuestID == 0 {
		return nil
	}

	rootDataset, err := s.findLocalGuestDataset(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return fmt.Errorf("unfence_find_dataset_failed: %w", err)
	}
	if rootDataset == "" {
		return nil
	}

	val, err := utils.RunCommandWithContext(ctx, "zfs", "get", "-H", "-o", "value", "readonly", rootDataset)
	if err != nil {
		return fmt.Errorf("unfence_check_readonly_failed: %w", err)
	}
	if strings.TrimSpace(val) != "on" {
		return nil
	}

	logger.L.Info().
		Uint("policy_id", policy.ID).
		Uint("guest_id", policy.GuestID).
		Str("guest_type", policy.GuestType).
		Str("root_dataset", rootDataset).
		Msg("replication_self_fence_unfencing_datasets")

	if err := s.prepareReplicatedDatasetForActivation(ctx, rootDataset); err != nil {
		return fmt.Errorf("unfence_prepare_dataset_failed: %w", err)
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

func (s *Service) rollbackReplicationDatasetActivation(ctx context.Context, datasets []string) {
	for i := len(datasets) - 1; i >= 0; i-- {
		ds, err := s.getLocalDataset(ctx, datasets[i])
		if err != nil || ds == nil {
			continue
		}
		_ = ds.Unmount(ctx, true)
		if err := ds.SetProperties(ctx, "readonly", "on"); err != nil {
			logger.L.Warn().
				Err(err).
				Str("dataset", datasets[i]).
				Msg("rollback_activation_readonly_failed")
		}
	}
}

func (s *Service) prepareReplicatedDatasetForActivation(ctx context.Context, rootDataset string) error {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil
	}

	filesystems, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return err
	}
	volumes, volErr := s.listLocalVolumeDatasets(ctx)
	if volErr != nil {
		logger.L.Warn().Err(volErr).Msg("failed_to_list_volumes_for_replication_activation")
	}

	seen := map[string]struct{}{
		rootDataset: {},
	}
	subtree := []string{rootDataset}
	prefix := rootDataset + "/"

	for _, candidate := range filesystems {
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

	volSubtree := []string{}
	for _, candidate := range volumes {
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
		volSubtree = append(volSubtree, ds)
	}

	sort.SliceStable(subtree, func(i, j int) bool {
		di := strings.Count(subtree[i], "/")
		dj := strings.Count(subtree[j], "/")
		if di == dj {
			return subtree[i] < subtree[j]
		}
		return di < dj
	})
	sort.Strings(volSubtree)

	var activated []string
	rolledBack := false

	defer func() {
		if rolledBack {
			return
		}
		if len(activated) > 0 {
			s.rollbackReplicationDatasetActivation(context.Background(), activated)
		}
	}()

	for idx, dataset := range subtree {
		ds, err := s.getLocalDataset(ctx, dataset)
		if err != nil {
			return fmt.Errorf("failed_to_open_replication_dataset_%s: %w", dataset, err)
		}
		if ds == nil {
			continue
		}

		if ds.IsEncrypted() {
			keyLoaded, err := s.ensureEncryptionKeyForDataset(ctx, ds)
			if err != nil {
				return fmt.Errorf("replication_encryption_key_failed_%s: %w", dataset, err)
			}
			if !keyLoaded {
				return fmt.Errorf("replication_encryption_key_not_available_for_%s: run 'zfs load-key %s' first", dataset, dataset)
			}
		}

		if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
			return fmt.Errorf("failed_to_set_replication_dataset_properties_%s: %w", dataset, err)
		}
		activated = append(activated, dataset)

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

	for _, dataset := range volSubtree {
		ds, err := s.getLocalDataset(ctx, dataset)
		if err != nil {
			return fmt.Errorf("failed_to_open_replication_volume_%s: %w", dataset, err)
		}
		if ds == nil {
			continue
		}

		if ds.IsEncrypted() {
			keyLoaded, err := s.ensureEncryptionKeyForDataset(ctx, ds)
			if err != nil {
				return fmt.Errorf("replication_encryption_key_failed_%s: %w", dataset, err)
			}
			if !keyLoaded {
				return fmt.Errorf("replication_encryption_key_not_available_for_%s: run 'zfs load-key %s' first", dataset, dataset)
			}
		}

		if err := ds.SetProperties(ctx, "readonly", "off"); err != nil {
			return fmt.Errorf("failed_to_set_replication_volume_readonly_%s: %w", dataset, err)
		}
		activated = append(activated, dataset)
	}

	rolledBack = true
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
	if err := s.DB.Find(&policies).Error; err != nil {
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
				if err := s.unfenceReplicationGuestDatasetsIfNeeded(ctx, &policy); err != nil {
					logger.L.Warn().
						Err(err).
						Uint("policy_id", policy.ID).
						Uint("guest_id", policy.GuestID).
						Str("guest_type", strings.TrimSpace(policy.GuestType)).
						Msg("replication_self_fence_unfence_failed")
				}
				continue
			}

			fenceReason = replicationFenceReasonOwnerLeaseInvalid

			if s.Cluster.Raft != nil &&
				s.Cluster.Raft.State() != raft.Leader {
				_, leaderID := s.Cluster.Raft.LeaderWithID()
				if string(leaderID) == "" &&
					time.Now().UTC().Before(lease.ExpiresAt) {
					logger.L.Warn().
						Uint("policy_id", policy.ID).
						Msg("replication_self_fence_deferred_no_raft_leader_lease_still_valid")
					continue
				}
			}
		}

		if s.IsPolicyTransitionRunning(policy.ID) {
			continue
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
		if !s.replicationGuestExistsLocally(policy.GuestType, policy.GuestID) {
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

func (s *Service) isReplicationGuestRunning(guestType string, guestID uint) (bool, error) {
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		if s.VM == nil {
			return false, nil
		}
		shutoff, err := s.VM.IsDomainShutOff(guestID)
		if err != nil {
			if isVMDomainNotFoundError(err) {
				return false, nil
			}
			return false, err
		}
		return !shutoff, nil
	case clusterModels.ReplicationGuestTypeJail:
		if s.Jail == nil {
			return false, nil
		}
		return s.Jail.IsJailRunning(guestID)
	default:
		return false, nil
	}
}

func (s *Service) isReplicationGuestIntentionallyStopped(guestType string, guestID uint) (bool, error) {
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		var vm vmModels.VM
		if err := s.DB.Where("rid = ?", guestID).First(&vm).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		return vm.IntentionallyStopped, nil
	case clusterModels.ReplicationGuestTypeJail:
		var jail jailModels.Jail
		if err := s.DB.Where("ct_id = ?", guestID).First(&jail).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		return jail.IntentionallyStopped, nil
	default:
		return false, nil
	}
}

func (s *Service) recoverCrashedReplicationGuests(ctx context.Context) error {
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
		if expectedOwner == "" || expectedOwner != localNodeID {
			continue
		}

		expectedEpoch := replicationPolicyOwnerEpoch(&policy)
		if expectedEpoch == 0 {
			continue
		}

		lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
		if err != nil || lease == nil {
			continue
		}
		if lease.OwnerNodeID != localNodeID || lease.OwnerEpoch != expectedEpoch ||
			time.Now().UTC().After(lease.ExpiresAt) {
			continue
		}

		if !policy.CrashRecovery {
			s.crashMissesReset(policy.ID)
			continue
		}

		intentionallyStopped, err := s.isReplicationGuestIntentionallyStopped(policy.GuestType, policy.GuestID)
		if err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Msg("replication_crash_recovery_intentional_stop_check_failed")
			continue
		}
		if intentionallyStopped {
			s.crashMissesReset(policy.ID)
			continue
		}

		running, err := s.isReplicationGuestRunning(policy.GuestType, policy.GuestID)
		if err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Msg("replication_crash_recovery_guest_check_failed")
			running = false
		}

		if running {
			s.crashMissesReset(policy.ID)
			continue
		}

		crashLimit := policy.CrashRestartMax
		if crashLimit < 1 {
			crashLimit = replicationCrashRestartLimit
		}
		crashVal := s.crashMissesIncr(policy.ID, uint64(crashLimit+1))
		if crashVal < uint64(crashLimit) {
			logger.L.Warn().
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Int("crash_miss", int(crashVal)).
				Msg("replication_guest_crashed_attempting_local_restart")

			if err := s.restartReplicationGuestLocally(policy.GuestType, policy.GuestID); err != nil {
				logger.L.Warn().
					Err(err).
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Msg("replication_crash_recovery_local_restart_failed")
			}
			continue
		}

		if crashVal != uint64(crashLimit) {
			continue
		}

		logger.L.Warn().
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Msg("replication_guest_crash_restart_exhausted_initiating_failover")

		requestMode := replicationFailoverRequestSafe
		var confirmDataLoss bool
		switch policy.FailoverMode {
		case clusterModels.ReplicationFailoverAutoForce:
			requestMode = replicationFailoverRequestForce
			confirmDataLoss = true
		case clusterModels.ReplicationFailoverManual:
			logger.L.Warn().
				Uint("policy_id", policy.ID).
				Msg("replication_guest_crashed_manual_failover_mode_no_auto_action")
			continue
		}

		targetNodeID, err := s.selectCrashRecoveryFailoverTarget(&policy, localNodeID)
		if err != nil || targetNodeID == "" {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Msg("replication_crash_recovery_no_failover_target")
			continue
		}

		if err := s.enqueueFailoverToLeader(policy.ID, targetNodeID,
			requestMode, confirmDataLoss, false); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Msg("replication_crash_recovery_failover_enqueue_failed")
			continue
		}

		s.crashMissesReset(policy.ID)
	}

	s.checkLocalPoolHealth(ctx, localNodeID)

	return nil
}

func (s *Service) restartReplicationGuestLocally(guestType string, guestID uint) error {
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		if s.VM == nil {
			return fmt.Errorf("vm_service_unavailable")
		}
		vm, err := s.findVMByRID(guestID)
		if err != nil {
			return err
		}
		if vm == nil {
			return fmt.Errorf("vm_not_found")
		}
		return s.VM.LvVMAction(*vm, "start")
	case clusterModels.ReplicationGuestTypeJail:
		if s.Jail == nil {
			return fmt.Errorf("jail_service_unavailable")
		}
		return s.Jail.JailAction(int(guestID), "start")
	default:
		return fmt.Errorf("unknown_guest_type")
	}
}

func (s *Service) selectCrashRecoveryFailoverTarget(policy *clusterModels.ReplicationPolicy, localNodeID string) (string, error) {
	nodes, err := s.Cluster.Nodes()
	if err != nil {
		return "", err
	}
	nodeByID := make(map[string]clusterModels.ClusterNode, len(nodes))
	for _, node := range nodes {
		nodeByID[strings.TrimSpace(node.NodeUUID)] = node
	}
	return s.selectFailoverTarget(policy, localNodeID, nodeByID)
}

func (s *Service) enqueueFailoverToLeader(policyID uint, targetNodeID string, mode string, confirmDataLoss bool, movePinnedSource bool) error {
	if s.Cluster == nil || s.Cluster.Raft == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Cluster.Raft.State() == raft.Leader {
		return s.EnqueueReplicationPolicyFailover(policyID, targetNodeID, mode, confirmDataLoss, movePinnedSource)
	}
	_, leaderID := s.Cluster.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	if leaderNodeID == "" {
		return fmt.Errorf("leader_not_available")
	}
	return s.forwardReplicationPolicyControl(leaderNodeID, "replication-failover-enqueue", map[string]any{
		"policy_id":          policyID,
		"target_node_id":     targetNodeID,
		"mode":               mode,
		"confirm_data_loss":  confirmDataLoss,
		"move_pinned_source": movePinnedSource,
	}, replicationControlDefaultTimeout)
}

func (s *Service) checkLocalPoolHealth(ctx context.Context, localNodeID string) {
	if s.GZFS == nil || s.GZFS.Zpool == nil {
		return
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		logger.L.Warn().Err(err).Msg("replication_pool_health_policy_query_failed")
		return
	}

	poolCache := make(map[string]*gzfs.ZPool)
	for _, policy := range policies {
		expectedOwner := replicationPolicyOwnerNode(&policy)
		if expectedOwner == "" || expectedOwner != localNodeID {
			continue
		}

		expectedEpoch := replicationPolicyOwnerEpoch(&policy)
		if expectedEpoch == 0 {
			continue
		}

		lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
		if err != nil || lease == nil {
			continue
		}
		if lease.OwnerNodeID != localNodeID || lease.OwnerEpoch != expectedEpoch ||
			time.Now().UTC().After(lease.ExpiresAt) {
			continue
		}

		if !policy.PoolHealthCheck {
			continue
		}

		var pools []string
		switch strings.TrimSpace(policy.GuestType) {
		case clusterModels.ReplicationGuestTypeVM:
			seen := make(map[string]struct{})
			var rawPools []string
			_ = s.DB.Model(&vmModels.Storage{}).
				Select("DISTINCT vm_storages.pool").
				Joins("JOIN vms ON vms.id = vm_storages.vm_id").
				Where("vms.rid = ? AND vm_storages.pool != ''", policy.GuestID).
				Pluck("pool", &rawPools)
			for _, p := range rawPools {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if _, ok := seen[p]; ok {
					continue
				}
				seen[p] = struct{}{}
				pools = append(pools, p)
			}
			var datasetPools []string
			_ = s.DB.Model(&vmModels.VMStorageDataset{}).
				Select("DISTINCT vm_storage_datasets.pool").
				Joins("JOIN vm_storages ON vm_storages.dataset_id = vm_storage_datasets.id").
				Joins("JOIN vms ON vms.id = vm_storages.vm_id").
				Where("vms.rid = ? AND vm_storage_datasets.pool != ''", policy.GuestID).
				Pluck("pool", &datasetPools)
			for _, p := range datasetPools {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if _, ok := seen[p]; ok {
					continue
				}
				seen[p] = struct{}{}
				pools = append(pools, p)
			}
		case clusterModels.ReplicationGuestTypeJail:
			var jail jailModels.Jail
			if err := s.DB.Where("ct_id = ?", policy.GuestID).First(&jail).Error; err != nil {
				continue
			}
			for _, storage := range jail.Storages {
				pool := strings.TrimSpace(storage.Pool)
				if pool == "" {
					continue
				}
				pools = append(pools, pool)
			}
		}

		for _, pool := range pools {
			p, ok := poolCache[pool]
			if !ok {
				var err error
				p, err = s.GZFS.Zpool.Get(ctx, pool)
				if err != nil {
					logger.L.Warn().Err(err).Str("pool", pool).Msg("replication_pool_health_get_failed")
					continue
				}
				if p == nil {
					continue
				}
				poolCache[pool] = p

				state := p.State
				healthy := state == gzfs.ZPoolStateOnline || state == ""
				if healthy {
					s.poolDownMisses[pool] = 0
				} else {
					s.poolDownMisses[pool]++
					if s.poolDownMisses[pool] == 3 {
						logger.L.Warn().
							Str("pool", pool).
							Str("state", string(state)).
							Uint("policy_id", policy.ID).
							Msg("replication_pool_unhealthy_initiating_failover")

						if err := s.enqueueFailoverToLeader(policy.ID, "", clusterModels.ReplicationFailoverAutoForce, true, false); err != nil {
							logger.L.Warn().Err(err).Str("pool", pool).Msg("replication_pool_unhealthy_failover_enqueue_failed")
						} else {
							s.poolDownMisses[pool] = 0
						}
					}
				}
			}

			if p.Size > 0 && p.Alloc < (1<<64)/100 {
				capacityPct := policy.PoolCapacityPct
				if capacityPct <= 0 {
					capacityPct = replicationLowPoolCapacityPercent
				}
				usedPercent := int(uint64(p.Alloc) * 100 / uint64(p.Size))
				if usedPercent > capacityPct {
					logger.L.Warn().
						Str("pool", pool).
						Int("used_pct", usedPercent).
						Uint("policy_id", policy.ID).
						Msg("replication_pool_capacity_high_initiating_failover")

					_ = s.enqueueFailoverToLeader(policy.ID, "", clusterModels.ReplicationFailoverAutoSafe, false, false)
				}
			}
		}
	}
}
