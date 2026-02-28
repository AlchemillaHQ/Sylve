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
	"runtime/debug"
	"sort"
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
	RestoreNetwork     *bool  `json:"restore_network,omitempty"`
}

type BackupTargetDatasetInfo struct {
	Name          string `json:"name"` // full remote dataset path
	Suffix        string `json:"suffix"`
	BaseSuffix    string `json:"baseSuffix"`
	Lineage       string `json:"lineage"` // "active" | "rotated" | "preserved" | "other"
	OutOfBand     bool   `json:"outOfBand"`
	SnapshotCount int    `json:"snapshotCount"`
	Kind          string `json:"kind"` // "dataset" | "jail" | "vm"
	JailCTID      uint   `json:"jailCtId,omitempty"`
	VMRID         uint   `json:"vmRid,omitempty"`
}

type BackupJailMetadataInfo struct {
	CTID     uint   `json:"ctId"`
	Name     string `json:"name"`
	BasePool string `json:"basePool"`
}

type BackupVMMetadataInfo struct {
	RID   uint     `json:"rid"`
	Name  string   `json:"name"`
	Pools []string `json:"pools"`
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
		if !isBackupSnapshotShortName(name[idx+1:]) {
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
		lineage, outOfBand, baseSuffix := classifyDatasetLineage(suffix)
		kind, guestID := inferRestoreDatasetKind(baseSuffix)
		if kind != clusterModels.BackupJobModeJail && kind != clusterModels.BackupJobModeVM {
			kind, guestID = inferRestoreDatasetKind(suffix)
		}
		if kind == clusterModels.BackupJobModeJail && guestID > 0 {
			baseSuffix = fmt.Sprintf("jails/%d", guestID)
		}
		if kind == clusterModels.BackupJobModeVM && guestID > 0 {
			baseSuffix = fmt.Sprintf("virtual-machines/%d", guestID)
		}

		jailCTID := uint(0)
		vmRID := uint(0)
		if kind == clusterModels.BackupJobModeJail {
			jailCTID = guestID
		} else if kind == clusterModels.BackupJobModeVM {
			vmRID = guestID
		}

		datasets = append(datasets, BackupTargetDatasetInfo{
			Name:          dataset,
			Suffix:        suffix,
			BaseSuffix:    baseSuffix,
			Lineage:       lineage,
			OutOfBand:     outOfBand,
			SnapshotCount: snapCount,
			Kind:          kind,
			JailCTID:      jailCTID,
			VMRID:         vmRID,
		})
	}

	sort.Slice(datasets, func(i, j int) bool {
		left := datasets[i]
		right := datasets[j]

		if left.BaseSuffix != right.BaseSuffix {
			return left.BaseSuffix < right.BaseSuffix
		}

		leftRank := datasetLineageRank(left.Lineage)
		rightRank := datasetLineageRank(right.Lineage)
		if leftRank != rightRank {
			return leftRank < rightRank
		}

		if left.SnapshotCount != right.SnapshotCount {
			return left.SnapshotCount > right.SnapshotCount
		}

		return left.Suffix < right.Suffix
	})

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

	snapshots, err := s.listRemoteSnapshotsWithLineage(ctx, &target, remoteDataset)
	if err != nil {
		return nil, err
	}
	snapshots = filterBackupSnapshots(snapshots)

	kind, _ := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset))
	if kind == clusterModels.BackupJobModeVM {
		snapshots = collapseSnapshotsByShortName(snapshots)
	}

	return snapshots, nil
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

func (s *Service) GetRemoteTargetVMMetadata(ctx context.Context, targetID uint, remoteDataset string) (*BackupVMMetadataInfo, error) {
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
	kind, fallbackRID := inferRestoreDatasetKind(suffix)
	if kind != clusterModels.BackupJobModeVM {
		return nil, nil
	}

	info, err := s.readRemoteVMMetadata(ctx, &target, remoteDataset, fallbackRID)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (s *Service) EnqueueRestoreFromTarget(
	ctx context.Context,
	targetID uint,
	remoteDataset, snapshot, destinationDataset string,
	restoreNetwork bool,
) error {
	if targetID == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	remoteDataset = strings.TrimSpace(remoteDataset)
	if remoteDataset == "" {
		return fmt.Errorf("remote_dataset_required")
	}

	remoteDataset, snapshot, err := parseRestoreSnapshotInput(snapshot, remoteDataset)
	if err != nil {
		return err
	}
	destinationDataset = normalizeRestoreDestinationDataset(destinationDataset)
	if destinationDataset == "" {
		return fmt.Errorf("destination_dataset_required")
	}
	if !isValidRestoreDestinationDataset(destinationDataset) {
		return fmt.Errorf("destination_dataset_invalid: expected fully qualified dataset like 'pool/path'")
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
		RestoreNetwork:     &restoreNetwork,
	})
}

