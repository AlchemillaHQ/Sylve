// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ScheduledRunKindBackup      = "backup"
	ScheduledRunKindReplication = "replication"

	ReplicationRunOperationQueued  = "queued"
	ReplicationRunOperationRunning = "running"
)

// ReplicationRunOperation is the durable, replicated outbox/exclusion row for
// one replication-policy run. Queue delivery is intentionally outside Raft;
// the assigned holder republishes this exact token until it is terminal.
type ReplicationRunOperation struct {
	PolicyID         uint       `gorm:"primaryKey;autoIncrement:false" json:"policyId"`
	Token            string     `gorm:"uniqueIndex;not null" json:"token"`
	State            string     `gorm:"index;not null" json:"state"`
	HolderNodeID     string     `gorm:"index;not null" json:"holderNodeId"`
	Scheduled        bool       `gorm:"index;not null;default:false" json:"scheduled"`
	OccurrenceAt     *time.Time `gorm:"index" json:"occurrenceAt,omitempty"`
	ScheduleRevision uint64     `gorm:"not null;default:0" json:"scheduleRevision"`
	OwnerEpoch       uint64     `gorm:"not null;default:0" json:"ownerEpoch"`
	PublishAfter     *time.Time `gorm:"index" json:"publishAfter,omitempty"`
	Revision         uint64     `gorm:"not null;default:1" json:"revision"`
	AcquiredAt       time.Time  `gorm:"index;not null" json:"acquiredAt"`
	UpdatedAt        time.Time  `gorm:"index;not null" json:"updatedAt"`
}

// ScheduledRunReceipt makes terminal delivery idempotent. Applied is false
// when a later configuration, placement, or ownership revision fenced the
// result; the historical outcome remains available without mutating current
// runtime state.
type ScheduledRunReceipt struct {
	Token            string     `gorm:"primaryKey" json:"token"`
	Kind             string     `gorm:"index;not null" json:"kind"`
	ObjectID         uint       `gorm:"index;not null;autoIncrement:false" json:"objectId"`
	HolderNodeID     string     `gorm:"index;not null" json:"holderNodeId"`
	Scheduled        bool       `gorm:"index;not null;default:false" json:"scheduled"`
	OccurrenceAt     *time.Time `gorm:"index" json:"occurrenceAt,omitempty"`
	ScheduleRevision uint64     `gorm:"not null;default:0" json:"scheduleRevision"`
	OwnerEpoch       uint64     `gorm:"not null;default:0" json:"ownerEpoch"`
	Status           string     `gorm:"index;not null" json:"status"`
	Error            string     `gorm:"type:text" json:"error"`
	NextRunAt        *time.Time `json:"nextRunAt,omitempty"`
	Encrypted        *bool      `json:"encrypted,omitempty"`
	Applied          bool       `gorm:"index;not null;default:false" json:"applied"`
	CompletedAt      time.Time  `gorm:"index;not null" json:"completedAt"`
}

