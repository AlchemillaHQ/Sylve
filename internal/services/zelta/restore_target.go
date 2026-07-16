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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	RestoreNetwork     *bool  `json:"restore_network,omitempty"`
}

type BackupTargetDatasetInfo struct {
	Name          string `json:"name"` // full remote dataset path
	Encrypted     bool   `json:"encrypted"`
	Suffix        string `json:"suffix"`
	BaseSuffix    string `json:"baseSuffix"`
	Lineage       string `json:"lineage"` // "active" | "rotated" | "other"
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

type oobGuestRestoreDestination struct {
	Kind    string
	GuestID uint
	Dataset string
}

func canonicalGuestRestoreDestination(dataset, kind string, guestID uint) bool {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" || guestID == 0 || guestID > 9999 {
		return false
	}

	parts := strings.Split(dataset, "/")
	if len(parts) != 4 || strings.TrimSpace(parts[0]) == "" || parts[1] != "sylve" {
		return false
	}

	segment := ""
	switch kind {
	case clusterModels.BackupJobModeVM:
		segment = "virtual-machines"
	case clusterModels.BackupJobModeJail:
		segment = "jails"
	default:
		return false
	}

	return parts[2] == segment && parts[3] == strconv.FormatUint(uint64(guestID), 10)
}

func resolveOOBGuestRestoreDestination(
	backupRoot, remoteDataset, destinationDataset string,
) (*oobGuestRestoreDestination, error) {
	remoteSuffix := relativeDatasetSuffix(backupRoot, remoteDataset)
	sourceKind, _ := inferRestoreDatasetKind(remoteSuffix)
	destinationDataset = normalizeRestoreDestinationDataset(destinationDataset)
	destinationKind, destinationID := inferRestoreDatasetKind(destinationDataset)

	sourceIsGuest := sourceKind == clusterModels.BackupJobModeVM || sourceKind == clusterModels.BackupJobModeJail
	destinationIsGuest := destinationKind == clusterModels.BackupJobModeVM || destinationKind == clusterModels.BackupJobModeJail
	if !sourceIsGuest && !destinationIsGuest {
		return nil, nil
	}
	if !sourceIsGuest || !destinationIsGuest || sourceKind != destinationKind {
		return nil, fmt.Errorf(
			"restore_guest_destination_kind_mismatch: source_kind=%s destination_kind=%s",
			sourceKind,
			destinationKind,
		)
	}
	if destinationID == 0 || destinationID > 9999 {
		return nil, fmt.Errorf("invalid_guest_id")
	}
	if !canonicalGuestRestoreDestination(destinationDataset, destinationKind, destinationID) {
		return nil, fmt.Errorf(
			"restore_guest_destination_must_be_canonical_root: expected pool/sylve/%s/%d",
			map[string]string{
				clusterModels.BackupJobModeVM:   "virtual-machines",
				clusterModels.BackupJobModeJail: "jails",
			}[destinationKind],
			destinationID,
		)
	}

	return &oobGuestRestoreDestination{
		Kind:    destinationKind,
		GuestID: destinationID,
		Dataset: destinationDataset,
	}, nil
}

func (s *Service) requireOOBGuestRestoreAvailable(
	ctx context.Context,
	destination *oobGuestRestoreDestination,
	datasets []string,
	checkDatasets bool,
) error {
	if destination == nil {
		return nil
	}
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: cluster_service_not_initialized")
	}
	if err := s.Cluster.RequireGuestIDAvailable(ctx, destination.GuestID); err != nil {
		return err
	}

	if checkDatasets {
		seen := make(map[string]struct{}, len(datasets)+1)
		for _, dataset := range append([]string{destination.Dataset}, datasets...) {
			dataset = normalizeRestoreDestinationDataset(dataset)
			if dataset == "" {
				continue
			}
			if _, ok := seen[dataset]; ok {
				continue
			}
			seen[dataset] = struct{}{}

			exists, err := s.localDatasetExists(ctx, dataset)
			if err != nil {
				return fmt.Errorf("restore_destination_dataset_check_failed: %w", err)
			}
			if exists {
				return fmt.Errorf(
					"restore_destination_guest_dataset_exists: guest_id=%d dataset=%s",
					destination.GuestID,
					dataset,
				)
			}
		}
	}

	return nil
}

func (s *Service) preflightOOBGuestRestoreDestination(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset, destinationDataset string,
) (*oobGuestRestoreDestination, error) {
	if target == nil {
		return nil, fmt.Errorf("backup_target_required")
	}

	destination, err := resolveOOBGuestRestoreDestination(
		target.BackupRoot,
		strings.TrimSpace(remoteDataset),
		destinationDataset,
	)
	if err != nil {
		return nil, err
	}
	if err := s.requireOOBGuestRestoreAvailable(ctx, destination, nil, true); err != nil {
		return nil, err
	}

	return destination, nil
}

