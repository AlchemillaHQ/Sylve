// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/storage/mount"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
	goLibvirt "github.com/digitalocean/go-libvirt"
)

type targetMigrationImportRequest struct {
	GuestID            uint              `json:"guestId"`
	OperationToken     string            `json:"operationToken"`
	StartGuest         *bool             `json:"startGuest"`
	SourceDatasetRoots []string          `json:"sourceDatasetRoots"`
	TargetPools        map[string]string `json:"targetPools,omitempty"`
}

type targetMigrationImportReceipt struct {
	Message            string            `json:"message"`
	Warnings           []string          `json:"warnings"`
	GuestID            uint              `json:"guestId"`
	OperationToken     string            `json:"operationToken"`
	StartGuest         *bool             `json:"startGuest"`
	SourceDatasetRoots []string          `json:"sourceDatasetRoots"`
	TargetPools        map[string]string `json:"targetPools,omitempty"`
}

// countingReader counts the bytes read through it, for transfer progress.
type countingReader struct {
	r io.Reader
	n *uint64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	atomic.AddUint64(cr.n, uint64(n))
	return n, err
}

func (s *Service) phasePreflight(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	detail := s.Cluster.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}

	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", mp.TargetNodeUUID).First(&targetNode).Error; err != nil {
		return fmt.Errorf("target_node_not_found: %w", err)
	}
	if err := s.Cluster.CheckGuestBackupTargetsForMigration(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID,
	); err != nil {
		return err
	}
	if err := s.requireTargetGuestRecordAbsent(ctx, targetNode, task.GuestID); err != nil {
		return err
	}

	if task.GuestType == taskModels.GuestTypeVM {
		var vm vmModels.VM
		if err := s.DB.
			Preload("Storages").
			Preload("Storages.Dataset").
			Preload("Networks").
			Preload("CPUPinning").
			Where("rid = ?", task.GuestID).First(&vm).Error; err != nil {
			return fmt.Errorf("vm_not_found_for_preflight: %w", err)
		}

		var reasons []string
		reasons = append(reasons, s.vmConfigPreflightReasons(vm, targetNode)...)
		reasons = append(reasons, s.vmTargetPreflightReasons(ctx, vm, targetNode)...)

		var hard []string
		for _, r := range reasons {
			if strings.HasPrefix(strings.ToLower(r), "warning_") {
				logger.L.Warn().Str("reason", r).Uint("rid", task.GuestID).Msg("vm_migration_preflight_warning")
			} else {
				hard = append(hard, r)
			}
		}
		if len(hard) > 0 {
			return fmt.Errorf("%s", strings.Join(hard, "; "))
		}
	}

	return nil
}

const (
	// Catch-up passes stop once one sends at most this much, which the final
	// sync sends in well under a second.
	migrationCatchUpMinBytes  = 64 << 20
	migrationCatchUpMaxPasses = 3
)

func (s *Service) phaseInitialReplication(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	sent, err := s.replicateGuestDatasets(ctx, mp, task, "initial")
	if err != nil {
		return err
	}
	// A running guest keeps writing during the full send. Catch-up passes
	// send those writes while it still runs, so the final sync, which runs
	// with the guest stopped, only sends what the last pass missed.
	active, err := s.sourceGuestActive(task)
	if err != nil || !active {
		return err
	}
	return catchUpMigration(sent, func() (uint64, error) {
		if err := s.checkCancelled(task.ID); err != nil {
			return 0, err
		}
		return s.replicateGuestDatasets(ctx, mp, task, "catchup")
	})
}

// catchUpMigration runs send while the last pass sent more than
// migrationCatchUpMinBytes, up to migrationCatchUpMaxPasses times. It stops
// early when a pass sends no less than the one before: the guest writes as
// fast as the link sends, so another pass wouldn't shrink the final sync.
func catchUpMigration(sent uint64, send func() (uint64, error)) error {
	for pass := 0; pass < migrationCatchUpMaxPasses && sent > migrationCatchUpMinBytes; pass++ {
		last := sent
		var err error
		if sent, err = send(); err != nil {
			return err
		}
		if sent >= last {
			return nil
		}
	}
	return nil
}

