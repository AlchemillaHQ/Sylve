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
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

var maxSafeJSInt = big.NewInt(9007199254740991)

const backupJobIDGenerationAttempts = 2

func boolPtrDefaultTrue(v *bool) bool {
	if v == nil {
		return true
	}

	return *v
}

func backupJobIdentitySource(job *clusterModels.BackupJob, mode string) string {
	if job == nil {
		return ""
	}
	if mode == clusterModels.BackupJobModeJail {
		return normalizeManagedGuestDatasetPath(job.JailRootDataset)
	}
	return normalizeManagedGuestDatasetPath(job.SourceDataset)
}

func validateBackupJobUpdateIdentity(existing, proposed *clusterModels.BackupJob) error {
	if existing == nil || proposed == nil {
		return fmt.Errorf("backup_job_required")
	}
	if existing.TargetID != proposed.TargetID {
		return fmt.Errorf("backup_job_target_immutable")
	}

	existingMode := strings.ToLower(strings.TrimSpace(existing.Mode))
	proposedMode := strings.ToLower(strings.TrimSpace(proposed.Mode))
	if existingMode != proposedMode {
		return fmt.Errorf("backup_job_mode_immutable")
	}

	existingSource := backupJobIdentitySource(existing, existingMode)
	proposedSource := backupJobIdentitySource(proposed, proposedMode)
	switch existingMode {
	case clusterModels.BackupJobModeDataset:
		if existingSource != proposedSource {
			return fmt.Errorf("backup_job_source_immutable")
		}
		if strings.TrimSpace(existing.RunnerNodeID) != strings.TrimSpace(proposed.RunnerNodeID) {
			return fmt.Errorf("backup_job_runner_immutable")
		}
	case clusterModels.BackupJobModeJail, clusterModels.BackupJobModeVM:
		namespace := "jails"
		if existingMode == clusterModels.BackupJobModeVM {
			namespace = "virtual-machines"
		}
		existingID, existingOK := canonicalManagedGuestRootID(existingSource, namespace)
		proposedID, proposedOK := canonicalManagedGuestRootID(proposedSource, namespace)
		if !existingOK || !proposedOK {
			if existingSource != proposedSource {
				return fmt.Errorf("backup_job_source_immutable")
			}
			if strings.TrimSpace(existing.RunnerNodeID) != strings.TrimSpace(proposed.RunnerNodeID) {
				return fmt.Errorf("backup_job_runner_immutable")
			}
		} else if existingID != proposedID {
			return fmt.Errorf("backup_job_source_immutable")
		}
	}

	return nil
}

const BackupJobRuntimeStateVersion = 1

// BackupJobRuntimeStateUpdate carries runtime-only fields that should be
// synchronized cluster-wide after a backup run finishes. Encrypted is optional
// so legacy forwarded requests that omitted it preserve the committed value.
type BackupJobRuntimeStateUpdate struct {
	Version          uint       `json:"version,omitempty"`
	JobID            uint       `json:"jobId"`
	Token            string     `json:"token,omitempty"`
	HolderNodeID     string     `json:"holderNodeId,omitempty"`
	ScheduleRevision uint64     `json:"scheduleRevision,omitempty"`
	LastRunAt        *time.Time `json:"lastRunAt"`
	LastStatus       string     `json:"lastStatus"`
	LastError        string     `json:"lastError"`
	NextRunAt        *time.Time `json:"nextRunAt"`
	Encrypted        *bool      `json:"encrypted,omitempty"`
	NextRunOnly      bool       `json:"nextRunOnly,omitempty"`
}

type BackupJobFriendlySourceUpdate struct {
	GuestType   string `json:"guestType"`
	GuestID     uint   `json:"guestId"`
	FriendlySrc string `json:"friendlySrc"`
}

type backupJobFriendlySourceCommandPayload struct {
	JobIDs      []uint `json:"jobIds"`
	FriendlySrc string `json:"friendlySrc"`
}

