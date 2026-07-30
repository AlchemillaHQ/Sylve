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
		case "abort":
			value := payload.(clusterModels.BackupJobRunnerRebindAbort)
			return clusterModels.AbortFailedFailoverBackupJobRunnerRebindTxn(s.DB, &value)
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

type backupJobRunnerRebindPreparationAuthority struct {
	oldRunnerNodeID string
	authorization   BackupJobPlacementAuthorization
}

func (s *Service) backupJobRunnerRebindPreparationAuthority(
	ctx context.Context,
	kind string,
	guestType string,
	guestID uint,
	newRunnerNodeID string,
	token string,
) (backupJobRunnerRebindPreparationAuthority, error) {
	var authority backupJobRunnerRebindPreparationAuthority
	switch kind {
	case clusterModels.BackupJobRunnerRebindKindMigration:
		var operation clusterModels.ReplicationGuestOperation
		result := s.DB.WithContext(ctx).
			Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			Limit(1).Find(&operation)
		if result.Error != nil {
			return authority, result.Error
		}
		if result.RowsAffected != 1 || operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
			strings.TrimSpace(operation.Token) != token ||
			strings.TrimSpace(operation.OwnerNodeID) == "" ||
			strings.TrimSpace(operation.TargetNodeID) != newRunnerNodeID {
			return authority, fmt.Errorf("backup_job_runner_rebind_guest_operation_mismatch")
		}
		authority.oldRunnerNodeID = strings.TrimSpace(operation.OwnerNodeID)
		authority.authorization.GuestOperationToken = token
		return authority, nil
	case clusterModels.BackupJobRunnerRebindKindFailover:
		var policy clusterModels.ReplicationPolicy
		result := s.DB.WithContext(ctx).
			Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			Limit(1).Find(&policy)
		if result.Error != nil {
			return authority, result.Error
		}
		state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
		oldRunnerNodeID := strings.TrimSpace(policy.TransitionSourceNodeID)
		if result.RowsAffected != 1 || !policy.Enabled ||
			strings.TrimSpace(policy.TransitionRunID) != token ||
			oldRunnerNodeID == "" || strings.TrimSpace(policy.TransitionTargetNodeID) != newRunnerNodeID ||
			(state != clusterModels.ReplicationTransitionStateDemoting &&
				state != clusterModels.ReplicationTransitionStateCatchup) {
			return authority, fmt.Errorf("backup_job_runner_rebind_policy_transition_mismatch")
		}
		activeNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if activeNodeID == "" {
			activeNodeID = strings.TrimSpace(policy.SourceNodeID)
		}
		if activeNodeID != oldRunnerNodeID || policy.OwnerEpoch == 0 ||
			policy.TransitionOwnerEpoch != policy.OwnerEpoch {
			return authority, fmt.Errorf("backup_job_runner_rebind_policy_transition_not_pre_cutover")
		}
		authority.oldRunnerNodeID = oldRunnerNodeID
		authority.authorization.TransitionRunID = token
		return authority, nil
	default:
		return authority, fmt.Errorf("backup_job_runner_rebind_kind_invalid")
	}
}

