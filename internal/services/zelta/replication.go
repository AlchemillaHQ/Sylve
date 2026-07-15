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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	replicationJobQueueName         = "zelta-replication-run"
	replicationFailoverJobQueueName = "zelta-replication-failover"
)

const (
	defaultReplicationPruneKeepLast  = 64
	defaultReplicationLineageKeepOld = 2
	replicationFailoverDownMissLimit = 3
	replicationFailbackHitLimit      = 3
	replicationCrashRestartLimit     = 3
	replicationLeaseTTL              = 20 * time.Second
	// Forced cutover must wait substantially longer than the local fencing
	// poll interval.  This also leaves bounded room for clock skew between
	// nodes after the old owner's authoritative lease has expired.
	replicationSelfFenceInterval       = 2 * time.Second
	replicationLeaseRenewalInterval    = 5 * time.Second
	replicationLeaseExpirySafetyMargin = 30 * time.Second
	replicationLowPoolCapacityPercent  = 90

	replicationEventStatusRunning     = "running"
	replicationEventStatusDemoting    = "demoting"
	replicationEventStatusPromoting   = "promoting"
	replicationEventStatusActive      = "active"
	replicationEventStatusSuccess     = "success"
	replicationEventStatusFailed      = "failed"
	replicationEventStatusDegraded    = "degraded"
	replicationEventStatusInterrupted = "interrupted"

	replicationFailoverRequestSafe  = "safe"
	replicationFailoverRequestForce = "force"

	replicationControlDefaultTimeout       = 30 * time.Second
	replicationControlCatchupTimeout       = 2 * time.Hour
	replicationPromotionRollbackAfter      = 2 * time.Minute
	replicationTerminalEventRecreateWindow = 10 * time.Minute

	replicationControlForwardAttempts = 3
	replicationControlForwardBackoff  = 500 * time.Millisecond
)

type replicationJobPayload struct {
	PolicyID uint `json:"policy_id"`
}

type replicationFailoverJobPayload struct {
	PolicyID         uint   `json:"policy_id"`
	RunID            string `json:"run_id"`
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
var errReplicationRollbackPending = errors.New("replication_rollback_pending_recovery")
var errReplicationGenerationCommitUncertain = errors.New("replication_generation_commit_uncertain")
var errReplicationPreviousOwnerRecoveryUnconfirmed = errors.New("replication_previous_owner_recovery_unconfirmed")
var errReplicationTargetFallbackReadinessInvalidation = errors.New("replication_target_fallback_readiness_invalidation_failed")

type replicationFenceObservation struct {
	Policy         clusterModels.ReplicationPolicy
	LeaseOwner     string
	LeaseEpoch     uint64
	LeaseExpiresAt time.Time
}

type replicationEmergencyReadonlyChange struct {
	Dataset              string `json:"dataset"`
	DatasetGUID          string `json:"datasetGuid"`
	FenceToken           string `json:"fenceToken"`
	GuestType            string `json:"guestType"`
	GuestID              uint   `json:"guestId"`
	PreviousValue        string `json:"previousValue"`
	PreviousSource       string `json:"previousSource"`
	PreviousMarkerValue  string `json:"previousMarkerValue"`
	PreviousMarkerSource string `json:"previousMarkerSource"`
}

// These narrow capabilities deliberately remain outside the broad VM and jail
// service interfaces.  The watchdog needs a database-independent, host-runtime
// fail-stop path, while ordinary service stubs should not silently inherit a
// no-op implementation.
type emergencyVMRuntimeFencer interface {
	EmergencyStopAllManagedVMs(context.Context) error
}

type emergencyJailRuntimeFencer interface {
	EmergencyStopAllManagedJails(context.Context) error
}

type replicationFenceCacheState struct {
	mu           sync.RWMutex
	observations map[uint]replicationFenceObservation
}

var replicationFenceCaches sync.Map

var replicationEmergencyReadonlyMu sync.Mutex

const (
	replicationFenceObservationFile  = "replication-fence-observations.json"
	replicationEmergencyReadonlyFile = "replication-emergency-readonly.json"
	replicationEmergencyReadonlyProp = "sylve:emergency-readonly-token"
)

func (s *Service) replicationFenceCacheState() *replicationFenceCacheState {
	if existing, ok := replicationFenceCaches.Load(s); ok {
		return existing.(*replicationFenceCacheState)
	}
	created := &replicationFenceCacheState{observations: make(map[uint]replicationFenceObservation)}
	actual, _ := replicationFenceCaches.LoadOrStore(s, created)
	return actual.(*replicationFenceCacheState)
}

func (s *Service) snapshotReplicationFenceCache() map[uint]replicationFenceObservation {
	state := s.replicationFenceCacheState()
	state.mu.RLock()
	defer state.mu.RUnlock()
	out := make(map[uint]replicationFenceObservation, len(state.observations))
	for policyID, observation := range state.observations {
		out[policyID] = observation
	}
	return out
}

func (s *Service) replaceReplicationFenceCache(observations map[uint]replicationFenceObservation) {
	state := s.replicationFenceCacheState()
	state.mu.Lock()
	state.observations = observations
	state.mu.Unlock()
}

func replicationFenceObservationPath() (string, error) {
	dataPath := ""
	if configured, ok := os.LookupEnv("SYLVE_DATA_PATH"); ok {
		dataPath = strings.TrimSpace(configured)
	}
	if dataPath == "" && config.ParsedConfig != nil {
		dataPath = strings.TrimSpace(config.ParsedConfig.DataPath)
	}
	if dataPath == "" {
		return "", fmt.Errorf("replication_fence_observation_path_unconfigured")
	}
	if !filepath.IsAbs(dataPath) {
		absolute, err := filepath.Abs(dataPath)
		if err != nil {
			return "", err
		}
		dataPath = absolute
	}
	return filepath.Join(dataPath, replicationFenceObservationFile), nil
}

func loadDurableReplicationFenceObservations() (map[uint]replicationFenceObservation, error) {
	path, err := replicationFenceObservationPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[uint]replicationFenceObservation{}, nil
	}
	if err != nil {
		return nil, err
	}
	observations := make(map[uint]replicationFenceObservation)
	if len(data) == 0 {
		return observations, nil
	}
	if err := json.Unmarshal(data, &observations); err != nil {
		return nil, fmt.Errorf("decode_replication_fence_observations_failed: %w", err)
	}
	return observations, nil
}

func persistDurableReplicationFenceObservations(observations map[uint]replicationFenceObservation) error {
	path, err := replicationFenceObservationPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func replicationEmergencyReadonlyPath() (string, error) {
	observationPath, err := replicationFenceObservationPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(observationPath), replicationEmergencyReadonlyFile), nil
}

func loadDurableReplicationEmergencyReadonlyChanges() (map[string]replicationEmergencyReadonlyChange, error) {
	path, err := replicationEmergencyReadonlyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]replicationEmergencyReadonlyChange{}, nil
	}
	if err != nil {
		return nil, err
	}
	changes := make(map[string]replicationEmergencyReadonlyChange)
	if len(data) == 0 {
		return changes, nil
	}
	if err := json.Unmarshal(data, &changes); err != nil {
		return nil, fmt.Errorf("decode_replication_emergency_readonly_failed: %w", err)
	}
	return changes, nil
}

func persistDurableReplicationEmergencyReadonlyChanges(changes map[string]replicationEmergencyReadonlyChange) error {
	path, err := replicationEmergencyReadonlyPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return err
	}
	return nil
}

func replicationFenceObservationLeaseValid(
	observation replicationFenceObservation,
	localNodeID string,
	expectedOwner string,
	expectedEpoch uint64,
	now time.Time,
) bool {
	return strings.TrimSpace(expectedOwner) == strings.TrimSpace(localNodeID) &&
		strings.TrimSpace(observation.LeaseOwner) == strings.TrimSpace(localNodeID) &&
		observation.LeaseEpoch == expectedEpoch &&
		!observation.LeaseExpiresAt.IsZero() && now.UTC().Before(observation.LeaseExpiresAt.UTC())
}

func replicationFenceMissingLeaseDeadline(previousDeadline, policyCreatedAt time.Time) time.Time {
	if policyCreatedAt.IsZero() {
		return previousDeadline.UTC()
	}
	graceDeadline := policyCreatedAt.UTC().Add(2 * replicationLeaseTTL)
	if previousDeadline.IsZero() || graceDeadline.Before(previousDeadline) {
		return graceDeadline
	}
	return previousDeadline.UTC()
}

func appendReplicationStepError(base error, label string, detail error) error {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "replication_step_failed"
	}
	if detail == nil {
		detail = errors.New(label)
	}
	if base == nil {
		return fmt.Errorf("%s: %w", label, detail)
	}
	return fmt.Errorf("%v; %s: %w", base, label, detail)
}

type replicationOwnershipCommitDisposition uint8

const (
	replicationOwnershipCommitAmbiguous replicationOwnershipCommitDisposition = iota
	replicationOwnershipCommitNotApplied
	replicationOwnershipCommitApplied
)

func classifyReplicationOwnershipCommit(
	policy *clusterModels.ReplicationPolicy,
	lease *clusterModels.ReplicationLease,
	previousOwner string,
	targetOwner string,
	previousEpoch uint64,
	targetEpoch uint64,
	runID string,
) replicationOwnershipCommitDisposition {
	if policy == nil || strings.TrimSpace(policy.TransitionRunID) != strings.TrimSpace(runID) {
		return replicationOwnershipCommitAmbiguous
	}
	owner := replicationPolicyOwnerNode(policy)
	epoch := replicationPolicyOwnerEpoch(policy)
	state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
	if owner == strings.TrimSpace(targetOwner) && epoch == targetEpoch &&
		(state == clusterModels.ReplicationTransitionStatePromoting ||
			state == clusterModels.ReplicationTransitionStateCompleted) {
		if lease != nil && strings.TrimSpace(lease.OwnerNodeID) == owner &&
			lease.OwnerEpoch == epoch {
			return replicationOwnershipCommitApplied
		}
		return replicationOwnershipCommitAmbiguous
	}
	if owner == strings.TrimSpace(previousOwner) && epoch == previousEpoch {
		switch strings.ToLower(strings.TrimSpace(policy.TransitionState)) {
		case clusterModels.ReplicationTransitionStateDemoting,
			clusterModels.ReplicationTransitionStateCatchup:
			return replicationOwnershipCommitNotApplied
		}
	}
	return replicationOwnershipCommitAmbiguous
}

type replicationTransitionOptions struct {
	RunID                string
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
			logger.L.Warn().Msg("queued_replication_policy_invalid_payload_discarded")
			return nil
		}

		policy, err := s.Cluster.GetReplicationPolicyByID(payload.PolicyID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_not_found")
			// A queue record can outlive its policy.  Retrying a deleted or
			// otherwise terminal policy forever creates a poison message.
			return nil
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
			// Replication policy state/events already record the operational
			// failure.  The scheduler is the retry policy; one durable queue
			// message represents exactly one attempt.
			return nil
		}
		return nil
	})
}

func (s *Service) registerReplicationFailoverJob() {
	db.QueueRegisterJSON(replicationFailoverJobQueueName, func(ctx context.Context, payload replicationFailoverJobPayload) error {
		if payload.PolicyID == 0 {
			logger.L.Warn().Msg("queued_failover_invalid_payload_discarded")
			return nil
		}

		if strings.TrimSpace(payload.RunID) == "" {
			// Legacy failover messages predate idempotency keys. Replaying one
			// after an upgrade could move a workload twice, so require the user
			// or controller to submit a fresh request.
			logger.L.Warn().Uint("policy_id", payload.PolicyID).Msg("queued_failover_without_run_id_discarded")
			return nil
		}

		err := s.requestReplicationPolicyFailover(
			ctx,
			payload.PolicyID,
			strings.TrimSpace(payload.TargetNodeID),
			payload.Mode,
			payload.ConfirmDataLoss,
			payload.MovePinnedSource,
			payload.RunID,
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
			// The transition state machine owns recovery.  Returning an error
			// here would make goqite replay the same non-idempotent request
			// indefinitely.
			return nil
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
	if !replicationPolicyAllowsRuns(policy) {
		return fmt.Errorf("replication_policy_not_runnable")
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ReconcileReplicationEventsAfterRestart(); err != nil {
		logger.L.Warn().Err(err).Msg("replication_orphaned_event_cleanup_failed")
	}

	// Lease publication and local fencing deliberately have independent
	// loops.  A catch-up may legitimately block for hours; it must never
	// starve leases for unrelated policies or delay an expired owner's fence.
	var workers sync.WaitGroup
	startWorker := func(worker func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			worker(ctx)
		}()
	}

	startWorker(s.runReplicationLeaseRenewalLoop)
	startWorker(s.runReplicationSelfFenceLoop)
	startWorker(s.runReplicationSchedulingLoop)
	startWorker(s.runReplicationLeaderControlLoop)
	startWorker(s.runReplicationMaintenanceLoop)

	<-ctx.Done()
	workers.Wait()
}

func (s *Service) ReconcileReplicationEventsAfterRestart() error {
	if s == nil || s.Cluster == nil {
		return nil
	}
	startupCutoff := s.startedAt
	if startupCutoff.IsZero() {
		startupCutoff = s.now().UTC()
	}
	return s.interruptOrphanedLocalReplicationEvents(
		startupCutoff,
		strings.TrimSpace(s.Cluster.LocalNodeID()),
	)
}

func (s *Service) interruptOrphanedLocalReplicationEvents(startupCutoff time.Time, localNodeID string) error {
	if s == nil || s.DB == nil || strings.TrimSpace(localNodeID) == "" {
		return nil
	}

	query := s.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("event_type = ? AND status = ? AND completed_at IS NULL", "replication", replicationEventStatusRunning).
		Where("source_node_id = ? AND started_at < ?", strings.TrimSpace(localNodeID), startupCutoff.UTC())
	var orphaned []clusterModels.ReplicationEvent
	if err := query.Find(&orphaned).Error; err != nil {
		return err
	}

	if len(orphaned) > 0 {
		completedAt := s.now().UTC()
		eventIDs := make([]uint, 0, len(orphaned))
		for i := range orphaned {
			eventIDs = append(eventIDs, orphaned[i].ID)
		}
		result := s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id IN ?", eventIDs).Updates(map[string]any{
			"status":       replicationEventStatusInterrupted,
			"message":      "replication_run_interrupted",
			"error":        "process_crashed_or_restarted",
			"completed_at": completedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		s.emitLeftPanelRefresh("replication_orphaned_events_interrupted")
	}

	if s.TelemetryDB == nil {
		return nil
	}
	var interrupted []clusterModels.ReplicationEvent
	if err := s.DB.
		Where("event_type = ? AND status = ? AND error = ? AND source_node_id = ? AND started_at < ?",
			"replication", replicationEventStatusInterrupted, "process_crashed_or_restarted",
			strings.TrimSpace(localNodeID), startupCutoff.UTC()).
		Find(&interrupted).Error; err != nil {
		return err
	}
	eventsByPolicy := make(map[uint][]uint)
	for i := range interrupted {
		if interrupted[i].PolicyID != nil {
			eventsByPolicy[*interrupted[i].PolicyID] = append(eventsByPolicy[*interrupted[i].PolicyID], interrupted[i].ID)
		}
	}
	for policyID, eventIDs := range eventsByPolicy {
		db.FinalizeAsyncAuditRecordsBefore(
			s.TelemetryDB,
			"replication_policy_run",
			policyID,
			"failed",
			"process_crashed_or_restarted",
			map[string]any{
				"eventIds": eventIDs,
				"status":   replicationEventStatusInterrupted,
				"error":    "process_crashed_or_restarted",
			},
			startupCutoff,
		)
	}
	return nil
}

func runReplicationPeriodicLoop(ctx context.Context, interval time.Duration, operation func(context.Context)) {
	operation(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			operation(ctx)
		}
	}
}

func (s *Service) runReplicationLeaseRenewalLoop(ctx context.Context) {
	runReplicationPeriodicLoop(ctx, replicationLeaseRenewalInterval, func(ctx context.Context) {
		if err := s.runReplicationLeaseRenewalTick(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_lease_renewal_tick_failed")
		}
	})
}

func (s *Service) runReplicationSelfFenceLoop(ctx context.Context) {
	runReplicationPeriodicLoop(ctx, replicationSelfFenceInterval, func(ctx context.Context) {
		if err := s.selfFenceExpiredLeases(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_self_fence_check_failed")
		}
	})
}

func (s *Service) runReplicationSchedulingLoop(ctx context.Context) {
	runReplicationPeriodicLoop(ctx, 5*time.Second, func(ctx context.Context) {
		if err := s.recoverCrashedReplicationGuests(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_crash_recovery_failed")
		}
		if err := s.runReplicationSchedulerTick(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_scheduler_tick_failed")
		}
	})
}

func (s *Service) runReplicationLeaderControlLoop(ctx context.Context) {
	runReplicationPeriodicLoop(ctx, 5*time.Second, func(ctx context.Context) {
		if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
			return
		}
		if err := s.runTransitionRecoveryTick(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_transition_recovery_tick_failed")
		}
		if err := s.runFailoverControllerTick(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("replication_failover_tick_failed")
		}
	})
}

func (s *Service) runReplicationMaintenanceLoop(ctx context.Context) {
	lastSSHSync := time.Time{}
	runReplicationPeriodicLoop(ctx, 5*time.Second, func(ctx context.Context) {
		now := s.now().UTC()
		if s.Cluster != nil && now.Sub(lastSSHSync) > 30*time.Second {
			if err := s.Cluster.EnsureAndPublishLocalSSHIdentity(); err != nil {
				logger.L.Warn().Err(err).Msg("cluster_ssh_identity_sync_failed")
			}
			lastSSHSync = now
		}
	})
}

func transitionStateInProgress(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup,
		clusterModels.ReplicationTransitionStatePromoting,
		clusterModels.ReplicationTransitionStateRollingBack:
		return true
	default:
		return false
	}
}

func replicationPolicyAllowsRuns(policy *clusterModels.ReplicationPolicy) bool {
	if policy == nil || !policy.Enabled || transitionStateInProgress(policy.TransitionState) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(policy.ProtectionState)) {
	case clusterModels.ReplicationProtectionStateDeleting,
		clusterModels.ReplicationProtectionStateSuspended,
		clusterModels.ReplicationProtectionStateUnprotected:
		return false
	default:
		return true
	}
}

func replicationPolicyAcceptsNewTransition(policy *clusterModels.ReplicationPolicy) bool {
	if policy == nil || !policy.Enabled || transitionStateInProgress(policy.TransitionState) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(policy.ProtectionState)) {
	case clusterModels.ReplicationProtectionStateDeleting,
		clusterModels.ReplicationProtectionStateUnprotected:
		return false
	default:
		return true
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

func rotatedReplicationPolicyTargets(
	policy *clusterModels.ReplicationPolicy,
	previousOwner string,
	newOwner string,
	newOwnerEpoch uint64,
) []clusterModels.ReplicationPolicyTarget {
	previousOwner = strings.TrimSpace(previousOwner)
	newOwner = strings.TrimSpace(newOwner)
	if policy == nil {
		return nil
	}

	newOwnerWeight := 100
	weights := make(map[string]int, len(policy.Targets))
	for _, target := range policy.Targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" {
			continue
		}
		weight := target.Weight
		if weight == 0 {
			weight = 100
		}
		weights[nodeID] = weight
		if nodeID == newOwner {
			newOwnerWeight = weight
		}
	}

	nodeWeights := make(map[string]int, len(policy.Targets)+1)
	for _, target := range policy.Targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" || nodeID == newOwner {
			continue
		}
		nodeWeights[nodeID] = weights[nodeID]
	}
	if previousOwner != "" && previousOwner != newOwner {
		if weight, ok := weights[previousOwner]; ok {
			nodeWeights[previousOwner] = weight
		} else {
			nodeWeights[previousOwner] = newOwnerWeight
		}
	}

	rotated := make([]clusterModels.ReplicationPolicyTarget, 0, len(nodeWeights))
	for nodeID, weight := range nodeWeights {
		rotated = append(rotated, clusterModels.ReplicationPolicyTarget{
			PolicyID:   policy.ID,
			NodeID:     nodeID,
			Weight:     weight,
			OwnerEpoch: newOwnerEpoch,
			Ready:      false,
			LastError:  "awaiting_post_transition_validation",
		})
	}
	sort.SliceStable(rotated, func(i, j int) bool {
		if rotated[i].Weight == rotated[j].Weight {
			return rotated[i].NodeID < rotated[j].NodeID
		}
		return rotated[i].Weight > rotated[j].Weight
	})
	return rotated
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
		State:                policy.TransitionState,
		RunID:                policy.TransitionRunID,
		Reason:               policy.TransitionReason,
		SourceNodeID:         policy.TransitionSourceNodeID,
		TargetNodeID:         policy.TransitionTargetNodeID,
		OwnerEpoch:           policy.TransitionOwnerEpoch,
		RequestedAt:          policy.TransitionRequestedAt,
		DemotedAt:            policy.TransitionDemotedAt,
		CatchupAt:            policy.TransitionCatchupAt,
		PromotedAt:           policy.TransitionPromotedAt,
		CompletedAt:          policy.TransitionCompletedAt,
		Error:                policy.TransitionError,
		AllowUnsafe:          policy.TransitionAllowUnsafe,
		MovePinnedSource:     policy.TransitionMovePinnedSource,
		TriggerValidationRun: policy.TransitionTriggerValidationRun,
		OriginalRunning:      policy.TransitionOriginalRunning,
		OriginalSourceNodeID: strings.TrimSpace(policy.TransitionOriginalSourceNodeID),
		GenerationID:         strings.TrimSpace(policy.TransitionGenerationID),
		GenerationOwnerEpoch: policy.TransitionGenerationOwnerEpoch,
		GenerationManifest:   strings.TrimSpace(policy.TransitionGenerationManifest),
		GenerationRootCount:  policy.TransitionGenerationRootCount,
	}
}

func (s *Service) findReplicationTransitionEvent(
	policyID uint,
	transitionRunID string,
	requestedAt *time.Time,
	completedAt *time.Time,
	sourceNodeID string,
	targetNodeID string,
) (*clusterModels.ReplicationEvent, error) {
	if s == nil || s.DB == nil || policyID == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	transitionRunID = strings.TrimSpace(transitionRunID)
	if transitionRunID != "" {
		var event clusterModels.ReplicationEvent
		err := s.DB.
			Where("policy_id = ? AND event_type = ? AND transition_run_id = ? AND completed_at IS NULL",
				policyID, "failover", transitionRunID).
			Order("started_at DESC").
			First(&event).Error
		if err == nil {
			return &event, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	if requestedAt == nil || requestedAt.IsZero() {
		return nil, gorm.ErrRecordNotFound
	}

	sourceNodeID = strings.TrimSpace(sourceNodeID)
	targetNodeID = strings.TrimSpace(targetNodeID)
	legacyQuery := func() *gorm.DB {
		query := s.DB.
			Where("policy_id = ? AND event_type = ? AND COALESCE(transition_run_id, '') = '' AND completed_at IS NULL",
				policyID, "failover")
		if sourceNodeID != "" && targetNodeID != "" {
			query = query.Where(
				"((source_node_id = ? AND target_node_id = ?) OR (source_node_id = ? AND target_node_id = ?))",
				sourceNodeID, targetNodeID, targetNodeID, sourceNodeID,
			)
		}
		return query
	}

	var candidates []clusterModels.ReplicationEvent
	delayedQuery := legacyQuery().Where("started_at >= ?", requestedAt.UTC())
	if completedAt != nil && !completedAt.IsZero() {
		delayedQuery = delayedQuery.Where("started_at <= ?", completedAt.UTC())
	}
	err := delayedQuery.
		Order("started_at ASC").
		Limit(2).
		Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	if len(candidates) != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	return &candidates[0], nil
}

func replicationTransitionHasFailoverIntent(policy *clusterModels.ReplicationPolicy) bool {
	if policy == nil {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(policy.TransitionReason))
	runID := strings.ToLower(strings.TrimSpace(policy.TransitionRunID))
	return strings.Contains(reason, "failover") ||
		strings.Contains(reason, "failback") ||
		strings.HasPrefix(reason, "node_down_") ||
		strings.HasPrefix(runID, "failover-") ||
		strings.HasPrefix(runID, "auto-failback-")
}

func (s *Service) ensureReplicationTransitionEvent(
	policy *clusterModels.ReplicationPolicy,
	transitionRunID string,
	startedAt time.Time,
	sourceNodeID string,
	targetNodeID string,
	status string,
	message string,
) (*clusterModels.ReplicationEvent, error) {
	if policy == nil || policy.ID == 0 || s == nil || s.Cluster == nil {
		return nil, fmt.Errorf("replication_transition_event_context_unavailable")
	}

	transitionRunID = strings.TrimSpace(transitionRunID)
	startedAt = startedAt.UTC()
	if event, err := s.findReplicationTransitionEvent(
		policy.ID, transitionRunID, &startedAt, nil, sourceNodeID, targetNodeID,
	); err == nil {
		event.TransitionRunID = transitionRunID
		return event, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	event := clusterModels.ReplicationEvent{
		PolicyID:        &policy.ID,
		TransitionRunID: transitionRunID,
		EventType:       "failover",
		Status:          status,
		Message:         message,
		SourceNodeID:    strings.TrimSpace(sourceNodeID),
		TargetNodeID:    strings.TrimSpace(targetNodeID),
		GuestType:       policy.GuestType,
		GuestID:         policy.GuestID,
		StartedAt:       startedAt,
	}
	eventID, err := s.Cluster.CreateOrUpdateReplicationEvent(event, false)
	if err != nil {
		return nil, err
	}
	event.ID = eventID
	return &event, nil
}

func replicationTransitionEventStatus(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup:
		return replicationEventStatusDemoting
	case clusterModels.ReplicationTransitionStatePromoting,
		clusterModels.ReplicationTransitionStateRollingBack:
		return replicationEventStatusPromoting
	case clusterModels.ReplicationTransitionStateCompleted:
		return replicationEventStatusActive
	case clusterModels.ReplicationTransitionStateFailed:
		return replicationEventStatusFailed
	default:
		return ""
	}
}

func (s *Service) reconcileReplicationTransitionEvent(
	policy *clusterModels.ReplicationPolicy,
	recoveryErr error,
) error {
	if policy == nil || policy.ID == 0 || s == nil || s.Cluster == nil {
		return nil
	}

	state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
	status := replicationTransitionEventStatus(state)
	if status == "" {
		return nil
	}
	event, err := s.findReplicationTransitionEvent(
		policy.ID,
		policy.TransitionRunID,
		policy.TransitionRequestedAt,
		policy.TransitionCompletedAt,
		policy.TransitionSourceNodeID,
		policy.TransitionTargetNodeID,
	)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var existing int64
		if runID := strings.TrimSpace(policy.TransitionRunID); runID != "" {
			if countErr := s.DB.Model(&clusterModels.ReplicationEvent{}).
				Where("policy_id = ? AND event_type = ? AND transition_run_id = ?", policy.ID, "failover", runID).
				Count(&existing).Error; countErr != nil {
				return countErr
			}
		}
		if existing > 0 || !replicationTransitionHasFailoverIntent(policy) {
			return nil
		}
		if policy.TransitionRequestedAt != nil && policy.TransitionCompletedAt != nil {
			legacyQuery := s.DB.Model(&clusterModels.ReplicationEvent{}).
				Where("policy_id = ? AND event_type = ? AND COALESCE(transition_run_id, '') = ''",
					policy.ID, "failover").
				Where("started_at >= ? AND started_at <= ?",
					policy.TransitionRequestedAt.UTC(), policy.TransitionCompletedAt.UTC())
			sourceNodeID := strings.TrimSpace(policy.TransitionSourceNodeID)
			targetNodeID := strings.TrimSpace(policy.TransitionTargetNodeID)
			if sourceNodeID != "" && targetNodeID != "" {
				legacyQuery = legacyQuery.Where(
					"((source_node_id = ? AND target_node_id = ?) OR (source_node_id = ? AND target_node_id = ?))",
					sourceNodeID, targetNodeID, targetNodeID, sourceNodeID,
				)
			}
			var legacyCount int64
			if countErr := legacyQuery.Count(&legacyCount).Error; countErr != nil {
				return countErr
			}
			if legacyCount > 0 {
				return nil
			}
		}
		if policy.TransitionCompletedAt == nil || strings.TrimSpace(policy.TransitionRunID) == "" ||
			(state != clusterModels.ReplicationTransitionStateCompleted && state != clusterModels.ReplicationTransitionStateFailed) {
			return nil
		}
		completedAt := policy.TransitionCompletedAt.UTC()
		age := s.now().UTC().Sub(completedAt)
		if age < 0 || age > replicationTerminalEventRecreateWindow {
			return nil
		}
		sourceNodeID := strings.TrimSpace(policy.TransitionSourceNodeID)
		targetNodeID := strings.TrimSpace(policy.TransitionTargetNodeID)
		if strings.Contains(strings.ToLower(policy.TransitionReason), "rollback") {
			sourceNodeID, targetNodeID = targetNodeID, sourceNodeID
		}
		startedAt := completedAt
		if policy.TransitionRequestedAt != nil {
			startedAt = policy.TransitionRequestedAt.UTC()
		}
		message := strings.TrimSpace(policy.TransitionReason) + "_active"
		errMsg := ""
		if state == clusterModels.ReplicationTransitionStateFailed {
			message = strings.TrimSpace(policy.TransitionError)
			if message == "" {
				message = "transition_failed"
			}
			errMsg = message
		}
		missing := clusterModels.ReplicationEvent{
			PolicyID:        &policy.ID,
			TransitionRunID: strings.TrimSpace(policy.TransitionRunID),
			EventType:       "failover",
			Status:          status,
			Message:         message,
			Error:           errMsg,
			SourceNodeID:    sourceNodeID,
			TargetNodeID:    targetNodeID,
			GuestType:       policy.GuestType,
			GuestID:         policy.GuestID,
			StartedAt:       startedAt,
			CompletedAt:     &completedAt,
		}
		eventID, createErr := s.Cluster.CreateOrUpdateReplicationEvent(missing, false)
		if createErr != nil {
			return createErr
		}
		if s.TelemetryDB != nil {
			auditStatus := "success"
			if state == clusterModels.ReplicationTransitionStateFailed {
				auditStatus = "failed"
			}
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_failover", policy.ID, auditStatus, errMsg, map[string]any{
				"eventId": eventID,
				"status":  auditStatus,
				"error":   errMsg,
			})
		}
		s.emitLeftPanelRefresh(fmt.Sprintf("replication_transition_event_recreated_%d", eventID))
		return nil
	}
	if err != nil {
		return err
	}

	event.TransitionRunID = strings.TrimSpace(policy.TransitionRunID)
	event.Status = status

	reason := strings.TrimSpace(policy.TransitionReason)
	switch state {
	case clusterModels.ReplicationTransitionStateCompleted:
		event.Message = reason + "_active"
		event.Error = ""
		completedAt := s.now().UTC()
		if policy.TransitionCompletedAt != nil {
			completedAt = policy.TransitionCompletedAt.UTC()
		}
		event.CompletedAt = &completedAt
	case clusterModels.ReplicationTransitionStateFailed:
		result := strings.TrimSpace(policy.TransitionError)
		if result == "" {
			result = "transition_failed"
		}
		event.Message = result
		if strings.TrimSpace(event.Error) == "" {
			event.Error = result
		} else if !strings.Contains(event.Error, result) {
			event.Error += "; transition_result: " + result
		}
		completedAt := s.now().UTC()
		if policy.TransitionCompletedAt != nil {
			completedAt = policy.TransitionCompletedAt.UTC()
		}
		event.CompletedAt = &completedAt
	default:
		if recoveryErr == nil {
			return nil
		}
		event.Message = reason + "_recovery_pending"
		event.Error = recoveryErr.Error()
	}

	if _, err := s.Cluster.CreateOrUpdateReplicationEvent(*event, false); err != nil {
		return err
	}
	if event.CompletedAt != nil && s.TelemetryDB != nil {
		auditStatus := "success"
		errMsg := ""
		if state == clusterModels.ReplicationTransitionStateFailed {
			auditStatus = "failed"
			errMsg = event.Error
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_failover", policy.ID, auditStatus, errMsg, map[string]any{
			"eventId": event.ID,
			"status":  auditStatus,
			"error":   errMsg,
		})
	}
	s.emitLeftPanelRefresh(fmt.Sprintf("replication_transition_event_reconciled_%d", event.ID))
	return nil
}