func (s *Service) ListBackupTargets() ([]clusterModels.BackupTarget, error) {
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}
	if err := s.InitializeBackupTargetHostTrust(); err != nil {
		logger.L.Warn().Err(err).Msg("backup_target_host_key_initialization_failed")
	}
	var targets []clusterModels.BackupTarget
	if err := s.DB.Order("name ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	s.attachBackupTargetHostTrust(targets)
	if err := s.attachBackupTargetReadiness(targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Service) GetBackupTargetByID(id uint) (*clusterModels.BackupTarget, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}

	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Service) ProposeBackupTargetCreate(input clusterServiceInterfaces.BackupTargetReq, hostKey string) error {
	resolvedSSHKey, err := resolveSSHKeyMaterial(input.SSHKey, input.SSHKeyPath)
	if err != nil {
		return err
	}
	input.SSHKey = resolvedSSHKey
	candidate, err := s.BuildBackupTargetCreateCandidate(input)
	if err != nil {
		return err
	}
	candidate.SSHHostKey = hostKey
	_, err = s.ProposeBackupTargetCreateCandidate(candidate)
	return err
}

func (s *Service) ProposeBackupTargetUpdate(input clusterServiceInterfaces.BackupTargetReq) error {
	if input.ID == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	if !s.Standalone() && s.Raft() == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	existing, err := s.GetBackupTargetByID(input.ID)
	if err != nil {
		return err
	}
	plan, err := s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		return err
	}
	return s.ProposeBackupTargetUpdatePlan(plan)
}

func (s *Service) ProposeBackupTargetDelete(id uint) error {
	if id == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	return s.apply("backup_target", "delete_v2", struct {
		ID uint `json:"id"`
	}{ID: id})
}

func (s *Service) ListBackupJobs(targetID uint) ([]clusterModels.BackupJob, error) {
	var jobs []clusterModels.BackupJob
	query := s.DB.
		Preload("Target").
		Order("target_id ASC").
		Order("name ASC")
	if targetID > 0 {
		query = query.Where("target_id = ?", targetID)
	}
	err := query.Find(&jobs).Error
	return jobs, err
}

// ListBackupJobsForGuest returns jobs that belong to one guest. Guest identity
// is derived from the canonical source dataset so this also works for jobs
// created before a dedicated guest filter was exposed by the API.
func (s *Service) ListBackupJobsForGuest(targetID uint, guestType string, guestID uint) ([]clusterModels.BackupJob, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if !clusterModels.ValidReplicationGuestType(guestType) {
		return nil, fmt.Errorf("invalid_guest_type")
	}
	if guestID == 0 {
		return nil, fmt.Errorf("guest_id_required")
	}

	jobs, err := s.ListBackupJobs(targetID)
	if err != nil {
		return nil, err
	}

	filtered := make([]clusterModels.BackupJob, 0)
	for _, job := range jobs {
		switch guestType {
		case clusterModels.ReplicationGuestTypeVM:
			if job.Mode != clusterModels.BackupJobModeVM {
				continue
			}
			rid, ok := parseVMRIDFromDataset(job.SourceDataset)
			if ok && rid == guestID {
				filtered = append(filtered, job)
			}
		case clusterModels.ReplicationGuestTypeJail:
			if job.Mode != clusterModels.BackupJobModeJail {
				continue
			}
			ctID, ok := parseJailCTIDFromDataset(job.JailRootDataset)
			if !ok {
				ctID, ok = parseJailCTIDFromDataset(job.SourceDataset)
			}
			if ok && ctID == guestID {
				filtered = append(filtered, job)
			}
		}
	}

	return filtered, nil
}

