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
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// SnapshotInfo represents a single ZFS snapshot on the backup target.
type SnapshotInfo struct {
	Name       string `json:"name"`      // full dataset@snap name
	ShortName  string `json:"shortName"` // just the @snap portion
	Dataset    string `json:"dataset"`   // dataset portion (without @snap)
	Encrypted  bool   `json:"encrypted"`
	Creation   string `json:"creation"` // creation timestamp
	Used       string `json:"used"`     // space used
	Refer      string `json:"refer"`    // referenced size
	Guid       string `json:"guid,omitempty"`
	Lineage    string `json:"lineage,omitempty"`   // "active" | "rotated" | "other"
	OutOfBand  bool   `json:"outOfBand,omitempty"` // true when snapshot is outside the active lineage
	Committed  bool   `json:"committed,omitempty"` // verified c1 commit marker is present
	Legacy     bool   `json:"legacy,omitempty"`    // restore point predates commit manifests
	ChildCount int    `json:"childCount"`          // number of child datasets on the target
}

const restoreJobQueueName = "zelta-restore-run"

// restoreJobPayload is the goqite queue payload for a restore job.
type restoreJobPayload struct {
	JobID          uint   `json:"job_id"`
	Snapshot       string `json:"snapshot"` // e.g. "@zelta_2026-02-18_12.00.00"
	RemoteDataset  string `json:"remote_dataset,omitempty"`
	OperationToken string `json:"operation_token,omitempty"`
	HolderNodeID   string `json:"holder_node_id,omitempty"`
	EventID        uint   `json:"event_id,omitempty"`
	AuditRecordID  uint   `json:"audit_record_id,omitempty"`
}

func backupJobSnapshotTarget(job *clusterModels.BackupJob) (clusterModels.BackupTarget, string, error) {
	if job == nil {
		return clusterModels.BackupTarget{}, "", fmt.Errorf("backup_job_required")
	}
	target := job.Target
	if target.SSHHost == "" {
		return clusterModels.BackupTarget{}, "", fmt.Errorf("failed_to_list_remote_snapshots: target SSH host is empty (target not loaded?)")
	}
	_, rootDataset, err := canonicalizeBackupTarget(&target)
	if err != nil {
		return clusterModels.BackupTarget{}, "", err
	}
	remoteDataset := remoteDatasetForJob(job)
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil {
		return clusterModels.BackupTarget{}, "", fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	if !parsedRemoteDataset.Within(rootDataset) {
		return clusterModels.BackupTarget{}, "", fmt.Errorf("remote_dataset_outside_backup_root")
	}
	return target, parsedRemoteDataset.String(), nil
}

func (s *Service) listBackupJobSnapshotCandidates(
	ctx context.Context,
	job *clusterModels.BackupJob,
	target *clusterModels.BackupTarget,
	remoteDataset string,
) ([]SnapshotInfo, error) {
	snapshots, err := s.listRemoteSnapshotsWithLineage(ctx, target, remoteDataset)
	if err != nil {
		return nil, err
	}
	snapshots = filterSnapshotsForRestoreJob(job, target.BackupRoot, snapshots)
	snapshots = filterBackupSnapshots(snapshots)
	return filterSnapshotsForBackupJob(snapshots, job.ID), nil
}

func (s *Service) ListRemoteSnapshotsPage(
	ctx context.Context,
	job *clusterModels.BackupJob,
	request SnapshotPageRequest,
) (SnapshotPage, error) {
	started := time.Now()
	request, err := NewSnapshotPageRequest(request.Limit, request.Cursor)
	if err != nil {
		return SnapshotPage{}, err
	}
	target, remoteDataset, err := backupJobSnapshotTarget(job)
	if err != nil {
		return SnapshotPage{}, err
	}
	releaseTargetKey, err := s.acquireBackupTargetSSHKey(&target)
	if err != nil {
		return SnapshotPage{}, fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	defer releaseTargetKey()

	candidates, err := s.listBackupJobSnapshotCandidates(ctx, job, &target, remoteDataset)
	if err != nil {
		return SnapshotPage{}, err
	}
	if job.Mode == clusterModels.BackupJobModeVM {
		candidates = collapseSnapshotsByShortName(candidates)
	}
	pageCandidates, nextCursor, hasMore, err := paginateSnapshotCandidates(candidates, request)
	if err != nil {
		return SnapshotPage{}, err
	}
	items, err := s.filterRestorableBackupSnapshots(ctx, job, pageCandidates)
	if err != nil {
		return SnapshotPage{}, fmt.Errorf("failed_to_validate_backup_restore_points: %w", err)
	}

	childCount := s.remoteDatasetChildCount(ctx, &target, remoteDataset)
	for i := range items {
		items[i].ChildCount = childCount
	}
	page := SnapshotPage{Items: items, NextCursor: nextCursor, HasMore: hasMore}
	logger.L.Debug().
		Uint("job_id", job.ID).
		Int("candidate_count", len(candidates)).
		Int("page_candidate_count", len(pageCandidates)).
		Int("returned_count", len(items)).
		Int("limit", request.Limit).
		Bool("continued", request.Cursor != "").
		Bool("has_more", hasMore).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("backup_restore_snapshot_page_listed")
	return page, nil
}

// EnqueueRestoreJob enqueues a restore job for async execution via goqite.
func (s *Service) EnqueueRestoreJob(ctx context.Context, jobID uint, snapshot string) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return fmt.Errorf("snapshot_required")
	}

	// Verify job exists
	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	remoteDataset, normalizedSnapshot, destinationDataset, err := canonicalRestoreJobInput(&job, snapshot, "")
	if err != nil {
		return err
	}

	if !s.reserveJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	requestBytes, err := json.Marshal(restoreOperationRequest{
		Snapshot: normalizedSnapshot, RemoteDataset: remoteDataset,
	})
	if err != nil {
		s.releaseReservedJob(jobID)
		return err
	}
	handle, err := s.acquireDurableBackupJobOperation(
		ctx,
		jobID,
		clusterModels.BackupJobOperationRestore,
		string(requestBytes),
	)
	if err != nil {
		s.releaseReservedJob(jobID)
		if strings.Contains(strings.ToLower(err.Error()), "backup_job_running") {
			return fmt.Errorf("backup_job_already_running")
		}
		return err
	}

	eventSource := job.Name + normalizedSnapshot
	if job.Mode == clusterModels.BackupJobModeVM {
		eventSource = remoteDataset + normalizedSnapshot
	}
	jobIDCopy := job.ID
	execution, event, observabilityErr := s.prepareRestoreObservability(
		ctx,
		restoreAuditTypeJob,
		job.ID,
		handle.Token,
		restoreEventSpec{
			JobID:          &jobIDCopy,
			SourceDataset:  eventSource,
			TargetEndpoint: destinationDataset,
		},
	)
	if observabilityErr != nil {
		s.releaseReservedJob(jobID)
		abortCtx, abortCancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
		defer abortCancel()
		return errors.Join(observabilityErr, s.abortDurableBackupJobOperation(abortCtx, handle))
	}
	if restoreEventTerminal(event.Status) {
		s.releaseReservedJob(jobID)
		return s.finishDurableBackupJobOperation(handle)
	}

	queuePayload := restoreJobPayload{
		JobID: jobID, Snapshot: normalizedSnapshot, RemoteDataset: remoteDataset,
		OperationToken: handle.Token, HolderNodeID: handle.HolderNodeID,
		EventID: execution.EventID, AuditRecordID: execution.Audit.RecordID,
	}
	enqueueErr := error(nil)
	if s.backupOperationEnqueue != nil {
		enqueueErr = s.backupOperationEnqueue(ctx, restoreJobQueueName, queuePayload)
	} else {
		enqueueErr = db.EnqueueJSON(ctx, restoreJobQueueName, queuePayload)
	}
	if err := enqueueErr; err != nil {
		s.releaseReservedJob(jobID)
		abortCtx, abortCancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
		defer abortCancel()
		abortErr := s.abortDurableBackupJobOperation(abortCtx, handle)
		if abortErr == nil {
			if eventErr := s.finalizeRestoreEventByID(event.ID, fmt.Errorf("restore_queue_failed: %w", err), ""); eventErr != nil {
				return errors.Join(err, eventErr)
			}
		}
		return errors.Join(err, abortErr)
	}
	return nil
}