func (s *Service) failPolicyTransition(policy *clusterModels.ReplicationPolicy, transitionErr error) error {
	if policy == nil || policy.ID == 0 || s.Cluster == nil {
		return transitionErr
	}

	transition := transitionPayloadFromPolicy(policy)
	transition.State = clusterModels.ReplicationTransitionStateFailed
	now := s.now().UTC()
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
	restoreState := replicationProtectionStateFromTargets(policy, now)
	if restoreState == clusterModels.ReplicationProtectionStateUnprotected {
		restoreState = clusterModels.ReplicationProtectionStateDegraded
	}
	if err := s.Cluster.UpdateReplicationPolicyProtectionState(
		policy.ID,
		replicationPolicyOwnerEpoch(policy),
		restoreState,
		false,
	); err != nil {
		if transitionErr != nil {
			return fmt.Errorf("%v; transition_protection_state_restore_failed: %v", transitionErr, err)
		}
		return fmt.Errorf("transition_protection_state_restore_failed: %w", err)
	}
	policy.ProtectionState = restoreState
	return transitionErr
}

func (s *Service) rollbackRecoveringPromotion(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	cause error,
) error {
	if policy == nil || policy.ID == 0 || s.Cluster == nil {
		return cause
	}
	currentOwner := replicationPolicyOwnerNode(policy)
	previousOwner := strings.TrimSpace(policy.TransitionSourceNodeID)
	currentEpoch := replicationPolicyOwnerEpoch(policy)
	if currentOwner == "" || previousOwner == "" || currentOwner == previousOwner || currentEpoch == 0 {
		return appendReplicationStepError(cause, "replication_rollback_identity_invalid", nil)
	}
	if currentEpoch == math.MaxUint64 {
		return appendReplicationStepError(cause, "replication_owner_epoch_exhausted", nil)
	}
	previousOnline, err := s.isClusterNodeOnline(previousOwner)
	if err != nil {
		return appendReplicationStepError(cause, "replication_previous_owner_health_unknown", err)
	}
	if !previousOnline {
		return appendReplicationStepError(cause, "replication_previous_owner_offline", nil)
	}

	currentOnline, err := s.isClusterNodeOnline(currentOwner)
	if err != nil {
		return appendReplicationStepError(cause, "replication_current_owner_health_unknown", err)
	}
	var previousLeaseExpiresAtOrBefore *time.Time
	if currentOnline {
		var demoteErr error
		if currentOwner == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			demoteErr = s.DemoteReplicationPolicyForTransition(
				ctx,
				policy.ID,
				currentEpoch,
				policy.TransitionRunID,
			)
		} else {
			demoteErr = s.forwardDemoteReplicationPolicy(
				currentOwner,
				policy.ID,
				currentEpoch,
				policy.TransitionRunID,
			)
		}
		if demoteErr != nil {
			return appendReplicationStepError(cause, "replication_current_owner_fence_unconfirmed", demoteErr)
		}
	} else {
		// The current owner cannot acknowledge demotion. Stop renewing it,
		// wait through lease expiry plus the fencing/skew margin, and carry the
		// same cutoff into the atomic rollback transaction.
		if err := s.waitForPreviousOwnerLeaseExpiry(ctx, policy, currentOwner, currentEpoch); err != nil {
			return appendReplicationStepError(cause, "replication_current_owner_lease_barrier_failed", err)
		}
		cutoff := s.now().UTC().Add(-replicationLeaseExpirySafetyMargin)
		previousLeaseExpiresAtOrBefore = &cutoff
	}

	rollbackEpoch := currentEpoch + 1
	rollbackSourceNodeID := strings.TrimSpace(policy.SourceNodeID)
	switch strings.TrimSpace(policy.SourceMode) {
	case clusterModels.ReplicationSourceModeFollowActive:
		rollbackSourceNodeID = previousOwner
	case clusterModels.ReplicationSourceModePinned:
		if policy.TransitionMovePinnedSource {
			rollbackSourceNodeID = strings.TrimSpace(policy.TransitionOriginalSourceNodeID)
			if rollbackSourceNodeID == "" {
				// Compatibility for transitions begun before the original pinned
				// source became a persisted checkpoint.
				rollbackSourceNodeID = previousOwner
			}
		}
	}
	var rollbackSourceUpdate *string
	if rollbackSourceNodeID != strings.TrimSpace(policy.SourceNodeID) {
		rollbackSourceUpdate = &rollbackSourceNodeID
	}
	rollbackTargets := rotatedReplicationPolicyTargets(policy, currentOwner, previousOwner, rollbackEpoch)
	rollbackNow := s.now().UTC()
	leaseVersion, err := s.nextReplicationPolicyLeaseVersion(policy.ID, rollbackNow)
	if err != nil {
		return appendReplicationStepError(cause, "replication_rollback_lease_version_failed", err)
	}
	transition := transitionPayloadFromPolicy(policy)
	transition.State = clusterModels.ReplicationTransitionStateRollingBack
	transition.Reason = strings.TrimSpace(transition.Reason) + "_recovery_rollback"
	transition.SourceNodeID = currentOwner
	transition.TargetNodeID = previousOwner
	transition.OwnerEpoch = rollbackEpoch
	transition.CompletedAt = nil
	transition.Error = "rollback_in_progress"
	payload := clusterModels.ReplicationOwnershipTransitionPayload{
		PolicyID:                       policy.ID,
		ExpectedActiveNodeID:           currentOwner,
		ExpectedOwnerEpoch:             currentEpoch,
		ExpectedTransitionRunID:        policy.TransitionRunID,
		PreviousLeaseExpiresAtOrBefore: previousLeaseExpiresAtOrBefore,
		ActiveNodeID:                   previousOwner,
		SourceNodeID:                   rollbackSourceUpdate,
		OwnerEpoch:                     rollbackEpoch,
		ReplaceTargets:                 true,
		Targets:                        rollbackTargets,
		Lease: clusterModels.ReplicationLease{
			PolicyID:    policy.ID,
			GuestType:   policy.GuestType,
			GuestID:     policy.GuestID,
			OwnerNodeID: previousOwner,
			OwnerEpoch:  rollbackEpoch,
			ExpiresAt:   rollbackNow.Add(replicationLeaseTTL),
			Version:     leaseVersion,
			LastReason:  "promotion_recovery_rollback",
			LastActor:   s.Cluster.LocalNodeID(),
		},
		Transition:      transition,
		ProtectionState: clusterModels.ReplicationProtectionStateDegraded,
	}
	commitErr := s.Cluster.CommitReplicationOwnershipTransition(payload, false)
	if commitErr != nil {
		latest, policyErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
		latestLease, leaseErr := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
		if policyErr != nil || leaseErr != nil || latest == nil || latestLease == nil ||
			replicationPolicyOwnerNode(latest) != previousOwner ||
			replicationPolicyOwnerEpoch(latest) != rollbackEpoch ||
			strings.TrimSpace(latest.TransitionRunID) != strings.TrimSpace(policy.TransitionRunID) ||
			!strings.EqualFold(strings.TrimSpace(latest.TransitionState), clusterModels.ReplicationTransitionStateRollingBack) ||
			strings.TrimSpace(latestLease.OwnerNodeID) != previousOwner || latestLease.OwnerEpoch != rollbackEpoch {
			return fmt.Errorf("replication_rollback_commit_outcome_ambiguous: %w", commitErr)
		}
		policy = latest
	} else {
		policy.SourceNodeID = rollbackSourceNodeID
		policy.ActiveNodeID = previousOwner
		policy.OwnerEpoch = rollbackEpoch
		policy.Targets = rollbackTargets
		policy.ProtectionState = clusterModels.ReplicationProtectionStateDegraded
		policy.TransitionState = transition.State
		policy.TransitionReason = transition.Reason
		policy.TransitionSourceNodeID = transition.SourceNodeID
		policy.TransitionTargetNodeID = transition.TargetNodeID
		policy.TransitionOwnerEpoch = transition.OwnerEpoch
		policy.TransitionError = transition.Error
	}
	if err := s.resumeRollingBackTransition(ctx, policy); err != nil {
		return appendReplicationStepError(cause, "replication_rollback_pending_recovery", err)
	}
	return fmt.Errorf("%v; rollback_succeeded", cause)
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
		return targetOnlineErr
	}
	if !targetOnline {
		return s.rollbackRecoveringPromotion(ctx, policy, fmt.Errorf("replication_target_node_offline"))
	}

	var activateErr error
	desiredRunning := policy.TransitionOriginalRunning
	if desiredRunning == nil {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_transition_original_running_missing"))
	}
	if targetNodeID == strings.TrimSpace(s.Cluster.LocalNodeID()) {
		activateErr = s.ActivateReplicationPolicyForTransition(
			ctx,
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			policy.TransitionRunID,
			desiredRunning,
		)
	} else {
		activateErr = s.forwardActivateReplicationPolicy(
			targetNodeID,
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			policy.TransitionRunID,
			desiredRunning,
		)
	}
	if activateErr != nil {
		// A timeout is ambiguous and activation is idempotent under the exact
		// owner epoch/run ID. Retry for a bounded period, then explicitly fence
		// the target and enter the same durable rollback state used by the
		// uninterrupted transition path.
		promotionStartedAt := policy.UpdatedAt.UTC()
		if promotionStartedAt.IsZero() {
			promotionStartedAt = s.now().UTC()
		}
		if !s.now().UTC().Before(promotionStartedAt.Add(replicationPromotionRollbackAfter)) {
			return s.rollbackRecoveringPromotion(ctx, policy, activateErr)
		}
		return activateErr
	}
	if err := s.rebindReplicationGuestBackupJobRunners(policy, targetNodeID); err != nil {
		// Ownership and workload activation are already complete. Backup-runner
		// placement is retriable control-plane hygiene and must not keep the
		// ownership transition locked indefinitely.
		logger.L.Warn().Err(err).
			Uint("policy_id", policy.ID).
			Str("target_node_id", targetNodeID).
			Msg("replication_backup_job_runner_rebind_pending")
	}

	transition := transitionPayloadFromPolicy(policy)
	now := s.now().UTC()
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
	if transition.TriggerValidationRun {
		if err := s.enqueueReplicationValidationRun(ctx, policy.ID, targetNodeID); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("policy_id", policy.ID).
				Str("target_node_id", targetNodeID).
				Msg("replication_post_transition_validation_enqueue_failed")
		}
	}
	return nil
}

func (s *Service) resumeRollingBackTransition(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy_transition_input")
	}
	rollbackOwner := strings.TrimSpace(policy.TransitionTargetNodeID)
	if rollbackOwner == "" || replicationPolicyOwnerNode(policy) != rollbackOwner {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_rollback_owner_target_mismatch"))
	}
	if policy.TransitionOriginalRunning == nil {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_transition_original_running_missing"))
	}
	online, err := s.isClusterNodeOnline(rollbackOwner)
	if err != nil {
		return err
	}
	if !online {
		return fmt.Errorf("replication_rollback_owner_offline")
	}
	if rollbackOwner == strings.TrimSpace(s.Cluster.LocalNodeID()) {
		err = s.ActivateReplicationPolicyForTransition(
			ctx,
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			policy.TransitionRunID,
			policy.TransitionOriginalRunning,
		)
	} else {
		err = s.forwardActivateReplicationPolicy(
			rollbackOwner,
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			policy.TransitionRunID,
			policy.TransitionOriginalRunning,
		)
	}
	if err != nil {
		return err
	}
	if err := s.rebindReplicationGuestBackupJobRunners(policy, rollbackOwner); err != nil {
		logger.L.Warn().Err(err).
			Uint("policy_id", policy.ID).
			Str("target_node_id", rollbackOwner).
			Msg("replication_rollback_backup_job_runner_rebind_pending")
	}

	transition := transitionPayloadFromPolicy(policy)
	now := s.now().UTC()
	transition.State = clusterModels.ReplicationTransitionStateFailed
	transition.CompletedAt = &now
	transition.Error = "failover_failed_rollback_succeeded"
	if err := s.Cluster.UpdateReplicationPolicyTransition(policy.ID, transition); err != nil {
		return err
	}
	if err := s.Cluster.UpdateReplicationPolicyProtectionState(
		policy.ID,
		replicationPolicyOwnerEpoch(policy),
		clusterModels.ReplicationProtectionStateDegraded,
		false,
	); err != nil {
		return err
	}
	policy.TransitionState = clusterModels.ReplicationTransitionStateFailed
	policy.TransitionCompletedAt = &now
	policy.TransitionError = transition.Error
	policy.ProtectionState = clusterModels.ReplicationProtectionStateDegraded
	if transition.TriggerValidationRun {
		if err := s.enqueueReplicationValidationRun(ctx, policy.ID, rollbackOwner); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("replication_rollback_validation_enqueue_failed")
		}
	}
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
	case clusterModels.ReplicationTransitionStateRollingBack:
		return s.resumeRollingBackTransition(ctx, policy)
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
			!policy.TransitionAllowUnsafe,
			replicationTransitionOptions{
				RunID:                strings.TrimSpace(policy.TransitionRunID),
				AllowUnsafe:          policy.TransitionAllowUnsafe,
				MovePinnedSource:     policy.TransitionMovePinnedSource,
				TriggerValidationRun: policy.TransitionTriggerValidationRun,
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
		state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
		if !transitionStateInProgress(state) {
			if state == clusterModels.ReplicationTransitionStateCompleted ||
				state == clusterModels.ReplicationTransitionStateFailed {
				if err := s.reconcileReplicationTransitionEvent(&policy, nil); err != nil {
					logger.L.Warn().Err(err).
						Uint("policy_id", policy.ID).
						Msg("replication_terminal_transition_event_reconcile_failed")
				}
			}
			continue
		}
		if !s.acquirePolicyTransition(policy.ID) {
			continue
		}

		startedAt := s.now().UTC()
		if policy.TransitionRequestedAt != nil {
			startedAt = policy.TransitionRequestedAt.UTC()
		}
		sourceNodeID := policy.TransitionSourceNodeID
		targetNodeID := policy.TransitionTargetNodeID
		if state == clusterModels.ReplicationTransitionStateRollingBack {
			sourceNodeID, targetNodeID = targetNodeID, sourceNodeID
		}
		if _, ensureErr := s.ensureReplicationTransitionEvent(
			&policy,
			policy.TransitionRunID,
			startedAt,
			sourceNodeID,
			targetNodeID,
			replicationTransitionEventStatus(state),
			strings.TrimSpace(policy.TransitionReason)+"_recovery_pending",
		); ensureErr != nil {
			logger.L.Warn().Err(ensureErr).
				Uint("policy_id", policy.ID).
				Msg("replication_transition_recovery_event_ensure_failed")
		}

		resumeErr := s.resumePolicyTransition(ctx, &policy)
		s.releasePolicyTransition(policy.ID)
		if latest, latestErr := s.Cluster.GetReplicationPolicyByID(policy.ID); latestErr == nil && latest != nil {
			policy = *latest
		} else if latestErr != nil {
			logger.L.Warn().Err(latestErr).
				Uint("policy_id", policy.ID).
				Msg("replication_transition_recovery_policy_reload_failed")
		}
		if reconcileErr := s.reconcileReplicationTransitionEvent(&policy, resumeErr); reconcileErr != nil {
			logger.L.Warn().Err(reconcileErr).
				Uint("policy_id", policy.ID).
				Msg("replication_transition_recovery_event_reconcile_failed")
		}
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

func (s *Service) runReplicationSchedulerTick(ctx context.Context) error {
	if s.DB == nil || s.Cluster == nil {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Preload("Targets").Where("enabled = ? AND COALESCE(cron_expr, '') != ''", true).Find(&policies).Error; err != nil {
		return err
	}

	now := s.now().UTC()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	for i := range policies {
		policy := policies[i]
		if !replicationPolicyAllowsRuns(&policy) {
			continue
		}
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

func nextReplicationLeaseVersion(now time.Time, persisted uint64) (uint64, error) {
	candidate := uint64(1)
	if unixNanos := now.UTC().UnixNano(); unixNanos > 0 {
		candidate = uint64(unixNanos)
	}
	if candidate > persisted {
		return candidate, nil
	}
	if persisted == math.MaxUint64 {
		return 0, fmt.Errorf("replication_lease_version_exhausted")
	}
	return persisted + 1, nil
}

func (s *Service) nextReplicationPolicyLeaseVersion(policyID uint, now time.Time) (uint64, error) {
	persisted := uint64(0)
	lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policyID)
	if err == nil && lease != nil {
		persisted = lease.Version
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	return nextReplicationLeaseVersion(now, persisted)
}

func (s *Service) renewReplicationTransitionLease(
	policy *clusterModels.ReplicationPolicy,
	ownerNodeID string,
	ownerEpoch uint64,
	reason string,
) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	now := s.now().UTC()
	version, err := s.nextReplicationPolicyLeaseVersion(policy.ID, now)
	if err != nil {
		return err
	}
	return s.Cluster.UpsertReplicationLease(clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: strings.TrimSpace(ownerNodeID),
		OwnerEpoch:  ownerEpoch,
		ExpiresAt:   now.Add(3 * replicationLeaseTTL),
		Version:     version,
		LastReason:  strings.TrimSpace(reason) + "_transition_hold",
		LastActor:   s.Cluster.LocalNodeID(),
	}, false)
}

func (s *Service) withReplicationTransitionLeaseKeeper(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	ownerNodeID string,
	ownerEpoch uint64,
	reason string,
	operation func(context.Context) error,
) error {
	if operation == nil {
		return fmt.Errorf("replication_transition_operation_required")
	}
	if err := s.renewReplicationTransitionLease(policy, ownerNodeID, ownerEpoch, reason); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancelOperation := context.WithCancel(ctx)
	defer cancelOperation()
	done := make(chan struct{})
	renewErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(replicationLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-operationCtx.Done():
				return
			case <-ticker.C:
				if err := s.renewReplicationTransitionLease(policy, ownerNodeID, ownerEpoch, reason); err != nil {
					select {
					case renewErr <- err:
					default:
					}
					cancelOperation()
					return
				}
			}
		}
	}()
	operationErr := operation(operationCtx)
	close(done)
	select {
	case err := <-renewErr:
		if operationErr != nil {
			return fmt.Errorf("%v; replication_transition_lease_renewal_failed: %w", operationErr, err)
		}
		return fmt.Errorf("replication_transition_lease_renewal_failed: %w", err)
	default:
		return operationErr
	}
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
	if s.now().UTC().After(lease.ExpiresAt) {
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
	return s.fenceReplicationDatasetRoots(ctx, policy.ID, datasets, reason)
}

func (s *Service) fenceReplicationDatasetRoots(
	ctx context.Context,
	policyID uint,
	datasets []string,
	reason string,
) error {
	var fenceErr error
	for _, dataset := range datasets {
		subtreeOutput, listErr := utils.RunCommandWithContext(
			ctx, "zfs", "list", "-H", "-o", "name", "-r", "-t", "filesystem,volume", dataset,
		)
		if listErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, listErr)
			continue
		}
		subtree := strings.Fields(subtreeOutput)
		if len(subtree) == 0 {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, fmt.Errorf("recursive_dataset_list_empty"))
			continue
		}
		// Children can carry local readonly overrides. Set every filesystem and
		// zvol explicitly; the target host's ZFS CLI does not support `set -r`.
		sort.SliceStable(subtree, func(i, j int) bool {
			return strings.Count(subtree[i], "/") > strings.Count(subtree[j], "/")
		})
		for _, child := range subtree {
			if _, setErr := utils.RunCommandWithContext(ctx, "zfs", "set", "readonly=on", child); setErr != nil {
				fenceErr = appendReplicationFenceDatasetError(fenceErr, child, setErr)
			}
		}
		readonlyOutput, verifyErr := utils.RunCommandWithContext(
			ctx, "zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", dataset,
		)
		if verifyErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, verifyErr)
			continue
		}
		verified := true
		for _, value := range strings.Fields(readonlyOutput) {
			if value != "on" {
				verified = false
				break
			}
		}
		if !verified {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, fmt.Errorf("recursive_readonly_verification_failed"))
			continue
		}

		logger.L.Info().
			Uint("policy_id", policyID).
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

// replicationRunSummaryTargetNodeID preserves the existing single-target event
// path while ensuring a fan-out run is not attributed to whichever target ran
// last. Per-target details for fan-out runs live in the labelled event output
// and target readiness records.
func replicationRunSummaryTargetNodeID(targets []clusterModels.ReplicationPolicyTarget, localNodeID string) string {
	localNodeID = strings.TrimSpace(localNodeID)
	targetNodeID := ""
	for _, target := range targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" || nodeID == localNodeID {
			continue
		}
		if targetNodeID == "" {
			targetNodeID = nodeID
			continue
		}
		if targetNodeID != nodeID {
			return ""
		}
	}
	return targetNodeID
}

func replicationRunOutcomeError(
	eligibleTargets int,
	attemptedTransfers int,
	succeededTargets int,
	failedTargets []string,
	skippedOffline int,
	skippedNoIdentity int,
) error {
	if succeededTargets == 0 && len(failedTargets) > 0 {
		return fmt.Errorf(
			"replication_all_targets_failed:%d_targets (offline=%d missing_identity=%d)",
			len(failedTargets),
			skippedOffline,
			skippedNoIdentity,
		)
	}
	if succeededTargets > 0 && len(failedTargets) > 0 {
		return fmt.Errorf(
			"replication_degraded:%d_succeeded_%d_failed (offline=%d missing_identity=%d)",
			succeededTargets,
			len(failedTargets),
			skippedOffline,
			skippedNoIdentity,
		)
	}
	if eligibleTargets == 0 {
		return fmt.Errorf(
			"no_eligible_replication_targets (offline=%d missing_identity=%d)",
			skippedOffline,
			skippedNoIdentity,
		)
	}
	if attemptedTransfers == 0 {
		return fmt.Errorf("no_replication_transfers_executed")
	}
	return nil
}

func joinReplicationRunCriticalErrors(runErr error, criticalErrors []error) error {
	if len(criticalErrors) == 0 {
		return runErr
	}
	joined := make([]error, 0, len(criticalErrors)+1)
	if runErr != nil {
		joined = append(joined, runErr)
	}
	joined = append(joined, criticalErrors...)
	return errors.Join(joined...)
}

type replicationTargetGenerationTransfer func() (replicationGenerationTransferResult, error)

func (s *Service) invalidateReplicationTargetAfterAttemptFailure(
	policy *clusterModels.ReplicationPolicy,
	target clusterModels.ReplicationPolicyTarget,
	result replicationGenerationTransferResult,
	eventID uint,
	attemptErr error,
) error {
	if attemptErr == nil {
		return nil
	}

	generationID := target.GenerationID
	manifestHash := target.ManifestHash
	requiredDatasetCount := target.RequiredDatasetCount
	completedDatasetCount := target.CompletedDatasetCount
	if strings.TrimSpace(result.GenerationID) != "" {
		generationID = result.GenerationID
		manifestHash = result.ManifestHash
		requiredDatasetCount = result.RequiredDatasetCount
		completedDatasetCount = result.CompletedDatasetCount
	}

	fallbackErr := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID:              policy.ID,
		NodeID:                strings.TrimSpace(target.NodeID),
		ExpectedOwnerEpoch:    replicationPolicyOwnerEpoch(policy),
		EvaluatedAt:           s.now().UTC(),
		Ready:                 false,
		GenerationID:          generationID,
		ManifestHash:          manifestHash,
		RequiredDatasetCount:  requiredDatasetCount,
		CompletedDatasetCount: completedDatasetCount,
		LastError:             attemptErr.Error(),
	})
	if fallbackErr == nil {
		return attemptErr
	}

	fallbackErr = errors.Join(
		errReplicationTargetFallbackReadinessInvalidation,
		fmt.Errorf("publish_replication_target_readiness_failed: %w", fallbackErr),
	)
	s.appendReplicationTargetEventOutputBestEffort(
		eventID,
		target.NodeID,
		fallbackErr.Error(),
	)
	logger.L.Error().
		Err(fallbackErr).
		Uint("policy_id", policy.ID).
		Str("target_node_id", strings.TrimSpace(target.NodeID)).
		Msg("replication_target_fallback_readiness_invalidation_failed")
	return errors.Join(attemptErr, fallbackErr)
}

// runReplicationTargetGenerationAttempt is the fail-closed boundary around a
// target transfer. A previously ready target is invalidated before transfer
// setup starts, so authority, snapshot, manifest, or SSH failures cannot leave
// stale readiness selectable for failover.
func (s *Service) runReplicationTargetGenerationAttempt(
	policy *clusterModels.ReplicationPolicy,
	target clusterModels.ReplicationPolicyTarget,
	eventID uint,
	transfer replicationTargetGenerationTransfer,
) (result replicationGenerationTransferResult, attemptErr error) {
	if policy == nil || policy.ID == 0 {
		return result, fmt.Errorf("invalid_policy")
	}
	targetNodeID := strings.TrimSpace(target.NodeID)
	if targetNodeID == "" {
		return result, fmt.Errorf("replication_target_node_required")
	}
	if transfer == nil {
		return result, fmt.Errorf("replication_target_transfer_required")
	}

	preTransferErr := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID:              policy.ID,
		NodeID:                targetNodeID,
		ExpectedOwnerEpoch:    replicationPolicyOwnerEpoch(policy),
		EvaluatedAt:           s.now().UTC(),
		Ready:                 false,
		GenerationID:          target.GenerationID,
		ManifestHash:          target.ManifestHash,
		RequiredDatasetCount:  target.RequiredDatasetCount,
		CompletedDatasetCount: target.CompletedDatasetCount,
		LastError:             "replication_generation_attempt_in_progress",
	})
	if preTransferErr != nil {
		attemptErr = fmt.Errorf("replication_target_pre_transfer_readiness_invalidation_failed: %w", preTransferErr)
		s.appendReplicationTargetEventOutputBestEffort(eventID, targetNodeID, attemptErr.Error())
		logger.L.Error().
			Err(preTransferErr).
			Uint("policy_id", policy.ID).
			Str("target_node_id", targetNodeID).
			Msg("replication_target_pre_transfer_readiness_invalidation_failed")
		return result, s.invalidateReplicationTargetAfterAttemptFailure(
			policy,
			target,
			result,
			eventID,
			attemptErr,
		)
	}

	result, attemptErr = transfer()
	if attemptErr == nil {
		verifiedAt := s.now().UTC()
		readyUntil := replicationTargetReadyUntil(policy, verifiedAt)
		if readinessErr := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
			PolicyID:              policy.ID,
			NodeID:                targetNodeID,
			ExpectedOwnerEpoch:    replicationPolicyOwnerEpoch(policy),
			EvaluatedAt:           verifiedAt,
			Ready:                 true,
			GenerationID:          result.GenerationID,
			ManifestHash:          result.ManifestHash,
			RequiredDatasetCount:  result.RequiredDatasetCount,
			CompletedDatasetCount: result.CompletedDatasetCount,
			LastVerifiedAt:        &verifiedAt,
			ReadyUntil:            &readyUntil,
		}); readinessErr != nil {
			attemptErr = fmt.Errorf("publish_replication_target_readiness_failed: %w", readinessErr)
		}
	}
	if attemptErr != nil {
		attemptErr = s.invalidateReplicationTargetAfterAttemptFailure(
			policy,
			target,
			result,
			eventID,
			attemptErr,
		)
	}
	return result, attemptErr
}

func (s *Service) runReplicationPolicy(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	if !replicationPolicyAllowsRuns(policy) {
		return fmt.Errorf("replication_policy_not_runnable")
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

	// vm.json is part of every VM generation. Refresh it from the
	// authoritative DB while holding the same guest-wide fence as snapshot
	// discovery, so a stale metadata file can never be snapshotted and re-arm
	// target readiness. Fail closed before source discovery or any snapshot.
	if runErr := s.refreshReplicationSourceMetadataForRun(policy); runErr != nil {
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	sourceDatasets, err := s.replicationSourceDatasets(ctx, policy)
	if err != nil {
		if errors.Is(err, errReplicationVMFilesystemStorageUnsupported) {
			if invalidateErr := s.invalidateReplicationPolicyTargetReadiness(policy, err); invalidateErr != nil {
				err = errors.Join(err, fmt.Errorf("invalidate_replication_target_readiness_failed: %w", invalidateErr))
			}
		}
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
		TargetNodeID: replicationRunSummaryTargetNodeID(policy.Targets, localNodeID),
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
		if finalizeErr := s.finalizeReplicationEvent(&event, runErr); finalizeErr != nil {
			runErr = errors.Join(runErr, finalizeErr)
		}
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
	criticalTargetErrors := make([]error, 0)
	succeededTargets := 0
	markTargetUnavailable := func(target clusterModels.ReplicationPolicyTarget, reason string) {
		targetNodeID := strings.TrimSpace(target.NodeID)
		reason = strings.TrimSpace(reason)
		failedTargets = append(failedTargets, targetNodeID)
		s.appendReplicationTargetEventOutputBestEffort(event.ID, targetNodeID, reason)

		if readinessErr := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
			PolicyID:              policy.ID,
			NodeID:                targetNodeID,
			ExpectedOwnerEpoch:    replicationPolicyOwnerEpoch(policy),
			EvaluatedAt:           s.now().UTC(),
			Ready:                 false,
			GenerationID:          target.GenerationID,
			ManifestHash:          target.ManifestHash,
			RequiredDatasetCount:  target.RequiredDatasetCount,
			CompletedDatasetCount: target.CompletedDatasetCount,
			LastError:             reason,
		}); readinessErr != nil {
			criticalTargetErrors = append(
				criticalTargetErrors,
				fmt.Errorf("replication_target_%s_readiness_invalidation_failed: %w", targetNodeID, readinessErr),
			)
			s.appendReplicationTargetEventOutputBestEffort(
				event.ID,
				targetNodeID,
				fmt.Sprintf("replication_target_readiness_invalidation_failed: %v", readinessErr),
			)
			logger.L.Warn().
				Err(readinessErr).
				Uint("policy_id", policy.ID).
				Str("target_node_id", targetNodeID).
				Msg("replication_target_readiness_invalidation_failed")
		}

		logger.L.Warn().
			Uint("policy_id", policy.ID).
			Str("target_node_id", targetNodeID).
			Str("reason", reason).
			Msg("replication_target_unavailable")
	}

	// Re-verify the lease immediately before starting transfers.
	// The lease may have expired or been transferred since the
	// initial check at the top of runReplicationPolicy.
	if ownershipErr := s.validateLocalReplicationPolicyLease(policy); ownershipErr != nil {
		runErr = fmt.Errorf("pre_transfer_lease_check_failed: %w", ownershipErr)
		if finalizeErr := s.finalizeReplicationEvent(&event, runErr); finalizeErr != nil {
			runErr = errors.Join(runErr, finalizeErr)
		}
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
			markTargetUnavailable(target, fmt.Sprintf("replication_target_offline: status=%s", status))
			continue
		}

		_, ok := identityByNode[targetNodeID]
		if !ok {
			skippedNoIdentity++
			markTargetUnavailable(target, "replication_target_ssh_identity_missing")
			continue
		}
		eligibleTargets++

		attemptedTransfers += len(sourceDatasets)
		s.appendReplicationTargetEventOutputBestEffort(event.ID, targetNodeID, "replication_target_attempt_started")
		logger.L.Info().
			Uint("policy_id", policy.ID).
			Str("target_node_id", targetNodeID).
			Int("dataset_count", len(sourceDatasets)).
			Msg("replication_target_attempt_started")

		generationID := fmt.Sprintf("replication-%d-%s-%d", policy.ID, compactNowToken(), eligibleTargets)
		generationResult, attemptErr := s.runReplicationTargetGenerationAttempt(
			policy,
			target,
			event.ID,
			func() (replicationGenerationTransferResult, error) {
				return s.replicatePolicyGenerationToTarget(
					ctx,
					policy,
					targetNodeID,
					replicationPolicyOwnerEpoch(policy),
					"",
					generationID,
					event.ID,
				)
			},
		)
		if attemptErr == nil {
			for _, sourceDataset := range sourceDatasets {
				targetSpec, destSuffix, specErr := s.replicationTargetSpec(targetNodeID, sourceDataset, identityByNode, privateKeyPath)
				if specErr != nil {
					logger.L.Warn().Err(specErr).Str("source_dataset", sourceDataset).Msg("replication_retention_target_spec_failed")
					continue
				}
				if retentionErr := s.applyReplicationRetention(ctx, targetSpec, sourceDataset, destSuffix, event.ID, targetNodeID); retentionErr != nil {
					logger.L.Warn().Err(retentionErr).Str("source_dataset", sourceDataset).Msg("replication_retention_post_run_failed")
				}
			}
		}

		if attemptErr != nil {
			// Track this target as failed but continue to remaining
			// targets. One bad target shouldn't block healthy ones.
			s.appendReplicationTargetEventOutputBestEffort(
				event.ID,
				targetNodeID,
				fmt.Sprintf("replication_target_attempt_failed: %v", attemptErr),
			)
			logger.L.Warn().
				Err(attemptErr).
				Uint("policy_id", policy.ID).
				Str("target_node_id", targetNodeID).
				Msg("replication_target_failed_continuing")
			failedTargets = append(failedTargets, targetNodeID)
			if errors.Is(attemptErr, errReplicationTargetFallbackReadinessInvalidation) {
				criticalTargetErrors = append(
					criticalTargetErrors,
					fmt.Errorf("replication_target_%s_fail_closed_error: %w", targetNodeID, attemptErr),
				)
			}
		} else {
			succeededTargets++
			s.appendReplicationTargetEventOutputBestEffort(
				event.ID,
				targetNodeID,
				fmt.Sprintf(
					"replication_target_attempt_succeeded: generation_id=%s completed_datasets=%d required_datasets=%d",
					generationResult.GenerationID,
					generationResult.CompletedDatasetCount,
					generationResult.RequiredDatasetCount,
				),
			)
			logger.L.Info().
				Uint("policy_id", policy.ID).
				Str("target_node_id", targetNodeID).
				Str("generation_id", generationResult.GenerationID).
				Int("completed_datasets", generationResult.CompletedDatasetCount).
				Int("required_datasets", generationResult.RequiredDatasetCount).
				Msg("replication_target_succeeded")
		}
	}

	if succeededTargets == 0 && len(failedTargets) > 0 {
		logger.L.Error().
			Uint("policy_id", policy.ID).
			Strs("failed_targets", failedTargets).
			Msg("replication_all_targets_failed")
	}

	// If some targets succeeded and some failed, surface a degraded
	// status so the UI can show a warning rather than hiding failures
	// behind "success".
	if succeededTargets > 0 && len(failedTargets) > 0 {
		logger.L.Warn().
			Uint("policy_id", policy.ID).
			Strs("failed_targets", failedTargets).
			Int("succeeded", succeededTargets).
			Int("failed", len(failedTargets)).
			Msg("replication_partial_success")
	}
	runErr = replicationRunOutcomeError(
		eligibleTargets,
		attemptedTransfers,
		succeededTargets,
		failedTargets,
		skippedOffline,
		skippedNoIdentity,
	)
	runErr = joinReplicationRunCriticalErrors(runErr, criticalTargetErrors)
	s.appendReplicationEventOutputBestEffort(event.ID, fmt.Sprintf(
		"replication_run_summary: succeeded=%d failed=%d offline=%d missing_identity=%d",
		succeededTargets,
		len(failedTargets),
		skippedOffline,
		skippedNoIdentity,
	))

	if finalizeErr := s.finalizeReplicationEvent(&event, runErr); finalizeErr != nil {
		runErr = errors.Join(runErr, finalizeErr)
	}
	s.updateReplicationPolicyResult(policy, runErr)

	return runErr
}