// RunningJobIDsForTarget returns the IDs of jobs belonging to targetID that
// have a durable queued, running, or finishing operation. Backup events are
// node-local telemetry and must not determine cluster-visible active state.
//
// Raft-backed callers must read on the leader after a barrier so a lagging API
// ingress node cannot incorrectly report that no operation is active.
func (s *Service) RunningJobIDsForTarget(targetID uint) ([]uint, error) {
	if targetID == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("backup_job_database_unavailable")
	}
	if s.Raft() != nil {
		if s.Raft().State() != raft.Leader {
			return nil, fmt.Errorf("not_leader")
		}
		if err := s.Raft().Barrier(raftApplyTimeout).Error(); err != nil {
			return nil, fmt.Errorf("backup_job_operation_status_barrier_failed: %w", err)
		}
	}

	var target clusterModels.BackupTarget
	if err := s.DB.Select("id").First(&target, targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("backup_target_not_found")
		}
		return nil, fmt.Errorf("backup_target_lookup_failed: %w", err)
	}

	var jobIDs []uint
	err := s.DB.
		Table("backup_job_operations").
		Select("DISTINCT backup_job_operations.job_id").
		Joins("JOIN backup_jobs ON backup_jobs.id = backup_job_operations.job_id").
		Where("backup_jobs.target_id = ?", targetID).
		Order("backup_job_operations.job_id ASC").
		Pluck("backup_job_operations.job_id", &jobIDs).Error
	return jobIDs, err
}

func (s *Service) GetBackupJobByID(id uint) (*clusterModels.BackupJob, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_job_id")
	}

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Service) UpdateBackupJobRuntimeState(update BackupJobRuntimeStateUpdate) error {
	if update.Version > BackupJobRuntimeStateVersion {
		return fmt.Errorf("unsupported_backup_job_runtime_state_version")
	}
	if update.JobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}
	if strings.TrimSpace(update.Token) != "" {
		if update.LastRunAt == nil {
			return fmt.Errorf("backup_job_run_completed_at_required")
		}
		return s.CompleteBackupJobRun(clusterModels.BackupJobRunResult{
			JobID: update.JobID, Token: update.Token, HolderNodeID: update.HolderNodeID,
			ScheduleRevision: update.ScheduleRevision, CompletedAt: *update.LastRunAt,
			LastStatus: update.LastStatus, LastError: update.LastError,
			NextRunAt: update.NextRunAt, Encrypted: update.Encrypted,
		})
	}
	if !update.NextRunOnly {
		status := strings.TrimSpace(strings.ToLower(update.LastStatus))
		if status == "" {
			return fmt.Errorf("last_status_required")
		}
		if status != "success" && status != "failed" && status != "running" && status != "blocked" {
			return fmt.Errorf("invalid_last_status")
		}
		update.LastStatus = status
	}
	return s.apply("backup_job_state", "update", update)
}

func (s *Service) SyncBackupJobFriendlySourceByGuest(update BackupJobFriendlySourceUpdate) error {
	update.GuestType = strings.TrimSpace(strings.ToLower(update.GuestType))
	update.FriendlySrc = strings.TrimSpace(update.FriendlySrc)
	if !clusterModels.ValidReplicationGuestType(update.GuestType) {
		return fmt.Errorf("invalid_guest_type")
	}
	if update.GuestID == 0 {
		return fmt.Errorf("guest_id_required")
	}
	if update.FriendlySrc == "" {
		return fmt.Errorf("friendly_src_required")
	}

	jobIDs, err := s.matchBackupJobIDsForGuest(update.GuestType, update.GuestID)
	if err != nil {
		return err
	}
	if len(jobIDs) == 0 {
		return nil
	}
	// The job IDs come from this node's state, so a follower leaves the
	// whole update to the leader.
	return s.apply("backup_job_friendly_source", "update", backupJobFriendlySourceCommandPayload{
		JobIDs:      jobIDs,
		FriendlySrc: update.FriendlySrc,
	})
}

func (s *Service) SyncBackupJobFriendlySourceByGuestClusterWide(update BackupJobFriendlySourceUpdate) error {
	_, err := CallLeader[BackupJobFriendlySourceUpdate, struct{}](
		context.Background(), s, "backup-job-friendly-source", update,
	)
	return err
}