func (s *Service) ListRemoteTargetDatasets(ctx context.Context, targetID uint) ([]BackupTargetDatasetInfo, error) {
	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return nil, err
	}

	fsOutput, err := s.runTargetZFSList(ctx, &target, "-t", "filesystem", "-r", "-Hp", "-o", "name,encryption", target.BackupRoot)
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
		dataset, encrypted := parseRemoteDatasetEncryption(line)
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

		jailCTID := uint(0)
		vmRID := uint(0)
		switch kind {
		case clusterModels.BackupJobModeJail:
			jailCTID = guestID
		case clusterModels.BackupJobModeVM:
			vmRID = guestID
		}

		datasets = append(datasets, BackupTargetDatasetInfo{
			Name:          dataset,
			Encrypted:     encrypted,
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

func parseRemoteDatasetEncryption(line string) (dataset string, encrypted bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", false
	}
	dataset = fields[0]
	if len(fields) < 2 {
		return dataset, false
	}

	encryption := strings.ToLower(strings.TrimSpace(fields[len(fields)-1]))
	return dataset, encryption != "" && encryption != "-" && encryption != "none" && encryption != "off"
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
	snapshots, err = s.filterRestorableTargetSnapshots(ctx, &target, kind, snapshots)
	if err != nil {
		return nil, fmt.Errorf("failed_to_validate_target_restore_points: %w", err)
	}
	if kind == clusterModels.BackupJobModeVM {
		snapshots = collapseSnapshotsByShortName(snapshots)
	}

	return snapshots, nil
}

// filterRestorableTargetSnapshots applies the commit contract to out-of-band
// target browsing. Dataset and jail restore points created before c1 remain
// visible for compatibility. VM restore requires a c1 root-set manifest, so a
// legacy VM snapshot can never be advertised as restorable.
func (s *Service) filterRestorableTargetSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	datasetKind string,
	snapshots []SnapshotInfo,
) ([]SnapshotInfo, error) {
	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		_, commitRequired, parseErr := backupCommitJobIDFromSnapshot(shortName)
		if parseErr != nil {
			// A malformed c1-looking name is neither a valid legacy point nor a
			// committed point. Do not expose it through restore discovery.
			continue
		}
		if !commitRequired {
			if datasetKind == clusterModels.BackupJobModeVM {
				continue
			}
			snapshot.Legacy = true
			filtered = append(filtered, snapshot)
			continue
		}

		remoteDataset := snapshotDatasetName(snapshot.Name)
		if remoteDataset == "" {
			remoteDataset = normalizeDatasetPath(snapshot.Dataset)
		}
		metadata, err := s.requireRemoteBackupRestoreCommitBySnapshot(
			ctx,
			target,
			remoteDataset,
			shortName,
		)
		if err != nil {
			if strings.Contains(err.Error(), "get_backup_commit_metadata_failed") {
				return nil, err
			}
			continue
		}
		snapshot.Committed = metadata.Version == backupCommitVersion
		filtered = append(filtered, snapshot)
	}
	return filtered, nil
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
	if _, err := s.preflightOOBGuestRestoreDestination(
		ctx,
		&target,
		remoteDataset,
		destinationDataset,
	); err != nil {
		return err
	}

	if acquired, holder := s.acquireRestoreDestination(destinationDataset); !acquired {
		return fmt.Errorf(
			"restore_destination_already_running: dataset=%s holder=%s",
			destinationDataset,
			holder,
		)
	}
	s.releaseRestoreDestination(destinationDataset)

	return db.EnqueueJSON(ctx, restoreFromTargetQueueName, restoreFromTargetPayload{
		TargetID:           targetID,
		RemoteDataset:      remoteDataset,
		Snapshot:           snapshot,
		DestinationDataset: destinationDataset,
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
	if acquired, holder := s.acquireRestoreDestination(payload.DestinationDataset); !acquired {
		return fmt.Errorf(
			"restore_destination_already_running: dataset=%s holder=%s",
			payload.DestinationDataset,
			holder,
		)
	}
	defer s.releaseRestoreDestination(payload.DestinationDataset)
	restoreWorkloadType, restoreWorkloadID := restoreWorkloadIdentityForDataset(payload.DestinationDataset)
	if acquired, holder := s.acquireWorkloadOperation(
		restoreWorkloadType,
		restoreWorkloadID,
		"restore_from_target",
	); !acquired {
		return fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			restoreWorkloadType,
			restoreWorkloadID,
		)
	}
	defer s.releaseWorkloadOperation(restoreWorkloadType, restoreWorkloadID)
	if restoreWorkloadType == clusterModels.BackupJobModeDataset {
		if err := s.requireNoManagedGuestsWithinRestore(ctx, payload.DestinationDataset); err != nil {
			return err
		}
	}

	remoteDataset := strings.TrimSpace(payload.RemoteDataset)
	if !datasetWithinRoot(target.BackupRoot, remoteDataset) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}
	if _, err := s.preflightOOBGuestRestoreDestination(
		ctx,
		target,
		remoteDataset,
		payload.DestinationDataset,
	); err != nil {
		return err
	}

	restoreSuffix := relativeDatasetSuffix(target.BackupRoot, remoteDataset)
	kind, vmRID := inferRestoreDatasetKind(restoreSuffix)
	if kind == clusterModels.BackupJobModeVM && vmRID > 0 {
		return s.runRestoreFromTargetVM(ctx, target, payload, nil)
	}

	_, err := s.runRestoreFromTargetSingleDataset(ctx, target, payload, nil, true, false, false, nil)
	return err
}

