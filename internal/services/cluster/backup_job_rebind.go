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
	"sync"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const backupJobRunnerRebindErrorLimit = 2048

type permanentBackupJobRebindError struct {
	reason string
}

func (e *permanentBackupJobRebindError) Error() string {
	if e == nil {
		return "backup_job_configuration_invalid"
	}
	return e.reason
}

func permanentBackupJobRebindFailure(format string, args ...any) error {
	return &permanentBackupJobRebindError{reason: fmt.Sprintf(format, args...)}
}

func boundedBackupJobRunnerRebindError(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.TrimSpace(err.Error())
	if len(reason) > backupJobRunnerRebindErrorLimit {
		reason = reason[:backupJobRunnerRebindErrorLimit]
	}
	return reason
}

func isPermanentBackupJobRunnerRebindError(err error) bool {
	if err == nil {
		return false
	}
	var permanent *permanentBackupJobRebindError
	if errors.As(err, &permanent) {
		return true
	}
	var rejection *backupJobSafetyRejectionError
	if !errors.As(err, &rejection) {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(rejection.Reason))
	for _, prefix := range []string{
		"invalid_backup_job_mode",
		"invalid_dataset_backup_source",
		"dataset_backup_source_reserved_managed_scope",
		"dataset_backup_source_contains_managed_guest",
		"jail_root_dataset_required",
		"jail_backup_requires_registered_canonical_root",
		"vm_backup_requires_recursive",
		"vm_backup_requires_registered_canonical_root",
	} {
		if strings.HasPrefix(reason, prefix) {
			return true
		}
	}
	return false
}

func (s *Service) applyBackupJobRunnerRebindCommand(action string, payload any, bypassRaft bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal_backup_job_runner_rebind_%s_failed: %w", action, err)
	}
	if bypassRaft {
		switch action {
		case "prepare":
			value := payload.(clusterModels.BackupJobRunnerRebindPlan)
			return clusterModels.PrepareBackupJobRunnerRebindTxn(s.DB, &value)
		case "ready":
			value := payload.(clusterModels.BackupJobRunnerRebindReady)
			return clusterModels.ReadyBackupJobRunnerRebindTxn(s.DB, &value)
		case "apply":
			value := payload.(clusterModels.BackupJobRunnerRebindApply)
			return clusterModels.ApplyBackupJobRunnerRebindTxn(s.DB, &value)
		case "repair":
			value := payload.(clusterModels.BackupJobRunnerRebindRepair)
			return clusterModels.RepairBackupJobRunnerRebindTxn(s.DB, &value)
		case "pending":
			value := payload.(clusterModels.BackupJobRunnerRebindPending)
			return clusterModels.PendingBackupJobRunnerRebindTxn(s.DB, &value)
		default:
			return fmt.Errorf("invalid_backup_job_runner_rebind_action")
		}
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_runner_rebind", Action: action, Data: raw,
	})
}