func (s *Service) matchBackupJobIDsForGuest(guestType string, guestID uint) ([]uint, error) {
	type jobRow struct {
		ID              uint
		SourceDataset   string
		JailRootDataset string
	}

	rows := []jobRow{}
	query := s.DB.Model(&clusterModels.BackupJob{}).
		Select("id", "source_dataset", "jail_root_dataset")

	switch guestType {
	case clusterModels.ReplicationGuestTypeVM:
		query = query.Where("mode = ?", clusterModels.BackupJobModeVM)
	case clusterModels.ReplicationGuestTypeJail:
		query = query.Where("mode = ?", clusterModels.BackupJobModeJail)
	default:
		return nil, fmt.Errorf("invalid_guest_type")
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_backup_jobs_for_guest: %w", err)
	}

	jobIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		switch guestType {
		case clusterModels.ReplicationGuestTypeVM:
			rid, ok := parseVMRIDFromDataset(row.SourceDataset)
			if ok && rid == guestID {
				jobIDs = append(jobIDs, row.ID)
			}
		case clusterModels.ReplicationGuestTypeJail:
			ctid, ok := parseJailCTIDFromDataset(row.JailRootDataset)
			if (!ok || ctid == 0) && strings.TrimSpace(row.SourceDataset) != "" {
				ctid, ok = parseJailCTIDFromDataset(row.SourceDataset)
			}
			if ok && ctid == guestID {
				jobIDs = append(jobIDs, row.ID)
			}
		}
	}

	return jobIDs, nil
}

func (s *Service) ProposeBackupJobCreate(input clusterServiceInterfaces.BackupJobReq) error {
	return s.ProposeBackupJobCreateContext(context.Background(), input)
}

func (s *Service) ProposeBackupJobCreateContext(
	ctx context.Context,
	input clusterServiceInterfaces.BackupJobReq,
) error {
	if !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
	}

	id, err := s.newAvailableBackupJobID(ctx)
	if err != nil {
		return fmt.Errorf("new_backup_job_id_failed: %w", err)
	}

	job, fence, err := s.buildBackupJob(ctx, id, input, BackupJobPlacementAuthorization{})
	if err != nil {
		return err
	}
	targetReadiness := job.TargetReadinessReceipt
	if targetReadiness == nil {
		return fmt.Errorf("backup_target_readiness_job_receipt_missing")
	}

	return s.apply("backup_job", "create", clusterModels.BackupJobCommandPayload{
		Job:             *job,
		PlacementFence:  fence,
		TargetReadiness: targetReadiness,
	})
}

func (s *Service) ProposeBackupJobUpdate(id uint, input clusterServiceInterfaces.BackupJobReq) error {
	return s.ProposeBackupJobUpdateContext(
		context.Background(), id, input, BackupJobPlacementAuthorization{},
	)
}

func (s *Service) ProposeBackupJobUpdateContext(
	ctx context.Context,
	id uint,
	input clusterServiceInterfaces.BackupJobReq,
	authorization BackupJobPlacementAuthorization,
) error {
	if !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
	}

	if id == 0 {
		return fmt.Errorf("invalid_job_id")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var existing clusterModels.BackupJob
	if err := s.DB.WithContext(ctx).Where("id = ?", id).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("backup_job_not_found")
		}
		return err
	}
	rebindPending, err := clusterModels.BackupJobRunnerRebindPendingForJob(s.DB.WithContext(ctx), id)
	if err != nil {
		return err
	}
	if rebindPending {
		return fmt.Errorf("backup_job_runner_rebind_pending")
	}
	if strings.TrimSpace(input.RunnerNodeID) == "" {
		input.RunnerNodeID = existing.RunnerNodeID
	}
	proposedMode := strings.ToLower(strings.TrimSpace(input.Mode))
	if proposedMode == "" {
		proposedMode = clusterModels.BackupJobModeDataset
	}
	proposedIdentity := clusterModels.BackupJob{
		TargetID: input.TargetID, RunnerNodeID: strings.TrimSpace(input.RunnerNodeID),
		Mode: proposedMode, SourceDataset: normalizeManagedGuestDatasetPath(input.SourceDataset),
		JailRootDataset: normalizeManagedGuestDatasetPath(input.JailRootDataset),
	}
	if err := validateBackupJobUpdateIdentity(&existing, &proposedIdentity); err != nil {
		return err
	}

	previousFence, err := s.backupJobPreviousPlacementFence(ctx, id, authorization)
	if err != nil {
		return err
	}
	job, fence, err := s.buildBackupJob(ctx, id, input, authorization)
	if err != nil {
		return err
	}
	if input.Encrypted == nil {
		job.Encrypted = existing.Encrypted
	}
	targetReadiness := job.TargetReadinessReceipt
	if targetReadiness == nil {
		return fmt.Errorf("backup_target_readiness_job_receipt_missing")
	}
	if strings.TrimSpace(existing.RunnerNodeID) != strings.TrimSpace(job.RunnerNodeID) {
		guestType, guestID := clusterModels.BackupJobGuestIdentity(job)
		if guestType != "" && guestID > 0 {
			if err := s.RequireGuestPlacement(ctx, guestType, guestID, job.RunnerNodeID); err != nil {
				return fmt.Errorf("backup_job_runner_placement_invalid: %w", err)
			}
		}
	}

	return s.apply("backup_job", "update", clusterModels.BackupJobCommandPayload{
		Job:                    *job,
		PlacementFence:         fence,
		PreviousPlacementFence: previousFence,
		TargetReadiness:        targetReadiness,
	})
}