func (s *Service) runRestoreFromTargetVM(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	payload restoreFromTargetPayload,
	jobID *uint,
) (retErr error) {
	snapshot := strings.TrimSpace(payload.Snapshot)
	if snapshot != "" && !strings.HasPrefix(snapshot, "@") {
		snapshot = "@" + snapshot
	}
	if err := validateVMRestoreSnapshot(snapshot, jobID); err != nil {
		return err
	}
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

	strictAsNew := jobID == nil
	var oobDestination *oobGuestRestoreDestination
	if strictAsNew {
		var err error
		oobDestination, err = resolveOOBGuestRestoreDestination(
			target.BackupRoot,
			remoteDataset,
			destinationDataset,
		)
		if err != nil {
			return err
		}
		if oobDestination == nil || oobDestination.Kind != clusterModels.BackupJobModeVM {
			return fmt.Errorf("restore_guest_destination_kind_mismatch")
		}
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
		SourceDataset:  remoteDataset + snapshot,
		TargetEndpoint: destinationDataset,
		StartedAt:      time.Now().UTC(),
	}
	if err := s.DB.Create(&event).Error; err != nil {
		return fmt.Errorf("create_restore_event_failed: %w", err)
	}
	stopHeartbeat := s.startBackupEventHeartbeat(ctx, event.ID, time.Minute)
	defer func() {
		stopHeartbeat()
		output := ""
		if current, err := s.GetLocalBackupEvent(event.ID); err == nil && current != nil {
			output = current.Output
		}
		s.finalizeRestoreEvent(&event, retErr, output)

		if s.TelemetryDB != nil {
			auditStatus := "success"
			errMsg := ""
			if retErr != nil {
				auditStatus = "failed"
				errMsg = retErr.Error()
			}
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_target_restore", target.ID, auditStatus, errMsg, map[string]any{
				"eventId": event.ID,
				"status":  auditStatus,
				"error":   errMsg,
			})
		}
	}()
	defer recoverOperationPanic("restore_from_target_vm", &retErr)

	restoreNetwork := true
	if payload.RestoreNetwork != nil {
		restoreNetwork = *payload.RestoreNetwork
	}

	_, remoteRID := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset))
	if remoteRID == 0 {
		return fmt.Errorf("vm_rid_missing")
	}

	destKind, destRID := inferRestoreDatasetKind(destinationDataset)
	if strictAsNew {
		destRID = oobDestination.GuestID
	} else if destKind != clusterModels.BackupJobModeVM || destRID == 0 {
		destRID = remoteRID
	}

	vmRoots, err := s.listRemoteVMRepresentativeRoots(ctx, target, remoteRID, remoteDataset)
	if err != nil {
		return fmt.Errorf("list_remote_vm_roots_failed: %w", err)
	}
	if len(vmRoots) == 0 {
		return fmt.Errorf("remote_vm_roots_not_found")
	}
	primaryRemoteRoot := ""
	if strictAsNew {
		primaryRemoteRoot = selectPrimaryRemoteVMRoot(
			target.BackupRoot,
			remoteDataset,
			vmRoots,
			remoteRID,
		)
	}

	primaryDestination := destinationDataset
	if kind, _ := inferRestoreDatasetKind(primaryDestination); kind != clusterModels.BackupJobModeVM {
		primaryDestination = destinationVMRootFromRemoteRoot(target.BackupRoot, vmRoots[0], primaryDestination, destRID)
	}
	if primaryDestination == "" {
		return fmt.Errorf("destination_dataset_required")
	}
	destinationForRemoteRoot := func(remoteRoot string) string {
		if strictAsNew {
			return destinationVMRootForRestore(
				target.BackupRoot,
				remoteRoot,
				primaryRemoteRoot,
				primaryDestination,
				destRID,
			)
		}
		return destinationVMRootFromRemoteRoot(target.BackupRoot, remoteRoot, primaryDestination, destRID)
	}

	rootPlans, err := buildVMRestoreRootPlans(vmRoots, destinationForRemoteRoot)
	if err != nil {
		return err
	}
	if len(rootPlans) == 0 {
		return fmt.Errorf("remote_vm_roots_not_found")
	}

	candidateDestinations := make([]string, 0, len(rootPlans))
	for _, plan := range rootPlans {
		candidateDestinations = append(candidateDestinations, plan.destination)
	}

	if err := s.validateDestinationPoolsExist(ctx, candidateDestinations); err != nil {
		return err
	}
	locked, holder, additionalDestinationRoots := s.acquireDatasetOperationsWhileHolding(
		payload.DestinationDataset,
		candidateDestinations,
	)
	if !locked {
		return fmt.Errorf(
			"restore_destination_already_running: dataset=%s holder=%s",
			strings.Join(normalizeDatasetOperationRoots(candidateDestinations), ","),
			holder,
		)
	}
	defer s.releaseDatasetOperations(additionalDestinationRoots)
	if strictAsNew {
		if err := s.requireOOBGuestRestoreAvailable(ctx, oobDestination, candidateDestinations, true); err != nil {
			return err
		}
	}
	resolvedRemoteRoots := make(map[string]string, len(rootPlans))
	generationSelections := make([]restoreTargetGenerationSelection, 0, len(rootPlans))
	var committedMetadata *backupCommitMetadata
	committedEntries := make([]backupManifestEntry, 0)
	for _, plan := range rootPlans {
		resolved, err := s.resolveRemoteDatasetForSnapshot(ctx, target, plan.remote, snapshot)
		if err != nil {
			return fmt.Errorf("resolve_restore_snapshot_dataset_failed: dataset=%s: %w", plan.remote, err)
		}
		metadata, err := s.requireRemoteBackupRestoreCommitBySnapshot(ctx, target, resolved, snapshot)
		if err != nil {
			return fmt.Errorf("restore_vm_root_commit_invalid: dataset=%s: %w", plan.remote, err)
		}
		if metadata.Version == backupCommitVersion {
			if jobID != nil && *jobID > 0 && metadata.JobID != *jobID {
				return fmt.Errorf("restore_vm_root_commit_job_mismatch: dataset=%s", plan.remote)
			}
			if committedMetadata == nil {
				copy := metadata
				committedMetadata = &copy
			} else if !backupCommitMetadataEquivalent(*committedMetadata, metadata) {
				return fmt.Errorf("restore_vm_root_commit_mismatch: dataset=%s", plan.remote)
			}
			canonicalRoot := canonicalRemoteVMRoot(target.BackupRoot, plan.remote, remoteRID)
			if canonicalRoot == "" {
				return fmt.Errorf("restore_vm_root_commit_mapping_invalid: dataset=%s", plan.remote)
			}
			part, err := s.remoteBackupManifestEntries(
				ctx,
				target,
				resolved,
				canonicalRoot,
				snapshot,
				metadata.Recursive,
			)
			if err != nil {
				return fmt.Errorf("restore_vm_root_manifest_read_failed: dataset=%s: %w", plan.remote, err)
			}
			committedEntries = append(committedEntries, part...)
		}
		if _, err := s.recursiveRestoreManifestRemote(ctx, target, resolved, snapshot); err != nil {
			return fmt.Errorf("restore_preflight_recursive_snapshot_failed: dataset=%s: %w", plan.remote, err)
		}
		resolvedRemoteRoots[plan.remote] = resolved
		generationSelections = append(generationSelections, restoreTargetGenerationSelection{
			ActiveDataset:   plan.remote,
			SelectedDataset: resolved,
		})

	}
	if committedMetadata != nil {
		if len(committedMetadata.Roots) != len(rootPlans) {
			return fmt.Errorf(
				"restore_vm_root_count_mismatch: committed=%d discovered=%d",
				len(committedMetadata.Roots),
				len(rootPlans),
			)
		}
		manifest, err := buildBackupManifest(
			committedMetadata.JobID,
			snapshot,
			committedMetadata.Recursive,
			committedEntries,
		)
		if err != nil {
			return fmt.Errorf("restore_vm_manifest_invalid: %w", err)
		}
		if len(manifest.Entries) != committedMetadata.EntryCount ||
			backupManifestHash(manifest) != committedMetadata.ManifestHash {
			return fmt.Errorf("restore_vm_manifest_mismatch")
		}
	}
	vmRestoreFence, err := s.acquireVMRestoreFence(ctx, destRID, event.ID)
	if err != nil {
		return fmt.Errorf("acquire_vm_restore_fence_failed: %w", err)
	}
	defer func() {
		if vmRestoreFence == nil || retErr == nil {
			return
		}
		if releaseErr := vmRestoreFence.release(); releaseErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release_vm_restore_fence_failed: %w", releaseErr))
			return
		}
		if vmRestoreFence.wasRunning {
			if restartErr := s.startVMIfPresent(vmRestoreFence.guestID); restartErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("restore_vm_restart_failed: %w", restartErr))
			}
		}
	}()
	for _, plan := range rootPlans {
		identity := newRestoreStagingIdentity(jobID, target.ID, plan.destination)
		if err := s.prepareRestoreStagingDataset(ctx, plan.destination+".restoring", identity); err != nil {
			return fmt.Errorf("restore_preflight_staging_check_failed: %w", err)
		}
	}

	existingVM, err := s.findVMByRID(destRID)
	if err != nil {
		return fmt.Errorf("failed_to_lookup_existing_vm_before_restore: %w", err)
	}
	if existingVM != nil {
		if strictAsNew {
			return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=vm", destRID)
		}
		vmRuntimeGuard, err := s.prepareInPlaceVMRestore(destRID, primaryDestination)
		if err != nil {
			return err
		}
		// The VM is deliberately left stopped after a successful restore. If
		// anything after this point fails (including another root, metadata
		// reconciliation, or remote generation activation), put a VM stopped by
		// this attempt back into its original running state. This defer runs
		// before the event finalizer above, so a restart failure is recorded too.
		defer func() {
			retErr, _ = vmRuntimeGuard.restoreAfterFailure(retErr)
		}()
	}

	appliedBackups := make([]restoredDatasetBackup, 0, len(rootPlans))
	rollbackAppliedBackups := func() error {
		return s.rollbackRestoredDatasetBackups(appliedBackups)
	}

	for _, plan := range rootPlans {
		runPayload := payload
		runPayload.RemoteDataset = plan.remote
		runPayload.DestinationDataset = plan.destination
		disableNetworkRestore := false
		runPayload.RestoreNetwork = &disableNetworkRestore

		backupDataset, err := s.runRestoreFromTargetSingleDataset(
			ctx,
			target,
			runPayload,
			jobID,
			false,
			true,
			false,
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
			destination: plan.destination,
			backup:      backupDataset,
		})
	}

	if strictAsNew {
		if err := s.requireOOBGuestRestoreAvailable(ctx, oobDestination, nil, false); err != nil {
			rollbackErr := rollbackAppliedBackups()
			if rollbackErr != nil {
				return fmt.Errorf("restore_guest_final_identity_check_failed: %w; rollback_failed: %v", err, rollbackErr)
			}
			return err
		}
	}

	reconcileErr := error(nil)
	if strictAsNew {
		sourcePrimaryRoot := canonicalRemoteVMRoot(
			target.BackupRoot,
			primaryRemoteRoot,
			remoteRID,
		)
		reconcileErr = s.reconcileRestoredVMFromDatasetAsNew(
			ctx,
			primaryDestination,
			sourcePrimaryRoot,
			restoreNetwork,
		)
	} else {
		reconcileErr = s.reconcileRestoredVMFromDatasetWithOptions(ctx, primaryDestination, restoreNetwork)
	}
	if reconcileErr != nil {
		rollbackErr := rollbackAppliedBackups()
		if rollbackErr != nil {
			return fmt.Errorf("reconcile_restored_vm_failed: %w; rollback_failed: %v", reconcileErr, rollbackErr)
		}
		return fmt.Errorf("reconcile_restored_vm_failed: %w", reconcileErr)
	}

	if jobID != nil && *jobID > 0 {
		// Remote generation activation is deliberately deferred until every local
		// root and VM metadata have activated successfully. The helper rolls back
		// any earlier remote swaps if a later swap fails.
		for idx := range generationSelections {
			generationSelections[idx].SelectedDataset = resolvedRemoteRoots[generationSelections[idx].ActiveDataset]
		}
		if _, err := s.activateTargetGenerationsForRestore(ctx, target, generationSelections); err != nil {
			rollbackErr := rollbackAppliedBackups()
			if rollbackErr != nil {
				return fmt.Errorf("%w; rollback_failed: %v", err, rollbackErr)
			}
			return err
		}
	}

	for _, entry := range appliedBackups {
		if strings.TrimSpace(entry.backup) == "" {
			continue
		}
		if err := s.cleanupRestoreBackupDataset(ctx, entry.backup); err != nil {
			warning := fmt.Sprintf(
				"restore_backup_cleanup_pending: dataset=%s retained=true error=%v",
				entry.backup,
				err,
			)
			if appendErr := s.AppendBackupEventOutput(event.ID, warning); appendErr != nil {
				logger.L.Warn().
					Err(appendErr).
					Uint("event_id", event.ID).
					Msg("append_restore_backup_cleanup_warning_failed")
			}
			logger.L.Warn().
				Err(err).
				Str("backup_dataset", entry.backup).
				Msg("failed_to_cleanup_vm_restore_backup_dataset")
		}
	}
	if vmRestoreFence != nil {
		if releaseErr := vmRestoreFence.release(); releaseErr != nil {
			return fmt.Errorf("release_vm_restore_fence_failed: %w", releaseErr)
		}
		vmRestoreFence = nil
	}

	return nil
}