func (s *Service) phaseFinalSync(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	_, err := s.replicateGuestDatasets(ctx, mp, task, "final")
	return err
}

// replicateGuestDatasets sends a snapshot of each guest root to the target and
// returns the bytes sent. The "initial" pass sends everything; "catchup" and
// "final" send what changed since the last snapshot both sides have.
func (s *Service) replicateGuestDatasets(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask, pass string) (uint64, error) {
	if mp == nil {
		return 0, fmt.Errorf("migration_payload_required")
	}
	resolved, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		return 0, fmt.Errorf("resolve_datasets_failed: %w", err)
	}
	resolved = filterParentDatasets(normalizedMigrationDatasetRootManifest(resolved))
	datasets := resolved
	if len(mp.SourceDatasetRoots) > 0 {
		datasets = filterParentDatasets(normalizedMigrationDatasetRootManifest(mp.SourceDatasetRoots))
		if !sameMigrationDatasetRootManifest(datasets, resolved) {
			return 0, fmt.Errorf("migration_source_dataset_roots_changed")
		}
	}
	if len(datasets) == 0 {
		return 0, fmt.Errorf("no_datasets_found_for_guest")
	}

	for _, dataset := range datasets {
		if !isCanonicalMigrationGuestDataset(dataset, task.GuestType, task.GuestID) {
			return 0, fmt.Errorf("migration_source_dataset_root_invalid: %s", dataset)
		}
	}

	var metadataWriter func(uint) error
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt == nil {
			return 0, fmt.Errorf("vm_runtime_service_unavailable")
		}
		metadataWriter = s.Libvirt.WriteVMJson
	case taskModels.GuestTypeJail:
		if s.Jail == nil {
			return 0, fmt.Errorf("jail_runtime_service_unavailable")
		}
		metadataWriter = s.Jail.WriteJailJSON
	}
	if err := flushMigrationGuestMetadata(task.GuestType, task.GuestID, metadataWriter); err != nil {
		return 0, err
	}

	incremental := pass != "initial"
	// Nanoseconds keep the names in order when passes finish within a second.
	snapName := fmt.Sprintf("%s-%s-%d", migrationSnapPrefix, pass, time.Now().UnixNano())

	if !incremental {
		for _, dataset := range datasets {
			if err := s.destroyLocalMigrationSnapshots(ctx, dataset); err != nil {
				return 0, fmt.Errorf("destroy_previous_migration_snapshots_%s_failed: %w", dataset, err)
			}
		}
	}

	message, progressLabel := "replicating_dataset", mp.Phase
	if pass == "catchup" {
		message, progressLabel = "catching_up", "catching_up"
	}

	var total uint64
	for _, root := range TargetDatasetRoots(datasets, mp.TargetPools) {
		dataset := root.Source
		target := TargetDatasetRequest{
			GuestType: task.GuestType, GuestID: task.GuestID, OperationToken: mp.OperationToken, Dataset: root.Receive,
		}

		mp.PhaseMessage = fmt.Sprintf("%s: %s", message, dataset)
		s.updateTaskPhase(task.ID, *mp)

		taskID := task.ID
		progressFn := func(ds string, total, sent uint64) {
			s.writeTransferProgress(taskID, ds, progressLabel, total, sent)
		}

		started := time.Now()
		sent, err := s.sendDatasetToNode(ctx, mp.TargetNodeUUID, dataset, target, snapName, incremental, progressFn, taskID)
		if err != nil {
			if isExplicitMigrationCancellation(err) {
				return 0, err
			}
			return 0, fmt.Errorf("replicate_dataset_%s_failed: %w", dataset, err)
		}
		logger.L.Info().Uint("task_id", taskID).Str("dataset", dataset).Str("pass", pass).
			Uint64("bytes", sent).Dur("took", time.Since(started)).Msg("migration_dataset_sent")
		total += sent
	}

	return total, nil
}

