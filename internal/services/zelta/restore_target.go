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
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"gorm.io/gorm"
)

const restoreFromTargetQueueName = "zelta-restore-from-target-run"

type restoreFromTargetPayload struct {
	TargetID           uint   `json:"target_id"`
	RemoteDataset      string `json:"remote_dataset"`
	Snapshot           string `json:"snapshot"`
	DestinationDataset string `json:"destination_dataset"`
	RestoreNetwork     *bool  `json:"restore_network,omitempty"`
	OperationToken     string `json:"operation_token,omitempty"`
	HolderNodeID       string `json:"holder_node_id,omitempty"`
	EventID            uint   `json:"event_id,omitempty"`
	AuditRecordID      uint   `json:"audit_record_id,omitempty"`
}

type BackupTargetDatasetInfo struct {
	Name               string `json:"name"` // full remote dataset path
	Encrypted          bool   `json:"encrypted"`
	Suffix             string `json:"suffix"`
	BaseSuffix         string `json:"baseSuffix"`
	Lineage            string `json:"lineage"` // "active" | "rotated" | "other"
	OutOfBand          bool   `json:"outOfBand"`
	SnapshotCount      int    `json:"snapshotCount"`
	SnapshotCountKnown bool   `json:"snapshotCountKnown"`
	Kind               string `json:"kind"` // "dataset" | "jail" | "vm"
	JailCTID           uint   `json:"jailCtId,omitempty"`
	VMRID              uint   `json:"vmRid,omitempty"`
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
		// Everything under <pool>/sylve belongs to a subsystem with its own
		// records, which a plain dataset restore can't recreate.
		if parts := strings.Split(destinationDataset, "/"); len(parts) > 1 && parts[1] == "sylve" {
			return nil, fmt.Errorf("restore_destination_sylve_managed: destination=%s", destinationDataset)
		}
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
	if _, reserved := restoreGuestIdentityReservationFromContext(
		ctx,
		destination.Kind,
		destination.GuestID,
	); !reserved {
		if s == nil {
			return fmt.Errorf("guest_identity_inventory_scan_failed: cluster_service_not_initialized")
		}
		if err := s.Cluster.RequireGuestIDAvailable(ctx, destination.GuestID); err != nil {
			return err
		}
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
	started := time.Now()
	target, _, releaseTargetKey, err := s.getRestoreTargetWithKey(targetID)
	if err != nil {
		return nil, err
	}
	defer releaseTargetKey()

	fsOutput, err := s.runTargetZFSList(ctx, &target, "-t", "filesystem", "-r", "-Hp", "-o", "name,encryption", target.BackupRoot)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_target_datasets: %w", err)
	}

	markerOutput, err := s.runTargetSSH(
		ctx,
		&target,
		"zfs", "get", "-H", "-p", "-r", "-t", "filesystem", "-s", "local", "-o", "name,value",
		"sylve:backup_job_id", target.BackupRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_target_dataset_markers: %w", err)
	}

	datasets := buildBackupTargetDatasetInfos(target.BackupRoot, fsOutput, markerOutput)
	logger.L.Debug().
		Uint("target_id", targetID).
		Int("dataset_count", len(datasets)).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("backup_target_datasets_discovered")
	return datasets, nil
}

func parseRemoteBackupDatasetMarkers(output string) map[string]struct{} {
	markers := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		jobID, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil || jobID == 0 {
			continue
		}
		dataset := normalizeDatasetPath(fields[0])
		if dataset != "" {
			markers[dataset] = struct{}{}
		}
	}
	return markers
}

func isConventionalBackupLineageDataset(suffix string) bool {
	parts := strings.Split(strings.Trim(normalizeDatasetPath(suffix), "/"), "/")
	if len(parts) < 2 {
		return false
	}
	parent := strings.ToLower(strings.TrimSpace(parts[len(parts)-2]))
	if !((strings.HasPrefix(parent, "j-") && len(parent) > 2) ||
		(strings.HasPrefix(parent, "job-") && len(parent) > 4)) {
		return false
	}
	leaf := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	return leaf == "active" || strings.HasPrefix(leaf, "active_gen-") || strings.HasPrefix(leaf, "active.pre_")
}