func (s *Service) refreshReplicationVMMetadata(policy *clusterModels.ReplicationPolicy) error {
	if policy == nil {
		return fmt.Errorf("invalid_policy")
	}
	if strings.TrimSpace(policy.GuestType) != clusterModels.ReplicationGuestTypeVM {
		return nil
	}
	if policy.GuestID == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}
	if s.VM == nil {
		return fmt.Errorf("vm_service_unavailable")
	}
	return s.VM.WriteVMJson(policy.GuestID)
}

func (s *Service) refreshReplicationSourceMetadataForRun(policy *clusterModels.ReplicationPolicy) error {
	metadataErr := s.refreshReplicationVMMetadata(policy)
	if metadataErr == nil {
		return nil
	}
	runErr := fmt.Errorf("replication_vm_metadata_refresh_failed: %w", metadataErr)
	if invalidateErr := s.invalidateReplicationPolicyTargetReadiness(policy, runErr); invalidateErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("invalidate_replication_target_readiness_failed: %w", invalidateErr))
	}
	return runErr
}

func (s *Service) invalidateReplicationPolicyTargetReadiness(
	policy *clusterModels.ReplicationPolicy,
	reason error,
) error {
	if policy == nil || policy.ID == 0 || policy.OwnerEpoch == 0 {
		return fmt.Errorf("invalid_replication_policy_readiness_invalidation")
	}
	if len(policy.Targets) == 0 {
		return fmt.Errorf("replication_policy_targets_unavailable_for_invalidation")
	}
	lastError := "replication_target_invalidated"
	if reason != nil && strings.TrimSpace(reason.Error()) != "" {
		lastError = strings.TrimSpace(reason.Error())
	}
	var invalidationErrs []error
	for _, target := range policy.Targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" {
			continue
		}
		if err := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
			PolicyID:              policy.ID,
			NodeID:                nodeID,
			ExpectedOwnerEpoch:    policy.OwnerEpoch,
			EvaluatedAt:           s.now().UTC(),
			Ready:                 false,
			GenerationID:          target.GenerationID,
			ManifestHash:          target.ManifestHash,
			RequiredDatasetCount:  target.RequiredDatasetCount,
			CompletedDatasetCount: target.CompletedDatasetCount,
			LastVerifiedAt:        target.LastVerifiedAt,
			LastError:             lastError,
		}); err != nil {
			invalidationErrs = append(invalidationErrs, fmt.Errorf("target_%s: %w", nodeID, err))
		}
	}
	return errors.Join(invalidationErrs...)
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

type replicationGenerationTransferResult struct {
	GenerationID          string
	SnapshotName          string
	ManifestHash          string
	RequiredDatasetCount  int
	CompletedDatasetCount int
}

type replicationStagedDataset struct {
	sourceDataset string
	destSuffix    string
	target        *clusterModels.BackupTarget
	options       ReplicationZFSTransferOptions
	result        ReplicationStagedTransferResult
	alreadyReady  bool
}

func shouldCleanupReplicationSourceSnapshot(
	createdHere bool,
	cleanupProven bool,
	generationVerifiedOnTarget bool,
	runErr error,
) bool {
	return createdHere &&
		cleanupProven &&
		runErr != nil &&
		!generationVerifiedOnTarget &&
		!errors.Is(runErr, errReplicationGenerationCommitUncertain)
}

func (s *Service) cleanupReplicationSourceSnapshotGroup(
	ctx context.Context,
	sourceDatasets []string,
	snapshotName string,
) error {
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replicationControlDefaultTimeout)
	defer cancel()

	seen := make(map[string]struct{}, len(sourceDatasets))
	cleanupErrs := make([]error, 0)
	for _, sourceDataset := range sourceDatasets {
		sourceDataset = normalizeDatasetPath(sourceDataset)
		if sourceDataset == "" {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("source_dataset_required"))
			continue
		}
		if _, exists := seen[sourceDataset]; exists {
			continue
		}
		seen[sourceDataset] = struct{}{}
		if err := s.destroyLocalSnapshotBestEffort(cleanupCtx, sourceDataset, snapshotName); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy_source_snapshot_%s_failed: %w", sourceDataset, err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (s *Service) replicationDatasetGenerationReady(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	opts ReplicationZFSTransferOptions,
) (bool, error) {
	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	exists, err := s.targetReplicationDatasetExists(ctx, target, targetDataset)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	expected := replicationProvenanceProperties(opts, sourceDataset, targetDataset, replicationStateReady)
	actual, err := s.readTargetReplicationProperties(
		ctx,
		target,
		targetDataset,
		append(sortedReplicationPropertyNames(expected), "readonly"),
	)
	if err != nil {
		return false, err
	}
	recoverPromotedState := false
	if verifyReplicationPropertyValues(actual, expected) != nil {
		expectedWithoutState := make(map[string]string, len(expected)-1)
		for property, value := range expected {
			if property != replicationPropertyState {
				expectedWithoutState[property] = value
			}
		}
		if verifyReplicationPropertyValues(actual, expectedWithoutState) != nil ||
			actual[replicationPropertyState] != replicationStateStaged {
			return false, nil
		}
		recoverPromotedState = true
	}
	if actual["readonly"] != "on" {
		return false, nil
	}
	if err := s.verifyTargetReplicationReadonly(ctx, target, targetDataset); err != nil {
		return false, nil
	}
	identities, err := s.listHaSnapshotIdentitiesRemote(ctx, target, targetDataset)
	if err != nil {
		return false, err
	}
	guid, err := replicationSnapshotGUID(identities, opts.SnapshotName)
	if err != nil {
		return false, nil
	}
	if guid != strings.TrimSpace(opts.SnapshotGUID) {
		return false, nil
	}
	if recoverPromotedState {
		if err := s.setTargetReplicationProperties(ctx, target, targetDataset, map[string]string{
			replicationPropertyState: replicationStateReady,
		}); err != nil {
			return false, fmt.Errorf("replication_promoted_generation_finalize_failed: %w", err)
		}
	}
	return true, nil
}

func replicationGenerationSnapshotName(generationID string) (string, error) {
	generationID = strings.TrimSpace(generationID)
	if !validReplicationZFSToken(generationID) {
		return "", fmt.Errorf("invalid_replication_generation_id")
	}
	snapshotName := haSnapPrefix + generationID
	if len(snapshotName) > 128 {
		sum := sha256.Sum256([]byte(generationID))
		snapshotName = fmt.Sprintf("%s%x", haSnapPrefix, sum[:16])
	}
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return "", err
	}
	return snapshotName, nil
}

func replicationSnapshotManifestHash(
	policyID uint,
	ownerEpoch uint64,
	generationID string,
	manifest []ReplicationSnapshotManifestEntry,
) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "policy=%d\nepoch=%d\ngeneration=%s\n", policyID, ownerEpoch, strings.TrimSpace(generationID))
	for _, entry := range manifest {
		_, _ = fmt.Fprintf(
			h,
			"dataset=%s\nsnapshot=%s\nguid=%s\n",
			normalizeDatasetPath(entry.SourceDataset),
			strings.TrimSpace(entry.SnapshotName),
			strings.TrimSpace(entry.SnapshotGUID),
		)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func replicationTargetReadyUntil(policy *clusterModels.ReplicationPolicy, verifiedAt time.Time) time.Time {
	window := 5 * time.Minute
	if policy != nil {
		if next, err := nextRunTime(policy.CronExpr, verifiedAt); err == nil {
			interval := next.Sub(verifiedAt)
			if following, nextErr := nextRunTime(policy.CronExpr, next); nextErr == nil {
				if nextInterval := following.Sub(next); nextInterval > interval {
					interval = nextInterval
				}
			}
			if interval > 0 && 2*interval > window {
				window = 2 * interval
			}
		}
	}
	return verifiedAt.Add(window)
}

func replicationProtectionStateFromTargets(policy *clusterModels.ReplicationPolicy, now time.Time) string {
	if policy == nil || !policy.Enabled {
		return clusterModels.ReplicationProtectionStateUnprotected
	}
	if len(policy.Targets) == 0 {
		return clusterModels.ReplicationProtectionStateInitializing
	}
	verified := false
	for i := range policy.Targets {
		if policy.Targets[i].LastVerifiedAt != nil && !policy.Targets[i].LastVerifiedAt.IsZero() {
			verified = true
		}
	}
	for i := range policy.Targets {
		target := &policy.Targets[i]
		if !replicationTargetEligibleForPromotion(target, replicationPolicyOwnerEpoch(policy), now, false) {
			if verified {
				return clusterModels.ReplicationProtectionStateDegraded
			}
			return clusterModels.ReplicationProtectionStateInitializing
		}
	}
	return clusterModels.ReplicationProtectionStateArmed
}

func (s *Service) validateReplicationTransferAuthority(
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) (*clusterModels.ReplicationPolicy, error) {
	if policyID == 0 || expectedOwnerEpoch == 0 {
		return nil, fmt.Errorf("invalid_replication_transfer_authority")
	}
	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("replication_policy_disabled")
	}
	if strings.EqualFold(
		strings.TrimSpace(policy.ProtectionState),
		clusterModels.ReplicationProtectionStateDeleting,
	) {
		// Marking a policy deleting is the cancellation signal for every
		// in-flight send guarded by withReplicationAuthorityMonitor.
		return nil, fmt.Errorf("replication_policy_deleting")
	}
	if replicationPolicyOwnerEpoch(policy) != expectedOwnerEpoch {
		return nil, fmt.Errorf("replication_transfer_owner_epoch_changed")
	}
	transitionRunID = strings.TrimSpace(transitionRunID)
	if transitionRunID == "" {
		if transitionStateInProgress(policy.TransitionState) {
			return nil, fmt.Errorf("replication_policy_transition_in_progress")
		}
	} else if strings.TrimSpace(policy.TransitionRunID) != transitionRunID ||
		!transitionStateInProgress(policy.TransitionState) {
		return nil, fmt.Errorf("replication_transition_run_mismatch")
	}
	if err := s.validateLocalReplicationPolicyLease(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *Service) withReplicationAuthorityMonitor(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	operation func(context.Context) error,
) error {
	if _, err := s.validateReplicationTransferAuthority(policyID, expectedOwnerEpoch, transitionRunID); err != nil {
		return err
	}
	monitoredCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	authorityErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-monitoredCtx.Done():
				return
			case <-ticker.C:
				if _, err := s.validateReplicationTransferAuthority(policyID, expectedOwnerEpoch, transitionRunID); err != nil {
					select {
					case authorityErr <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()

	operationErr := operation(monitoredCtx)
	close(done)
	select {
	case err := <-authorityErr:
		return fmt.Errorf("replication_transfer_authority_lost: %w", err)
	default:
		return operationErr
	}
}

func (s *Service) replicationTargetSpec(
	targetNodeID string,
	sourceDataset string,
	identities map[string]clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
) (*clusterModels.BackupTarget, string, error) {
	identity, ok := identities[strings.TrimSpace(targetNodeID)]
	if !ok {
		return nil, "", fmt.Errorf("replication_target_identity_missing")
	}
	targetHost := strings.TrimSpace(identity.SSHHost)
	if targetHost == "" {
		return nil, "", fmt.Errorf("replication_target_identity_host_missing")
	}
	targetUser := strings.TrimSpace(identity.SSHUser)
	if targetUser == "" {
		targetUser = "root"
	}
	backupRoot, destSuffix := splitDatasetForTarget(sourceDataset)
	return &clusterModels.BackupTarget{
		SSHHost:    fmt.Sprintf("%s@%s", targetUser, targetHost),
		SSHPort:    identity.SSHPort,
		SSHKeyPath: privateKeyPath,
		BackupRoot: backupRoot,
		Enabled:    true,
	}, destSuffix, nil
}

func (s *Service) replicatePolicyGenerationToTarget(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	generationID string,
	eventID uint,
) (result replicationGenerationTransferResult, retErr error) {
	if policy == nil || policy.ID == 0 {
		return result, fmt.Errorf("invalid_policy")
	}
	if _, err := s.validateReplicationTransferAuthority(policy.ID, expectedOwnerEpoch, transitionRunID); err != nil {
		return result, err
	}
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		generationID = fmt.Sprintf("replication-%d-%s", policy.ID, compactNowToken())
	}
	snapshotName, err := replicationGenerationSnapshotName(generationID)
	if err != nil {
		return result, err
	}

	sourceDatasets, err := s.replicationSourceDatasets(ctx, policy)
	if err != nil {
		return result, err
	}
	sort.Strings(sourceDatasets)
	if len(sourceDatasets) == 0 {
		return result, fmt.Errorf("no_source_datasets_found")
	}
	snapshotErr := s.CreateReplicationSnapshotGroup(ctx, sourceDatasets, snapshotName)
	snapshotCreatedHere := snapshotErr == nil
	generationVerifiedOnTarget := false
	sourceSnapshotCleanupProven := snapshotCreatedHere
	untrackedTargetCleanupProven := true
	defer func() {
		if !shouldCleanupReplicationSourceSnapshot(
			snapshotCreatedHere,
			sourceSnapshotCleanupProven,
			generationVerifiedOnTarget,
			retErr,
		) {
			return
		}
		if cleanupErr := s.cleanupReplicationSourceSnapshotGroup(ctx, sourceDatasets, snapshotName); cleanupErr != nil {
			// Preserve the original failure identity while surfacing cleanup
			// diagnostics. Callers may rely on errors.Is for cancellation or
			// other control-flow errors.
			retErr = fmt.Errorf("%w; cleanup_failed_replication_source_snapshot_group: %v", retErr, cleanupErr)
		}
	}()
	manifest, err := s.GetReplicationSnapshotManifest(ctx, sourceDatasets, snapshotName)
	if err != nil {
		if snapshotErr != nil {
			return result, fmt.Errorf("replication_snapshot_group_failed: %v; manifest_verification_failed: %w", snapshotErr, err)
		}
		return result, err
	}
	treeManifest, err := s.GetReplicationSnapshotTreeManifest(ctx, sourceDatasets, snapshotName)
	if err != nil {
		return result, fmt.Errorf("replication_snapshot_tree_manifest_failed: %w", err)
	}
	result.GenerationID = generationID
	result.SnapshotName = snapshotName
	result.ManifestHash = replicationSnapshotManifestHash(policy.ID, expectedOwnerEpoch, generationID, treeManifest)
	result.RequiredDatasetCount = len(manifest)

	identities, err := s.Cluster.ListClusterSSHIdentities()
	if err != nil {
		return result, err
	}
	identityByNode := make(map[string]clusterModels.ClusterSSHIdentity, len(identities))
	for _, identity := range identities {
		identityByNode[strings.TrimSpace(identity.NodeUUID)] = identity
	}
	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return result, fmt.Errorf("cluster_ssh_private_key_path_failed: %w", err)
	}

	staged := make([]replicationStagedDataset, 0, len(manifest))
	// Invalidate the prior aggregate before inspecting/replaying any candidate.
	// An exact transition run may update its target checkpoint while the policy
	// remains suspended; this prevents a failed catch-up from re-arming stale
	// metadata after a partial power-loss boundary.
	if err := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID:              policy.ID,
		NodeID:                targetNodeID,
		ExpectedOwnerEpoch:    expectedOwnerEpoch,
		EvaluatedAt:           s.now().UTC(),
		Ready:                 false,
		GenerationID:          generationID,
		ManifestHash:          result.ManifestHash,
		RequiredDatasetCount:  result.RequiredDatasetCount,
		CompletedDatasetCount: 0,
		LastError:             "replication_generation_commit_in_progress",
		TransitionRunID:       strings.TrimSpace(transitionRunID),
	}); err != nil {
		return result, fmt.Errorf("clear_replication_target_readiness_before_commit_failed: %w", err)
	}

	candidateReadyAt := func(candidateCtx context.Context, item replicationStagedDataset) (bool, error) {
		return s.replicationDatasetGenerationReady(
			candidateCtx,
			item.target,
			item.sourceDataset,
			item.destSuffix,
			item.options,
		)
	}
	candidateReady := func(item replicationStagedDataset) (bool, error) {
		return candidateReadyAt(ctx, item)
	}
	rollbackCandidateGeneration := func(baseErr error) error {
		rollbackBaseCtx := ctx
		if rollbackBaseCtx == nil {
			rollbackBaseCtx = context.Background()
		}
		rollbackCtx, cancelRollback := context.WithTimeout(
			context.WithoutCancel(rollbackBaseCtx),
			replicationControlDefaultTimeout,
		)
		defer cancelRollback()

		uncertain := false
		rollbackCleanupProven := true
		for i := len(staged) - 1; i >= 0; i-- {
			item := staged[i]
			ready, probeErr := candidateReadyAt(rollbackCtx, item)
			if probeErr != nil {
				baseErr = appendReplicationStepError(baseErr, "probe_candidate_before_rollback_"+item.sourceDataset, probeErr)
				uncertain = true
				rollbackCleanupProven = false
				continue
			}
			targetDataset := targetDatasetPath(item.target.BackupRoot, item.destSuffix)
			stagingDataset, stagingPathErr := replicationStagingDatasetPath(targetDataset, item.options)
			if stagingPathErr != nil {
				baseErr = appendReplicationStepError(baseErr, "derive_candidate_stage_"+item.sourceDataset, stagingPathErr)
				uncertain = true
				rollbackCleanupProven = false
				continue
			}
			stageExists, stageErr := s.targetReplicationDatasetExists(rollbackCtx, item.target, stagingDataset)
			if stageErr != nil {
				baseErr = appendReplicationStepError(baseErr, "probe_candidate_stage_"+item.sourceDataset, stageErr)
				uncertain = true
				rollbackCleanupProven = false
				continue
			}
			if !ready && !stageExists {
				continue
			}
			if rollbackErr := s.RollbackPromotedReplicationDataset(
				rollbackCtx,
				item.target,
				item.sourceDataset,
				item.destSuffix,
				item.options,
			); rollbackErr != nil {
				baseErr = appendReplicationStepError(baseErr, "rollback_promoted_dataset_"+item.sourceDataset, rollbackErr)
				uncertain = true
				rollbackCleanupProven = false
			}
		}
		for _, item := range staged {
			ready, probeErr := candidateReadyAt(rollbackCtx, item)
			if probeErr != nil {
				baseErr = appendReplicationStepError(baseErr, "probe_candidate_after_rollback_"+item.sourceDataset, probeErr)
				uncertain = true
				rollbackCleanupProven = false
				continue
			}
			if ready {
				baseErr = appendReplicationStepError(baseErr, "candidate_generation_remains_after_rollback_"+item.sourceDataset, nil)
				uncertain = true
				rollbackCleanupProven = false
			}
		}
		if !uncertain {
			// The active target has been proven free of this candidate on every
			// root. Remove the exact rolled-back staging datasets so repeated
			// failed runs cannot consume the target pool indefinitely.
			for _, item := range staged {
				targetDataset := targetDatasetPath(item.target.BackupRoot, item.destSuffix)
				stagingDataset, stagingErr := replicationStagingDatasetPath(targetDataset, item.options)
				if stagingErr != nil {
					baseErr = appendReplicationStepError(baseErr, "derive_rolled_back_stage_"+item.sourceDataset, stagingErr)
					rollbackCleanupProven = false
					continue
				}
				if cleanupErr := s.cleanupExactReplicationStagingAfterFailure(
					rollbackCtx,
					item.target,
					stagingDataset,
					item.options,
					item.sourceDataset,
					targetDataset,
				); cleanupErr != nil {
					baseErr = appendReplicationStepError(baseErr, "cleanup_rolled_back_stage_"+item.sourceDataset, cleanupErr)
					rollbackCleanupProven = false
				}
			}
		}
		if uncertain {
			sourceSnapshotCleanupProven = false
			return fmt.Errorf("%w: %v", errReplicationGenerationCommitUncertain, baseErr)
		}
		sourceSnapshotCleanupProven = snapshotCreatedHere && untrackedTargetCleanupProven && rollbackCleanupProven
		return baseErr
	}
	for _, entry := range manifest {
		targetSpec, destSuffix, specErr := s.replicationTargetSpec(targetNodeID, entry.SourceDataset, identityByNode, privateKeyPath)
		if specErr != nil {
			return result, specErr
		}
		opts := ReplicationZFSTransferOptions{
			PolicyID:               policy.ID,
			RunID:                  generationID,
			OwnerEpoch:             expectedOwnerEpoch,
			SnapshotName:           snapshotName,
			SnapshotGUID:           entry.SnapshotGUID,
			SnapshotAlreadyCreated: true,
			GenerationName:         generationID,
		}
		alreadyReady, readyErr := s.replicationDatasetGenerationReady(
			ctx,
			targetSpec,
			entry.SourceDataset,
			destSuffix,
			opts,
		)
		if readyErr != nil {
			return result, rollbackCandidateGeneration(fmt.Errorf("verify_replication_dataset_generation_%s_failed: %w", entry.SourceDataset, readyErr))
		}
		if alreadyReady {
			sourceSnapshotCleanupProven = false
			targetDataset := targetDatasetPath(targetSpec.BackupRoot, destSuffix)
			staged = append(staged, replicationStagedDataset{
				sourceDataset: entry.SourceDataset,
				destSuffix:    destSuffix,
				target:        targetSpec,
				options:       opts,
				result: ReplicationStagedTransferResult{
					SnapshotName:  snapshotName,
					SnapshotGUID:  entry.SnapshotGUID,
					TargetDataset: targetDataset,
				},
				alreadyReady: true,
			})
			continue
		}
		var stagedResult ReplicationStagedTransferResult
		sourceSnapshotCleanupProven = false
		untrackedTargetCleanupProven = false
		transferErr := s.withReplicationAuthorityMonitor(
			ctx,
			policy.ID,
			expectedOwnerEpoch,
			transitionRunID,
			func(transferCtx context.Context) error {
				var sendErr error
				// The callback receives both seed and transfer output. Intentionally
				// ignore the returned aggregate so each event line is stored once.
				stagedResult, _, sendErr = s.ReplicationZFSSendStaged(
					transferCtx,
					targetSpec,
					entry.SourceDataset,
					destSuffix,
					opts,
					func(line string) {
						if eventID != 0 {
							s.appendReplicationTargetEventOutputBestEffort(eventID, targetNodeID, line)
						}
					},
				)
				return sendErr
			},
		)
		if transferErr != nil {
			targetDataset := targetDatasetPath(targetSpec.BackupRoot, destSuffix)
			stagingDataset, stagingErr := replicationStagingDatasetPath(targetDataset, opts)
			if stagingErr != nil {
				transferErr = appendReplicationStepError(transferErr, "derive_failed_replication_stage", stagingErr)
			} else if cleanupErr := s.cleanupExactReplicationStagingAfterFailure(
				ctx,
				targetSpec,
				stagingDataset,
				opts,
				entry.SourceDataset,
				targetDataset,
			); cleanupErr != nil {
				transferErr = appendReplicationStepError(transferErr, "cleanup_failed_replication_stage", cleanupErr)
			} else {
				untrackedTargetCleanupProven = true
			}
			return result, rollbackCandidateGeneration(fmt.Errorf("stage_replication_dataset_%s_failed: %w", entry.SourceDataset, transferErr))
		}
		staged = append(staged, replicationStagedDataset{
			sourceDataset: entry.SourceDataset,
			destSuffix:    destSuffix,
			target:        targetSpec,
			options:       opts,
			result:        stagedResult,
		})
		untrackedTargetCleanupProven = true
	}

	for _, item := range staged {
		if item.alreadyReady {
			result.CompletedDatasetCount++
			continue
		}
		if _, err := s.validateReplicationTransferAuthority(policy.ID, expectedOwnerEpoch, transitionRunID); err != nil {
			return result, rollbackCandidateGeneration(fmt.Errorf("pre_promotion_authority_check_failed: %w", err))
		}
		promoteErr := s.PromoteStagedReplicationDataset(
			ctx,
			item.target,
			item.sourceDataset,
			item.destSuffix,
			item.options,
		)
		if promoteErr != nil {
			// The rename can commit remotely even when the SSH response is lost.
			// Treat an exactly proven candidate as success; otherwise roll back
			// every root observed at this generation.
			ready, probeErr := candidateReady(item)
			if probeErr != nil {
				promoteErr = appendReplicationStepError(promoteErr, "promotion_outcome_probe_failed", probeErr)
				return result, rollbackCandidateGeneration(fmt.Errorf("promote_replication_dataset_%s_ambiguous: %w", item.sourceDataset, promoteErr))
			}
			if !ready {
				return result, rollbackCandidateGeneration(fmt.Errorf("promote_replication_dataset_%s_failed: %w", item.sourceDataset, promoteErr))
			}
		}
		result.CompletedDatasetCount++
	}
	// Readiness is published only after every root independently proves the
	// exact policy/run/epoch/snapshot generation. This also closes replay cases
	// where an `alreadyReady` root was left behind by a prior crash.
	verifiedCount := 0
	for _, item := range staged {
		ready, verifyErr := candidateReady(item)
		if verifyErr != nil {
			return result, rollbackCandidateGeneration(fmt.Errorf("verify_committed_replication_dataset_%s_failed: %w", item.sourceDataset, verifyErr))
		}
		if !ready {
			return result, rollbackCandidateGeneration(fmt.Errorf("replication_dataset_generation_not_committed_%s", item.sourceDataset))
		}
		verifiedCount++
	}
	remoteTreeParts := make([][]ReplicationSnapshotManifestEntry, 0, len(staged))
	for _, item := range staged {
		targetDataset := targetDatasetPath(item.target.BackupRoot, item.destSuffix)
		remoteTree, treeErr := s.replicationSnapshotTreeManifestRemote(
			ctx,
			item.target,
			targetDataset,
			item.sourceDataset,
			snapshotName,
		)
		if treeErr != nil {
			return result, rollbackCandidateGeneration(fmt.Errorf(
				"verify_committed_replication_tree_%s_failed: %w",
				item.sourceDataset,
				treeErr,
			))
		}
		remoteTreeParts = append(remoteTreeParts, remoteTree)
	}
	remoteTreeManifest, treeErr := mergeReplicationSnapshotTreeManifests(remoteTreeParts...)
	if treeErr != nil {
		return result, rollbackCandidateGeneration(fmt.Errorf("merge_committed_replication_tree_failed: %w", treeErr))
	}
	if !replicationSnapshotTreeManifestsEqual(treeManifest, remoteTreeManifest) ||
		replicationSnapshotManifestHash(policy.ID, expectedOwnerEpoch, generationID, remoteTreeManifest) != result.ManifestHash {
		return result, rollbackCandidateGeneration(fmt.Errorf("replication_committed_tree_manifest_mismatch"))
	}
	generationVerifiedOnTarget = true
	result.CompletedDatasetCount = verifiedCount
	if strings.TrimSpace(transitionRunID) != "" {
		verifiedAt := s.now().UTC()
		readyUntil := replicationTargetReadyUntil(policy, verifiedAt)
		if err := s.publishReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
			PolicyID:              policy.ID,
			NodeID:                targetNodeID,
			ExpectedOwnerEpoch:    expectedOwnerEpoch,
			EvaluatedAt:           verifiedAt,
			Ready:                 true,
			GenerationID:          result.GenerationID,
			ManifestHash:          result.ManifestHash,
			RequiredDatasetCount:  result.RequiredDatasetCount,
			CompletedDatasetCount: result.CompletedDatasetCount,
			LastVerifiedAt:        &verifiedAt,
			ReadyUntil:            &readyUntil,
			TransitionRunID:       strings.TrimSpace(transitionRunID),
		}); err != nil {
			return result, fmt.Errorf("checkpoint_transition_target_generation_failed: %w", err)
		}
	}
	// Cleanup is deliberately after the complete multi-root commit. Each
	// primitive revalidates provenance and keeps unknown history untouched.
	for _, item := range staged {
		if cleanupErr := s.CleanupPreviousReplicationGenerations(
			ctx,
			item.target,
			item.result.TargetDataset,
			policy.ID,
			defaultReplicationLineageKeepOld,
		); cleanupErr != nil {
			logger.L.Warn().
				Err(cleanupErr).
				Uint("policy_id", policy.ID).
				Str("target_dataset", item.result.TargetDataset).
				Msg("replication_previous_generation_cleanup_failed")
		}
		if cleanupErr := s.cleanupStaleReplicationStagingGenerations(
			ctx,
			item.target,
			item.result.TargetDataset,
			policy.ID,
			expectedOwnerEpoch,
			generationID,
		); cleanupErr != nil {
			logger.L.Warn().
				Err(cleanupErr).
				Uint("policy_id", policy.ID).
				Str("target_dataset", item.result.TargetDataset).
				Msg("replication_stale_staging_cleanup_failed")
		}
	}
	return result, nil
}

func (s *Service) applyReplicationRetention(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	eventID uint,
	targetNodeID string,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	if err := s.retainReplicationSnapshots(ctx, target, sourceDataset, destSuffix, defaultReplicationPruneKeepLast); err != nil {
		s.appendReplicationTargetEventOutputBestEffort(eventID, targetNodeID, fmt.Sprintf("replication_prune_warning: %v", err))
		logger.L.Warn().
			Err(err).
			Uint("event_id", eventID).
			Str("source_dataset", sourceDataset).
			Msg("replication_prune_failed")
	}
	// Dataset-generation cleanup is performed by provenance-aware primitives
	// only. The legacy name-pattern trimmer could destroy an unknown sibling
	// that merely happened to use `_gen-` naming.
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

func (s *Service) publishReplicationTargetReadiness(update clusterModels.ReplicationTargetReadinessUpdate) error {
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	bypassRaft := s.Cluster.Raft == nil
	err := s.Cluster.UpdateReplicationTargetReadiness(update, bypassRaft)
	if err == nil {
		return nil
	}
	if bypassRaft || !isReplicationRaftNotLeaderError(err) {
		return err
	}
	_, leaderID := s.Cluster.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	if leaderNodeID == "" {
		return fmt.Errorf("leader_unknown")
	}
	return s.forwardReplicationPolicyControlWithRetry(
		leaderNodeID,
		"replication-target-readiness",
		map[string]any{
			"policyId":              update.PolicyID,
			"nodeId":                update.NodeID,
			"expectedOwnerEpoch":    update.ExpectedOwnerEpoch,
			"evaluatedAt":           update.EvaluatedAt,
			"ready":                 update.Ready,
			"generationId":          update.GenerationID,
			"manifestHash":          update.ManifestHash,
			"requiredDatasetCount":  update.RequiredDatasetCount,
			"completedDatasetCount": update.CompletedDatasetCount,
			"lastVerifiedAt":        update.LastVerifiedAt,
			"readyUntil":            update.ReadyUntil,
			"lastError":             update.LastError,
			"transitionRunId":       update.TransitionRunID,
		},
		5*time.Second,
	)
}

func isReplicationRaftNotLeaderError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, raft.ErrNotLeader) {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not_leader") ||
		strings.Contains(message, "not the leader") ||
		strings.Contains(message, "not leader")
}