// sendDatasetToNode streams a snapshot of dataset to the target node over the
// intra-cluster mTLS channel, where it is received into target.Dataset.
func (s *Service) sendDatasetToNode(
	ctx context.Context,
	nodeID string,
	dataset string,
	target TargetDatasetRequest,
	snapName string,
	incremental bool,
	progressFn func(dataset string, totalBytes, sentBytes uint64),
	taskID uint,
) (uint64, error) {
	parsedDataset, err := remoteexec.ParseZFSDataset(dataset)
	if err != nil {
		return 0, fmt.Errorf("migration_dataset_invalid: %w", err)
	}
	if !target.Valid() {
		return 0, fmt.Errorf("migration_target_dataset_invalid: %s", target.Dataset)
	}
	parsedSnapshot, err := remoteexec.ParseZFSSnapshotName(snapName)
	if err != nil || !isMigrationOwnedSnapshot(parsedSnapshot.String()) {
		return 0, fmt.Errorf("migration_snapshot_invalid")
	}
	dataset = parsedDataset.String()
	snapName = parsedSnapshot.String()
	commonSnap := ""
	if incremental {
		prevSnaps, snapErr := s.listMigrationSnapshots(ctx, dataset)
		if snapErr != nil {
			return 0, fmt.Errorf("list_migration_snapshots_failed: %w", snapErr)
		}
		info, err := s.targetDataset(ctx, nodeID, target)
		if err != nil {
			return 0, fmt.Errorf("list_target_migration_snapshots_failed: %w", err)
		}
		commonSnap = latestCommonMigrationSnapshot(prevSnaps, info.MigrationSnapshots)
		if commonSnap == "" {
			return 0, fmt.Errorf("migration_incremental_common_snapshot_missing: %s", dataset)
		}
	} else if err := s.writeTargetDataset(ctx, nodeID, "migration-target-prepare", target); err != nil {
		return 0, fmt.Errorf("target_dataset_prepare_failed: %w", err)
	}

	fullSnap := dataset + "@" + snapName
	if _, err := s.GZFS.ZFS.Snapshot(ctx, dataset, snapName, true); err != nil {
		return 0, fmt.Errorf("snapshot_failed: %w", err)
	}

	sendArgs := []string{"send", "-P", "-R", "-L", "-c", "-e"}
	if commonSnap != "" {
		sendArgs = append(sendArgs, "-i", "@"+commonSnap)
	}
	sendArgs = append(sendArgs, fullSnap)

	sendCmd := exec.CommandContext(ctx, "zfs", sendArgs...)
	stdout, err := sendCmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("zfs_send_start_failed: %w", err)
	}
	var bytesSent uint64

	// zfs send -P prints its estimate on stderr before the stream starts.
	sendStderrPr, sendStderrPw := io.Pipe()
	sendCmd.Stderr = sendStderrPw
	var sendStderrLines []string
	var sendTotal uint64
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(sendStderrPr)
		for scanner.Scan() {
			line := scanner.Text()
			sendStderrLines = append(sendStderrLines, line)
			if total, ok := parseSendSizeLine(line); ok {
				atomic.StoreUint64(&sendTotal, total)
			}
		}
	}()

	// Periodic progress polling — reads the counting pipe every 2 seconds.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stderrDone:
				// Send final progress with total bytes from pipe.
				total := atomic.LoadUint64(&sendTotal)
				sent := atomic.LoadUint64(&bytesSent)
				if total == 0 {
					total = sent
				}
				if progressFn != nil && total > 0 {
					progressFn(dataset, total, sent)
				}
				return
			case <-ticker.C:
				if progressFn != nil {
					total := atomic.LoadUint64(&sendTotal)
					sent := atomic.LoadUint64(&bytesSent)
					if total > 0 && sent > 0 {
						if sent > total {
							sent = total
						}
						progressFn(dataset, total, sent)
					}
				}
			}
		}
	}()

	if err := sendCmd.Start(); err != nil {
		sendStderrPw.Close()
		return 0, fmt.Errorf("zfs_send_start_failed: %w", err)
	}

	// Cancelling the stream ends the request, which stops zfs recv on the
	// target. The watcher stays alive until the stream has finished so it can
	// still interrupt a receiver that is draining after zfs send completes.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	transferDone := make(chan struct{})
	cancelDone := make(chan struct{})
	var cancelled atomic.Bool
	var cancellationPollErr atomic.Value
	go func() {
		defer close(cancelDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-transferDone:
				return
			case <-ticker.C:
				cancelErr := s.checkCancelled(taskID)
				select {
				case <-transferDone:
					return
				default:
				}
				if cancelErr == nil {
					continue
				}
				if !isExplicitMigrationCancellation(cancelErr) {
					pollErr := fmt.Errorf("migration_cancellation_poll_failed: %w", cancelErr)
					cancellationPollErr.Store(pollErr)
					logger.L.Warn().Err(pollErr).Uint("task_id", taskID).Str("dataset", dataset).
						Msg("migration_cancellation_poll_failed")
					sendCmd.Process.Kill()
					cancelStream()
					return
				}
				cancelled.Store(true)
				logger.L.Warn().Uint("task_id", taskID).Str("dataset", dataset).
					Msg("migration_cancelled_during_transfer_stopping_stream")
				sendCmd.Process.Kill()
				cancelStream()
				return
			}
		}
	}()

	_, recvErr := cluster.StreamToNode[TargetDatasetRequest, struct{}](
		streamCtx, s.Cluster, nodeID, "migration-target-receive", target, &countingReader{r: stdout, n: &bytesSent},
	)
	if recvErr != nil {
		// The target stopped reading, so zfs send would block on a full pipe.
		sendCmd.Process.Kill()
	}
	sendErr := sendCmd.Wait()
	sendStderrPw.Close()
	close(transferDone)
	<-stderrDone
	<-progressDone
	<-cancelDone

	if pollErr := cancellationPollErr.Load(); pollErr != nil {
		return 0, pollErr.(error)
	}

	// If the cancel goroutine killed our processes, return a clean cancel error.
	if cancelled.Load() {
		return 0, fmt.Errorf("migration_cancelled")
	}

	if recvErr != nil {
		return 0, fmt.Errorf("recv_failed_on_%s: %w", nodeID, recvErr)
	}
	if sendErr != nil {
		errStr := strings.Join(sendStderrLines, "\n")
		return 0, fmt.Errorf("send_failed: %s: %w", errStr, sendErr)
	}

	return atomic.LoadUint64(&bytesSent), nil
}

