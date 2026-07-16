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
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"github.com/alchemillahq/sylve/internal/logger"
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
	JobID         uint   `json:"job_id"`
	Snapshot      string `json:"snapshot"` // e.g. "@zelta_2026-02-18_12.00.00"
	RemoteDataset string `json:"remote_dataset,omitempty"`
}

// ListRemoteSnapshots SSHs to the backup target and lists snapshots for a job's destination dataset.
func (s *Service) ListRemoteSnapshots(ctx context.Context, job *clusterModels.BackupJob) ([]SnapshotInfo, error) {
	target := job.Target
	if target.SSHHost == "" {
		return nil, fmt.Errorf("failed_to_list_remote_snapshots: target SSH host is empty (target not loaded?)")
	}
	if err := s.ensureBackupTargetSSHKeyMaterialized(&target); err != nil {
		return nil, fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}

	remoteDataset := remoteDatasetForJob(job)
	snapshots, err := s.listRemoteSnapshotsWithLineage(ctx, &target, remoteDataset)
	if err != nil {
		return nil, err
	}

	filtered := filterSnapshotsForRestoreJob(job, target.BackupRoot, snapshots)
	filtered = filterBackupSnapshots(filtered)
	filtered = filterSnapshotsForBackupJob(filtered, job.ID)
	filtered, err = s.filterRestorableBackupSnapshots(ctx, job, filtered)
	if err != nil {
		return nil, fmt.Errorf("failed_to_validate_backup_restore_points: %w", err)
	}

	childCount := s.remoteDatasetChildCount(ctx, &target, remoteDataset)
	for i := range filtered {
		filtered[i].ChildCount = childCount
	}

	if job.Mode == clusterModels.BackupJobModeVM {
		return collapseSnapshotsByShortName(filtered), nil
	}

	return filtered, nil
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

	defaultRemoteDataset := remoteDatasetForJob(&job)
	remoteDataset, normalizedSnapshot, err := parseRestoreSnapshotInput(snapshot, defaultRemoteDataset)
	if err != nil {
		return err
	}
	if !datasetWithinRoot(job.Target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	s.releaseJob(jobID)

	return db.EnqueueJSON(ctx, restoreJobQueueName, restoreJobPayload{
		JobID:         jobID,
		Snapshot:      normalizedSnapshot,
		RemoteDataset: remoteDataset,
	})
}

// runRestoreJob executes the full restore flow:
//  1. Pull from remote backup@snapshot → temp dataset (.restoring)
//  2. Verify the staged recursive dataset/snapshot manifest
//  3. Archive the original dataset by renaming it (when present)
//  4. Rename temp → original path and activate it
//  5. Remove only the ownership-proven archive with ordinary recursive cleanup
func (s *Service) runRestoreJob(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshot string,
	remoteDataset string,
) (retErr error) {
	if !s.acquireJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(job.ID)
	if err := cluster.ValidateBackupJobSafetyWithDB(ctx, s.DB, job); err != nil {
		return fmt.Errorf("restore_backup_job_safety_check_failed: %w", err)
	}
	if err := s.ensureBackupTargetSSHKeyMaterialized(&job.Target); err != nil {
		return fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}

	sourceDataset := strings.TrimSpace(job.SourceDataset)
	if job.Mode == clusterModels.BackupJobModeJail {
		sourceDataset = strings.TrimSpace(job.JailRootDataset)
	}
	if sourceDataset == "" {
		return fmt.Errorf("source_dataset_required")
	}
	if acquired, holder := s.acquireRestoreDestination(sourceDataset); !acquired {
		return fmt.Errorf(
			"restore_destination_already_running: dataset=%s holder=%s",
			sourceDataset,
			holder,
		)
	}
	defer s.releaseRestoreDestination(sourceDataset)
	restoreWorkloadType, restoreWorkloadID := backupJobGuestIdentity(job)
	if restoreWorkloadType == "" && restoreWorkloadID == 0 {
		restoreWorkloadType = clusterModels.BackupJobModeDataset
		restoreWorkloadID = datasetHash(sourceDataset)
	}
	if acquired, holder := s.acquireWorkloadOperation(
		restoreWorkloadType,
		restoreWorkloadID,
		fmt.Sprintf("restore_job:%d", job.ID),
	); !acquired {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			restoreWorkloadType,
			restoreWorkloadID,
		)
	}
	defer s.releaseWorkloadOperation(restoreWorkloadType, restoreWorkloadID)

	if job.Mode == clusterModels.BackupJobModeDataset {
		if err := s.requireNoManagedGuestsWithinRestore(ctx, sourceDataset); err != nil {
			return err
		}
	}

	if job.Mode == clusterModels.BackupJobModeVM {
		return s.runRestoreVMJob(ctx, job, snapshot, remoteDataset, sourceDataset)
	}

	if strings.TrimSpace(remoteDataset) == "" {
		remoteDataset = remoteDatasetForJob(job)
	}
	remoteDataset = strings.TrimSpace(remoteDataset)
	if !datasetWithinRoot(job.Target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return err
	}

	resolvedRemoteDataset, err := s.resolveRemoteDatasetForSnapshot(ctx, &job.Target, remoteDataset, snapshot)
	if err != nil {
		return fmt.Errorf("resolve_restore_snapshot_dataset_failed: %w", err)
	}
	remoteDataset = resolvedRemoteDataset
	commitMetadata, err := s.requireRemoteBackupRestoreCommit(ctx, job, remoteDataset, snapshot)
	if err != nil {
		return err
	}
	if err := s.verifyRemoteBackupRestoreManifest(ctx, job, remoteDataset, snapshot, commitMetadata); err != nil {
		return err
	}
	restoreRecursive := job.Recursive
	if backupSnapshotRequiresCommit(job.ID, snapshot) {
		restoreRecursive = commitMetadata.Recursive
	}

	expectedRestoreManifest, err := s.restoreManifestRemote(
		ctx,
		&job.Target,
		remoteDataset,
		snapshot,
		restoreRecursive,
	)
	if err != nil {
		return fmt.Errorf("restore_preflight_snapshot_failed: %w", err)
	}

	// Build the remote source endpoint with snapshot suffix:
	// e.g. root@192.168.180.1:zroot/sylve-backups/jails/105@zelta_2026-02-18_12.00.00
	remoteEndpoint := job.Target.SSHHost + ":" + remoteDataset + snapshot

	// The temp local dataset for receiving the restore sits next to the original:
	// e.g. zroot/sylve/jails/105 → zroot/sylve/jails/105.restoring
	restorePath := sourceDataset + ".restoring"
	stagingIdentity := newRestoreStagingIdentity(&job.ID, job.TargetID, sourceDataset)

	// Pre-flight: ensure the destination pool exists before pulling data.
	destinationRoot := sourceDataset
	if idx := strings.Index(destinationRoot, "/"); idx > 0 {
		destinationRoot = destinationRoot[:idx]
	}
	if err := s.ensureLocalPoolExists(ctx, destinationRoot); err != nil {
		return fmt.Errorf("restore_preflight_pool_check_failed: %w", err)
	}
	event := clusterModels.BackupEvent{
		JobID:          &job.ID,
		Mode:           "restore",
		Status:         "running",
		SourceDataset:  job.Name + snapshot,
		TargetEndpoint: sourceDataset,
		StartedAt:      time.Now().UTC(),
	}
	if err := s.DB.Create(&event).Error; err != nil {
		return fmt.Errorf("create_restore_event_failed: %w", err)
	}
	stopHeartbeat := s.startBackupEventHeartbeat(ctx, event.ID, time.Minute)
	defer stopHeartbeat()
	// Verify the replication lease before the restore operation itself becomes
	// the durable guest-wide exclusion lock.
	if guestType, guestID := backupJobGuestIdentity(job); guestType != "" && guestID > 0 && s.Cluster != nil {
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, guestType, guestID, s.localNodeID())
		if leaseErr != nil {
			restoreErr := fmt.Errorf("pre_restore_lease_check_failed: %w", leaseErr)
			s.finalizeRestoreEvent(&event, restoreErr, "")
			return restoreErr
		}
		if !allowed {
			restoreErr := fmt.Errorf("lease_lost_before_restore: ownership transferred to another node")
			s.finalizeRestoreEvent(&event, restoreErr, "")
			return restoreErr
		}
	}
	var jailRestoreFence *restoreGuestFence
	jailSafeToRestart := true
	if job.Mode == clusterModels.BackupJobModeJail {
		jailRestoreFence, err = s.acquireJailRestoreFence(ctx, sourceDataset, event.ID)
		if err != nil {
			restoreErr := fmt.Errorf("acquire_jail_restore_fence_failed: %w", err)
			s.finalizeRestoreEvent(&event, restoreErr, "")
			return restoreErr
		}
		defer func() {
			if jailRestoreFence == nil || retErr == nil {
				return
			}
			if releaseErr := jailRestoreFence.release(); releaseErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("release_jail_restore_fence_failed: %w", releaseErr))
				return
			}
			if jailRestoreFence.wasRunning && jailSafeToRestart {
				if restartErr := s.Jail.JailAction(int(jailRestoreFence.guestID), "start"); restartErr != nil {
					retErr = errors.Join(retErr, fmt.Errorf("restore_jail_restart_failed: %w", restartErr))
				}
			}
		}()
	}
	if err := s.prepareRestoreStagingDataset(ctx, restorePath, stagingIdentity); err != nil {
		restoreErr := fmt.Errorf("restore_preflight_staging_check_failed: %w", err)
		s.finalizeRestoreEvent(&event, restoreErr, "")
		return restoreErr
	}

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("remote", remoteEndpoint).
		Str("local", sourceDataset).
		Str("snapshot", snapshot).
		Msg("starting_zelta_restore")

	var restoreErr error
	var output string

	// Step 1: Pull from remote backup@snapshot → temp dataset using zelta.
	// A pre-existing staging dataset is never inferred to be ours and destroyed;
	// the preflight above requires an operator to inspect it first.
	// Override recv flags: skip readonly=on (RECV_TOP default) so the restored
	// dataset is writable. Setting RECV_TOP=no makes zelta treat it as falsy (0),
	// which arr_join() skips. RECV_FS keeps its default flags.
	extraEnv := s.buildZeltaEnv(&job.Target)
	receiveTopOptions, err := stagingIdentity.receiveTopOptions()
	if err != nil {
		restoreErr = err
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	extraEnv = setEnvValue(extraEnv, "ZELTA_RECV_TOP", receiveTopOptions)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")

	restoreArgs := restoreZeltaArgs(remoteEndpoint, restorePath, restoreRecursive)
	output, restoreErr = runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		func(line string) {
			if err := s.AppendBackupEventOutput(event.ID, line); err != nil {
				logger.L.Warn().
					Uint("event_id", event.ID).
					Err(err).
					Msg("append_restore_event_output_failed")
			}
		},
		restoreArgs...,
	)

	logger.L.Info().
		Str("zelta_output", output).
		Err(restoreErr).
		Str("restore_path", restorePath).
		Msg("zelta_backup_pull_finished")

	if restoreErr != nil {
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Verify the dataset was actually created
	restoreExists, verifyErr := s.localDatasetExists(ctx, restorePath)
	if verifyErr != nil || !restoreExists {
		restoreErr = fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist", restorePath)
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	logger.L.Debug().Str("verify", restorePath).Msg("restore_dataset_verified")
	if err := s.verifyRestoreManifest(
		ctx,
		restorePath,
		snapshot,
		expectedRestoreManifest,
		restoreRecursive,
	); err != nil {
		restoreErr = err
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	// Revalidate after the potentially long receive. Registered guests are
	// rejected regardless of runtime state, so this also catches a guest added
	// while the restore stream was in flight before any local cutover occurs.
	if job.Mode == clusterModels.BackupJobModeDataset {
		if err := s.requireNoManagedGuestsWithinRestore(ctx, sourceDataset); err != nil {
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, err)
			s.finalizeRestoreEvent(&event, restoreErr, output)
			return restoreErr
		}
	}

	// Step 2: Check whether the destination dataset exists.
	destinationExisted, existErr := s.localDatasetExists(ctx, sourceDataset)
	if existErr != nil {
		restoreErr = fmt.Errorf("failed_to_check_source_dataset_before_restore: %w", existErr)
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 3: Ensure the parent dataset exists (for restoring to a fresh system)
	if idx := strings.LastIndex(sourceDataset, "/"); idx > 0 {
		parent := sourceDataset[:idx]
		_ = s.ensureLocalFilesystemPath(ctx, parent)
	}

	// Step 4: Promote the staged dataset into place. A jail was stopped before
	// staging began and remains fenced until this restore is fully finalized.
	backupDataset := ""
	var promoteErr error
	if job.Mode == clusterModels.BackupJobModeJail && destinationExisted {
		jailRuntimeGuard, quiesceErr := s.prepareInPlaceJailRestore(ctx, sourceDataset)
		if quiesceErr != nil {
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(
				restorePath,
				stagingIdentity,
				quiesceErr,
			)
			s.finalizeRestoreEvent(&event, restoreErr, output)
			return restoreErr
		}
		defer func() {
			var restartErr error
			retErr, restartErr = jailRuntimeGuard.restoreAfterFailure(retErr)
			if restartErr == nil {
				return
			}
			jailSafeToRestart = false
			if strings.TrimSpace(output) == "" {
				output = restartErr.Error()
			} else {
				output = strings.TrimRight(output, "\n") + "\n" + restartErr.Error()
			}
			s.finalizeRestoreEvent(&event, retErr, output)
		}()
		backupDataset, promoteErr = s.promoteRestoredDataset(ctx, restorePath, sourceDataset)
		if promoteErr != nil {
			promoteErr = fmt.Errorf(
				"rename_restore_failed: could not promote restored dataset into %s: %w",
				sourceDataset,
				promoteErr,
			)
		}
	} else {
		backupDataset, promoteErr = s.promoteRestoredDataset(ctx, restorePath, sourceDataset)
		if promoteErr != nil {
			promoteErr = fmt.Errorf(
				"rename_restore_failed: could not promote %s → %s: %w",
				restorePath,
				sourceDataset,
				promoteErr,
			)
		}
	}
	if promoteErr != nil {
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, promoteErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	if err := s.clearRestoreStagingProperties(ctx, sourceDataset, stagingIdentity); err != nil {
		restoreErr = s.rollbackRestorePromotionAfterError(
			sourceDataset,
			backupDataset,
			destinationExisted,
			fmt.Errorf("restore_activation_failed: %w", err),
		)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 5: Fix ZFS properties for the restored dataset.
	if err := s.fixRestoredProperties(ctx, sourceDataset); err != nil {
		restoreErr = s.rollbackRestorePromotionAfterError(
			sourceDataset,
			backupDataset,
			destinationExisted,
			fmt.Errorf("restore_activation_failed: %w", err),
		)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 6: If this is a jail dataset, reconcile jail metadata/config from restored jail.json.
	// The ZFS restore succeeded — metadata reconciliation failure does not
	// roll back the data. The error is surfaced in the event output.
	if err := s.reconcileRestoredJailFromDataset(ctx, sourceDataset); err != nil {
		output += "\n" + fmt.Sprintf("jail_metadata_reconcile_failed: %v", err)
		logger.L.Warn().
			Err(err).
			Str("dataset", sourceDataset).
			Msg("restore_jail_metadata_reconcile_failed_data_intact")
	}

	activeRemoteDataset := remoteDatasetForJob(job)
	if _, activationErr := s.activateTargetGenerationsForRestore(
		ctx,
		&job.Target,
		[]restoreTargetGenerationSelection{{
			ActiveDataset:   activeRemoteDataset,
			SelectedDataset: remoteDataset,
		}},
	); activationErr != nil {
		restoreErr = s.rollbackRestorePromotionAfterError(
			sourceDataset,
			backupDataset,
			destinationExisted,
			activationErr,
		)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	activeDestSuffix := relativeDatasetSuffix(job.Target.BackupRoot, activeRemoteDataset)
	if metaErr := s.syncTargetBackupJobMetadata(ctx, job, sourceDataset, activeDestSuffix); metaErr != nil {
		output += "\n" + metaErr.Error()
	}

	// Only remove the ownership-proven rollback archive after activation and
	// metadata reconciliation have completed. Ordinary recursive destruction is
	// intentionally used by cleanupRestoreBackupDataset; dependent clones keep
	// the archive in place and surface a warning instead of being destroyed.
	if strings.TrimSpace(backupDataset) != "" {
		if cleanupErr := s.cleanupRestoreBackupDataset(ctx, backupDataset); cleanupErr != nil {
			warning := fmt.Sprintf(
				"restore_backup_cleanup_pending: dataset=%s retained=true error=%v",
				backupDataset,
				cleanupErr,
			)
			if strings.TrimSpace(output) == "" {
				output = warning
			} else {
				output = strings.TrimRight(output, "\n") + "\n" + warning
			}
			logger.L.Warn().
				Err(cleanupErr).
				Str("backup_dataset", backupDataset).
				Msg("failed_to_cleanup_restore_backup_dataset")
		}
	}
	if scheduleErr := s.advanceBackupJobScheduleAfterRestore(job); scheduleErr != nil {
		warning := fmt.Sprintf("restore_backup_schedule_advance_failed: %v", scheduleErr)
		if strings.TrimSpace(output) == "" {
			output = warning
		} else {
			output = strings.TrimRight(output, "\n") + "\n" + warning
		}
		logger.L.Warn().Err(scheduleErr).Uint("job_id", job.ID).Msg("failed_to_advance_backup_schedule_after_restore")
	}
	if jailRestoreFence != nil {
		if releaseErr := jailRestoreFence.release(); releaseErr != nil {
			restoreErr = fmt.Errorf("release_jail_restore_fence_failed: %w", releaseErr)
			s.finalizeRestoreEvent(&event, restoreErr, output)
			return restoreErr
		}
		jailRestoreFence = nil
	}

	s.finalizeRestoreEvent(&event, nil, output)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("snapshot", snapshot).
		Str("dataset", sourceDataset).
		Msg("zelta_restore_completed")

	return nil
}

type restoreGuestFence struct {
	service    *Service
	guestType  string
	guestID    uint
	token      string
	wasRunning bool
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
	if ownerNodeID == "" {
		ownerNodeID = "local"
	}
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
	var err error
	if s.Cluster != nil && s.Cluster.Raft != nil {
		err = s.applyGuestMigrationInterlock(ctx, "acquire", acquire, transition)
	} else if s.Cluster != nil {
		err = s.Cluster.AcquireReplicationGuestOperation(acquire, true)
	} else {
		err = clusterModels.AcquireReplicationGuestOperationTxn(s.DB, &acquire)
	}
	if err != nil {
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
	if f.service.Cluster != nil && f.service.Cluster.Raft != nil {
		return f.service.applyGuestMigrationInterlock(ctx, "abort", clusterModels.ReplicationGuestOperationAcquire{}, transition)
	}
	if f.service.Cluster != nil {
		return f.service.Cluster.AbortReplicationGuestOperation(transition, true)
	}
	return clusterModels.AbortReplicationGuestOperationTxn(f.service.DB, &transition)
}

// advanceBackupJobScheduleAfterRestore prevents an overdue pre-restore
// schedule from immediately starting another backup when the restore releases
// the job lock.
func (s *Service) advanceBackupJobScheduleAfterRestore(job *clusterModels.BackupJob) error {
	if job == nil || job.ID == 0 {
		return fmt.Errorf("backup_job_required")
	}
	if !job.Enabled {
		return nil
	}

	nextRunAt, err := nextRunTime(job.CronExpr, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("next_backup_run: %w", err)
	}

	update := cluster.BackupJobRuntimeStateUpdate{
		JobID:       job.ID,
		NextRunAt:   &nextRunAt,
		NextRunOnly: true,
	}
	if s.syncBackupJobRuntimeState(update) {
		job.NextRunAt = &nextRunAt
		return nil
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_database_unavailable")
	}
	if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextRunAt).Error; err != nil {
		return fmt.Errorf("update_next_backup_run: %w", err)
	}
	job.NextRunAt = &nextRunAt
	return nil
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

// recursiveRestoreManifestRemote proves that the selected snapshot exists on
// every filesystem and volume currently in the remote tree. Zelta otherwise
// skips an individual descendant with no source snapshot while allowing the
// overall command to succeed. Snapshot GUIDs bind the post-receive check to the
// exact selected restore point, not merely a reused snapshot name.
func (s *Service) recursiveRestoreManifestRemote(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
	snapshot string,
) ([]restoreDatasetManifestEntry, error) {
	return s.restoreManifestRemote(ctx, target, remoteRoot, snapshot, true)
}

// restoreManifestRemote builds the exact dataset/snapshot generation expected
// in staging. Recursive restores cover the full source tree. Nonrecursive
// restores deliberately prove only the selected root; the staging verifier
// still inspects the full received tree and rejects any unexpected descendant.
func (s *Service) restoreManifestRemote(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
	snapshot string,
	recursive bool,
) ([]restoreDatasetManifestEntry, error) {
	remoteRoot = normalizeDatasetPath(remoteRoot)
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return nil, err
	}

	datasetArgs := []string{"zfs", "list", "-H", "-p"}
	if recursive {
		datasetArgs = append(datasetArgs, "-r")
	}
	datasetArgs = append(datasetArgs, "-t", "filesystem,volume", "-o", "name,type", remoteRoot)
	datasetOutput, err := s.runTargetSSH(ctx, target, datasetArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_restore_dataset_tree_failed: %w", err)
	}
	manifest, err := parseRestoreDatasetTree(datasetOutput, remoteRoot)
	if err != nil {
		return nil, err
	}

	snapshotArgs := []string{"zfs", "list", "-H", "-p"}
	if recursive {
		snapshotArgs = append(snapshotArgs, "-r")
		snapshotArgs = append(snapshotArgs, "-t", "snapshot", "-o", "name,guid", remoteRoot)
	} else {
		snapshotArgs = append(
			snapshotArgs,
			"-t", "snapshot", "-o", "name,guid", remoteRoot+snapshot,
		)
	}
	snapshotOutput, err := s.runTargetSSH(ctx, target, snapshotArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_restore_snapshot_tree_failed: %w", err)
	}
	guids, err := parseReplicationSnapshotTreeGUIDs(
		snapshotOutput,
		remoteRoot,
		strings.TrimPrefix(snapshot, "@"),
	)
	if err != nil {
		return nil, fmt.Errorf("parse_restore_snapshot_tree_failed: %w", err)
	}

	missing := make([]string, 0)
	for idx := range manifest {
		dataset := datasetForRestoreManifestSuffix(remoteRoot, manifest[idx].Suffix)
		guid := strings.TrimSpace(guids[dataset])
		if guid == "" {
			missing = append(missing, dataset)
			continue
		}
		manifest[idx].SnapshotGUID = guid
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		mode := "nonrecursive"
		if recursive {
			mode = "recursive"
		}
		return nil, fmt.Errorf(
			"%s_restore_snapshot_incomplete: snapshot=%s missing_datasets=%s",
			mode,
			snapshot,
			strings.Join(missing, ","),
		)
	}

	return manifest, nil
}

func (s *Service) verifyRecursiveRestoreManifest(
	ctx context.Context,
	restoreRoot string,
	snapshot string,
	expected []restoreDatasetManifestEntry,
) error {
	return s.verifyRestoreManifest(ctx, restoreRoot, snapshot, expected, true)
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

func (s *Service) runRestoreVMJob(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshot string,
	remoteDataset string,
	sourceDataset string,
) error {
	if strings.TrimSpace(remoteDataset) == "" {
		remoteDataset = remoteDatasetForJob(job)
	}
	remoteDataset = strings.TrimSpace(remoteDataset)
	if !datasetWithinRoot(job.Target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return err
	}

	restoreNetwork := true
	payload := restoreFromTargetPayload{
		TargetID:           job.TargetID,
		RemoteDataset:      remoteDataset,
		Snapshot:           strings.TrimSpace(snapshot),
		DestinationDataset: normalizeRestoreDestinationDataset(sourceDataset),
		RestoreNetwork:     &restoreNetwork,
	}

	jobID := job.ID
	return s.runRestoreFromTargetVM(ctx, &job.Target, payload, &jobID)
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

	allDatasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return fmt.Errorf("list_restored_datasets_failed: %w", err)
	}
	var restoreErrors []error

	seen := map[string]struct{}{dataset: {}}
	subtree := []string{dataset}
	prefix := dataset + "/"

	for _, candidate := range allDatasets {
		ds := normalizeDatasetPath(candidate)
		if ds == "" || ds == dataset {
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

	for idx, dsName := range subtree {
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

		if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
			logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_set_failed")
			restoreErrors = append(restoreErrors, fmt.Errorf("set_restored_properties_for_%s_failed: %w", dsName, err))
			continue
		}

		if idx == 0 {
			if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", "mountpoint", dsName); err != nil {
				logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_inherit_mountpoint_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("inherit_restored_mountpoint_for_%s_failed: %w", dsName, err))
				continue
			}
		}

		if err := ds.Mount(ctx, false); err != nil {
			lowerErr := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowerErr, "already mounted") {
				continue
			}
			logger.L.Warn().Err(err).Str("dataset", dsName).Msg("fix_restored_property_mount_failed")
			restoreErrors = append(restoreErrors, fmt.Errorf("mount_restored_dataset_%s_failed: %w", dsName, err))
		}
	}

	// Fix volumes separately: no canmount, no mountpoint, no mount.
	volDatasets, volErr := s.listLocalVolumeDatasets(ctx)
	if volErr == nil {
		prefix := dataset + "/"
		for _, v := range volDatasets {
			vn := normalizeDatasetPath(v)
			if vn == "" || !strings.HasPrefix(vn, prefix) {
				continue
			}
			vol, getErr := s.getLocalDataset(ctx, vn)
			if getErr != nil {
				logger.L.Warn().Err(getErr).Str("dataset", vn).Msg("fix_restored_volume_get_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("get_restored_volume_%s_failed: %w", vn, getErr))
				continue
			}
			if vol == nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restored_volume_not_found: %s", vn))
				continue
			}
			if vol.IsEncrypted() {
				keyLoaded, keyErr := s.ensureEncryptionKeyForDataset(ctx, vol)
				if keyErr != nil {
					logger.L.Error().Err(keyErr).Str("dataset", vn).Msg("fix_restored_volume_key_load_failed")
					restoreErrors = append(restoreErrors, fmt.Errorf("restore_encryption_key_load_failed_for_%s: %w", vn, keyErr))
					continue
				}
				if !keyLoaded {
					logger.L.Error().Str("dataset", vn).Msg("fix_restored_volume_key_not_auto_loaded")
					restoreErrors = append(restoreErrors, fmt.Errorf("restore_encryption_key_required_for_%s", vn))
					continue
				}
			}
			if err := vol.SetProperties(ctx, "readonly", "off"); err != nil {
				logger.L.Warn().Err(err).Str("dataset", vn).Msg("fix_restored_volume_readonly_failed")
				restoreErrors = append(restoreErrors, fmt.Errorf("set_restored_volume_%s_readonly_failed: %w", vn, err))
			}
		}
	} else {
		logger.L.Warn().Err(volErr).Msg("fix_restored_property_list_volumes_failed")
		restoreErrors = append(restoreErrors, fmt.Errorf("list_restored_volumes_failed: %w", volErr))
	}

	return errors.Join(restoreErrors...)
}

func (s *Service) finalizeRestoreEvent(event *clusterModels.BackupEvent, err error, output string) {
	if event == nil || event.ID == 0 {
		return
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	event.Output = output
	if err != nil {
		event.Status = "failed"
		event.Error = err.Error()
	} else {
		event.Status = "success"
		event.Error = ""
	}
	if saveErr := s.DB.Save(event).Error; saveErr != nil {
		logger.L.Warn().Err(saveErr).Uint("event_id", event.ID).Msg("failed_to_finalize_restore_event")
	}

	if event.JobID != nil && s.TelemetryDB != nil {
		auditStatus := "success"
		errMsg := ""
		if err != nil {
			auditStatus = "failed"
			errMsg = err.Error()
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_restore", *event.JobID, auditStatus, errMsg, map[string]any{
			"eventId": event.ID,
			"status":  auditStatus,
			"error":   errMsg,
		})
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("restore_event_finalized_%d", event.ID))
}

// registerRestoreJob registers the restore queue handler with goqite.
func (s *Service) registerRestoreJob() {
	db.QueueRegisterJSON(restoreJobQueueName, func(ctx context.Context, payload restoreJobPayload) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.L.Error().
					Interface("panic", recovered).
					Uint("job_id", payload.JobID).
					Str("stack", string(debug.Stack())).
					Msg("queued_restore_job_panicked")

				// Do not return an error: restore jobs should not retry on failure.
				err = nil
			}
		}()

		if payload.JobID == 0 {
			logger.L.Warn().Msg("queued_restore_job_invalid_payload_job_id")
			return nil
		}

		var job clusterModels.BackupJob
		if err := s.DB.Preload("Target").First(&job, payload.JobID).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_restore_job_not_found")
			return nil
		}

		if err := s.runRestoreJob(ctx, &job, payload.Snapshot, payload.RemoteDataset); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_restore_job_failed")
			return nil
		}

		return nil
	})
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
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return false, fmt.Errorf("dataset_required")
	}

	output, err := s.runTargetSSH(ctx, target, "zfs", "list", "-H", "-o", "name", dataset)
	if err != nil {
		combined := strings.ToLower(strings.TrimSpace(output) + " " + err.Error())
		if strings.Contains(combined, "does not exist") || strings.Contains(combined, "dataset does not exist") {
			return false, nil
		}
		return false, err
	}

	for _, line := range strings.Split(output, "\n") {
		if normalizeDatasetPath(strings.TrimSpace(line)) == dataset {
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
	fromDataset = normalizeDatasetPath(fromDataset)
	toDataset = normalizeDatasetPath(toDataset)
	if fromDataset == "" || toDataset == "" {
		return fmt.Errorf("dataset_required")
	}
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
	activation := restoreTargetGenerationActivation{
		ActiveDataset:   normalizeDatasetPath(activeDataset),
		SelectedDataset: normalizeDatasetPath(selectedDataset),
	}
	activeDataset = normalizeDatasetPath(activeDataset)
	selectedDataset = normalizeDatasetPath(selectedDataset)
	if target == nil {
		return activation, fmt.Errorf("target_required")
	}
	if activeDataset == "" || selectedDataset == "" {
		return activation, fmt.Errorf("target_dataset_required")
	}
	if activeDataset == selectedDataset {
		return activation, nil
	}
	if !datasetWithinRoot(target.BackupRoot, activeDataset) || !datasetWithinRoot(target.BackupRoot, selectedDataset) {
		return activation, fmt.Errorf("remote_dataset_outside_backup_root")
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
		remoteDataset := strings.TrimSpace(raw[:idx])
		snapshot := strings.TrimSpace(raw[idx:])
		if remoteDataset == "" {
			return "", "", fmt.Errorf("remote_dataset_required")
		}
		if snapshot == "@" {
			return "", "", fmt.Errorf("snapshot_required")
		}
		return remoteDataset, snapshot, nil
	}

	snapshot := raw
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}

	defaultRemoteDataset = strings.TrimSpace(defaultRemoteDataset)
	if defaultRemoteDataset == "" {
		return "", "", fmt.Errorf("remote_dataset_required")
	}

	return defaultRemoteDataset, snapshot, nil
}

func (s *Service) resolveRemoteDatasetForSnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	preferredDataset string,
	snapshot string,
) (string, error) {
	preferredDataset = strings.TrimSpace(preferredDataset)
	snapshot = strings.TrimSpace(snapshot)
	if preferredDataset == "" {
		return "", fmt.Errorf("remote_dataset_required")
	}
	if snapshot == "" || snapshot == "@" {
		return "", fmt.Errorf("snapshot_required")
	}
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}

	if !datasetWithinRoot(target.BackupRoot, preferredDataset) {
		return "", fmt.Errorf("remote_dataset_outside_backup_root")
	}

	lineageSnapshots, err := s.listRemoteSnapshotsWithLineage(ctx, target, preferredDataset)
	if err != nil {
		return "", err
	}
	if len(lineageSnapshots) == 0 {
		return "", fmt.Errorf("snapshot_not_found_on_target")
	}

	preferredFullName := preferredDataset + snapshot
	for _, info := range lineageSnapshots {
		if strings.TrimSpace(info.Name) == preferredFullName {
			return preferredDataset, nil
		}
	}

	resolvedDataset := ""
	for _, info := range lineageSnapshots {
		if strings.TrimSpace(info.ShortName) != snapshot {
			continue
		}
		dataset := snapshotDatasetName(info.Name)
		if dataset == "" {
			continue
		}
		// listRemoteSnapshotsWithLineage returns oldest→newest; last match wins.
		resolvedDataset = dataset
	}

	if resolvedDataset == "" {
		return "", fmt.Errorf("snapshot_not_found_on_target")
	}
	if !datasetWithinRoot(target.BackupRoot, resolvedDataset) {
		return "", fmt.Errorf("remote_dataset_outside_backup_root")
	}

	return resolvedDataset, nil
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