// PrepareBackupJobRunnerRebind captures the exact set and immutable versions of
// all persisted VM/jail jobs while the replicated migration interlock fences
// ordinary updates. The FSM independently verifies completeness and identity.
func (s *Service) PrepareBackupJobRunnerRebind(
	ctx context.Context,
	guestType string,
	guestID uint,
	newRunnerNodeID string,
	operationToken string,
	bypassRaft bool,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_runner_rebind_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	newRunnerNodeID = strings.TrimSpace(newRunnerNodeID)
	operationToken = strings.TrimSpace(operationToken)
	if (guestType != clusterModels.BackupJobModeVM && guestType != clusterModels.BackupJobModeJail) ||
		guestID == 0 || newRunnerNodeID == "" || operationToken == "" {
		return fmt.Errorf("backup_job_runner_rebind_input_invalid")
	}
	if !bypassRaft {
		if err := s.requireReplicationRaftLeader(); err != nil {
			return err
		}
		if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
			return fmt.Errorf("backup_job_runner_rebind_leader_barrier_failed: %w", err)
		}
	}

	var operation clusterModels.ReplicationGuestOperation
	opResult := s.DB.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", guestType, guestID).
		Limit(1).Find(&operation)
	if opResult.Error != nil {
		return opResult.Error
	}
	if opResult.RowsAffected != 1 || operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
		strings.TrimSpace(operation.Token) != operationToken ||
		strings.TrimSpace(operation.OwnerNodeID) == "" ||
		strings.TrimSpace(operation.TargetNodeID) != newRunnerNodeID {
		return fmt.Errorf("backup_job_runner_rebind_guest_operation_mismatch")
	}

	var existing clusterModels.BackupJobRunnerRebind
	result := s.DB.WithContext(ctx).Where("token = ?", operationToken).Limit(1).Find(&existing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		if existing.Kind != clusterModels.BackupJobRunnerRebindKindMigration ||
			existing.GuestType != guestType || existing.GuestID != guestID ||
			existing.OldRunnerNodeID != strings.TrimSpace(operation.OwnerNodeID) ||
			existing.NewRunnerNodeID != newRunnerNodeID ||
			existing.State == clusterModels.BackupJobRunnerRebindStateAborted {
			return fmt.Errorf("backup_job_runner_rebind_plan_mismatch")
		}
		return nil
	}

	if !bypassRaft {
		if _, _, err := s.backupJobRunnerVoter(newRunnerNodeID); err != nil {
			return fmt.Errorf("backup_job_runner_rebind_target_invalid: %w", err)
		}
	}
	var jobs []clusterModels.BackupJob
	if err := s.DB.WithContext(ctx).Order("id ASC").Find(&jobs).Error; err != nil {
		return err
	}
	matchedJobs := make([]clusterModels.BackupJob, 0)
	preflightErrors := make([]string, 0)
	for i := range jobs {
		jobGuestType, jobGuestID := clusterModels.BackupJobGuestIdentity(&jobs[i])
		if jobGuestType != guestType || jobGuestID != guestID {
			continue
		}
		matchedJobs = append(matchedJobs, jobs[i])
		preflightError := ""
		if err := validateBackupJobRunnerRebindConfiguration(s.DB.WithContext(ctx), &jobs[i]); err != nil {
			preflightError = boundedBackupJobRunnerRebindError(err)
		}
		preflightErrors = append(preflightErrors, preflightError)
	}
	if !bypassRaft && len(matchedJobs) != 0 {
		validationCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var validationWG sync.WaitGroup
		for i := range matchedJobs {
			if preflightErrors[i] != "" {
				continue
			}
			validationWG.Add(1)
			go func(index int) {
				defer validationWG.Done()
				_, _, err := s.validateBackupJobOnRunner(
					validationCtx,
					&matchedJobs[index],
					false,
					BackupJobPlacementAuthorization{GuestOperationToken: operationToken},
				)
				if err != nil {
					preflightErrors[index] = boundedBackupJobRunnerRebindError(err)
				}
			}(i)
		}
		validationWG.Wait()
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	items := make([]clusterModels.BackupJobRunnerRebindPlanItem, 0, len(matchedJobs))
	for i := range matchedJobs {
		items = append(items, clusterModels.BackupJobRunnerRebindPlanItem{
			JobID: matchedJobs[i].ID, ExpectedRunnerID: strings.TrimSpace(matchedJobs[i].RunnerNodeID),
			ExpectedFingerprint: clusterModels.BackupJobConfigurationFingerprint(&matchedJobs[i]),
			PreflightError:      preflightErrors[i],
		})
	}
	plan := clusterModels.BackupJobRunnerRebindPlan{
		Token: operationToken, Kind: clusterModels.BackupJobRunnerRebindKindMigration,
		GuestType: guestType, GuestID: guestID,
		OldRunnerNodeID: strings.TrimSpace(operation.OwnerNodeID), NewRunnerNodeID: newRunnerNodeID,
		Items: items,
	}
	return s.applyBackupJobRunnerRebindCommand("prepare", plan, bypassRaft)
}

func (s *Service) ReadyBackupJobRunnerRebind(operationToken string, bypassRaft bool) error {
	operationToken = strings.TrimSpace(operationToken)
	if !bypassRaft {
		if err := s.requireReplicationRaftLeader(); err != nil {
			return err
		}
		if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
			return fmt.Errorf("backup_job_runner_rebind_leader_barrier_failed: %w", err)
		}
		var operation clusterModels.BackupJobRunnerRebind
		if err := s.DB.Where("token = ?", operationToken).First(&operation).Error; err != nil {
			return err
		}
		if _, _, err := s.backupJobRunnerVoter(operation.NewRunnerNodeID); err != nil {
			return fmt.Errorf("backup_job_runner_rebind_target_invalid: %w", err)
		}
	}
	return s.applyBackupJobRunnerRebindCommand("ready", clusterModels.BackupJobRunnerRebindReady{
		Token: operationToken,
	}, bypassRaft)
}