func (s *Service) ProposeBackupJobDelete(id uint) error {
	if id == 0 {
		return fmt.Errorf("invalid_job_id")
	}
	return s.apply("backup_job", "delete", struct {
		ID uint `json:"id"`
	}{ID: id})
}

func (s *Service) buildBackupJob(
	ctx context.Context,
	id uint,
	input clusterServiceInterfaces.BackupJobReq,
	authorization BackupJobPlacementAuthorization,
) (*clusterModels.BackupJob, *clusterModels.BackupJobPlacementFence, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.TargetID == 0 {
		return nil, nil, fmt.Errorf("target_id_required")
	}

	var target clusterModels.BackupTarget
	if err := s.DB.WithContext(ctx).First(&target, input.TargetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("backup_target_not_found")
		}
		return nil, nil, fmt.Errorf("backup_target_lookup_failed: %w", err)
	}
	if _, err := remoteexec.ParseSSHDestination(target.SSHHost); err != nil {
		return nil, nil, fmt.Errorf("backup_target_ssh_host_invalid: %w", err)
	}
	if _, err := remoteexec.ParseZFSDataset(target.BackupRoot); err != nil {
		return nil, nil, fmt.Errorf("backup_target_root_invalid: %w", err)
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, nil, fmt.Errorf("name_required")
	}

	runnerNodeID := strings.TrimSpace(input.RunnerNodeID)
	if runnerNodeID == "" {
		if detail := s.Detail(); detail != nil {
			runnerNodeID = strings.TrimSpace(detail.NodeID)
		}
	}
	if runnerNodeID == "" {
		return nil, nil, fmt.Errorf("backup_runner_node_id_required")
	}

	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = clusterModels.BackupJobModeDataset
	}
	if mode != clusterModels.BackupJobModeDataset &&
		mode != clusterModels.BackupJobModeJail &&
		mode != clusterModels.BackupJobModeVM {
		return nil, nil, fmt.Errorf("invalid_mode")
	}

	var schedule cron.Schedule
	cronExpr := strings.TrimSpace(input.CronExpr)
	if cronExpr != "" {
		var err error
		schedule, err = cron.ParseStandard(cronExpr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid_cron_expr")
		}
	}

	now := time.Now().UTC()
	var next time.Time
	if schedule != nil {
		next = schedule.Next(now)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if !enabled {
		next = time.Time{}
	}

	job := &clusterModels.BackupJob{
		ID:               id,
		Name:             strings.TrimSpace(input.Name),
		TargetID:         input.TargetID,
		RunnerNodeID:     runnerNodeID,
		Mode:             mode,
		SourceDataset:    normalizeManagedGuestDatasetPath(input.SourceDataset),
		JailRootDataset:  normalizeManagedGuestDatasetPath(input.JailRootDataset),
		PruneKeepLast:    input.PruneKeepLast,
		PruneTarget:      input.PruneTarget,
		StopBeforeBackup: input.StopBeforeBackup,
		Recursive:        input.Recursive,
		CronExpr:         cronExpr,
		Enabled:          enabled,
		ScheduleRevision: 1,
	}
	if input.Encrypted != nil {
		job.Encrypted = *input.Encrypted
	}
	if job.PruneKeepLast < 0 {
		return nil, nil, fmt.Errorf("invalid_prune_keep_last")
	}

	switch mode {
	case clusterModels.BackupJobModeDataset:
		if job.SourceDataset == "" {
			return nil, nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	case clusterModels.BackupJobModeJail:
		if job.JailRootDataset == "" {
			return nil, nil, fmt.Errorf("jail_root_dataset_required")
		}
		job.SourceDataset = ""
	case clusterModels.BackupJobModeVM:
		if job.SourceDataset == "" {
			return nil, nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	}
	dataset := &job.SourceDataset
	if mode == clusterModels.BackupJobModeJail {
		dataset = &job.JailRootDataset
	}
	parsedDataset, err := remoteexec.ParseZFSDataset(*dataset)
	if err != nil {
		return nil, nil, fmt.Errorf("source_dataset_invalid: %w", err)
	}
	*dataset = parsedDataset.String()

	validation, placementFence, err := s.validateBackupJobOnRunner(ctx, job, authorization)
	if err != nil {
		return nil, nil, err
	}
	if job.StopBeforeBackup && mode == clusterModels.BackupJobModeDataset {
		return nil, nil, fmt.Errorf("stop_before_backup_not_supported_for_dataset_mode")
	}

	job.DestSuffix = autoBackupJobDestSuffix(job.ID, job.Mode, job.SourceDataset, job.JailRootDataset)
	if job.DestSuffix != "" {
		var conflictCount int64
		if err := s.DB.WithContext(ctx).Model(&clusterModels.BackupJob{}).
			Where("target_id = ? AND dest_suffix = ? AND id != ?", job.TargetID, job.DestSuffix, job.ID).
			Count(&conflictCount).Error; err != nil {
			return nil, nil, fmt.Errorf("dest_suffix_uniqueness_check_failed: %w", err)
		}
		if conflictCount > 0 {
			return nil, nil, fmt.Errorf(
				"dest_suffix_already_in_use: target_id=%d dest_suffix=%s",
				job.TargetID,
				job.DestSuffix,
			)
		}
	}

	job.FriendlySrc = strings.TrimSpace(validation.FriendlySource)
	job.TargetReadinessReceipt = validation.TargetReadiness
	if !next.IsZero() {
		job.NextRunAt = &next
	}
	return job, placementFence, nil
}

func autoBackupJobDestSuffix(jobID uint, mode, sourceDataset, jailRootDataset string) string {
	source := strings.TrimSpace(sourceDataset)
	if strings.TrimSpace(mode) == clusterModels.BackupJobModeJail {
		source = strings.TrimSpace(jailRootDataset)
	}

	base := autoBackupDestBase(source)
	if base == "" {
		base = "backups"
	}

	if jobID == 0 {
		return fmt.Sprintf("%s/j-pending/active", base)
	}

	return fmt.Sprintf("%s/j-%s/active", base, compactBackupJobToken(jobID))
}

func compactBackupJobToken(jobID uint) string {
	if jobID == 0 {
		return "0"
	}
	return strings.ToLower(strconv.FormatUint(uint64(jobID), 36))
}

func autoBackupDestBase(source string) string {
	source = normalizeBackupDatasetPath(source)
	if source == "" {
		return ""
	}

	parts := strings.Split(source, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		switch parts[i] {
		case "jails", "virtual-machines":
			return strings.Join(parts[i:], "/")
		}
	}

	return strings.ReplaceAll(source, "/", "-")
}

func normalizeBackupDatasetPath(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	for strings.HasSuffix(dataset, "/") {
		dataset = strings.TrimSuffix(dataset, "/")
	}
	return dataset
}

func parseJailCTIDFromDataset(dataset string) (uint, bool) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return 0, false
	}

	idx := strings.LastIndex(dataset, "/")
	if idx < 0 || idx == len(dataset)-1 {
		return 0, false
	}

	ctIDRaw := strings.TrimSpace(dataset[idx+1:])
	ctID, err := strconv.ParseUint(ctIDRaw, 10, 64)
	if err != nil {
		return 0, false
	}

	return uint(ctID), true
}

