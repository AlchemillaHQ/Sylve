// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const (
	BackupJobRunnerRebindKindMigration = "migration"
	BackupJobRunnerRebindKindFailover  = "failover"

	BackupJobRunnerRebindStatePlanned              = "planned"
	BackupJobRunnerRebindStateReady                = "ready"
	BackupJobRunnerRebindStateCompleted            = "completed"
	BackupJobRunnerRebindStateCompletedWithRepairs = "completed_with_repairs"
	BackupJobRunnerRebindStateAborted              = "aborted"

	BackupJobRunnerRebindItemPending        = "pending"
	BackupJobRunnerRebindItemRebound        = "rebound"
	BackupJobRunnerRebindItemDeleted        = "deleted"
	BackupJobRunnerRebindItemRepairRequired = "repair_required"
	BackupJobRunnerRebindItemRepaired       = "repaired"
	BackupJobRunnerRebindItemAborted        = "aborted"

	BackupJobRunnerRebindStatusPending = "runner_rebind_pending"
)

// BackupJobRunnerRebind is replicated coordination state for moving every
// backup job associated with one guest. Token is the exact migration guest
// operation or replication-policy transition identity that authorizes the move.
type BackupJobRunnerRebind struct {
	Token           string `gorm:"primaryKey" json:"token"`
	Kind            string `gorm:"index;not null" json:"kind"`
	GuestType       string `gorm:"index:idx_backup_job_rebind_guest,priority:1;not null" json:"guestType"`
	GuestID         uint   `gorm:"index:idx_backup_job_rebind_guest,priority:2;not null" json:"guestId"`
	OldRunnerNodeID string `gorm:"index;not null" json:"oldRunnerNodeId"`
	NewRunnerNodeID string `gorm:"index;not null" json:"newRunnerNodeId"`
	State           string `gorm:"index;not null" json:"state"`
	Revision        uint64 `gorm:"not null;default:1" json:"revision"`
}

// BackupJobRunnerRebindItem records the immutable job version selected by a
// rebind plan and its durable terminal result. A composite key prevents one
// operation from carrying duplicate decisions for the same job.
type BackupJobRunnerRebindItem struct {
	OperationToken      string `gorm:"primaryKey;index;not null" json:"operationToken"`
	JobID               uint   `gorm:"primaryKey;index;not null" json:"jobId"`
	ExpectedRunnerID    string `gorm:"not null" json:"expectedRunnerId"`
	ExpectedFingerprint string `gorm:"not null" json:"expectedFingerprint"`
	State               string `gorm:"index;not null" json:"state"`
	Error               string `gorm:"type:text" json:"error"`
	Revision            uint64 `gorm:"not null;default:1" json:"revision"`
}

type BackupJobRunnerRebindPlanItem struct {
	JobID               uint   `json:"jobId"`
	ExpectedRunnerID    string `json:"expectedRunnerId"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
	PreflightError      string `json:"preflightError,omitempty"`
}

type BackupJobRunnerRebindPlan struct {
	Token           string                          `json:"token"`
	Kind            string                          `json:"kind"`
	GuestType       string                          `json:"guestType"`
	GuestID         uint                            `json:"guestId"`
	OldRunnerNodeID string                          `json:"oldRunnerNodeId"`
	NewRunnerNodeID string                          `json:"newRunnerNodeId"`
	Items           []BackupJobRunnerRebindPlanItem `json:"items"`
}

type BackupJobRunnerRebindReady struct {
	Token string `json:"token"`
}

type BackupJobRunnerRebindApply struct {
	Token                  string                           `json:"token"`
	JobID                  uint                             `json:"jobId"`
	ExpectedFingerprint    string                           `json:"expectedFingerprint"`
	FriendlySource         string                           `json:"friendlySource"`
	PlacementFence         *BackupJobPlacementFence         `json:"placementFence,omitempty"`
	PreviousPlacementFence *BackupJobPlacementFence         `json:"previousPlacementFence,omitempty"`
	TargetReadiness        *BackupTargetNodeReadinessUpdate `json:"targetReadiness,omitempty"`
}

type BackupJobRunnerRebindRepair struct {
	Token               string `json:"token"`
	JobID               uint   `json:"jobId"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
	ObservedFingerprint string `json:"observedFingerprint,omitempty"`
	Reason              string `json:"reason"`
}

type BackupJobRunnerRebindPending struct {
	Token  string `json:"token"`
	JobID  uint   `json:"jobId"`
	Reason string `json:"reason"`
}

type BackupJobRunnerRebindAbort struct {
	Token  string `json:"token"`
	Reason string `json:"reason"`
}

