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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) runtimeStateBypassRaft() (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("runtime_state_database_unavailable")
	}
	if s.Cluster != nil {
		return s.Cluster.RuntimeStateBypassRaft()
	}
	// Still inspect persisted state when the cluster service itself has not
	// been wired. An enabled cluster must fail closed in this condition.
	probe := &cluster.Service{DB: s.DB}
	return probe.RuntimeStateBypassRaft()
}

func (s *Service) applyBackupJobScheduleDecision(
	decision clusterModels.BackupJobScheduleDecision,
	bypassRaft bool,
) error {
	if s.Cluster != nil {
		return s.Cluster.ApplyBackupJobScheduleDecision(decision, bypassRaft)
	}
	if !bypassRaft {
		return fmt.Errorf("cluster_service_unavailable")
	}
	return clusterModels.ApplyBackupJobScheduleDecisionTxn(s.DB, &decision)
}

func (s *Service) applyReplicationPolicyScheduleDecision(
	decision clusterModels.ReplicationPolicyScheduleDecision,
	bypassRaft bool,
) error {
	if s.Cluster != nil {
		return s.Cluster.ApplyReplicationPolicyScheduleDecision(decision, bypassRaft)
	}
	if !bypassRaft {
		return fmt.Errorf("cluster_service_unavailable")
	}
	return clusterModels.ApplyReplicationPolicyScheduleDecisionTxn(s.DB, &decision)
}

func (s *Service) deliverBackupJobRunResult(result clusterModels.BackupJobRunResult) error {
	bypassRaft, err := s.runtimeStateBypassRaft()
	if err != nil {
		return err
	}
	if s.Cluster == nil {
		if !bypassRaft {
			return fmt.Errorf("cluster_service_unavailable")
		}
		return clusterModels.CompleteBackupJobRunTxn(s.DB, &result)
	}
	update := cluster.BackupJobRuntimeStateUpdate{
		Version: cluster.BackupJobRuntimeStateVersion,
		JobID:   result.JobID, Token: result.Token, HolderNodeID: result.HolderNodeID,
		ScheduleRevision: result.ScheduleRevision, LastRunAt: &result.CompletedAt,
		LastStatus: result.LastStatus, LastError: result.LastError,
		NextRunAt: result.NextRunAt, Encrypted: result.Encrypted,
	}
	err = s.Cluster.UpdateBackupJobRuntimeState(update, bypassRaft)
	if err == nil {
		return nil
	}
	if !bypassRaft && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		return s.forwardBackupJobStateToLeader(update)
	}
	return err
}

func (s *Service) deliverReplicationPolicyRunResult(result clusterModels.ReplicationPolicyRunResult) error {
	bypassRaft, err := s.runtimeStateBypassRaft()
	if err != nil {
		return err
	}
	if s.Cluster == nil {
		if !bypassRaft {
			return fmt.Errorf("cluster_service_unavailable")
		}
		return clusterModels.CompleteReplicationPolicyRunTxn(s.DB, &result)
	}
	update := cluster.ReplicationPolicyRuntimeState{
		ID: result.PolicyID, Token: result.Token, HolderNodeID: result.HolderNodeID,
		ScheduleRevision: result.ScheduleRevision, OwnerEpoch: result.OwnerEpoch,
		LastRunAt: &result.CompletedAt, LastStatus: result.LastStatus,
		LastError: result.LastError, NextRunAt: result.NextRunAt,
	}
	err = s.Cluster.ProposeReplicationPolicyStateUpdate(update, bypassRaft)
	if err == nil {
		return nil
	}
	if !bypassRaft && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		return s.forwardReplicationPolicyStateToLeader(update)
	}
	return err
}