// parseSendSizeLine reads the stream total from the "size" line that zfs send
// -P prints after one line per dataset in the stream.
func parseSendSizeLine(line string) (uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "size" {
		return 0, false
	}
	total, err := strconv.ParseUint(fields[1], 10, 64)
	return total, err == nil && total > 0
}

func (s *Service) listMigrationSnapshots(ctx context.Context, dataset string) ([]string, error) {
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	list, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset, true)
	if err != nil {
		return nil, err
	}

	var snaps []string
	prefix := dataset + "@" + migrationSnapPrefix
	for _, ds := range list {
		if strings.HasPrefix(ds.Name, prefix) {
			short := strings.TrimPrefix(ds.Name, dataset+"@")
			if isMigrationOwnedSnapshot(short) {
				snaps = append(snaps, short)
			}
		}
	}

	return snaps, nil
}

func latestCommonMigrationSnapshot(local, remote []string) string {
	remoteSet := make(map[string]struct{}, len(remote))
	for _, snapshot := range remote {
		remoteSet[strings.TrimSpace(snapshot)] = struct{}{}
	}
	best := ""
	var bestTimestamp int64 = -1
	for _, value := range local {
		snapshot := strings.TrimSpace(value)
		if !isMigrationOwnedSnapshot(snapshot) {
			continue
		}
		if _, exists := remoteSet[snapshot]; !exists {
			continue
		}
		timestamp := int64(0)
		if dash := strings.LastIndexByte(snapshot, '-'); dash >= 0 {
			if parsed, err := strconv.ParseInt(snapshot[dash+1:], 10, 64); err == nil {
				timestamp = parsed
			}
		}
		if best == "" || timestamp >= bestTimestamp {
			best = snapshot
			bestTimestamp = timestamp
		}
	}
	return best
}