func backupDatasetExplicitRootAncestors(
	backupRoot string,
	explicitRoots map[string]struct{},
) map[string]struct{} {
	ancestors := make(map[string]struct{})
	for root := range explicitRoots {
		current := normalizeDatasetPath(root)
		for current != "" && current != backupRoot {
			slash := strings.LastIndex(current, "/")
			if slash < 0 {
				break
			}
			current = current[:slash]
			if current != backupRoot && datasetWithinRoot(backupRoot, current) {
				ancestors[current] = struct{}{}
			}
		}
	}
	return ancestors
}

func backupDatasetHasExplicitRootAncestor(
	dataset string,
	backupRoot string,
	explicitRoots map[string]struct{},
) bool {
	current := normalizeDatasetPath(dataset)
	for current != "" && current != backupRoot {
		slash := strings.LastIndex(current, "/")
		if slash < 0 {
			return false
		}
		current = current[:slash]
		if _, exists := explicitRoots[current]; exists {
			return true
		}
	}
	return false
}

func buildBackupTargetDatasetInfos(backupRoot, fsOutput, markerOutput string) []BackupTargetDatasetInfo {
	backupRoot = normalizeDatasetPath(backupRoot)
	type remoteDataset struct {
		name      string
		encrypted bool
	}
	entries := make([]remoteDataset, 0)
	known := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(fsOutput), "\n") {
		dataset, encrypted := parseRemoteDatasetEncryption(line)
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" || dataset == backupRoot || !datasetWithinRoot(backupRoot, dataset) {
			continue
		}
		if _, exists := known[dataset]; exists {
			continue
		}
		known[dataset] = struct{}{}
		entries = append(entries, remoteDataset{name: dataset, encrypted: encrypted})
	}

	explicitRoots := make(map[string]struct{})
	for dataset := range parseRemoteBackupDatasetMarkers(markerOutput) {
		if _, exists := known[dataset]; exists {
			explicitRoots[dataset] = struct{}{}
		}
	}
	for _, entry := range entries {
		if isConventionalBackupLineageDataset(relativeDatasetSuffix(backupRoot, entry.name)) {
			explicitRoots[entry.name] = struct{}{}
		}
	}

	managedAncestors := backupDatasetExplicitRootAncestors(backupRoot, explicitRoots)
	datasets := make([]BackupTargetDatasetInfo, 0, len(entries))
	for _, entry := range entries {
		_, explicit := explicitRoots[entry.name]
		if !explicit && len(explicitRoots) > 0 {
			_, managedAncestor := managedAncestors[entry.name]
			if managedAncestor || backupDatasetHasExplicitRootAncestor(entry.name, backupRoot, explicitRoots) {
				continue
			}
		}

		suffix := relativeDatasetSuffix(backupRoot, entry.name)
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
			Name:               entry.name,
			Encrypted:          entry.encrypted,
			Suffix:             suffix,
			BaseSuffix:         baseSuffix,
			Lineage:            lineage,
			OutOfBand:          outOfBand,
			SnapshotCount:      0,
			SnapshotCountKnown: false,
			Kind:               kind,
			JailCTID:           jailCTID,
			VMRID:              vmRID,
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

		return left.Suffix < right.Suffix
	})

	return datasets
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

func (s *Service) ListRemoteTargetDatasetSnapshotsPage(
	ctx context.Context,
	targetID uint,
	remoteDataset string,
	request SnapshotPageRequest,
) (SnapshotPage, error) {
	started := time.Now()
	request, err := NewSnapshotPageRequest(request.Limit, request.Cursor)
	if err != nil {
		return SnapshotPage{}, err
	}
	parsedRemoteDataset, err := requiredRemoteDataset(remoteDataset)
	if err != nil {
		return SnapshotPage{}, err
	}
	remoteDataset = parsedRemoteDataset.String()
	target, rootDataset, releaseTargetKey, err := s.getRestoreTargetWithKey(targetID)
	if err != nil {
		return SnapshotPage{}, err
	}
	defer releaseTargetKey()

	if !parsedRemoteDataset.Within(rootDataset) {
		return SnapshotPage{}, fmt.Errorf("remote_dataset_outside_backup_root")
	}

	candidates, err := s.listRemoteSnapshotsWithLineage(ctx, &target, remoteDataset)
	if err != nil {
		return SnapshotPage{}, err
	}
	candidates = filterBackupSnapshots(candidates)
	kind, _ := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset))
	if kind == clusterModels.BackupJobModeVM {
		candidates = collapseSnapshotsByShortName(candidates)
	}
	pageCandidates, nextCursor, hasMore, err := paginateSnapshotCandidates(candidates, request)
	if err != nil {
		return SnapshotPage{}, err
	}
	items, err := s.filterRestorableTargetSnapshots(ctx, &target, kind, pageCandidates)
	if err != nil {
		return SnapshotPage{}, fmt.Errorf("failed_to_validate_target_restore_points: %w", err)
	}

	page := SnapshotPage{Items: items, NextCursor: nextCursor, HasMore: hasMore}
	logger.L.Debug().
		Uint("target_id", targetID).
		Str("dataset", remoteDatasetForLog(remoteDataset)).
		Int("candidate_count", len(candidates)).
		Int("page_candidate_count", len(pageCandidates)).
		Int("returned_count", len(items)).
		Int("limit", request.Limit).
		Bool("continued", request.Cursor != "").
		Bool("has_more", hasMore).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("backup_target_snapshot_page_listed")
	return page, nil
}