func (s *Service) registerRestoreFromTargetJob() {
	db.QueueRegisterJSON(restoreFromTargetQueueName, func(ctx context.Context, payload restoreFromTargetPayload) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.L.Error().
					Interface("panic", recovered).
					Uint("target_id", payload.TargetID).
					Str("remote_dataset", strings.TrimSpace(payload.RemoteDataset)).
					Str("destination_dataset", strings.TrimSpace(payload.DestinationDataset)).
					Str("stack", string(debug.Stack())).
					Msg("queued_restore_from_target_job_panicked")

				// Do not return an error: restore-from-target jobs should not retry on failure.
				err = nil
			}
		}()

		if payload.TargetID == 0 {
			logger.L.Warn().
				Msg("queued_restore_from_target_job_invalid_payload_target_id")
			return nil
		}
		if strings.TrimSpace(payload.RemoteDataset) == "" {
			logger.L.Warn().
				Uint("target_id", payload.TargetID).
				Msg("queued_restore_from_target_job_invalid_payload_remote_dataset")
			return nil
		}
		if strings.TrimSpace(payload.DestinationDataset) == "" {
			logger.L.Warn().
				Uint("target_id", payload.TargetID).
				Msg("queued_restore_from_target_job_invalid_payload_destination_dataset")
			return nil
		}

		target, err := s.getRestoreTarget(payload.TargetID)
		if err != nil {
			logger.L.Warn().
				Err(err).
				Uint("target_id", payload.TargetID).
				Msg("queued_restore_from_target_job_target_lookup_failed")
			return nil
		}

		if err := s.runRestoreFromTarget(ctx, &target, payload); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("target_id", payload.TargetID).
				Str("remote_dataset", strings.TrimSpace(payload.RemoteDataset)).
				Str("destination_dataset", strings.TrimSpace(payload.DestinationDataset)).
				Msg("queued_restore_from_target_job_failed")
			return nil
		}

		return nil
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
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}

	restoreSuffix := relativeDatasetSuffix(target.BackupRoot, remoteDataset)
	kind, vmRID := inferRestoreDatasetKind(restoreSuffix)
	if kind == clusterModels.BackupJobModeVM && vmRID > 0 {
		return s.runRestoreFromTargetVM(ctx, target, payload, nil)
	}

	_, err := s.runRestoreFromTargetSingleDataset(ctx, target, payload, nil, true, false, nil)
	return err
}