// prepareBackupJobRunnerRebind captures the complete immutable job manifest
// while an exact migration or failover transition fences ordinary updates.
// The FSM independently revalidates completeness and authority.
func (s *Service) prepareBackupJobRunnerRebind(
	ctx context.Context,
	kind string,
	guestType string,
	guestID uint,
	newRunnerNodeID string,
	token string,
	bypassRaft bool,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_runner_rebind_service_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	newRunnerNodeID = strings.TrimSpace(newRunnerNodeID)
	token = strings.TrimSpace(token)
	if (guestType != clusterModels.BackupJobModeVM && guestType != clusterModels.BackupJobModeJail) ||
		guestID == 0 || newRunnerNodeID == "" || token == "" {
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

	authority, err := s.backupJobRunnerRebindPreparationAuthority(
		ctx, kind, guestType, guestID, newRunnerNodeID, token,
	)
	if err != nil {
		return err
	}
	var existing clusterModels.BackupJobRunnerRebind
	result := s.DB.WithContext(ctx).Where("token = ?", token).Limit(1).Find(&existing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		if existing.Kind != kind || existing.GuestType != guestType || existing.GuestID != guestID ||
			existing.OldRunnerNodeID != authority.oldRunnerNodeID ||
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
				_, _, validationErr := s.validateBackupJobOnRunner(
					validationCtx,
					&matchedJobs[index],
					false,
					authority.authorization,
				)
				if validationErr != nil {
					preflightErrors[index] = boundedBackupJobRunnerRebindError(validationErr)
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
	if !bypassRaft {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(newRunnerNodeID); err != nil {
			return fmt.Errorf("backup_job_runner_rebind_target_invalid: %w", err)
		}
	}
	return s.applyBackupJobRunnerRebindCommand("prepare", clusterModels.BackupJobRunnerRebindPlan{
		Token: token, Kind: kind, GuestType: guestType, GuestID: guestID,
		OldRunnerNodeID: authority.oldRunnerNodeID, NewRunnerNodeID: newRunnerNodeID,
		Items: items,
	}, bypassRaft)
}

func (s *Service) PrepareBackupJobRunnerRebind(
	ctx context.Context,
	guestType string,
	guestID uint,
	newRunnerNodeID string,
	operationToken string,
	bypassRaft bool,
) error {
	return s.prepareBackupJobRunnerRebind(
		ctx, clusterModels.BackupJobRunnerRebindKindMigration,
		guestType, guestID, newRunnerNodeID, operationToken, bypassRaft,
	)
}

func (s *Service) PrepareBackupJobRunnerRebindForFailover(
	ctx context.Context,
	guestType string,
	guestID uint,
	newRunnerNodeID string,
	transitionRunID string,
	bypassRaft bool,
) error {
	return s.prepareBackupJobRunnerRebind(
		ctx, clusterModels.BackupJobRunnerRebindKindFailover,
		guestType, guestID, newRunnerNodeID, transitionRunID, bypassRaft,
	)
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

func (s *Service) backupJobRunnerRebindAuthorization(
	ctx context.Context,
	operation clusterModels.BackupJobRunnerRebind,
) (BackupJobPlacementAuthorization, error) {
	switch operation.Kind {
	case clusterModels.BackupJobRunnerRebindKindMigration:
		return BackupJobPlacementAuthorization{GuestOperationToken: operation.Token}, nil
	case clusterModels.BackupJobRunnerRebindKindFailover:
		var policy clusterModels.ReplicationPolicy
		result := s.DB.WithContext(ctx).
			Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
			Limit(1).Find(&policy)
		if result.Error != nil {
			return BackupJobPlacementAuthorization{}, result.Error
		}
		activeNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if activeNodeID == "" {
			activeNodeID = strings.TrimSpace(policy.SourceNodeID)
		}
		if result.RowsAffected != 1 || !policy.Enabled ||
			strings.TrimSpace(policy.TransitionRunID) != operation.Token ||
			strings.TrimSpace(policy.TransitionSourceNodeID) != operation.OldRunnerNodeID ||
			strings.TrimSpace(policy.TransitionTargetNodeID) != operation.NewRunnerNodeID ||
			activeNodeID != operation.NewRunnerNodeID ||
			policy.OwnerEpoch == 0 || policy.TransitionOwnerEpoch != policy.OwnerEpoch {
			return BackupJobPlacementAuthorization{}, fmt.Errorf("backup_job_runner_rebind_policy_transition_mismatch")
		}
		if strings.ToLower(strings.TrimSpace(policy.TransitionState)) !=
			clusterModels.ReplicationTransitionStateCompleted {
			return BackupJobPlacementAuthorization{}, fmt.Errorf("backup_job_runner_rebind_transition_not_completed")
		}
		// Completed transitions no longer accept the run ID as a placement
		// bypass. Normal owner fencing now authorizes independent repair.
		return BackupJobPlacementAuthorization{}, nil
	default:
		return BackupJobPlacementAuthorization{}, fmt.Errorf("backup_job_runner_rebind_kind_invalid")
	}
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
	authorization, err := s.backupJobRunnerRebindAuthorization(ctx, operation)
	if err != nil {
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
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
	targetReadiness := validation.TargetReadiness
	if targetReadiness == nil {
		err := fmt.Errorf("backup_target_readiness_job_receipt_missing")
		_ = s.proposeBackupJobRunnerRebindPending(operation.Token, item, err)
		return err
	}
	apply := clusterModels.BackupJobRunnerRebindApply{
		Token: operation.Token, JobID: item.JobID,
		ExpectedFingerprint: item.ExpectedFingerprint,
		FriendlySource:      strings.TrimSpace(validation.FriendlySource),
		PlacementFence:      placementFence, PreviousPlacementFence: previousFence,
		TargetReadiness: targetReadiness,
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
		Where("state IN ?", []string{
			clusterModels.BackupJobRunnerRebindStatePlanned,
			clusterModels.BackupJobRunnerRebindStateReady,
		}).
		Order("token ASC").Find(&operations).Error; err != nil {
		return err
	}
	errs := make([]error, 0)
	for i := range operations {
		operation := operations[i]
		if operation.State == clusterModels.BackupJobRunnerRebindStatePlanned {
			if operation.Kind != clusterModels.BackupJobRunnerRebindKindFailover {
				continue
			}
			var policy clusterModels.ReplicationPolicy
			result := s.DB.WithContext(ctx).
				Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
				Limit(1).Find(&policy)
			if result.Error != nil {
				errs = append(errs, fmt.Errorf("%s: %w", operation.Token, result.Error))
				continue
			}
			sameRun := result.RowsAffected == 1 && strings.TrimSpace(policy.TransitionRunID) == operation.Token
			state := strings.ToLower(strings.TrimSpace(policy.TransitionState))
			if sameRun && (state == clusterModels.ReplicationTransitionStatePromoting ||
				state == clusterModels.ReplicationTransitionStateCompleted) {
				if err := s.ReadyBackupJobRunnerRebind(operation.Token, false); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", operation.Token, err))
					continue
				}
				operation.State = clusterModels.BackupJobRunnerRebindStateReady
			} else if !sameRun || state == clusterModels.ReplicationTransitionStateFailed ||
				state == clusterModels.ReplicationTransitionStateRollingBack {
				if err := s.applyBackupJobRunnerRebindCommand("abort", clusterModels.BackupJobRunnerRebindAbort{
					Token: operation.Token, Reason: "failover_transition_aborted",
				}, false); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", operation.Token, err))
				}
				continue
			} else {
				continue
			}
		}
		if operation.State == clusterModels.BackupJobRunnerRebindStateReady {
			if operation.Kind == clusterModels.BackupJobRunnerRebindKindFailover {
				var policy clusterModels.ReplicationPolicy
				result := s.DB.WithContext(ctx).
					Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
					Limit(1).Find(&policy)
				if result.Error != nil {
					errs = append(errs, fmt.Errorf("%s: %w", operation.Token, result.Error))
					continue
				}
				if result.RowsAffected != 1 || strings.TrimSpace(policy.TransitionRunID) != operation.Token ||
					strings.ToLower(strings.TrimSpace(policy.TransitionState)) != clusterModels.ReplicationTransitionStateCompleted {
					continue
				}
			}
			if err := s.ReconcileBackupJobRunnerRebind(ctx, operation.Token); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", operation.Token, err))
			}
		}
	}
	return errors.Join(errs...)
}

func appendBackupJobRunnerRebindHAReason(policy *clusterModels.ReplicationPolicy, reason string) {
	if policy == nil || strings.TrimSpace(reason) == "" {
		return
	}
	for _, existing := range policy.HAReasons {
		if existing == reason {
			return
		}
	}
	policy.HAReasons = append(policy.HAReasons, reason)
	sort.Strings(policy.HAReasons)
}

// applyBackupJobRunnerRebindHAState projects replicated pending/repair state
// into the existing policy health fields without changing persisted policy
// telemetry or the response envelope.
func (s *Service) applyBackupJobRunnerRebindHAState(policy *clusterModels.ReplicationPolicy) error {
	if s == nil || s.DB == nil || policy == nil || policy.GuestID == 0 ||
		!s.DB.Migrator().HasTable(&clusterModels.BackupJobRunnerRebind{}) ||
		!s.DB.Migrator().HasTable(&clusterModels.BackupJobRunnerRebindItem{}) {
		return nil
	}
	base := s.DB.Model(&clusterModels.BackupJobRunnerRebindItem{}).
		Joins("JOIN backup_job_runner_rebinds ON backup_job_runner_rebinds.token = backup_job_runner_rebind_items.operation_token").
		Where("backup_job_runner_rebinds.guest_type = ? AND backup_job_runner_rebinds.guest_id = ?",
			strings.ToLower(strings.TrimSpace(policy.GuestType)), policy.GuestID)
	var pending int64
	if err := base.
		Where("backup_job_runner_rebinds.state = ? AND backup_job_runner_rebind_items.state = ?",
			clusterModels.BackupJobRunnerRebindStateReady, clusterModels.BackupJobRunnerRebindItemPending).
		Count(&pending).Error; err != nil {
		return err
	}
	var repairs int64
	if err := s.DB.Model(&clusterModels.BackupJobRunnerRebindItem{}).
		Joins("JOIN backup_job_runner_rebinds ON backup_job_runner_rebinds.token = backup_job_runner_rebind_items.operation_token").
		Where("backup_job_runner_rebinds.guest_type = ? AND backup_job_runner_rebinds.guest_id = ? AND backup_job_runner_rebind_items.state = ?",
			strings.ToLower(strings.TrimSpace(policy.GuestType)), policy.GuestID,
			clusterModels.BackupJobRunnerRebindItemRepairRequired).
		Count(&repairs).Error; err != nil {
		return err
	}
	if pending != 0 {
		policy.HADegraded = true
		appendBackupJobRunnerRebindHAReason(policy, "backup_job_runner_rebind_pending")
	}
	if repairs != 0 {
		policy.HADegraded = true
		appendBackupJobRunnerRebindHAReason(policy, "backup_job_runner_rebind_repair_required")
	}
	return nil
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