func isMigrationSnapshotPathWithinRoot(root, name string) bool {
	root = strings.TrimSpace(root)
	name = strings.TrimSpace(name)
	at := strings.LastIndex(name, "@")
	if root == "" || at <= 0 || at == len(name)-1 {
		return false
	}
	dataset := name[:at]
	return (dataset == root || strings.HasPrefix(dataset, root+"/")) &&
		isMigrationOwnedSnapshot(name[at+1:])
}

func (s *Service) destroyLocalMigrationSnapshots(ctx context.Context, root string) error {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("migration_snapshot_cleanup_zfs_unavailable")
	}
	snapshots, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, root, true)
	if err != nil {
		if isDatasetNotFound(err) {
			return nil
		}
		return err
	}

	var cleanupErrs []error
	for _, snapshot := range snapshots {
		if snapshot == nil || !isMigrationSnapshotPathWithinRoot(root, snapshot.Name) {
			continue
		}
		if err := snapshot.Destroy(ctx, false, false); err != nil && !isMigrationSnapshotNotFound(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy_local_migration_snapshot_%s_failed: %w", snapshot.Name, err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (s *Service) cleanupPreCutoverMigrationSnapshots(
	ctx context.Context,
	task taskModels.GuestLifecycleTask,
	payload migrationPayload,
) error {
	if s == nil || s.DB == nil || s.Cluster == nil {
		return fmt.Errorf("migration_snapshot_cleanup_unavailable")
	}
	roots := filterParentDatasets(normalizedMigrationDatasetRootManifest(payload.SourceDatasetRoots))
	if len(roots) == 0 {
		return nil
	}
	for _, root := range roots {
		if !isCanonicalMigrationGuestDataset(root, task.GuestType, task.GuestID) {
			return fmt.Errorf("migration_cleanup_dataset_root_invalid: %s", root)
		}
	}

	var cleanupErrs []error
	for _, root := range roots {
		if err := s.destroyLocalMigrationSnapshots(ctx, root); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup_local_migration_snapshots_%s_failed: %w", root, err))
		}
	}

	for _, root := range TargetDatasetRoots(roots, payload.TargetPools) {
		target := TargetDatasetRequest{
			GuestType: task.GuestType, GuestID: task.GuestID, OperationToken: payload.OperationToken, Dataset: root.Receive,
		}
		if err := s.writeTargetDataset(ctx, payload.TargetNodeUUID, "migration-target-cleanup", target); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup_remote_migration_snapshots_%s_failed: %w", root.Receive, err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (s *Service) runPreCutoverSnapshotCleanup(
	ctx context.Context,
	task taskModels.GuestLifecycleTask,
	payload migrationPayload,
) error {
	if s.preCutoverSnapshotCleanup != nil {
		return s.preCutoverSnapshotCleanup(ctx, task, payload)
	}
	return s.cleanupPreCutoverMigrationSnapshots(ctx, task, payload)
}

func (s *Service) phaseStopSource(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var stopSource func() error
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt == nil {
			return fmt.Errorf("vm_runtime_service_unavailable")
		}
		stopSource = func() error { return s.Libvirt.PerformActionContext(ctx, task.GuestID, "stop") }
	case taskModels.GuestTypeJail:
		if s.Jail == nil {
			return fmt.Errorf("jail_runtime_service_unavailable")
		}
		stopSource = func() error { return s.Jail.JailActionContext(ctx, int(task.GuestID), "stop") }
	default:
		return fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}
	sourceActive := func() (bool, error) { return s.sourceGuestActive(task) }

	active, err := sourceActive()
	if err != nil {
		return err
	}
	if err := persistMigrationOriginalRunning(mp, active, func() error {
		return s.persistTaskPhase(task.ID, *mp)
	}); err != nil {
		return fmt.Errorf("migration_original_running_checkpoint_failed: %w", err)
	}
	if !active {
		mp.PhaseMessage = "guest_already_stopped"
		s.updateTaskPhase(task.ID, *mp)
		return nil
	}
	if err := stopSource(); err != nil {
		return fmt.Errorf("stop_guest_failed: %w", err)
	}

	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitForMigrationSourceStopped(stopCtx, 250*time.Millisecond, sourceActive); err != nil {
		return fmt.Errorf("source_quiescence_unverified: %w", err)
	}

	return nil
}

