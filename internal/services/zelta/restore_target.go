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
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const restoreFromTargetQueueName = "zelta-restore-from-target-run"

type restoreFromTargetPayload struct {
	TargetID           uint   `json:"target_id"`
	RemoteDataset      string `json:"remote_dataset"`
	Snapshot           string `json:"snapshot"`
	DestinationDataset string `json:"destination_dataset"`
	LockID             uint   `json:"lock_id"`
}

type BackupTargetDatasetInfo struct {
	Name          string `json:"name"` // full remote dataset path
	Suffix        string `json:"suffix"`
	SnapshotCount int    `json:"snapshotCount"`
	Kind          string `json:"kind"` // "dataset" | "jail"
	JailCTID      uint   `json:"jailCtId,omitempty"`
}

type BackupJailMetadataInfo struct {
	CTID     uint   `json:"ctId"`
	Name     string `json:"name"`
	BasePool string `json:"basePool"`
}

func (s *Service) ListRemoteTargetDatasets(ctx context.Context, targetID uint) ([]BackupTargetDatasetInfo, error) {
	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return nil, err
	}

	fsOutput, err := s.runTargetZFSList(ctx, &target, "-t", "filesystem", "-r", "-Hp", "-o", "name", target.BackupRoot)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_target_datasets: %w", err)
	}

	snapOutput, err := s.runTargetZFSList(ctx, &target, "-t", "snapshot", "-r", "-Hp", "-o", "name", target.BackupRoot)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_target_snapshots: %w", err)
	}

	snapshotCountByDataset := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(snapOutput), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		idx := strings.Index(name, "@")
		if idx <= 0 {
			continue
		}
		ds := strings.TrimSpace(name[:idx])
		if ds == "" {
			continue
		}
		snapshotCountByDataset[ds]++
	}

	datasets := []BackupTargetDatasetInfo{}
	for _, line := range strings.Split(strings.TrimSpace(fsOutput), "\n") {
		dataset := strings.TrimSpace(line)
		if dataset == "" {
			continue
		}

		snapCount := snapshotCountByDataset[dataset]
		if snapCount < 1 {
			continue
		}

		suffix := relativeDatasetSuffix(target.BackupRoot, dataset)
		kind, jailCTID := inferRestoreDatasetKind(suffix)

		datasets = append(datasets, BackupTargetDatasetInfo{
			Name:          dataset,
			Suffix:        suffix,
			SnapshotCount: snapCount,
			Kind:          kind,
			JailCTID:      jailCTID,
		})
	}

	return datasets, nil
}

func (s *Service) ListRemoteTargetDatasetSnapshots(ctx context.Context, targetID uint, remoteDataset string) ([]SnapshotInfo, error) {
	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return nil, err
	}

	remoteDataset = strings.TrimSpace(remoteDataset)
	if remoteDataset == "" {
		return nil, fmt.Errorf("remote_dataset_required")
	}
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return nil, fmt.Errorf("remote_dataset_outside_backup_root")
	}

	return s.listRemoteSnapshotsForDataset(ctx, &target, remoteDataset)
}

func (s *Service) GetRemoteTargetJailMetadata(ctx context.Context, targetID uint, remoteDataset string) (*BackupJailMetadataInfo, error) {
	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return nil, err
	}

	remoteDataset = strings.TrimSpace(remoteDataset)
	if remoteDataset == "" {
		return nil, fmt.Errorf("remote_dataset_required")
	}
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return nil, fmt.Errorf("remote_dataset_outside_backup_root")
	}

	suffix := relativeDatasetSuffix(target.BackupRoot, remoteDataset)
	kind, fallbackCTID := inferRestoreDatasetKind(suffix)
	if kind != clusterModels.BackupJobModeJail {
		return nil, nil
	}

	info, err := s.readRemoteJailMetadata(ctx, &target, remoteDataset, fallbackCTID)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (s *Service) EnqueueRestoreFromTarget(ctx context.Context, targetID uint, remoteDataset, snapshot, destinationDataset string) error {
	if targetID == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	remoteDataset = strings.TrimSpace(remoteDataset)
	destinationDataset = strings.TrimSpace(destinationDataset)
	if remoteDataset == "" {
		return fmt.Errorf("remote_dataset_required")
	}
	if destinationDataset == "" {
		return fmt.Errorf("destination_dataset_required")
	}

	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return err
	}

	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return err
	}
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}

	lockID := restoreLockIDFromDestination(destinationDataset)
	if !s.acquireJob(lockID) {
		return fmt.Errorf("backup_job_already_running")
	}
	s.releaseJob(lockID)

	return db.EnqueueJSON(ctx, restoreFromTargetQueueName, restoreFromTargetPayload{
		TargetID:           targetID,
		RemoteDataset:      remoteDataset,
		Snapshot:           snapshot,
		DestinationDataset: destinationDataset,
		LockID:             lockID,
	})
}

