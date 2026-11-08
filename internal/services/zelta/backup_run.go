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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) runBackupJobCore(
	ctx context.Context,
	job *clusterModels.BackupJob,
	resultEncryption *backupEncryptionObservation,
) (resultErr error) {
	repairRequired, err := clusterModels.BackupJobRepairRequired(s.DB, job.ID)
	if err != nil {
		return fmt.Errorf("backup_job_repair_state_lookup_failed: %w", err)
	}
	if repairRequired {
		return fmt.Errorf("backup_job_repair_required")
	}
	backupEventCreated := false
	defer func() {
		if backupEventCreated || s.TelemetryDB == nil {
			return
		}

		auditStatus := "success"
		errMsg := ""
		if resultErr != nil {
			auditStatus = "failed"
			errMsg = resultErr.Error()
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_job_run", job.ID, auditStatus, errMsg, map[string]any{
			"status": auditStatus,
			"error":  errMsg,
		})
	}()

	jobGuestType, jobGuestID := backupJobGuestIdentity(job)

	// Dataset-mode jobs don't have a guest identity, so use the
	// source dataset path as the lock key to prevent concurrent
	// operations on the same dataset.
	if jobGuestType == "" && jobGuestID == 0 &&
		job.Mode == clusterModels.BackupJobModeDataset &&
		strings.TrimSpace(job.SourceDataset) != "" {
		jobGuestType = clusterModels.BackupJobModeDataset
		jobGuestID = datasetHash(strings.TrimSpace(job.SourceDataset))
	}

	if jobGuestType != "" && jobGuestID > 0 {
		localNodeID := s.localNodeID()
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, localNodeID)
		if leaseErr != nil {
			return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
		}
		if !allowed {
			return fmt.Errorf("replication_lease_not_owned")
		}
	}
	if ok, holder := s.acquireWorkloadOperation(jobGuestType, jobGuestID, fmt.Sprintf("backup_job:%d", job.ID)); !ok {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			jobGuestType,
			jobGuestID,
		)
	}
	defer s.releaseWorkloadOperation(jobGuestType, jobGuestID)

	if job.StopBeforeBackup {
		logger.L.Debug().Uint("job_id", job.ID).Msg("stop_before_backup_enabled")
	}

	if !job.Target.Enabled {
		return fmt.Errorf("backup_target_disabled")
	}
	_, targetRoot, err := canonicalizeBackupTarget(&job.Target)
	if err != nil {
		return err
	}

	event := clusterModels.BackupEvent{
		JobID:     &job.ID,
		Mode:      job.Mode,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}

	sourceDataset, missingSource := strings.TrimSpace(job.SourceDataset), "source_dataset_required"
	switch job.Mode {
	case clusterModels.BackupJobModeDataset, clusterModels.BackupJobModeVM:
	case clusterModels.BackupJobModeJail:
		sourceDataset, missingSource = strings.TrimSpace(job.JailRootDataset), "jail_root_dataset_required"
	default:
		return fmt.Errorf("invalid_backup_job_mode")
	}
	if sourceDataset == "" {
		return errors.New(missingSource)
	}
	parsedSource, err := remoteexec.ParseZFSDataset(sourceDataset)
	if err != nil {
		return fmt.Errorf("invalid_backup_source_dataset: %w", err)
	}
	sourceDataset = parsedSource.String()
	if err := cluster.ValidateBackupJobSafetyWithDB(ctx, s.DB, job); err != nil {
		return fmt.Errorf("backup_job_safety_validation_failed: %w", err)
	}

	vmRID := uint(0)
	vmSourceDatasets := []string{}
	if job.Mode == clusterModels.BackupJobModeVM {
		_, vmRID = inferRestoreDatasetKind(sourceDataset)
		if vmRID == 0 {
			return fmt.Errorf("invalid_vm_source_dataset")
		}

		sources, err := s.resolveVMBackupSourceDatasets(ctx, vmRID, sourceDataset)
		if err != nil {
			return fmt.Errorf("resolve_vm_backup_sources_failed: %w", err)
		}

		for _, vmSource := range sources {
			parsedVMSource, err := remoteexec.ParseZFSDataset(vmSource)
			if err != nil {
				return fmt.Errorf("invalid_vm_backup_source_dataset: %w", err)
			}
			vmSource = parsedVMSource.String()

			exists, err := s.localDatasetExists(ctx, vmSource)
			if err != nil {
				return fmt.Errorf("failed_to_check_vm_backup_source_dataset_%s: %w", vmSource, err)
			}
			if !exists {
				if vmSource == sourceDataset {
					logger.L.Warn().
						Uint("job_id", job.ID).
						Uint("vm_rid", vmRID).
						Str("dataset", vmSource).
						Msg("skipping_missing_preferred_vm_backup_source_dataset")
					continue
				}

				return fmt.Errorf("vm_backup_source_dataset_not_found: %s", vmSource)
			}

			vmSourceDatasets = append(vmSourceDatasets, vmSource)
		}

		if len(vmSourceDatasets) == 0 {
			return fmt.Errorf("vm_source_datasets_not_found")
		}
		if !slices.Contains(vmSourceDatasets, sourceDataset) {
			logger.L.Info().
				Uint("job_id", job.ID).
				Uint("vm_rid", vmRID).
				Str("missing_source", sourceDataset).
				Str("fallback_source", vmSourceDatasets[0]).
				Msg("vm_backup_source_fallback_selected")
			sourceDataset = vmSourceDatasets[0]
		}
	}

	guestID, guestSources := jobGuestID, []string{sourceDataset}
	if job.Mode == clusterModels.BackupJobModeVM {
		guestID, guestSources = vmRID, vmSourceDatasets
	}
	if job.Mode == clusterModels.BackupJobModeVM || job.Mode == clusterModels.BackupJobModeJail {
		if err := s.refreshGuestMetadata(job.Mode, guestID); err != nil {
			return fmt.Errorf("backup_%s_metadata_refresh_failed: %w", job.Mode, err)
		}
	}

	event.SourceDataset = sourceDataset

	encrypted, encryptionErr := s.detectBackupSourcesEncryption(
		ctx,
		guestSources,
		job.Recursive,
	)
	if encryptionErr != nil {
		logger.L.Warn().
			Err(encryptionErr).
			Uint("job_id", job.ID).
			Msg("backup_source_encryption_detection_failed")
	} else if resultEncryption != nil {
		*resultEncryption = backupEncryptionObservation{known: true, encrypted: encrypted}
	}

	// Verify encryption keys are loaded before starting the backup.
	// Without this check the zelta send would fail with a cryptic ZFS error.
	if encrypted {
		for _, encryptionSource := range guestSources {
			ds, dsErr := s.getLocalDataset(ctx, encryptionSource)
			if dsErr != nil || ds == nil || !ds.IsEncrypted() {
				continue
			}
			keyLoaded, keyErr := s.ensureEncryptionKeyForDataset(ctx, ds)
			if keyErr != nil {
				return fmt.Errorf("encryption_key_load_failed_for_%s: %w", encryptionSource, keyErr)
			}
			if !keyLoaded {
				return fmt.Errorf(
					"encryption_key_not_available_for_%s: run 'zfs load-key %s' first",
					encryptionSource,
					encryptionSource,
				)
			}
		}
	}

	var destSuffix string
	switch job.Mode {
	case clusterModels.BackupJobModeVM:
		destSuffix = vmDestSuffixForSource(job.DestSuffix, sourceDataset)
	case clusterModels.BackupJobModeJail:
		destSuffix = jailDestSuffixForSource(job.DestSuffix, sourceDataset)
	default:
		destSuffix = datasetDestSuffix(job.DestSuffix, sourceDataset)
	}
	backupScopes := s.backupRunScopes(job, sourceDataset, destSuffix, vmSourceDatasets)
	operationRoots := make([]string, 0, len(backupScopes))
	for _, scope := range backupScopes {
		parsedScope, err := remoteexec.ParseZFSDataset(scope.sourceDataset)
		if err != nil {
			return fmt.Errorf("invalid_backup_scope_source_dataset: %w", err)
		}
		if _, err := remoteexec.JoinZFSDataset(targetRoot, scope.destSuffix); err != nil {
			return fmt.Errorf("invalid_backup_scope_target_dataset: %w", err)
		}
		operationRoots = append(operationRoots, parsedScope.String())
	}
	acquired, holder, heldRoots := s.acquireDatasetOperations(operationRoots)
	if !acquired {
		return fmt.Errorf("backup_dataset_operation_conflict: holder=%s", holder)
	}
	defer s.releaseDatasetOperations(heldRoots)
	releaseTargetKey, err := s.acquireBackupTargetSSHKey(&job.Target)
	if err != nil {
		return fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	defer releaseTargetKey()
	if err := s.EnsureBackupTargetHostKey(ctx, &job.Target); err != nil {
		return err
	}

	if err := s.validateBackupScopesDoNotOverlapTarget(ctx, job, backupScopes); err != nil {
		return fmt.Errorf("backup_scope_validation_failed: %w", err)
	}
	eventTargetEndpoint, err := canonicalZeltaEndpoint(&job.Target, destSuffix)
	if err != nil {
		return err
	}
	event.TargetEndpoint = eventTargetEndpoint
	if err := s.DB.Create(&event).Error; err != nil {
		return fmt.Errorf("create_backup_event_failed: %w", err)
	}
	backupEventCreated = true
	stopHeartbeat := s.startBackupEventHeartbeat(ctx, event.ID, time.Minute)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("source", sourceDataset).
		Str("target", event.TargetEndpoint).
		Msg("starting_zelta_backup")

	var output string
	var runErr error
	note := func(line string) {
		if line = strings.TrimSpace(line); line == "" {
			return
		}
		if output != "" {
			output += "\n"
		}
		output += line
	}
	fail := func(err error) error {
		runErr = err
		note(err.Error())
		return err
	}

	defer func() {
		stopHeartbeat()
		s.finalizeBackupEvent(&event, runErr, output)

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("status", event.Status).
			Err(runErr).
			Msg("zelta_backup_completed")
	}()
	defer func() {
		resultErr = runErr
	}()
	defer recoverOperationPanic("backup_job", &runErr)

	// Inspect the currently active tree before topology preflight is allowed
	// to rename it. Otherwise an unowned snapshot could be moved into a
	// generation dataset, disappear from this check, and later be mistaken
	// for job-owned cleanup state.
	for _, sc := range backupScopes {
		activeDataset := remoteActiveDatasetForSuffix(job.Target.BackupRoot, sc.destSuffix)
		foreign, listErr := s.findForeignTargetSnapshots(ctx, job, sc.sourceDataset, activeDataset, backupScopes)
		if listErr != nil {
			return fail(fmt.Errorf("pre_backup_foreign_snapshot_check_failed_%s: %w", activeDataset, listErr))
		}
		if len(foreign) > 0 {
			return fail(fmt.Errorf("backup_target_foreign_snapshots_present:%s", strings.Join(foreign, ",")))
		}
	}

	prune := func(keepLast bool) error {
		var pruneErr error
		if keepLast {
			var lines []string
			lines, pruneErr = s.pruneBackupJob(ctx, job, backupScopes)
			for _, line := range lines {
				note(line)
			}
		}
		lines, generationErr := s.pruneArchivedGenerations(ctx, job, backupScopes)
		for _, line := range lines {
			note(line)
		}
		return errors.Join(pruneErr, generationErr)
	}

	if since, localPoints, err := s.backupUnchangedSince(ctx, job, backupScopes); err != nil {
		logger.L.Debug().Err(err).Uint("job_id", job.ID).Msg("backup_change_check_inconclusive")
	} else if since != "" {
		note("backup_unchanged_since: " + since)
		logger.L.Info().Uint("job_id", job.ID).Str("snapshot", since).Msg("backup_skipped_unchanged")
		runErr = prune(job.PruneKeepLast > 0 && localPoints > job.PruneKeepLast)
		return runErr
	}

	// The lease can change hands while a guest stops, so it's checked before
	// stopping and again at the transfer boundary.
	leaseHeld := func() error {
		if jobGuestType == "" || jobGuestID == 0 {
			return nil
		}
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, s.localNodeID())
		if leaseErr != nil {
			return fmt.Errorf("pre_transfer_lease_check_failed: %w", leaseErr)
		}
		if !allowed {
			return fmt.Errorf("lease_lost_before_transfer: ownership transferred to another node")
		}
		return nil
	}
	if err := leaseHeld(); err != nil {
		runErr = err
		return runErr
	}

	guestRestore, guestStoppedByBackup, quiesceErr := s.quiesceBackupGuestContext(ctx, job, vmRID)
	if quiesceErr != nil {
		return fail(quiesceErr)
	}
	// The guest restarts right after the snapshot; this covers runs that stop earlier.
	var restartErr error
	defer func() {
		if restartErr != nil {
			runErr = errors.Join(runErr, restartErr)
		}
	}()
	restartGuest := func() {
		if !guestStoppedByBackup || guestRestore == nil {
			return
		}
		guestStoppedByBackup = false
		if err := guestRestore(); err != nil {
			restartErr = fmt.Errorf("failed_to_restore_guest_running_state: %w", err)
			note(restartErr.Error())
			logger.L.Warn().Err(restartErr).Uint("job_id", job.ID).Msg("failed_to_restart_guest_after_backup")
		}
	}
	defer restartGuest()
	var topologyArchives []archivedBackupTopology
	backupTransferStarted := false
	defer func() {
		if backupTransferStarted || len(topologyArchives) == 0 {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		s.rollbackBackupTopologyArchives(rollbackCtx, &job.Target, topologyArchives)
	}()

	topologyArchives, archiveErr := s.archiveChangedBackupTopologies(ctx, job, backupScopes)
	if archiveErr != nil {
		return fail(fmt.Errorf("backup_topology_preflight_failed: %w", archiveErr))
	}
	for _, archive := range topologyArchives {
		note(fmt.Sprintf("backup_topology_rotated: %s -> %s", archive.Active, archive.Archived))
	}

	if err := leaseHeld(); err != nil {
		return fail(err)
	}

	snapshotName := backupSnapshotNameForJob(job.ID)
	if err := snapshotBackupScopes(ctx, backupScopes, snapshotName, job.Recursive); err != nil {
		return fail(err)
	}
	restartGuest()
	backupTransferStarted = true

	failed, runErr := s.sendBackupScopes(ctx, job, backupScopes, event.ID, snapshotName, note)

	if runErr != nil && isReplicationResumeStateError(runErr) {
		logger.L.Info().
			Uint("job_id", job.ID).
			Str("source", failed.sourceDataset).
			Str("target", job.Target.ZeltaEndpoint(failed.destSuffix)).
			Err(runErr).
			Msg("backup_resume_state_abort_starting")
		abortOut, abortErr := s.abortTargetResumableReceiveState(ctx, &job.Target, failed.destSuffix)
		note(abortOut)
		if abortErr != nil {
			runErr = fmt.Errorf("backup_resume_abort_failed: %w (original: %v)", abortErr, runErr)
		} else {
			failed, runErr = s.sendBackupScopes(ctx, job, backupScopes, event.ID, snapshotName, note)
		}
	}

	if runErr != nil && shouldAutoRotateBackupErrorCode(runErr.Error()) {
		logger.L.Info().
			Uint("job_id", job.ID).
			Str("source", failed.sourceDataset).
			Str("target", job.Target.ZeltaEndpoint(failed.destSuffix)).
			Str("reason", runErr.Error()).
			Msg("backup_auto_reseed_starting")
		if reseedErr := s.reseedDivergedBackupScope(ctx, job, failed, note); reseedErr != nil {
			runErr = fmt.Errorf("backup_auto_reseed_failed: %w", reseedErr)
		} else {
			failed, runErr = s.sendBackupScopes(ctx, job, backupScopes, event.ID, snapshotName, note)
			if runErr == nil {
				logger.L.Info().Uint("job_id", job.ID).Str("source", failed.sourceDataset).Msg("backup_completed_after_reseed")
			}
		}
	}
	if runErr != nil {
		return runErr
	}

	const phase = "backup_phase: finalizing"
	note(phase)
	if appendErr := s.AppendBackupEventOutput(event.ID, phase); appendErr != nil {
		logger.L.Warn().Uint("event_id", event.ID).Err(appendErr).Msg("append_backup_event_phase_failed")
	}

	if job.Mode == clusterModels.BackupJobModeVM || job.Mode == clusterModels.BackupJobModeJail {
		if metadataErr := s.validateBackupSnapshotNetworkMetadata(
			ctx,
			job.Mode,
			guestID,
			guestSources,
			snapshotName,
		); metadataErr != nil {
			cleanupErr := s.cleanupRejectedBackupSnapshot(ctx, job, snapshotName, backupScopes)
			if cleanupErr != nil {
				cleanupErr = fmt.Errorf("backup_stale_snapshot_cleanup_failed: %w", cleanupErr)
			}
			return fail(errors.Join(metadataErr, cleanupErr))
		}
	}
	if commitErr := s.commitBackupSnapshot(ctx, job, snapshotName, backupScopes); commitErr != nil {
		return fail(fmt.Errorf("backup_commit_failed: %w", commitErr))
	}

	for _, scope := range backupScopes {
		if metaErr := s.syncTargetBackupJobMetadata(ctx, job, scope.sourceDataset, scope.destSuffix); metaErr != nil {
			return fail(fmt.Errorf("backup_target_metadata_sync_failed: source=%s: %w", scope.sourceDataset, metaErr))
		}
	}

	runErr = prune(job.PruneKeepLast > 0)
	return runErr
}