func (s *Service) finalizeReplicationEvent(event *clusterModels.ReplicationEvent, runErr error) error {
	if event == nil || event.ID == 0 {
		return fmt.Errorf("replication_event_required_for_finalization")
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

	result := s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
		"status":       event.Status,
		"error":        event.Error,
		"message":      event.Message,
		"completed_at": event.CompletedAt,
	})
	if result.Error != nil || result.RowsAffected != 1 {
		finalizeErr := result.Error
		if finalizeErr == nil {
			finalizeErr = gorm.ErrRecordNotFound
		}
		finalizeErr = fmt.Errorf("replication_event_finalize_persist_failed: %w", finalizeErr)
		logger.L.Error().
			Err(finalizeErr).
			Uint("event_id", event.ID).
			Msg("replication_event_finalize_persist_failed")
		return finalizeErr
	}

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
	return nil
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

func (s *Service) appendReplicationEventOutputBestEffort(eventID uint, chunk string) {
	if err := s.AppendReplicationEventOutput(eventID, chunk); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("event_id", eventID).
			Msg("append_replication_event_output_failed")
	}
}

func formatReplicationTargetEventOutput(targetNodeID string, chunk string) string {
	targetNodeID = strings.TrimSpace(targetNodeID)
	targetNodeID = strings.NewReplacer("\r", "", "\n", "").Replace(targetNodeID)
	if targetNodeID == "" {
		targetNodeID = "unknown"
	}

	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return ""
	}
	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")
	chunk = strings.ReplaceAll(chunk, "\r", "\n")
	lines := strings.Split(chunk, "\n")
	labelled := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		labelled = append(labelled, fmt.Sprintf("[target=%s] %s", targetNodeID, line))
	}
	return strings.Join(labelled, "\n")
}

func (s *Service) AppendReplicationTargetEventOutput(eventID uint, targetNodeID string, chunk string) error {
	return s.AppendReplicationEventOutput(eventID, formatReplicationTargetEventOutput(targetNodeID, chunk))
}

func (s *Service) appendReplicationTargetEventOutputBestEffort(eventID uint, targetNodeID string, chunk string) {
	if err := s.AppendReplicationTargetEventOutput(eventID, targetNodeID, chunk); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("event_id", eventID).
			Str("target_node_id", strings.TrimSpace(targetNodeID)).
			Msg("append_replication_target_event_output_failed")
	}
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
	badgerKeyCrashMisses  = "repl:crash:"
	badgerKeyDownMisses   = "repl:down:"
	badgerKeyFailbackHits = "repl:failback:"
	badgerCounterTTL      = 86400
)

func badgerCrashKey(policyID uint) string { return fmt.Sprintf("%s%d", badgerKeyCrashMisses, policyID) }
func badgerDownKey(policyID uint) string  { return fmt.Sprintf("%s%d", badgerKeyDownMisses, policyID) }

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
	s.clearFailoverWarnings(policyID)
}

func replicationFailoverWarningKey(ownerNodeID, message string) string {
	return strings.TrimSpace(ownerNodeID) + "\x00" + strings.TrimSpace(message)
}

func (s *Service) reserveFailoverWarning(policyID uint, ownerNodeID, message string) bool {
	if s == nil || policyID == 0 || strings.TrimSpace(message) == "" {
		return false
	}

	key := replicationFailoverWarningKey(ownerNodeID, message)
	s.failoverWarningMu.Lock()
	defer s.failoverWarningMu.Unlock()
	if s.failoverWarnings == nil {
		s.failoverWarnings = make(map[uint]map[string]struct{})
	}
	if s.failoverWarnings[policyID] == nil {
		s.failoverWarnings[policyID] = make(map[string]struct{})
	}
	if _, exists := s.failoverWarnings[policyID][key]; exists {
		return false
	}
	s.failoverWarnings[policyID][key] = struct{}{}
	return true
}

func (s *Service) releaseFailoverWarning(policyID uint, ownerNodeID, message string) {
	if s == nil || policyID == 0 {
		return
	}

	key := replicationFailoverWarningKey(ownerNodeID, message)
	s.failoverWarningMu.Lock()
	defer s.failoverWarningMu.Unlock()
	warnings := s.failoverWarnings[policyID]
	delete(warnings, key)
	if len(warnings) == 0 {
		delete(s.failoverWarnings, policyID)
	}
}

func (s *Service) clearFailoverWarnings(policyID uint) {
	if s == nil || policyID == 0 {
		return
	}
	s.failoverWarningMu.Lock()
	delete(s.failoverWarnings, policyID)
	s.failoverWarningMu.Unlock()

}

func (s *Service) createFailoverWarningOnce(
	policyID uint,
	ownerNodeID string,
	event clusterModels.ReplicationEvent,
) {
	if s == nil || s.Cluster == nil || !s.reserveFailoverWarning(policyID, ownerNodeID, event.Message) {
		return
	}
	if _, err := s.Cluster.CreateOrUpdateReplicationEvent(event, false); err != nil {
		s.releaseFailoverWarning(policyID, ownerNodeID, event.Message)
		logger.L.Warn().Err(err).
			Uint("policy_id", policyID).
			Str("message", strings.TrimSpace(event.Message)).
			Msg("replication_failover_warning_create_failed")
	}
}

func replicationPolicyAllowsLeaseRenewal(policy *clusterModels.ReplicationPolicy) bool {
	if policy == nil || !policy.Enabled || policy.ID == 0 ||
		replicationPolicyOwnerNode(policy) == "" || replicationPolicyOwnerEpoch(policy) == 0 {
		return false
	}

	// An unsafe transition deliberately lets the unreachable owner's lease
	// expire before cutover.  Every other state, including safe catch-up,
	// promoting, rollback, and deleting, retains an authoritative owner.
	switch strings.ToLower(strings.TrimSpace(policy.TransitionState)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup:
		return !policy.TransitionAllowUnsafe
	default:
		return true
	}
}