func validateVMRestoreSnapshot(snapshot string, jobID *uint) error {
	snapshot = strings.TrimPrefix(strings.TrimSpace(snapshot), "@")
	if snapshot == "" {
		return fmt.Errorf("restore_vm_committed_snapshot_required")
	}
	snapshotJobID, commitRequired, err := backupCommitJobIDFromSnapshot(snapshot)
	if err != nil {
		return fmt.Errorf("restore_vm_snapshot_commit_invalid: %w", err)
	}
	if commitRequired {
		if jobID != nil && (*jobID == 0 || snapshotJobID != *jobID) {
			return fmt.Errorf("restore_vm_snapshot_job_mismatch")
		}
		return nil
	}
	if jobID == nil || *jobID == 0 {
		return fmt.Errorf("restore_vm_legacy_snapshot_unsupported")
	}
	if !strings.HasPrefix(snapshot, backupSnapshotPrefixForJob(*jobID)+"_") {
		return fmt.Errorf("restore_vm_snapshot_job_mismatch")
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
	activateRemoteGeneration bool,
	sharedEventID *uint,
) (backupResult string, retErr error) {
	remoteDataset := strings.TrimSpace(payload.RemoteDataset)
	preferredRemoteDataset := remoteDataset
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
	commitMetadata, err := s.requireRemoteBackupRestoreCommitBySnapshot(ctx, target, remoteDataset, snapshot)
	if err != nil {
		return "", err
	}
	if commitMetadata.Version == backupCommitVersion && len(commitMetadata.Roots) == 1 {
		entries, err := s.remoteBackupManifestEntries(
			ctx,
			target,
			remoteDataset,
			commitMetadata.Roots[0],
			snapshot,
			commitMetadata.Recursive,
		)
		if err != nil {
			return "", fmt.Errorf("restore_backup_manifest_read_failed: %w", err)
		}
		manifest, err := buildBackupManifest(
			commitMetadata.JobID,
			snapshot,
			commitMetadata.Recursive,
			entries,
		)
		if err != nil {
			return "", fmt.Errorf("restore_backup_manifest_invalid: %w", err)
		}
		if len(manifest.Entries) != commitMetadata.EntryCount ||
			backupManifestHash(manifest) != commitMetadata.ManifestHash {
			return "", fmt.Errorf("restore_backup_manifest_mismatch")
		}
	}
	expectedManifest, err := s.recursiveRestoreManifestRemote(ctx, target, remoteDataset, snapshot)
	if err != nil {
		return "", fmt.Errorf("restore_preflight_recursive_snapshot_failed: %w", err)
	}

	remoteEndpoint := target.SSHHost + ":" + remoteDataset + snapshot
	restorePath := destinationDataset + ".restoring"
	stagingIdentity := newRestoreStagingIdentity(jobID, target.ID, destinationDataset)
	destinationKind, destinationGuestID := inferRestoreDatasetKind(destinationDataset)

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
			SourceDataset:  remoteDataset + snapshot,
			TargetEndpoint: destinationDataset,
			StartedAt:      time.Now().UTC(),
		}
		if err := s.DB.Create(&event).Error; err != nil {
			return "", fmt.Errorf("create_restore_event_failed: %w", err)
		}
		activeEventID = event.ID
		ownsEvent = true
	}
	stopHeartbeat := func() {}
	if ownsEvent {
		stopHeartbeat = s.startBackupEventHeartbeat(ctx, activeEventID, time.Minute)
		defer stopHeartbeat()
	}

	var restoreErr error
	var output string

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
	recordRestoreFailure := func(err error) {
		if ownsEvent {
			s.finalizeRestoreEvent(&event, err, output)
			return
		}
		appendEventOutput(fmt.Sprintf(
			"vm_dataset_restore_failed: %s -> %s: %v",
			remoteEndpoint,
			destinationDataset,
			err,
		))
	}
	var jailRestoreFence *restoreGuestFence
	jailSafeToRestart := true
	if destinationKind == clusterModels.BackupJobModeJail {
		jailRestoreFence, err = s.acquireJailRestoreFence(ctx, destinationDataset, activeEventID)
		if err != nil {
			restoreErr = fmt.Errorf("acquire_jail_restore_fence_failed: %w", err)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
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
		restoreErr = fmt.Errorf("restore_preflight_staging_check_failed: %w", err)
		recordRestoreFailure(restoreErr)
		return "", restoreErr
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

	extraEnv := s.buildZeltaEnv(target)
	receiveTopOptions, err := stagingIdentity.receiveTopOptions()
	if err != nil {
		restoreErr = err
		recordRestoreFailure(restoreErr)
		return "", restoreErr
	}
	extraEnv = setEnvValue(extraEnv, "ZELTA_RECV_TOP", receiveTopOptions)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")
	output, restoreErr = runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		func(line string) {
			appendEventOutput(line)
		},
		"backup",
		"--json",
		"--no-snapshot",
		remoteEndpoint,
		restorePath,
	)
	if restoreErr != nil {
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
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
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}
	if err := s.verifyRecursiveRestoreManifest(ctx, restorePath, snapshot, expectedManifest); err != nil {
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, err)
		recordRestoreFailure(restoreErr)
		return "", restoreErr
	}

	isJailDestination := destinationKind == clusterModels.BackupJobModeJail
	isGuestDestination := isJailDestination || destinationKind == clusterModels.BackupJobModeVM
	if destinationKind == clusterModels.BackupJobModeDataset {
		if err := s.requireNoManagedGuestsWithinRestore(ctx, destinationDataset); err != nil {
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, err)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
	}
	strictAsNew := jobID == nil && isGuestDestination
	var oobDestination *oobGuestRestoreDestination
	if strictAsNew {
		oobDestination = &oobGuestRestoreDestination{
			Kind:    destinationKind,
			GuestID: destinationGuestID,
			Dataset: destinationDataset,
		}
		if err := s.requireOOBGuestRestoreAvailable(ctx, oobDestination, nil, true); err != nil {
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, err)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
	}

	destExists, existErr := s.localDatasetExists(ctx, destinationDataset)
	if existErr != nil {
		restoreErr = fmt.Errorf("failed_to_check_destination_dataset_before_restore: %w", existErr)
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		if ownsEvent {
			s.finalizeRestoreEvent(&event, restoreErr, output)
		} else {
			appendEventOutput(fmt.Sprintf("vm_dataset_restore_failed: %s -> %s: %v", remoteEndpoint, destinationDataset, restoreErr))
		}
		return "", restoreErr
	}
	if destExists {
		if strictAsNew {
			restoreErr = fmt.Errorf(
				"restore_destination_guest_dataset_exists: guest_id=%d dataset=%s",
				destinationGuestID,
				destinationDataset,
			)
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
	}

	if idx := strings.LastIndex(destinationDataset, "/"); idx > 0 {
		parent := destinationDataset[:idx]
		_ = s.ensureLocalFilesystemPath(ctx, parent)
	}

	backupDataset := ""
	var renameErr error
	if strictAsNew {
		renameErr = s.promoteRestoredDatasetAsNew(ctx, restorePath, destinationDataset)
	} else if isJailDestination && destExists {
		jailRuntimeGuard, quiesceErr := s.prepareInPlaceJailRestore(ctx, destinationDataset)
		if quiesceErr != nil {
			restoreErr = s.cleanupOwnedRestoreStagingAfterError(
				restorePath,
				stagingIdentity,
				quiesceErr,
			)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
		// Keep the guard alive for the whole post-stop transaction, not only the
		// ZFS rename. A later property, reconciliation, or generation-activation
		// failure must restart a jail that this restore stopped. Successful
		// restores intentionally leave it stopped.
		defer func() {
			var restartErr error
			retErr, restartErr = jailRuntimeGuard.restoreAfterFailure(retErr)
			if restartErr != nil {
				jailSafeToRestart = false
				recordRestoreFailure(retErr)
			}
		}()
		backupDataset, renameErr = s.promoteRestoredDataset(ctx, restorePath, destinationDataset)
		if renameErr != nil {
			renameErr = fmt.Errorf(
				"rename_restore_failed: could not promote restored dataset into %s: %w",
				destinationDataset,
				renameErr,
			)
		}
	} else {
		backupDataset, renameErr = s.promoteRestoredDataset(ctx, restorePath, destinationDataset)
	}
	if renameErr != nil {
		if isJailDestination && destExists && !strictAsNew {
			restoreErr = renameErr
		} else {
			restoreErr = fmt.Errorf(
				"rename_restore_failed: could not promote %s → %s: %w",
				restorePath,
				destinationDataset,
				renameErr,
			)
		}
		restoreErr = s.cleanupOwnedRestoreStagingAfterError(restorePath, stagingIdentity, restoreErr)
		recordRestoreFailure(restoreErr)
		return "", restoreErr
	}
	if err := s.clearRestoreStagingProperties(ctx, destinationDataset, stagingIdentity); err != nil {
		restoreErr = s.rollbackRestorePromotionAfterError(
			destinationDataset,
			backupDataset,
			destExists,
			fmt.Errorf("restore_activation_failed: %w", err),
		)
		recordRestoreFailure(restoreErr)
		return "", restoreErr
	}

	if err := s.fixRestoredProperties(ctx, destinationDataset); err != nil {
		restoreErr = s.rollbackRestorePromotionAfterError(
			destinationDataset,
			backupDataset,
			destExists,
			fmt.Errorf("restore_activation_failed: %w", err),
		)
		recordRestoreFailure(restoreErr)
		return "", restoreErr
	}

	if reconcileJail {
		restoreNetwork := true
		if payload.RestoreNetwork != nil {
			restoreNetwork = *payload.RestoreNetwork
		}
		var reconcileErr error
		if strictAsNew {
			reconcileErr = s.reconcileRestoredJailFromDatasetAsNew(ctx, destinationDataset, restoreNetwork)
		} else {
			reconcileErr = s.reconcileRestoredJailFromDatasetWithOptions(ctx, destinationDataset, restoreNetwork)
		}
		if reconcileErr != nil {
			if strictAsNew {
				restoreErr = s.rollbackRestorePromotionAfterError(
					destinationDataset,
					backupDataset,
					destExists,
					fmt.Errorf("reconcile_restored_jail_failed: %w", reconcileErr),
				)
				recordRestoreFailure(restoreErr)
				return "", restoreErr
			}
			output += "\n" + fmt.Sprintf("jail_metadata_reconcile_failed: %v", reconcileErr)
			logger.L.Warn().
				Err(reconcileErr).
				Str("destination_dataset", destinationDataset).
				Msg("restore_jail_metadata_reconcile_failed_data_intact")
		}
	}

	if activateRemoteGeneration {
		activationActive := preferredRemoteDataset
		activationSelected := remoteDataset
		if datasetWithinRoot(activationActive, activationSelected) && activationActive != activationSelected {
			// Snapshot resolution can land on a child dataset (for example a VM zvol under the VM root).
			// Generation activation is only defined for the lineage root dataset, so keep it anchored there.
			activationSelected = activationActive
		}
		if _, err := s.activateTargetGenerationsForRestore(
			ctx,
			target,
			[]restoreTargetGenerationSelection{{
				ActiveDataset:   activationActive,
				SelectedDataset: activationSelected,
			}},
		); err != nil {
			restoreErr = s.rollbackRestorePromotionAfterError(
				destinationDataset,
				backupDataset,
				destExists,
				err,
			)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
	}

	if !keepBackup && strings.TrimSpace(backupDataset) != "" {
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
			appendEventOutput(warning)
			logger.L.Warn().
				Err(cleanupErr).
				Str("backup_dataset", backupDataset).
				Msg("failed_to_cleanup_restore_backup_dataset")
		} else {
			backupDataset = ""
		}
	}
	if jailRestoreFence != nil {
		if releaseErr := jailRestoreFence.release(); releaseErr != nil {
			restoreErr = fmt.Errorf("release_jail_restore_fence_failed: %w", releaseErr)
			recordRestoreFailure(restoreErr)
			return "", restoreErr
		}
		jailRestoreFence = nil
	}

	if ownsEvent {
		s.finalizeRestoreEvent(&event, nil, output)

		if s.TelemetryDB != nil {
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_target_restore", target.ID, "success", "", map[string]any{
				"eventId": event.ID,
				"status":  "success",
			})
		}
	} else {
		appendEventOutput(fmt.Sprintf("vm_dataset_restore_complete: %s -> %s", remoteEndpoint, destinationDataset))
	}

	logger.L.Info().
		Uint("target_id", target.ID).
		Str("snapshot", snapshot).
		Str("dataset", destinationDataset).
		Msg("target_dataset_restore_completed")

	return backupDataset, nil
}