// restoreRequest is a validated restore whose destination its front-end holds.
type restoreRequest struct {
	target         *clusterModels.BackupTarget
	job            *clusterModels.BackupJob    // nil when restoring from a target
	newGuest       *oobGuestRestoreDestination // a target's guest restored as a new guest
	kind           string
	remoteDataset  string
	snapshot       string
	destination    string
	restoreNetwork bool
}

func (r restoreRequest) jobID() *uint {
	if r.job == nil {
		return nil
	}
	return &r.job.ID
}

// runRestoreJob restores a job's source from its own backups.
func (s *Service) runRestoreJob(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshot string,
	remoteDataset string,
) error {
	if job == nil || job.ID == 0 {
		return fmt.Errorf("backup_job_required")
	}
	if err := cluster.ValidateBackupJobSafetyWithDB(ctx, s.DB, job); err != nil {
		return fmt.Errorf("restore_backup_job_safety_check_failed: %w", err)
	}
	remoteDataset, snapshot, destination, err := canonicalRestoreJobInput(job, snapshot, remoteDataset)
	if err != nil {
		return err
	}
	if !job.Target.Enabled {
		return fmt.Errorf("backup_target_disabled")
	}
	releaseTargetKey, err := s.acquireBackupTargetSSHKey(&job.Target)
	if err != nil {
		return fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	defer releaseTargetKey()

	guestType, guestID := backupJobGuestIdentity(job)
	workloadType, workloadID := guestType, guestID
	if guestID == 0 {
		workloadType, workloadID = clusterModels.BackupJobModeDataset, datasetHash(destination)
	}
	release, err := s.lockRestoreDestination(
		ctx,
		destination,
		workloadType,
		workloadID,
		fmt.Sprintf("restore_job:%d", job.ID),
	)
	if err != nil {
		return err
	}
	defer release()
	if guestID > 0 {
		allowed, err := cluster.CanNodeMutateProtectedGuest(s.DB, guestType, guestID, s.localNodeID())
		if err != nil {
			return fmt.Errorf("pre_restore_lease_check_failed: %w", err)
		}
		if !allowed {
			return fmt.Errorf("lease_lost_before_restore: ownership transferred to another node")
		}
	}

	request := restoreRequest{
		target:         &job.Target,
		job:            job,
		kind:           job.Mode,
		remoteDataset:  remoteDataset,
		snapshot:       snapshot,
		destination:    destination,
		restoreNetwork: true,
	}
	if job.Mode == clusterModels.BackupJobModeVM {
		err = s.runRestoreVM(ctx, request)
	} else {
		err = s.runRestoreDataset(ctx, request)
	}
	if err != nil {
		return err
	}
	if err := s.advanceBackupJobScheduleAfterRestore(job); err != nil {
		s.appendRestoreOutput(ctx, fmt.Sprintf("restore_backup_schedule_advance_failed: %v", err))
	}
	return nil
}

// lockRestoreDestination keeps other restores and workload operations off a
// restore's destination. A plain dataset mustn't hold a registered guest.
func (s *Service) lockRestoreDestination(
	ctx context.Context,
	destination, workloadType string,
	workloadID uint,
	holder string,
) (func(), error) {
	if acquired, current := s.acquireRestoreDestination(destination); !acquired {
		return nil, fmt.Errorf(
			"restore_destination_already_running: dataset=%s holder=%s",
			destination,
			current,
		)
	}
	if acquired, current := s.acquireWorkloadOperation(workloadType, workloadID, holder); !acquired {
		s.releaseRestoreDestination(destination)
		return nil, fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			current,
			workloadType,
			workloadID,
		)
	}
	release := func() {
		s.releaseWorkloadOperation(workloadType, workloadID)
		s.releaseRestoreDestination(destination)
	}
	if workloadType == clusterModels.BackupJobModeDataset {
		if err := s.requireNoManagedGuestsWithinRestore(ctx, destination); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

// runRestoreDataset restores a dataset or a jail. A jail stays fenced and
// stopped until it's done, and its records come from the restored jail.json.
func (s *Service) runRestoreDataset(ctx context.Context, request restoreRequest) (retErr error) {
	var plan remoteRestoreDatasetPlan
	var err error
	if request.job != nil {
		plan, err = s.prepareJobRestoreDatasetPlan(ctx, request.job, request.remoteDataset, request.snapshot)
	} else {
		plan, err = s.prepareTargetRestoreDatasetPlan(ctx, request.target, request.remoteDataset, request.snapshot)
	}
	if err != nil {
		return err
	}
	var reservation *restoreGuestIdentityReservation
	if request.newGuest != nil {
		ctx, reservation, err = s.reserveGuestIdentityForRestore(ctx, request.newGuest.Kind, request.newGuest.GuestID)
		if err != nil {
			return err
		}
		defer s.releaseGuestIdentityReservationAfterError(ctx, reservation, &retErr)
		if err := s.requireOOBGuestRestoreAvailable(ctx, request.newGuest, nil, true); err != nil {
			return err
		}
	}
	if err := s.validateDestinationPoolsExist(ctx, []string{request.destination}); err != nil {
		return fmt.Errorf("restore_preflight_pool_check_failed: %w", err)
	}
	// Before the fence: a leftover staging dataset must not cost a hard stop.
	identity := newRestoreStagingIdentity(request.jobID(), request.target.ID, request.destination)
	if err := s.prepareRestoreStagingDataset(ctx, request.destination+".restoring", identity); err != nil {
		return fmt.Errorf("restore_preflight_staging_check_failed: %w", err)
	}

	var fence *restoreGuestFence
	restartJail := true
	if request.kind == clusterModels.BackupJobModeJail {
		execution := restoreExecutionFromContext(ctx)
		fence, err = s.acquireJailRestoreFence(ctx, request.destination, execution.EventID)
		if err == nil && fence == nil {
			err = fmt.Errorf("restore_fence_schema_unavailable")
		}
		if err != nil {
			return fmt.Errorf("acquire_jail_restore_fence_failed: %w", err)
		}
		defer func() {
			if fence == nil || retErr == nil {
				return
			}
			if err := fence.release(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("release_jail_restore_fence_failed: %w", err))
				return
			}
			if fence.wasRunning && restartJail {
				restartCtx, cancel := guestStateRecoveryContext(ctx)
				err := s.jailActionContext(restartCtx, int(fence.guestID), "start")
				cancel()
				if err != nil {
					retErr = errors.Join(retErr, fmt.Errorf("restore_jail_restart_failed: %w", err))
				}
			}
		}()
	}
	if fence != nil && request.newGuest == nil {
		if err := s.requireInPlaceGuestRestorePlacement(ctx, fence.guestType, fence.guestID); err != nil {
			return fmt.Errorf("restore_guest_placement_check_failed: %w", err)
		}
		existed, err := s.localDatasetExists(ctx, request.destination)
		if err != nil {
			return fmt.Errorf("failed_to_check_destination_dataset_before_restore: %w", err)
		}
		if existed {
			guard, err := s.prepareInPlaceJailRestore(ctx, fence.guestID, request.destination)
			if err != nil {
				return err
			}
			defer func() {
				var remountErr error
				if retErr, remountErr = guard.restoreAfterFailure(retErr); remountErr != nil {
					restartJail = false
				}
			}()
		}
	}

	restored, err := s.restoreDatasetRoot(ctx, request, plan, request.destination)
	if err != nil {
		return err
	}
	applied := []restoredDatasetBackup{restored}
	// Activate before reconcile writes the jail's rows, while it can still be rolled back.
	if request.job != nil {
		if _, err := s.activateTargetGenerationsForRestore(ctx, request.target, []restoreTargetGenerationSelection{{
			ActiveDataset:   remoteDatasetForJob(request.job),
			SelectedDataset: plan.resolvedRemoteDataset,
		}}); err != nil {
			return s.rollbackRestoreAfterError(applied, err)
		}
	}
	if request.newGuest != nil {
		if err := s.reconcileRestoredJailFromDatasetAsNew(ctx, request.destination, request.restoreNetwork); err != nil {
			return s.rollbackRestoreAfterError(applied, fmt.Errorf("reconcile_restored_jail_failed: %w", err))
		}
		if err := s.finalizeGuestIdentityRestore(ctx, reservation); err != nil {
			return err
		}
	} else if err := s.reconcileRestoredJailFromDatasetWithOptions(ctx, request.destination, request.restoreNetwork); err != nil {
		// The restored data stays; only the jail's records are behind.
		s.appendRestoreOutput(ctx, fmt.Sprintf("jail_metadata_reconcile_failed: %v", err))
		logger.L.Warn().
			Err(err).
			Str("dataset", request.destination).
			Msg("restore_jail_metadata_reconcile_failed_data_intact")
	}
	if request.job != nil {
		activeSuffix := relativeDatasetSuffix(request.target.BackupRoot, remoteDatasetForJob(request.job))
		if err := s.syncTargetBackupJobMetadata(ctx, request.job, request.destination, activeSuffix); err != nil {
			s.appendRestoreOutput(ctx, err.Error())
		}
	}
	s.cleanupRestoreArchives(ctx, applied)
	if fence != nil {
		if err := fence.release(); err != nil {
			return fmt.Errorf("release_jail_restore_fence_failed: %w", err)
		}
		fence = nil
	}
	return nil
}

// restoreDatasetRoot receives a planned root into a staging dataset next to
// destination, verifies it and swaps it in. A replaced dataset stays archived
// in the result until the caller removes it or rolls back.
func (s *Service) restoreDatasetRoot(
	ctx context.Context,
	request restoreRequest,
	plan remoteRestoreDatasetPlan,
	destination string,
) (restored restoredDatasetBackup, retErr error) {
	target := request.target
	releaseDownloadStorage, err := s.DownloadStorage.TryMutation(destination)
	if err != nil {
		return restored, err
	}
	defer releaseDownloadStorage()
	endpoint, err := canonicalZeltaSnapshotEndpoint(target, plan.resolvedRemoteDataset, plan.snapshot)
	if err != nil {
		return restored, err
	}
	s.appendRestoreOutput(ctx, fmt.Sprintf("dataset_restore_start: %s -> %s", endpoint, destination))
	defer func() {
		if retErr != nil {
			s.appendRestoreOutput(ctx, fmt.Sprintf("dataset_restore_failed: %s -> %s: %v", endpoint, destination, retErr))
			return
		}
		s.appendRestoreOutput(ctx, fmt.Sprintf("dataset_restore_complete: %s -> %s", endpoint, destination))
	}()

	restorePath := destination + ".restoring"
	identity := newRestoreStagingIdentity(request.jobID(), target.ID, destination)
	env, err := s.buildZeltaEnv(target)
	if err != nil {
		return restored, err
	}
	receiveTop, err := identity.receiveTopOptions()
	if err != nil {
		return restored, err
	}
	env = setEnvValue(env, "ZELTA_RECV_TOP", receiveTop)
	env = setEnvValue(env, "ZELTA_LOG_LEVEL", "3")
	if err := s.recheckRemoteRestoreDatasetPlan(ctx, target, plan); err != nil {
		return restored, fmt.Errorf("restore_preflight_recheck_failed: %w", err)
	}

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("remote", endpoint).
		Str("local", destination).
		Msg("starting_zelta_restore")
	output, err := s.pullRestore(
		ctx, target, env, func(line string) { s.appendRestoreOutput(ctx, line) },
		destination, endpoint, plan.snapshot, restorePath,
		identity, plan.expectedManifest, plan.restoreRecursive,
	)
	if err == nil {
		var staged bool
		if staged, err = s.localDatasetExists(ctx, restorePath); err != nil {
			err = fmt.Errorf("verify_restored_dataset_failed: %w", err)
		} else if !staged {
			err = zeltaRecvMissingError(restorePath, output)
		} else {
			err = s.verifyRestoreManifest(ctx, restorePath, plan.snapshot, plan.expectedManifest, plan.restoreRecursive)
		}
	}
	// Checked again after the long receive, before anything local changes.
	if err == nil && request.kind == clusterModels.BackupJobModeDataset {
		err = s.requireNoManagedGuestsWithinRestore(ctx, destination)
	}
	if err != nil {
		logger.L.Warn().
			Err(err).
			Str("restore_path", restorePath).
			Str("zelta_output", output).
			Msg("zelta_restore_failed")
		return restored, s.cleanupOwnedRestoreStagingAfterError(restorePath, identity, err)
	}

	restored.destination = destination
	if request.newGuest != nil {
		err = s.promoteRestoredDatasetAsNew(ctx, restorePath, destination)
	} else {
		restored.backup, err = s.promoteRestoredDataset(ctx, restorePath, destination)
	}
	if err != nil {
		err = fmt.Errorf("rename_restore_failed: could not promote %s → %s: %w", restorePath, destination, err)
		return restored, s.cleanupOwnedRestoreStagingAfterError(restorePath, identity, err)
	}
	if err = s.clearRestoreStagingProperties(ctx, destination, identity); err == nil {
		err = s.fixRestoredProperties(ctx, destination)
	}
	if err != nil {
		return restored, s.rollbackRestoreAfterError(
			[]restoredDatasetBackup{restored},
			fmt.Errorf("restore_activation_failed: %w", err),
		)
	}
	logger.L.Info().
		Uint("target_id", target.ID).
		Str("dataset", destination).
		Str("snapshot", plan.snapshot).
		Msg("zelta_restore_completed")
	return restored, nil
}

func (s *Service) appendRestoreOutput(ctx context.Context, line string) {
	execution := restoreExecutionFromContext(ctx)
	if err := s.AppendBackupEventOutput(execution.EventID, line); err != nil {
		logger.L.Warn().Err(err).Uint("event_id", execution.EventID).Msg("append_restore_event_output_failed")
	}
}

type restoreGuestFence struct {
	service    *Service
	guestType  string
	guestID    uint
	token      string
	wasRunning bool
}

func (s *Service) requireInPlaceGuestRestorePlacement(
	ctx context.Context,
	guestType string,
	guestID uint,
) error {
	if s == nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: cluster_service_not_initialized")
	}
	return s.Cluster.RequireGuestRestorePlacement(ctx, guestType, guestID, "")
}