func requiredRemoteDataset(raw string) (remoteexec.ZFSDataset, error) {
	if strings.TrimSpace(raw) == "" {
		return remoteexec.ZFSDataset{}, fmt.Errorf("remote_dataset_required")
	}
	dataset, err := remoteexec.ParseZFSDataset(raw)
	if err != nil {
		return remoteexec.ZFSDataset{}, fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	return dataset, nil
}

func CanonicalRestoreFromTargetInput(
	remoteDataset, snapshot, destinationDataset string,
) (string, string, string, error) {
	if strings.TrimSpace(remoteDataset) == "" {
		return "", "", "", fmt.Errorf("remote_dataset_required")
	}
	if strings.TrimSpace(snapshot) == "" {
		return "", "", "", fmt.Errorf("snapshot_required")
	}
	if strings.TrimSpace(destinationDataset) == "" {
		return "", "", "", fmt.Errorf("destination_dataset_required")
	}
	defaultRemoteDataset, err := requiredRemoteDataset(remoteDataset)
	if err != nil {
		return "", "", "", err
	}
	selectedRemoteDataset := defaultRemoteDataset.String()

	remoteDataset, snapshot, err = parseRestoreSnapshotInput(snapshot, defaultRemoteDataset.String())
	if err != nil {
		return "", "", "", err
	}
	if remoteDataset != selectedRemoteDataset {
		return "", "", "", fmt.Errorf("snapshot_dataset_mismatch")
	}
	destination, err := remoteexec.ParseZFSDataset(destinationDataset)
	if err != nil || !strings.Contains(destination.String(), "/") {
		if err == nil {
			err = fmt.Errorf("fully_qualified_dataset_required")
		}
		return "", "", "", fmt.Errorf("destination_dataset_invalid: %w", err)
	}
	return remoteDataset, snapshot, destination.String(), nil
}

func (s *Service) EnqueueRestoreFromTarget(
	ctx context.Context,
	targetID uint,
	remoteDataset, snapshot, destinationDataset string,
	restoreNetwork bool,
	operationID string,
) error {
	if targetID == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	remoteDataset, snapshot, destinationDataset, err := CanonicalRestoreFromTargetInput(
		remoteDataset,
		snapshot,
		destinationDataset,
	)
	if err != nil {
		return err
	}

	target, rootDataset, releaseTargetKey, err := s.getRestoreTargetWithKey(targetID)
	if err != nil {
		return err
	}
	defer releaseTargetKey()
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil {
		return fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	if !parsedRemoteDataset.Within(rootDataset) {
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

	handle, payload, err := s.acquireDurableBackupTargetRestoreOperation(ctx, restoreFromTargetPayload{
		TargetID:           targetID,
		RemoteDataset:      remoteDataset,
		Snapshot:           snapshot,
		DestinationDataset: destinationDataset,
		RestoreNetwork:     &restoreNetwork,
	}, operationID)
	if err != nil {
		return err
	}

	execution, event, observabilityErr := s.prepareRestoreObservability(
		ctx,
		restoreAuditTypeTarget,
		targetID,
		handle.Token,
		restoreEventSpec{
			SourceDataset:  payload.RemoteDataset + payload.Snapshot,
			TargetEndpoint: payload.DestinationDataset,
		},
	)
	if observabilityErr != nil {
		abortCtx, abortCancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
		defer abortCancel()
		return errors.Join(observabilityErr, s.abortDurableBackupTargetRestoreOperation(abortCtx, handle))
	}
	if restoreEventTerminal(event.Status) {
		return s.finishDurableBackupTargetRestoreOperation(handle)
	}

	payload.OperationToken = handle.Token
	payload.HolderNodeID = handle.HolderNodeID
	payload.EventID = execution.EventID
	payload.AuditRecordID = execution.Audit.RecordID
	if err := s.enqueueRestoreFromTargetOperation(ctx, payload); err != nil {
		abortCtx, abortCancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
		defer abortCancel()
		abortErr := s.abortDurableBackupTargetRestoreOperation(abortCtx, handle)
		if abortErr == nil {
			if eventErr := s.finalizeRestoreEventByID(event.ID, fmt.Errorf("restore_queue_failed: %w", err), ""); eventErr != nil {
				return errors.Join(err, eventErr)
			}
		}
		return errors.Join(err, abortErr)
	}
	return nil
}

func (s *Service) registerRestoreFromTargetJob() {
	db.QueueRegisterJSON(restoreFromTargetQueueName, s.handleRestoreFromTargetQueue)
}

func (s *Service) finalizeRestoreFromTargetAdmissionFailure(
	payload restoreFromTargetPayload,
	handle backupTargetRestoreOperationHandle,
	admissionErr error,
) error {
	execution := newRestoreExecution(payload.EventID, handle.Token, payload.AuditRecordID)
	event, err := s.restoreEventForExecution(execution, restoreEventSpec{
		SourceDataset:  payload.RemoteDataset + payload.Snapshot,
		TargetEndpoint: payload.DestinationDataset,
	})
	if err != nil {
		return errors.Join(admissionErr, err)
	}
	if restoreEventTerminal(event.Status) {
		s.finalizeRestoreAuditForEvent(event)
		return nil
	}
	return s.finalizeRestoreEventByID(event.ID, admissionErr, "")
}

func (s *Service) handleRestoreFromTargetQueue(
	ctx context.Context,
	payload restoreFromTargetPayload,
) error {
	s.setTargetRestoreToken(&s.queuedTargetRestoreTokens, payload.OperationToken, false)
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = admittedCtx
	handle, payload, execute, err := s.prepareQueuedBackupTargetRestoreOperation(ctx, payload)
	if err != nil {
		logger.L.Warn().
			Err(err).
			Uint("target_id", payload.TargetID).
			Str("destination_dataset", strings.TrimSpace(payload.DestinationDataset)).
			Msg("queued_restore_from_target_job_invalid_or_unavailable")
		if backupTargetRestoreQueuePayloadInvalid(err) {
			return nil
		}
		if backupTargetRestoreOperationTargetUnavailable(err) {
			return s.finalizeRestoreFromTargetAdmissionFailure(payload, handle, err)
		}
		return err
	}
	if !execute {
		_ = s.reconcileRestoreEventWithoutExecution(handle.Token)
		return nil
	}
	defer s.releaseActiveBackupTargetRestoreToken(handle.Token)

	execution := newRestoreExecution(payload.EventID, handle.Token, payload.AuditRecordID)
	spec := restoreEventSpec{
		SourceDataset:  payload.RemoteDataset + payload.Snapshot,
		TargetEndpoint: payload.DestinationDataset,
	}
	finish := func() error { return s.finishDurableBackupTargetRestoreOperation(handle) }
	return s.runQueuedRestore(ctx, execution, spec, finish, func(ctx context.Context) error {
		target, _, releaseTargetKey, err := s.getRestoreTargetWithKey(payload.TargetID)
		if err != nil {
			return err
		}
		defer releaseTargetKey()
		if s.restoreFromTargetRun != nil {
			return s.restoreFromTargetRun(ctx, &target, payload)
		}
		return s.runRestoreFromTarget(ctx, &target, payload)
	})
}

// runRestoreFromTarget restores a target's dataset, jail or VM to this node;
// a guest comes back as a new guest.
func (s *Service) runRestoreFromTarget(ctx context.Context, target *clusterModels.BackupTarget, payload restoreFromTargetPayload) error {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return err
	}
	remoteDataset, snapshot, destination, err := CanonicalRestoreFromTargetInput(
		payload.RemoteDataset,
		payload.Snapshot,
		payload.DestinationDataset,
	)
	if err != nil {
		return err
	}
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil {
		return fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	if !parsedRemoteDataset.Within(root) {
		return fmt.Errorf("remote_dataset_outside_backup_root")
	}
	workloadType, workloadID := restoreWorkloadIdentityForDataset(destination)
	release, err := s.lockRestoreDestination(ctx, destination, workloadType, workloadID, "restore_from_target")
	if err != nil {
		return err
	}
	defer release()
	newGuest, err := s.preflightOOBGuestRestoreDestination(ctx, target, remoteDataset, destination)
	if err != nil {
		return err
	}

	kind, _ := inferRestoreDatasetKind(destination)
	request := restoreRequest{
		target:         target,
		newGuest:       newGuest,
		kind:           kind,
		remoteDataset:  remoteDataset,
		snapshot:       snapshot,
		destination:    destination,
		restoreNetwork: payload.RestoreNetwork == nil || *payload.RestoreNetwork,
	}
	if remoteKind, vmRID := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset)); remoteKind == clusterModels.BackupJobModeVM && vmRID > 0 {
		request.kind = clusterModels.BackupJobModeVM
		return s.runRestoreVM(ctx, request)
	}
	return s.runRestoreDataset(ctx, request)
}

// runRestoreVM restores every root of a VM, which can span pools, and then the
// VM's records. The VM stays fenced and stopped until it's done.
func (s *Service) runRestoreVM(ctx context.Context, request restoreRequest) (retErr error) {
	target := request.target
	snapshot := request.snapshot
	jobID := request.jobID()
	if err := validateVMRestoreSnapshot(snapshot, jobID); err != nil {
		return err
	}
	if s.VM == nil || !s.VM.IsVirtualizationEnabled() {
		return fmt.Errorf("virtualization_disabled")
	}

	remoteDataset := request.remoteDataset
	destinationDataset := request.destination
	strictAsNew := request.newGuest != nil

	_, remoteRID := inferRestoreDatasetKind(relativeDatasetSuffix(target.BackupRoot, remoteDataset))
	if remoteRID == 0 {
		return fmt.Errorf("vm_rid_missing")
	}

	destKind, destRID := inferRestoreDatasetKind(destinationDataset)
	if strictAsNew {
		destRID = request.newGuest.GuestID
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
		destinationDataset,
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
		if err := s.requireOOBGuestRestoreAvailable(ctx, request.newGuest, candidateDestinations, true); err != nil {
			return err
		}
	}
	preparedRootPlans := make(map[string]remoteRestoreDatasetPlan, len(rootPlans))
	generationSelections := make([]restoreTargetGenerationSelection, 0, len(rootPlans))
	var committedMetadata *backupCommitMetadata
	committedEntries := make([]backupManifestEntry, 0)
	for _, plan := range rootPlans {
		prepared, err := s.prepareTargetRestoreDatasetPlan(
			ctx,
			target,
			plan.remote,
			snapshot,
		)
		if err != nil {
			return fmt.Errorf("restore_vm_root_preflight_failed: dataset=%s: %w", plan.remote, err)
		}
		resolved := prepared.resolvedRemoteDataset
		metadata := prepared.commitMetadata
		if metadata.Version == backupCommitVersion {
			if jobID != nil && metadata.JobID != *jobID {
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
			part, err := prepared.manifestInventory.manifestEntries(
				canonicalRoot,
				snapshot,
				metadata.Recursive,
				false,
			)
			if err != nil {
				return fmt.Errorf("restore_vm_root_manifest_read_failed: dataset=%s: %w", plan.remote, err)
			}
			committedEntries = append(committedEntries, part...)
		}
		preparedRootPlans[plan.remote] = prepared
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

	var guestIdentityReservation *restoreGuestIdentityReservation
	if strictAsNew {
		var reserveErr error
		ctx, guestIdentityReservation, reserveErr = s.reserveGuestIdentityForRestore(
			ctx,
			clusterModels.ReplicationGuestTypeVM,
			destRID,
		)
		if reserveErr != nil {
			return reserveErr
		}
		defer func() {
			s.releaseGuestIdentityReservationAfterError(ctx, guestIdentityReservation, &retErr)
		}()
	}
	// Before the fence: a leftover staging dataset must not cost a hard stop.
	for _, plan := range rootPlans {
		identity := newRestoreStagingIdentity(jobID, target.ID, plan.destination)
		if err := s.prepareRestoreStagingDataset(ctx, plan.destination+".restoring", identity); err != nil {
			return fmt.Errorf("restore_preflight_staging_check_failed: %w", err)
		}
	}
	execution := restoreExecutionFromContext(ctx)
	vmRestoreFence, err := s.acquireVMRestoreFence(ctx, destRID, execution.EventID)
	if err != nil {
		return fmt.Errorf("acquire_vm_restore_fence_failed: %w", err)
	}
	if vmRestoreFence == nil {
		return fmt.Errorf("acquire_vm_restore_fence_failed: restore_fence_schema_unavailable")
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
			restartCtx, restartCancel := guestStateRecoveryContext(ctx)
			restartErr := s.startVMIfPresentContext(restartCtx, vmRestoreFence.guestID)
			restartCancel()
			if restartErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("restore_vm_restart_failed: %w", restartErr))
			}
		}
	}()
	if !strictAsNew {
		if err := s.requireInPlaceGuestRestorePlacement(
			ctx,
			vmRestoreFence.guestType,
			vmRestoreFence.guestID,
		); err != nil {
			return fmt.Errorf("restore_guest_placement_check_failed: %w", err)
		}
	}

	existingVM, err := s.findVMByRID(destRID)
	if err != nil {
		return fmt.Errorf("failed_to_lookup_existing_vm_before_restore: %w", err)
	}
	if existingVM != nil && strictAsNew {
		return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=vm", destRID)
	}

	applied := make([]restoredDatasetBackup, 0, len(rootPlans))
	for _, plan := range rootPlans {
		restored, err := s.restoreDatasetRoot(ctx, request, preparedRootPlans[plan.remote], plan.destination)
		if err != nil {
			return s.rollbackRestoreAfterError(applied, err)
		}
		applied = append(applied, restored)
	}

	if strictAsNew {
		if err := s.requireOOBGuestRestoreAvailable(ctx, request.newGuest, nil, false); err != nil {
			return s.rollbackRestoreAfterError(applied, err)
		}
	}

	// Activate before reconcile commits the VM rows, while it can still be rolled back.
	var activations []restoreTargetGenerationActivation
	if jobID != nil {
		activations, err = s.activateTargetGenerationsForRestore(ctx, target, generationSelections)
		if err != nil {
			return s.rollbackRestoreAfterError(applied, err)
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
			request.restoreNetwork,
		)
	} else {
		reconcileErr = s.reconcileRestoredVMFromDatasetWithOptions(ctx, primaryDestination, request.restoreNetwork)
	}
	if errors.Is(reconcileErr, errRestoredVMApplied) {
		return fmt.Errorf("reconcile_restored_vm_failed: %w", reconcileErr)
	}
	if reconcileErr != nil {
		rollbackErr := errors.Join(
			s.rollbackRestoredDatasetBackups(applied),
			s.rollbackTargetGenerationActivations(target, activations),
		)
		if rollbackErr != nil {
			return fmt.Errorf("reconcile_restored_vm_failed: %w; rollback_failed: %v", reconcileErr, rollbackErr)
		}
		return fmt.Errorf("reconcile_restored_vm_failed: %w", reconcileErr)
	}

	if strictAsNew {
		if err := s.finalizeGuestIdentityRestore(ctx, guestIdentityReservation); err != nil {
			return err
		}
	}

	s.cleanupRestoreArchives(ctx, applied)
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

func (s *Service) listRemoteVMRepresentativeRoots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	vmRID uint,
	remoteDataset string,
) ([]string, error) {
	if vmRID == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return nil, err
	}
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil || !parsedRemoteDataset.Within(root) {
		if err == nil {
			err = fmt.Errorf("dataset_outside_backup_root")
		}
		return nil, fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	backupRoot := root.String()
	remoteDataset = parsedRemoteDataset.String()

	output, err := s.runTargetZFSList(ctx, target, "-t", "filesystem", "-r", "-Hp", "-o", "name", target.BackupRoot)
	if err != nil {
		return nil, err
	}

	selectedSuffix := relativeDatasetSuffix(backupRoot, remoteDataset)
	_, _, selectedBase := classifyDatasetLineage(selectedSuffix)
	selectedVMRoot := vmDatasetRoot(selectedBase)
	jobLineage := jobLineageTail(selectedBase)
	if selectedVMRoot == "" || jobLineage == "" ||
		selectedBase != normalizeDatasetPath(selectedVMRoot+"/"+jobLineage) {
		return nil, fmt.Errorf("remote_vm_job_lineage_required")
	}

	bestByBase := make(map[string]string)
	bestRank := make(map[string]int)

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parsedDataset, parseErr := remoteexec.ParseZFSDataset(strings.TrimSpace(line))
		if parseErr != nil || !parsedDataset.Within(root) {
			continue
		}
		dataset := parsedDataset.String()

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
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return 0
	}
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil || !parsedRemoteDataset.Within(root) {
		return 0
	}
	remoteDataset = parsedRemoteDataset.String()
	argv := []string{
		"zfs", "list", "-t", "filesystem,volume", "-r", "-d", "1",
		"-Hp", "-o", "name", remoteDataset,
	}

	output, err := s.runTargetSSH(ctx, target, argv...)
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
	return s.listRemoteSnapshots(ctx, target, []string{remoteDataset}, false)
}