func (s *Service) backupUnchangedSince(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) (string, int, error) {
	coordinator, err := backupCommitCoordinatorScope(job, scopes)
	if err != nil {
		return "", 0, err
	}
	root := normalizeDatasetPath(scopes[coordinator].sourceDataset)
	snapshots, err := s.listLocalSnapshotsForDataset(ctx, root)
	if err != nil {
		return "", 0, err
	}
	last, points := "", 0
	for _, snapshot := range snapshots {
		shortName := strings.TrimPrefix(snapshotShortName(snapshot), "@")
		if snapshotDatasetName(snapshot.Name) == root && backupSnapshotRequiresCommit(job.ID, shortName) {
			last = shortName
			points++
		}
	}
	if last == "" {
		return "", points, nil
	}

	// `written` only counts synced transaction groups.
	pools := []string{}
	for _, scope := range scopes {
		pool, _, _ := strings.Cut(scope.sourceDataset, "/")
		if !slices.Contains(pools, pool) {
			pools = append(pools, pool)
		}
	}
	if _, err := utils.RunCommandWithContext(ctx, "zpool", append([]string{"sync"}, pools...)...); err != nil {
		return "", points, err
	}
	for _, scope := range scopes {
		args := []string{"get", "-H", "-p", "-o", "name,value", "-t", "filesystem,volume"}
		if job.Recursive {
			args = append(args, "-r")
		}
		output, err := utils.RunCommandWithContext(ctx, "zfs", append(args, "written@"+last, scope.sourceDataset)...)
		if err != nil {
			return "", points, err
		}
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			// "-" means a dataset created since the snapshot.
			if fields := strings.Split(line, "\t"); len(fields) != 2 || fields[1] != "0" {
				return "", points, nil
			}
		}
	}

	expected, err := s.buildLocalBackupManifest(ctx, job.ID, last, job.Recursive, scopes)
	if err != nil {
		return "", points, err
	}
	actual, err := s.buildRemoteBackupManifest(ctx, job, last, scopes)
	if err != nil {
		return "", points, err
	}
	if err := compareBackupManifests(expected, actual); err != nil {
		return "", points, err
	}
	commitRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scopes[coordinator].destSuffix)
	metadata, err := s.getRemoteBackupCommitMetadata(ctx, &job.Target, commitRoot+"@"+last)
	if err != nil {
		return "", points, err
	}
	if err := validateBackupCommitForJob(metadata, job.ID, last); err != nil {
		return "", points, err
	}
	if metadata.ManifestHash != backupManifestHash(expected) {
		return "", points, fmt.Errorf("backup_commit_manifest_changed")
	}
	return last, points, nil
}