func (s *Service) acquireJailRestoreFence(ctx context.Context, dataset string, eventID uint) (*restoreGuestFence, error) {
	if s == nil || s.Jail == nil || s.DB == nil {
		return nil, fmt.Errorf("jail_restore_fence_service_unavailable")
	}
	ctID, err := s.Jail.GetJailCTIDFromDataset(dataset)
	if err != nil {
		return nil, fmt.Errorf("jail_restore_fence_identity_lookup_failed: %w", err)
	}
	if ctID == 0 {
		return nil, fmt.Errorf("jail_restore_fence_identity_invalid")
	}
	fence, err := s.acquireRestoreGuestFence(ctx, clusterModels.ReplicationGuestTypeJail, ctID, eventID)
	if err != nil || fence == nil {
		return fence, err
	}

	fence.wasRunning, err = s.Jail.IsJailRunning(ctID)
	if err != nil {
		_ = fence.release()
		return nil, fmt.Errorf("jail_restore_fence_state_check_failed: %w", err)
	}
	if !fence.wasRunning {
		return fence, nil
	}
	if err := s.Jail.ForceStopJail(ctID); err != nil {
		_ = fence.release()
		return nil, fmt.Errorf("jail_restore_fence_stop_failed: %w", err)
	}
	if err := s.waitForJailRestoreStopped(ctx, ctID); err != nil {
		_ = fence.release()
		return nil, err
	}
	return fence, nil
}