func parseVMRIDFromDataset(dataset string) (uint, bool) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return 0, false
	}

	parts := strings.Split(strings.Trim(dataset, "/"), "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "virtual-machines" {
			continue
		}

		ridRaw := strings.TrimSpace(parts[idx+1])
		if ridRaw == "" {
			continue
		}

		cutAt := len(ridRaw)
		if split := strings.IndexAny(ridRaw, "._"); split > 0 && split < cutAt {
			cutAt = split
		}
		ridRaw = strings.TrimSpace(ridRaw[:cutAt])
		if ridRaw == "" {
			continue
		}

		rid, err := strconv.ParseUint(ridRaw, 10, 64)
		if err == nil && rid > 0 {
			return uint(rid), true
		}
	}

	return 0, false
}

func canonicalBackupTargetInput(
	input clusterServiceInterfaces.BackupTargetReq,
) (remoteexec.SSHDestination, remoteexec.ZFSDataset, error) {
	if strings.TrimSpace(input.Name) == "" {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("name_required")
	}
	destination, err := remoteexec.ParseSSHDestination(input.SSHHost)
	if err != nil {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_ssh_host: %w", err)
	}
	root, err := remoteexec.ParseZFSDataset(input.BackupRoot)
	if err != nil {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_backup_root: %w", err)
	}
	if input.SSHPort < 0 || input.SSHPort > 65535 {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_ssh_port")
	}
	return destination, root, nil
}