func (s *Service) runRestoreFromTargetVM(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	payload restoreFromTargetPayload,
	jobID *uint,
) (retErr error) {
	if s.VM == nil || !s.VM.IsVirtualizationEnabled() {
		return fmt.Errorf("virtualization_disabled")
	}

	remoteDataset := strings.TrimSpace(payload.RemoteDataset)
	destinationDataset := normalizeRestoreDestinationDataset(payload.DestinationDataset)
	if destinationDataset == "" {
		return fmt.Errorf("destination_dataset_required")
	}
	if !isValidRestoreDestinationDataset(destinationDataset) {
		return fmt.Errorf("destination_dataset_invalid: expected fully qualified dataset like 'pool/path'")
	}
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}

	snapshot := strings.TrimSpace(payload.Snapshot)
	if snapshot != "" && !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}
	remoteEndpoint := strings.TrimSpace(remoteDataset)
	if host := strings.TrimSpace(target.SSHHost); host != "" {
		remoteEndpoint = host + ":" + remoteEndpoint
	}
	if snapshot != "" {
		remoteEndpoint = remoteEndpoint + snapshot
	}

	event := clusterModels.BackupEvent{
		JobID:          jobID,
		Mode:           "restore",
		Status:         "running",
		SourceDataset:  remoteEndpoint,
		TargetEndpoint: destinationDataset,
		StartedAt:      time.Now().UTC(),
	}
	s.DB.Create(&event)
	defer func() {
		output := ""
		if current, err := s.GetLocalBackupEvent(event.ID); err == nil && current != nil {
			output = current.Output
		}
		s.finalizeRestoreEvent(&event, retErr, output)
	}()

	restoreNetwork := true
	if payload.RestoreNetwork != nil {
		restoreNetwork = *payload.RestoreNetwork
	}

	_, remoteRID := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset))
	if remoteRID == 0 {
		return fmt.Errorf("vm_rid_missing")
	}

	destKind, destRID := inferRestoreDatasetKind(destinationDataset)
	if destKind != clusterModels.BackupJobModeVM || destRID == 0 {
		destRID = remoteRID
	}

	vmRoots, err := s.listRemoteVMRepresentativeRoots(ctx, target, remoteRID, remoteDataset)
	if err != nil {
		return fmt.Errorf("list_remote_vm_roots_failed: %w", err)
	}
	if len(vmRoots) == 0 {
		return fmt.Errorf("remote_vm_roots_not_found")
	}

	existingVM, err := s.findVMByRID(destRID)
	if err != nil {
		return fmt.Errorf("failed_to_lookup_existing_vm_before_restore: %w", err)
	}
	if existingVM != nil {
		if err := s.stopVMIfPresent(destRID); err != nil {
			return fmt.Errorf("failed_to_stop_vm_before_restore: %w", err)
		}
	}

	primaryDestination := destinationDataset
	if kind, _ := inferRestoreDatasetKind(primaryDestination); kind != clusterModels.BackupJobModeVM {
		primaryDestination = destinationVMRootFromRemoteRoot(target.BackupRoot, vmRoots[0], destRID)
	}
	if primaryDestination == "" {
		return fmt.Errorf("destination_dataset_required")
	}

	candidateDestinations := make([]string, 0, len(vmRoots)+1)
	candidateDestinations = append(candidateDestinations, primaryDestination)
	seenDestinations := make(map[string]struct{})
	for _, remoteRoot := range vmRoots {
		localRoot := destinationVMRootFromRemoteRoot(target.BackupRoot, remoteRoot, destRID)
		if localRoot == "" {
			continue
		}
		if _, ok := seenDestinations[localRoot]; ok {
			continue
		}
		seenDestinations[localRoot] = struct{}{}
		candidateDestinations = append(candidateDestinations, localRoot)
	}

	if err := s.validateDestinationPoolsExist(ctx, candidateDestinations); err != nil {
		return err
	}

	type restoredDatasetBackup struct {
		destination string
		backup      string
	}
	appliedBackups := make([]restoredDatasetBackup, 0, len(vmRoots))
	rollbackAppliedBackups := func() error {
		var lastErr error
		for idx := len(appliedBackups) - 1; idx >= 0; idx-- {
			entry := appliedBackups[idx]
			if strings.TrimSpace(entry.backup) == "" {
				continue
			}
			if err := s.rollbackPromotedDataset(ctx, entry.destination, entry.backup); err != nil {
				logger.L.Warn().
					Err(err).
					Str("destination_dataset", entry.destination).
					Str("backup_dataset", entry.backup).
					Msg("failed_to_rollback_vm_dataset_after_restore_failure")
				lastErr = err
			}
		}
		return lastErr
	}

	for _, remoteRoot := range vmRoots {
		localRoot := destinationVMRootFromRemoteRoot(target.BackupRoot, remoteRoot, destRID)
		if localRoot == "" {
			continue
		}
		if _, ok := seenDestinations[localRoot]; !ok {
			continue
		}
		delete(seenDestinations, localRoot)

		runPayload := payload
		runPayload.RemoteDataset = remoteRoot
		runPayload.DestinationDataset = localRoot
		disableNetworkRestore := false
		runPayload.RestoreNetwork = &disableNetworkRestore

		backupDataset, err := s.runRestoreFromTargetSingleDataset(
			ctx,
			target,
			runPayload,
			jobID,
			false,
			true,
			&event.ID,
		)
		if err != nil {
			rollbackErr := rollbackAppliedBackups()
			if rollbackErr != nil {
				return fmt.Errorf("vm_multi_root_restore_failed: %w; rollback_failed: %v", err, rollbackErr)
			}
			return err
		}

		appliedBackups = append(appliedBackups, restoredDatasetBackup{
			destination: localRoot,
			backup:      backupDataset,
		})
	}

	if err := s.reconcileRestoredVMFromDatasetWithOptions(ctx, primaryDestination, restoreNetwork); err != nil {
		rollbackErr := rollbackAppliedBackups()
		if rollbackErr != nil {
			return fmt.Errorf("reconcile_restored_vm_failed: %w; rollback_failed: %v", err, rollbackErr)
		}
		return fmt.Errorf("reconcile_restored_vm_failed: %w", err)
	}

	for _, entry := range appliedBackups {
		if strings.TrimSpace(entry.backup) == "" {
			continue
		}
		if err := s.cleanupRestoreBackupDataset(ctx, entry.backup); err != nil {
			logger.L.Warn().
				Err(err).
				Str("backup_dataset", entry.backup).
				Msg("failed_to_cleanup_vm_restore_backup_dataset")
		}
	}

	return nil
}

func (s *Service) validateDestinationPoolsExist(ctx context.Context, datasets []string) error {
	seenRoots := make(map[string]struct{})

	for _, dataset := range datasets {
		dataset = normalizeRestoreDestinationDataset(dataset)
		if dataset == "" {
			return fmt.Errorf("destination_dataset_required")
		}

		root := dataset
		if idx := strings.Index(root, "/"); idx > 0 {
			root = root[:idx]
		}
		root = strings.TrimSpace(root)
		if root == "" {
			return fmt.Errorf("destination_dataset_required")
		}
		if _, ok := seenRoots[root]; ok {
			continue
		}

		if err := s.ensureLocalPoolExists(ctx, root); err != nil {
			return err
		}

		seenRoots[root] = struct{}{}
	}

	return nil
}

func (s *Service) runRestoreFromTargetSingleDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	payload restoreFromTargetPayload,
	jobID *uint,
	reconcileJail bool,
	keepBackup bool,
	sharedEventID *uint,
) (string, error) {
	remoteDataset := strings.TrimSpace(payload.RemoteDataset)
	destinationDataset := normalizeRestoreDestinationDataset(payload.DestinationDataset)
	if destinationDataset == "" {
		return "", fmt.Errorf("destination_dataset_required")
	}
	if !isValidRestoreDestinationDataset(destinationDataset) {
		return "", fmt.Errorf("destination_dataset_invalid: expected fully qualified dataset like 'pool/path'")
	}
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return "", fmt.Errorf("remote_dataset_outside_backup_root")
	}
	destinationRoot := destinationDataset
	if idx := strings.Index(destinationRoot, "/"); idx > 0 {
		destinationRoot = destinationRoot[:idx]
	}
	if strings.TrimSpace(destinationRoot) == "" {
		return "", fmt.Errorf("destination_dataset_required")
	}
	if err := s.ensureLocalPoolExists(ctx, destinationRoot); err != nil {
		return "", err
	}

	snapshot := strings.TrimSpace(payload.Snapshot)
	if !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}

	resolvedRemoteDataset, err := s.resolveRemoteDatasetForSnapshot(ctx, target, remoteDataset, snapshot)
	if err != nil {
		return "", fmt.Errorf("resolve_restore_snapshot_dataset_failed: %w", err)
	}
	remoteDataset = resolvedRemoteDataset

	remoteEndpoint := target.SSHHost + ":" + remoteDataset + snapshot
	restorePath := destinationDataset + ".restoring"

	activeEventID := uint(0)
	ownsEvent := false
	event := clusterModels.BackupEvent{}
	if sharedEventID != nil && *sharedEventID > 0 {
		activeEventID = *sharedEventID
	} else {
		event = clusterModels.BackupEvent{
			JobID:          jobID,
			Mode:           "restore",
			Status:         "running",
			SourceDataset:  remoteEndpoint,
			TargetEndpoint: destinationDataset,
			StartedAt:      time.Now().UTC(),
		}
		s.DB.Create(&event)
		activeEventID = event.ID
		ownsEvent = true
	}

	appendEventOutput := func(chunk string) {
		if activeEventID == 0 {
			return
		}
		if err := s.AppendBackupEventOutput(activeEventID, chunk); err != nil {
			logger.L.Warn().
				Uint("event_id", activeEventID).
				Err(err).
				Msg("append_restore_event_output_failed")
		}
	}

	if !ownsEvent {
		appendEventOutput(fmt.Sprintf("vm_dataset_restore_start: %s -> %s", remoteEndpoint, destinationDataset))
	}

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("remote", remoteEndpoint).
		Str("local", destinationDataset).
		Str("snapshot", snapshot).
		Msg("starting_target_dataset_restore")

	var restoreErr error
	var output string

	_ = s.destroyLocalDatasetWithRetry(ctx, restorePath, true, 5, 500*time.Millisecond)

	extraEnv := s.buildZeltaEnv(target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_RECV_TOP", "no")
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")
	output, restoreErr = runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		func(line string) {
			appendEventOutput(line)
		},
		"backup",
		"--json",
		remoteEndpoint,
		restorePath,
	)
	if restoreErr != nil {
		logger.L.Warn().
			Err(restoreErr).
			Str("restore_path", restorePath).
			Str("zelta_output", output).
			Msg("target_restore_pull_failed")
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}

	restoreExists, verifyErr := s.localDatasetExists(ctx, restorePath)
	if verifyErr != nil || !restoreExists {
		restoreErr = fmt.Errorf("zelta_recv_dataset_missing: zelta exited successfully but '%s' does not exist", restorePath)
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}

	destinationKind, _ := inferRestoreDatasetKind(destinationDataset)
	isJailDestination := destinationKind == clusterModels.BackupJobModeJail

	destExists, existErr := s.localDatasetExists(ctx, destinationDataset)
	if existErr != nil {
		restoreErr = fmt.Errorf("failed_to_check_destination_dataset_before_restore: %w", existErr)
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}
	if destExists {
		if isJailDestination {
			ctID, err := s.Jail.GetJailCTIDFromDataset(destinationDataset)
			if err == nil {
				logger.L.Info().
					Uint("ct_id", ctID).
					Str("dataset", destinationDataset).
					Msg("stopping_jail_before_restore")

				if err := s.Jail.JailAction(int(ctID), "stop"); err != nil {
					logger.L.Warn().
						Uint("ct_id", ctID).
						Err(err).
						Msg("failed_to_stop_jail_before_restore_continuing_anyway")
				}
			} else {
				logger.L.Warn().
					Str("dataset", destinationDataset).
					Err(err).
					Msg("failed_to_get_jail_ctid_from_existing_dataset_before_restore_continuing_anyway")
			}
		}

	}

	if idx := strings.LastIndex(destinationDataset, "/"); idx > 0 {
		parent := destinationDataset[:idx]
		_ = s.ensureLocalFilesystemPath(ctx, parent)
	}

	backupDataset, renameErr := s.promoteRestoredDataset(ctx, restorePath, destinationDataset)
	if renameErr != nil {
		restoreErr = fmt.Errorf("rename_restore_failed: could not promote %s → %s: %v", restorePath, destinationDataset, renameErr)
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}

	s.fixRestoredProperties(ctx, destinationDataset)

	if reconcileJail {
		restoreNetwork := true
		if payload.RestoreNetwork != nil {
			restoreNetwork = *payload.RestoreNetwork
		}
		if err := s.reconcileRestoredJailFromDatasetWithOptions(ctx, destinationDataset, restoreNetwork); err != nil {
			restoreErr = fmt.Errorf("reconcile_restored_jail_failed: %w", err)
			if rollbackErr := s.rollbackPromotedDataset(ctx, destinationDataset, backupDataset); rollbackErr != nil {
				logger.L.Warn().
					Err(rollbackErr).
					Str("destination_dataset", destinationDataset).
					Str("backup_dataset", backupDataset).
					Msg("failed_to_rollback_jail_dataset_after_reconcile_failure")
				restoreErr = fmt.Errorf("%w; rollback_failed: %v", restoreErr, rollbackErr)
			}
			if ownsEvent {
				s.finalizeRestoreEvent(&event, restoreErr, output)
			} else {
				appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
			}
			return "", restoreErr
		}
	}

	if ownsEvent {
		s.finalizeRestoreEvent(&event, nil, output)
	} else {
		appendEventOutput(fmt.Sprintf("vm_dataset_restore_complete: %s -> %s", remoteEndpoint, destinationDataset))
	}

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("snapshot", snapshot).
		Str("dataset", destinationDataset).
		Msg("target_dataset_restore_completed")

	if !keepBackup && strings.TrimSpace(backupDataset) != "" {
		if err := s.cleanupRestoreBackupDataset(ctx, backupDataset); err != nil {
			logger.L.Warn().
				Err(err).
				Str("backup_dataset", backupDataset).
				Msg("failed_to_cleanup_restore_backup_dataset")
		}
		backupDataset = ""
	}

	return backupDataset, nil
}