func (s *Service) acquireVMRestoreFence(ctx context.Context, rid, eventID uint) (*restoreGuestFence, error) {
	if s == nil || s.VM == nil || s.DB == nil {
		return nil, fmt.Errorf("vm_restore_fence_service_unavailable")
	}
	fence, err := s.acquireRestoreGuestFence(ctx, clusterModels.ReplicationGuestTypeVM, rid, eventID)
	if err != nil || fence == nil {
		return fence, err
	}

	shutOff, err := s.VM.IsDomainShutOff(rid)
	if err != nil {
		if isVMDomainNotFoundError(err) {
			return fence, nil
		}
		_ = fence.release()
		return nil, fmt.Errorf("vm_restore_fence_state_check_failed: %w", err)
	}
	if shutOff {
		return fence, nil
	}
	fence.wasRunning = true
	if err := s.VM.ForceStopVM(rid); err != nil {
		_ = fence.release()
		return nil, fmt.Errorf("vm_restore_fence_stop_failed: %w", err)
	}
	shutOff, err = s.VM.IsDomainShutOff(rid)
	if err != nil || !shutOff {
		_ = fence.release()
		if err != nil {
			return nil, fmt.Errorf("vm_restore_fence_stop_check_failed: %w", err)
		}
		return nil, fmt.Errorf("vm_restore_fence_stop_incomplete")
	}
	return fence, nil
}

func (s *Service) acquireRestoreGuestFence(
	ctx context.Context,
	guestType string,
	guestID, eventID uint,
) (*restoreGuestFence, error) {
	if s == nil || s.DB == nil || guestID == 0 {
		return nil, fmt.Errorf("restore_fence_input_invalid")
	}
	if !replicationguard.GuestOperationSchemaReady(s.DB) {
		return nil, nil
	}
	ownerNodeID := s.localNodeID()
	fence := &restoreGuestFence{
		service:   s,
		guestType: guestType,
		guestID:   guestID,
		token:     fmt.Sprintf("restore:%s:%d:%s", ownerNodeID, eventID, compactNowToken()),
	}
	acquire := clusterModels.ReplicationGuestOperationAcquire{
		GuestType:   guestType,
		GuestID:     guestID,
		Operation:   clusterModels.ReplicationGuestOperationRestore,
		Token:       fence.token,
		OwnerNodeID: ownerNodeID,
		TaskID:      eventID,
		AcquiredAt:  time.Now().UTC(),
	}
	transition := clusterModels.ReplicationGuestOperationTransition{
		GuestType: guestType,
		GuestID:   guestID,
		Operation: clusterModels.ReplicationGuestOperationRestore,
		Token:     fence.token,
	}
	if err := s.applyGuestMigrationInterlock(ctx, "acquire", acquire, transition); err != nil {
		return nil, err
	}
	return fence, nil
}

func (f *restoreGuestFence) release() error {
	if f == nil || f.service == nil {
		return nil
	}
	transition := clusterModels.ReplicationGuestOperationTransition{
		GuestType: f.guestType,
		GuestID:   f.guestID,
		Operation: clusterModels.ReplicationGuestOperationRestore,
		Token:     f.token,
	}
	ctx, cancel := restoreRecoveryContext()
	defer cancel()
	return f.service.applyGuestMigrationInterlock(ctx, "abort", clusterModels.ReplicationGuestOperationAcquire{}, transition)
}

