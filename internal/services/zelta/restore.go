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
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// SnapshotInfo represents a single ZFS snapshot on the backup target.
type SnapshotInfo struct {
	Name      string `json:"name"`                // full dataset@snap name
	ShortName string `json:"shortName"`           // just the @snap portion
	Dataset   string `json:"dataset"`             // dataset portion (without @snap)
	Creation  string `json:"creation"`            // creation timestamp
	Used      string `json:"used"`                // space used
	Refer     string `json:"refer"`               // referenced size
	Lineage   string `json:"lineage,omitempty"`   // "active" | "rotated" | "preserved" | "other"
	OutOfBand bool   `json:"outOfBand,omitempty"` // true when snapshot is outside the active lineage
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
//  2. Destroy original dataset (only if it exists; fails if busy)
//  3. Ensure parent dataset exists (for fresh systems)
//  4. Rename temp → original path
//  5. Fix ZFS properties (mountpoint, readonly, canmount) and mount
func (s *Service) runRestoreJob(ctx context.Context, job *clusterModels.BackupJob, snapshot string, remoteDataset string) error {
	if !s.acquireJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(job.ID)
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

	resolvedRemoteDataset, err := s.resolveRemoteDatasetForSnapshot(ctx, &job.Target, remoteDataset, snapshot)
	if err != nil {
		return fmt.Errorf("resolve_restore_snapshot_dataset_failed: %w", err)
	}
	remoteDataset = resolvedRemoteDataset

	// Build the remote source endpoint with snapshot suffix:
	// e.g. root@192.168.180.1:zroot/sylve-backups/jails/105@zelta_2026-02-18_12.00.00
	remoteEndpoint := job.Target.SSHHost + ":" + remoteDataset + snapshot

	// The temp local dataset for receiving the restore sits next to the original:
	// e.g. zroot/sylve/jails/105 → zroot/sylve/jails/105.restoring
	restorePath := sourceDataset + ".restoring"

	event := clusterModels.BackupEvent{
		JobID:          &job.ID,
		Mode:           "restore",
		Status:         "running",
		SourceDataset:  remoteEndpoint,
		TargetEndpoint: sourceDataset,
		StartedAt:      time.Now().UTC(),
	}
	s.DB.Create(&event)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("remote", remoteEndpoint).
		Str("local", sourceDataset).
		Str("snapshot", snapshot).
		Msg("starting_zelta_restore")

	var restoreErr error
	var output string

	if job.Mode == clusterModels.BackupJobModeJail {
		ctID, err := s.Jail.GetJailCTIDFromDataset(sourceDataset)
		if err == nil {
			logger.L.Info().
				Uint("ct_id", ctID).
				Str("dataset", sourceDataset).
				Msg("stopping_jail_before_restore")

			if err := s.Jail.JailAction(int(ctID), "stop"); err != nil {
				logger.L.Warn().
					Uint("ct_id", ctID).
					Str("dataset", sourceDataset).
					Err(err).
					Msg("failed_to_stop_jail_before_restore_continuing_anyway")
			}
		}
	}

	// Step 1: Clean up any previous restore temp dataset
	_ = s.destroyLocalDatasetWithRetry(ctx, restorePath, true, 5, 500*time.Millisecond)

	// Step 2: Pull from remote backup@snapshot → temp dataset using zelta
	// Override recv flags: skip readonly=on (RECV_TOP default) so the restored
	// dataset is writable. Setting RECV_TOP=no makes zelta treat it as falsy (0),
	// which arr_join() skips. RECV_FS keeps its default flags.
	extraEnv := s.buildZeltaEnv(&job.Target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_RECV_TOP", "no")
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")

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
		"backup",
		"--json",
		remoteEndpoint,
		restorePath,
	)

	logger.L.Info().
		Str("zelta_output", output).
		Err(restoreErr).
		Str("restore_path", restorePath).
		Msg("zelta_backup_pull_finished")

	if restoreErr != nil {
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Verify the dataset was actually created
	restoreExists, verifyErr := s.localDatasetExists(ctx, restorePath)
	if verifyErr != nil || !restoreExists {
		restoreErr = fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist", restorePath)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	logger.L.Debug().Str("verify", restorePath).Msg("restore_dataset_verified")

	// Step 3: Check whether the destination dataset exists.
	_, existErr := s.localDatasetExists(ctx, sourceDataset)
	if existErr != nil {
		restoreErr = fmt.Errorf("failed_to_check_source_dataset_before_restore: %w", existErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 4: Ensure the parent dataset exists (for restoring to a fresh system)
	if idx := strings.LastIndex(sourceDataset, "/"); idx > 0 {
		parent := sourceDataset[:idx]
		_ = s.ensureLocalFilesystemPath(ctx, parent)
	}

	// Step 5: Promote restored temp dataset into place.
	backupDataset, promoteErr := s.promoteRestoredDataset(ctx, restorePath, sourceDataset)
	if promoteErr != nil {
		restoreErr = fmt.Errorf("rename_restore_failed: could not promote %s → %s: %v", restorePath, sourceDataset, promoteErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 6: Fix ZFS properties for the restored dataset
	s.fixRestoredProperties(ctx, sourceDataset)

	// Step 7: If this is a jail dataset, reconcile jail metadata/config from restored jail.json.
	if err := s.reconcileRestoredJailFromDataset(ctx, sourceDataset); err != nil {
		restoreErr = fmt.Errorf("reconcile_restored_jail_failed: %w", err)
		if rollbackErr := s.rollbackPromotedDataset(ctx, sourceDataset, backupDataset); rollbackErr != nil {
			logger.L.Warn().
				Err(rollbackErr).
				Str("dataset", sourceDataset).
				Str("backup_dataset", backupDataset).
				Msg("failed_to_rollback_dataset_after_restore_reconcile_failure")
			restoreErr = fmt.Errorf("%w; rollback_failed: %v", restoreErr, rollbackErr)
		}
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	if strings.TrimSpace(backupDataset) != "" {
		if err := s.cleanupRestoreBackupDataset(ctx, backupDataset); err != nil {
			logger.L.Warn().
				Err(err).
				Str("backup_dataset", backupDataset).
				Msg("failed_to_cleanup_restore_backup_dataset")
		}
	}

	s.finalizeRestoreEvent(&event, nil, output)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("snapshot", snapshot).
		Str("dataset", sourceDataset).
		Msg("zelta_restore_completed")

	return nil
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
func (s *Service) fixRestoredProperties(ctx context.Context, dataset string) {
	ds, err := s.getLocalDataset(ctx, dataset)
	if err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("fix_restored_property_failed")
		return
	}
	if ds == nil {
		logger.L.Warn().Str("dataset", dataset).Msg("fix_restored_property_failed_dataset_not_found")
		return
	}

	if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("fix_restored_property_set_failed")
	}

	if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", "mountpoint", dataset); err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("fix_restored_property_inherit_mountpoint_failed")
	}

	if err := ds.Mount(ctx, false); err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("fix_restored_property_mount_failed")
	}
}

func (s *Service) finalizeRestoreEvent(event *clusterModels.BackupEvent, err error, output string) {
	now := time.Now().UTC()
	event.CompletedAt = &now
	event.Output = output
	if err != nil {
		event.Status = "failed"
		event.Error = err.Error()
	} else {
		event.Status = "success"
	}
	s.DB.Save(event)
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
	if destSuffix == "" {
		sourceDataset := job.SourceDataset
		if job.Mode == clusterModels.BackupJobModeJail {
			sourceDataset = job.JailRootDataset
		}
		destSuffix = normalizeDatasetPath(sourceDataset)
		if job.Mode != clusterModels.BackupJobModeVM {
			destSuffix = autoDestSuffix(sourceDataset)
		}
	}

	remoteDataset := strings.TrimSpace(job.Target.BackupRoot)
	if destSuffix != "" {
		remoteDataset = remoteDataset + "/" + destSuffix
	}
	return remoteDataset
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