// zfs cannot snapshot across pools in one call, so this runs one command per pool.
func snapshotBackupScopes(ctx context.Context, scopes []backupScope, name string, recursive bool) error {
	flag := []string{}
	if recursive {
		flag = []string{"-r"}
	}
	var pools []string
	byPool := make(map[string][]string)
	for _, scope := range scopes {
		pool, _, _ := strings.Cut(scope.sourceDataset, "/")
		if _, seen := byPool[pool]; !seen {
			pools = append(pools, pool)
		}
		byPool[pool] = append(byPool[pool], scope.sourceDataset+"@"+name)
	}
	var taken []string
	for _, pool := range pools {
		args := append(append([]string{"snapshot"}, flag...), byPool[pool]...)
		if _, err := utils.RunCommandWithContext(ctx, "zfs", args...); err != nil {
			for _, snapshot := range taken {
				destroyArgs := append(append([]string{"destroy"}, flag...), snapshot)
				if _, destroyErr := utils.RunCommandWithContext(context.WithoutCancel(ctx), "zfs", destroyArgs...); destroyErr != nil {
					err = errors.Join(err, destroyErr)
				}
			}
			return fmt.Errorf("%s: %w", backupErrorSourceSnapshotFailed, err)
		}
		taken = append(taken, byPool[pool]...)
	}
	return nil
}