// advanceBackupJobScheduleAfterRestore prevents an overdue pre-restore
// schedule from immediately starting another backup when the restore releases
// the job lock.
func (s *Service) advanceBackupJobScheduleAfterRestore(job *clusterModels.BackupJob) error {
	if job == nil || job.ID == 0 {
		return fmt.Errorf("backup_job_required")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_database_unavailable")
	}
	var current clusterModels.BackupJob
	if err := s.DB.First(&current, job.ID).Error; err != nil {
		return err
	}
	if !current.Enabled {
		return nil
	}
	var operation clusterModels.BackupJobOperation
	if err := s.DB.Where("job_id = ? AND operation = ?", job.ID, clusterModels.BackupJobOperationRestore).
		First(&operation).Error; err != nil {
		return err
	}
	if current.ScheduleRevision != operation.ScheduleRevision {
		return nil
	}

	now := time.Now().UTC()
	nextRunAt, err := nextRunTime(current.CronExpr, now)
	if err != nil {
		return fmt.Errorf("next_backup_run: %w", err)
	}

	decision := clusterModels.BackupJobScheduleDecision{
		JobID: current.ID, ExpectedScheduleRevision: current.ScheduleRevision,
		ExpectedNextRunAt: current.NextRunAt, NextRunAt: &nextRunAt, DecidedAt: now,
	}
	if err := s.callLeader(context.Background(), "backup-job-schedule", decision); err != nil {
		return fmt.Errorf("update_next_backup_run: %w", err)
	}
	job.NextRunAt = &nextRunAt
	job.ScheduleRevision = current.ScheduleRevision + 1
	return nil
}

// zelta logs a failed zfs send or receive (a target user without send
// permission, a missing snapshot) at info level and still exits 0.
func zeltaRecvMissingError(restorePath, output string) error {
	err := fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist", restorePath)
	var reasons []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "info:"))
		if strings.Contains(line, "cannot ") {
			reasons = append(reasons, line)
		}
	}
	if len(reasons) == 0 {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.Join(reasons, "; "))
}

func restoreZeltaArgs(remoteEndpoint, restorePath string, recursive bool) []string {
	args := []string{
		"backup",
		"--json",
		"--no-snapshot",
	}
	if !recursive {
		args = append(args, "--depth", "1")
	}
	return append(args, remoteEndpoint, restorePath)
}

type restoreDatasetManifestEntry struct {
	Suffix       string
	Type         string
	SnapshotGUID string
}

func (s *Service) verifyRestoreManifest(
	ctx context.Context,
	restoreRoot string,
	snapshot string,
	expected []restoreDatasetManifestEntry,
	recursive bool,
) error {
	restoreRoot = normalizeDatasetPath(restoreRoot)
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return err
	}

	datasetOutput, err := utils.RunCommandWithContext(
		ctx,
		"zfs", "list", "-H", "-p", "-r", "-t", "filesystem,volume", "-o", "name,type", restoreRoot,
	)
	if err != nil {
		return fmt.Errorf("list_recursive_restore_staging_dataset_tree_failed: %w", err)
	}
	actual, err := parseRestoreDatasetTree(datasetOutput, restoreRoot)
	if err != nil {
		return err
	}

	snapshotOutput, err := utils.RunCommandWithContext(
		ctx,
		"zfs", "list", "-H", "-p", "-r", "-t", "snapshot", "-o", "name,guid", restoreRoot,
	)
	if err != nil {
		return fmt.Errorf("list_recursive_restore_staging_snapshot_tree_failed: %w", err)
	}
	guids, err := parseReplicationSnapshotTreeGUIDs(
		snapshotOutput,
		restoreRoot,
		strings.TrimPrefix(snapshot, "@"),
	)
	if err != nil {
		return fmt.Errorf("parse_recursive_restore_staging_snapshot_tree_failed: %w", err)
	}
	for idx := range actual {
		dataset := datasetForRestoreManifestSuffix(restoreRoot, actual[idx].Suffix)
		actual[idx].SnapshotGUID = strings.TrimSpace(guids[dataset])
	}

	problems := compareRestoreDatasetManifests(expected, actual)
	if len(problems) > 0 {
		mode := "nonrecursive"
		if recursive {
			mode = "recursive"
		}
		return fmt.Errorf(
			"%s_restore_staging_manifest_mismatch: %s",
			mode,
			strings.Join(problems, "; "),
		)
	}
	return nil
}

func parseRestoreDatasetTree(output, root string) ([]restoreDatasetManifestEntry, error) {
	root = normalizeDatasetPath(root)
	if root == "" {
		return nil, fmt.Errorf("restore_root_dataset_required")
	}

	seen := make(map[string]struct{})
	manifest := make([]restoreDatasetManifestEntry, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_restore_dataset_tree_entry: %q", line)
		}
		dataset := normalizeDatasetPath(fields[0])
		if !datasetWithinRoot(root, dataset) {
			return nil, fmt.Errorf("restore_dataset_outside_tree: %s", dataset)
		}
		suffix := relativeDatasetSuffix(root, dataset)
		if _, ok := seen[suffix]; ok {
			return nil, fmt.Errorf("duplicate_restore_dataset_tree_entry: %s", dataset)
		}
		seen[suffix] = struct{}{}
		datasetType := strings.ToLower(strings.TrimSpace(fields[1]))
		if datasetType != "filesystem" && datasetType != "volume" {
			return nil, fmt.Errorf("invalid_restore_dataset_type: dataset=%s type=%s", dataset, datasetType)
		}
		manifest = append(manifest, restoreDatasetManifestEntry{
			Suffix: suffix,
			Type:   datasetType,
		})
	}
	if _, ok := seen[""]; !ok {
		return nil, fmt.Errorf("restore_dataset_tree_root_missing: %s", root)
	}
	sort.Slice(manifest, func(i, j int) bool {
		return manifest[i].Suffix < manifest[j].Suffix
	})
	return manifest, nil
}

func compareRestoreDatasetManifests(expected, actual []restoreDatasetManifestEntry) []string {
	expectedBySuffix := make(map[string]restoreDatasetManifestEntry, len(expected))
	actualBySuffix := make(map[string]restoreDatasetManifestEntry, len(actual))
	for _, entry := range expected {
		entry.Suffix = normalizeDatasetPath(entry.Suffix)
		expectedBySuffix[entry.Suffix] = entry
	}
	for _, entry := range actual {
		entry.Suffix = normalizeDatasetPath(entry.Suffix)
		actualBySuffix[entry.Suffix] = entry
	}

	problems := make([]string, 0)
	for suffix, expectedEntry := range expectedBySuffix {
		actualEntry, ok := actualBySuffix[suffix]
		if !ok {
			problems = append(problems, "missing_dataset="+formatRestoreDatasetSuffix(suffix))
			continue
		}
		if expectedEntry.Type != actualEntry.Type {
			problems = append(problems, fmt.Sprintf(
				"dataset_type_mismatch=%s expected=%s actual=%s",
				formatRestoreDatasetSuffix(suffix),
				expectedEntry.Type,
				actualEntry.Type,
			))
		}
		if strings.TrimSpace(actualEntry.SnapshotGUID) == "" {
			problems = append(problems, "selected_snapshot_missing="+formatRestoreDatasetSuffix(suffix))
		} else if expectedEntry.SnapshotGUID != actualEntry.SnapshotGUID {
			problems = append(problems, fmt.Sprintf(
				"snapshot_guid_mismatch=%s expected=%s actual=%s",
				formatRestoreDatasetSuffix(suffix),
				expectedEntry.SnapshotGUID,
				actualEntry.SnapshotGUID,
			))
		}
	}
	for suffix := range actualBySuffix {
		if _, ok := expectedBySuffix[suffix]; !ok {
			problems = append(problems, "unexpected_dataset="+formatRestoreDatasetSuffix(suffix))
		}
	}
	sort.Strings(problems)
	return problems
}