func (s *Service) listRemoteVMRepresentativeRoots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	vmRID uint,
	remoteDataset string,
) ([]string, error) {
	if vmRID == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	output, err := s.runTargetZFSList(ctx, target, "-t", "filesystem", "-r", "-Hp", "-o", "name", target.BackupRoot)
	if err != nil {
		return nil, err
	}
	backupRoot := normalizeDatasetPath(target.BackupRoot)
	remoteDataset = normalizeDatasetPath(remoteDataset)
	if !datasetWithinRoot(backupRoot, remoteDataset) {
		return nil, fmt.Errorf("remote_dataset_outside_backup_root")
	}

	selectedSuffix := relativeDatasetSuffix(backupRoot, remoteDataset)
	_, _, selectedBase := classifyDatasetLineage(selectedSuffix)
	selectedVMRoot := vmDatasetRoot(selectedBase)
	jobLineage := vmJobLineageTail(selectedBase)
	if selectedVMRoot == "" || jobLineage == "" ||
		selectedBase != normalizeDatasetPath(selectedVMRoot+"/"+jobLineage) {
		return nil, fmt.Errorf("remote_vm_job_lineage_required")
	}

	bestByBase := make(map[string]string)
	bestRank := make(map[string]int)

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		dataset := normalizeDatasetPath(line)
		if dataset == "" || !datasetWithinRoot(backupRoot, dataset) {
			continue
		}

		suffix := relativeDatasetSuffix(backupRoot, dataset)
		lineage, _, baseSuffix := classifyDatasetLineage(suffix)
		baseRoot := vmDatasetRoot(baseSuffix)
		kind, rid := inferRestoreDatasetKind(baseRoot)
		if kind != clusterModels.BackupJobModeVM || rid != vmRID {
			continue
		}
		if baseSuffix != normalizeDatasetPath(baseRoot+"/"+jobLineage) {
			continue
		}

		baseKey := canonicalVMDatasetRoot(baseRoot, vmRID)
		if baseKey == "" {
			continue
		}
		rank := datasetLineageRank(lineage)

		if existing, ok := bestByBase[baseKey]; ok {
			if rank > bestRank[baseKey] {
				continue
			}
			if rank == bestRank[baseKey] && existing <= dataset {
				continue
			}
		}

		bestByBase[baseKey] = dataset
		bestRank[baseKey] = rank
	}

	results := make([]string, 0, len(bestByBase))
	for _, dataset := range bestByBase {
		results = append(results, dataset)
	}
	sort.Strings(results)

	return results, nil
}