func (s *Service) sendBackupScopes(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
	eventID uint,
	snapshotName string,
	note func(string),
) (backupScope, error) {
	for i, scope := range scopes {
		if job.Mode == clusterModels.BackupJobModeVM {
			note(fmt.Sprintf(
				"vm_dataset_backup_start[%d/%d]: %s -> %s",
				i+1, len(scopes), scope.sourceDataset, job.Target.ZeltaEndpoint(scope.destSuffix),
			))
		}
		output, err := s.backupWithEventProgressSnapshotNameRecursive(
			ctx, &job.Target, scope.sourceDataset, scope.destSuffix, eventID, snapshotName, job.Recursive,
		)
		note(output)
		if err == nil {
			if code := backupOutputErrorCode(output); code != "" {
				err = errors.New(code)
			}
		}
		if err != nil {
			return scope, err
		}
	}
	return backupScope{}, nil
}

func (s *Service) reseedDivergedBackupScope(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scope backupScope,
	note func(string),
) error {
	active := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
	var bases map[string]SnapshotInfo
	local, localErr := s.listLocalSnapshotsForDataset(ctx, scope.sourceDataset)
	remote, remoteErr := s.listRemoteSnapshotsForDatasetRecursive(ctx, &job.Target, active)
	if localErr == nil && remoteErr == nil {
		bases = latestCommonBackupSnapshotsByDataset(
			local, remote, scope.sourceDataset, active, backupSnapshotPrefixForJob(job.ID),
		)
	}

	from, archived, err := s.archiveActiveTargetDatasetForReseed(ctx, &job.Target, scope.destSuffix)
	if err != nil {
		return err
	}
	if archived == "" {
		return nil
	}
	note(fmt.Sprintf("auto_archived_target_dataset: %s -> %s", from, archived))
	if len(bases) == 0 {
		return nil
	}
	if err := s.cloneBackupTreeFromArchive(ctx, job, scope, active, archived, bases); err != nil {
		note(fmt.Sprintf("backup_reseed_clone_failed_full_send: %v", err))
		logger.L.Warn().Err(err).Uint("job_id", job.ID).Str("archive", archived).Msg("backup_reseed_clone_failed")
		return nil
	}
	note(fmt.Sprintf("backup_reseed_cloned_from: %s", archived))
	return nil
}