func datasetForRestoreManifestSuffix(root, suffix string) string {
	root = normalizeDatasetPath(root)
	suffix = normalizeDatasetPath(suffix)
	if suffix == "" {
		return root
	}
	return root + "/" + suffix
}

func formatRestoreDatasetSuffix(suffix string) string {
	suffix = normalizeDatasetPath(suffix)
	if suffix == "" {
		return "<root>"
	}
	return suffix
}

// fixRestoredProperties corrects ZFS properties after a restore so the dataset
// behaves like the original (not readonly, correct mountpoint, canmount=on).
// For encrypted datasets, it ensures the key file is present and loads the key
// before mounting. Walks all child datasets under the root to handle
// independent encryption roots.
func (s *Service) fixRestoredProperties(ctx context.Context, dataset string) error {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil
	}

	output, err := utils.RunCommandWithContext(ctx, "zfs", backupDatasetListArgs(dataset, true)[1:]...)
	if err != nil {
		return fmt.Errorf("list_restored_datasets_failed: %w", err)
	}
	types, err := parseBackupDatasetTree(output, dataset, true)
	if err != nil {
		return fmt.Errorf("list_restored_datasets_failed: %w", err)
	}
	subtree := make([]string, 0, len(types))
	for name := range types {
		subtree = append(subtree, name)
	}
	sort.SliceStable(subtree, func(i, j int) bool {
		di, dj := strings.Count(subtree[i], "/"), strings.Count(subtree[j], "/")
		if di == dj {
			return subtree[i] < subtree[j]
		}
		return di < dj
	})

	var restoreErrors []error
	for _, dsName := range subtree {
		volume := types[dsName] == "volume"
		ds, err := s.getLocalDataset(ctx, dsName)
		if err != nil {
			logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_get_failed")
			restoreErrors = append(restoreErrors, fmt.Errorf("get_restored_dataset_%s_failed: %w", dsName, err))
			continue
		}
		if ds == nil {
			logger.L.Warn().Str("dataset", dsName).Msg("fix_restored_property_dataset_not_found")
			restoreErrors = append(restoreErrors, fmt.Errorf("restored_dataset_not_found: %s", dsName))
			continue
		}

		if ds.IsEncrypted() {
			keyLoaded, err := s.ensureEncryptionKeyForDataset(ctx, ds)
			if err != nil {
				logger.L.Error().Err(err).Str("dataset", dsName).Msg("fix_restored_property_key_load_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("restore_encryption_key_load_failed_for_%s: %w", dsName, err))
				continue
			}
			if !keyLoaded {
				logger.L.Error().Str("dataset", dsName).Msg("fix_restored_property_key_not_auto_loaded")
				restoreErrors = append(restoreErrors, fmt.Errorf("restore_encryption_key_required_for_%s", dsName))
				continue
			}
		}

		// Volumes have no canmount, mountpoint or mount.
		if volume {
			if err := ds.SetProperties(ctx, "readonly", "off"); err != nil {
				logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_volume_readonly_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("set_restored_volume_%s_readonly_failed: %w", dsName, err))
			}
			continue
		}
		if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
			logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_set_failed")
			restoreErrors = append(restoreErrors, fmt.Errorf("set_restored_properties_for_%s_failed: %w", dsName, err))
			continue
		}
		if dsName == dataset {
			if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", "mountpoint", dsName); err != nil {
				logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_inherit_mountpoint_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("inherit_restored_mountpoint_for_%s_failed: %w", dsName, err))
				continue
			}
		}
		if err := ds.Mount(ctx, false); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already mounted") {
				continue
			}
			logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_mount_failed")
			restoreErrors = append(restoreErrors, fmt.Errorf("mount_restored_dataset_%s_failed: %w", dsName, err))
		}
	}
	return errors.Join(restoreErrors...)
}

// registerRestoreJob registers the restore queue handler with goqite.
func (s *Service) registerRestoreJob() {
	db.QueueRegisterJSON(restoreJobQueueName, s.handleRestoreJobQueue)
}

func (s *Service) handleRestoreJobQueue(ctx context.Context, payload restoreJobPayload) error {
	defer s.releaseReservedJob(payload.JobID)
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = admittedCtx
	if payload.JobID == 0 {
		logger.L.Warn().Msg("queued_restore_job_invalid_payload_job_id")
		return nil
	}
	requestBytes, err := json.Marshal(restoreOperationRequest{
		Snapshot: payload.Snapshot, RemoteDataset: payload.RemoteDataset,
	})
	if err != nil {
		return err
	}
	handle, execute, err := s.prepareQueuedBackupJobOperation(
		ctx,
		payload.JobID,
		clusterModels.BackupJobOperationRestore,
		payload.OperationToken,
		payload.HolderNodeID,
		string(requestBytes),
	)
	if err != nil {
		return err
	}
	if !execute {
		_ = s.reconcileRestoreEventWithoutExecution(handle.Token)
		return nil
	}
	// A redelivered message leaves the delivery that's still running alone.
	if !s.beginJob(payload.JobID) {
		logger.L.Info().Uint("job_id", payload.JobID).Msg("queued_restore_job_duplicate_discarded")
		return nil
	}
	defer s.releaseJob(payload.JobID)

	var job clusterModels.BackupJob
	jobErr := s.DB.Preload("Target").First(&job, payload.JobID).Error
	jobID := payload.JobID
	spec := restoreEventSpec{
		JobID:          &jobID,
		SourceDataset:  fmt.Sprintf("backup-job-%d%s", payload.JobID, payload.Snapshot),
		TargetEndpoint: "unknown",
	}
	if jobErr == nil {
		destinationDataset := strings.TrimSpace(job.SourceDataset)
		if job.Mode == clusterModels.BackupJobModeJail {
			destinationDataset = strings.TrimSpace(job.JailRootDataset)
		}
		spec.SourceDataset = job.Name + payload.Snapshot
		if job.Mode == clusterModels.BackupJobModeVM {
			spec.SourceDataset = payload.RemoteDataset + payload.Snapshot
		}
		spec.TargetEndpoint = destinationDataset
	}
	execution := newRestoreExecution(payload.EventID, handle.Token, payload.AuditRecordID)
	finish := func() error { return s.finishDurableBackupJobOperation(handle) }
	return s.runQueuedRestore(ctx, execution, spec, finish, func(ctx context.Context) error {
		if jobErr != nil {
			return jobErr
		}
		if s.restoreJobRun != nil {
			return s.restoreJobRun(ctx, &job, payload.Snapshot, payload.RemoteDataset)
		}
		return s.runRestoreJob(ctx, &job, payload.Snapshot, payload.RemoteDataset)
	})
}