func validateBackupJobRunnerRebindConfiguration(db *gorm.DB, job *clusterModels.BackupJob) error {
	if db == nil || job == nil || job.ID == 0 {
		return permanentBackupJobRebindFailure("backup_job_required")
	}
	if strings.TrimSpace(job.Name) == "" {
		return permanentBackupJobRebindFailure("name_required")
	}
	if job.TargetID == 0 {
		return permanentBackupJobRebindFailure("target_id_required")
	}
	var targetCount int64
	if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", job.TargetID).Count(&targetCount).Error; err != nil {
		return err
	}
	if targetCount != 1 {
		return permanentBackupJobRebindFailure("backup_target_not_found")
	}
	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	switch mode {
	case clusterModels.BackupJobModeDataset:
		if strings.TrimSpace(job.SourceDataset) == "" {
			return permanentBackupJobRebindFailure("source_dataset_required")
		}
		if job.StopBeforeBackup {
			return permanentBackupJobRebindFailure("stop_before_backup_not_supported_for_dataset_mode")
		}
	case clusterModels.BackupJobModeJail:
		if strings.TrimSpace(job.JailRootDataset) == "" {
			return permanentBackupJobRebindFailure("jail_root_dataset_required")
		}
	case clusterModels.BackupJobModeVM:
		if strings.TrimSpace(job.SourceDataset) == "" {
			return permanentBackupJobRebindFailure("source_dataset_required")
		}
		if !job.Recursive {
			return permanentBackupJobRebindFailure("vm_backup_requires_recursive")
		}
	default:
		return permanentBackupJobRebindFailure("invalid_backup_job_mode")
	}
	if job.PruneKeepLast < 0 {
		return permanentBackupJobRebindFailure("invalid_prune_keep_last")
	}
	if cronExpr := strings.TrimSpace(job.CronExpr); cronExpr != "" {
		if _, err := cron.ParseStandard(cronExpr); err != nil {
			return permanentBackupJobRebindFailure("invalid_cron_expr")
		}
	}
	if strings.TrimSpace(job.DestSuffix) != "" {
		var conflictCount int64
		if err := db.Model(&clusterModels.BackupJob{}).
			Where("target_id = ? AND dest_suffix = ? AND id != ?", job.TargetID, job.DestSuffix, job.ID).
			Count(&conflictCount).Error; err != nil {
			return err
		}
		if conflictCount != 0 {
			return permanentBackupJobRebindFailure(
				"dest_suffix_already_in_use: target_id=%d dest_suffix=%s", job.TargetID, job.DestSuffix,
			)
		}
	}
	return nil
}

func (s *Service) proposeBackupJobRunnerRebindPending(token string, item clusterModels.BackupJobRunnerRebindItem, cause error) error {
	reason := boundedBackupJobRunnerRebindError(cause)
	if reason == "" {
		reason = "backup_job_runner_rebind_pending"
	}
	return s.applyBackupJobRunnerRebindCommand("pending", clusterModels.BackupJobRunnerRebindPending{
		Token: token, JobID: item.JobID, Reason: reason,
	}, false)
}

func (s *Service) proposeBackupJobRunnerRebindRepair(token string, item clusterModels.BackupJobRunnerRebindItem, observedFingerprint string, cause error) error {
	reason := boundedBackupJobRunnerRebindError(cause)
	if reason == "" {
		reason = "backup_job_configuration_invalid"
	}
	return s.applyBackupJobRunnerRebindCommand("repair", clusterModels.BackupJobRunnerRebindRepair{
		Token: token, JobID: item.JobID, ExpectedFingerprint: item.ExpectedFingerprint,
		ObservedFingerprint: strings.TrimSpace(observedFingerprint), Reason: reason,
	}, false)
}