func (s *Service) storeScheduledRunResult(kind string, objectID uint, token string, result any) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("scheduled_run_outbox_database_unavailable")
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	row := clusterModels.ScheduledRunResultOutbox{
		Token: strings.TrimSpace(token), Kind: kind, ObjectID: objectID, Payload: string(payload),
	}
	return s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (s *Service) drainScheduledRunResultOutbox() error {
	if s == nil || s.DB == nil || !s.DB.Migrator().HasTable(&clusterModels.ScheduledRunResultOutbox{}) {
		return nil
	}
	var rows []clusterModels.ScheduledRunResultOutbox
	if err := s.DB.Order("created_at ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	var combined error
	for _, row := range rows {
		var deliveryErr error
		switch row.Kind {
		case clusterModels.ScheduledRunKindBackup:
			var result clusterModels.BackupJobRunResult
			if err := json.Unmarshal([]byte(row.Payload), &result); err != nil {
				deliveryErr = fmt.Errorf("decode_backup_run_result_%s: %w", row.Token, err)
			} else {
				deliveryErr = s.deliverBackupJobRunResult(result)
			}
		case clusterModels.ScheduledRunKindReplication:
			var result clusterModels.ReplicationPolicyRunResult
			if err := json.Unmarshal([]byte(row.Payload), &result); err != nil {
				deliveryErr = fmt.Errorf("decode_replication_run_result_%s: %w", row.Token, err)
			} else {
				deliveryErr = s.deliverReplicationPolicyRunResult(result)
			}
		default:
			deliveryErr = fmt.Errorf("invalid_scheduled_run_outbox_kind")
		}
		if deliveryErr != nil {
			combined = errors.Join(combined, deliveryErr)
			continue
		}
		if err := s.DB.Where("token = ?", row.Token).
			Delete(&clusterModels.ScheduledRunResultOutbox{}).Error; err != nil {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}

func (s *Service) DrainScheduledRunResultOutbox() error {
	return s.drainScheduledRunResultOutbox()
}

func (s *Service) completeBackupJobOperation(
	job *clusterModels.BackupJob,
	status, lastError string,
	completedAt time.Time,
	nextRunAt *time.Time,
	encrypted *bool,
) (bool, error) {
	var operation clusterModels.BackupJobOperation
	err := s.DB.Where("job_id = ?", job.ID).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var receiptCount int64
		if countErr := s.DB.Model(&clusterModels.ScheduledRunReceipt{}).
			Where("kind = ? AND object_id = ?", clusterModels.ScheduledRunKindBackup, job.ID).
			Count(&receiptCount).Error; countErr == nil && receiptCount != 0 {
			return true, nil
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if operation.Operation != clusterModels.BackupJobOperationBackup {
		return false, nil
	}
	result := clusterModels.BackupJobRunResult{
		JobID: job.ID, Token: operation.Token, HolderNodeID: operation.HolderNodeID,
		ScheduleRevision: operation.ScheduleRevision, CompletedAt: completedAt,
		LastStatus: status, LastError: lastError, NextRunAt: nextRunAt, Encrypted: encrypted,
	}
	bypassRaft, authorityErr := s.runtimeStateBypassRaft()
	if authorityErr != nil {
		if strings.Contains(strings.ToLower(authorityErr.Error()), "cluster_enabled") {
			if err := s.storeScheduledRunResult(
				clusterModels.ScheduledRunKindBackup, job.ID, operation.Token, result,
			); err != nil {
				return true, errors.Join(authorityErr, err)
			}
		}
		return true, authorityErr
	}
	if bypassRaft {
		return true, s.deliverBackupJobRunResult(result)
	}
	if err := s.storeScheduledRunResult(
		clusterModels.ScheduledRunKindBackup, job.ID, operation.Token, result,
	); err != nil {
		return true, err
	}
	return true, s.drainScheduledRunResultOutbox()
}

func (s *Service) completeReplicationOperation(
	policy *clusterModels.ReplicationPolicy,
	status, lastError string,
	completedAt time.Time,
	nextRunAt *time.Time,
) (bool, error) {
	var operation clusterModels.ReplicationRunOperation
	err := s.DB.Where("policy_id = ?", policy.ID).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var receiptCount int64
		if countErr := s.DB.Model(&clusterModels.ScheduledRunReceipt{}).
			Where("kind = ? AND object_id = ?", clusterModels.ScheduledRunKindReplication, policy.ID).
			Count(&receiptCount).Error; countErr == nil && receiptCount != 0 {
			return true, nil
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result := clusterModels.ReplicationPolicyRunResult{
		PolicyID: policy.ID, Token: operation.Token, HolderNodeID: operation.HolderNodeID,
		ScheduleRevision: operation.ScheduleRevision, OwnerEpoch: operation.OwnerEpoch,
		CompletedAt: completedAt, LastStatus: status, LastError: lastError, NextRunAt: nextRunAt,
	}
	bypassRaft, authorityErr := s.runtimeStateBypassRaft()
	if authorityErr != nil {
		if strings.Contains(strings.ToLower(authorityErr.Error()), "cluster_enabled") {
			if err := s.storeScheduledRunResult(
				clusterModels.ScheduledRunKindReplication, policy.ID, operation.Token, result,
			); err != nil {
				return true, errors.Join(authorityErr, err)
			}
		}
		return true, authorityErr
	}
	if bypassRaft {
		return true, s.deliverReplicationPolicyRunResult(result)
	}
	if err := s.storeScheduledRunResult(
		clusterModels.ScheduledRunKindReplication, policy.ID, operation.Token, result,
	); err != nil {
		return true, err
	}
	return true, s.drainScheduledRunResultOutbox()
}

func (s *Service) logScheduledResultDeliveryFailure(kind string, objectID uint, err error) {
	if err == nil {
		return
	}
	logger.L.Warn().Err(err).Str("kind", kind).Uint("object_id", objectID).
		Msg("scheduled_run_result_deferred")
}

func (s *Service) acquireReplicationRunOperation(
	policy *clusterModels.ReplicationPolicy,
	scheduled bool,
	occurrenceAt time.Time,
	nextRunAt *time.Time,
	publishAfter *time.Time,
	bypassRaft bool,
) (*clusterModels.ReplicationRunOperation, error) {
	if policy == nil || policy.ID == 0 {
		return nil, fmt.Errorf("invalid_policy_id")
	}
	holder := strings.TrimSpace(s.replicationRunnerNodeID(policy))
	if holder == "" {
		holder = strings.TrimSpace(s.localNodeID())
	}
	if holder == "" && bypassRaft {
		holder = "local"
	}
	if holder == "" {
		return nil, fmt.Errorf("replication_run_holder_unavailable")
	}
	now := s.now().UTC()
	if occurrenceAt.IsZero() {
		occurrenceAt = now
	}
	token := fmt.Sprintf("replication:%s:%s", holder, uuid.NewString())
	decision := clusterModels.ReplicationPolicyScheduleDecision{
		PolicyID: policy.ID, ExpectedScheduleRevision: policy.ScheduleRevision,
		ExpectedOwnerEpoch: policy.OwnerEpoch, ExpectedNextRunAt: policy.NextRunAt,
		NextRunAt: nextRunAt, DecidedAt: now, ClaimToken: token,
		HolderNodeID: holder, Scheduled: scheduled, OccurrenceAt: &occurrenceAt,
		PublishAfter: publishAfter,
	}
	if err := s.applyReplicationPolicyScheduleDecision(decision, bypassRaft); err != nil {
		return nil, err
	}
	var operation clusterModels.ReplicationRunOperation
	if err := s.DB.Where("token = ?", token).First(&operation).Error; err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *Service) startReplicationRunOperation(operation *clusterModels.ReplicationRunOperation) error {
	if operation == nil {
		return fmt.Errorf("replication_run_operation_required")
	}
	now := s.now().UTC()
	bypassRaft, err := s.runtimeStateBypassRaft()
	if err != nil {
		return err
	}
	transition := clusterModels.ReplicationRunOperationTransition{
		PolicyID: operation.PolicyID, Token: operation.Token,
		HolderNodeID: operation.HolderNodeID, OccurredAt: now,
	}
	if s.Cluster == nil {
		if !bypassRaft {
			return fmt.Errorf("cluster_service_unavailable")
		}
		return clusterModels.StartReplicationRunOperationTxn(s.DB, &transition)
	}
	err = s.Cluster.StartReplicationRun(transition, bypassRaft)
	if err == nil {
		return nil
	}
	if !bypassRaft && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		return s.forwardReplicationPolicyStateToLeader(cluster.ReplicationPolicyRuntimeState{
			Action: "start", ID: operation.PolicyID, Token: operation.Token,
			HolderNodeID: operation.HolderNodeID, LastRunAt: &now,
		})
	}
	return err
}

func (s *Service) prepareReplicationRunOperation(
	policyID uint,
	token, holder string,
) (*clusterModels.ReplicationRunOperation, bool, error) {
	token = strings.TrimSpace(token)
	holder = strings.TrimSpace(holder)
	if token == "" {
		var policy clusterModels.ReplicationPolicy
		if err := s.DB.First(&policy, policyID).Error; err != nil {
			return nil, false, err
		}
		bypassRaft, err := s.runtimeStateBypassRaft()
		if err != nil {
			return nil, false, err
		}
		operation, err := s.acquireReplicationRunOperation(
			&policy, false, s.now().UTC(), policy.NextRunAt, nil, bypassRaft,
		)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already_running") {
				return nil, false, nil
			}
			return nil, false, err
		}
		token = operation.Token
		holder = operation.HolderNodeID
	}

	var operation clusterModels.ReplicationRunOperation
	err := s.DB.Where("policy_id = ? AND token = ?", policyID, token).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if holder != "" && operation.HolderNodeID != holder {
		return nil, false, nil
	}
	localHolder := strings.TrimSpace(s.localNodeID())
	if localHolder == "" {
		localHolder = "local"
	}
	if operation.HolderNodeID != localHolder {
		return nil, false, nil
	}
	if err := s.startReplicationRunOperation(&operation); err != nil {
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "not_found") || strings.Contains(text, "token_mismatch") {
			return nil, false, nil
		}
		if strings.Contains(text, "schedule_stale") {
			var policy clusterModels.ReplicationPolicy
			if loadErr := s.DB.First(&policy, policyID).Error; loadErr != nil {
				return nil, false, loadErr
			}
			handled, completionErr := s.completeReplicationOperation(
				&policy, "interrupted", "replication_schedule_changed_before_start",
				s.now().UTC(), policy.NextRunAt,
			)
			if handled {
				s.logScheduledResultDeliveryFailure(
					clusterModels.ScheduledRunKindReplication, policyID, completionErr,
				)
				return nil, false, nil
			}
			return nil, false, completionErr
		}
		return nil, false, err
	}
	operation.State = clusterModels.ReplicationRunOperationRunning
	return &operation, true, nil
}

func (s *Service) RepublishQueuedReplicationRuns(ctx context.Context) error {
	return s.reconcileReplicationRuns(ctx, true)
}

// ReconcileReplicationRunsAfterRestart replays interrupted running work once
// at startup. Steady-state passes republish queued rows only.
func (s *Service) ReconcileReplicationRunsAfterRestart(ctx context.Context) error {
	return s.reconcileReplicationRuns(ctx, false)
}

func (s *Service) reconcileReplicationRuns(ctx context.Context, queuedOnly bool) error {
	if s == nil || s.DB == nil || !s.DB.Migrator().HasTable(&clusterModels.ReplicationRunOperation{}) {
		return nil
	}
	holder := strings.TrimSpace(s.localNodeID())
	if holder == "" {
		holder = "local"
	}
	now := s.now().UTC()
	var operations []clusterModels.ReplicationRunOperation
	query := s.DB.Where("holder_node_id = ?", holder)
	if queuedOnly {
		query = query.Where("state = ? AND (publish_after IS NULL OR publish_after <= ?)",
			clusterModels.ReplicationRunOperationQueued, now)
	} else {
		query = query.Where("state != ? OR publish_after IS NULL OR publish_after <= ?",
			clusterModels.ReplicationRunOperationQueued, now)
	}
	if err := query.Order("policy_id ASC").Find(&operations).Error; err != nil {
		return err
	}
	var combined error
	for _, operation := range operations {
		if err := db.EnqueueJSON(ctx, replicationJobQueueName, replicationJobPayload{
			PolicyID: operation.PolicyID, OperationToken: operation.Token,
			HolderNodeID: operation.HolderNodeID,
		}); err != nil {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}