// sourceGuestActive reports whether the guest is running on this node.
func (s *Service) sourceGuestActive(task taskModels.GuestLifecycleTask) (bool, error) {
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt == nil {
			return false, fmt.Errorf("vm_runtime_service_unavailable")
		}
		state, err := s.Libvirt.GetDomainState(int(task.GuestID))
		if err != nil {
			if libvirtServiceInterfaces.IsDomainNotFoundError(err) {
				return false, nil
			}
			return false, fmt.Errorf("vm_state_check_failed: %w", err)
		}
		if state == goLibvirt.DomainNostate {
			return false, fmt.Errorf("vm_state_unknown")
		}
		return state != goLibvirt.DomainShutoff, nil
	case taskModels.GuestTypeJail:
		if s.Jail == nil {
			return false, fmt.Errorf("jail_runtime_service_unavailable")
		}
		active, err := s.Jail.IsJailRunning(task.GuestID)
		if err != nil {
			return false, fmt.Errorf("jail_state_check_failed: %w", err)
		}
		return active, nil
	default:
		return false, fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}
}

func persistMigrationOriginalRunning(mp *migrationPayload, active bool, persist func() error) error {
	if mp == nil || persist == nil {
		return fmt.Errorf("migration_original_running_checkpoint_invalid")
	}
	if mp.OriginalRunning != nil {
		return nil
	}

	originalRunning := active
	mp.OriginalRunning = &originalRunning
	if err := persist(); err != nil {
		mp.OriginalRunning = nil
		return err
	}
	return nil
}