func newRestoreExecution(eventID uint, operationID string, auditRecordID uint) restoreExecution {
	return restoreExecution{
		EventID:     eventID,
		OperationID: operationID,
		Audit:       db.AsyncAuditRef{RecordID: auditRecordID, OperationID: operationID},
	}
}

// runQueuedRestore runs a restore whose durable operation this node holds. Its
// outcome is recorded on the event before finish releases the operation; a
// failed restore consumes the queue message, only control failures keep it.
func (s *Service) runQueuedRestore(
	ctx context.Context,
	execution restoreExecution,
	spec restoreEventSpec,
	finish func() error,
	run func(context.Context) error,
) (retErr error) {
	var event *clusterModels.BackupEvent
	var runErr error
	outcomeReady := false
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("panic_in_restore: %v", recovered)
			outcomeReady = true
			logger.L.Error().
				Interface("panic", recovered).
				Str("operation_id", execution.OperationID).
				Str("stack", string(debug.Stack())).
				Msg("queued_restore_panicked")
		}
		if !outcomeReady {
			return
		}
		if event == nil || event.ID == 0 {
			retErr = errors.Join(retErr, runErr)
			return
		}
		if err := s.finalizeRestoreEventByID(event.ID, runErr, ""); err != nil {
			retErr = errors.Join(retErr, err)
			return
		}
		if err := finish(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	event, err := s.restoreEventForExecution(execution, spec)
	if err != nil {
		return err
	}
	execution.EventID = event.ID
	if restoreEventTerminal(event.Status) {
		s.finalizeRestoreAuditForEvent(event)
		return finish()
	}
	started, err := s.beginRestoreEvent(event.ID, execution.OperationID)
	if err != nil {
		return err
	}
	if !started {
		return finish()
	}
	stopHeartbeat := s.startBackupEventHeartbeat(ctx, event.ID, time.Minute)
	defer stopHeartbeat()

	outcomeReady = true
	runErr = run(withRestoreExecution(ctx, execution))
	if runErr != nil {
		logger.L.Warn().Err(runErr).Str("operation_id", execution.OperationID).Msg("queued_restore_failed")
	}
	return nil
}

func remoteDatasetForJob(job *clusterModels.BackupJob) string {
	destSuffix := strings.TrimSpace(job.DestSuffix)
	mode := strings.TrimSpace(job.Mode)
	if mode == clusterModels.BackupJobModeVM {
		destSuffix = vmDestSuffixForSource(destSuffix, strings.TrimSpace(job.SourceDataset))
	} else if mode == clusterModels.BackupJobModeJail {
		destSuffix = jailDestSuffixForSource(destSuffix, strings.TrimSpace(job.JailRootDataset))
	} else if mode == clusterModels.BackupJobModeDataset && destSuffix == "" {
		destSuffix = autoDestSuffix(strings.TrimSpace(job.SourceDataset))
	}
	remoteDataset := strings.TrimSpace(job.Target.BackupRoot)
	if destSuffix != "" {
		remoteDataset = remoteDataset + "/" + destSuffix
	}
	return remoteDataset
}