// ScheduledRunResultOutbox is node-local by design and must never be included
// in a cluster snapshot or resync. Payload is one BackupJobRunResult or
// ReplicationPolicyRunResult encoded as JSON.
type ScheduledRunResultOutbox struct {
	Token     string    `gorm:"primaryKey" json:"token"`
	Kind      string    `gorm:"index;not null" json:"kind"`
	ObjectID  uint      `gorm:"index;not null;autoIncrement:false" json:"objectId"`
	Payload   string    `gorm:"type:text;not null" json:"payload"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type BackupJobScheduleDecision struct {
	JobID                    uint       `json:"jobId"`
	ExpectedScheduleRevision uint64     `json:"expectedScheduleRevision"`
	ExpectedNextRunAt        *time.Time `json:"expectedNextRunAt"`
	NextRunAt                *time.Time `json:"nextRunAt"`
	DecidedAt                time.Time  `json:"decidedAt"`
	SetRuntime               bool       `json:"setRuntime,omitempty"`
	LastRunAt                *time.Time `json:"lastRunAt,omitempty"`
	LastStatus               string     `json:"lastStatus,omitempty"`
	LastError                string     `json:"lastError,omitempty"`
	ClaimToken               string     `json:"claimToken,omitempty"`
	HolderNodeID             string     `json:"holderNodeId,omitempty"`
	OccurrenceAt             *time.Time `json:"occurrenceAt,omitempty"`
	PublishAfter             *time.Time `json:"publishAfter,omitempty"`
}

type ReplicationPolicyScheduleDecision struct {
	PolicyID                 uint       `json:"policyId"`
	ExpectedScheduleRevision uint64     `json:"expectedScheduleRevision"`
	ExpectedOwnerEpoch       uint64     `json:"expectedOwnerEpoch"`
	ExpectedNextRunAt        *time.Time `json:"expectedNextRunAt"`
	NextRunAt                *time.Time `json:"nextRunAt"`
	DecidedAt                time.Time  `json:"decidedAt"`
	SetRuntime               bool       `json:"setRuntime,omitempty"`
	LastRunAt                *time.Time `json:"lastRunAt,omitempty"`
	LastStatus               string     `json:"lastStatus,omitempty"`
	LastError                string     `json:"lastError,omitempty"`
	ClaimToken               string     `json:"claimToken,omitempty"`
	HolderNodeID             string     `json:"holderNodeId,omitempty"`
	Scheduled                bool       `json:"scheduled,omitempty"`
	OccurrenceAt             *time.Time `json:"occurrenceAt,omitempty"`
	PublishAfter             *time.Time `json:"publishAfter,omitempty"`
}

type BackupJobRunResult struct {
	JobID            uint       `json:"jobId"`
	Token            string     `json:"token"`
	HolderNodeID     string     `json:"holderNodeId"`
	ScheduleRevision uint64     `json:"scheduleRevision"`
	CompletedAt      time.Time  `json:"completedAt"`
	LastStatus       string     `json:"lastStatus"`
	LastError        string     `json:"lastError"`
	NextRunAt        *time.Time `json:"nextRunAt"`
	Encrypted        *bool      `json:"encrypted,omitempty"`
}

type ReplicationPolicyRunResult struct {
	PolicyID         uint       `json:"policyId"`
	Token            string     `json:"token"`
	HolderNodeID     string     `json:"holderNodeId"`
	ScheduleRevision uint64     `json:"scheduleRevision"`
	OwnerEpoch       uint64     `json:"ownerEpoch"`
	CompletedAt      time.Time  `json:"completedAt"`
	LastStatus       string     `json:"lastStatus"`
	LastError        string     `json:"lastError"`
	NextRunAt        *time.Time `json:"nextRunAt"`
}

type ReplicationRunOperationTransition struct {
	PolicyID     uint      `json:"policyId"`
	Token        string    `json:"token"`
	HolderNodeID string    `json:"holderNodeId"`
	OccurredAt   time.Time `json:"occurredAt"`
}

func normalizeScheduleTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := NormalizeCommandTime(*value)
	return &normalized
}

func scheduleTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return NormalizeCommandTime(*left).Equal(NormalizeCommandTime(*right))
}

func validateScheduledStatus(status string) (string, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "success", "failed", "blocked", "degraded", "interrupted":
		return status, nil
	default:
		return "", fmt.Errorf("invalid_scheduled_run_status")
	}
}

func optionalBoolsEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func ApplyBackupJobScheduleDecisionTxn(db *gorm.DB, decision *BackupJobScheduleDecision) error {
	if db == nil || decision == nil || decision.JobID == 0 {
		return fmt.Errorf("backup_job_schedule_decision_invalid")
	}
	if decision.DecidedAt.IsZero() {
		return fmt.Errorf("backup_job_schedule_decided_at_required")
	}
	decision.DecidedAt = NormalizeCommandTime(decision.DecidedAt)
	decision.ExpectedNextRunAt = normalizeScheduleTime(decision.ExpectedNextRunAt)
	decision.NextRunAt = normalizeScheduleTime(decision.NextRunAt)
	decision.LastRunAt = normalizeScheduleTime(decision.LastRunAt)
	decision.OccurrenceAt = normalizeScheduleTime(decision.OccurrenceAt)
	decision.PublishAfter = normalizeScheduleTime(decision.PublishAfter)
	decision.ClaimToken = strings.TrimSpace(decision.ClaimToken)
	decision.HolderNodeID = strings.TrimSpace(decision.HolderNodeID)
	decision.LastError = strings.TrimSpace(decision.LastError)
	if decision.SetRuntime {
		status, err := validateScheduledStatus(decision.LastStatus)
		if err != nil {
			return err
		}
		decision.LastStatus = status
	}
	if decision.ClaimToken != "" {
		if decision.HolderNodeID == "" || decision.OccurrenceAt == nil {
			return fmt.Errorf("backup_job_schedule_claim_invalid")
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var job BackupJob
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, decision.JobID)
		if result.Error != nil {
			return result.Error
		}

		if decision.ClaimToken != "" {
			var replay BackupJobOperation
			replayResult := tx.Where("token = ?", decision.ClaimToken).Limit(1).Find(&replay)
			if replayResult.Error != nil {
				return replayResult.Error
			}
			if replayResult.RowsAffected == 1 {
				if replay.JobID == decision.JobID &&
					replay.HolderNodeID == decision.HolderNodeID &&
					replay.ScheduleRevision == decision.ExpectedScheduleRevision+1 {
					return nil
				}
				return fmt.Errorf("backup_job_schedule_claim_token_conflict")
			}
		}

		if job.ScheduleRevision != decision.ExpectedScheduleRevision ||
			!scheduleTimesEqual(job.NextRunAt, decision.ExpectedNextRunAt) {
			return fmt.Errorf("backup_job_schedule_revision_conflict")
		}

		nextRevision := job.ScheduleRevision + 1
		updates := map[string]any{
			"next_run_at":       decision.NextRunAt,
			"schedule_revision": nextRevision,
			"updated_at":        decision.DecidedAt,
		}
		if decision.SetRuntime {
			updates["last_run_at"] = decision.LastRunAt
			updates["last_status"] = decision.LastStatus
			updates["last_error"] = decision.LastError
		}
		updated := tx.Model(&BackupJob{}).
			Where("id = ? AND schedule_revision = ?", decision.JobID, decision.ExpectedScheduleRevision).
			Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_job_schedule_revision_conflict")
		}

		if decision.ClaimToken == "" {
			// Coalescing or restore advancement is part of the same schedule,
			// not a configuration edit. Carry an active operation's matching
			// fence forward so its eventual result can still apply.
			if err := tx.Model(&BackupJobOperation{}).
				Where("job_id = ? AND schedule_revision = ?",
					decision.JobID, decision.ExpectedScheduleRevision).
				Update("schedule_revision", nextRevision).Error; err != nil {
				return err
			}
			return nil
		}
		if !job.Enabled {
			return fmt.Errorf("backup_job_disabled")
		}
		if runner := strings.TrimSpace(job.RunnerNodeID); runner != "" && runner != decision.HolderNodeID {
			return fmt.Errorf("backup_job_schedule_holder_mismatch")
		}
		if job.TargetID != 0 {
			var target BackupTarget
			if err := tx.First(&target, job.TargetID).Error; err != nil {
				return err
			}
			if !target.Enabled {
				return fmt.Errorf("backup_target_disabled")
			}
		}
		rebindPending, err := BackupJobRunnerRebindPendingForJob(tx, decision.JobID)
		if err != nil {
			return err
		}
		if rebindPending {
			return fmt.Errorf("backup_job_runner_rebind_pending")
		}
		repairRequired, err := BackupJobRepairRequired(tx, decision.JobID)
		if err != nil {
			return err
		}
		if repairRequired {
			return fmt.Errorf("backup_job_repair_required")
		}
		var active int64
		if err := tx.Model(&BackupJobOperation{}).Where("job_id = ?", decision.JobID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return fmt.Errorf("backup_job_running")
		}
		return tx.Create(&BackupJobOperation{
			JobID: decision.JobID, Token: decision.ClaimToken,
			Operation: BackupJobOperationBackup, State: BackupJobOperationQueued,
			HolderNodeID: decision.HolderNodeID, Scheduled: true,
			OccurrenceAt: decision.OccurrenceAt, ScheduleRevision: nextRevision,
			PublishAfter: decision.PublishAfter, Revision: 1,
			AcquiredAt: decision.DecidedAt, UpdatedAt: decision.DecidedAt,
		}).Error
	})
}

func ApplyReplicationPolicyScheduleDecisionTxn(db *gorm.DB, decision *ReplicationPolicyScheduleDecision) error {
	if db == nil || decision == nil || decision.PolicyID == 0 {
		return fmt.Errorf("replication_policy_schedule_decision_invalid")
	}
	if decision.DecidedAt.IsZero() || decision.ExpectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_schedule_fence_required")
	}
	decision.DecidedAt = NormalizeCommandTime(decision.DecidedAt)
	decision.ExpectedNextRunAt = normalizeScheduleTime(decision.ExpectedNextRunAt)
	decision.NextRunAt = normalizeScheduleTime(decision.NextRunAt)
	decision.LastRunAt = normalizeScheduleTime(decision.LastRunAt)
	decision.OccurrenceAt = normalizeScheduleTime(decision.OccurrenceAt)
	decision.PublishAfter = normalizeScheduleTime(decision.PublishAfter)
	decision.ClaimToken = strings.TrimSpace(decision.ClaimToken)
	decision.HolderNodeID = strings.TrimSpace(decision.HolderNodeID)
	decision.LastError = strings.TrimSpace(decision.LastError)
	if decision.SetRuntime {
		status, err := validateScheduledStatus(decision.LastStatus)
		if err != nil {
			return err
		}
		decision.LastStatus = status
	}
	if decision.ClaimToken != "" && (decision.HolderNodeID == "" || decision.OccurrenceAt == nil) {
		return fmt.Errorf("replication_run_claim_invalid")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&policy, decision.PolicyID).Error; err != nil {
			return err
		}

		if decision.ClaimToken != "" {
			var replay ReplicationRunOperation
			replayResult := tx.Where("token = ?", decision.ClaimToken).Limit(1).Find(&replay)
			if replayResult.Error != nil {
				return replayResult.Error
			}
			if replayResult.RowsAffected == 1 {
				if replay.PolicyID == decision.PolicyID &&
					replay.HolderNodeID == decision.HolderNodeID &&
					replay.OwnerEpoch == decision.ExpectedOwnerEpoch &&
					replay.ScheduleRevision == decision.ExpectedScheduleRevision+1 {
					return nil
				}
				return fmt.Errorf("replication_run_claim_token_conflict")
			}
		}

		if policy.OwnerEpoch != decision.ExpectedOwnerEpoch ||
			policy.ScheduleRevision != decision.ExpectedScheduleRevision ||
			!scheduleTimesEqual(policy.NextRunAt, decision.ExpectedNextRunAt) {
			return fmt.Errorf("replication_policy_schedule_revision_conflict")
		}

		nextRevision := policy.ScheduleRevision + 1
		updates := map[string]any{
			"next_run_at":       decision.NextRunAt,
			"schedule_revision": nextRevision,
			"updated_at":        decision.DecidedAt,
		}
		if decision.SetRuntime {
			updates["last_run_at"] = decision.LastRunAt
			updates["last_status"] = decision.LastStatus
			updates["last_error"] = decision.LastError
		}
		updated := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ? AND schedule_revision = ?",
				decision.PolicyID, decision.ExpectedOwnerEpoch, decision.ExpectedScheduleRevision).
			Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("replication_policy_schedule_revision_conflict")
		}

		if decision.ClaimToken == "" {
			if err := tx.Model(&ReplicationRunOperation{}).
				Where("policy_id = ? AND schedule_revision = ? AND owner_epoch = ?",
					decision.PolicyID, decision.ExpectedScheduleRevision, decision.ExpectedOwnerEpoch).
				Update("schedule_revision", nextRevision).Error; err != nil {
				return err
			}
			return nil
		}
		if !policy.Enabled {
			return fmt.Errorf("replication_policy_disabled")
		}
		if replicationTransitionInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}
		switch strings.TrimSpace(strings.ToLower(policy.ProtectionState)) {
		case ReplicationProtectionStateDeleting,
			ReplicationProtectionStateSuspended,
			ReplicationProtectionStateUnprotected:
			return fmt.Errorf("replication_policy_not_runnable")
		}
		owner := strings.TrimSpace(policy.ActiveNodeID)
		if owner == "" {
			owner = strings.TrimSpace(policy.SourceNodeID)
		}
		if owner != "" && owner != decision.HolderNodeID {
			return fmt.Errorf("replication_run_holder_mismatch")
		}
		var active int64
		if err := tx.Model(&ReplicationRunOperation{}).Where("policy_id = ?", decision.PolicyID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return fmt.Errorf("replication_policy_already_running")
		}
		return tx.Create(&ReplicationRunOperation{
			PolicyID: decision.PolicyID, Token: decision.ClaimToken,
			State: ReplicationRunOperationQueued, HolderNodeID: decision.HolderNodeID,
			Scheduled: decision.Scheduled, OccurrenceAt: decision.OccurrenceAt,
			ScheduleRevision: nextRevision, OwnerEpoch: decision.ExpectedOwnerEpoch,
			PublishAfter: decision.PublishAfter, Revision: 1,
			AcquiredAt: decision.DecidedAt, UpdatedAt: decision.DecidedAt,
		}).Error
	})
}

func StartReplicationRunOperationTxn(db *gorm.DB, transition *ReplicationRunOperationTransition) error {
	if db == nil || transition == nil || transition.PolicyID == 0 {
		return fmt.Errorf("replication_run_transition_invalid")
	}
	transition.Token = strings.TrimSpace(transition.Token)
	transition.HolderNodeID = strings.TrimSpace(transition.HolderNodeID)
	if transition.Token == "" || transition.HolderNodeID == "" || transition.OccurredAt.IsZero() {
		return fmt.Errorf("replication_run_transition_fence_required")
	}
	transition.OccurredAt = NormalizeCommandTime(transition.OccurredAt)

	return db.Transaction(func(tx *gorm.DB) error {
		var operation ReplicationRunOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&operation, "policy_id = ?", transition.PolicyID).Error; err != nil {
			return err
		}
		if operation.Token != transition.Token || operation.HolderNodeID != transition.HolderNodeID {
			return fmt.Errorf("replication_run_token_mismatch")
		}
		if operation.State == ReplicationRunOperationRunning {
			return nil
		}
		if operation.State != ReplicationRunOperationQueued {
			return fmt.Errorf("invalid_replication_run_state")
		}
		var policy ReplicationPolicy
		if err := tx.First(&policy, operation.PolicyID).Error; err != nil {
			return err
		}
		owner := strings.TrimSpace(policy.ActiveNodeID)
		if owner == "" {
			owner = strings.TrimSpace(policy.SourceNodeID)
		}
		if policy.ScheduleRevision != operation.ScheduleRevision ||
			policy.OwnerEpoch != operation.OwnerEpoch ||
			(owner != "" && owner != operation.HolderNodeID) {
			return fmt.Errorf("replication_run_schedule_stale")
		}
		updated := tx.Model(&ReplicationRunOperation{}).
			Where("policy_id = ? AND token = ? AND revision = ?",
				transition.PolicyID, transition.Token, operation.Revision).
			Updates(map[string]any{
				"state":      ReplicationRunOperationRunning,
				"revision":   operation.Revision + 1,
				"updated_at": transition.OccurredAt,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("replication_run_revision_conflict")
		}
		return nil
	})
}

func CompleteBackupJobRunTxn(db *gorm.DB, result *BackupJobRunResult) error {
	if db == nil || result == nil || result.JobID == 0 {
		return fmt.Errorf("backup_job_run_result_invalid")
	}
	result.Token = strings.TrimSpace(result.Token)
	result.HolderNodeID = strings.TrimSpace(result.HolderNodeID)
	result.LastError = strings.TrimSpace(result.LastError)
	if result.Token == "" || result.HolderNodeID == "" || result.CompletedAt.IsZero() {
		return fmt.Errorf("backup_job_run_result_fence_required")
	}
	status, err := validateScheduledStatus(result.LastStatus)
	if err != nil {
		return err
	}
	result.LastStatus = status
	result.CompletedAt = NormalizeCommandTime(result.CompletedAt)
	result.NextRunAt = normalizeScheduleTime(result.NextRunAt)

	return db.Transaction(func(tx *gorm.DB) error {
		var receipt ScheduledRunReceipt
		receiptResult := tx.Where("token = ?", result.Token).Limit(1).Find(&receipt)
		if receiptResult.Error != nil {
			return receiptResult.Error
		}
		if receiptResult.RowsAffected == 1 {
			if receipt.Kind == ScheduledRunKindBackup && receipt.ObjectID == result.JobID &&
				receipt.HolderNodeID == result.HolderNodeID &&
				receipt.ScheduleRevision == result.ScheduleRevision &&
				receipt.Status == result.LastStatus && receipt.Error == result.LastError &&
				receipt.CompletedAt.Equal(result.CompletedAt) &&
				scheduleTimesEqual(receipt.NextRunAt, result.NextRunAt) &&
				optionalBoolsEqual(receipt.Encrypted, result.Encrypted) {
				return nil
			}
			return fmt.Errorf("scheduled_run_receipt_token_conflict")
		}

		var operation BackupJobOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&operation, "job_id = ?", result.JobID).Error; err != nil {
			return err
		}
		if operation.Token != result.Token || operation.HolderNodeID != result.HolderNodeID ||
			operation.ScheduleRevision != result.ScheduleRevision {
			return fmt.Errorf("backup_job_run_result_token_mismatch")
		}

		var job BackupJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, result.JobID).Error; err != nil {
			return err
		}
		runner := strings.TrimSpace(job.RunnerNodeID)
		applied := job.ScheduleRevision == operation.ScheduleRevision &&
			(runner == "" || runner == operation.HolderNodeID)

		receipt = ScheduledRunReceipt{
			Token: operation.Token, Kind: ScheduledRunKindBackup, ObjectID: result.JobID,
			HolderNodeID: operation.HolderNodeID, Scheduled: operation.Scheduled,
			OccurrenceAt: operation.OccurrenceAt, ScheduleRevision: operation.ScheduleRevision,
			Status: result.LastStatus, Error: result.LastError, Applied: applied,
			NextRunAt: result.NextRunAt, Encrypted: result.Encrypted, CompletedAt: result.CompletedAt,
		}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		if applied {
			updates := map[string]any{
				"last_run_at":       result.CompletedAt,
				"last_status":       result.LastStatus,
				"last_error":        result.LastError,
				"next_run_at":       result.NextRunAt,
				"schedule_revision": job.ScheduleRevision + 1,
				"updated_at":        result.CompletedAt,
			}
			if result.Encrypted != nil {
				updates["encrypted"] = *result.Encrypted
			}
			updated := tx.Model(&BackupJob{}).
				Where("id = ? AND schedule_revision = ?", result.JobID, job.ScheduleRevision).
				Updates(updates)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("backup_job_run_result_revision_conflict")
			}
		}
		deleted := tx.Where("job_id = ? AND token = ?", result.JobID, result.Token).
			Delete(&BackupJobOperation{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return fmt.Errorf("backup_job_run_result_finalize_conflict")
		}
		return nil
	})
}

func CompleteReplicationPolicyRunTxn(db *gorm.DB, result *ReplicationPolicyRunResult) error {
	if db == nil || result == nil || result.PolicyID == 0 {
		return fmt.Errorf("replication_policy_run_result_invalid")
	}
	result.Token = strings.TrimSpace(result.Token)
	result.HolderNodeID = strings.TrimSpace(result.HolderNodeID)
	result.LastError = strings.TrimSpace(result.LastError)
	if result.Token == "" || result.HolderNodeID == "" || result.CompletedAt.IsZero() || result.OwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_run_result_fence_required")
	}
	status, err := validateScheduledStatus(result.LastStatus)
	if err != nil {
		return err
	}
	result.LastStatus = status
	result.CompletedAt = NormalizeCommandTime(result.CompletedAt)
	result.NextRunAt = normalizeScheduleTime(result.NextRunAt)

	return db.Transaction(func(tx *gorm.DB) error {
		var receipt ScheduledRunReceipt
		receiptResult := tx.Where("token = ?", result.Token).Limit(1).Find(&receipt)
		if receiptResult.Error != nil {
			return receiptResult.Error
		}
		if receiptResult.RowsAffected == 1 {
			if receipt.Kind == ScheduledRunKindReplication && receipt.ObjectID == result.PolicyID &&
				receipt.HolderNodeID == result.HolderNodeID &&
				receipt.ScheduleRevision == result.ScheduleRevision &&
				receipt.OwnerEpoch == result.OwnerEpoch &&
				receipt.Status == result.LastStatus && receipt.Error == result.LastError &&
				receipt.CompletedAt.Equal(result.CompletedAt) &&
				scheduleTimesEqual(receipt.NextRunAt, result.NextRunAt) {
				return nil
			}
			return fmt.Errorf("scheduled_run_receipt_token_conflict")
		}

		var operation ReplicationRunOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&operation, "policy_id = ?", result.PolicyID).Error; err != nil {
			return err
		}
		if operation.Token != result.Token || operation.HolderNodeID != result.HolderNodeID ||
			operation.ScheduleRevision != result.ScheduleRevision || operation.OwnerEpoch != result.OwnerEpoch {
			return fmt.Errorf("replication_policy_run_result_token_mismatch")
		}

		var policy ReplicationPolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&policy, result.PolicyID).Error; err != nil {
			return err
		}
		owner := strings.TrimSpace(policy.ActiveNodeID)
		if owner == "" {
			owner = strings.TrimSpace(policy.SourceNodeID)
		}
		applied := policy.ScheduleRevision == operation.ScheduleRevision &&
			policy.OwnerEpoch == operation.OwnerEpoch &&
			(owner == "" || owner == operation.HolderNodeID)

		receipt = ScheduledRunReceipt{
			Token: operation.Token, Kind: ScheduledRunKindReplication, ObjectID: result.PolicyID,
			HolderNodeID: operation.HolderNodeID, Scheduled: operation.Scheduled,
			OccurrenceAt: operation.OccurrenceAt, ScheduleRevision: operation.ScheduleRevision,
			OwnerEpoch: operation.OwnerEpoch, Status: result.LastStatus, Error: result.LastError,
			NextRunAt: result.NextRunAt, Applied: applied, CompletedAt: result.CompletedAt,
		}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		if applied {
			updated := tx.Model(&ReplicationPolicy{}).
				Where("id = ? AND owner_epoch = ? AND schedule_revision = ?",
					result.PolicyID, result.OwnerEpoch, policy.ScheduleRevision).
				Updates(map[string]any{
					"last_run_at":       result.CompletedAt,
					"last_status":       result.LastStatus,
					"last_error":        result.LastError,
					"next_run_at":       result.NextRunAt,
					"schedule_revision": policy.ScheduleRevision + 1,
					"updated_at":        result.CompletedAt,
				})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("replication_policy_run_result_revision_conflict")
			}
		}
		deleted := tx.Where("policy_id = ? AND token = ?", result.PolicyID, result.Token).
			Delete(&ReplicationRunOperation{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return fmt.Errorf("replication_policy_run_result_finalize_conflict")
		}
		return nil
	})
}