func waitForMigrationSourceStopped(ctx context.Context, pollInterval time.Duration, sourceActive func() (bool, error)) error {
	if ctx == nil || sourceActive == nil || pollInterval <= 0 {
		return fmt.Errorf("source_quiescence_check_invalid")
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		active, err := sourceActive()
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) phaseStartTarget(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask, operationToken string) error {
	if mp == nil || mp.OriginalRunning == nil {
		return fmt.Errorf("migration_original_running_state_missing")
	}
	name := "migration-import-jail"
	if task.GuestType == taskModels.GuestTypeVM {
		name = "migration-import-vm"
	}
	request := targetMigrationImportRequest{
		GuestID:            task.GuestID,
		OperationToken:     strings.TrimSpace(operationToken),
		StartGuest:         mp.OriginalRunning,
		SourceDatasetRoots: append([]string(nil), mp.SourceDatasetRoots...),
		TargetPools:        mp.TargetPools,
	}
	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	receipt, err := cluster.CallNode[targetMigrationImportRequest, targetMigrationImportReceipt](
		callCtx, s.Cluster, mp.TargetNodeUUID, name, request,
	)
	if err != nil {
		return fmt.Errorf("import_on_target_failed: %w", err)
	}
	if err := validateTargetMigrationImportReceipt(
		receipt, task.GuestID, operationToken, *mp.OriginalRunning, mp.SourceDatasetRoots, mp.TargetPools,
	); err != nil {
		return err
	}
	if len(receipt.Warnings) > 0 {
		mp.Warnings = receipt.Warnings
		s.updateTaskPhase(task.ID, *mp)
		logger.L.Info().Strs("warnings", receipt.Warnings).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_import_warnings")
	}

	return nil
}

func isMigrationOwnedSnapshot(shortName string) bool {
	name := strings.TrimSpace(shortName)
	suffix := strings.TrimPrefix(name, migrationSnapPrefix+"-")
	if suffix == name {
		return false
	}
	for _, phasePrefix := range []string{"initial-", "catchup-", "final-", "pre-migration-"} {
		timestamp := strings.TrimPrefix(suffix, phasePrefix)
		if timestamp == suffix || timestamp == "" {
			continue
		}
		parsed, err := strconv.ParseInt(timestamp, 10, 64)
		if err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == timestamp {
			return true
		}
	}
	return false
}

func flushMigrationGuestMetadata(guestType string, guestID uint, write func(uint) error) error {
	if guestID == 0 || write == nil {
		return fmt.Errorf("migration_guest_metadata_flush_unavailable")
	}
	if err := write(guestID); err != nil {
		switch guestType {
		case taskModels.GuestTypeVM:
			return fmt.Errorf("migration_writevmjson_flush_failed: %w", err)
		case taskModels.GuestTypeJail:
			return fmt.Errorf("migration_writejailjson_flush_failed: %w", err)
		default:
			return fmt.Errorf("migration_guest_metadata_flush_failed: %w", err)
		}
	}
	return nil
}

func validateTargetMigrationImportReceipt(
	receipt targetMigrationImportReceipt,
	guestID uint,
	operationToken string,
	startGuest bool,
	sourceDatasetRoots []string,
	targetPools map[string]string,
) error {
	if receipt.GuestID != guestID ||
		strings.TrimSpace(receipt.OperationToken) != strings.TrimSpace(operationToken) ||
		receipt.StartGuest == nil || *receipt.StartGuest != startGuest ||
		!sameMigrationDatasetRootManifest(receipt.SourceDatasetRoots, sourceDatasetRoots) ||
		!maps.Equal(receipt.TargetPools, targetPools) {
		return fmt.Errorf("import_on_target_receipt_mismatch")
	}
	return nil
}

func isExplicitMigrationCancellation(err error) bool {
	return err != nil && strings.TrimSpace(err.Error()) == "migration_cancelled"
}

func normalizedMigrationDatasetRootManifest(roots []string) []string {
	seen := make(map[string]struct{}, len(roots))
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		normalized = append(normalized, root)
	}
	sort.Strings(normalized)
	return normalized
}