func (s *Service) runReplicationLeaseRenewalTick(ctx context.Context) error {
	if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return nil
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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
	leases, err := s.Cluster.ListReplicationLeases()
	if err != nil {
		return err
	}
	leaseByPolicy := make(map[uint]clusterModels.ReplicationLease, len(leases))
	for _, lease := range leases {
		leaseByPolicy[lease.PolicyID] = lease
	}

	now := s.now().UTC()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	renewals := make([]clusterModels.ReplicationLease, 0, len(policies))
	for i := range policies {
		policy := &policies[i]
		if !replicationPolicyAllowsLeaseRenewal(policy) {
			continue
		}
		owner := replicationPolicyOwnerNode(policy)
		if !nodeOnlineByID(nodeByID, owner) && owner != localNodeID {
			continue
		}
		version, versionErr := nextReplicationLeaseVersion(now, leaseByPolicy[policy.ID].Version)
		if versionErr != nil {
			logger.L.Warn().Err(versionErr).Uint("policy_id", policy.ID).Msg("replication_lease_version_exhausted")
			continue
		}
		renewals = append(renewals, clusterModels.ReplicationLease{
			PolicyID:    policy.ID,
			GuestType:   policy.GuestType,
			GuestID:     policy.GuestID,
			OwnerNodeID: owner,
			OwnerEpoch:  replicationPolicyOwnerEpoch(policy),
			ExpiresAt:   now.Add(replicationLeaseTTL),
			Version:     version,
			LastReason:  "leader_renew",
			LastActor:   localNodeID,
		})
	}

	if len(renewals) > 0 {
		if err := s.Cluster.UpsertReplicationLeasesBatch(renewals); err != nil {
			return err
		}
	}

	// Readiness has a bounded lifetime. Keep the user-visible protection state
	// synchronized with expiry even when no replication run happens at that
	// exact moment; target selection already enforces the same predicate.
	for i := range policies {
		policy := &policies[i]
		if transitionStateInProgress(policy.TransitionState) {
			continue
		}
		currentState := strings.ToLower(strings.TrimSpace(policy.ProtectionState))
		switch currentState {
		case clusterModels.ReplicationProtectionStateArmed,
			clusterModels.ReplicationProtectionStateInitializing,
			clusterModels.ReplicationProtectionStateDegraded:
		default:
			continue
		}
		desiredState := replicationProtectionStateFromTargets(policy, now)
		if desiredState == currentState {
			continue
		}
		if err := s.Cluster.UpdateReplicationPolicyProtectionState(
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			desiredState,
			false,
		); err != nil {
			logger.L.Warn().Err(err).
				Uint("policy_id", policy.ID).
				Str("protection_state", desiredState).
				Msg("replication_protection_state_reconcile_failed")
		}
	}
	return nil
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
	now := s.now().UTC()
	for i := range policies {
		policy := policies[i]
		observedOwner := replicationPolicyOwnerNode(&policy)
		if observedNode, ok := nodeByID[observedOwner]; ok &&
			strings.EqualFold(strings.TrimSpace(observedNode.Status), "online") {
			// Clear outage-local state even when HA eligibility or an in-progress
			// transition causes this policy to skip the controller below.
			s.downMissesReset(policy.ID)
			s.clearFailoverWarnings(policy.ID)
		}
		if !policy.Enabled {
			continue
		}
		if transitionStateInProgress(policy.TransitionState) || s.IsPolicyTransitionRunning(policy.ID) {
			continue
		}
		if !replicationPolicyAcceptsNewTransition(&policy) {
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
			if policy.FailbackMode == clusterModels.ReplicationFailbackAuto &&
				strings.TrimSpace(policy.SourceNodeID) != "" &&
				strings.TrimSpace(policy.SourceNodeID) != owner {
				sourceOnline := false
				if sourceNode, ok := nodeByID[strings.TrimSpace(policy.SourceNodeID)]; ok {
					sourceOnline = strings.ToLower(strings.TrimSpace(sourceNode.Status)) == "online"
				}
				if sourceOnline {
					fbVal := s.failbackHitsIncr(policy.ID)
					if fbVal >= uint64(replicationFailbackHitLimit) {
						if err := s.failoverPolicyToNode(
							ctx,
							&policy,
							strings.TrimSpace(policy.SourceNodeID),
							"auto_failback",
							true,
							replicationTransitionOptions{
								RunID:                fmt.Sprintf("auto-failback-%d-%s", policy.ID, compactNowToken()),
								AllowUnsafe:          false,
								MovePinnedSource:     false,
								TriggerValidationRun: true,
							},
						); err != nil {
							if isReplicationPolicyTransitionRunningError(err) {
								logger.L.Debug().Uint("policy_id", policy.ID).Msg("auto_failback_transition_already_running")
							} else {
								logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("auto_failback_failed")
							}
						}
						s.failbackHitsReset(policy.ID)
						continue
					}
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

		requireCompleteGeneration := failoverMode == clusterModels.ReplicationFailoverAutoForce
		targetNodeID, selectErr := s.selectFailoverTargetWithReadiness(
			&policy,
			owner,
			nodeByID,
			requireCompleteGeneration,
			requireCompleteGeneration,
		)
		if selectErr != nil {
			s.createFailoverWarningOnce(policy.ID, owner, clusterModels.ReplicationEvent{
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
			})
			continue
		}

		if failoverMode == clusterModels.ReplicationFailoverAutoSafe {
			// A safe handoff requires the current owner to acknowledge demotion and
			// final synchronization. Never reinterpret auto-safe as force recovery.
			s.createFailoverWarningOnce(policy.ID, owner, clusterModels.ReplicationEvent{
				PolicyID:     &policy.ID,
				EventType:    "failover",
				Status:       replicationEventStatusDegraded,
				Message:      "node_down_auto_safe_blocked_owner_unreachable",
				Error:        "safe_failover_requires_owner_reachable",
				SourceNodeID: owner,
				TargetNodeID: targetNodeID,
				GuestType:    policy.GuestType,
				GuestID:      policy.GuestID,
				StartedAt:    now,
				CompletedAt:  &now,
			})
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
				s.createFailoverWarningOnce(policy.ID, owner, clusterModels.ReplicationEvent{
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
				})
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
		s.clearFailoverWarnings(policy.ID)
	}

	return nil
}

func replicationTargetGenerationComplete(
	target *clusterModels.ReplicationPolicyTarget,
	ownerEpoch uint64,
) bool {
	return target != nil &&
		target.Ready &&
		target.OwnerEpoch == ownerEpoch &&
		strings.TrimSpace(target.GenerationID) != "" &&
		strings.TrimSpace(target.ManifestHash) != "" &&
		target.RequiredDatasetCount > 0 &&
		target.CompletedDatasetCount == target.RequiredDatasetCount &&
		target.LastVerifiedAt != nil && !target.LastVerifiedAt.IsZero() &&
		target.ReadyUntil != nil && !target.ReadyUntil.IsZero()
}

func replicationTargetEligibleForPromotion(
	target *clusterModels.ReplicationPolicyTarget,
	ownerEpoch uint64,
	now time.Time,
	allowStale bool,
) bool {
	if !replicationTargetGenerationComplete(target, ownerEpoch) {
		return false
	}
	return allowStale || now.UTC().Before(target.ReadyUntil.UTC())
}

func replicationPolicyTargetByNode(
	policy *clusterModels.ReplicationPolicy,
	nodeID string,
) *clusterModels.ReplicationPolicyTarget {
	if policy == nil {
		return nil
	}
	nodeID = strings.TrimSpace(nodeID)
	for i := range policy.Targets {
		if strings.TrimSpace(policy.Targets[i].NodeID) == nodeID {
			return &policy.Targets[i]
		}
	}
	return nil
}

func (s *Service) validateUnsafeFailoverTargetGeneration(
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
) error {
	target := replicationPolicyTargetByNode(policy, targetNodeID)
	if target == nil {
		return fmt.Errorf("replication_target_not_configured_for_policy")
	}
	if !replicationTargetEligibleForPromotion(target, replicationPolicyOwnerEpoch(policy), s.now(), true) {
		return fmt.Errorf("replication_force_target_has_no_complete_verified_generation")
	}
	return nil
}

func bindReplicationTransitionGenerationEvidence(
	policy *clusterModels.ReplicationPolicy,
	targetNodeID string,
	transition *clusterModels.ReplicationPolicyTransition,
	requireTransitionGeneration bool,
) error {
	if policy == nil || transition == nil {
		return fmt.Errorf("replication_transition_generation_input_invalid")
	}
	target := replicationPolicyTargetByNode(policy, targetNodeID)
	if !replicationTargetGenerationComplete(target, replicationPolicyOwnerEpoch(policy)) {
		return fmt.Errorf("replication_transition_target_generation_incomplete")
	}
	if requireTransitionGeneration && strings.TrimSpace(target.GenerationID) != strings.TrimSpace(transition.RunID) {
		return fmt.Errorf("replication_transition_target_generation_run_mismatch")
	}
	if transition.GenerationID != "" {
		if transition.GenerationID != strings.TrimSpace(target.GenerationID) ||
			transition.GenerationOwnerEpoch != target.OwnerEpoch ||
			transition.GenerationManifest != strings.TrimSpace(target.ManifestHash) ||
			transition.GenerationRootCount != target.RequiredDatasetCount {
			return fmt.Errorf("replication_transition_target_generation_changed")
		}
		return nil
	}
	transition.GenerationID = strings.TrimSpace(target.GenerationID)
	transition.GenerationOwnerEpoch = target.OwnerEpoch
	transition.GenerationManifest = strings.TrimSpace(target.ManifestHash)
	transition.GenerationRootCount = target.RequiredDatasetCount
	return nil
}

func (s *Service) selectFailoverTarget(
	policy *clusterModels.ReplicationPolicy,
	currentOwner string,
	nodes map[string]clusterModels.ClusterNode,
) (string, error) {
	return s.selectFailoverTargetWithReadiness(policy, currentOwner, nodes, false, false)
}

func (s *Service) selectFailoverTargetWithReadiness(
	policy *clusterModels.ReplicationPolicy,
	currentOwner string,
	nodes map[string]clusterModels.ClusterNode,
	requireCompleteGeneration bool,
	allowStale bool,
) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("policy_required")
	}

	targets := append([]clusterModels.ReplicationPolicyTarget{}, policy.Targets...)
	sort.SliceStable(targets, func(i, j int) bool {
		if requireCompleteGeneration {
			var left, right time.Time
			if targets[i].LastVerifiedAt != nil {
				left = targets[i].LastVerifiedAt.UTC()
			}
			if targets[j].LastVerifiedAt != nil {
				right = targets[j].LastVerifiedAt.UTC()
			}
			if !left.Equal(right) {
				return left.After(right)
			}
		}
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
		if requireCompleteGeneration && !replicationTargetEligibleForPromotion(
			&target,
			replicationPolicyOwnerEpoch(policy),
			s.now(),
			allowStale,
		) {
			continue
		}
		return nodeID, nil
	}

	if requireCompleteGeneration {
		return "", fmt.Errorf("no_healthy_target_with_complete_replication_generation")
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

func (s *Service) waitForPreviousOwnerLeaseExpiry(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	previousOwner string,
	expectedOwnerEpoch uint64,
) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	previousOwner = strings.TrimSpace(previousOwner)
	if previousOwner == "" || expectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_previous_owner_lease_identity_missing")
	}
	barrierStartedAt := s.now().UTC()
	missingLeaseBarrier := barrierStartedAt.Add(2*replicationLeaseTTL + replicationLeaseExpirySafetyMargin)
	olderLeaseBarrier := barrierStartedAt.Add(replicationLeaseExpirySafetyMargin)

	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		barrier := missingLeaseBarrier
		lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
		if err == nil && lease != nil {
			leaseOwner := strings.TrimSpace(lease.OwnerNodeID)
			switch {
			case lease.OwnerEpoch > expectedOwnerEpoch:
				return fmt.Errorf("replication_previous_owner_lease_epoch_advanced")
			case lease.OwnerEpoch == expectedOwnerEpoch && leaseOwner != previousOwner:
				return fmt.Errorf("replication_previous_owner_lease_owner_changed")
			case lease.OwnerEpoch == expectedOwnerEpoch:
				barrier = lease.ExpiresAt.UTC().Add(replicationLeaseExpirySafetyMargin)
			case lease.OwnerEpoch < expectedOwnerEpoch:
				// An older lease cannot authorize the current owner epoch. Keep
				// only the clock-skew margin before promotion.
				barrier = olderLeaseBarrier
			}
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("replication_previous_owner_lease_lookup_failed: %w", err)
		}

		waitFor := barrier.Sub(s.now().UTC())
		if waitFor <= 0 {
			// Re-read once after crossing the barrier. A renewal assembled
			// before the persisted transition became visible may have extended
			// the old lease while we were waiting.
			lease, recheckErr := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
			if recheckErr == nil && lease != nil &&
				strings.TrimSpace(lease.OwnerNodeID) == previousOwner &&
				lease.OwnerEpoch == expectedOwnerEpoch &&
				s.now().UTC().Before(lease.ExpiresAt.UTC().Add(replicationLeaseExpirySafetyMargin)) {
				continue
			}
			if recheckErr != nil && !errors.Is(recheckErr, gorm.ErrRecordNotFound) {
				return fmt.Errorf("replication_previous_owner_lease_recheck_failed: %w", recheckErr)
			}
			return nil
		}

		// Keep real-clock cancellation responsive while allowing the fake
		// clock to advance deterministically in tests.
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		s.sleep(waitFor)
	}
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
	if !replicationPolicyAcceptsNewTransition(policy) {
		return fmt.Errorf("replication_policy_not_available_for_transition")
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
	if ownerNodeID == "" || replicationPolicyOwnerEpoch(policy) == 0 {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if requestMode == replicationFailoverRequestSafe && !nodeOnlineByID(nodeByID, ownerNodeID) {
		return fmt.Errorf("safe_failover_requires_online_owner_use_force_for_owner_down")
	}

	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID == "" {
		forceMode := requestMode == replicationFailoverRequestForce
		targetNodeID, err = s.selectFailoverTargetWithReadiness(
			policy,
			ownerNodeID,
			nodeByID,
			forceMode,
			forceMode,
		)
		if err != nil {
			return err
		}
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
	if requestMode == replicationFailoverRequestForce {
		if err := s.validateUnsafeFailoverTargetGeneration(policy, targetNodeID); err != nil {
			return err
		}
		quorumOK, quorumErr := s.hasFailoverQuorum(nodeByID)
		if quorumErr != nil {
			return fmt.Errorf("force_failover_quorum_check_failed: %w", quorumErr)
		}
		if !quorumOK {
			return fmt.Errorf("force_failover_requires_quorum")
		}
	}
	if replicationPolicyOwnerEpoch(policy) == math.MaxUint64 {
		return fmt.Errorf("replication_owner_epoch_exhausted")
	}
	projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
		policy,
		targetNodeID,
		movePinnedSource,
	)
	projectedPolicy := *policy
	projectedPolicy.Targets = rotatedReplicationPolicyTargets(
		policy,
		ownerNodeID,
		targetNodeID,
		replicationPolicyOwnerEpoch(policy)+1,
	)
	transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
		&projectedPolicy,
		projectedSourceNodeID,
		projectedActiveNodeID,
	)
	if !transitionEval.Eligible {
		return replicationPolicyHAError(transitionEval)
	}

	runID := fmt.Sprintf("failover-%d-%s", policyID, compactNowToken())
	reason := "manual_failover"
	if requestMode == replicationFailoverRequestForce {
		reason = "manual_force_failover"
	}
	requestedAt := s.now().UTC()
	transition := clusterModels.ReplicationPolicyTransition{
		State:                clusterModels.ReplicationTransitionStateDemoting,
		RunID:                runID,
		Reason:               reason,
		SourceNodeID:         ownerNodeID,
		TargetNodeID:         targetNodeID,
		OwnerEpoch:           replicationPolicyOwnerEpoch(policy),
		RequestedAt:          &requestedAt,
		AllowUnsafe:          requestMode == replicationFailoverRequestForce,
		MovePinnedSource:     movePinnedSource,
		TriggerValidationRun: true,
		OriginalSourceNodeID: strings.TrimSpace(policy.SourceNodeID),
	}
	if requestMode == replicationFailoverRequestForce {
		if err := bindReplicationTransitionGenerationEvidence(policy, targetNodeID, &transition, false); err != nil {
			return err
		}
	}
	if err := s.Cluster.BeginReplicationPolicyTransition(
		clusterModels.ReplicationPolicyTransitionBegin{
			PolicyID:           policy.ID,
			ExpectedOwnerEpoch: replicationPolicyOwnerEpoch(policy),
			Transition:         transition,
			ProtectionState:    clusterModels.ReplicationProtectionStateSuspended,
		},
		false,
	); err != nil {
		return err
	}
	policy.ProtectionState = clusterModels.ReplicationProtectionStateSuspended
	policy.TransitionState = transition.State
	policy.TransitionRunID = transition.RunID
	policy.TransitionReason = transition.Reason
	policy.TransitionSourceNodeID = transition.SourceNodeID
	policy.TransitionTargetNodeID = transition.TargetNodeID
	policy.TransitionOwnerEpoch = transition.OwnerEpoch
	policy.TransitionRequestedAt = transition.RequestedAt
	policy.TransitionAllowUnsafe = transition.AllowUnsafe
	policy.TransitionMovePinnedSource = transition.MovePinnedSource
	policy.TransitionTriggerValidationRun = transition.TriggerValidationRun
	policy.TransitionOriginalSourceNodeID = transition.OriginalSourceNodeID
	policy.TransitionGenerationID = transition.GenerationID
	policy.TransitionGenerationOwnerEpoch = transition.GenerationOwnerEpoch
	policy.TransitionGenerationManifest = transition.GenerationManifest
	policy.TransitionGenerationRootCount = transition.GenerationRootCount

	enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.EnqueueJSON(enqueueCtx, replicationFailoverJobQueueName, replicationFailoverJobPayload{
		PolicyID:         policyID,
		RunID:            runID,
		TargetNodeID:     targetNodeID,
		Mode:             requestMode,
		ConfirmDataLoss:  confirmDataLoss,
		MovePinnedSource: movePinnedSource,
	}); err != nil {
		return s.failPolicyTransition(policy, fmt.Errorf("replication_failover_enqueue_failed: %w", err))
	}
	return nil
}

func (s *Service) requestReplicationPolicyFailover(
	ctx context.Context,
	policyID uint,
	targetNodeID string,
	mode string,
	confirmDataLoss bool,
	movePinnedSource bool,
	runID string,
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
	if !policy.Enabled {
		return fmt.Errorf("replication_policy_disabled")
	}
	runID = strings.TrimSpace(runID)
	if runID != "" && strings.TrimSpace(policy.TransitionRunID) == runID {
		switch strings.ToLower(strings.TrimSpace(policy.TransitionState)) {
		case clusterModels.ReplicationTransitionStateCompleted:
			return nil
		case clusterModels.ReplicationTransitionStateFailed:
			return fmt.Errorf("replication_transition_run_failed: %s", strings.TrimSpace(policy.TransitionError))
		default:
			if transitionStateInProgress(policy.TransitionState) {
				if !s.acquirePolicyTransition(policy.ID) {
					return errReplicationPolicyTransitionAlreadyRunning
				}
				defer s.releasePolicyTransition(policy.ID)
				return s.resumePolicyTransition(ctx, policy)
			}
		}
	}
	if !replicationPolicyAcceptsNewTransition(policy) {
		return fmt.Errorf("replication_policy_not_available_for_transition")
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
		forceMode := requestMode == replicationFailoverRequestForce
		selectedTarget, selectErr := s.selectFailoverTargetWithReadiness(
			policy,
			ownerNodeID,
			nodeByID,
			forceMode,
			forceMode,
		)
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
	if requestMode == replicationFailoverRequestForce {
		if err := s.validateUnsafeFailoverTargetGeneration(policy, targetNodeID); err != nil {
			return err
		}
	}

	projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
		policy,
		targetNodeID,
		movePinnedSource,
	)
	projectedPolicy := *policy
	projectedPolicy.Targets = rotatedReplicationPolicyTargets(
		policy,
		ownerNodeID,
		targetNodeID,
		replicationPolicyOwnerEpoch(policy)+1,
	)
	transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
		&projectedPolicy,
		projectedSourceNodeID,
		projectedActiveNodeID,
	)
	if !transitionEval.Eligible {
		return replicationPolicyHAError(transitionEval)
	}

	options := replicationTransitionOptions{
		RunID:                runID,
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

	previousOwner := replicationPolicyOwnerNode(policy)
	previousSourceNodeID := strings.TrimSpace(policy.SourceNodeID)
	currentEpoch := replicationPolicyOwnerEpoch(policy)
	if currentEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_missing")
	}
	if currentEpoch == math.MaxUint64 {
		return fmt.Errorf("replication_policy_owner_epoch_exhausted")
	}
	if strings.TrimSpace(options.RunID) != "" &&
		strings.TrimSpace(policy.TransitionRunID) == strings.TrimSpace(options.RunID) &&
		transitionStateInProgress(policy.TransitionState) {
		options.AllowUnsafe = policy.TransitionAllowUnsafe
		options.MovePinnedSource = policy.TransitionMovePinnedSource
		options.TriggerValidationRun = policy.TransitionTriggerValidationRun
		requireDemoteAck = !options.AllowUnsafe
	}
	nextEpoch := currentEpoch + 1
	rotatedTargets := rotatedReplicationPolicyTargets(policy, previousOwner, targetNodeID, nextEpoch)

	projectedSourceNodeID, projectedActiveNodeID := projectedPolicyTopologyAfterFailover(
		policy,
		targetNodeID,
		options.MovePinnedSource,
	)
	projectedPolicy := *policy
	projectedPolicy.Targets = rotatedTargets
	transitionEval := s.Cluster.EvaluateReplicationPolicyTransitionHA(
		&projectedPolicy,
		projectedSourceNodeID,
		projectedActiveNodeID,
	)
	if !transitionEval.Eligible {
		return replicationPolicyHAError(transitionEval)
	}

	transitionRunID := strings.TrimSpace(options.RunID)
	if transitionRunID == "" {
		transitionRunID = fmt.Sprintf("%d-%s", policy.ID, compactNowToken())
	}
	eventStartedAt := s.now().UTC()
	if strings.TrimSpace(policy.TransitionRunID) == transitionRunID &&
		transitionStateInProgress(policy.TransitionState) && policy.TransitionRequestedAt != nil {
		eventStartedAt = policy.TransitionRequestedAt.UTC()
	}
	var transitionEvent *clusterModels.ReplicationEvent
	ensureTransitionEvent := func() {
		if transitionEvent != nil {
			return
		}
		event, eventErr := s.ensureReplicationTransitionEvent(
			policy,
			transitionRunID,
			eventStartedAt,
			previousOwner,
			targetNodeID,
			replicationEventStatusDemoting,
			reason+"_demoting",
		)
		if eventErr != nil {
			logger.L.Warn().Err(eventErr).
				Uint("policy_id", policy.ID).
				Str("transition_run_id", transitionRunID).
				Msg("replication_transition_event_ensure_failed")
			return
		}
		transitionEvent = event
	}
	updateTransitionEvent := func(status, message string, transitionErr error, completed bool) {
		if transitionEvent == nil || transitionEvent.ID == 0 {
			return
		}

		transitionEvent.TransitionRunID = transitionRunID
		transitionEvent.Status = status
		transitionEvent.Message = message
		transitionEvent.Error = ""
		if transitionErr != nil {
			transitionEvent.Error = transitionErr.Error()
		}
		if completed {
			completedAt := time.Now().UTC()
			transitionEvent.CompletedAt = &completedAt

			if s.TelemetryDB != nil {
				auditStatus := "success"
				errMsg := ""
				if transitionErr != nil {
					auditStatus = "failed"
					errMsg = transitionErr.Error()
				}
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_failover", policy.ID, auditStatus, errMsg, map[string]any{
					"eventId": transitionEvent.ID,
					"status":  auditStatus,
					"error":   errMsg,
				})
			}
		}
		if _, err := s.Cluster.CreateOrUpdateReplicationEvent(*transitionEvent, false); err != nil {
			logger.L.Warn().Err(err).
				Uint("policy_id", policy.ID).
				Uint("event_id", transitionEvent.ID).
				Msg("replication_transition_event_update_failed")
		}
	}

	transition := clusterModels.ReplicationPolicyTransition{
		State:                clusterModels.ReplicationTransitionStateDemoting,
		RunID:                transitionRunID,
		Reason:               reason,
		SourceNodeID:         previousOwner,
		TargetNodeID:         targetNodeID,
		OwnerEpoch:           currentEpoch,
		RequestedAt:          &eventStartedAt,
		AllowUnsafe:          options.AllowUnsafe,
		MovePinnedSource:     options.MovePinnedSource,
		TriggerValidationRun: options.TriggerValidationRun,
		OriginalSourceNodeID: previousSourceNodeID,
	}
	if strings.TrimSpace(policy.TransitionRunID) == transitionRunID &&
		transitionStateInProgress(policy.TransitionState) {
		transition = transitionPayloadFromPolicy(policy)
		options.AllowUnsafe = transition.AllowUnsafe
		options.MovePinnedSource = transition.MovePinnedSource
		options.TriggerValidationRun = transition.TriggerValidationRun
		requireDemoteAck = !transition.AllowUnsafe
		reason = strings.TrimSpace(transition.Reason)
		if reason == "" {
			reason = "transition_recovery"
		}
	}
	if options.AllowUnsafe {
		if err := bindReplicationTransitionGenerationEvidence(policy, targetNodeID, &transition, false); err != nil {
			return err
		}
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
	activateOnNode := func(nodeID string, expectedEpoch uint64) error {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return fmt.Errorf("replication_target_node_required")
		}
		if transition.OriginalRunning == nil {
			return fmt.Errorf("replication_transition_original_running_missing")
		}
		if nodeID == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			return s.ActivateReplicationPolicyForTransition(
				ctx,
				policy.ID,
				expectedEpoch,
				transition.RunID,
				transition.OriginalRunning,
			)
		}
		return s.forwardActivateReplicationPolicy(
			nodeID,
			policy.ID,
			expectedEpoch,
			transition.RunID,
			transition.OriginalRunning,
		)
	}
	demoteOnNode := func(nodeID string, expectedEpoch uint64) error {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return fmt.Errorf("replication_target_node_required")
		}
		if nodeID == strings.TrimSpace(s.Cluster.LocalNodeID()) {
			return s.DemoteReplicationPolicyForTransition(ctx, policy.ID, expectedEpoch, transition.RunID)
		}
		return s.forwardDemoteReplicationPolicy(nodeID, policy.ID, expectedEpoch, transition.RunID)
	}
	recoverPreviousOwnerIfDemoted := func(baseErr error) error {
		if transition.DemotedAt == nil {
			return baseErr
		}
		if strings.TrimSpace(previousOwner) == "" {
			return fmt.Errorf("%w: %v; replication_previous_owner_missing", errReplicationPreviousOwnerRecoveryUnconfirmed, baseErr)
		}
		if options.AllowUnsafe {
			if renewErr := s.renewReplicationTransitionLease(policy, previousOwner, currentEpoch, reason+"_restore"); renewErr != nil {
				return fmt.Errorf("%w: %v; previous_owner_lease_restore_failed: %v", errReplicationPreviousOwnerRecoveryUnconfirmed, baseErr, renewErr)
			}
		}
		if activateErr := activateOnNode(previousOwner, currentEpoch); activateErr != nil {
			return fmt.Errorf("%w: %v; previous_owner_reactivate_failed: %v", errReplicationPreviousOwnerRecoveryUnconfirmed, baseErr, activateErr)
		}
		return baseErr
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

		// Ownership already belongs to the target at nextEpoch. Before granting
		// the previous owner a newer lease, prove the target is fenced. An online
		// or health-unknown target must acknowledge exact-run demotion; a
		// definitively offline target must cross its lease-expiry/skew barrier,
		// which is then repeated as an atomic commit predicate.
		var targetLeaseExpiresAtOrBefore *time.Time
		targetKnownOnline, targetHealthErr := s.isClusterNodeOnline(targetNodeID)
		if targetHealthErr == nil && !targetKnownOnline {
			if barrierErr := s.waitForPreviousOwnerLeaseExpiry(ctx, policy, targetNodeID, nextEpoch); barrierErr != nil {
				return fmt.Errorf("%w: rollback_target_lease_barrier_failed: %v", errReplicationRollbackPending, barrierErr)
			}
			cutoff := s.now().UTC().Add(-replicationLeaseExpirySafetyMargin)
			targetLeaseExpiresAtOrBefore = &cutoff
		} else if demoteErr := demoteOnNode(targetNodeID, nextEpoch); demoteErr != nil {
			if targetHealthErr != nil {
				demoteErr = fmt.Errorf("target_health_unknown: %v; target_demote_failed: %w", targetHealthErr, demoteErr)
			}
			return fmt.Errorf("%w: rollback_target_fence_unconfirmed: %v", errReplicationRollbackPending, demoteErr)
		}

		rollbackSourceNodeID := strings.TrimSpace(policy.SourceNodeID)
		if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
			rollbackSourceNodeID = rollbackOwner
		} else if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned {
			originalSource := strings.TrimSpace(transition.OriginalSourceNodeID)
			if originalSource == "" {
				originalSource = previousSourceNodeID
			}
			if originalSource != "" {
				rollbackSourceNodeID = originalSource
			}
		}
		var rollbackSourceUpdate *string
		if rollbackSourceNodeID != strings.TrimSpace(policy.SourceNodeID) {
			rollbackSourceUpdate = &rollbackSourceNodeID
		}
		rollbackTargets := rotatedReplicationPolicyTargets(policy, targetNodeID, rollbackOwner, rollbackEpoch)
		rollbackNow := s.now().UTC()
		rollbackLease := clusterModels.ReplicationLease{
			PolicyID:    policy.ID,
			GuestType:   policy.GuestType,
			GuestID:     policy.GuestID,
			OwnerNodeID: rollbackOwner,
			OwnerEpoch:  rollbackEpoch,
			ExpiresAt:   rollbackNow.Add(replicationLeaseTTL),
			Version:     uint64(rollbackNow.UnixNano()),
			LastReason:  reason + "_rollback",
			LastActor:   s.Cluster.LocalNodeID(),
		}
		transition.State = clusterModels.ReplicationTransitionStateRollingBack
		transition.Reason = reason + "_rollback"
		transition.SourceNodeID = targetNodeID
		transition.TargetNodeID = rollbackOwner
		transition.OwnerEpoch = rollbackEpoch
		transition.CompletedAt = nil
		transition.Error = "rollback_in_progress"
		if err := s.Cluster.CommitReplicationOwnershipTransition(
			clusterModels.ReplicationOwnershipTransitionPayload{
				PolicyID:                       policy.ID,
				ExpectedActiveNodeID:           targetNodeID,
				ExpectedOwnerEpoch:             nextEpoch,
				ExpectedTransitionRunID:        transition.RunID,
				PreviousLeaseExpiresAtOrBefore: targetLeaseExpiresAtOrBefore,
				ActiveNodeID:                   rollbackOwner,
				SourceNodeID:                   rollbackSourceUpdate,
				OwnerEpoch:                     rollbackEpoch,
				ReplaceTargets:                 true,
				Targets:                        rollbackTargets,
				Lease:                          rollbackLease,
				Transition:                     transition,
				ProtectionState:                clusterModels.ReplicationProtectionStateDegraded,
			},
			false,
		); err != nil {
			return err
		}

		policy.SourceNodeID = rollbackSourceNodeID
		policy.ActiveNodeID = rollbackOwner
		policy.OwnerEpoch = rollbackEpoch
		policy.Targets = rollbackTargets
		policy.ProtectionState = clusterModels.ReplicationProtectionStateDegraded
		policy.TransitionState = transition.State
		policy.TransitionReason = transition.Reason
		policy.TransitionSourceNodeID = transition.SourceNodeID
		policy.TransitionTargetNodeID = transition.TargetNodeID
		policy.TransitionOwnerEpoch = rollbackEpoch
		if err := activateOnNode(rollbackOwner, rollbackEpoch); err != nil {
			return fmt.Errorf("%w: %v", errReplicationRollbackPending, err)
		}
		return nil
	}
	markTransitionFailed := func(transitionErr error) error {
		now := s.now().UTC()
		transition.State = clusterModels.ReplicationTransitionStateFailed
		transition.CompletedAt = &now
		if transitionErr != nil {
			transition.Error = transitionErr.Error()
		} else {
			transition.Error = "transition_failed"
		}
		if persistErr := persistTransition(); persistErr != nil {
			return appendStepError(transitionErr, "transition_failure_checkpoint_failed", persistErr)
		}
		policy.TransitionState = clusterModels.ReplicationTransitionStateFailed
		policy.TransitionCompletedAt = &now
		policy.TransitionError = transition.Error
		restoreState := replicationProtectionStateFromTargets(policy, now)
		if replicationPolicyOwnerEpoch(policy) != currentEpoch {
			restoreState = clusterModels.ReplicationProtectionStateDegraded
		}
		if restoreState == clusterModels.ReplicationProtectionStateUnprotected {
			restoreState = clusterModels.ReplicationProtectionStateDegraded
		}
		if stateErr := s.Cluster.UpdateReplicationPolicyProtectionState(
			policy.ID,
			replicationPolicyOwnerEpoch(policy),
			restoreState,
			false,
		); stateErr != nil {
			return appendStepError(transitionErr, "transition_protection_state_restore_failed", stateErr)
		}
		policy.ProtectionState = restoreState
		return transitionErr
	}
	recoverPreviousOwnerThenFail := func(transitionErr error, eventMessage string) error {
		effectiveErr := recoverPreviousOwnerIfDemoted(transitionErr)
		if errors.Is(effectiveErr, errReplicationPreviousOwnerRecoveryUnconfirmed) {
			updateTransitionEvent(replicationEventStatusDemoting, eventMessage+"_recovery_pending", effectiveErr, false)
			return effectiveErr
		}
		effectiveErr = markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, eventMessage, effectiveErr, true)
		return effectiveErr
	}
	recoverAfterCommittedCutover := func(transitionErr error, eventMessage string) error {
		previousOwnerOnline, previousOwnerOnlineErr := s.isClusterNodeOnline(previousOwner)
		if previousOwnerOnlineErr != nil || !previousOwnerOnline {
			pendingErr := transitionErr
			if previousOwnerOnlineErr != nil {
				pendingErr = appendStepError(pendingErr, "previous_owner_health_unknown", previousOwnerOnlineErr)
			} else {
				pendingErr = appendStepError(pendingErr, "previous_owner_offline", errReplicationRollbackPending)
			}
			updateTransitionEvent(replicationEventStatusPromoting, eventMessage+"_recovery_pending", pendingErr, false)
			return pendingErr
		}
		if rollbackErr := rollbackToPreviousOwner(); rollbackErr != nil {
			pendingErr := appendStepError(transitionErr, "rollback_pending_recovery", rollbackErr)
			updateTransitionEvent(replicationEventStatusPromoting, eventMessage+"_recovery_pending", pendingErr, false)
			return pendingErr
		}
		effectiveErr := fmt.Errorf("%v; rollback_succeeded", transitionErr)
		effectiveErr = markTransitionFailed(effectiveErr)
		updateTransitionEvent(replicationEventStatusFailed, eventMessage, effectiveErr, true)
		return effectiveErr
	}

	if err := s.Cluster.BeginReplicationPolicyTransition(
		clusterModels.ReplicationPolicyTransitionBegin{
			PolicyID:           policy.ID,
			ExpectedOwnerEpoch: currentEpoch,
			Transition:         transition,
			ProtectionState:    clusterModels.ReplicationProtectionStateSuspended,
		},
		false,
	); err != nil {
		completedAt := s.now().UTC()
		failedEvent, findErr := s.findReplicationTransitionEvent(
			policy.ID,
			transitionRunID,
			&eventStartedAt,
			&completedAt,
			previousOwner,
			targetNodeID,
		)
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			failedEvent = &clusterModels.ReplicationEvent{
				PolicyID:        &policy.ID,
				TransitionRunID: transitionRunID,
				EventType:       "failover",
				SourceNodeID:    previousOwner,
				TargetNodeID:    targetNodeID,
				GuestType:       policy.GuestType,
				GuestID:         policy.GuestID,
				StartedAt:       eventStartedAt,
			}
		} else if findErr != nil {
			logger.L.Warn().Err(findErr).
				Uint("policy_id", policy.ID).
				Msg("replication_rejected_transition_event_lookup_failed")
		}
		if failedEvent != nil {
			failedEvent.TransitionRunID = transitionRunID
			failedEvent.Status = replicationEventStatusFailed
			failedEvent.Message = reason + "_transition_checkpoint_failed"
			failedEvent.Error = err.Error()
			failedEvent.CompletedAt = &completedAt
			if eventID, eventErr := s.Cluster.CreateOrUpdateReplicationEvent(*failedEvent, false); eventErr != nil {
				logger.L.Warn().Err(eventErr).
					Uint("policy_id", policy.ID).
					Msg("replication_rejected_transition_event_create_failed")
			} else if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "replication_policy_failover", policy.ID, "failed", err.Error(), map[string]any{
					"eventId": eventID,
					"status":  "failed",
					"error":   err.Error(),
				})
			}
		}
		return err
	}
	policy.ProtectionState = clusterModels.ReplicationProtectionStateSuspended
	policy.TransitionState = transition.State
	policy.TransitionRunID = transition.RunID
	policy.TransitionReason = transition.Reason
	policy.TransitionSourceNodeID = transition.SourceNodeID
	policy.TransitionTargetNodeID = transition.TargetNodeID
	policy.TransitionOwnerEpoch = transition.OwnerEpoch
	policy.TransitionRequestedAt = transition.RequestedAt
	policy.TransitionAllowUnsafe = transition.AllowUnsafe
	policy.TransitionMovePinnedSource = transition.MovePinnedSource
	policy.TransitionTriggerValidationRun = transition.TriggerValidationRun
	policy.TransitionOriginalSourceNodeID = transition.OriginalSourceNodeID
	policy.TransitionGenerationID = transition.GenerationID
	policy.TransitionGenerationOwnerEpoch = transition.GenerationOwnerEpoch
	policy.TransitionGenerationManifest = transition.GenerationManifest
	policy.TransitionGenerationRootCount = transition.GenerationRootCount
	ensureTransitionEvent()

	if transition.OriginalRunning == nil {
		// Unknown runtime state must never be interpreted as permission to
		// start a previously stopped workload. Safe transitions require an
		// exact sample; force transitions sample when the owner is reachable
		// and otherwise promote in the stopped state.
		originalRunning := false
		ownerReachable := requireDemoteAck
		if !ownerReachable {
			if online, onlineErr := s.isClusterNodeOnline(previousOwner); onlineErr == nil {
				ownerReachable = online
			}
		}
		if ownerReachable {
			var stateErr error
			if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
				originalRunning, stateErr = s.ReplicationPolicyRuntimeState(
					ctx,
					policy.ID,
					currentEpoch,
					transition.RunID,
				)
			} else {
				originalRunning, stateErr = s.forwardReplicationPolicyRuntimeState(
					previousOwner,
					policy.ID,
					currentEpoch,
					transition.RunID,
				)
			}
			if stateErr != nil && requireDemoteAck {
				stateErr = markTransitionFailed(stateErr)
				updateTransitionEvent(replicationEventStatusFailed, reason+"_runtime_state_failed", stateErr, true)
				return stateErr
			}
			if stateErr != nil {
				logger.L.Warn().Err(stateErr).
					Uint("policy_id", policy.ID).
					Str("transition_run_id", transition.RunID).
					Msg("force_failover_runtime_state_unknown_promoting_stopped")
				originalRunning = false
			}
		}
		transition.OriginalRunning = &originalRunning
		policy.TransitionOriginalRunning = &originalRunning
		if err := persistTransition(); err != nil {
			err = markTransitionFailed(err)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_runtime_state_checkpoint_failed", err, true)
			return err
		}
	}

	if requireDemoteAck {
		// Catch-up can legitimately run far longer than the normal lease TTL.
		// Hold the current epoch through the planned transition so rollback
		// remains possible. Standard guest mutations are blocked by the
		// persisted in-progress transition state, not by allowing this lease
		// to lapse mid-transfer.
		if holdErr := s.renewReplicationTransitionLease(policy, previousOwner, currentEpoch, reason); holdErr != nil {
			holdErr = markTransitionFailed(holdErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_lease_hold_failed", holdErr, true)
			return holdErr
		}

		if transition.DemotedAt == nil {
			updateTransitionEvent(replicationEventStatusDemoting, reason+"_demote_requested", nil, false)
			var demoteErr error
			if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
				demoteErr = s.DemoteReplicationPolicyForTransition(
					ctx,
					policy.ID,
					currentEpoch,
					transition.RunID,
				)
			} else {
				demoteErr = s.forwardDemoteReplicationPolicy(
					previousOwner,
					policy.ID,
					currentEpoch,
					transition.RunID,
				)
			}
			if demoteErr != nil {
				if strings.Contains(strings.ToLower(demoteErr.Error()), "workload_operation_conflict") {
					updateTransitionEvent(replicationEventStatusDemoting, reason+"_waiting_for_replication_drain", demoteErr, false)
					return demoteErr
				}
				// A stopped source with a lost HTTP acknowledgement is a successful
				// demotion, not a terminal transition failure. Query through the
				// same exact-run authorization before deciding. Unknown state stays
				// pending so recovery can retry the idempotent demote.
				var sourceRunning bool
				var stateErr error
				if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
					sourceRunning, stateErr = s.ReplicationPolicyRuntimeState(
						ctx,
						policy.ID,
						currentEpoch,
						transition.RunID,
					)
				} else {
					sourceRunning, stateErr = s.forwardReplicationPolicyRuntimeState(
						previousOwner,
						policy.ID,
						currentEpoch,
						transition.RunID,
					)
				}
				if stateErr != nil {
					ambiguousErr := appendReplicationStepError(demoteErr, "demote_outcome_probe_failed", stateErr)
					updateTransitionEvent(replicationEventStatusDemoting, reason+"_demote_reconciliation_pending", ambiguousErr, false)
					return ambiguousErr
				}
				if sourceRunning {
					demoteErr = markTransitionFailed(demoteErr)
					updateTransitionEvent(replicationEventStatusFailed, reason+"_demote_failed", demoteErr, true)
					return demoteErr
				}
			}
			updateTransitionEvent(replicationEventStatusDemoting, reason+"_demote_ack", nil, false)
			demotedAt := s.now().UTC()
			transition.DemotedAt = &demotedAt
			transition.Error = ""
			if err := persistTransition(); err != nil {
				return recoverPreviousOwnerThenFail(err, reason+"_transition_checkpoint_failed")
			}
		}

		if transition.CatchupAt == nil {
			updateTransitionEvent(replicationEventStatusDemoting, reason+"_catchup_requested", nil, false)
			transition.State = clusterModels.ReplicationTransitionStateCatchup
			transition.Error = ""
			if err := persistTransition(); err != nil {
				return recoverPreviousOwnerThenFail(err, reason+"_transition_checkpoint_failed")
			}

			catchupErr := s.withReplicationTransitionLeaseKeeper(
				ctx,
				policy,
				previousOwner,
				currentEpoch,
				reason,
				func(operationCtx context.Context) error {
					if strings.TrimSpace(previousOwner) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
						return s.CatchupReplicationPolicyToNodeForTransition(
							operationCtx,
							policy.ID,
							targetNodeID,
							currentEpoch,
							transition.RunID,
							transition.RunID,
						)
					}
					return s.forwardCatchupReplicationPolicy(
						previousOwner,
						policy.ID,
						targetNodeID,
						currentEpoch,
						transition.RunID,
						transition.RunID,
					)
				},
			)
			if catchupErr != nil {
				return recoverPreviousOwnerThenFail(catchupErr, reason+"_catchup_failed")
			}
			latestPolicy, evidenceErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
			if evidenceErr == nil {
				evidenceErr = bindReplicationTransitionGenerationEvidence(
					latestPolicy,
					targetNodeID,
					&transition,
					true,
				)
			}
			if evidenceErr != nil {
				return recoverPreviousOwnerThenFail(evidenceErr, reason+"_catchup_evidence_failed")
			}
			policy.Targets = latestPolicy.Targets
			updateTransitionEvent(replicationEventStatusDemoting, reason+"_catchup_synced", nil, false)
			catchupAt := s.now().UTC()
			transition.CatchupAt = &catchupAt
			transition.Error = ""
			if err := persistTransition(); err != nil {
				return recoverPreviousOwnerThenFail(err, reason+"_transition_checkpoint_failed")
			}
			policy.TransitionGenerationID = transition.GenerationID
			policy.TransitionGenerationOwnerEpoch = transition.GenerationOwnerEpoch
			policy.TransitionGenerationManifest = transition.GenerationManifest
			policy.TransitionGenerationRootCount = transition.GenerationRootCount
		}
	} else {
		if !options.AllowUnsafe {
			transitionErr := fmt.Errorf("unsafe_failover_blocked_without_demote_and_catchup")
			transitionErr = markTransitionFailed(transitionErr)
			updateTransitionEvent(replicationEventStatusFailed, reason+"_blocked_without_demote_or_catchup", transitionErr, true)
			return transitionErr
		}
		updateTransitionEvent(replicationEventStatusDemoting, reason+"_waiting_for_previous_owner_lease_expiry", nil, false)
		if barrierErr := s.waitForPreviousOwnerLeaseExpiry(ctx, policy, previousOwner, currentEpoch); barrierErr != nil {
			// Renewal is already disabled for this unsafe run. A cancellation or
			// leadership/read error cannot prove whether the watchdog fenced the
			// old owner, so leave the transition recoverable rather than terminal.
			updateTransitionEvent(replicationEventStatusDemoting, reason+"_lease_expiry_barrier_pending", barrierErr, false)
			return barrierErr
		}
		if transition.DemotedAt == nil {
			fencedAt := s.now().UTC()
			transition.DemotedAt = &fencedAt
			transition.Error = ""
			if checkpointErr := persistTransition(); checkpointErr != nil {
				updateTransitionEvent(replicationEventStatusDemoting, reason+"_fence_checkpoint_pending", checkpointErr, false)
				return checkpointErr
			}
			policy.TransitionDemotedAt = &fencedAt
		}
		updateTransitionEvent(replicationEventStatusDemoting, reason+"_previous_owner_lease_expired", nil, false)
		latestPolicy, latestErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
		if latestErr != nil {
			return recoverPreviousOwnerThenFail(latestErr, reason+"_target_generation_revalidation_failed")
		}
		if replicationPolicyOwnerNode(latestPolicy) != previousOwner ||
			replicationPolicyOwnerEpoch(latestPolicy) != currentEpoch {
			return recoverPreviousOwnerThenFail(
				fmt.Errorf("replication_ownership_changed_before_force_cutover"),
				reason+"_target_generation_revalidation_failed",
			)
		}
		if latestErr = s.validateUnsafeFailoverTargetGeneration(latestPolicy, targetNodeID); latestErr != nil {
			return recoverPreviousOwnerThenFail(latestErr, reason+"_target_generation_revalidation_failed")
		}
		if latestErr = bindReplicationTransitionGenerationEvidence(
			latestPolicy,
			targetNodeID,
			&transition,
			false,
		); latestErr != nil {
			return recoverPreviousOwnerThenFail(latestErr, reason+"_target_generation_changed")
		}
	}

	targetOnline, targetOnlineErr := s.isClusterNodeOnline(targetNodeID)
	if targetOnlineErr != nil {
		return recoverPreviousOwnerThenFail(targetOnlineErr, reason+"_target_health_check_failed")
	}
	if !targetOnline {
		return recoverPreviousOwnerThenFail(
			fmt.Errorf("replication_target_node_offline"),
			reason+"_target_offline_before_promote",
		)
	}

	newSourceNodeID := strings.TrimSpace(policy.SourceNodeID)
	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive ||
		(strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned && options.MovePinnedSource) {
		newSourceNodeID = targetNodeID
	}
	var sourceNodeUpdate *string
	if newSourceNodeID != strings.TrimSpace(policy.SourceNodeID) {
		sourceNodeUpdate = &newSourceNodeID
	}

	cutoverNow := s.now().UTC()
	var previousLeaseExpiresAtOrBefore *time.Time
	if options.AllowUnsafe {
		cutoff := cutoverNow.Add(-replicationLeaseExpirySafetyMargin)
		previousLeaseExpiresAtOrBefore = &cutoff
	}
	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: targetNodeID,
		OwnerEpoch:  nextEpoch,
		ExpiresAt:   cutoverNow.Add(replicationLeaseTTL),
		Version:     uint64(cutoverNow.UnixNano()),
		LastReason:  reason,
		LastActor:   s.Cluster.LocalNodeID(),
	}
	updateTransitionEvent(replicationEventStatusPromoting, reason+"_promoting", nil, false)
	transition.State = clusterModels.ReplicationTransitionStatePromoting
	transition.OwnerEpoch = nextEpoch
	transition.PromotedAt = nil
	transition.CompletedAt = nil
	transition.Error = ""
	commitErr := s.Cluster.CommitReplicationOwnershipTransition(
		clusterModels.ReplicationOwnershipTransitionPayload{
			PolicyID:                       policy.ID,
			ExpectedActiveNodeID:           previousOwner,
			ExpectedOwnerEpoch:             currentEpoch,
			ExpectedTransitionRunID:        transition.RunID,
			PreviousLeaseExpiresAtOrBefore: previousLeaseExpiresAtOrBefore,
			ActiveNodeID:                   targetNodeID,
			SourceNodeID:                   sourceNodeUpdate,
			OwnerEpoch:                     nextEpoch,
			ReplaceTargets:                 true,
			Targets:                        rotatedTargets,
			Lease:                          lease,
			Transition:                     transition,
			ProtectionState:                clusterModels.ReplicationProtectionStateDegraded,
		},
		false,
	)
	if commitErr != nil {
		// Raft can report leadership loss after the command was accepted. Its
		// outcome is therefore not inferred from the Future error. Re-read the
		// atomically committed policy+lease pair before deciding whether the
		// previous owner can ever be reactivated.
		latestPolicy, policyLookupErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
		var latestLease *clusterModels.ReplicationLease
		leaseLookupErr := error(nil)
		if policyLookupErr == nil {
			latestLease, leaseLookupErr = s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
			if errors.Is(leaseLookupErr, gorm.ErrRecordNotFound) {
				leaseLookupErr = nil
				latestLease = nil
			}
		}
		disposition := replicationOwnershipCommitAmbiguous
		if policyLookupErr == nil && leaseLookupErr == nil {
			disposition = classifyReplicationOwnershipCommit(
				latestPolicy,
				latestLease,
				previousOwner,
				targetNodeID,
				currentEpoch,
				nextEpoch,
				transition.RunID,
			)
		}
		switch disposition {
		case replicationOwnershipCommitApplied:
			// Continue promotion from the durable target ownership. A retry of
			// activation is exact-run and idempotent.
			policy.SourceNodeID = latestPolicy.SourceNodeID
			policy.ActiveNodeID = latestPolicy.ActiveNodeID
			policy.OwnerEpoch = latestPolicy.OwnerEpoch
			policy.Targets = latestPolicy.Targets
			policy.ProtectionState = latestPolicy.ProtectionState
			policy.TransitionState = latestPolicy.TransitionState
			policy.TransitionOwnerEpoch = latestPolicy.TransitionOwnerEpoch
		case replicationOwnershipCommitNotApplied:
			return recoverPreviousOwnerThenFail(commitErr, reason+"_ownership_commit_failed")
		default:
			ambiguousErr := fmt.Errorf("replication_ownership_commit_outcome_ambiguous: %w", commitErr)
			if policyLookupErr != nil {
				ambiguousErr = appendReplicationStepError(ambiguousErr, "policy_reconciliation_failed", policyLookupErr)
			}
			if leaseLookupErr != nil {
				ambiguousErr = appendReplicationStepError(ambiguousErr, "lease_reconciliation_failed", leaseLookupErr)
			}
			// Leave the transition pending. Recovery will classify the durable
			// outcome again; speculative old-owner activation is forbidden.
			updateTransitionEvent(replicationEventStatusPromoting, reason+"_ownership_commit_reconciliation_pending", ambiguousErr, false)
			return ambiguousErr
		}
	}

	if commitErr == nil {
		policy.SourceNodeID = newSourceNodeID
		policy.ActiveNodeID = targetNodeID
		policy.OwnerEpoch = nextEpoch
		policy.Targets = rotatedTargets
		policy.ProtectionState = clusterModels.ReplicationProtectionStateDegraded
		policy.TransitionState = transition.State
		policy.TransitionOwnerEpoch = nextEpoch
	}
	targetOnline, targetOnlineErr = s.isClusterNodeOnline(targetNodeID)
	if targetOnlineErr != nil {
		return recoverAfterCommittedCutover(targetOnlineErr, reason+"_target_health_check_failed")
	}
	if !targetOnline {
		return recoverAfterCommittedCutover(
			fmt.Errorf("replication_target_node_offline"),
			reason+"_target_offline_during_promote",
		)
	}

	activateErr := activateOnNode(targetNodeID, nextEpoch)

	if activateErr != nil {
		return recoverAfterCommittedCutover(activateErr, reason+"_promoting_failed")
	}

	if err := s.rebindReplicationGuestBackupJobRunners(policy, targetNodeID); err != nil {
		logger.L.Warn().Err(err).
			Uint("policy_id", policy.ID).
			Str("target_node_id", targetNodeID).
			Msg("replication_backup_job_runner_rebind_pending")
		updateTransitionEvent(replicationEventStatusPromoting, reason+"_backup_runner_rebind_pending", err, false)
	}

	now := s.now().UTC()
	transition.State = clusterModels.ReplicationTransitionStateCompleted
	transition.PromotedAt = &now
	transition.CompletedAt = &now
	transition.OwnerEpoch = nextEpoch
	transition.Error = ""
	if err := persistTransition(); err != nil {
		updateTransitionEvent(replicationEventStatusFailed, reason+"_transition_checkpoint_failed", err, true)
		return err
	}
	policy.TransitionState = clusterModels.ReplicationTransitionStateCompleted
	policy.TransitionPromotedAt = &now
	policy.TransitionCompletedAt = &now
	policy.TransitionError = ""
	updateTransitionEvent(replicationEventStatusActive, reason+"_active", nil, true)

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

func (s *Service) forwardActivateReplicationPolicy(
	nodeID string,
	policyID uint,
	ownerEpoch uint64,
	transitionRunID string,
	desiredRunning *bool,
) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "activate", map[string]any{
		"policyId":        policyID,
		"ownerEpoch":      ownerEpoch,
		"transitionRunId": strings.TrimSpace(transitionRunID),
		"desiredRunning":  desiredRunning,
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardDemoteReplicationPolicy(nodeID string, policyID uint, ownerEpoch uint64, transitionRunID string) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "demote", map[string]any{
		"policyId":        policyID,
		"ownerEpoch":      ownerEpoch,
		"transitionRunId": strings.TrimSpace(transitionRunID),
	}, replicationControlDefaultTimeout)
}

func (s *Service) forwardCatchupReplicationPolicy(
	nodeID string,
	policyID uint,
	targetNodeID string,
	ownerEpoch uint64,
	transitionRunID string,
	generationID string,
) error {
	return s.forwardReplicationPolicyControlWithRetry(nodeID, "catchup", map[string]any{
		"policyId":        policyID,
		"targetNodeId":    targetNodeID,
		"ownerEpoch":      ownerEpoch,
		"transitionRunId": strings.TrimSpace(transitionRunID),
		"generationId":    strings.TrimSpace(generationID),
	}, replicationControlCatchupTimeout)
}