func (s *Service) targetDatasetExists(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
) (bool, error) {
	parsedDataset, err := canonicalTargetDataset(target, dataset)
	if err != nil {
		return false, fmt.Errorf("target_dataset_invalid: %w", err)
	}
	dataset = parsedDataset.String()

	output, err := s.runTargetSSH(ctx, target, "zfs", "list", "-H", "-o", "name", dataset)
	if err != nil {
		combined := strings.ToLower(strings.TrimSpace(output) + " " + err.Error())
		if strings.Contains(combined, "does not exist") || strings.Contains(combined, "dataset does not exist") {
			return false, nil
		}
		return false, err
	}

	for _, line := range strings.Split(output, "\n") {
		parsedLine, parseErr := remoteexec.ParseZFSDataset(strings.TrimSpace(line))
		if parseErr == nil && parsedLine.String() == dataset {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) renameTargetDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	fromDataset, toDataset string,
) error {
	parsedFrom, err := canonicalTargetDataset(target, fromDataset)
	if err != nil {
		return fmt.Errorf("rename_source_dataset_invalid: %w", err)
	}
	parsedTo, err := canonicalTargetDataset(target, toDataset)
	if err != nil {
		return fmt.Errorf("rename_destination_dataset_invalid: %w", err)
	}
	fromDataset = parsedFrom.String()
	toDataset = parsedTo.String()
	if fromDataset == toDataset {
		return nil
	}

	output, err := s.runTargetSSH(ctx, target, "zfs", "rename", fromDataset, toDataset)
	if err != nil {
		return fmt.Errorf("failed_to_rename_target_dataset: %s: %w", strings.TrimSpace(output), err)
	}

	return nil
}

func (s *Service) activateTargetGenerationForRestore(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	activeDataset string,
	selectedDataset string,
) (restoreTargetGenerationActivation, error) {
	activation := restoreTargetGenerationActivation{}
	parsedActive, err := canonicalTargetDataset(target, activeDataset)
	if err != nil {
		return activation, fmt.Errorf("active_target_dataset_invalid: %w", err)
	}
	parsedSelected, err := canonicalTargetDataset(target, selectedDataset)
	if err != nil {
		return activation, fmt.Errorf("selected_target_dataset_invalid: %w", err)
	}
	activeDataset = parsedActive.String()
	selectedDataset = parsedSelected.String()
	activation.ActiveDataset = activeDataset
	activation.SelectedDataset = selectedDataset
	if activeDataset == selectedDataset {
		return activation, nil
	}
	activeBase := datasetLineageBaseSuffixForDataset(target.BackupRoot, activeDataset)
	selectedBase := datasetLineageBaseSuffixForDataset(target.BackupRoot, selectedDataset)
	if activeBase != "" && selectedBase != "" && activeBase != selectedBase {
		return activation, fmt.Errorf("restore_dataset_lineage_mismatch")
	}

	selectedExists, err := s.targetDatasetExists(ctx, target, selectedDataset)
	if err != nil {
		return activation, err
	}
	if !selectedExists {
		return activation, fmt.Errorf("selected_restore_dataset_not_found")
	}

	archivedDataset := ""
	activeExists, err := s.targetDatasetExists(ctx, target, activeDataset)
	if err != nil {
		return activation, err
	}
	activation.ActiveExisted = activeExists
	if activeExists {
		generationToken := compactNowToken()
		archivedDataset = ""
		for attempt := 0; attempt < 16; attempt++ {
			candidate := targetGenerationDatasetCandidate(activeDataset, generationToken, attempt)
			if candidate == selectedDataset {
				continue
			}
			candidateExists, existsErr := s.targetDatasetExists(ctx, target, candidate)
			if existsErr != nil {
				return activation, existsErr
			}
			if candidateExists {
				continue
			}
			archivedDataset = candidate
			break
		}
		if archivedDataset == "" {
			return activation, fmt.Errorf("failed_to_allocate_archive_dataset_name")
		}
		// Record the intended archive topology before issuing the rename. SSH can
		// report an error after ZFS executed the command remotely; in that case
		// the rollback helper must be able to recognize and reverse either state.
		activation.ArchivedDataset = archivedDataset
		activation.Activated = true
		if err := s.renameTargetDataset(ctx, target, activeDataset, archivedDataset); err != nil {
			recoveryCtx, cancel := restoreRecoveryContext()
			defer cancel()
			if rollbackErr := s.rollbackTargetGenerationForRestore(recoveryCtx, target, activation); rollbackErr != nil {
				return activation, fmt.Errorf(
					"activate_restore_generation_archive_failed: %w; remote_rollback_failed: %v",
					err,
					rollbackErr,
				)
			}
			activation.Activated = false
			return activation, err
		}
	}

	// Mark the topology as rollback-capable before the final rename. If SSH
	// reports an ambiguous failure after executing the rename remotely, the
	// topology-aware rollback can recover either the pre- or post-rename state.
	activation.Activated = true
	if err := s.renameTargetDataset(ctx, target, selectedDataset, activeDataset); err != nil {
		recoveryCtx, cancel := restoreRecoveryContext()
		defer cancel()
		if rollbackErr := s.rollbackTargetGenerationForRestore(recoveryCtx, target, activation); rollbackErr != nil {
			return activation, fmt.Errorf(
				"activate_restore_generation_selected_failed: %w; remote_rollback_failed: %v",
				err,
				rollbackErr,
			)
		}
		activation.Activated = false
		return activation, err
	}

	return activation, nil
}

func datasetLineageBaseSuffixForDataset(backupRoot, dataset string) string {
	suffix := relativeDatasetSuffix(backupRoot, dataset)
	_, _, baseSuffix := classifyDatasetLineage(suffix)
	return normalizeDatasetPath(baseSuffix)
}

func parseRestoreSnapshotInput(snapshotInput, defaultRemoteDataset string) (string, string, error) {
	raw := strings.TrimSpace(snapshotInput)
	if raw == "" {
		return "", "", fmt.Errorf("snapshot_required")
	}
	if idx := strings.LastIndex(raw, "@"); idx > 0 {
		parsed, err := remoteexec.ParseZFSSnapshot(raw)
		if err != nil {
			return "", "", fmt.Errorf("snapshot_invalid: %w", err)
		}
		return parsed.Dataset().String(), parsed.Name().WithAt(), nil
	}
	if strings.TrimSpace(defaultRemoteDataset) == "" {
		return "", "", fmt.Errorf("remote_dataset_required")
	}
	defaultDataset, err := remoteexec.ParseZFSDataset(defaultRemoteDataset)
	if err != nil {
		return "", "", fmt.Errorf("remote_dataset_invalid: %w", err)
	}

	snapshot, err := remoteexec.ParseZFSSnapshotName(raw)
	if err != nil {
		return "", "", fmt.Errorf("snapshot_invalid: %w", err)
	}

	if _, err := remoteexec.NewZFSSnapshot(defaultDataset, snapshot); err != nil {
		return "", "", fmt.Errorf("snapshot_invalid: %w", err)
	}

	return defaultDataset.String(), snapshot.WithAt(), nil
}

func canonicalRestoreJobInput(
	job *clusterModels.BackupJob,
	snapshot, remoteDataset string,
) (string, string, string, error) {
	if job == nil || job.ID == 0 {
		return "", "", "", fmt.Errorf("backup_job_required")
	}
	_, root, err := canonicalizeBackupTarget(&job.Target)
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(remoteDataset) == "" {
		remoteDataset = remoteDatasetForJob(job)
	}
	remoteDataset, snapshot, err = parseRestoreSnapshotInput(snapshot, remoteDataset)
	if err != nil {
		return "", "", "", err
	}
	parsedRemote, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil || !parsedRemote.Within(root) {
		return "", "", "", fmt.Errorf("remote_dataset_outside_backup_root")
	}
	destination := job.SourceDataset
	if job.Mode == clusterModels.BackupJobModeJail {
		destination = job.JailRootDataset
	}
	parsedDestination, err := remoteexec.ParseZFSDataset(destination)
	if err != nil {
		return "", "", "", fmt.Errorf("destination_dataset_invalid: %w", err)
	}
	return parsedRemote.String(), snapshot, parsedDestination.String(), nil
}

func snapshotDatasetName(fullSnapshotName string) string {
	fullSnapshotName = strings.TrimSpace(fullSnapshotName)
	if fullSnapshotName == "" {
		return ""
	}
	idx := strings.LastIndex(fullSnapshotName, "@")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(fullSnapshotName[:idx])
}

func filterSnapshotsForRestoreJob(
	job *clusterModels.BackupJob,
	backupRoot string,
	snapshots []SnapshotInfo,
) []SnapshotInfo {
	if job == nil || len(snapshots) == 0 {
		return snapshots
	}

	sourceDataset := strings.TrimSpace(job.JailRootDataset)
	if sourceDataset == "" {
		sourceDataset = strings.TrimSpace(job.SourceDataset)
	}

	expectedSuffix := normalizeDatasetPath(autoDestSuffix(sourceDataset))
	if job.Mode == clusterModels.BackupJobModeVM {
		expectedSuffix = normalizeDatasetPath(sourceDataset)
	}
	kind, expectedGuestID := inferRestoreDatasetKind(expectedSuffix)
	if (kind != clusterModels.BackupJobModeJail && kind != clusterModels.BackupJobModeVM) || expectedGuestID == 0 {
		return snapshots
	}

	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		dataset := snapshotDatasetName(snapshot.Name)
		if dataset == "" {
			dataset = strings.TrimSpace(snapshot.Dataset)
		}
		if dataset == "" || !datasetWithinRoot(backupRoot, dataset) {
			continue
		}

		suffix := relativeDatasetSuffix(backupRoot, dataset)
		_, _, baseSuffix := classifyDatasetLineage(suffix)
		baseKind, baseGuestID := inferRestoreDatasetKind(baseSuffix)
		if baseKind != kind || baseGuestID != expectedGuestID {
			continue
		}

		filtered = append(filtered, snapshot)
	}

	if len(filtered) == 0 {
		return snapshots
	}

	return filtered
}