func sameMigrationDatasetRootManifest(left, right []string) bool {
	left = normalizedMigrationDatasetRootManifest(left)
	right = normalizedMigrationDatasetRootManifest(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) phaseCleanupSource(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	if mp == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("migration_cleanup_unavailable")
	}
	datasets := filterParentDatasets(append([]string(nil), mp.SourceDatasetRoots...))
	if len(datasets) == 0 {
		return fmt.Errorf("migration_cleanup_source_dataset_roots_missing")
	}
	for _, dataset := range datasets {
		if !isCanonicalMigrationGuestDataset(dataset, task.GuestType, task.GuestID) {
			return fmt.Errorf("migration_cleanup_dataset_root_invalid: %s", dataset)
		}
	}

	if task.GuestType == taskModels.GuestTypeJail && s.Jail != nil {
		running, err := s.Jail.IsJailRunning(task.GuestID)
		if err != nil {
			return fmt.Errorf("migration_cleanup_jail_state_check_failed: %w", err)
		}
		if running {
			return fmt.Errorf("migration_cleanup_jail_still_running")
		}
	}

	type cleanupDataset struct {
		name       string
		dataset    *gzfs.Dataset
		mountpoint string
	}
	resolved := make([]cleanupDataset, 0, len(datasets))
	for _, dataset := range datasets {
		ds, getErr := s.GZFS.ZFS.Get(ctx, dataset, false)
		if getErr != nil {
			if isDatasetNotFound(getErr) {
				continue
			}
			return fmt.Errorf("migration_cleanup_get_dataset_%s_failed: %w", dataset, getErr)
		}
		if ds == nil {
			return fmt.Errorf("migration_cleanup_get_dataset_%s_returned_nil", dataset)
		}

		entry := cleanupDataset{name: dataset, dataset: ds}
		if task.GuestType == taskModels.GuestTypeJail {
			mountpoint, mountErr := storagezfs.FilesystemMountpoint(ds)
			if mountErr != nil {
				return fmt.Errorf("migration_cleanup_dataset_%s_mountpoint_invalid: %w", dataset, mountErr)
			}
			entry.mountpoint = mountpoint
		}
		resolved = append(resolved, entry)
	}

	if task.GuestType == taskModels.GuestTypeJail {
		for _, entry := range resolved {
			if unmountErr := mount.UnmountDescendants(
				ctx,
				entry.name,
				entry.mountpoint,
			); unmountErr != nil {
				return fmt.Errorf(
					"migration_cleanup_unmount_dataset_%s_descendants_failed: %w",
					entry.name,
					unmountErr,
				)
			}
		}
	}

	for _, entry := range resolved {
		if destroyErr := entry.dataset.Destroy(ctx, true, false); destroyErr != nil && !isDatasetNotFound(destroyErr) {
			return fmt.Errorf("migration_cleanup_destroy_dataset_%s_failed: %w", entry.name, destroyErr)
		}
	}

	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt != nil {
			var metadataCount int64
			if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", task.GuestID).Count(&metadataCount).Error; err != nil {
				return fmt.Errorf("lookup_vm_metadata_for_retire_failed: %w", err)
			}
			if metadataCount != 0 {
				if retireErr := s.Libvirt.RetireVMLocalMetadata(task.GuestID, false); retireErr != nil {
					return fmt.Errorf("retire_vm_metadata_failed: %w", retireErr)
				}
			}
		}

	case taskModels.GuestTypeJail:
		if s.Jail != nil {
			var metadataCount int64
			if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", task.GuestID).Count(&metadataCount).Error; err != nil {
				return fmt.Errorf("lookup_jail_metadata_for_retire_failed: %w", err)
			}
			if metadataCount != 0 {
				if deleteErr := s.Jail.RetireJailLocalMetadata(ctx, task.GuestID, false); deleteErr != nil {
					return fmt.Errorf("delete_jail_metadata_failed: %w", deleteErr)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}

	return nil
}

func isDatasetNotFound(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())

	return strings.Contains(lower, "dataset does not exist") ||
		strings.Contains(lower, "no such pool")
}

func isMigrationSnapshotNotFound(err error) bool {
	if err == nil {
		return false
	}
	if isDatasetNotFound(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "snapshot does not exist") ||
		strings.Contains(lower, "could not find any snapshots to destroy")
}

func filterParentDatasets(datasets []string) []string {
	if len(datasets) <= 1 {
		return datasets
	}
	out := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		isChild := false
		for _, other := range datasets {
			if ds != other && strings.HasPrefix(ds, other+"/") {
				isChild = true
				break
			}
		}
		if !isChild {
			out = append(out, ds)
		}
	}
	return out
}