// Promoting each clone moves the shared history back into the active tree; the
// archive keeps only the diverged snapshots.
func (s *Service) cloneBackupTreeFromArchive(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scope backupScope,
	active, archived string,
	bases map[string]SnapshotInfo,
) error {
	root := normalizeDatasetPath(scope.sourceDataset)
	if _, ok := bases[root]; !ok {
		return fmt.Errorf("no_shared_snapshot_for_root")
	}
	output, err := utils.RunCommandWithContext(ctx, "zfs", backupDatasetListArgs(root, job.Recursive)[1:]...)
	if err != nil {
		return err
	}
	types, err := parseBackupDatasetTree(output, root, job.Recursive)
	if err != nil {
		return err
	}
	datasets := make([]string, 0, len(bases))
	for dataset := range bases {
		if _, ok := types[dataset]; ok {
			datasets = append(datasets, dataset)
		}
	}
	sort.Strings(datasets)

	for _, dataset := range datasets {
		suffix := relativeDatasetSuffix(root, dataset)
		origin := remoteActiveDatasetForSuffix(archived, suffix) + snapshotShortName(bases[dataset])
		clone := remoteActiveDatasetForSuffix(active, suffix)
		// Match what zelta sets on a received tree.
		args := []string{"zfs", "clone"}
		if suffix == "" {
			args = append(args, "-o", "readonly=on")
		}
		if types[dataset] == "filesystem" {
			args = append(args, "-o", "canmount=noauto")
		}
		if _, err := s.runTargetSSH(ctx, &job.Target, append(args, origin, clone)...); err != nil {
			return fmt.Errorf("clone %s: %w", clone, err)
		}
		if _, err := s.runTargetSSH(ctx, &job.Target, "zfs", "promote", clone); err != nil {
			// An unpromoted clone owns no snapshots; drop it so the send can create it.
			if _, destroyErr := s.runTargetSSH(ctx, &job.Target, "zfs", "destroy", clone); destroyErr != nil {
				err = errors.Join(err, destroyErr)
			}
			return fmt.Errorf("promote %s: %w", clone, err)
		}
	}
	return nil
}
