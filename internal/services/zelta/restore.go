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
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// SnapshotInfo represents a single ZFS snapshot on the backup target.
type SnapshotInfo struct {
	Name      string `json:"name"`      // full dataset@snap name
	ShortName string `json:"shortName"` // just the @snap portion
	Creation  string `json:"creation"`  // creation timestamp
	Used      string `json:"used"`      // space used
	Refer     string `json:"refer"`     // referenced size
}

const restoreJobQueueName = "zelta-restore-run"

// restoreJobPayload is the goqite queue payload for a restore job.
type restoreJobPayload struct {
	JobID    uint   `json:"job_id"`
	Snapshot string `json:"snapshot"` // e.g. "@zelta_2026-02-18_12.00.00"
}

// ListRemoteSnapshots SSHs to the backup target and lists snapshots for a job's destination dataset.
func (s *Service) ListRemoteSnapshots(ctx context.Context, job *clusterModels.BackupJob) ([]SnapshotInfo, error) {
	target := job.Target
	if target.SSHHost == "" {
		return nil, fmt.Errorf("failed_to_list_remote_snapshots: target SSH host is empty (target not loaded?)")
	}

	destSuffix := strings.TrimSpace(job.DestSuffix)
	if destSuffix == "" {
		sourceDataset := job.SourceDataset
		if job.Mode == clusterModels.BackupJobModeJail {
			sourceDataset = job.JailRootDataset
		}
		destSuffix = autoDestSuffix(sourceDataset)
	}

	remoteDataset := target.BackupRoot
	if destSuffix != "" {
		remoteDataset = remoteDataset + "/" + destSuffix
	}

	sshArgs := s.buildSSHArgs(&target)
	sshArgs = append(sshArgs, target.SSHHost,
		"zfs", "list", "-t", "snapshot", "-r", "-Hp",
		"-o", "name,creation,used,refer",
		"-s", "creation",
		remoteDataset,
	)

	logger.L.Debug().
		Str("ssh_host", target.SSHHost).
		Str("ssh_key_path", target.SSHKeyPath).
		Str("remote_dataset", remoteDataset).
		Strs("ssh_args", sshArgs).
		Msg("listing_remote_snapshots")

	var output string
	var err error
	const maxRetries = 2
	for attempt := 1; attempt <= maxRetries; attempt++ {
		output, err = utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
		if err == nil {
			break
		}
		if attempt < maxRetries {
			logger.L.Warn().
				Err(err).
				Int("attempt", attempt).
				Str("output", output).
				Msg("ssh_snapshot_list_failed_retrying")
			time.Sleep(2 * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_remote_snapshots: %s", err)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return []SnapshotInfo{}, nil
	}

	var snapshots []SnapshotInfo
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.SplitN(line, "\t", 4)
		if len(fields) < 4 {
			continue
		}

		fullName := fields[0]
		shortName := ""
		if idx := strings.Index(fullName, "@"); idx >= 0 {
			shortName = fullName[idx:]
		}

		// Convert creation from epoch to readable format
		creation := fields[1]
		if epoch, err := strconv.ParseInt(strings.TrimSpace(creation), 10, 64); err == nil {
			creation = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
		}

		snapshots = append(snapshots, SnapshotInfo{
			Name:      fullName,
			ShortName: shortName,
			Creation:  creation,
			Used:      fields[2],
			Refer:     fields[3],
		})
	}

	return snapshots, nil
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

	// Ensure snapshot starts with @
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}

	// Verify job exists
	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	s.releaseJob(jobID)

	return db.EnqueueJSON(ctx, restoreJobQueueName, restoreJobPayload{
		JobID:    jobID,
		Snapshot: snapshot,
	})
}

// runRestoreJob executes the full restore flow:
//  1. Pull from remote backup@snapshot → temp dataset (.restoring)
//  2. Destroy original dataset (only if it exists; fails if busy)
//  3. Ensure parent dataset exists (for fresh systems)
//  4. Rename temp → original path
//  5. Fix ZFS properties (mountpoint, readonly, canmount) and mount
func (s *Service) runRestoreJob(ctx context.Context, job *clusterModels.BackupJob, snapshot string) error {
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

	destSuffix := strings.TrimSpace(job.DestSuffix)
	if destSuffix == "" {
		destSuffix = autoDestSuffix(sourceDataset)
	}

	// The remote dataset on the backup target
	remoteDataset := job.Target.BackupRoot
	if destSuffix != "" {
		remoteDataset = remoteDataset + "/" + destSuffix
	}

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

		if err := s.runRestoreJob(ctx, &job, payload.Snapshot); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_restore_job_failed")
			return err
		}

		return nil
	})
}