func (s *Service) reconcileBackupJobRunnerRebindItem(
	ctx context.Context,
	operation clusterModels.BackupJobRunnerRebind,
	item clusterModels.BackupJobRunnerRebindItem,
) error {
	var job clusterModels.BackupJob
	result := s.DB.WithContext(ctx).Where("id = ?", item.JobID).Limit(1).Find(&job)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return s.applyBackupJobRunnerRebindCommand("apply", clusterModels.BackupJobRunnerRebindApply{
			Token: operation.Token, JobID: item.JobID, ExpectedFingerprint: item.ExpectedFingerprint,
		}, false)
	}
	currentFingerprint := clusterModels.BackupJobConfigurationFingerprint(&job)
	configurationChanged := currentFingerprint != item.ExpectedFingerprint ||
		strings.TrimSpace(job.RunnerNodeID) != item.ExpectedRunnerID
	candidate := job
	candidate.RunnerNodeID = operation.NewRunnerNodeID
	authorization := BackupJobPlacementAuthorization{GuestOperationToken: operation.Token}
	validation, placementFence, err := s.validateBackupJobOnRunner(ctx, &candidate, false, authorization)
	if err != nil {
		if isPermanentBackupJobRunnerRebindError(err) {
			// A semantic rejection is still a bound target-runner receipt: node
			// identity, Raft catch-up, and request scope were verified before
			// the runner reported the invalid legacy configuration.
			observedFingerprint := ""
			if configurationChanged {
				observedFingerprint = currentFingerprint
			}
			return s.proposeBackupJobRunnerRebindRepair(operation.Token, item, observedFingerprint, err)
		}
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
	if configurationChanged {
		changedErr := permanentBackupJobRebindFailure("backup_job_configuration_changed_during_rebind")
		if repairErr := s.proposeBackupJobRunnerRebindRepair(
			operation.Token, item, currentFingerprint, changedErr,
		); repairErr != nil {
			return errors.Join(changedErr, repairErr)
		}
		return nil
	}
	if err := validateBackupJobRunnerRebindConfiguration(s.DB.WithContext(ctx), &job); err != nil {
		if isPermanentBackupJobRunnerRebindError(err) {
			return s.proposeBackupJobRunnerRebindRepair(operation.Token, item, "", err)
		}
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
	previousFence, err := s.backupJobPreviousPlacementFence(ctx, job.ID, authorization)
	if err != nil {
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
	apply := clusterModels.BackupJobRunnerRebindApply{
		Token: operation.Token, JobID: item.JobID,
		ExpectedFingerprint: item.ExpectedFingerprint,
		FriendlySource:      strings.TrimSpace(validation.FriendlySource),
		PlacementFence:      placementFence, PreviousPlacementFence: previousFence,
	}
	if err := s.applyBackupJobRunnerRebindCommand("apply", apply, false); err != nil {
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
	return nil
}

// ReconcileBackupJobRunnerRebind validates on the new runner and persists one
// exact-token terminal decision per item. Remote calls happen before proposal;
// FSM application reads replicated state only.
func (s *Service) ReconcileBackupJobRunnerRebind(ctx context.Context, operationToken string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_runner_rebind_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("backup_job_runner_rebind_leader_barrier_failed: %w", err)
	}
	s.backupJobRebindMu.Lock()
	defer s.backupJobRebindMu.Unlock()

	operationToken = strings.TrimSpace(operationToken)
	var operation clusterModels.BackupJobRunnerRebind
	if err := s.DB.WithContext(ctx).Where("token = ?", operationToken).First(&operation).Error; err != nil {
		return err
	}
	if clusterModels.IsBackupJobRunnerRebindTerminal(operation.State) {
		return nil
	}
	if operation.State != clusterModels.BackupJobRunnerRebindStateReady {
		return fmt.Errorf("backup_job_runner_rebind_not_ready")
	}
	var items []clusterModels.BackupJobRunnerRebindItem
	if err := s.DB.WithContext(ctx).
		Where("operation_token = ? AND state = ?", operation.Token, clusterModels.BackupJobRunnerRebindItemPending).
		Order("job_id ASC").Find(&items).Error; err != nil {
		return err
	}
	itemErrors := make([]string, 0)
	for i := range items {
		if err := ctx.Err(); err != nil {
			itemErrors = append(itemErrors, err.Error())
			break
		}
		if err := s.reconcileBackupJobRunnerRebindItem(ctx, operation, items[i]); err != nil {
			itemErrors = append(itemErrors, fmt.Sprintf("job_%d: %v", items[i].JobID, err))
		}
	}
	if err := s.DB.WithContext(ctx).Where("token = ?", operation.Token).First(&operation).Error; err != nil {
		return err
	}
	if clusterModels.IsBackupJobRunnerRebindTerminal(operation.State) {
		return nil
	}
	if len(itemErrors) == 0 {
		return fmt.Errorf("backup_job_runner_rebind_pending")
	}
	sort.Strings(itemErrors)
	return fmt.Errorf("backup_job_runner_rebind_pending: %s", strings.Join(itemErrors, "; "))
}

func (s *Service) reconcilePendingBackupJobRunnerRebinds(ctx context.Context) error {
	if s == nil || s.DB == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return nil
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("backup_job_runner_rebind_leader_barrier_failed: %w", err)
	}
	var operations []clusterModels.BackupJobRunnerRebind
	if err := s.DB.WithContext(ctx).
		Where("state = ?", clusterModels.BackupJobRunnerRebindStateReady).
		Order("token ASC").Find(&operations).Error; err != nil {
		return err
	}
	errs := make([]error, 0)
	for i := range operations {
		if err := s.ReconcileBackupJobRunnerRebind(ctx, operations[i].Token); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", operations[i].Token, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) StartBackupJobRunnerRebindReconciler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	reconcile := func() {
		reconcileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := s.reconcilePendingBackupJobRunnerRebinds(reconcileCtx); err != nil {
			logger.L.Warn().Err(err).Msg("backup_job_runner_rebind_reconciliation_pending")
		}
	}
	reconcile()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