func destinationVMRootFromRemoteRoot(backupRoot, remoteRoot, destinationDataset string, vmRID uint) string {
	suffix := canonicalVMDatasetRoot(vmDatasetRoot(relativeDatasetSuffix(backupRoot, remoteRoot)), vmRID)
	suffix = normalizeRestoreDestinationDataset(suffix)
	if suffix == "" {
		return ""
	}

	if strings.HasPrefix(suffix, "virtual-machines/") {
		anchor := vmDestinationAnchor(destinationDataset)
		if anchor == "" {
			return suffix
		}
		return normalizeRestoreDestinationDataset(anchor + "/" + suffix)
	}

	return suffix
}

func destinationVMRootForRestore(
	backupRoot, remoteRoot, primaryRemoteRoot, destinationDataset string,
	vmRID uint,
) string {
	if normalizeDatasetPath(remoteRoot) == normalizeDatasetPath(primaryRemoteRoot) {
		return normalizeRestoreDestinationDataset(destinationDataset)
	}
	return destinationVMRootFromRemoteRoot(backupRoot, remoteRoot, destinationDataset, vmRID)
}

type vmRestoreRootPlan struct {
	remote      string
	destination string
}

func buildVMRestoreRootPlans(
	remoteRoots []string,
	destinationForRemoteRoot func(string) string,
) ([]vmRestoreRootPlan, error) {
	if destinationForRemoteRoot == nil {
		return nil, fmt.Errorf("vm_restore_destination_mapper_required")
	}

	plans := make([]vmRestoreRootPlan, 0, len(remoteRoots))
	remoteByDestination := make(map[string]string, len(remoteRoots))
	for _, remoteRoot := range remoteRoots {
		localRoot := destinationForRemoteRoot(remoteRoot)
		if localRoot == "" {
			continue
		}

		normalizedRemote := normalizeDatasetPath(remoteRoot)
		normalizedDestination := normalizeRestoreDestinationDataset(localRoot)
		if previousRemote, ok := remoteByDestination[normalizedDestination]; ok {
			if previousRemote != normalizedRemote {
				return nil, fmt.Errorf(
					"restore_vm_destination_root_collision: destination=%s remote_roots=%s,%s",
					normalizedDestination,
					previousRemote,
					normalizedRemote,
				)
			}
			continue
		}

		remoteByDestination[normalizedDestination] = normalizedRemote
		plans = append(plans, vmRestoreRootPlan{
			remote:      remoteRoot,
			destination: normalizedDestination,
		})
	}

	return plans, nil
}