func (s *Service) listRemoteSnapshotsForDatasetRecursive(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	return s.listRemoteSnapshots(ctx, target, []string{remoteDataset}, true)
}

func (s *Service) listRemoteSnapshots(ctx context.Context, target *clusterModels.BackupTarget, remoteDatasets []string, recursive bool) ([]SnapshotInfo, error) {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return nil, err
	}
	argv := []string{"zfs", "list", "-t", "snapshot", "-Hp"}
	if recursive {
		argv = append(argv, "-r")
	}
	argv = append(argv, "-o", "name,creation,used,refer,guid,encryption", "-s", "creation")
	for _, remoteDataset := range remoteDatasets {
		parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
		if err != nil || !parsedRemoteDataset.Within(root) {
			if err == nil {
				err = fmt.Errorf("dataset_outside_backup_root")
			}
			return nil, fmt.Errorf("remote_dataset_invalid: %w", err)
		}
		argv = append(argv, parsedRemoteDataset.String())
	}
	remoteDataset := strings.Join(remoteDatasets, ",")

	var output string
	err = nil
	const maxRetries = 2
	for attempt := 1; attempt <= maxRetries; attempt++ {
		output, err = s.runTargetSSH(ctx, target, argv...)
		if err == nil {
			break
		}
		if attempt < maxRetries {
			logger.L.Warn().
				Int("attempt", attempt).
				Str("command_kind", "zfs.list").
				Str("ssh_host", target.SSHHost).
				Str("dataset", remoteDatasetForLog(remoteDataset)).
				Bool("recursive", recursive).
				Msg("ssh_snapshot_list_failed_retrying")
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("failed_to_list_remote_snapshots: %w", ctx.Err())
			case <-time.After(2 * time.Second):
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_remote_snapshots: %s", err)
	}

	return parseSnapshotInfoOutput(output), nil
}