func (s *Service) listRemoteVMRepresentativeRoots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	vmRID uint,
	_ string,
) ([]string, error) {
	if vmRID == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	output, err := s.runTargetZFSList(ctx, target, "-t", "filesystem", "-r", "-Hp", "-o", "name", target.BackupRoot)
	if err != nil {
		return nil, err
	}

	bestByBase := make(map[string]string)
	bestRank := make(map[string]int)
	bestDepth := make(map[string]int)

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		dataset := normalizeDatasetPath(line)
		if dataset == "" {
			continue
		}

		suffix := relativeDatasetSuffix(target.BackupRoot, dataset)
		if vmDatasetRoot(suffix) != suffix {
			continue
		}

		kind, rid := inferRestoreDatasetKind(suffix)
		if kind != clusterModels.BackupJobModeVM || rid != vmRID {
			continue
		}

		lineage, _, baseSuffix := classifyDatasetLineage(suffix)
		baseRoot := vmDatasetRoot(baseSuffix)
		baseSuffix = canonicalVMDatasetRoot(baseRoot, vmRID)
		if baseSuffix == "" {
			baseSuffix = baseRoot
		}
		rank := datasetLineageRank(lineage)
		depth := len(strings.Split(suffix, "/"))

		if existing, ok := bestByBase[baseSuffix]; ok {
			existingDepth := bestDepth[baseSuffix]
			if depth > existingDepth {
				continue
			}
			if depth < existingDepth {
				bestByBase[baseSuffix] = dataset
				bestRank[baseSuffix] = rank
				bestDepth[baseSuffix] = depth
				continue
			}

			existingRank := bestRank[baseSuffix]
			if rank > existingRank {
				continue
			}
			if rank == existingRank && existing <= dataset {
				continue
			}
		}

		bestByBase[baseSuffix] = dataset
		bestRank[baseSuffix] = rank
		bestDepth[baseSuffix] = depth
	}

	results := make([]string, 0, len(bestByBase))
	for _, dataset := range bestByBase {
		results = append(results, dataset)
	}
	sort.Strings(results)

	return results, nil
}

func destinationVMRootFromRemoteRoot(backupRoot, remoteRoot string, vmRID uint) string {
	suffix := canonicalVMDatasetRoot(vmDatasetRoot(relativeDatasetSuffix(backupRoot, remoteRoot)), vmRID)
	return normalizeRestoreDestinationDataset(suffix)
}

func canonicalVMDatasetRoot(dataset string, vmRID uint) string {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return ""
	}

	parts := strings.Split(dataset, "/")
	vmIdx := -1
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "virtual-machines" {
			continue
		}
		rid := extractDatasetGuestID(parts[idx+1])
		if rid == 0 {
			continue
		}
		if vmRID > 0 && rid != uint64(vmRID) {
			continue
		}
		vmIdx = idx
	}
	if vmIdx < 0 {
		return dataset
	}

	ridPart := parts[vmIdx+1]
	if vmRID > 0 {
		ridPart = strconv.FormatUint(uint64(vmRID), 10)
	} else if rid := extractDatasetGuestID(ridPart); rid > 0 {
		ridPart = strconv.FormatUint(rid, 10)
	}

	for idx := vmIdx - 1; idx > 0; idx-- {
		if parts[idx] != "sylve" {
			continue
		}
		pool := strings.TrimSpace(parts[idx-1])
		if pool == "" {
			break
		}
		return normalizeDatasetPath(strings.Join([]string{pool, "sylve", "virtual-machines", ridPart}, "/"))
	}

	root := append([]string{}, parts[:vmIdx+2]...)
	root[vmIdx+1] = ridPart
	return normalizeDatasetPath(strings.Join(root, "/"))
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