func selectPrimaryRemoteVMRoot(
	backupRoot, selectedRemoteDataset string,
	remoteRoots []string,
	vmRID uint,
) string {
	if len(remoteRoots) == 0 {
		return ""
	}

	selectedKey := canonicalRemoteVMRoot(backupRoot, selectedRemoteDataset, vmRID)
	for _, remoteRoot := range remoteRoots {
		if selectedKey != "" && canonicalRemoteVMRoot(backupRoot, remoteRoot, vmRID) == selectedKey {
			return remoteRoot
		}
	}

	return remoteRoots[0]
}

func canonicalRemoteVMRoot(backupRoot, remoteDataset string, vmRID uint) string {
	suffix := vmDatasetRoot(relativeDatasetSuffix(backupRoot, remoteDataset))
	_, _, baseSuffix := classifyDatasetLineage(suffix)
	if baseSuffix != "" {
		suffix = baseSuffix
	}
	return canonicalVMDatasetRoot(suffix, vmRID)
}

func vmDestinationAnchor(dataset string) string {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return ""
	}

	parts := strings.Split(dataset, "/")
	for idx := 0; idx < len(parts); idx++ {
		if parts[idx] != "virtual-machines" {
			continue
		}
		if idx == 0 {
			return ""
		}
		return strings.Join(parts[:idx], "/")
	}

	if len(parts) > 1 {
		return dataset
	}

	return ""
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