func (s *Service) listRemoteSnapshotsWithLineage(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	snapshots, err := s.listJobLineageSnapshots(ctx, target, remoteDataset)
	if err != nil {
		snapshots, err = s.listLineageSnapshotsByDataset(ctx, target, remoteDataset)
		if err != nil {
			return nil, err
		}
	}

	combined := make([]SnapshotInfo, 0, len(snapshots))
	seen := make(map[string]struct{})
	for _, snapshot := range snapshots {
		key := strings.TrimSpace(snapshot.Name)
		if key == "" {
			continue
		}
		lineage, outOfBand, _ := classifyDatasetLineage(relativeDatasetSuffix(target.BackupRoot, snapshotDatasetName(key)))
		snapshot.Lineage = lineage
		snapshot.OutOfBand = outOfBand
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		combined = append(combined, snapshot)
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

// A job's folder holds only its active dataset and the archived generations,
// so one listing two levels down covers the lineage in a single round trip.
// Other layouts, or a failed listing, take the per-dataset path.
func (s *Service) listJobLineageSnapshots(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	suffix := relativeDatasetSuffix(target.BackupRoot, remoteDataset)
	if !isConventionalBackupLineageDataset(suffix) || strings.Contains(path.Base(suffix), ".pre_") {
		return nil, fmt.Errorf("not_a_job_lineage_dataset")
	}
	parent := path.Dir(normalizeDatasetPath(remoteDataset))
	output, err := s.runTargetDatasetSSH(
		ctx, target, parent,
		"zfs", "list", "-t", "snapshot", "-Hp", "-d", "2",
		"-o", "name,creation,used,refer,guid,encryption", "-s", "creation", parent,
	)
	if err != nil {
		return nil, err
	}
	snapshots := make([]SnapshotInfo, 0)
	for _, snapshot := range parseSnapshotInfoOutput(output) {
		leaf := strings.TrimPrefix(snapshotDatasetName(snapshot.Name), parent+"/")
		if leaf == "active" || strings.HasPrefix(leaf, "active_gen-") {
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots, nil
}

func (s *Service) listLineageSnapshotsByDataset(ctx context.Context, target *clusterModels.BackupTarget, remoteDataset string) ([]SnapshotInfo, error) {
	lineageDatasets, err := s.listRemoteLineageDatasets(ctx, target, remoteDataset)
	if err != nil {
		return nil, err
	}

	snapshots, err := s.listRemoteSnapshots(ctx, target, lineageDatasets, false)
	if err != nil {
		// A generation renamed or destroyed mid-listing fails the whole batch.
		snapshots = nil
		for i, dataset := range lineageDatasets {
			part, snapErr := s.listRemoteSnapshotsForDataset(ctx, target, dataset)
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
			snapshots = append(snapshots, part...)
		}
	}

	return snapshots, nil
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
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return nil, err
	}
	parsedRemoteDataset, err := remoteexec.ParseZFSDataset(remoteDataset)
	if err != nil || !parsedRemoteDataset.Within(root) {
		if err == nil {
			err = fmt.Errorf("dataset_outside_backup_root")
		}
		return nil, fmt.Errorf("remote_dataset_invalid: %w", err)
	}
	remoteDataset = parsedRemoteDataset.String()

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
		return nil, fmt.Errorf("list_remote_lineage_datasets_failed: %w", err)
	}

	results := make([]string, 0)
	seen := make(map[string]struct{})

	add := func(dataset string) {
		parsedDataset, parseErr := remoteexec.ParseZFSDataset(strings.TrimSpace(dataset))
		if parseErr != nil || !parsedDataset.Within(root) {
			return
		}
		dataset = parsedDataset.String()
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_not_found: %w", err)
		}
		return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_lookup_failed: %w", err)
	}
	if !target.Enabled {
		return clusterModels.BackupTarget{}, fmt.Errorf("backup_target_disabled")
	}
	return target, nil
}

func (s *Service) getRestoreTargetWithKey(
	targetID uint,
) (clusterModels.BackupTarget, remoteexec.ZFSDataset, func(), error) {
	target, err := s.getRestoreTarget(targetID)
	if err != nil {
		return clusterModels.BackupTarget{}, remoteexec.ZFSDataset{}, func() {}, err
	}
	_, root, err := canonicalizeBackupTarget(&target)
	if err != nil {
		return clusterModels.BackupTarget{}, remoteexec.ZFSDataset{}, func() {}, err
	}
	release, err := s.acquireBackupTargetSSHKey(&target)
	if err != nil {
		return clusterModels.BackupTarget{}, remoteexec.ZFSDataset{}, func() {}, fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	return target, root, release, nil
}

func (s *Service) runTargetZFSList(ctx context.Context, target *clusterModels.BackupTarget, args ...string) (string, error) {
	argv := append([]string{"zfs", "list"}, args...)
	return s.runTargetSSH(ctx, target, argv...)
}

func normalizeSnapshotName(snapshot string) (string, error) {
	if strings.TrimSpace(snapshot) == "" {
		return "", fmt.Errorf("snapshot_required")
	}
	parsed, err := remoteexec.ParseZFSSnapshotName(snapshot)
	if err != nil {
		return "", fmt.Errorf("snapshot_invalid: %w", err)
	}
	return parsed.WithAt(), nil
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