func (s *Service) listRemoteSnapshotsWithLineage(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	lineageDatasets, err := s.listRemoteLineageDatasets(ctx, target, remoteDataset)
	if err != nil {
		return nil, err
	}

	combined := make([]SnapshotInfo, 0)
	seen := make(map[string]struct{})

	for i, dataset := range lineageDatasets {
		snapshots, snapErr := s.listRemoteSnapshotsForDataset(ctx, target, dataset)
		if snapErr != nil {
			if i == 0 {
				return nil, snapErr
			}

			logger.L.Warn().
				Err(snapErr).
				Str("dataset", dataset).
				Str("base_dataset", remoteDataset).
				Msg("failed_to_list_lineage_snapshots_for_sibling_dataset")
			continue
		}

		for _, snapshot := range snapshots {
			key := strings.TrimSpace(snapshot.Name)
			if key == "" {
				continue
			}
			lineage, outOfBand, _ := classifyDatasetLineage(relativeDatasetSuffix(target.BackupRoot, dataset))
			snapshot.Lineage = lineage
			snapshot.OutOfBand = outOfBand
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			combined = append(combined, snapshot)
		}
	}

	sort.Slice(combined, func(i, j int) bool {
		ti, okI := parseSnapshotCreationTime(combined[i].Creation)
		tj, okJ := parseSnapshotCreationTime(combined[j].Creation)
		if okI && okJ && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if okI != okJ {
			return okI
		}
		return combined[i].Name < combined[j].Name
	})

	return combined, nil
}

func parseSnapshotCreationTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}

	if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(epoch, 0).UTC(), true
	}

	return time.Time{}, false
}

func collapseSnapshotsByShortName(snapshots []SnapshotInfo) []SnapshotInfo {
	if len(snapshots) == 0 {
		return snapshots
	}

	grouped := make(map[string]SnapshotInfo, len(snapshots))
	for _, snapshot := range snapshots {
		shortName := strings.TrimSpace(snapshot.ShortName)
		if shortName == "" {
			if idx := strings.LastIndex(strings.TrimSpace(snapshot.Name), "@"); idx >= 0 {
				shortName = strings.TrimSpace(snapshot.Name[idx:])
			}
		}
		if shortName == "" {
			shortName = strings.TrimSpace(snapshot.Name)
		}
		if shortName == "" {
			continue
		}

		if strings.TrimSpace(snapshot.ShortName) == "" {
			snapshot.ShortName = shortName
		}

		existing, ok := grouped[shortName]
		if !ok || snapshotRepresentativeLess(snapshot, existing) {
			grouped[shortName] = snapshot
		}
	}

	out := make([]SnapshotInfo, 0, len(grouped))
	for _, snapshot := range grouped {
		out = append(out, snapshot)
	}

	sort.Slice(out, func(i, j int) bool {
		ti, okI := parseSnapshotCreationTime(out[i].Creation)
		tj, okJ := parseSnapshotCreationTime(out[j].Creation)
		if okI && okJ && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if okI != okJ {
			return okI
		}

		leftShort := strings.TrimSpace(out[i].ShortName)
		if leftShort == "" {
			leftShort = strings.TrimSpace(out[i].Name)
		}
		rightShort := strings.TrimSpace(out[j].ShortName)
		if rightShort == "" {
			rightShort = strings.TrimSpace(out[j].Name)
		}
		if leftShort != rightShort {
			return leftShort < rightShort
		}

		return strings.TrimSpace(out[i].Name) < strings.TrimSpace(out[j].Name)
	})

	return out
}

func filterBackupSnapshots(snapshots []SnapshotInfo) []SnapshotInfo {
	if len(snapshots) == 0 {
		return snapshots
	}

	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		if !isBackupSnapshotShortName(shortName) {
			continue
		}
		filtered = append(filtered, snapshot)
	}
	return filtered
}

func snapshotShortName(snapshot SnapshotInfo) string {
	shortName := strings.TrimSpace(snapshot.ShortName)
	if shortName != "" {
		return shortName
	}

	fullName := strings.TrimSpace(snapshot.Name)
	if idx := strings.LastIndex(fullName, "@"); idx >= 0 {
		return strings.TrimSpace(fullName[idx:])
	}

	return fullName
}

func isBackupSnapshotShortName(snapshotName string) bool {
	snapshotName = strings.TrimSpace(snapshotName)
	snapshotName = strings.TrimPrefix(snapshotName, "@")
	if snapshotName == "" {
		return false
	}

	return strings.HasPrefix(snapshotName, "zelta_")
}

func snapshotRepresentativeLess(left, right SnapshotInfo) bool {
	leftLineage := datasetLineageRank(left.Lineage)
	rightLineage := datasetLineageRank(right.Lineage)
	if leftLineage != rightLineage {
		return leftLineage < rightLineage
	}

	if left.OutOfBand != right.OutOfBand {
		return !left.OutOfBand
	}

	leftDataset := snapshotDatasetName(left.Name)
	if leftDataset == "" {
		leftDataset = strings.TrimSpace(left.Dataset)
	}
	rightDataset := snapshotDatasetName(right.Name)
	if rightDataset == "" {
		rightDataset = strings.TrimSpace(right.Dataset)
	}

	leftDepth := datasetDepth(leftDataset)
	rightDepth := datasetDepth(rightDataset)
	if leftDepth != rightDepth {
		return leftDepth < rightDepth
	}

	if leftDataset != rightDataset {
		return leftDataset < rightDataset
	}

	return strings.TrimSpace(left.Name) < strings.TrimSpace(right.Name)
}

func datasetDepth(dataset string) int {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return int(^uint(0) >> 1)
	}
	return len(strings.Split(dataset, "/"))
}