func (s *Service) registerRestoreFromTargetJob() {
	db.QueueRegisterJSON(restoreFromTargetQueueName, func(ctx context.Context, payload restoreFromTargetPayload) error {
		if payload.TargetID == 0 {
			return fmt.Errorf("invalid_target_id_in_restore_payload")
		}
		if strings.TrimSpace(payload.RemoteDataset) == "" {
			return fmt.Errorf("remote_dataset_required")
		}
		if strings.TrimSpace(payload.DestinationDataset) == "" {
			return fmt.Errorf("destination_dataset_required")
		}

		target, err := s.getRestoreTarget(payload.TargetID)
		if err != nil {
			return err
		}

		return s.runRestoreFromTarget(ctx, &target, payload)
	})
}

func (s *Service) runRestoreFromTarget(ctx context.Context, target *clusterModels.BackupTarget, payload restoreFromTargetPayload) error {
	if payload.LockID == 0 {
		payload.LockID = restoreLockIDFromDestination(payload.DestinationDataset)
	}
	if !s.acquireJob(payload.LockID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(payload.LockID)

	remoteDataset := strings.TrimSpace(payload.RemoteDataset)
	destinationDataset := strings.TrimSpace(payload.DestinationDataset)
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}

	snapshot := strings.TrimSpace(payload.Snapshot)
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}

	remoteEndpoint := target.SSHHost + ":" + remoteDataset + snapshot
	restorePath := destinationDataset + ".restoring"

	event := clusterModels.BackupEvent{
		Mode:           "restore",
		Status:         "running",
		SourceDataset:  remoteEndpoint,
		TargetEndpoint: destinationDataset,
		StartedAt:      time.Now().UTC(),
	}
	s.DB.Create(&event)

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("remote", remoteEndpoint).
		Str("local", destinationDataset).
		Str("snapshot", snapshot).
		Msg("starting_target_dataset_restore")

	var restoreErr error
	var output string

	_, _ = utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", restorePath)

	extraEnv := s.buildZeltaEnv(target)
	extraEnv = append(extraEnv, "ZELTA_RECV_TOP=no")
	output, restoreErr = runZeltaWithEnv(ctx, extraEnv, "backup", "--json", remoteEndpoint, restorePath)
	if restoreErr != nil {
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	verifyOut, verifyErr := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", restorePath)
	if verifyErr != nil {
		restoreErr = fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist: %s", restorePath, verifyOut)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	_, existErr := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", destinationDataset)
	if existErr == nil {
		_, destroyErr := utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", destinationDataset)
		if destroyErr != nil {
			_, _ = utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", restorePath)
			restoreErr = fmt.Errorf("destroy_original_failed: cannot remove %s (is it still in use?): %v", destinationDataset, destroyErr)
			s.finalizeRestoreEvent(&event, restoreErr, output)
			return restoreErr
		}
	}

	if idx := strings.LastIndex(destinationDataset, "/"); idx > 0 {
		parent := destinationDataset[:idx]
		_, _ = utils.RunCommandWithContext(ctx, "zfs", "create", "-p", parent)
	}

	_, renameErr := utils.RunCommandWithContext(ctx, "zfs", "rename", restorePath, destinationDataset)
	if renameErr != nil {
		restoreErr = fmt.Errorf("rename_restore_failed: could not rename %s → %s: %v", restorePath, destinationDataset, renameErr)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	s.fixRestoredProperties(ctx, destinationDataset)

	if err := s.reconcileRestoredJailFromDataset(ctx, destinationDataset); err != nil {
		restoreErr = fmt.Errorf("reconcile_restored_jail_failed: %w", err)
		s.finalizeRestoreEvent(&event, restoreErr, output)
		return restoreErr
	}

	s.finalizeRestoreEvent(&event, nil, output)

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("snapshot", snapshot).
		Str("dataset", destinationDataset).
		Msg("target_dataset_restore_completed")

	return nil
}

func (s *Service) listRemoteSnapshotsForDataset(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	sshArgs := s.buildSSHArgs(target)
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

	return parseSnapshotInfoOutput(output), nil
}

func parseSnapshotInfoOutput(output string) []SnapshotInfo {
	output = strings.TrimSpace(output)
	if output == "" {
		return []SnapshotInfo{}
	}

	snapshots := make([]SnapshotInfo, 0)
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

	return snapshots
}

func (s *Service) readRemoteJailMetadata(ctx context.Context, target *clusterModels.BackupTarget, dataset string, fallbackCTID uint) (*BackupJailMetadataInfo, error) {
	mountedOut, err := s.runTargetSSH(ctx, target, "zfs", "get", "-H", "-o", "value", "mounted", dataset)
	if err != nil {
		return nil, fmt.Errorf("failed_to_read_dataset_mounted_property: %w", err)
	}
	mountpointOut, err := s.runTargetSSH(ctx, target, "zfs", "get", "-H", "-o", "value", "mountpoint", dataset)
	if err != nil {
		return nil, fmt.Errorf("failed_to_read_dataset_mountpoint_property: %w", err)
	}

	mounted := strings.EqualFold(strings.TrimSpace(mountedOut), "yes")
	mountpoint := strings.TrimSpace(mountpointOut)
	if mountpoint == "" || mountpoint == "-" || mountpoint == "none" || mountpoint == "legacy" {
		return nil, nil
	}

	mountedByUs := false
	if !mounted {
		if _, err := s.runTargetSSH(ctx, target, "zfs", "mount", dataset); err != nil {
			return nil, fmt.Errorf("failed_to_mount_remote_dataset_for_metadata: %w", err)
		}
		mountedByUs = true
	}
	if mountedByUs {
		defer func() {
			_, _ = s.runTargetSSH(context.Background(), target, "zfs", "unmount", dataset)
		}()
	}

	metaPath := strings.TrimSuffix(mountpoint, "/") + "/.sylve/jail.json"
	metaRaw, err := s.runTargetSSH(ctx, target, "cat", metaPath)
	if err != nil {
		lower := strings.ToLower(strings.TrimSpace(metaRaw) + " " + err.Error())
		if strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed_to_read_remote_jail_metadata: %w", err)
	}

	var payload struct {
		CTID     uint   `json:"ctId"`
		Name     string `json:"name"`
		Storages []struct {
			Pool   string `json:"pool"`
			IsBase bool   `json:"isBase"`
		} `json:"storages"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(metaRaw)), &payload); err != nil {
		return nil, fmt.Errorf("invalid_remote_jail_metadata_json: %w", err)
	}

	basePool := ""
	for _, storage := range payload.Storages {
		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			continue
		}
		if storage.IsBase {
			basePool = pool
			break
		}
		if basePool == "" {
			basePool = pool
		}
	}

	ctid := payload.CTID
	if ctid == 0 {
		ctid = fallbackCTID
	}

	if ctid == 0 && strings.TrimSpace(payload.Name) == "" && basePool == "" {
		return nil, nil
	}

	return &BackupJailMetadataInfo{
		CTID:     ctid,
		Name:     strings.TrimSpace(payload.Name),
		BasePool: basePool,
	}, nil
}

func (s *Service) getRestoreTarget(targetID uint) (clusterModels.BackupTarget, error) {
	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, targetID).Error; err != nil {
		return clusterModels.BackupTarget{}, err
	}
	if !target.Enabled {
		return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_disabled")
	}
	return target, nil
}

func (s *Service) runTargetZFSList(ctx context.Context, target *clusterModels.BackupTarget, args ...string) (string, error) {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list")
	sshArgs = append(sshArgs, args...)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return output, nil
}

func (s *Service) runTargetSSH(ctx context.Context, target *clusterModels.BackupTarget, args ...string) (string, error) {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost)
	sshArgs = append(sshArgs, args...)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return output, nil
}

func normalizeSnapshotName(snapshot string) (string, error) {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return "", fmt.Errorf("snapshot_required")
	}
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}
	return snapshot, nil
}

func datasetWithinRoot(root, dataset string) bool {
	root = strings.TrimSpace(root)
	dataset = strings.TrimSpace(dataset)
	if root == "" || dataset == "" {
		return false
	}
	if dataset == root {
		return true
	}
	return strings.HasPrefix(dataset, root+"/")
}

func relativeDatasetSuffix(root, dataset string) string {
	root = strings.TrimSpace(root)
	dataset = strings.TrimSpace(dataset)
	if dataset == root {
		return ""
	}
	return strings.TrimPrefix(dataset, root+"/")
}

func inferRestoreDatasetKind(suffix string) (string, uint) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "jails" {
			continue
		}
		raw := strings.TrimSpace(parts[i+1])
		if raw == "" {
			continue
		}
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id > 0 {
			return clusterModels.BackupJobModeJail, uint(id)
		}
	}
	return clusterModels.BackupJobModeDataset, 0
}

func restoreLockIDFromDestination(dataset string) uint {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(dataset)))
	id := uint(h.Sum32())
	if id == 0 {
		return 1
	}
	return id
}