func (s *Service) applyRaftCommand(cmd clusterModels.Command) error {
	r := s.Raft()
	if r == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if err := clusterModels.PrepareCommand(&cmd, time.Now()); err != nil {
		return fmt.Errorf("failed_to_prepare_command: %w", err)
	}
	_, err := s.raftApply(r, cmd)
	return err
}

// backupRunnerNodeExists is retained for replication-policy compatibility.
// Clustered callers resolve current Raft voters; standalone callers accept
// only the local machine identity. Health rows are never authoritative.
func (s *Service) backupRunnerNodeExists(nodeID string) bool {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false
	}

	if s.Raft() != nil {
		return s.RequireCurrentRaftVoter(nodeID) == nil
	}
	standalone, err := s.RuntimeStateBypassRaft()
	if err != nil || !standalone {
		return false
	}
	detail := s.Detail()
	return detail != nil && strings.TrimSpace(detail.NodeID) == nodeID
}

func (s *Service) newRaftObjectID(table string) (uint, error) {
	idRangeMax := raftObjectIDRangeForTable(table)
	n, err := rand.Int(rand.Reader, idRangeMax)
	if err != nil {
		return 0, err
	}
	return uint(n.Uint64() + 1), nil
}

func raftObjectIDRangeForTable(_ string) *big.Int {
	return maxSafeJSInt
}

func (s *Service) newAvailableBackupJobID(ctx context.Context) (uint, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("backup_job_database_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for attempt := 0; attempt < backupJobIDGenerationAttempts; attempt++ {
		var (
			id  uint
			err error
		)
		if s.backupJobIDGenerator != nil {
			id, err = s.backupJobIDGenerator()
		} else {
			id, err = s.newRaftObjectID("backup_jobs")
		}
		if err != nil {
			return 0, err
		}
		if id == 0 || uint64(id) > maxSafeJSInt.Uint64() {
			return 0, fmt.Errorf("generated_backup_job_id_out_of_range")
		}

		var existing clusterModels.BackupJob
		result := s.DB.WithContext(ctx).Select("id").Where("id = ?", id).Limit(1).Find(&existing)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 0 {
			return id, nil
		}
	}

	return 0, fmt.Errorf("backup_job_id_conflict")
}

func resolveSSHKeyMaterial(sshKey, sshKeyPath string) (string, error) {
	trimmedKey := strings.TrimSpace(sshKey)
	if trimmedKey != "" {
		return trimmedKey, nil
	}

	trimmedPath := strings.TrimSpace(sshKeyPath)
	if trimmedPath == "" {
		return "", nil
	}

	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return "", fmt.Errorf("ssh_key_file_not_found: %s: %w", trimmedPath, err)
	}

	return strings.TrimSpace(string(raw)), nil
}