func (s *Service) listRemoteLineageDatasets(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]string, error) {
	remoteDataset = strings.TrimSpace(remoteDataset)
	if remoteDataset == "" {
		return nil, fmt.Errorf("remote_dataset_required")
	}

	remoteSuffix := relativeDatasetSuffix(target.BackupRoot, remoteDataset)
	_, _, baseSuffix := classifyDatasetLineage(remoteSuffix)
	if strings.TrimSpace(baseSuffix) == "" {
		baseSuffix = remoteSuffix
	}

	parent := remoteDataset
	leaf := remoteDataset
	if idx := strings.LastIndex(remoteDataset, "/"); idx > 0 {
		parent = remoteDataset[:idx]
		leaf = remoteDataset[idx+1:]
	}
	baseLeaf := leaf
	if idx := strings.LastIndex(baseSuffix, "/"); idx >= 0 && idx+1 < len(baseSuffix) {
		baseLeaf = baseSuffix[idx+1:]
	} else if strings.TrimSpace(baseSuffix) != "" {
		baseLeaf = baseSuffix
	}

	output, err := s.runTargetZFSList(ctx, target, "-t", "filesystem", "-d", "1", "-Hp", "-o", "name", parent)
	if err != nil {
		return []string{remoteDataset}, nil
	}

	results := make([]string, 0)
	seen := make(map[string]struct{})

	add := func(dataset string) {
		dataset = strings.TrimSpace(dataset)
		if dataset == "" {
			return
		}
		if _, ok := seen[dataset]; ok {
			return
		}
		seen[dataset] = struct{}{}
		results = append(results, dataset)
	}

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		dataset := strings.TrimSpace(line)
		if dataset == "" {
			continue
		}
		if dataset == remoteDataset {
			add(dataset)
			continue
		}

		suffix := strings.TrimPrefix(dataset, parent+"/")
		switch {
		case suffix == baseLeaf:
			add(dataset)
		case strings.HasPrefix(suffix, baseLeaf+"_zelta_"):
			add(dataset)
		case strings.HasPrefix(suffix, baseLeaf+".pre_sylve_"):
			add(dataset)
		}
	}

	if len(results) == 0 {
		return []string{remoteDataset}, nil
	}

	sort.Strings(results)
	return results, nil
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
		datasetName := ""
		if idx := strings.Index(fullName, "@"); idx >= 0 {
			shortName = fullName[idx:]
			datasetName = strings.TrimSpace(fullName[:idx])
		}

		creation := fields[1]
		if epoch, err := strconv.ParseInt(strings.TrimSpace(creation), 10, 64); err == nil {
			creation = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
		}

		snapshots = append(snapshots, SnapshotInfo{
			Name:      fullName,
			ShortName: shortName,
			Dataset:   datasetName,
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

func (s *Service) readRemoteVMMetadata(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	fallbackRID uint,
) (*BackupVMMetadataInfo, error) {
	candidates := vmMetadataCandidateDatasets(dataset)
	for _, candidate := range candidates {
		metaRaw, err := s.readRemoteDatasetMetadataFile(ctx, target, candidate, ".sylve/vm.json")
		if err != nil {
			return nil, fmt.Errorf("failed_to_read_remote_vm_metadata: %w", err)
		}
		if strings.TrimSpace(metaRaw) == "" {
			continue
		}

		var payload struct {
			RID      uint   `json:"rid"`
			Name     string `json:"name"`
			Storages []struct {
				Pool string `json:"pool"`
			} `json:"storages"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(metaRaw)), &payload); err != nil {
			return nil, fmt.Errorf("invalid_remote_vm_metadata_json: %w", err)
		}

		rid := payload.RID
		if rid == 0 {
			rid = fallbackRID
		}

		seenPools := make(map[string]struct{})
		pools := make([]string, 0, len(payload.Storages))
		for _, storage := range payload.Storages {
			pool := strings.TrimSpace(storage.Pool)
			if pool == "" {
				continue
			}
			if _, ok := seenPools[pool]; ok {
				continue
			}
			seenPools[pool] = struct{}{}
			pools = append(pools, pool)
		}
		sort.Strings(pools)

		if rid == 0 && strings.TrimSpace(payload.Name) == "" && len(pools) == 0 {
			continue
		}

		return &BackupVMMetadataInfo{
			RID:   rid,
			Name:  strings.TrimSpace(payload.Name),
			Pools: pools,
		}, nil
	}

	return nil, nil
}

func (s *Service) readRemoteDatasetMetadataFile(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	relativeMetaPath string,
) (string, error) {
	mountedOut, err := s.runTargetSSH(ctx, target, "zfs", "get", "-H", "-o", "value", "mounted", dataset)
	if err != nil {
		return "", fmt.Errorf("failed_to_read_dataset_mounted_property: %w", err)
	}
	mountpointOut, err := s.runTargetSSH(ctx, target, "zfs", "get", "-H", "-o", "value", "mountpoint", dataset)
	if err != nil {
		return "", fmt.Errorf("failed_to_read_dataset_mountpoint_property: %w", err)
	}

	mounted := strings.EqualFold(strings.TrimSpace(mountedOut), "yes")
	mountpoint := strings.TrimSpace(mountpointOut)
	if mountpoint == "" || mountpoint == "-" || mountpoint == "none" || mountpoint == "legacy" {
		return "", nil
	}

	mountedByUs := false
	if !mounted {
		if _, err := s.runTargetSSH(ctx, target, "zfs", "mount", dataset); err != nil {
			return "", fmt.Errorf("failed_to_mount_remote_dataset_for_metadata: %w", err)
		}
		mountedByUs = true
	}
	if mountedByUs {
		defer func() {
			_, _ = s.runTargetSSH(context.Background(), target, "zfs", "unmount", dataset)
		}()
	}

	metaPath := strings.TrimSuffix(mountpoint, "/") + "/" + strings.TrimLeft(relativeMetaPath, "/")
	metaRaw, err := s.runTargetSSH(ctx, target, "cat", metaPath)
	if err != nil {
		lower := strings.ToLower(strings.TrimSpace(metaRaw) + " " + err.Error())
		if strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") {
			return "", nil
		}
		return "", err
	}

	return strings.TrimSpace(metaRaw), nil
}

func vmMetadataCandidateDatasets(dataset string) []string {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return nil
	}

	out := []string{dataset}
	root := vmDatasetRoot(dataset)
	if root != "" && root != dataset {
		out = append(out, root)
	}
	return out
}

func vmDatasetRoot(dataset string) string {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return ""
	}

	parts := strings.Split(dataset, "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "virtual-machines" {
			continue
		}
		return strings.Join(parts[:idx+2], "/")
	}

	return dataset
}

func (s *Service) getRestoreTarget(targetID uint) (clusterModels.BackupTarget, error) {
	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, targetID).Error; err != nil {
		return clusterModels.BackupTarget{}, err
	}
	if !target.Enabled {
		return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_disabled")
	}
	if err := s.ensureBackupTargetSSHKeyMaterialized(&target); err != nil {
		return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
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
	root = normalizeDatasetPath(root)
	dataset = normalizeDatasetPath(dataset)
	if root == "" || dataset == "" {
		return false
	}
	if dataset == root {
		return true
	}
	return strings.HasPrefix(dataset, root+"/")
}

func relativeDatasetSuffix(root, dataset string) string {
	root = normalizeDatasetPath(root)
	dataset = normalizeDatasetPath(dataset)
	if dataset == root {
		return ""
	}
	return strings.TrimPrefix(dataset, root+"/")
}

func normalizeDatasetPath(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	for strings.HasSuffix(dataset, "/") {
		dataset = strings.TrimSuffix(dataset, "/")
	}
	return dataset
}

func normalizeRestoreDestinationDataset(destinationDataset string) string {
	destinationDataset = strings.TrimLeft(strings.TrimSpace(destinationDataset), "/")
	return normalizeDatasetPath(destinationDataset)
}

func isValidRestoreDestinationDataset(destinationDataset string) bool {
	destinationDataset = normalizeDatasetPath(destinationDataset)
	if destinationDataset == "" {
		return false
	}
	if !strings.Contains(destinationDataset, "/") {
		return false
	}
	return !strings.Contains(destinationDataset, "@")
}

func classifyDatasetLineage(suffix string) (string, bool, string) {
	suffix = normalizeDatasetPath(suffix)
	if suffix == "" {
		return "active", false, ""
	}

	parts := strings.Split(suffix, "/")
	leaf := parts[len(parts)-1]
	dir := ""
	if len(parts) > 1 {
		dir = strings.Join(parts[:len(parts)-1], "/")
	}

	baseLeaf := leaf
	lineage := "active"

	if idx := strings.Index(leaf, ".pre_sylve_"); idx > 0 {
		lineage = "preserved"
		baseLeaf = leaf[:idx]
	} else if idx := strings.Index(leaf, "_zelta_"); idx > 0 {
		lineage = "rotated"
		baseLeaf = leaf[:idx]
	} else if strings.Contains(leaf, ".pre_") {
		lineage = "other"
	}

	baseSuffix := baseLeaf
	if dir != "" {
		baseSuffix = dir + "/" + baseLeaf
	}

	return lineage, lineage != "active", baseSuffix
}

func datasetLineageRank(lineage string) int {
	switch strings.ToLower(strings.TrimSpace(lineage)) {
	case "active":
		return 0
	case "rotated":
		return 1
	case "preserved":
		return 2
	default:
		return 3
	}
}

func inferRestoreDatasetKind(suffix string) (string, uint) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		segment := strings.TrimSpace(parts[i])
		if segment != "jails" && segment != "virtual-machines" {
			continue
		}
		if id := extractDatasetGuestID(parts[i+1]); id > 0 {
			if segment == "jails" {
				return clusterModels.BackupJobModeJail, uint(id)
			}
			return clusterModels.BackupJobModeVM, uint(id)
		}
	}
	return clusterModels.BackupJobModeDataset, 0
}

func extractDatasetGuestID(raw string) uint64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}

	cutAt := len(raw)
	if idx := strings.Index(raw, "_"); idx >= 0 && idx < cutAt {
		cutAt = idx
	}
	if idx := strings.Index(raw, "."); idx >= 0 && idx < cutAt {
		cutAt = idx
	}

	base := strings.TrimSpace(raw[:cutAt])
	if base == "" {
		return 0
	}

	id, err := strconv.ParseUint(base, 10, 64)
	if err != nil || id == 0 {
		return 0
	}
	return id
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