func (s *Service) forwardReplicationPolicyRuntimeState(
	nodeID string,
	policyID uint,
	ownerEpoch uint64,
	transitionRunID string,
) (bool, error) {
	body, err := s.forwardReplicationPolicyControlRead(
		nodeID,
		"replication-runtime-state",
		map[string]any{
			"policyId":        policyID,
			"ownerEpoch":      ownerEpoch,
			"transitionRunId": strings.TrimSpace(transitionRunID),
		},
		replicationControlDefaultTimeout,
	)
	if err != nil {
		return false, err
	}
	var response struct {
		Data struct {
			Running bool `json:"running"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false, fmt.Errorf("decode_replication_runtime_state_failed: %w", err)
	}
	return response.Data.Running, nil
}

func (s *Service) forwardCleanupReplicationPolicyDelete(
	nodeID string,
	policyID uint,
	expectedOwnerEpoch uint64,
) error {
	return s.forwardReplicationPolicyControl(nodeID, "cleanup-policy-delete", map[string]any{
		"policyId":           policyID,
		"expectedOwnerEpoch": expectedOwnerEpoch,
	}, replicationControlDefaultTimeout)
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
	req.Recursive = job.Recursive
	req.CronExpr = strings.TrimSpace(job.CronExpr)
	return req
}

func (s *Service) rebindReplicationGuestBackupJobRunners(policy *clusterModels.ReplicationPolicy, runnerNodeID string) error {
	if s == nil || policy == nil || policy.ID == 0 {
		return nil
	}
	return s.rebindGuestBackupJobRunners(policy.GuestType, policy.GuestID, runnerNodeID)
}

func (s *Service) rebindGuestBackupJobRunners(guestType string, guestID uint, runnerNodeID string) error {
	if s == nil || s.Cluster == nil || guestID == 0 {
		return nil
	}

	runnerNodeID = strings.TrimSpace(runnerNodeID)
	if runnerNodeID == "" {
		return nil
	}

	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if guestType == "" {
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
		if jobGuestType != guestType || jobGuestID != guestID {
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
			Uint("job_id", job.ID).
			Str("guest_type", guestType).
			Uint("guest_id", guestID).
			Str("runner_node_id", runnerNodeID).
			Msg("guest_backup_job_runner_rebound")
	}

	if len(updateErrs) > 0 {
		return fmt.Errorf("backup_job_runner_rebind_partial_failure: %s", strings.Join(updateErrs, "; "))
	}
	return nil
}

func (s *Service) MigrateGuestOwnership(
	ctx context.Context,
	guestType string,
	guestID uint,
	newOwnerNodeID string,
	operationTokens ...string,
) error {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	operationToken := ""
	if len(operationTokens) == 1 {
		operationToken = strings.TrimSpace(operationTokens[0])
	}
	if guestType == "" || guestID == 0 || newOwnerNodeID == "" || operationToken == "" {
		return fmt.Errorf("invalid_migrate_ownership_input")
	}
	if s.Cluster == nil || s.Cluster.Raft == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	if s.Cluster.Raft.State() != raft.Leader {
		_, leaderID := s.Cluster.Raft.LeaderWithID()
		leaderNodeID := strings.TrimSpace(string(leaderID))
		if leaderNodeID == "" {
			return fmt.Errorf("leader_not_available")
		}
		return s.forwardReplicationPolicyControlWithRetry(leaderNodeID, "replication-reassign-owner", map[string]any{
			"guest_type":        guestType,
			"guest_id":          guestID,
			"new_owner_node_id": newOwnerNodeID,
			"operation_token":   operationToken,
		}, replicationControlDefaultTimeout)
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.
		Preload("Targets").
		Where("guest_type = ? AND guest_id = ?", guestType, guestID).
		Find(&policies).Error; err != nil {
		return fmt.Errorf("lookup_replication_policies_failed: %w", err)
	}

	errs := make([]string, 0)
	for i := range policies {
		var err error
		if policies[i].Enabled {
			err = fmt.Errorf("replication_policy_must_be_disabled_before_migration")
		} else {
			err = s.reassignDisabledReplicationPolicyOwner(&policies[i], newOwnerNodeID, operationToken)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("policy_%d: %v", policies[i].ID, err))
		}
	}

	if err := s.rebindGuestBackupJobRunners(guestType, guestID, newOwnerNodeID); err != nil {
		errs = append(errs, fmt.Sprintf("backup_jobs: %v", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("migrate_guest_ownership_partial_failure: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *Service) AcquireGuestMigrationInterlock(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
	taskID uint,
	token string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	targetNodeID = strings.TrimSpace(targetNodeID)
	token = strings.TrimSpace(token)
	if guestType == "" || guestID == 0 || targetNodeID == "" || taskID == 0 || token == "" {
		return fmt.Errorf("invalid_migration_interlock_input")
	}
	if s.Cluster == nil || s.Cluster.Raft == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	ownerNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if ownerNodeID == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}
	payload := clusterModels.ReplicationGuestOperationAcquire{
		GuestType:    guestType,
		GuestID:      guestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		Token:        token,
		OwnerNodeID:  ownerNodeID,
		TargetNodeID: targetNodeID,
		TaskID:       taskID,
	}
	return s.applyGuestMigrationInterlock(ctx, "acquire", payload, clusterModels.ReplicationGuestOperationTransition{})
}

func (s *Service) SealGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, token string) error {
	payload, err := replicationGuestOperationTransition(guestType, guestID, token, "")
	if err != nil {
		return err
	}
	return s.applyGuestMigrationInterlock(ctx, "seal", clusterModels.ReplicationGuestOperationAcquire{}, payload)
}

func (s *Service) WaitGuestMigrationInterlockAcquired(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
	token string,
) error {
	return s.waitGuestMigrationInterlockApplied(
		ctx,
		guestType,
		guestID,
		targetNodeID,
		token,
		clusterModels.ReplicationGuestOperationPreCutover,
		false,
	)
}

func (s *Service) WaitGuestMigrationInterlockApplied(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
	token string,
) error {
	return s.waitGuestMigrationInterlockApplied(
		ctx,
		guestType,
		guestID,
		targetNodeID,
		token,
		clusterModels.ReplicationGuestOperationCutover,
		true,
	)
}

func (s *Service) waitGuestMigrationInterlockApplied(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
	token string,
	expectedState string,
	waitForTarget bool,
) error {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	targetNodeID = strings.TrimSpace(targetNodeID)
	token = strings.TrimSpace(token)
	expectedState = strings.TrimSpace(expectedState)
	if guestType == "" || guestID == 0 || targetNodeID == "" || token == "" || expectedState == "" || s.DB == nil || s.Cluster == nil {
		return fmt.Errorf("invalid_migration_interlock_barrier_input")
	}
	barrierCtx, cancel := context.WithTimeout(ctx, replicationControlDefaultTimeout)
	defer cancel()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	var lastErr error
	for {
		var localOperation clusterModels.ReplicationGuestOperation
		localErr := s.DB.Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			First(&localOperation).Error
		localApplied := localErr == nil &&
			localOperation.Operation == clusterModels.ReplicationGuestOperationMigration &&
			localOperation.State == expectedState &&
			strings.TrimSpace(localOperation.Token) == token &&
			strings.TrimSpace(localOperation.TargetNodeID) == targetNodeID
		if !localApplied {
			lastErr = localErr
			if lastErr == nil {
				lastErr = fmt.Errorf("local_migration_interlock_not_applied")
			}
		} else if !waitForTarget || targetNodeID == localNodeID {
			return nil
		} else {
			_, remoteErr := s.forwardReplicationPolicyControlRead(
				targetNodeID,
				"replication-guest-operation-status",
				map[string]any{
					"guestType": guestType, "guestId": guestID, "operation": clusterModels.ReplicationGuestOperationMigration,
					"state": expectedState, "token": token, "targetNodeId": targetNodeID,
				},
				5*time.Second,
			)
			if remoteErr == nil {
				return nil
			}
			lastErr = remoteErr
		}

		select {
		case <-barrierCtx.Done():
			if lastErr == nil {
				lastErr = barrierCtx.Err()
			}
			return fmt.Errorf("migration_interlock_apply_barrier_timeout: %w", lastErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) acquireReplicationEmergencyRestoreGuard(
	ctx context.Context,
	guestType string,
	guestID uint,
	token string,
) error {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	token = strings.TrimSpace(token)
	if s == nil || s.Cluster == nil || s.Cluster.Raft == nil || guestType == "" || guestID == 0 || token == "" {
		return fmt.Errorf("replication_emergency_restore_guard_unavailable")
	}
	ownerNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if ownerNodeID == "" {
		return fmt.Errorf("replication_local_node_id_unavailable")
	}
	payload := clusterModels.ReplicationGuestOperationAcquire{
		GuestType: guestType, GuestID: guestID,
		Operation: clusterModels.ReplicationGuestOperationEmergencyRestore,
		Token:     token, OwnerNodeID: ownerNodeID,
	}
	return s.applyGuestMigrationInterlock(
		ctx, "acquire", payload, clusterModels.ReplicationGuestOperationTransition{},
	)
}

func (s *Service) waitReplicationEmergencyRestoreGuardApplied(
	ctx context.Context,
	guestType string,
	guestID uint,
	token string,
) error {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	token = strings.TrimSpace(token)
	if s == nil || s.DB == nil || guestType == "" || guestID == 0 || token == "" {
		return fmt.Errorf("replication_emergency_restore_guard_barrier_invalid")
	}
	barrierCtx, cancel := context.WithTimeout(ctx, replicationControlDefaultTimeout)
	defer cancel()
	var lastErr error
	for {
		var operation clusterModels.ReplicationGuestOperation
		err := s.DB.Where("guest_type = ? AND guest_id = ?", guestType, guestID).First(&operation).Error
		if err == nil && operation.Operation == clusterModels.ReplicationGuestOperationEmergencyRestore &&
			operation.State == clusterModels.ReplicationGuestOperationPreCutover &&
			strings.TrimSpace(operation.Token) == token {
			return nil
		}
		lastErr = err
		if lastErr == nil {
			lastErr = fmt.Errorf("replication_emergency_restore_guard_not_applied")
		}
		select {
		case <-barrierCtx.Done():
			return fmt.Errorf("replication_emergency_restore_guard_barrier_timeout: %w", lastErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) releaseReplicationEmergencyRestoreGuard(
	ctx context.Context,
	guestType string,
	guestID uint,
	token string,
) error {
	payload := clusterModels.ReplicationGuestOperationTransition{
		GuestType: strings.ToLower(strings.TrimSpace(guestType)), GuestID: guestID,
		Operation: clusterModels.ReplicationGuestOperationEmergencyRestore,
		Token:     strings.TrimSpace(token),
	}
	return s.applyGuestMigrationInterlock(
		ctx, "abort", clusterModels.ReplicationGuestOperationAcquire{}, payload,
	)
}

func (s *Service) AbortGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, token string) error {
	payload, err := replicationGuestOperationTransition(guestType, guestID, token, "")
	if err != nil {
		return err
	}
	return s.applyGuestMigrationInterlock(ctx, "abort", clusterModels.ReplicationGuestOperationAcquire{}, payload)
}

func (s *Service) CompleteGuestMigrationInterlock(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNodeID string,
	token string,
) error {
	payload, err := replicationGuestOperationTransition(guestType, guestID, token, targetNodeID)
	if err != nil {
		return err
	}
	return s.applyGuestMigrationInterlock(ctx, "complete", clusterModels.ReplicationGuestOperationAcquire{}, payload)
}

func replicationGuestOperationTransition(
	guestType string,
	guestID uint,
	token string,
	targetNodeID string,
) (clusterModels.ReplicationGuestOperationTransition, error) {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	token = strings.TrimSpace(token)
	targetNodeID = strings.TrimSpace(targetNodeID)
	if guestType == "" || guestID == 0 || token == "" {
		return clusterModels.ReplicationGuestOperationTransition{}, fmt.Errorf("invalid_migration_interlock_input")
	}
	return clusterModels.ReplicationGuestOperationTransition{
		GuestType:    guestType,
		GuestID:      guestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		Token:        token,
		TargetNodeID: targetNodeID,
	}, nil
}

func (s *Service) applyGuestMigrationInterlock(
	ctx context.Context,
	action string,
	acquire clusterModels.ReplicationGuestOperationAcquire,
	transition clusterModels.ReplicationGuestOperationTransition,
) error {
	if s.Cluster == nil || s.Cluster.Raft == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	payload := map[string]any{
		"action":       "acquire",
		"guestType":    acquire.GuestType,
		"guestId":      acquire.GuestID,
		"operation":    acquire.Operation,
		"token":        acquire.Token,
		"ownerNodeId":  acquire.OwnerNodeID,
		"targetNodeId": acquire.TargetNodeID,
		"taskId":       acquire.TaskID,
	}
	if action != "acquire" {
		payload = map[string]any{
			"action":       action,
			"guestType":    transition.GuestType,
			"guestId":      transition.GuestID,
			"operation":    transition.Operation,
			"token":        transition.Token,
			"targetNodeId": transition.TargetNodeID,
		}
	}

	var lastErr error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Cluster.Raft.State() == raft.Leader {
			switch action {
			case "acquire":
				lastErr = s.Cluster.AcquireReplicationGuestOperation(acquire, false)
			case "seal":
				lastErr = s.Cluster.SealReplicationGuestOperation(transition, false)
			case "abort":
				lastErr = s.Cluster.AbortReplicationGuestOperation(transition, false)
			case "complete":
				lastErr = s.Cluster.CompleteReplicationGuestOperation(transition, false)
			default:
				return fmt.Errorf("invalid_replication_guest_operation_action")
			}
		} else {
			_, leaderID := s.Cluster.Raft.LeaderWithID()
			leaderNodeID := strings.TrimSpace(string(leaderID))
			if leaderNodeID == "" {
				lastErr = fmt.Errorf("leader_not_available")
			} else {
				lastErr = s.forwardReplicationPolicyControl(
					leaderNodeID,
					"replication-guest-operation",
					payload,
					replicationControlDefaultTimeout,
				)
			}
		}
		if lastErr == nil {
			return nil
		}
		if attempt < replicationControlForwardAttempts-1 {
			time.Sleep(replicationControlForwardBackoff * time.Duration(attempt+1))
		}
	}
	return lastErr
}

func (s *Service) reassignDisabledReplicationPolicyOwner(
	policy *clusterModels.ReplicationPolicy,
	newOwnerNodeID string,
	operationToken string,
) error {
	if policy == nil || policy.ID == 0 || policy.Enabled {
		return fmt.Errorf("invalid_disabled_replication_policy")
	}
	currentOwner := replicationPolicyOwnerNode(policy)
	currentEpoch := replicationPolicyOwnerEpoch(policy)
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	if currentOwner == "" || currentEpoch == 0 || newOwnerNodeID == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if currentOwner == newOwnerNodeID {
		return nil
	}
	if currentEpoch == math.MaxUint64 {
		return fmt.Errorf("replication_policy_owner_epoch_exhausted")
	}
	nextEpoch := currentEpoch + 1
	now := s.now().UTC()
	payload := clusterModels.ReplicationDisabledOwnerReassignment{
		PolicyID:             policy.ID,
		ExpectedActiveNodeID: currentOwner,
		ExpectedOwnerEpoch:   currentEpoch,
		ActiveNodeID:         newOwnerNodeID,
		SourceNodeID:         newOwnerNodeID,
		OwnerEpoch:           nextEpoch,
		Targets:              rotatedReplicationPolicyTargets(policy, currentOwner, newOwnerNodeID, nextEpoch),
		RunID:                fmt.Sprintf("migration-disabled-owner-%d-%s", policy.ID, compactNowToken()),
		OperationToken:       strings.TrimSpace(operationToken),
		OccurredAt:           now,
	}
	if err := s.Cluster.ReassignDisabledReplicationPolicyOwner(payload, false); err != nil {
		latest, lookupErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
		if lookupErr != nil || latest == nil || latest.Enabled ||
			replicationPolicyOwnerNode(latest) != newOwnerNodeID ||
			replicationPolicyOwnerEpoch(latest) != nextEpoch ||
			strings.TrimSpace(latest.SourceNodeID) != newOwnerNodeID ||
			strings.TrimSpace(latest.TransitionRunID) != payload.RunID {
			return fmt.Errorf("disabled_replication_owner_reassignment_ambiguous: %w", err)
		}
	}
	return nil
}

func (s *Service) reassignReplicationPolicyOwner(policy *clusterModels.ReplicationPolicy, newOwnerNodeID string) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}

	currentEpoch := replicationPolicyOwnerEpoch(policy)
	currentOwner := replicationPolicyOwnerNode(policy)
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	if currentOwner == "" || newOwnerNodeID == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if currentOwner == newOwnerNodeID {
		return nil
	}
	if currentEpoch == math.MaxUint64 {
		return fmt.Errorf("replication_policy_owner_epoch_exhausted")
	}
	nextEpoch := currentEpoch + 1
	runID := fmt.Sprintf("migration-owner-%d-%s", policy.ID, compactNowToken())
	now := s.now().UTC()
	transition := clusterModels.ReplicationPolicyTransition{
		State:                clusterModels.ReplicationTransitionStateDemoting,
		RunID:                runID,
		Reason:               "manual_migration_ownership",
		SourceNodeID:         currentOwner,
		TargetNodeID:         newOwnerNodeID,
		OwnerEpoch:           currentEpoch,
		RequestedAt:          &now,
		TriggerValidationRun: true,
		OriginalSourceNodeID: strings.TrimSpace(policy.SourceNodeID),
	}
	if err := s.Cluster.BeginReplicationPolicyTransition(
		clusterModels.ReplicationPolicyTransitionBegin{
			PolicyID:           policy.ID,
			ExpectedOwnerEpoch: currentEpoch,
			Transition:         transition,
			ProtectionState:    clusterModels.ReplicationProtectionStateSuspended,
		},
		false,
	); err != nil {
		return fmt.Errorf("begin_migration_ownership_transition_failed: %w", err)
	}
	policy.ProtectionState = clusterModels.ReplicationProtectionStateSuspended
	policy.TransitionState = transition.State
	policy.TransitionRunID = transition.RunID
	policy.TransitionReason = transition.Reason
	policy.TransitionSourceNodeID = transition.SourceNodeID
	policy.TransitionTargetNodeID = transition.TargetNodeID
	policy.TransitionOwnerEpoch = transition.OwnerEpoch
	policy.TransitionRequestedAt = transition.RequestedAt
	policy.TransitionTriggerValidationRun = transition.TriggerValidationRun
	policy.TransitionOriginalSourceNodeID = transition.OriginalSourceNodeID

	// A migrated guest no longer exists on the old source. Move both
	// follow-active and pinned replication sources to the new owner.
	sourceNode := newOwnerNodeID
	sourceNodeUpdate := &sourceNode
	transition.State = clusterModels.ReplicationTransitionStateCompleted
	transition.OwnerEpoch = nextEpoch
	transition.PromotedAt = &now
	transition.CompletedAt = &now
	leaseVersion, err := s.nextReplicationPolicyLeaseVersion(policy.ID, now)
	if err != nil {
		return s.failPolicyTransition(policy, fmt.Errorf("migration_ownership_lease_version_failed: %w", err))
	}
	rotatedTargets := rotatedReplicationPolicyTargets(policy, currentOwner, newOwnerNodeID, nextEpoch)
	payload := clusterModels.ReplicationOwnershipTransitionPayload{
		PolicyID:                policy.ID,
		ExpectedActiveNodeID:    currentOwner,
		ExpectedOwnerEpoch:      currentEpoch,
		ExpectedTransitionRunID: runID,
		ActiveNodeID:            newOwnerNodeID,
		SourceNodeID:            sourceNodeUpdate,
		OwnerEpoch:              nextEpoch,
		ReplaceTargets:          true,
		Targets:                 rotatedTargets,
		Lease: clusterModels.ReplicationLease{
			PolicyID:    policy.ID,
			GuestType:   policy.GuestType,
			GuestID:     policy.GuestID,
			OwnerNodeID: newOwnerNodeID,
			OwnerEpoch:  nextEpoch,
			ExpiresAt:   now.Add(replicationLeaseTTL),
			Version:     leaseVersion,
			LastReason:  "manual_migration",
			LastActor:   s.Cluster.LocalNodeID(),
		},
		Transition:      transition,
		ProtectionState: clusterModels.ReplicationProtectionStateDegraded,
	}
	commitErr := s.Cluster.CommitReplicationOwnershipTransition(payload, false)
	if commitErr != nil {
		latest, lookupErr := s.Cluster.GetReplicationPolicyByID(policy.ID)
		latestLease, leaseErr := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
		if lookupErr != nil || leaseErr != nil ||
			classifyReplicationOwnershipCommit(
				latest,
				latestLease,
				currentOwner,
				newOwnerNodeID,
				currentEpoch,
				nextEpoch,
				runID,
			) != replicationOwnershipCommitApplied {
			return fmt.Errorf("migration_ownership_commit_outcome_ambiguous: %w", commitErr)
		}
	}

	return nil
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
	forwardTimeout, err := replicationValidationForwardTimeout(ctx)
	if err != nil {
		return err
	}
	return s.forwardReplicationPolicyControl(targetNodeID, "run", map[string]any{
		"policyId": policyID,
	}, forwardTimeout)
}

func replicationValidationForwardTimeout(ctx context.Context) (time.Duration, error) {
	timeout := replicationControlDefaultTimeout
	if ctx == nil {
		return timeout, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return timeout, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	if remaining < timeout {
		timeout = remaining
	}
	return timeout, nil
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
	_, err := s.forwardReplicationPolicyControlRead(nodeID, action, payload, timeout)
	return err
}

func (s *Service) forwardReplicationPolicyControlRead(
	nodeID string,
	action string,
	payload map[string]any,
	timeout time.Duration,
) ([]byte, error) {
	targetAPI, err := s.resolveReplicationNodeAPI(nodeID)
	if err != nil {
		return nil, err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.Cluster.AuthService.CreateInternalClusterJWT(hostname, "")
	if err != nil {
		return nil, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/intra-cluster/%s", targetAPI, strings.TrimSpace(action))
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal_replication_control_payload_failed: %w", err)
	}
	if timeout <= 0 {
		timeout = replicationControlDefaultTimeout
	}

	responseBody, statusCode, err := utils.HTTPPostJSONWithTimeout(url, body, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}, timeout)
	if err != nil {
		return nil, fmt.Errorf("replication_control_%s_failed_status_%d: %w", strings.TrimSpace(action), statusCode, err)
	}
	return responseBody, nil
}

// CleanupReplicationPolicyDeleteBestEffort retains its historical name, but
// cleanup is now an all-node acknowledgement barrier: any failed or missing
// acknowledgement leaves the durable policy in deleting for a safe retry.
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
	if err := validateReplicationPolicyDeleteAuthority(policy, policy.OwnerEpoch); err != nil {
		return err
	}
	expectedOwnerEpoch := policy.OwnerEpoch

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
	clusterNodes, nodesErr := s.Cluster.Nodes()
	if nodesErr != nil {
		return fmt.Errorf("replication_policy_delete_cluster_membership_failed: %w", nodesErr)
	}
	for _, node := range clusterNodes {
		addNode(node.NodeUUID)
	}
	if localNodeID != "" {
		addNode(localNodeID)
	}

	sort.Strings(nodes)
	type cleanupResult struct {
		nodeID string
		err    error
	}
	results := make(chan cleanupResult, len(nodes))
	for _, nodeID := range nodes {
		nodeID := nodeID
		go func() {
			var cleanupErr error
			if localNodeID != "" && nodeID == localNodeID {
				cleanupErr = s.CleanupReplicationPolicyDeleteLocalBestEffort(
					ctx,
					policyID,
					expectedOwnerEpoch,
				)
			} else {
				cleanupErr = s.forwardCleanupReplicationPolicyDelete(
					nodeID,
					policyID,
					expectedOwnerEpoch,
				)
			}
			results <- cleanupResult{nodeID: nodeID, err: cleanupErr}
		}()
	}

	cleanupErrs := make([]string, 0)
	for range nodes {
		result := <-results
		if result.err == nil {
			continue
		}

		logger.L.Warn().
			Uint("policy_id", policyID).
			Uint64("owner_epoch", expectedOwnerEpoch).
			Str("node_id", result.nodeID).
			Err(result.err).
			Msg("replication_policy_delete_cleanup_node_failed")
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%s: %v", result.nodeID, result.err))
	}

	if len(cleanupErrs) > 0 {
		sort.Strings(cleanupErrs)
		return fmt.Errorf("replication_policy_delete_cleanup_partial_failure: %s", strings.Join(cleanupErrs, "; "))
	}

	return nil
}

func validateReplicationPolicyDeleteAuthority(
	policy *clusterModels.ReplicationPolicy,
	expectedOwnerEpoch uint64,
) error {
	if policy == nil || policy.ID == 0 || expectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_delete_authority_invalid")
	}
	if policy.OwnerEpoch != expectedOwnerEpoch {
		return fmt.Errorf("replication_policy_delete_authority_epoch_mismatch")
	}
	if !strings.EqualFold(
		strings.TrimSpace(policy.ProtectionState),
		clusterModels.ReplicationProtectionStateDeleting,
	) {
		return fmt.Errorf("replication_policy_not_deleting")
	}
	if transitionStateInProgress(policy.TransitionState) {
		return fmt.Errorf("replication_policy_delete_authority_transition_in_progress")
	}
	if replicationPolicyOwnerNode(policy) == "" {
		return fmt.Errorf("replication_policy_delete_authority_owner_missing")
	}
	return nil
}

func (s *Service) cleanupLocalReplicationDatasetsByProvenance(
	ctx context.Context,
	policyID uint,
	protectedRoots []string,
) error {
	filesystems, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return err
	}
	volumes, volumeErr := s.listLocalVolumeDatasets(ctx)
	if volumeErr != nil {
		return volumeErr
	}
	candidates := make([]string, 0)
	seen := make(map[string]struct{})
	for _, candidate := range append(filesystems, volumes...) {
		candidate = normalizeDatasetPath(candidate)
		if candidate == "" || datasetWithinAnyRoot(candidate, protectedRoots) {
			continue
		}
		value, propertyErr := readLocalReplicationProperty(ctx, candidate, replicationPropertyPolicyID)
		if propertyErr != nil || value != strconv.FormatUint(uint64(policyID), 10) {
			continue
		}
		role, roleErr := readLocalReplicationProperty(ctx, candidate, replicationPropertyRole)
		if roleErr != nil || role != replicationRoleStandby {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftDepth := strings.Count(candidates[i], "/")
		rightDepth := strings.Count(candidates[j], "/")
		if leftDepth == rightDepth {
			return candidates[i] < candidates[j]
		}
		return leftDepth < rightDepth
	})
	selected := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if datasetWithinAnyRoot(candidate, selected) {
			continue
		}
		selected = append(selected, candidate)
	}
	for i := len(selected) - 1; i >= 0; i-- {
		if err := s.destroyLocalDatasetIncludingDependentsWithRetry(ctx, selected[i], 20, 500*time.Millisecond); err != nil {
			return fmt.Errorf("destroy_proven_replication_dataset_%s_failed: %w", selected[i], err)
		}
	}
	return nil
}

func (s *Service) validateLocalReplicationPolicyDeleteAuthority(
	policyID uint,
	expectedOwnerEpoch uint64,
) (*clusterModels.ReplicationPolicy, error) {
	if s == nil || s.Cluster == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return nil, err
	}
	if err := validateReplicationPolicyDeleteAuthority(policy, expectedOwnerEpoch); err != nil {
		return nil, err
	}
	return policy, nil
}

const replicationDeleteCleanupQuiesceTimeout = 10 * time.Second

// acquireReplicationDeleteCleanupGuards waits for an existing replication
// transfer and any guest-wide backup/transition operation to drain. Holding
// both guards prevents a new local operation from starting during cleanup.
func (s *Service) acquireReplicationDeleteCleanupGuards(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
) (func(), error) {
	if policy == nil || policy.ID == 0 {
		return nil, fmt.Errorf("invalid_policy")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, replicationDeleteCleanupQuiesceTimeout)
	if err := waitCtx.Err(); err != nil {
		cancel()
		return nil, fmt.Errorf("replication_policy_delete_cleanup_quiescing_context: %w", err)
	}

	wait := func(holder string) error {
		select {
		case <-waitCtx.Done():
			cancel()
			return fmt.Errorf("replication_policy_delete_cleanup_quiescing_%s: %w", holder, waitCtx.Err())
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	}

	for !s.acquireReplication(policy.ID) {
		if err := wait("replication_transfer"); err != nil {
			return nil, err
		}
	}

	for {
		if err := waitCtx.Err(); err != nil {
			s.releaseReplication(policy.ID)
			cancel()
			return nil, fmt.Errorf("replication_policy_delete_cleanup_quiescing_context: %w", err)
		}
		ok, holder := s.acquireWorkloadOperation(
			policy.GuestType,
			policy.GuestID,
			fmt.Sprintf("replication_policy_delete:%d", policy.ID),
		)
		if ok {
			return func() {
				s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)
				s.releaseReplication(policy.ID)
				cancel()
			}, nil
		}
		if err := wait(holder); err != nil {
			s.releaseReplication(policy.ID)
			return nil, err
		}
	}
}

func (s *Service) CleanupReplicationPolicyDeleteLocalBestEffort(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	policy, err := s.validateLocalReplicationPolicyDeleteAuthority(policyID, expectedOwnerEpoch)
	if err != nil {
		return err
	}
	releaseGuards, err := s.acquireReplicationDeleteCleanupGuards(ctx, policy)
	if err != nil {
		return err
	}
	defer releaseGuards()

	// Re-read after both local operation guards are held. The epoch and
	// deleting state are the cleanup authorization until a durable deletion
	// token is added to the policy schema.
	policy, err = s.validateLocalReplicationPolicyDeleteAuthority(policyID, expectedOwnerEpoch)
	if err != nil {
		return err
	}

	cleanupErrs := make([]string, 0)
	s.replicationCountersDelete(policyID)

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	ownerNodeID := replicationPolicyOwnerNode(policy)
	if ownerNodeID == "" {
		return fmt.Errorf("replication_policy_owner_missing")
	}
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}

	datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return err
	}

	// Never remove the active owner's primary dataset during policy delete.
	if localNodeID == ownerNodeID {
		// Historical `_gen-*` siblings are not deleted by name. Only the
		// provenance-aware remote generation primitives may remove them; an
		// unknown local sibling could be user-owned data with a coincidental
		// name. Leaving such residue is safer than destructive guessing.
		return s.cleanupLocalReplicationDatasetsByProvenance(ctx, policy.ID, datasets)
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
	if err := s.cleanupLocalReplicationDatasetsByProvenance(ctx, policy.ID, nil); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("cleanup_proven_replication_datasets_failed: %v", err))
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

// ReplicationPolicyRuntimeState reports the source state only for the exact
// persisted transition. Once the durable lock is acquired, ordinary guest
// actions are blocked, so this value remains stable until demotion.
func (s *Service) ReplicationPolicyRuntimeState(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) (bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	policy, err := s.validateLocalTransitionSource(policyID, expectedOwnerEpoch, transitionRunID)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(policy.GuestType) {
	case clusterModels.ReplicationGuestTypeVM:
		if runtimeVM, ok := s.VM.(interface {
			ReplicationVMRuntimeStateForTransition(uint, string) (bool, error)
		}); ok {
			return runtimeVM.ReplicationVMRuntimeStateForTransition(policy.GuestID, transitionRunID)
		}
	case clusterModels.ReplicationGuestTypeJail:
		if runtimeJail, ok := s.Jail.(interface {
			ReplicationJailRuntimeStateForTransition(uint, string) (bool, error)
		}); ok {
			return runtimeJail.ReplicationJailRuntimeStateForTransition(policy.GuestID, transitionRunID)
		}
	}
	return s.isReplicationGuestRunning(policy.GuestType, policy.GuestID)
}

func (s *Service) validateLocalTransitionSource(
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) (*clusterModels.ReplicationPolicy, error) {
	policy, err := s.validateLocalTransitionOwner(policyID, expectedOwnerEpoch, transitionRunID)
	if err != nil {
		return nil, err
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if strings.TrimSpace(policy.TransitionSourceNodeID) != localNodeID {
		return nil, fmt.Errorf("replication_policy_not_owned_by_local_transition_source")
	}
	return policy, nil
}

func (s *Service) validateLocalTransitionOwner(
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) (*clusterModels.ReplicationPolicy, error) {
	if policyID == 0 {
		return nil, fmt.Errorf("invalid_policy_id")
	}
	if expectedOwnerEpoch == 0 {
		return nil, fmt.Errorf("replication_owner_epoch_required")
	}
	transitionRunID = strings.TrimSpace(transitionRunID)
	if transitionRunID == "" {
		return nil, fmt.Errorf("replication_transition_run_id_required")
	}
	if s.Cluster == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("replication_policy_disabled")
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return nil, fmt.Errorf("local_node_id_missing")
	}
	if replicationPolicyOwnerNode(policy) != localNodeID {
		return nil, fmt.Errorf("replication_policy_not_owned_by_local_transition_owner")
	}
	if replicationPolicyOwnerEpoch(policy) != expectedOwnerEpoch ||
		policy.TransitionOwnerEpoch != expectedOwnerEpoch {
		return nil, fmt.Errorf("replication_policy_owner_epoch_mismatch")
	}
	if strings.TrimSpace(policy.TransitionRunID) != transitionRunID ||
		!transitionStateInProgress(policy.TransitionState) {
		return nil, fmt.Errorf("replication_transition_run_mismatch")
	}
	return policy, nil
}

func (s *Service) DemoteReplicationPolicyForTransition(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) error {
	if strings.TrimSpace(transitionRunID) == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	return s.demoteReplicationPolicy(ctx, policyID, expectedOwnerEpoch, transitionRunID)
}

func (s *Service) demoteReplicationPolicy(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
) error {
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

	if strings.TrimSpace(transitionRunID) != "" {
		// Re-read after acquiring the local workload lock. This is the final
		// epoch/run validation immediately before source shutdown.
		policy, err = s.validateLocalTransitionOwner(policyID, expectedOwnerEpoch, transitionRunID)
		if err != nil {
			return err
		}
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

func (s *Service) CatchupReplicationPolicyToNodeForTransition(
	ctx context.Context,
	policyID uint,
	targetNodeID string,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	generationID string,
) error {
	if strings.TrimSpace(transitionRunID) == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	if strings.TrimSpace(generationID) == "" {
		return fmt.Errorf("replication_generation_id_required")
	}
	return s.catchupReplicationPolicyToNode(
		ctx,
		policyID,
		targetNodeID,
		expectedOwnerEpoch,
		transitionRunID,
		generationID,
	)
}

func (s *Service) catchupReplicationPolicyToNode(
	ctx context.Context,
	policyID uint,
	targetNodeID string,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	generationID string,
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
	if strings.TrimSpace(transitionRunID) != "" {
		policy, err = s.validateLocalTransitionSource(policyID, expectedOwnerEpoch, transitionRunID)
		if err != nil {
			return err
		}
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

	if expectedOwnerEpoch == 0 {
		expectedOwnerEpoch = currentEpoch
	}
	if strings.TrimSpace(generationID) == "" {
		generationID = fmt.Sprintf("catchup-%d-%s", policy.ID, compactNowToken())
	}
	_, err = s.replicatePolicyGenerationToTarget(
		ctx,
		policy,
		targetNodeID,
		expectedOwnerEpoch,
		transitionRunID,
		generationID,
		0,
	)
	return err
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

func (s *Service) waitForLocalReplicationOwnershipForTransition(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	timeout time.Duration,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_missing")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	transitionRunID = strings.TrimSpace(transitionRunID)
	deadline := s.now().UTC().Add(timeout)
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
			if expectedOwnerEpoch > 0 && expectedEpoch != expectedOwnerEpoch {
				return fmt.Errorf("replication_policy_owner_epoch_mismatch")
			}
			if transitionRunID != "" {
				if strings.TrimSpace(policy.TransitionRunID) != transitionRunID ||
					!transitionStateInProgress(policy.TransitionState) {
					return fmt.Errorf("replication_transition_run_mismatch")
				}
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
						s.now().UTC().Before(lease.ExpiresAt) {
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
		if s.now().UTC().After(deadline) {
			return fmt.Errorf("replication_activation_ownership_not_ready")
		}
		s.sleep(200 * time.Millisecond)
	}
}

// ActivateReplicationPolicyForTransition prepares the promoted replica and
// restores the guest's pre-transition running state. The caller-provided bit
// is accepted only when it matches the value persisted under the exact run ID.
func (s *Service) ActivateReplicationPolicyForTransition(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	desiredRunning *bool,
) error {
	transitionRunID = strings.TrimSpace(transitionRunID)
	if expectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_owner_epoch_required")
	}
	if transitionRunID == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	if desiredRunning == nil {
		return fmt.Errorf("replication_desired_running_required")
	}
	return s.activateReplicationPolicy(ctx, policyID, expectedOwnerEpoch, transitionRunID, desiredRunning)
}

func (s *Service) activateReplicationPolicy(
	ctx context.Context,
	policyID uint,
	expectedOwnerEpoch uint64,
	transitionRunID string,
	desiredRunning *bool,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	if err := s.waitForLocalReplicationOwnershipForTransition(
		ctx,
		policyID,
		expectedOwnerEpoch,
		transitionRunID,
		10*time.Second,
	); err != nil {
		return err
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}
	if desiredRunning == nil {
		return fmt.Errorf("replication_desired_running_required")
	}
	if transitionRunID != "" {
		if policy.TransitionOriginalRunning == nil {
			return fmt.Errorf("replication_transition_original_running_missing")
		}
		if *policy.TransitionOriginalRunning != *desiredRunning {
			return fmt.Errorf("replication_transition_desired_running_mismatch")
		}
		if strings.TrimSpace(policy.TransitionRunID) != transitionRunID ||
			!transitionStateInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_transition_run_mismatch")
		}
		ctx = clusterModels.WithReplicationTransitionAuthority(ctx, transitionRunID, policy.OwnerEpoch)
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
	running, stateErr := s.isReplicationGuestRunning(policy.GuestType, policy.GuestID)
	if stateErr != nil {
		return fmt.Errorf("replication_activation_existing_state_check_failed: %w", stateErr)
	}
	if *desiredRunning {
		if running {
			if err := s.validateAlreadyRunningReplicationActivation(ctx, policy); err != nil {
				return err
			}
			return s.setReplicationGuestIntentionallyStopped(policy.GuestType, policy.GuestID, false)
		}
	}
	if strings.EqualFold(strings.TrimSpace(policy.TransitionState), clusterModels.ReplicationTransitionStatePromoting) {
		if !*desiredRunning && !running {
			if s.replicationGuestExistsLocally(policy.GuestType, policy.GuestID) {
				if err := s.validateReplicationTransitionGenerationForActivation(ctx, policy, "off"); err == nil {
					return s.setReplicationGuestIntentionallyStopped(policy.GuestType, policy.GuestID, true)
				}
			}
		}
		if err := s.validateReplicationTransitionGenerationForActivation(ctx, policy, "on"); err != nil {
			return err
		}
	}
	if err := driver.activate(ctx, policy.GuestID, transitionRunID, *desiredRunning); err != nil {
		// Activation may have prepared more than one guest root before a
		// later root or guest registration failed.  Never leave a partially
		// writable replica behind after an unsuccessful promotion.
		if fenceErr := s.fenceReplicationGuestDatasets(ctx, policy, "activation_failed"); fenceErr != nil {
			return fmt.Errorf("%v; activation_refence_failed: %v", err, fenceErr)
		}
		return err
	}
	if err := s.setReplicationGuestIntentionallyStopped(policy.GuestType, policy.GuestID, !*desiredRunning); err != nil {
		return fmt.Errorf("replication_activation_persist_running_intent_failed: %w", err)
	}
	return nil
}

func (s *Service) validateAlreadyRunningReplicationActivation(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
) error {
	if policy == nil || policy.ID == 0 || policy.GuestID == 0 {
		return fmt.Errorf("invalid_replication_activation_policy")
	}
	if !s.replicationGuestExistsLocally(policy.GuestType, policy.GuestID) {
		return fmt.Errorf("replication_running_guest_registration_missing")
	}
	if strings.EqualFold(strings.TrimSpace(policy.TransitionState), clusterModels.ReplicationTransitionStatePromoting) {
		return s.validateReplicationTransitionGenerationForActivation(ctx, policy, "off")
	}
	if strings.EqualFold(strings.TrimSpace(policy.TransitionState), clusterModels.ReplicationTransitionStateRollingBack) {
		datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
		if err != nil {
			return err
		}
		if len(datasets) == 0 {
			return fmt.Errorf("replication_rollback_running_guest_dataset_missing")
		}
		for _, dataset := range datasets {
			readonlyOutput, readonlyErr := utils.RunCommandWithContext(
				ctx, "zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", dataset,
			)
			if readonlyErr != nil {
				return readonlyErr
			}
			for _, value := range strings.Fields(readonlyOutput) {
				if value != "off" {
					return fmt.Errorf("replication_rollback_running_guest_dataset_not_writable_%s", dataset)
				}
			}
		}
		return nil
	}
	datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return fmt.Errorf("replication_running_guest_dataset_missing")
	}
	expectedProperties := map[string]string{
		replicationPropertyPolicyID: strconv.FormatUint(uint64(policy.ID), 10),
		replicationPropertyRole:     replicationRoleStandby,
		replicationPropertyState:    replicationStateReady,
	}
	for _, dataset := range datasets {
		for property, expected := range expectedProperties {
			output, propertyErr := utils.RunCommandWithContext(
				ctx,
				"zfs",
				"get",
				"-H",
				"-o",
				"source,value",
				property,
				dataset,
			)
			if propertyErr != nil {
				return fmt.Errorf("replication_running_dataset_provenance_read_failed_%s: %w", dataset, propertyErr)
			}
			fields := strings.Fields(strings.TrimSpace(output))
			if len(fields) != 2 || fields[0] != "local" || fields[1] != expected {
				return fmt.Errorf("replication_running_dataset_provenance_mismatch_%s_%s", dataset, property)
			}
		}
		readonlyOutput, readonlyErr := utils.RunCommandWithContext(
			ctx,
			"zfs",
			"get",
			"-H",
			"-o",
			"value",
			"-r",
			"-t",
			"filesystem,volume",
			"readonly",
			dataset,
		)
		if readonlyErr != nil {
			return fmt.Errorf("replication_running_dataset_readonly_check_failed_%s: %w", dataset, readonlyErr)
		}
		for _, value := range strings.Fields(readonlyOutput) {
			if value != "off" {
				return fmt.Errorf("replication_running_dataset_not_writable_%s", dataset)
			}
		}
	}
	return nil
}

func readLocalReplicationProperty(ctx context.Context, dataset string, property string) (string, error) {
	output, err := utils.RunCommandWithContext(
		ctx,
		"zfs",
		"get",
		"-H",
		"-o",
		"source,value",
		property,
		dataset,
	)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) != 2 || fields[0] != "local" {
		return "", fmt.Errorf("replication_property_not_local")
	}
	return strings.TrimSpace(fields[1]), nil
}

func (s *Service) validateReplicationTransitionGenerationForActivation(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	requiredReadonly string,
) error {
	if policy == nil || policy.ID == 0 || policy.GuestID == 0 {
		return fmt.Errorf("invalid_replication_activation_policy")
	}
	generationID := strings.TrimSpace(policy.TransitionGenerationID)
	manifestHash := strings.TrimSpace(policy.TransitionGenerationManifest)
	if generationID == "" || manifestHash == "" ||
		policy.TransitionGenerationOwnerEpoch == 0 || policy.TransitionGenerationRootCount <= 0 {
		return fmt.Errorf("replication_transition_generation_evidence_missing")
	}
	snapshotName, err := replicationGenerationSnapshotName(generationID)
	if err != nil {
		return err
	}
	datasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return err
	}
	if len(datasets) != policy.TransitionGenerationRootCount {
		return fmt.Errorf(
			"replication_transition_generation_root_count_mismatch expected=%d actual=%d",
			policy.TransitionGenerationRootCount,
			len(datasets),
		)
	}
	expected := map[string]string{
		replicationPropertyPolicyID:   strconv.FormatUint(uint64(policy.ID), 10),
		replicationPropertyRunID:      generationID,
		replicationPropertyOwnerEpoch: strconv.FormatUint(policy.TransitionGenerationOwnerEpoch, 10),
		replicationPropertyRole:       replicationRoleStandby,
		replicationPropertyState:      replicationStateReady,
		replicationPropertySnapshot:   snapshotName,
	}
	manifestParts := make([][]ReplicationSnapshotManifestEntry, 0, len(datasets))
	seenSources := make(map[string]struct{}, len(datasets))
	for _, dataset := range datasets {
		for property, expectedValue := range expected {
			actual, propertyErr := readLocalReplicationProperty(ctx, dataset, property)
			if propertyErr != nil || actual != expectedValue {
				return fmt.Errorf("replication_transition_generation_property_mismatch_%s_%s", dataset, property)
			}
		}
		targetPath, targetErr := readLocalReplicationProperty(ctx, dataset, replicationPropertyTarget)
		if targetErr != nil || normalizeDatasetPath(targetPath) != normalizeDatasetPath(dataset) {
			return fmt.Errorf("replication_transition_generation_target_mismatch_%s", dataset)
		}
		sourcePath, sourceErr := readLocalReplicationProperty(ctx, dataset, replicationPropertySource)
		if sourceErr != nil || normalizeDatasetPath(sourcePath) == "" {
			return fmt.Errorf("replication_transition_generation_source_missing_%s", dataset)
		}
		if _, duplicate := seenSources[normalizeDatasetPath(sourcePath)]; duplicate {
			return fmt.Errorf("replication_transition_generation_source_duplicate_%s", sourcePath)
		}
		seenSources[normalizeDatasetPath(sourcePath)] = struct{}{}
		storedGUID, guidPropertyErr := readLocalReplicationProperty(ctx, dataset, replicationPropertySnapshotGUID)
		if guidPropertyErr != nil || strings.TrimSpace(storedGUID) == "" {
			return fmt.Errorf("replication_transition_generation_snapshot_guid_missing_%s", dataset)
		}
		actualGUID, guidErr := utils.RunCommandWithContext(
			ctx,
			"zfs",
			"get",
			"-H",
			"-o",
			"value",
			"guid",
			dataset+"@"+snapshotName,
		)
		if guidErr != nil || strings.TrimSpace(actualGUID) != strings.TrimSpace(storedGUID) {
			return fmt.Errorf("replication_transition_generation_snapshot_guid_mismatch_%s", dataset)
		}
		readonlyOutput, readonlyErr := utils.RunCommandWithContext(
			ctx,
			"zfs",
			"get",
			"-H",
			"-o",
			"value",
			"-r",
			"-t",
			"filesystem,volume",
			"readonly",
			dataset,
		)
		if readonlyErr != nil {
			return fmt.Errorf("replication_transition_generation_readonly_check_failed_%s: %w", dataset, readonlyErr)
		}
		for _, value := range strings.Fields(readonlyOutput) {
			if value != "on" && value != "off" {
				return fmt.Errorf("replication_transition_generation_readonly_invalid_%s", dataset)
			}
			if requiredReadonly != "" && value != requiredReadonly {
				return fmt.Errorf("replication_transition_generation_readonly_mismatch_%s", dataset)
			}
		}
		treeManifest, treeErr := s.replicationSnapshotTreeManifestLocal(
			ctx,
			dataset,
			normalizeDatasetPath(sourcePath),
			snapshotName,
		)
		if treeErr != nil {
			return fmt.Errorf("replication_transition_generation_tree_invalid_%s: %w", dataset, treeErr)
		}
		manifestParts = append(manifestParts, treeManifest)
	}
	manifest, err := mergeReplicationSnapshotTreeManifests(manifestParts...)
	if err != nil {
		return fmt.Errorf("replication_transition_generation_tree_merge_failed: %w", err)
	}
	actualManifest := replicationSnapshotManifestHash(
		policy.ID,
		policy.TransitionGenerationOwnerEpoch,
		generationID,
		manifest,
	)
	if actualManifest != manifestHash {
		return fmt.Errorf("replication_transition_generation_manifest_mismatch")
	}
	return nil
}

func (s *Service) setReplicationGuestIntentionallyStopped(guestType string, guestID uint, stopped bool) error {
	if s == nil || s.DB == nil || guestID == 0 {
		return fmt.Errorf("replication_guest_persistence_unavailable")
	}
	var result *gorm.DB
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		result = s.DB.Model(&vmModels.VM{}).Where("rid = ?", guestID).Update("intentionally_stopped", stopped)
	case clusterModels.ReplicationGuestTypeJail:
		result = s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", guestID).Update("intentionally_stopped", stopped)
	default:
		return fmt.Errorf("invalid_guest_type")
	}
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("replication_guest_registration_missing")
	}
	return nil
}

func (s *Service) activateReplicationJail(
	ctx context.Context,
	ctID uint,
	transitionRunID string,
	desiredRunning bool,
) error {
	running, stateErr := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeJail, ctID)
	if stateErr != nil {
		return fmt.Errorf("check_jail_state_before_activation_failed: %w", stateErr)
	}
	if running && desiredRunning {
		// Activation requests are retried across network timeouts.  A guest
		// already running under the validated local lease is the desired
		// idempotent outcome; do not bounce it.
		return nil
	}
	if err := s.stopLocalJailIfPresent(ctID); err != nil {
		return err
	}

	datasets, err := s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeJail, ctID)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return fmt.Errorf("jail_dataset_not_found")
	}
	for _, dataset := range datasets {
		if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
			return fmt.Errorf("prepare_jail_replication_dataset_%s_failed: %w", dataset, err)
		}
	}
	reconcileCtx := clusterModels.WithReplicationTransitionRun(ctx, transitionRunID)
	if err := s.reconcileRestoredJailFromDatasetWithOptions(reconcileCtx, datasets[0], true); err != nil {
		return err
	}

	if !desiredRunning {
		return nil
	}
	if strings.TrimSpace(transitionRunID) != "" {
		transitionJail, ok := s.Jail.(interface {
			JailActionForReplication(int, string, string) error
		})
		if !ok {
			return fmt.Errorf("jail_replication_transition_action_unavailable")
		}
		return transitionJail.JailActionForReplication(int(ctID), "start", transitionRunID)
	}
	return s.Jail.JailAction(int(ctID), "start")
}

func (s *Service) activateReplicationVM(
	ctx context.Context,
	rid uint,
	transitionRunID string,
	desiredRunning bool,
) error {
	return s.activateReplicationVMWithRegistrationRecovery(ctx, rid, transitionRunID, desiredRunning, false)
}

func (s *Service) activateReplicationVMWithRegistrationRecovery(
	ctx context.Context,
	rid uint,
	transitionRunID string,
	desiredRunning bool,
	recoverMissingRegistration bool,
) error {
	running, stateErr := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, rid)
	if stateErr != nil {
		return fmt.Errorf("check_vm_state_before_activation_failed: %w", stateErr)
	}
	if running {
		registeredVM, lookupErr := s.findVMByRID(rid)
		if lookupErr != nil {
			return fmt.Errorf("replication_vm_storage_eligibility_lookup_failed: %w", lookupErr)
		}
		if registeredVM == nil {
			return fmt.Errorf("vm_definition_not_found_before_activation")
		}
		if eligibilityErr := requireSupportedReplicationVMStorages(registeredVM.Storages); eligibilityErr != nil {
			return eligibilityErr
		}
	}
	if running && desiredRunning {
		return nil
	}
	if err := s.stopVMIfPresent(rid); err != nil {
		return err
	}

	datasets, err := s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeVM, rid)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return fmt.Errorf("vm_dataset_not_found")
	}
	if err := s.requireRestoredVMReplicationStorageEligibility(ctx, datasets[0], rid); err != nil {
		return err
	}
	for _, dataset := range datasets {
		if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
			return fmt.Errorf("prepare_vm_replication_dataset_%s_failed: %w", dataset, err)
		}
	}
	reconcileCtx := clusterModels.WithReplicationTransitionRun(ctx, transitionRunID)
	var reconcileErr error
	if recoverMissingRegistration {
		reconcileErr = s.reconcileRestoredVMFromDatasetForOwnerRecovery(reconcileCtx, datasets[0])
	} else {
		reconcileErr = s.reconcileRestoredVMFromDatasetWithOptions(reconcileCtx, datasets[0], true)
	}
	if reconcileErr != nil {
		s.cleanupOrphanedVMRegistration(rid)
		return reconcileErr
	}
	vm, err := s.findVMByRID(rid)
	if err != nil {
		s.cleanupOrphanedVMRegistration(rid)
		return err
	}
	if vm == nil {
		s.cleanupOrphanedVMRegistration(rid)
		return fmt.Errorf("vm_definition_not_found_after_reconcile")
	}
	if !desiredRunning {
		return nil
	}

	var startErr error
	if strings.TrimSpace(transitionRunID) != "" {
		transitionVM, ok := s.VM.(interface {
			LvVMActionForReplication(vmModels.VM, string, string) error
		})
		if !ok {
			return fmt.Errorf("vm_replication_transition_action_unavailable")
		}
		startErr = transitionVM.LvVMActionForReplication(*vm, "start", transitionRunID)
	} else {
		startErr = s.VM.LvVMAction(*vm, "start")
	}
	if startErr != nil {
		s.cleanupOrphanedVMRegistration(rid)
		return startErr
	}
	return nil
}

func (s *Service) requireRestoredVMReplicationStorageEligibility(
	ctx context.Context,
	dataset string,
	rid uint,
) error {
	restoredMetadata, err := s.readLocalRestoredVMMetadata(ctx, dataset, rid)
	if err != nil {
		return fmt.Errorf("replication_vm_storage_eligibility_metadata_read_failed: %w", err)
	}
	if restoredMetadata == nil {
		return fmt.Errorf("restored_vm_metadata_not_found")
	}
	return requireSupportedReplicationVMStorages(restoredMetadata.VM.Storages)
}

func (s *Service) cleanupOrphanedVMRegistration(rid uint) {
	if s.VM == nil || rid == 0 {
		return
	}

	var count int64
	if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", rid).Count(&count).Error; err != nil || count == 0 {
		return
	}

	if _, err := s.VM.GetLvDomain(rid); err == nil {
		return // a libvirt domain is defined here -> not an orphan
	} else if !isVMDomainNotFoundError(err) {
		return // libvirt unreachable / other error -> cannot confirm orphan, leave it
	}

	warnings, purgeErr := s.VM.PurgeVMRegistration(rid, true)
	if purgeErr != nil {
		logger.L.Warn().Err(purgeErr).Uint("rid", rid).Msg("failed_to_purge_orphaned_vm_after_failed_activation")
		return
	}
	if len(warnings) > 0 {
		logger.L.Warn().Strs("warnings", warnings).Uint("rid", rid).Msg("purged_orphaned_vm_after_failed_activation_with_warnings")
		return
	}
	logger.L.Info().Uint("rid", rid).Msg("purged_orphaned_vm_after_failed_activation")
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

	rootDatasets, err := s.findLocalGuestDatasets(ctx, policy.GuestType, policy.GuestID)
	if err != nil {
		return fmt.Errorf("unfence_find_dataset_failed: %w", err)
	}
	if len(rootDatasets) == 0 {
		return nil
	}

	for _, rootDataset := range rootDatasets {
		val, err := utils.RunCommandWithContext(
			ctx, "zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", rootDataset,
		)
		if err != nil {
			return fmt.Errorf("unfence_check_readonly_%s_failed: %w", rootDataset, err)
		}
		needsUnfence := false
		for _, value := range strings.Fields(val) {
			if value == "on" {
				needsUnfence = true
				break
			}
		}
		if !needsUnfence {
			continue
		}

		logger.L.Info().
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Str("guest_type", policy.GuestType).
			Str("root_dataset", rootDataset).
			Msg("replication_self_fence_unfencing_dataset")

		if err := s.prepareReplicatedDatasetForActivation(ctx, rootDataset); err != nil {
			return fmt.Errorf("unfence_prepare_dataset_%s_failed: %w", rootDataset, err)
		}
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

	if err := s.Jail.RetireJailLocalMetadata(ctx, ctID, false); err != nil {
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

func (s *Service) findLocalGuestDatasets(ctx context.Context, guestType string, guestID uint) ([]string, error) {
	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	candidates := make([]string, 0)
	for _, dataset := range datasets {
		if isReplicationLineageDatasetPath(dataset) {
			continue
		}
		kind, id, root, canonical := parseCanonicalReplicationGuestDataset(dataset)
		if !canonical || kind != strings.TrimSpace(guestType) || id != guestID {
			continue
		}
		root = normalizeDatasetPath(root)
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		candidates = append(candidates, root)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		leftDepth := strings.Count(candidates[i], "/")
		rightDepth := strings.Count(candidates[j], "/")
		if leftDepth == rightDepth {
			return candidates[i] < candidates[j]
		}
		return leftDepth < rightDepth
	})
	collapsed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		overlapsParent := false
		for _, root := range collapsed {
			if candidate == root || strings.HasPrefix(candidate, root+"/") {
				overlapsParent = true
				break
			}
		}
		if !overlapsParent {
			collapsed = append(collapsed, candidate)
		}
	}
	sort.Strings(collapsed)
	return collapsed, nil
}

func isReplicationLineageDatasetPath(dataset string) bool {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return false
	}
	parts := strings.Split(dataset, "/")
	for i := 0; i+1 < len(parts); i++ {
		container := strings.ToLower(strings.TrimSpace(parts[i]))
		if container != "virtual-machines" && container != "jails" {
			continue
		}
		guestLeaf := strings.ToLower(strings.TrimSpace(parts[i+1]))
		for _, marker := range []string{"_gen-", "_previous-"} {
			markerAt := strings.Index(guestLeaf, marker)
			if markerAt <= 0 {
				continue
			}
			guestID, err := strconv.ParseUint(guestLeaf[:markerAt], 10, 64)
			if err == nil && guestID > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Service) selfFenceReplicationPolicy(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	localNodeID string,
	expectedOwner string,
	fenceReason string,
	requireRegistration bool,
) {
	if policy == nil || policy.ID == 0 {
		return
	}
	cutoverTarget, cutoverErr := s.isLocalMigrationCutoverTarget(ctx, policy.GuestType, policy.GuestID, localNodeID)
	if cutoverErr != nil {
		logger.L.Warn().
			Err(cutoverErr).
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Msg("replication_self_fence_migration_cutover_check_failed")
	} else if cutoverTarget {
		// The target has a durable cutover permit and may already have imported
		// the VM before the policy ownership update reaches this node.
		return
	}
	if !requireRegistration {
		if err := s.emergencyStopReplicationGuest(policy.GuestType, policy.GuestID); err != nil {
			logger.L.Error().Err(err).
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Str("guest_type", strings.TrimSpace(policy.GuestType)).
				Msg("replication_emergency_self_fence_stop_failed")
		}
	}
	driver, err := s.replicationGuestDriver(policy.GuestType)
	if err != nil {
		logger.L.Warn().
			Err(err).
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Str("guest_type", strings.TrimSpace(policy.GuestType)).
			Msg("replication_self_fence_invalid_guest_type")
		return
	}
	if requireRegistration {
		registered, lookupErr := s.replicationGuestRegistrationStatus(policy.GuestType, policy.GuestID)
		if lookupErr != nil {
			if err := s.emergencyStopReplicationGuest(policy.GuestType, policy.GuestID); err != nil {
				logger.L.Error().Err(err).
					Uint("policy_id", policy.ID).
					Msg("replication_registration_lookup_failed_emergency_stop_failed")
			}
		} else if !registered {
			return
		}
	}
	driver.selfFence(ctx, policy.ID, policy.GuestID, localNodeID, expectedOwner, fenceReason)
	if err := s.fenceReplicationGuestDatasets(ctx, policy, fenceReason); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Str("guest_type", strings.TrimSpace(policy.GuestType)).
			Str("reason", fenceReason).
			Msg("replication_self_fence_dataset_fencing_failed")
	}
}

func (s *Service) isLocalMigrationCutoverTarget(
	ctx context.Context,
	guestType string,
	guestID uint,
	localNodeID string,
) (bool, error) {
	if s == nil || s.DB == nil || guestID == 0 || !replicationguard.GuestOperationSchemaReady(s.DB) {
		return false, nil
	}

	var operation clusterModels.ReplicationGuestOperation
	result := s.DB.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", strings.TrimSpace(guestType), guestID).
		Limit(1).
		Find(&operation)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}

	return operation.Operation == clusterModels.ReplicationGuestOperationMigration &&
		operation.State == clusterModels.ReplicationGuestOperationCutover &&
		strings.TrimSpace(operation.TargetNodeID) == strings.TrimSpace(localNodeID), nil
}

func (s *Service) replicationGuestRegistrationStatus(guestType string, guestID uint) (bool, error) {
	if s == nil || s.DB == nil || guestID == 0 {
		return false, nil
	}
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		var count int64
		if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false, err
		}
		return count > 0, nil
	case clusterModels.ReplicationGuestTypeJail:
		var count int64
		if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false, err
		}
		return count > 0, nil
	default:
		return false, nil
	}
}

func (s *Service) emergencyStopAllManagedGuestRuntimes(ctx context.Context) error {
	var fenceErrs []error

	if s == nil || s.VM == nil {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_vm_runtime_fencer_unavailable"))
	} else if fencer, ok := s.VM.(emergencyVMRuntimeFencer); !ok {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_vm_runtime_fencer_unavailable"))
	} else if err := fencer.EmergencyStopAllManagedVMs(ctx); err != nil {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_vm_runtime_fence_failed: %w", err))
	}

	// Always attempt jail fencing even when VM fencing failed.  A failure in one
	// runtime must never leave the other runtime with write authority.
	if s == nil || s.Jail == nil {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_jail_runtime_fencer_unavailable"))
	} else if fencer, ok := s.Jail.(emergencyJailRuntimeFencer); !ok {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_jail_runtime_fencer_unavailable"))
	} else if err := fencer.EmergencyStopAllManagedJails(ctx); err != nil {
		fenceErrs = append(fenceErrs, fmt.Errorf("replication_emergency_jail_runtime_fence_failed: %w", err))
	}

	return errors.Join(fenceErrs...)
}

func (s *Service) emergencyStopReplicationGuest(guestType string, guestID uint) error {
	if guestID == 0 {
		return nil
	}
	var err error
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		if s.VM != nil {
			err = s.VM.ForceStopVM(guestID)
		}
	case clusterModels.ReplicationGuestTypeJail:
		if s.Jail != nil {
			if runtimeStopper, ok := s.Jail.(interface{ EmergencyStopJailRuntime(uint) error }); ok {
				err = runtimeStopper.EmergencyStopJailRuntime(guestID)
			} else {
				err = s.Jail.ForceStopJail(guestID)
			}
		}
	default:
		return fmt.Errorf("invalid_replication_guest_type")
	}
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not running") || strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such process") || strings.Contains(lower, "failed to find jail") {
		return nil
	}
	return err
}

func parseCanonicalReplicationGuestDataset(dataset string) (string, uint, string, bool) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" || strings.Contains(dataset, "@") {
		return "", 0, "", false
	}
	parts := strings.Split(dataset, "/")
	if len(parts) < 4 || strings.TrimSpace(parts[0]) == "" || parts[1] != "sylve" {
		return "", 0, "", false
	}
	guestType := ""
	switch parts[2] {
	case "virtual-machines":
		guestType = clusterModels.ReplicationGuestTypeVM
	case "jails":
		guestType = clusterModels.ReplicationGuestTypeJail
	default:
		return "", 0, "", false
	}
	guestID64, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || guestID64 == 0 || guestID64 > uint64(^uint(0)) {
		return "", 0, "", false
	}
	// ParseUint accepts decimal aliases such as 0042.  Sylve creates guest
	// roots with the canonical base-10 ID, so accepting an alias here would
	// turn an unrelated dataset into a cold-fencing target.
	if parts[3] != strconv.FormatUint(guestID64, 10) {
		return "", 0, "", false
	}
	return guestType, uint(guestID64), strings.Join(parts[:4], "/"), true
}

func canonicalReplicationGuestDatasetRoot(dataset, guestType string) string {
	parsedType, _, root, ok := parseCanonicalReplicationGuestDataset(dataset)
	if !ok || parsedType != strings.TrimSpace(guestType) {
		return ""
	}
	return root
}

type replicationZFSPropertyState struct {
	Value  string
	Source string
}

func readReplicationZFSPropertyState(
	ctx context.Context,
	dataset string,
	property string,
) (replicationZFSPropertyState, error) {
	output, err := utils.RunCommandWithContext(
		ctx, "zfs", "get", "-H", "-o", "value,source", property, dataset,
	)
	if err != nil {
		return replicationZFSPropertyState{}, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return replicationZFSPropertyState{}, fmt.Errorf("replication_zfs_property_state_invalid")
	}
	return replicationZFSPropertyState{Value: fields[0], Source: strings.Join(fields[1:], " ")}, nil
}

func replicationZFSPropertySourceKind(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	switch {
	case source == "local":
		return "local"
	case strings.HasPrefix(source, "inherited from "):
		return "inherited"
	case source == "received":
		return "received"
	case source == "default":
		return "default"
	case source == "temporary":
		return "temporary"
	case source == "-" || source == "":
		return "none"
	default:
		return source
	}
}

func replicationZFSPropertyStateMatches(current, previous replicationZFSPropertyState) bool {
	return strings.TrimSpace(current.Value) == strings.TrimSpace(previous.Value) &&
		replicationZFSPropertySourceKind(current.Source) == replicationZFSPropertySourceKind(previous.Source)
}

func restoreReplicationZFSPropertyState(
	ctx context.Context,
	dataset string,
	property string,
	previous replicationZFSPropertyState,
) error {
	var err error
	if replicationZFSPropertySourceKind(previous.Source) == "local" {
		_, err = utils.RunCommandWithContext(ctx, "zfs", "set", property+"="+previous.Value, dataset)
	} else {
		_, err = utils.RunCommandWithContext(ctx, "zfs", "inherit", property, dataset)
	}
	if err != nil {
		return err
	}
	current, err := readReplicationZFSPropertyState(ctx, dataset, property)
	if err != nil {
		return err
	}
	if !replicationZFSPropertyStateMatches(current, previous) {
		return fmt.Errorf("replication_zfs_property_restore_verification_failed_%s", property)
	}
	return nil
}

func listReplicationDatasetGUIDs(
	ctx context.Context,
	roots []string,
) (map[string]string, error) {
	byGUID := make(map[string]string)
	for _, root := range roots {
		output, err := utils.RunCommandWithContext(
			ctx, "zfs", "list", "-H", "-p", "-o", "name,guid", "-r", "-t", "filesystem,volume", root,
		)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
		}
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || normalizeDatasetPath(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
				return nil, fmt.Errorf("replication_dataset_guid_state_invalid_%s", root)
			}
			byGUID[strings.TrimSpace(fields[1])] = normalizeDatasetPath(fields[0])
		}
	}
	return byGUID, nil
}

func listAllReplicationDatasetGUIDs(ctx context.Context) (map[string]string, error) {
	output, err := utils.RunCommandWithContext(
		ctx, "zfs", "list", "-H", "-p", "-o", "name,guid", "-t", "filesystem,volume",
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	byGUID := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("replication_dataset_guid_state_invalid")
		}
		byGUID[strings.TrimSpace(fields[1])] = normalizeDatasetPath(fields[0])
	}
	return byGUID, nil
}

func (s *Service) listReplicationDatasetGUIDIndex(ctx context.Context) (map[string]string, error) {
	if s == nil || s.localFilesystemDatasetLister == nil {
		return listAllReplicationDatasetGUIDs(ctx)
	}

	datasets, err := s.localFilesystemDatasetLister(ctx)
	if err != nil {
		return nil, err
	}
	byGUID := make(map[string]string, len(datasets))
	for _, dataset := range datasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			continue
		}
		output, getErr := utils.RunCommandWithContext(
			ctx, "zfs", "get", "-H", "-p", "-o", "value", "guid", dataset,
		)
		if getErr != nil {
			return nil, fmt.Errorf("replication_dataset_guid_lookup_failed_%s: %s: %w", dataset, strings.TrimSpace(output), getErr)
		}
		guid := strings.TrimSpace(output)
		if guid == "" || guid == "-" {
			return nil, fmt.Errorf("replication_dataset_guid_state_invalid_%s", dataset)
		}
		byGUID[guid] = dataset
	}
	return byGUID, nil
}

func replicationEmergencyReadonlyChangeKey(guid string) string {
	return "guid:" + strings.TrimSpace(guid)
}

func (s *Service) fenceReplicationEmergencyReadonlyRoots(
	ctx context.Context,
	guestType string,
	guestID uint,
	roots []string,
) error {
	replicationEmergencyReadonlyMu.Lock()
	defer replicationEmergencyReadonlyMu.Unlock()

	changes, err := loadDurableReplicationEmergencyReadonlyChanges()
	if err != nil {
		// Safety still requires a fence even when reversibility cannot be
		// recorded.  Do not write an untracked marker in this failure path.
		fenceErr := s.fenceReplicationDatasetRoots(ctx, 0, roots, replicationFenceReasonOwnerLeaseInvalid)
		return errors.Join(fmt.Errorf("load_replication_emergency_readonly_failed: %w", err), fenceErr)
	}
	if changes == nil {
		changes = make(map[string]replicationEmergencyReadonlyChange)
	}

	fenceToken := ""
	for _, change := range changes {
		if replicationGuestKey(change.GuestType, change.GuestID) == replicationGuestKey(guestType, guestID) &&
			strings.TrimSpace(change.FenceToken) != "" {
			fenceToken = strings.TrimSpace(change.FenceToken)
			break
		}
	}
	if fenceToken == "" {
		fenceToken = uuid.NewString()
	}

	byGUID, err := listReplicationDatasetGUIDs(ctx, roots)
	if err != nil {
		fenceErr := s.fenceReplicationDatasetRoots(ctx, 0, roots, replicationFenceReasonOwnerLeaseInvalid)
		return errors.Join(fmt.Errorf("record_replication_emergency_dataset_guid_failed: %w", err), fenceErr)
	}
	dirty := false
	for guid, dataset := range byGUID {
		key := replicationEmergencyReadonlyChangeKey(guid)
		if existing, ok := changes[key]; ok {
			if existing.GuestType != strings.TrimSpace(guestType) || existing.GuestID != guestID ||
				strings.TrimSpace(existing.DatasetGUID) != strings.TrimSpace(guid) {
				return fmt.Errorf("replication_emergency_readonly_journal_identity_conflict_%s", dataset)
			}
			if existing.Dataset != dataset {
				existing.Dataset = dataset
				changes[key] = existing
				dirty = true
			}
			continue
		}
		readonlyState, stateErr := readReplicationZFSPropertyState(ctx, dataset, "readonly")
		if stateErr != nil {
			return errors.Join(
				fmt.Errorf("record_replication_emergency_readonly_state_failed_%s: %w", dataset, stateErr),
				s.fenceReplicationDatasetRoots(ctx, 0, roots, replicationFenceReasonOwnerLeaseInvalid),
			)
		}
		markerState, stateErr := readReplicationZFSPropertyState(ctx, dataset, replicationEmergencyReadonlyProp)
		if stateErr != nil {
			return errors.Join(
				fmt.Errorf("record_replication_emergency_marker_state_failed_%s: %w", dataset, stateErr),
				s.fenceReplicationDatasetRoots(ctx, 0, roots, replicationFenceReasonOwnerLeaseInvalid),
			)
		}
		changes[key] = replicationEmergencyReadonlyChange{
			Dataset: dataset, DatasetGUID: guid, FenceToken: fenceToken,
			GuestType: strings.TrimSpace(guestType), GuestID: guestID,
			PreviousValue: readonlyState.Value, PreviousSource: readonlyState.Source,
			PreviousMarkerValue: markerState.Value, PreviousMarkerSource: markerState.Source,
		}
		dirty = true
	}
	if dirty {
		if err := persistDurableReplicationEmergencyReadonlyChanges(changes); err != nil {
			fenceErr := s.fenceReplicationDatasetRoots(ctx, 0, roots, replicationFenceReasonOwnerLeaseInvalid)
			return errors.Join(fmt.Errorf("persist_replication_emergency_readonly_failed: %w", err), fenceErr)
		}
	}

	datasets := make([]string, 0, len(byGUID))
	for _, dataset := range byGUID {
		datasets = append(datasets, dataset)
	}
	sort.SliceStable(datasets, func(i, j int) bool {
		return strings.Count(datasets[i], "/") > strings.Count(datasets[j], "/")
	})
	var fenceErr error
	for _, dataset := range datasets {
		guid := ""
		for candidateGUID, candidate := range byGUID {
			if candidate == dataset {
				guid = candidateGUID
				break
			}
		}
		change := changes[replicationEmergencyReadonlyChangeKey(guid)]
		if _, setErr := utils.RunCommandWithContext(
			ctx, "zfs", "set", replicationEmergencyReadonlyProp+"="+change.FenceToken, dataset,
		); setErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, setErr)
		}
		if _, setErr := utils.RunCommandWithContext(ctx, "zfs", "set", "readonly=on", dataset); setErr != nil {
			fenceErr = appendReplicationFenceDatasetError(fenceErr, dataset, setErr)
		}
	}
	for guid, dataset := range byGUID {
		change := changes[replicationEmergencyReadonlyChangeKey(guid)]
		readonlyState, readonlyErr := readReplicationZFSPropertyState(ctx, dataset, "readonly")
		markerState, markerErr := readReplicationZFSPropertyState(ctx, dataset, replicationEmergencyReadonlyProp)
		if readonlyErr != nil || markerErr != nil || readonlyState.Value != "on" ||
			replicationZFSPropertySourceKind(readonlyState.Source) != "local" ||
			markerState.Value != change.FenceToken || replicationZFSPropertySourceKind(markerState.Source) != "local" {
			fenceErr = appendReplicationFenceDatasetError(
				fenceErr, dataset, errors.Join(readonlyErr, markerErr, fmt.Errorf("replication_emergency_fence_verification_failed")),
			)
		}
	}
	return fenceErr
}

func restoreReplicationEmergencyReadonlyDataset(
	ctx context.Context,
	dataset string,
	change replicationEmergencyReadonlyChange,
) (bool, error) {
	guidOutput, err := utils.RunCommandWithContext(ctx, "zfs", "get", "-H", "-o", "value", "guid", dataset)
	if err != nil {
		combined := fmt.Errorf("%s: %w", strings.TrimSpace(guidOutput), err)
		if isGZFSDatasetNotFoundError(combined) {
			return true, nil
		}
		return false, combined
	}
	if strings.TrimSpace(guidOutput) != strings.TrimSpace(change.DatasetGUID) {
		// The path was reused. Never apply an old journal to a new dataset.
		return true, nil
	}

	markerState, err := readReplicationZFSPropertyState(ctx, dataset, replicationEmergencyReadonlyProp)
	if err != nil {
		return false, err
	}
	if markerState.Value != strings.TrimSpace(change.FenceToken) ||
		replicationZFSPropertySourceKind(markerState.Source) != "local" {
		// The exact marker is the ownership proof for this property reversal.
		return true, nil
	}

	previousReadonly := replicationZFSPropertyState{
		Value: strings.TrimSpace(change.PreviousValue), Source: strings.TrimSpace(change.PreviousSource),
	}
	currentReadonly, err := readReplicationZFSPropertyState(ctx, dataset, "readonly")
	if err != nil {
		return false, err
	}
	if !replicationZFSPropertyStateMatches(currentReadonly, previousReadonly) {
		if currentReadonly.Value != "on" || replicationZFSPropertySourceKind(currentReadonly.Source) != "local" {
			return false, fmt.Errorf("replication_emergency_readonly_state_superseded")
		}
		if err := restoreReplicationZFSPropertyState(ctx, dataset, "readonly", previousReadonly); err != nil {
			return false, err
		}
	}

	previousMarker := replicationZFSPropertyState{
		Value: strings.TrimSpace(change.PreviousMarkerValue), Source: strings.TrimSpace(change.PreviousMarkerSource),
	}
	if err := restoreReplicationZFSPropertyState(
		ctx, dataset, replicationEmergencyReadonlyProp, previousMarker,
	); err != nil {
		return false, err
	}
	return true, nil
}

func emergencyRestoreOperationToken(fenceToken string) string {
	return clusterModels.ReplicationGuestOperationEmergencyRestore + ":" + strings.TrimSpace(fenceToken)
}

func isEmergencyRestoreGuardDeferred(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"guest_operation_in_progress",
		"replication_policy_must_be_disabled",
		"replication_policy_deleting",
		"replication_policy_transition_in_progress",
		"replication_guest_lease_still_present",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (s *Service) reconcileReplicationEmergencyRestoreOperations(
	ctx context.Context,
	changes map[string]replicationEmergencyReadonlyChange,
) error {
	if s == nil || s.DB == nil || s.Cluster == nil {
		return nil
	}
	var operations []clusterModels.ReplicationGuestOperation
	if err := s.DB.Where("operation = ?", clusterModels.ReplicationGuestOperationEmergencyRestore).
		Find(&operations).Error; err != nil {
		return err
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("replication_local_node_id_unavailable")
	}
	activeTokens := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if strings.TrimSpace(change.FenceToken) != "" {
			activeTokens[emergencyRestoreOperationToken(change.FenceToken)] = struct{}{}
		}
	}
	var reconcileErr error
	for _, operation := range operations {
		if strings.TrimSpace(operation.OwnerNodeID) != localNodeID {
			continue
		}
		if _, active := activeTokens[strings.TrimSpace(operation.Token)]; active {
			continue
		}
		if err := s.releaseReplicationEmergencyRestoreGuard(
			ctx, operation.GuestType, operation.GuestID, operation.Token,
		); err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
		}
	}
	return reconcileErr
}

func (s *Service) restoreReplicationEmergencyReadonlyChanges(
	ctx context.Context,
	_ []clusterModels.ReplicationPolicy,
) error {
	replicationEmergencyReadonlyMu.Lock()
	changes, err := loadDurableReplicationEmergencyReadonlyChanges()
	if changes == nil && err == nil {
		changes = make(map[string]replicationEmergencyReadonlyChange)
	}
	replicationEmergencyReadonlyMu.Unlock()
	if err != nil {
		return fmt.Errorf("load_replication_emergency_readonly_failed: %w", err)
	}
	if len(changes) == 0 {
		return s.reconcileReplicationEmergencyRestoreOperations(ctx, changes)
	}

	type guestRestore struct {
		guestType  string
		guestID    uint
		fenceToken string
	}
	guests := make(map[string]guestRestore)
	var restoreErr error
	for key, change := range changes {
		if strings.TrimSpace(change.DatasetGUID) == "" || strings.TrimSpace(change.FenceToken) == "" ||
			change.GuestID == 0 || strings.TrimSpace(change.GuestType) == "" {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("replication_emergency_readonly_legacy_entry_%s", key))
			continue
		}
		guestKey := replicationGuestKey(change.GuestType, change.GuestID)
		candidate := guestRestore{guestType: change.GuestType, guestID: change.GuestID, fenceToken: change.FenceToken}
		if existing, ok := guests[guestKey]; !ok || candidate.fenceToken < existing.fenceToken {
			guests[guestKey] = candidate
		}
	}
	guestKeys := make([]string, 0, len(guests))
	for key := range guests {
		guestKeys = append(guestKeys, key)
	}
	sort.Strings(guestKeys)

	for _, guestKey := range guestKeys {
		guest := guests[guestKey]
		operationToken := emergencyRestoreOperationToken(guest.fenceToken)
		if err := s.acquireReplicationEmergencyRestoreGuard(
			ctx, guest.guestType, guest.guestID, operationToken,
		); err != nil {
			if !isEmergencyRestoreGuardDeferred(err) {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("replication_emergency_restore_guard_acquire_failed: %w", err))
			}
			continue
		}
		if err := s.waitReplicationEmergencyRestoreGuardApplied(
			ctx, guest.guestType, guest.guestID, operationToken,
		); err != nil {
			restoreErr = errors.Join(restoreErr, err)
			continue
		}

		replicationEmergencyReadonlyMu.Lock()
		current, loadErr := loadDurableReplicationEmergencyReadonlyChanges()
		if current == nil && loadErr == nil {
			current = make(map[string]replicationEmergencyReadonlyChange)
		}
		guestRestoreErr := loadErr
		dirty := false
		if loadErr == nil {
			byGUID, listErr := s.listReplicationDatasetGUIDIndex(ctx)
			if listErr != nil {
				guestRestoreErr = listErr
			} else {
				keys := make([]string, 0)
				for key, change := range current {
					if replicationGuestKey(change.GuestType, change.GuestID) == guestKey {
						keys = append(keys, key)
					}
				}
				sort.SliceStable(keys, func(i, j int) bool {
					left := current[keys[i]].Dataset
					right := current[keys[j]].Dataset
					leftDepth, rightDepth := strings.Count(left, "/"), strings.Count(right, "/")
					if leftDepth == rightDepth {
						return left < right
					}
					return leftDepth < rightDepth
				})
				for _, key := range keys {
					change := current[key]
					dataset, exists := byGUID[strings.TrimSpace(change.DatasetGUID)]
					if !exists {
						delete(current, key)
						dirty = true
						continue
					}
					restored, datasetErr := restoreReplicationEmergencyReadonlyDataset(ctx, dataset, change)
					if datasetErr != nil {
						guestRestoreErr = appendReplicationFenceDatasetError(guestRestoreErr, dataset, datasetErr)
						continue
					}
					if restored {
						delete(current, key)
						dirty = true
					}
				}
			}
		}
		if dirty && guestRestoreErr == nil {
			if persistErr := persistDurableReplicationEmergencyReadonlyChanges(current); persistErr != nil {
				guestRestoreErr = fmt.Errorf("persist_replication_emergency_readonly_failed: %w", persistErr)
			}
		}
		remainingForGuest := false
		for _, change := range current {
			if replicationGuestKey(change.GuestType, change.GuestID) == guestKey {
				remainingForGuest = true
				break
			}
		}
		replicationEmergencyReadonlyMu.Unlock()

		if guestRestoreErr != nil || remainingForGuest {
			restoreErr = errors.Join(restoreErr, guestRestoreErr)
			continue
		}
		if err := s.releaseReplicationEmergencyRestoreGuard(
			ctx, guest.guestType, guest.guestID, operationToken,
		); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("replication_emergency_restore_guard_release_failed: %w", err))
		}
	}
	return restoreErr
}

// selfFenceAllCanonicalGuests is the cold-start fallback for an unreadable
// policy database with no durable observations. In that state the process
// cannot distinguish protected from unprotected Sylve guests, so availability
// must yield to split-brain safety: stop every guest represented by a canonical
// local Sylve dataset and make its complete dataset tree readonly.
func (s *Service) selfFenceAllCanonicalGuests(ctx context.Context, localNodeID string) error {
	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return fmt.Errorf("replication_cold_fence_dataset_list_failed: %w", err)
	}

	type discoveredGuest struct {
		guestType string
		guestID   uint
		roots     map[string]struct{}
	}
	guests := make(map[string]*discoveredGuest)
	for _, dataset := range datasets {
		if isReplicationLineageDatasetPath(dataset) {
			continue
		}
		guestType, guestID, root, canonical := parseCanonicalReplicationGuestDataset(dataset)
		if !canonical {
			continue
		}
		key := replicationGuestKey(guestType, guestID)
		guest := guests[key]
		if guest == nil {
			guest = &discoveredGuest{guestType: guestType, guestID: guestID, roots: make(map[string]struct{})}
			guests[key] = guest
		}
		guest.roots[root] = struct{}{}
	}

	var fenceErr error
	for _, guest := range guests {
		if stopErr := s.emergencyStopReplicationGuest(guest.guestType, guest.guestID); stopErr != nil {
			fenceErr = appendReplicationStepError(fenceErr, "replication_cold_fence_guest_stop_failed", stopErr)
		}
		roots := make([]string, 0, len(guest.roots))
		for root := range guest.roots {
			roots = append(roots, root)
		}
		sort.Strings(roots)
		if datasetErr := s.fenceReplicationEmergencyReadonlyRoots(
			ctx, guest.guestType, guest.guestID, roots,
		); datasetErr != nil {
			fenceErr = appendReplicationStepError(fenceErr, "replication_cold_fence_guest_datasets_failed", datasetErr)
		}
		logger.L.Error().
			Str("node_id", strings.TrimSpace(localNodeID)).
			Str("guest_type", guest.guestType).
			Uint("guest_id", guest.guestID).
			Msg("replication_cold_start_guest_fenced_without_policy_state")
	}
	return fenceErr
}

func (s *Service) selfFenceUnknownProvenanceGuests(
	ctx context.Context,
	localNodeID string,
	known map[uint]replicationFenceObservation,
) error {
	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return fmt.Errorf("replication_cold_fence_dataset_list_failed: %w", err)
	}
	seen := make(map[uint]struct{})
	for _, dataset := range datasets {
		guestType, guestID, _, canonical := parseCanonicalReplicationGuestDataset(dataset)
		if !canonical {
			continue
		}
		policyValue, propertyErr := readLocalReplicationProperty(ctx, dataset, replicationPropertyPolicyID)
		if propertyErr != nil {
			continue
		}
		policyID64, parseErr := strconv.ParseUint(strings.TrimSpace(policyValue), 10, 64)
		if parseErr != nil || policyID64 == 0 || policyID64 > uint64(^uint(0)) {
			continue
		}
		policyID := uint(policyID64)
		if _, ok := known[policyID]; ok {
			continue
		}
		if _, ok := seen[policyID]; ok {
			continue
		}
		seen[policyID] = struct{}{}
		policy := clusterModels.ReplicationPolicy{
			ID: policyID, GuestType: guestType, GuestID: guestID,
			ActiveNodeID: localNodeID, SourceNodeID: localNodeID,
		}
		s.selfFenceReplicationPolicy(
			ctx, &policy, localNodeID, localNodeID, replicationFenceReasonOwnerLeaseInvalid, false,
		)
	}
	return nil
}

func (s *Service) selfFenceFromCachedObservations(
	ctx context.Context,
	localNodeID string,
	now time.Time,
	observations map[uint]replicationFenceObservation,
) {
	for _, observation := range observations {
		policy := observation.Policy
		expectedOwner := replicationPolicyOwnerNode(&policy)
		if expectedOwner == "" {
			continue
		}
		expectedEpoch := replicationPolicyOwnerEpoch(&policy)
		if replicationFenceObservationLeaseValid(
			observation,
			localNodeID,
			expectedOwner,
			expectedEpoch,
			now,
		) {
			continue
		}
		reason := replicationFenceReasonPolicyOwnerMismatch
		if strings.TrimSpace(expectedOwner) == strings.TrimSpace(localNodeID) {
			reason = replicationFenceReasonOwnerLeaseInvalid
		}
		s.selfFenceReplicationPolicy(ctx, &policy, localNodeID, expectedOwner, reason, false)
	}
}

func (s *Service) handleReplicationPolicyReadFailure(
	ctx context.Context,
	policyReadErr error,
	localNodeID string,
	now time.Time,
	previousObservations map[uint]replicationFenceObservation,
) error {
	// Runtime fail-stop is deliberately first. Dataset discovery may be slow or
	// unavailable, and no such failure may leave a running guest behind.
	runtimeFenceErr := s.emergencyStopAllManagedGuestRuntimes(ctx)

	// Record and fence every exact canonical Sylve guest tree before the cached
	// policy path can change readonly state. This preserves the pre-emergency
	// value needed to restore unprotected guests after the database recovers.
	canonicalFenceErr := s.selfFenceAllCanonicalGuests(ctx, localNodeID)

	// Preserve the more precise cached-policy path for metadata retirement. A
	// non-empty cache can still omit a policy enabled immediately before the
	// database became unreadable, which is why canonical discovery remains
	// mandatory above.
	s.selfFenceFromCachedObservations(ctx, localNodeID, now, previousObservations)
	discoveryErr := s.selfFenceUnknownProvenanceGuests(ctx, localNodeID, previousObservations)

	return errors.Join(policyReadErr, runtimeFenceErr, canonicalFenceErr, discoveryErr)
}

func (s *Service) selfFenceExpiredLeases(ctx context.Context) error {
	if s.Cluster == nil {
		return nil
	}
	return s.selfFenceExpiredLeasesForLocalNode(ctx, strings.TrimSpace(s.Cluster.LocalNodeID()))
}

func (s *Service) selfFenceExpiredLeasesForLocalNode(ctx context.Context, localNodeID string) error {
	localNodeID = strings.TrimSpace(localNodeID)
	now := s.now().UTC()
	previousObservations := s.snapshotReplicationFenceCache()
	if len(previousObservations) == 0 {
		if durable, err := loadDurableReplicationFenceObservations(); err == nil {
			previousObservations = durable
			s.replaceReplicationFenceCache(durable)
		} else {
			logger.L.Warn().Err(err).Msg("replication_durable_fence_observations_load_failed")
		}
	}
	if localNodeID == "" {
		return s.handleReplicationPolicyReadFailure(
			ctx,
			fmt.Errorf("replication_local_node_id_unavailable"),
			localNodeID,
			now,
			previousObservations,
		)
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		// A transient local DB failure must not extend write authority forever.
		// The last successfully observed lease deadline remains the hard bound.
		return s.handleReplicationPolicyReadFailure(
			ctx,
			err,
			localNodeID,
			now,
			previousObservations,
		)
	}

	observations := make(map[uint]replicationFenceObservation, len(policies))
	for _, policy := range policies {
		expectedOwner := strings.TrimSpace(replicationPolicyOwnerNode(&policy))
		if expectedOwner == "" {
			continue
		}
		expectedEpoch := replicationPolicyOwnerEpoch(&policy)
		observation := replicationFenceObservation{Policy: policy}
		if previous, ok := previousObservations[policy.ID]; ok &&
			replicationPolicyOwnerNode(&previous.Policy) == expectedOwner &&
			replicationPolicyOwnerEpoch(&previous.Policy) == expectedEpoch {
			observation.LeaseOwner = previous.LeaseOwner
			observation.LeaseEpoch = previous.LeaseEpoch
			observation.LeaseExpiresAt = previous.LeaseExpiresAt
		}

		fenceReason := replicationFenceReasonPolicyOwnerMismatch
		if expectedOwner == localNodeID {
			if expectedEpoch == 0 {
				observations[policy.ID] = observation
				s.selfFenceReplicationPolicy(ctx, &policy, localNodeID, expectedOwner, replicationFenceReasonOwnerLeaseInvalid, true)
				continue
			}

			lease, leaseLookupErr := s.Cluster.GetReplicationLeaseByPolicyID(policy.ID)
			if leaseLookupErr == nil && lease != nil {
				observation.LeaseOwner = strings.TrimSpace(lease.OwnerNodeID)
				observation.LeaseEpoch = lease.OwnerEpoch
				observation.LeaseExpiresAt = lease.ExpiresAt.UTC()
			} else if errors.Is(leaseLookupErr, gorm.ErrRecordNotFound) {
				graceAnchor := policy.CreatedAt
				// Preserve the legacy creation grace as an explicit bounded
				// observation. A missing lease can never extend a previously
				// observed deadline, and ordinary policy edits do not reset it.
				observation.LeaseOwner = localNodeID
				observation.LeaseEpoch = expectedEpoch
				observation.LeaseExpiresAt = replicationFenceMissingLeaseDeadline(
					observation.LeaseExpiresAt, graceAnchor,
				)
			} else if leaseLookupErr != nil {
				logger.L.Warn().
					Err(leaseLookupErr).
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Str("guest_type", strings.TrimSpace(policy.GuestType)).
					Msg("replication_self_fence_local_owner_lease_lookup_failed_using_cached_deadline")
			}
			observations[policy.ID] = observation

			if replicationFenceObservationLeaseValid(
				observation,
				localNodeID,
				expectedOwner,
				expectedEpoch,
				now,
			) {
				// Exact transition activation owns writable cutover. The watchdog
				// may fence during a transition, but never unfences merely because
				// the newly committed lease is valid.
				if !transitionStateInProgress(policy.TransitionState) {
					if err := s.unfenceReplicationGuestDatasetsIfNeeded(ctx, &policy); err != nil {
						logger.L.Warn().Err(err).
							Uint("policy_id", policy.ID).
							Uint("guest_id", policy.GuestID).
							Str("guest_type", strings.TrimSpace(policy.GuestType)).
							Msg("replication_self_fence_unfence_failed")
					}
				}
				continue
			}
			fenceReason = replicationFenceReasonOwnerLeaseInvalid
		} else {
			observations[policy.ID] = observation
		}

		s.selfFenceReplicationPolicy(ctx, &policy, localNodeID, expectedOwner, fenceReason, true)
	}

	// A successful policy enumeration is authoritative: remove cache entries
	// for policies that were disabled or deleted, and retain only this pass.
	s.replaceReplicationFenceCache(observations)
	if err := persistDurableReplicationFenceObservations(observations); err != nil {
		logger.L.Warn().Err(err).Msg("replication_durable_fence_observations_persist_failed")
	}
	if err := s.restoreReplicationEmergencyReadonlyChanges(ctx, policies); err != nil {
		return fmt.Errorf("replication_emergency_readonly_restore_failed: %w", err)
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
		if transitionStateInProgress(policy.TransitionState) ||
			strings.EqualFold(strings.TrimSpace(policy.ProtectionState), clusterModels.ReplicationProtectionStateDeleting) ||
			strings.EqualFold(strings.TrimSpace(policy.ProtectionState), clusterModels.ReplicationProtectionStateSuspended) {
			// Transition recovery owns the exact desired state. Consuming crash
			// attempts here can enqueue a competing failover while promotion is
			// still being reconciled.
			s.crashMissesReset(policy.ID)
			continue
		}
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
			// A control-plane or hypervisor error is not evidence that the
			// guest stopped.  Treat the state as unknown and do not consume a
			// crash-recovery attempt or initiate failover.
			continue
		}

		if running {
			s.crashMissesReset(policy.ID)
			continue
		}

		if policy.GuestType == clusterModels.ReplicationGuestTypeVM {
			vm, lookupErr := s.findVMByRID(policy.GuestID)
			if lookupErr != nil {
				logger.L.Warn().
					Err(lookupErr).
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Msg("replication_crash_recovery_vm_registration_check_failed")
				continue
			}
			if vm == nil {
				// The active owner still has the replicated dataset, so restore its
				// local VM registration before considering a cross-node failover.
				logger.L.Warn().
					Uint("policy_id", policy.ID).
					Uint("guest_id", policy.GuestID).
					Msg("replication_crash_recovery_rehydrating_missing_vm_registration")
				if err := s.activateReplicationVMWithRegistrationRecovery(ctx, policy.GuestID, "", true, true); err != nil {
					logger.L.Warn().
						Err(err).
						Uint("policy_id", policy.ID).
						Uint("guest_id", policy.GuestID).
						Msg("replication_crash_recovery_vm_registration_rehydrate_failed")
					continue
				}
				s.crashMissesReset(policy.ID)
				continue
			}
		}

		crashLimit := policy.CrashRestartMax
		if crashLimit < 1 {
			crashLimit = replicationCrashRestartLimit
		}
		crashVal := s.crashMissesIncr(policy.ID, uint64(crashLimit+1))
		if shouldAttemptLocalCrashRestart(crashVal, crashLimit) {
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

		logger.L.Warn().
			Uint("policy_id", policy.ID).
			Uint("guest_id", policy.GuestID).
			Msg("replication_guest_crash_restart_exhausted_initiating_failover")

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

func shouldAttemptLocalCrashRestart(crashObservation uint64, crashLimit int) bool {
	return crashLimit > 0 && crashObservation > 0 && crashObservation <= uint64(crashLimit)
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

func replicationPoolHealthCounterKey(policyID uint, pool string) string {
	return fmt.Sprintf("%d:%s", policyID, strings.TrimSpace(pool))
}

func (s *Service) replicationGuestPools(policy *clusterModels.ReplicationPolicy) ([]string, error) {
	if s == nil || s.DB == nil || policy == nil || policy.GuestID == 0 {
		return nil, nil
	}

	pools := make([]string, 0)
	seen := make(map[string]struct{})
	appendPool := func(pool string) {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			return
		}
		if _, exists := seen[pool]; exists {
			return
		}
		seen[pool] = struct{}{}
		pools = append(pools, pool)
	}

	switch strings.TrimSpace(policy.GuestType) {
	case clusterModels.ReplicationGuestTypeVM:
		var rawPools []string
		if err := s.DB.Model(&vmModels.Storage{}).
			Select("DISTINCT vm_storages.pool").
			Joins("JOIN vms ON vms.id = vm_storages.vm_id").
			Where("vms.rid = ? AND vm_storages.pool != ''", policy.GuestID).
			Pluck("pool", &rawPools).Error; err != nil {
			return nil, fmt.Errorf("replication_vm_storage_pool_query_failed: %w", err)
		}
		for _, pool := range rawPools {
			appendPool(pool)
		}

		var datasetPools []string
		if err := s.DB.Model(&vmModels.VMStorageDataset{}).
			Select("DISTINCT vm_storage_datasets.pool").
			Joins("JOIN vm_storages ON vm_storages.dataset_id = vm_storage_datasets.id").
			Joins("JOIN vms ON vms.id = vm_storages.vm_id").
			Where("vms.rid = ? AND vm_storage_datasets.pool != ''", policy.GuestID).
			Pluck("pool", &datasetPools).Error; err != nil {
			return nil, fmt.Errorf("replication_vm_dataset_pool_query_failed: %w", err)
		}
		for _, pool := range datasetPools {
			appendPool(pool)
		}

	case clusterModels.ReplicationGuestTypeJail:
		var jail jailModels.Jail
		if err := s.DB.Preload("Storages").Where("ct_id = ?", policy.GuestID).First(&jail).Error; err != nil {
			return nil, fmt.Errorf("replication_jail_storage_pool_query_failed: %w", err)
		}
		for _, storage := range jail.Storages {
			appendPool(storage.Pool)
		}
	}

	sort.Strings(pools)
	return pools, nil
}

func (s *Service) checkLocalPoolHealth(ctx context.Context, localNodeID string) {
	if s.GZFS == nil || s.GZFS.Zpool == nil {
		return
	}
	if s.poolDownMisses == nil {
		s.poolDownMisses = make(map[string]int)
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		logger.L.Warn().Err(err).Msg("replication_pool_health_policy_query_failed")
		return
	}

	poolCache := make(map[string]*gzfs.ZPool)
	for _, policy := range policies {
		if transitionStateInProgress(policy.TransitionState) || s.IsPolicyTransitionRunning(policy.ID) ||
			strings.EqualFold(strings.TrimSpace(policy.ProtectionState), clusterModels.ReplicationProtectionStateDeleting) ||
			strings.EqualFold(strings.TrimSpace(policy.ProtectionState), clusterModels.ReplicationProtectionStateSuspended) {
			continue
		}
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

		pools, err := s.replicationGuestPools(&policy)
		if err != nil {
			logger.L.Warn().Err(err).
				Uint("policy_id", policy.ID).
				Uint("guest_id", policy.GuestID).
				Msg("replication_pool_health_guest_pool_query_failed")
			continue
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
			}

			counterKey := replicationPoolHealthCounterKey(policy.ID, pool)
			state := p.State
			healthy := state == gzfs.ZPoolStateOnline || state == ""
			if healthy {
				s.poolDownMisses[counterKey] = 0
			} else {
				misses := s.poolDownMisses[counterKey] + 1
				if misses > 3 {
					misses = 3
				}
				s.poolDownMisses[counterKey] = misses
				if misses >= 3 {
					logger.L.Warn().
						Str("pool", pool).
						Str("state", string(state)).
						Uint("policy_id", policy.ID).
						Msg("replication_pool_unhealthy_initiating_failover")

					if err := s.enqueueFailoverToLeader(policy.ID, "", replicationFailoverRequestForce, true, false); err != nil {
						logger.L.Warn().Err(err).Str("pool", pool).Msg("replication_pool_unhealthy_failover_enqueue_failed")
					} else {
						s.poolDownMisses[counterKey] = 0
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

					if err := s.enqueueFailoverToLeader(policy.ID, "", replicationFailoverRequestSafe, false, false); err != nil {
						logger.L.Warn().Err(err).Str("pool", pool).Msg("replication_pool_capacity_failover_enqueue_failed")
					}
				}
			}
		}
	}
}