type backupJobFingerprintPayload struct {
	ID               uint   `json:"id"`
	Name             string `json:"name"`
	TargetID         uint   `json:"targetId"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	DestSuffix       string `json:"destSuffix"`
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	Recursive        bool   `json:"recursive"`
	Encrypted        bool   `json:"encrypted"`
	CronExpr         string `json:"cronExpr"`
	Enabled          bool   `json:"enabled"`
}

// BackupJobConfigurationFingerprint excludes local/runtime fields so scheduler
// progress and friendly-name refreshes cannot invalidate a durable plan.
func BackupJobConfigurationFingerprint(job *BackupJob) string {
	if job == nil {
		return ""
	}
	payload := backupJobFingerprintPayload{
		ID:               job.ID,
		Name:             strings.TrimSpace(job.Name),
		TargetID:         job.TargetID,
		RunnerNodeID:     strings.TrimSpace(job.RunnerNodeID),
		Mode:             strings.ToLower(strings.TrimSpace(job.Mode)),
		SourceDataset:    strings.TrimSpace(job.SourceDataset),
		JailRootDataset:  strings.TrimSpace(job.JailRootDataset),
		DestSuffix:       strings.TrimSpace(job.DestSuffix),
		PruneKeepLast:    job.PruneKeepLast,
		PruneTarget:      job.PruneTarget,
		StopBeforeBackup: job.StopBeforeBackup,
		Recursive:        job.Recursive,
		Encrypted:        job.Encrypted,
		CronExpr:         strings.TrimSpace(job.CronExpr),
		Enabled:          job.Enabled,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func backupJobDatasetGuestIdentity(dataset string) (string, uint) {
	parts := strings.Split(strings.Trim(dataset, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		segment := strings.TrimSpace(parts[i])
		if segment != "jails" && segment != "virtual-machines" {
			continue
		}
		rawID := strings.TrimSpace(parts[i+1])
		cutAt := len(rawID)
		if idx := strings.IndexByte(rawID, '_'); idx >= 0 && idx < cutAt {
			cutAt = idx
		}
		if idx := strings.IndexByte(rawID, '.'); idx >= 0 && idx < cutAt {
			cutAt = idx
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(rawID[:cutAt]), 10, 64)
		if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
			continue
		}
		if segment == "jails" {
			return BackupJobModeJail, uint(parsed)
		}
		return BackupJobModeVM, uint(parsed)
	}
	return "", 0
}

// BackupJobGuestIdentity deliberately follows the persisted path as well as
// mode so malformed legacy VM/jail jobs are included in a migration plan and
// can reach an explicit repair state instead of being silently omitted.
func BackupJobGuestIdentity(job *BackupJob) (string, uint) {
	if job == nil {
		return "", 0
	}
	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	if mode != BackupJobModeVM && mode != BackupJobModeJail {
		return "", 0
	}
	kind, guestID := backupJobDatasetGuestIdentity(job.JailRootDataset)
	if guestID == 0 {
		kind, guestID = backupJobDatasetGuestIdentity(job.SourceDataset)
	}
	return kind, guestID
}

func normalizeBackupJobRunnerRebindPlan(plan *BackupJobRunnerRebindPlan) error {
	if plan == nil {
		return fmt.Errorf("backup_job_runner_rebind_plan_required")
	}
	plan.Token = strings.TrimSpace(plan.Token)
	plan.Kind = strings.ToLower(strings.TrimSpace(plan.Kind))
	plan.GuestType = strings.ToLower(strings.TrimSpace(plan.GuestType))
	plan.OldRunnerNodeID = strings.TrimSpace(plan.OldRunnerNodeID)
	plan.NewRunnerNodeID = strings.TrimSpace(plan.NewRunnerNodeID)
	if plan.Token == "" ||
		(plan.Kind != BackupJobRunnerRebindKindMigration && plan.Kind != BackupJobRunnerRebindKindFailover) ||
		(plan.GuestType != BackupJobModeVM && plan.GuestType != BackupJobModeJail) ||
		plan.GuestID == 0 || plan.OldRunnerNodeID == "" || plan.NewRunnerNodeID == "" ||
		plan.OldRunnerNodeID == plan.NewRunnerNodeID {
		return fmt.Errorf("backup_job_runner_rebind_plan_invalid")
	}
	seen := make(map[uint]struct{}, len(plan.Items))
	for i := range plan.Items {
		item := &plan.Items[i]
		item.ExpectedRunnerID = strings.TrimSpace(item.ExpectedRunnerID)
		item.ExpectedFingerprint = strings.TrimSpace(item.ExpectedFingerprint)
		item.PreflightError = strings.TrimSpace(item.PreflightError)
		if item.JobID == 0 || item.ExpectedFingerprint == "" {
			return fmt.Errorf("backup_job_runner_rebind_item_invalid")
		}
		if _, exists := seen[item.JobID]; exists {
			return fmt.Errorf("backup_job_runner_rebind_item_duplicate")
		}
		seen[item.JobID] = struct{}{}
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].JobID < plan.Items[j].JobID })
	return nil
}

func loadExactBackupJobRunnerRebindAuthority(tx *gorm.DB, rebind *BackupJobRunnerRebind, allowPreCutover bool) error {
	if tx == nil || rebind == nil {
		return fmt.Errorf("backup_job_runner_rebind_authority_required")
	}
	switch rebind.Kind {
	case BackupJobRunnerRebindKindMigration:
		var operation ReplicationGuestOperation
		result := tx.Where("guest_type = ? AND guest_id = ?", rebind.GuestType, rebind.GuestID).
			Limit(1).Find(&operation)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || operation.Operation != ReplicationGuestOperationMigration ||
			strings.TrimSpace(operation.Token) != rebind.Token ||
			strings.TrimSpace(operation.OwnerNodeID) != rebind.OldRunnerNodeID ||
			strings.TrimSpace(operation.TargetNodeID) != rebind.NewRunnerNodeID {
			return fmt.Errorf("backup_job_runner_rebind_guest_operation_mismatch")
		}
		if operation.State == ReplicationGuestOperationCutover {
			return nil
		}
		if allowPreCutover && operation.State == ReplicationGuestOperationPreCutover {
			return nil
		}
		return fmt.Errorf("backup_job_runner_rebind_guest_operation_not_cutover")
	case BackupJobRunnerRebindKindFailover:
		var policy ReplicationPolicy
		result := tx.Where("guest_type = ? AND guest_id = ?", rebind.GuestType, rebind.GuestID).
			Limit(1).Find(&policy)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || !policy.Enabled ||
			strings.TrimSpace(policy.TransitionRunID) != rebind.Token ||
			strings.TrimSpace(policy.TransitionSourceNodeID) != rebind.OldRunnerNodeID ||
			strings.TrimSpace(policy.TransitionTargetNodeID) != rebind.NewRunnerNodeID {
			return fmt.Errorf("backup_job_runner_rebind_policy_transition_mismatch")
		}
		state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
		activeNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if activeNodeID == "" {
			activeNodeID = strings.TrimSpace(policy.SourceNodeID)
		}
		if allowPreCutover {
			if (state != ReplicationTransitionStateDemoting && state != ReplicationTransitionStateCatchup) ||
				activeNodeID != rebind.OldRunnerNodeID ||
				policy.OwnerEpoch == 0 || policy.TransitionOwnerEpoch != policy.OwnerEpoch {
				return fmt.Errorf("backup_job_runner_rebind_policy_transition_not_pre_cutover")
			}
			return nil
		}
		if (state != ReplicationTransitionStatePromoting && state != ReplicationTransitionStateCompleted) ||
			activeNodeID != rebind.NewRunnerNodeID ||
			policy.OwnerEpoch == 0 || policy.TransitionOwnerEpoch != policy.OwnerEpoch {
			return fmt.Errorf("backup_job_runner_rebind_policy_transition_not_cutover")
		}
		return nil
	default:
		return fmt.Errorf("backup_job_runner_rebind_kind_invalid")
	}
}

func loadExactBackupJobRunnerRebindDecisionAuthority(tx *gorm.DB, rebind *BackupJobRunnerRebind) error {
	if err := loadExactBackupJobRunnerRebindAuthority(tx, rebind, false); err != nil {
		return err
	}
	if rebind.Kind != BackupJobRunnerRebindKindFailover {
		return nil
	}
	var policy ReplicationPolicy
	if err := tx.Where("guest_type = ? AND guest_id = ?", rebind.GuestType, rebind.GuestID).
		Limit(1).First(&policy).Error; err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(policy.TransitionState)) != ReplicationTransitionStateCompleted {
		return fmt.Errorf("backup_job_runner_rebind_transition_not_completed")
	}
	return nil
}

func backupJobRunnerRebindPlansEquivalent(existing BackupJobRunnerRebind, plan *BackupJobRunnerRebindPlan) bool {
	return existing.Token == plan.Token && existing.Kind == plan.Kind &&
		existing.GuestType == plan.GuestType && existing.GuestID == plan.GuestID &&
		existing.OldRunnerNodeID == plan.OldRunnerNodeID && existing.NewRunnerNodeID == plan.NewRunnerNodeID
}

func PrepareBackupJobRunnerRebindTxn(db *gorm.DB, plan *BackupJobRunnerRebindPlan) error {
	if db == nil {
		return fmt.Errorf("backup_job_runner_rebind_database_required")
	}
	if err := normalizeBackupJobRunnerRebindPlan(plan); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var existing BackupJobRunnerRebind
		result := tx.Where("token = ?", plan.Token).Limit(1).Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if !backupJobRunnerRebindPlansEquivalent(existing, plan) {
				return fmt.Errorf("backup_job_runner_rebind_plan_mismatch")
			}
			var items []BackupJobRunnerRebindItem
			if err := tx.Where("operation_token = ?", plan.Token).Order("job_id ASC").Find(&items).Error; err != nil {
				return err
			}
			if len(items) != len(plan.Items) {
				return fmt.Errorf("backup_job_runner_rebind_plan_items_mismatch")
			}
			for i := range items {
				if items[i].JobID != plan.Items[i].JobID ||
					items[i].ExpectedRunnerID != plan.Items[i].ExpectedRunnerID ||
					items[i].ExpectedFingerprint != plan.Items[i].ExpectedFingerprint {
					return fmt.Errorf("backup_job_runner_rebind_plan_items_mismatch")
				}
			}
			return nil
		}

		rebind := BackupJobRunnerRebind{
			Token: plan.Token, Kind: plan.Kind, GuestType: plan.GuestType, GuestID: plan.GuestID,
			OldRunnerNodeID: plan.OldRunnerNodeID, NewRunnerNodeID: plan.NewRunnerNodeID,
			State: BackupJobRunnerRebindStatePlanned, Revision: 1,
		}
		if err := loadExactBackupJobRunnerRebindAuthority(tx, &rebind, true); err != nil {
			return err
		}

		planned := make(map[uint]BackupJobRunnerRebindPlanItem, len(plan.Items))
		for _, item := range plan.Items {
			planned[item.JobID] = item
		}
		var currentJobs []BackupJob
		if err := tx.Order("id ASC").Find(&currentJobs).Error; err != nil {
			return err
		}
		for i := range currentJobs {
			guestType, guestID := BackupJobGuestIdentity(&currentJobs[i])
			if guestType != plan.GuestType || guestID != plan.GuestID {
				continue
			}
			if _, exists := planned[currentJobs[i].ID]; !exists {
				return fmt.Errorf("backup_job_runner_rebind_plan_incomplete")
			}
		}

		if err := tx.Create(&rebind).Error; err != nil {
			return err
		}
		for _, plannedItem := range plan.Items {
			item := BackupJobRunnerRebindItem{
				OperationToken: plan.Token, JobID: plannedItem.JobID,
				ExpectedRunnerID:    plannedItem.ExpectedRunnerID,
				ExpectedFingerprint: plannedItem.ExpectedFingerprint,
				State:               BackupJobRunnerRebindItemPending,
				Error:               plannedItem.PreflightError,
				Revision:            1,
			}
			var job BackupJob
			jobResult := tx.Where("id = ?", item.JobID).Limit(1).Find(&job)
			if jobResult.Error != nil {
				return jobResult.Error
			}
			if jobResult.RowsAffected == 0 {
				item.State = BackupJobRunnerRebindItemDeleted
			} else {
				guestType, guestID := BackupJobGuestIdentity(&job)
				if guestType != plan.GuestType || guestID != plan.GuestID ||
					strings.TrimSpace(job.RunnerNodeID) != item.ExpectedRunnerID ||
					BackupJobConfigurationFingerprint(&job) != item.ExpectedFingerprint {
					return fmt.Errorf("backup_job_runner_rebind_item_changed")
				}
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func recomputeBackupJobRunnerRebindState(tx *gorm.DB, token string) error {
	var operation BackupJobRunnerRebind
	if err := tx.Where("token = ?", token).First(&operation).Error; err != nil {
		return err
	}
	if operation.State == BackupJobRunnerRebindStatePlanned || operation.State == BackupJobRunnerRebindStateAborted {
		return nil
	}
	var pending, repairs int64
	if err := tx.Model(&BackupJobRunnerRebindItem{}).
		Where("operation_token = ? AND state = ?", token, BackupJobRunnerRebindItemPending).
		Count(&pending).Error; err != nil {
		return err
	}
	if pending != 0 {
		if operation.State != BackupJobRunnerRebindStateReady {
			return tx.Model(&BackupJobRunnerRebind{}).Where("token = ?", token).
				Updates(map[string]any{"state": BackupJobRunnerRebindStateReady, "revision": operation.Revision + 1}).Error
		}
		return nil
	}
	if err := tx.Model(&BackupJobRunnerRebindItem{}).
		Where("operation_token = ? AND state = ?", token, BackupJobRunnerRebindItemRepairRequired).
		Count(&repairs).Error; err != nil {
		return err
	}
	state := BackupJobRunnerRebindStateCompleted
	if repairs != 0 {
		state = BackupJobRunnerRebindStateCompletedWithRepairs
	}
	if operation.State == state {
		return nil
	}
	return tx.Model(&BackupJobRunnerRebind{}).Where("token = ?", token).
		Updates(map[string]any{"state": state, "revision": operation.Revision + 1}).Error
}

func readyBackupJobRunnerRebind(tx *gorm.DB, payload *BackupJobRunnerRebindReady) error {
	var operation BackupJobRunnerRebind
	if err := tx.Where("token = ?", payload.Token).First(&operation).Error; err != nil {
		return err
	}
	if operation.State == BackupJobRunnerRebindStateAborted {
		return fmt.Errorf("backup_job_runner_rebind_aborted")
	}
	if operation.State == BackupJobRunnerRebindStateCompleted ||
		operation.State == BackupJobRunnerRebindStateCompletedWithRepairs ||
		operation.State == BackupJobRunnerRebindStateReady {
		return nil
	}
	if operation.State != BackupJobRunnerRebindStatePlanned {
		return fmt.Errorf("backup_job_runner_rebind_state_invalid")
	}
	if err := loadExactBackupJobRunnerRebindAuthority(tx, &operation, false); err != nil {
		return err
	}
	result := tx.Model(&BackupJobRunnerRebind{}).
		Where("token = ? AND state = ?", operation.Token, BackupJobRunnerRebindStatePlanned).
		Updates(map[string]any{"state": BackupJobRunnerRebindStateReady, "revision": operation.Revision + 1})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("backup_job_runner_rebind_ready_cas_conflict")
	}
	var items []BackupJobRunnerRebindItem
	if err := tx.Where("operation_token = ? AND state = ?", operation.Token, BackupJobRunnerRebindItemPending).
		Find(&items).Error; err != nil {
		return err
	}
	for i := range items {
		if err := tx.Model(&BackupJob{}).
			Where("id = ?", items[i].JobID).
			UpdateColumns(map[string]any{
				"next_run_at":       nil,
				"last_status":       BackupJobRunnerRebindStatusPending,
				"last_error":        "backup_job_runner_rebind_pending",
				"schedule_revision": gorm.Expr("schedule_revision + ?", 1),
			}).Error; err != nil {
			return err
		}
	}
	return recomputeBackupJobRunnerRebindState(tx, operation.Token)
}

func ReadyBackupJobRunnerRebindTxn(db *gorm.DB, payload *BackupJobRunnerRebindReady) error {
	if db == nil || payload == nil || strings.TrimSpace(payload.Token) == "" {
		return fmt.Errorf("backup_job_runner_rebind_ready_invalid")
	}
	payload.Token = strings.TrimSpace(payload.Token)
	return db.Transaction(func(tx *gorm.DB) error {
		return readyBackupJobRunnerRebind(tx, payload)
	})
}

func loadPendingBackupJobRunnerRebindItem(tx *gorm.DB, token string, jobID uint) (BackupJobRunnerRebind, BackupJobRunnerRebindItem, bool, error) {
	var operation BackupJobRunnerRebind
	var item BackupJobRunnerRebindItem
	if token == "" || jobID == 0 {
		return operation, item, false, fmt.Errorf("backup_job_runner_rebind_decision_invalid")
	}
	if err := tx.Where("token = ?", token).First(&operation).Error; err != nil {
		return operation, item, false, err
	}
	if operation.State != BackupJobRunnerRebindStateReady {
		if operation.State == BackupJobRunnerRebindStateCompleted || operation.State == BackupJobRunnerRebindStateCompletedWithRepairs {
			return operation, item, true, nil
		}
		return operation, item, false, fmt.Errorf("backup_job_runner_rebind_not_ready")
	}
	if err := tx.Where("operation_token = ? AND job_id = ?", token, jobID).First(&item).Error; err != nil {
		return operation, item, false, err
	}
	if item.State != BackupJobRunnerRebindItemPending {
		return operation, item, true, nil
	}
	if err := loadExactBackupJobRunnerRebindDecisionAuthority(tx, &operation); err != nil {
		return operation, item, false, err
	}
	return operation, item, false, nil
}

func backupJobHasActiveOperationTxn(tx *gorm.DB, jobID uint) (bool, error) {
	var count int64
	if err := tx.Model(&BackupJobOperation{}).Where("job_id = ?", jobID).Count(&count).Error; err != nil {
		return false, err
	}
	return count != 0, nil
}

func ApplyBackupJobRunnerRebindTxn(db *gorm.DB, payload *BackupJobRunnerRebindApply) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_runner_rebind_apply_invalid")
	}
	payload.Token = strings.TrimSpace(payload.Token)
	payload.ExpectedFingerprint = strings.TrimSpace(payload.ExpectedFingerprint)
	payload.FriendlySource = strings.TrimSpace(payload.FriendlySource)
	return db.Transaction(func(tx *gorm.DB) error {
		operation, item, terminal, err := loadPendingBackupJobRunnerRebindItem(tx, payload.Token, payload.JobID)
		if err != nil || terminal {
			return err
		}
		if payload.ExpectedFingerprint == "" || payload.ExpectedFingerprint != item.ExpectedFingerprint {
			return fmt.Errorf("backup_job_runner_rebind_fingerprint_mismatch")
		}
		var job BackupJob
		result := tx.Where("id = ?", payload.JobID).Limit(1).Find(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Model(&BackupJobRunnerRebindItem{}).
				Where("operation_token = ? AND job_id = ? AND state = ?", payload.Token, payload.JobID, BackupJobRunnerRebindItemPending).
				Updates(map[string]any{"state": BackupJobRunnerRebindItemDeleted, "error": "", "revision": item.Revision + 1}).Error; err != nil {
				return err
			}
			return recomputeBackupJobRunnerRebindState(tx, payload.Token)
		}
		guestType, guestID := BackupJobGuestIdentity(&job)
		if guestType != operation.GuestType || guestID != operation.GuestID ||
			strings.TrimSpace(job.RunnerNodeID) != item.ExpectedRunnerID ||
			BackupJobConfigurationFingerprint(&job) != item.ExpectedFingerprint {
			return fmt.Errorf("backup_job_runner_rebind_item_changed")
		}
		busy, err := backupJobHasActiveOperationTxn(tx, job.ID)
		if err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("backup_job_runner_rebind_job_busy")
		}
		if err := ValidateBackupJobPlacementFenceTxn(tx, &job, payload.PreviousPlacementFence); err != nil {
			return err
		}
		proposed := job
		proposed.RunnerNodeID = operation.NewRunnerNodeID
		if err := ValidateBackupJobPlacementFenceTxn(tx, &proposed, payload.PlacementFence); err != nil {
			return err
		}
		if payload.TargetReadiness != nil {
			if err := ApplyBackupTargetNodeReadinessForJobTxn(tx, &proposed, payload.TargetReadiness); err != nil {
				return err
			}
		}
		updates := map[string]any{
			"runner_node_id":    operation.NewRunnerNodeID,
			"friendly_src":      payload.FriendlySource,
			"last_status":       "",
			"last_error":        "",
			"schedule_revision": gorm.Expr("schedule_revision + ?", 1),
		}
		if err := tx.Model(&BackupJob{}).Where("id = ?", job.ID).UpdateColumns(updates).Error; err != nil {
			return err
		}
		if err := clearBackupJobRepairRequiredTxn(tx, job.ID); err != nil {
			return err
		}
		if err := tx.Model(&BackupJobRunnerRebindItem{}).
			Where("operation_token = ? AND job_id = ? AND state = ?", payload.Token, payload.JobID, BackupJobRunnerRebindItemPending).
			Updates(map[string]any{"state": BackupJobRunnerRebindItemRebound, "error": "", "revision": item.Revision + 1}).Error; err != nil {
			return err
		}
		return recomputeBackupJobRunnerRebindState(tx, payload.Token)
	})
}

func RepairBackupJobRunnerRebindTxn(db *gorm.DB, payload *BackupJobRunnerRebindRepair) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_runner_rebind_repair_invalid")
	}
	payload.Token = strings.TrimSpace(payload.Token)
	payload.ExpectedFingerprint = strings.TrimSpace(payload.ExpectedFingerprint)
	payload.ObservedFingerprint = strings.TrimSpace(payload.ObservedFingerprint)
	payload.Reason = strings.TrimSpace(payload.Reason)
	if payload.Reason == "" {
		return fmt.Errorf("backup_job_runner_rebind_repair_reason_required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		operation, item, terminal, err := loadPendingBackupJobRunnerRebindItem(tx, payload.Token, payload.JobID)
		if err != nil || terminal {
			return err
		}
		if payload.ExpectedFingerprint == "" || payload.ExpectedFingerprint != item.ExpectedFingerprint {
			return fmt.Errorf("backup_job_runner_rebind_fingerprint_mismatch")
		}
		var job BackupJob
		result := tx.Where("id = ?", payload.JobID).Limit(1).Find(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Model(&BackupJobRunnerRebindItem{}).
				Where("operation_token = ? AND job_id = ? AND state = ?", payload.Token, payload.JobID, BackupJobRunnerRebindItemPending).
				Updates(map[string]any{"state": BackupJobRunnerRebindItemDeleted, "error": "", "revision": item.Revision + 1}).Error; err != nil {
				return err
			}
			return recomputeBackupJobRunnerRebindState(tx, payload.Token)
		}
		guestType, guestID := BackupJobGuestIdentity(&job)
		currentFingerprint := BackupJobConfigurationFingerprint(&job)
		itemChanged := guestType != operation.GuestType || guestID != operation.GuestID ||
			strings.TrimSpace(job.RunnerNodeID) != item.ExpectedRunnerID ||
			currentFingerprint != item.ExpectedFingerprint
		if itemChanged && (payload.ObservedFingerprint == "" || payload.ObservedFingerprint != currentFingerprint) {
			return fmt.Errorf("backup_job_runner_rebind_item_changed")
		}
		busy, err := backupJobHasActiveOperationTxn(tx, job.ID)
		if err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("backup_job_runner_rebind_job_busy")
		}
		if err := tx.Model(&BackupJob{}).Where("id = ?", job.ID).UpdateColumns(map[string]any{
			"runner_node_id":    operation.NewRunnerNodeID,
			"enabled":           false,
			"next_run_at":       nil,
			"last_status":       BackupJobRunnerRebindItemRepairRequired,
			"last_error":        payload.Reason,
			"schedule_revision": gorm.Expr("schedule_revision + ?", 1),
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&BackupJobRunnerRebindItem{}).
			Where("operation_token = ? AND job_id = ? AND state = ?", payload.Token, payload.JobID, BackupJobRunnerRebindItemPending).
			Updates(map[string]any{"state": BackupJobRunnerRebindItemRepairRequired, "error": payload.Reason, "revision": item.Revision + 1}).Error; err != nil {
			return err
		}
		return recomputeBackupJobRunnerRebindState(tx, payload.Token)
	})
}

func PendingBackupJobRunnerRebindTxn(db *gorm.DB, payload *BackupJobRunnerRebindPending) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_runner_rebind_pending_invalid")
	}
	payload.Token = strings.TrimSpace(payload.Token)
	payload.Reason = strings.TrimSpace(payload.Reason)
	return db.Transaction(func(tx *gorm.DB) error {
		_, item, terminal, err := loadPendingBackupJobRunnerRebindItem(tx, payload.Token, payload.JobID)
		if err != nil || terminal {
			return err
		}
		if item.Error != payload.Reason {
			if err := tx.Model(&BackupJobRunnerRebindItem{}).
				Where("operation_token = ? AND job_id = ? AND state = ?", payload.Token, payload.JobID, BackupJobRunnerRebindItemPending).
				Updates(map[string]any{"error": payload.Reason, "revision": item.Revision + 1}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&BackupJob{}).
			Where("id = ? AND last_status = ?", payload.JobID, BackupJobRunnerRebindStatusPending).
			UpdateColumn("last_error", payload.Reason).Error
	})
}

func abortBackupJobRunnerRebindItems(db *gorm.DB, operation BackupJobRunnerRebind, reason string) error {
	var items []BackupJobRunnerRebindItem
	if err := db.Where("operation_token = ? AND state = ?", operation.Token, BackupJobRunnerRebindItemPending).
		Find(&items).Error; err != nil {
		return err
	}
	for i := range items {
		if err := db.Model(&BackupJobRunnerRebindItem{}).
			Where("operation_token = ? AND job_id = ? AND state = ?", operation.Token, items[i].JobID, BackupJobRunnerRebindItemPending).
			Updates(map[string]any{
				"state":    BackupJobRunnerRebindItemAborted,
				"error":    reason,
				"revision": items[i].Revision + 1,
			}).Error; err != nil {
			return err
		}
		if err := db.Model(&BackupJob{}).
			Where("id = ? AND last_status = ?", items[i].JobID, BackupJobRunnerRebindStatusPending).
			UpdateColumns(map[string]any{"last_status": "", "last_error": ""}).Error; err != nil {
			return err
		}
	}
	return db.Model(&BackupJobRunnerRebind{}).Where("token = ?", operation.Token).
		Updates(map[string]any{"state": BackupJobRunnerRebindStateAborted, "revision": operation.Revision + 1}).Error
}

func AbortBackupJobRunnerRebindTxn(db *gorm.DB, token string) error {
	if db == nil || !db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	var operation BackupJobRunnerRebind
	result := db.Where("token = ?", token).Limit(1).Find(&operation)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	if operation.State == BackupJobRunnerRebindStateAborted {
		return nil
	}
	if operation.State != BackupJobRunnerRebindStatePlanned {
		return fmt.Errorf("backup_job_runner_rebind_already_ready")
	}
	reason := "migration_aborted"
	if operation.Kind == BackupJobRunnerRebindKindFailover {
		reason = "failover_aborted"
	}
	return abortBackupJobRunnerRebindItems(db, operation, reason)
}

func AbortFailedFailoverBackupJobRunnerRebindTxn(db *gorm.DB, payload *BackupJobRunnerRebindAbort) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_runner_rebind_abort_invalid")
	}
	payload.Token = strings.TrimSpace(payload.Token)
	payload.Reason = strings.TrimSpace(payload.Reason)
	if payload.Token == "" || payload.Reason == "" {
		return fmt.Errorf("backup_job_runner_rebind_abort_invalid")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var operation BackupJobRunnerRebind
		result := tx.Where("token = ?", payload.Token).Limit(1).Find(&operation)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		if operation.State == BackupJobRunnerRebindStateAborted {
			return nil
		}
		if operation.Kind != BackupJobRunnerRebindKindFailover ||
			operation.State != BackupJobRunnerRebindStatePlanned {
			return fmt.Errorf("backup_job_runner_rebind_abort_state_invalid")
		}
		var policy ReplicationPolicy
		policyResult := tx.Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
			Limit(1).Find(&policy)
		if policyResult.Error != nil {
			return policyResult.Error
		}
		if policyResult.RowsAffected == 1 && strings.TrimSpace(policy.TransitionRunID) == operation.Token {
			state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
			if state != ReplicationTransitionStateFailed && state != ReplicationTransitionStateRollingBack {
				return fmt.Errorf("backup_job_runner_rebind_transition_still_active")
			}
		}
		return abortBackupJobRunnerRebindItems(tx, operation, payload.Reason)
	})
}

// RollbackFailoverBackupJobRunnerRebindTxn cancels a ready failover plan in
// the same transaction that atomically restores policy ownership. Rebinding is
// deliberately ordered after target activation, so any applied item here is a
// protocol violation and fences rollback rather than silently splitting state.
func RollbackFailoverBackupJobRunnerRebindTxn(
	db *gorm.DB,
	token string,
	currentRunnerNodeID string,
	restoredRunnerNodeID string,
) error {
	if db == nil || !db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		return nil
	}
	token = strings.TrimSpace(token)
	currentRunnerNodeID = strings.TrimSpace(currentRunnerNodeID)
	restoredRunnerNodeID = strings.TrimSpace(restoredRunnerNodeID)
	if token == "" || currentRunnerNodeID == "" || restoredRunnerNodeID == "" {
		return fmt.Errorf("backup_job_runner_rebind_rollback_invalid")
	}
	var operation BackupJobRunnerRebind
	result := db.Where("token = ?", token).Limit(1).Find(&operation)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	if operation.Kind != BackupJobRunnerRebindKindFailover ||
		operation.NewRunnerNodeID != currentRunnerNodeID ||
		operation.OldRunnerNodeID != restoredRunnerNodeID {
		return fmt.Errorf("backup_job_runner_rebind_rollback_mismatch")
	}
	if operation.State == BackupJobRunnerRebindStateAborted {
		return nil
	}
	if operation.State != BackupJobRunnerRebindStateReady &&
		operation.State != BackupJobRunnerRebindStateCompleted {
		return fmt.Errorf("backup_job_runner_rebind_rollback_state_invalid")
	}
	var applied int64
	if err := db.Model(&BackupJobRunnerRebindItem{}).
		Where("operation_token = ? AND state IN ?", token, []string{
			BackupJobRunnerRebindItemRebound,
			BackupJobRunnerRebindItemRepairRequired,
			BackupJobRunnerRebindItemRepaired,
		}).Count(&applied).Error; err != nil {
		return err
	}
	if applied != 0 {
		return fmt.Errorf("backup_job_runner_rebind_rollback_after_apply")
	}
	return abortBackupJobRunnerRebindItems(db, operation, "failover_rolled_back")
}

func MarkDeletedBackupJobRunnerRebindItemsTxn(db *gorm.DB, jobID uint) error {
	if db == nil || jobID == 0 || !db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
		return nil
	}
	var items []BackupJobRunnerRebindItem
	if err := db.Where("job_id = ? AND state IN ?", jobID, []string{
		BackupJobRunnerRebindItemPending,
		BackupJobRunnerRebindItemRebound,
		BackupJobRunnerRebindItemRepairRequired,
		BackupJobRunnerRebindItemRepaired,
	}).Find(&items).Error; err != nil {
		return err
	}
	tokens := make(map[string]struct{}, len(items))
	for _, item := range items {
		tokens[item.OperationToken] = struct{}{}
		if err := db.Model(&BackupJobRunnerRebindItem{}).
			Where("operation_token = ? AND job_id = ? AND state = ?", item.OperationToken, jobID, item.State).
			Updates(map[string]any{"state": BackupJobRunnerRebindItemDeleted, "error": "", "revision": item.Revision + 1}).Error; err != nil {
			return err
		}
	}
	for token := range tokens {
		if err := recomputeBackupJobRunnerRebindState(db, token); err != nil {
			return err
		}
	}
	return nil
}

func clearBackupJobRepairRequiredTxn(db *gorm.DB, jobID uint) error {
	if db == nil || jobID == 0 || !db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
		return nil
	}
	var items []BackupJobRunnerRebindItem
	if err := db.Where("job_id = ? AND state = ?", jobID, BackupJobRunnerRebindItemRepairRequired).Find(&items).Error; err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	tokens := make(map[string]struct{}, len(items))
	for _, item := range items {
		tokens[item.OperationToken] = struct{}{}
		if err := db.Model(&BackupJobRunnerRebindItem{}).
			Where("operation_token = ? AND job_id = ? AND state = ?", item.OperationToken, jobID, BackupJobRunnerRebindItemRepairRequired).
			Updates(map[string]any{"state": BackupJobRunnerRebindItemRepaired, "error": "", "revision": item.Revision + 1}).Error; err != nil {
			return err
		}
	}
	if err := db.Model(&BackupJob{}).Where("id = ?", jobID).
		UpdateColumns(map[string]any{"last_status": "", "last_error": ""}).Error; err != nil {
		return err
	}
	for token := range tokens {
		if err := recomputeBackupJobRunnerRebindState(db, token); err != nil {
			return err
		}
	}
	return nil
}

func ClearBackupJobRepairRequiredTxn(db *gorm.DB, jobID uint) error {
	return clearBackupJobRepairRequiredTxn(db, jobID)
}

func BackupJobRepairRequired(db *gorm.DB, jobID uint) (bool, error) {
	if db == nil || jobID == 0 || !db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&BackupJobRunnerRebindItem{}).
		Where("job_id = ? AND state = ?", jobID, BackupJobRunnerRebindItemRepairRequired).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count != 0, nil
}

func RequireNoPendingBackupJobRunnerRebindForGuestTxn(db *gorm.DB, guestType string, guestID uint) error {
	if db == nil || guestID == 0 || !db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) ||
		!db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		return nil
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	var count int64
	if err := db.Model(&BackupJobRunnerRebindItem{}).
		Joins("JOIN backup_job_runner_rebinds ON backup_job_runner_rebinds.token = backup_job_runner_rebind_items.operation_token").
		Where("backup_job_runner_rebinds.guest_type = ? AND backup_job_runner_rebinds.guest_id = ? AND backup_job_runner_rebinds.state = ? AND backup_job_runner_rebind_items.state = ?",
			guestType, guestID, BackupJobRunnerRebindStateReady, BackupJobRunnerRebindItemPending).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("backup_job_runner_rebind_pending")
	}
	return nil
}

func BackupJobRunnerRebindPendingForJob(db *gorm.DB, jobID uint) (bool, error) {
	if db == nil || jobID == 0 || !db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) ||
		!db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&BackupJobRunnerRebindItem{}).
		Joins("JOIN backup_job_runner_rebinds ON backup_job_runner_rebinds.token = backup_job_runner_rebind_items.operation_token").
		Where("backup_job_runner_rebind_items.job_id = ? AND backup_job_runner_rebind_items.state = ? AND backup_job_runner_rebinds.state = ?",
			jobID, BackupJobRunnerRebindItemPending, BackupJobRunnerRebindStateReady).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count != 0, nil
}

func RequireBackupJobRunnerRebindTerminalTxn(db *gorm.DB, token string) error {
	if db == nil || !db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		return nil
	}
	var operation BackupJobRunnerRebind
	result := db.Where("token = ?", strings.TrimSpace(token)).Limit(1).Find(&operation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Compatibility for migrations acquired before this state was added.
		return nil
	}
	if operation.State != BackupJobRunnerRebindStateCompleted &&
		operation.State != BackupJobRunnerRebindStateCompletedWithRepairs {
		return fmt.Errorf("backup_job_runner_rebind_not_terminal")
	}
	return nil
}

func IsBackupJobRunnerRebindTerminal(state string) bool {
	state = strings.ToLower(strings.TrimSpace(state))
	return state == BackupJobRunnerRebindStateCompleted || state == BackupJobRunnerRebindStateCompletedWithRepairs
}