func (s *Service) remoteDatasetChildCount(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) int {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost,
		"zfs", "list", "-t", "filesystem,volume", "-r", "-d", "1",
		"-Hp", "-o", "name", remoteDataset,
	)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return 0
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	// First line is the parent dataset; remaining are direct children.
	if len(lines) <= 1 {
		return 0
	}
	return len(lines) - 1
}

func (s *Service) listRemoteSnapshotsForDataset(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	return s.listRemoteSnapshots(ctx, target, remoteDataset, false)
}

func (s *Service) listRemoteSnapshotsForDatasetRecursive(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	return s.listRemoteSnapshots(ctx, target, remoteDataset, true)
}

func (s *Service) listRemoteSnapshots(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string, recursive bool) ([]SnapshotInfo, error) {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list", "-t", "snapshot", "-Hp")
	if recursive {
		sshArgs = append(sshArgs, "-r")
	}
	sshArgs = append(sshArgs,
		"-o", "name,creation,used,refer,guid,encryption",
		"-s", "creation",
		remoteDataset,
	)

	logger.L.Debug().
		Str("ssh_host", target.SSHHost).
		Str("ssh_key_path", target.SSHKeyPath).
		Str("remote_dataset", remoteDataset).
		Bool("recursive", recursive).
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

func filterSnapshotsForBackupJob(snapshots []SnapshotInfo, jobID uint) []SnapshotInfo {
	if len(snapshots) == 0 {
		return snapshots
	}

	jobPrefix := backupSnapshotPrefixForJob(jobID)
	jobScoped := make([]SnapshotInfo, 0, len(snapshots))

	for _, snapshot := range snapshots {
		shortName := strings.TrimPrefix(snapshotShortName(snapshot), "@")
		if strings.HasPrefix(shortName, jobPrefix+"_") {
			jobScoped = append(jobScoped, snapshot)
		}
	}

	return jobScoped
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

	return strings.HasPrefix(snapshotName, "bk_")
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
		case strings.HasPrefix(suffix, baseLeaf+"_gen-"):
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

		fields := strings.Split(line, "\t")
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

		guid := ""
		if len(fields) >= 5 {
			guid = strings.TrimSpace(fields[4])
		}
		encrypted := false
		if len(fields) >= 6 {
			encryption := strings.ToLower(strings.TrimSpace(fields[5]))
			encrypted = encryption != "" && encryption != "-" && encryption != "none" && encryption != "off"
		}

		snapshots = append(snapshots, SnapshotInfo{
			Name:      fullName,
			ShortName: shortName,
			Dataset:   datasetName,
			Encrypted: encrypted,
			Creation:  creation,
			Used:      fields[2],
			Refer:     fields[3],
			Guid:      guid,
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
	// OpenSSH concatenates every argument after the host into one remote shell
	// command. Transport scripts as base64 through a syntax shared by POSIX and
	// csh-family login shells, then execute the decoded script under /bin/sh.
	// Direct POSIX quoting breaks on FreeBSD's stock root csh login shell.
	if len(args) == 3 && args[0] == "sh" && args[1] == "-c" {
		args = []string{remotePOSIXShellCommand(args[2])}
	}
	sshArgs = append(sshArgs, args...)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return output, nil
}

func remotePOSIXShellCommand(script string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	return "/usr/bin/printf %s " + encoded + " | /usr/bin/base64 -d | /bin/sh"
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

	if idx := strings.Index(leaf, "_gen-"); idx > 0 {
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
	default:
		return 2
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

func (s *Service) acquireRestoreDestination(dataset string) (bool, string) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return false, ""
	}

	s.restoreDestinationMu.Lock()
	defer s.restoreDestinationMu.Unlock()
	if s.runningRestoreDestination == nil {
		s.runningRestoreDestination = make(map[string]struct{})
	}
	for existing := range s.runningRestoreDestination {
		if datasetWithinRoot(existing, dataset) || datasetWithinRoot(dataset, existing) {
			return false, existing
		}
	}
	s.runningRestoreDestination[dataset] = struct{}{}
	return true, ""
}

func restoreWorkloadIdentityForDataset(dataset string) (string, uint) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	kind, guestID := inferRestoreDatasetKind(dataset)
	if guestID > 0 && (kind == clusterModels.BackupJobModeJail || kind == clusterModels.BackupJobModeVM) {
		return kind, guestID
	}
	return clusterModels.BackupJobModeDataset, datasetHash(dataset)
}

func (s *Service) releaseRestoreDestination(dataset string) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return
	}

	s.restoreDestinationMu.Lock()
	defer s.restoreDestinationMu.Unlock()
	delete(s.runningRestoreDestination, dataset)
}
