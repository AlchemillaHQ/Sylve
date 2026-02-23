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

	remoteDataset := remoteDatasetForJob(job)
	return s.listRemoteSnapshotsWithLineage(ctx, &target, remoteDataset)
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

	sourceDataset := strings.TrimSpace(job.SourceDataset)
	if job.Mode == clusterModels.BackupJobModeJail {
		sourceDataset = strings.TrimSpace(job.JailRootDataset)
	}
	if sourceDataset == "" {
		return fmt.Errorf("source_dataset_required")
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
	var ctId uint

	if job.Mode == clusterModels.BackupJobModeJail {
		var err error

		ctId, err = s.Jail.GetJailCTIDFromDataset(sourceDataset)
		if err == nil {
			logger.L.Info().
				Uint("ct_id", ctId).
				Str("dataset", sourceDataset).
				Msg("stopping_jail_before_restore")

			if err := s.Jail.JailAction(int(ctId), "stop"); err != nil {
				logger.L.Warn().
					Uint("ct_id", ctId).
					Str("dataset", sourceDataset).
					Err(err).
					Msg("failed_to_stop_jail_before_restore_continuing_anyway")
			}
		}
	}

	// Step 1: Clean up any previous restore temp dataset
	_, _ = utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", restorePath)

	// Step 2: Pull from remote backup@snapshot → temp dataset using zelta
	// Override recv flags: skip readonly=on (RECV_TOP default) so the restored
	// dataset is writable. Setting RECV_TOP=no makes zelta treat it as falsy (0),
	// which arr_join() skips. RECV_FS keeps its default flags.
	extraEnv := s.buildZeltaEnv(&job.Target)
	extraEnv = append(extraEnv,
		"ZELTA_RECV_TOP=no",
	)

	output, restoreErr = runZeltaWithEnv(ctx, extraEnv, "backup", "--json", remoteEndpoint, restorePath)

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
	verifyOut, verifyErr := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", restorePath)
	if verifyErr != nil {
		restoreErr = fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist: %s", restorePath, verifyOut)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}
	logger.L.Debug().Str("verify", strings.TrimSpace(verifyOut)).Msg("restore_dataset_verified")

	// Step 3: Remove the original dataset only if it exists
	_, existErr := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", sourceDataset)
	if existErr == nil {
		// Dataset exists — must be destroyed before we can rename the restore into place
		_, destroyErr := utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", sourceDataset)
		if destroyErr != nil {
			// Likely busy (jail/VM running, dataset mounted). Clean up the temp dataset.
			_, _ = utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", restorePath)
			restoreErr = fmt.Errorf("destroy_original_failed: cannot remove %s (is the jail/VM still running?): %v", sourceDataset, destroyErr)
			s.finalizeRestoreEvent(&event, restoreErr, output)
			return restoreErr
		}
	}

	// Step 4: Ensure the parent dataset exists (for restoring to a fresh system)
	if idx := strings.LastIndex(sourceDataset, "/"); idx > 0 {
		parent := sourceDataset[:idx]
		_, _ = utils.RunCommandWithContext(ctx, "zfs", "create", "-p", parent)
	}

	// Step 5: Rename restored temp → original path
	_, renameErr := utils.RunCommandWithContext(ctx, "zfs", "rename", restorePath, sourceDataset)
	if renameErr != nil {
		restoreErr = fmt.Errorf("rename_restore_failed: could not rename %s → %s: %v", restorePath, sourceDataset, renameErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	// Step 6: Fix ZFS properties for the restored dataset
	s.fixRestoredProperties(ctx, sourceDataset)

	// Step 7: If this is a jail dataset, reconcile jail metadata/config from restored jail.json.
	if err := s.reconcileRestoredJailFromDataset(ctx, sourceDataset); err != nil {
		restoreErr = fmt.Errorf("reconcile_restored_jail_failed: %w", err)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	if job.Mode == clusterModels.BackupJobModeJail && ctId != 0 {
		err := s.Jail.JailAction(int(ctId), "start")
		if err != nil {
			logger.L.Warn().
				Uint("ct_id", ctId).
				Err(err).
				Msg("failed_to_start_jail_after_restore_continuing_anyway")
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

// fixRestoredProperties corrects ZFS properties after a restore so the dataset
// behaves like the original (not readonly, correct mountpoint, canmount=on).
func (s *Service) fixRestoredProperties(ctx context.Context, dataset string) {
	commands := [][]string{
		{"zfs", "set", "readonly=off", dataset},
		{"zfs", "inherit", "mountpoint", dataset},
		{"zfs", "set", "canmount=on", dataset},
		{"zfs", "mount", dataset},
	}

	for _, cmd := range commands {
		if _, err := utils.RunCommandWithContext(ctx, cmd[0], cmd[1:]...); err != nil {
			logger.L.Warn().Err(err).Strs("cmd", cmd).Msg("fix_restored_property_failed")
		}
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
	db.QueueRegisterJSON(restoreJobQueueName, func(ctx context.Context, payload restoreJobPayload) error {
		if payload.JobID == 0 {
			return fmt.Errorf("invalid_job_id_in_restore_payload")
		}

		var job clusterModels.BackupJob
		if err := s.DB.Preload("Target").First(&job, payload.JobID).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_restore_job_not_found")
			return fmt.Errorf("backup_job_not_found: %w", err)
		}

		if err := s.runRestoreJob(ctx, &job, payload.Snapshot, payload.RemoteDataset); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_restore_job_failed")
			return err
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
		destSuffix = autoDestSuffix(sourceDataset)
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
