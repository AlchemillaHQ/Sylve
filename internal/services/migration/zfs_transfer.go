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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/alchemillahq/sylve/internal/mountutil"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/zfsutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	goLibvirt "github.com/digitalocean/go-libvirt"
)

func buildClusterSSHArgs(identity *clusterModels.ClusterSSHIdentity, privateKeyPath string) []string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s:%d:%s", identity.SSHHost, identity.SSHPort, strings.TrimSpace(identity.NodeUUID))
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("sylve-migrate-ssh-%x.sock", h.Sum32()))

	args := []string{
		"-n",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "ConnectionAttempts=1",
		"-o", "UpdateHostKeys=no",
		"-o", "ControlMaster=auto",
		"-o", fmt.Sprintf("ControlPath=%s", sockPath),
		"-o", "ControlPersist=120",
	}

	if identity.SSHPort != 0 && identity.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", identity.SSHPort))
	}

	if privateKeyPath != "" {
		args = append(args, "-i", privateKeyPath)
	}

	return args
}

func clusterSSHDestination(identity *clusterModels.ClusterSSHIdentity) (remoteexec.SSHDestination, error) {
	if identity == nil {
		return remoteexec.SSHDestination{}, fmt.Errorf("cluster_ssh_identity_required")
	}
	if identity.SSHPort < 0 || identity.SSHPort > 65535 {
		return remoteexec.SSHDestination{}, fmt.Errorf("invalid_cluster_ssh_port")
	}
	destination := strings.TrimSpace(identity.SSHHost)
	if user := strings.TrimSpace(identity.SSHUser); user != "" {
		destination = user + "@" + destination
	}
	parsed, err := remoteexec.ParseSSHDestination(destination)
	if err != nil {
		return remoteexec.SSHDestination{}, fmt.Errorf("invalid_cluster_ssh_destination: %w", err)
	}
	return parsed, nil
}

func clusterRemoteCommandArgs(
	identity *clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
	readsStdin bool,
	commandKind string,
	dataset string,
	argv ...string,
) ([]string, error) {
	destination, err := clusterSSHDestination(identity)
	if err != nil {
		return nil, err
	}
	if dataset != "" {
		parsedDataset, err := remoteexec.ParseZFSDataset(dataset)
		if err != nil {
			return nil, fmt.Errorf("invalid_remote_dataset: %w", err)
		}
		dataset = parsedDataset.String()
	}
	command, err := remoteexec.NewCommand(argv...)
	if err != nil {
		return nil, err
	}
	args, err := command.SSHArgs(buildClusterSSHArgs(identity, privateKeyPath), destination, readsStdin)
	if err != nil {
		return nil, err
	}
	port := identity.SSHPort
	if port == 0 {
		port = 22
	}
	event := logger.L.Debug().
		Str("command_kind", commandKind).
		Str("target_node_id", strings.TrimSpace(identity.NodeUUID)).
		Str("ssh_host", destination.String()).
		Int("ssh_port", port)
	if dataset != "" {
		event.Str("dataset", dataset)
	}
	event.Msg("remote_command_execute")
	return args, nil
}

func runClusterRemoteCommand(
	ctx context.Context,
	identity *clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
	commandKind string,
	dataset string,
	argv ...string,
) (string, error) {
	args, err := clusterRemoteCommandArgs(identity, privateKeyPath, false, commandKind, dataset, argv...)
	if err != nil {
		return "", err
	}
	return utils.RunCommandWithContext(ctx, "ssh", args...)
}

// countingWriter wraps an io.Writer and atomically tracks bytes written.
type countingWriter struct {
	w         io.Writer
	bytesSent *uint64
}

type targetMigrationImportReceipt struct {
	Status             string   `json:"status"`
	Message            string   `json:"message"`
	Warnings           []string `json:"warnings"`
	GuestID            uint     `json:"guestId"`
	OperationToken     string   `json:"operationToken"`
	StartGuest         *bool    `json:"startGuest"`
	SourceDatasetRoots []string `json:"sourceDatasetRoots"`
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	atomic.AddUint64(cw.bytesSent, uint64(n))
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
	if err := s.requireTargetGuestRecordAbsent(ctx, targetNode, task.GuestID); err != nil {
		return err
	}

	identity, err := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if err != nil {
		return fmt.Errorf("target_ssh_identity_unavailable: %w", err)
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_key_unavailable: %w", err)
	}

	if _, err := runClusterRemoteCommand(ctx, identity, privateKeyPath, "zfs.version", "", "zfs", "version"); err != nil {
		return fmt.Errorf("%w: %v", ErrSSHUnreachable, err)
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

func (s *Service) phaseInitialReplication(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	return s.replicateGuestDatasets(ctx, mp, task, false)
}

func (s *Service) phaseFinalSync(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	return s.replicateGuestDatasets(ctx, mp, task, true)
}

func (s *Service) replicateGuestDatasets(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask, incremental bool) error {
	if mp == nil {
		return fmt.Errorf("migration_payload_required")
	}
	resolved, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		return fmt.Errorf("resolve_datasets_failed: %w", err)
	}
	resolved = filterParentDatasets(normalizedMigrationDatasetRootManifest(resolved))
	datasets := resolved
	if len(mp.SourceDatasetRoots) > 0 {
		datasets = filterParentDatasets(normalizedMigrationDatasetRootManifest(mp.SourceDatasetRoots))
		if !sameMigrationDatasetRootManifest(datasets, resolved) {
			return fmt.Errorf("migration_source_dataset_roots_changed")
		}
	}
	if len(datasets) == 0 {
		return fmt.Errorf("no_datasets_found_for_guest")
	}

	for _, dataset := range datasets {
		if !isCanonicalMigrationGuestDataset(dataset, task.GuestType, task.GuestID) {
			return fmt.Errorf("migration_source_dataset_root_invalid: %s", dataset)
		}
	}

	var metadataWriter func(uint) error
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt == nil {
			return fmt.Errorf("vm_runtime_service_unavailable")
		}
		metadataWriter = s.Libvirt.WriteVMJson
	case taskModels.GuestTypeJail:
		if s.Jail == nil {
			return fmt.Errorf("jail_runtime_service_unavailable")
		}
		metadataWriter = s.Jail.WriteJailJSON
	}
	if err := flushMigrationGuestMetadata(task.GuestType, task.GuestID, metadataWriter); err != nil {
		return err
	}

	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", mp.TargetNodeUUID).First(&targetNode).Error; err != nil {
		return fmt.Errorf("target_node_not_found: %w", err)
	}

	identity, err := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if err != nil {
		return fmt.Errorf("target_ssh_identity_unavailable: %w", err)
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_key_unavailable: %w", err)
	}

	snapSuffix := fmt.Sprintf("initial-%d", time.Now().Unix())
	if incremental {
		snapSuffix = fmt.Sprintf("final-%d", time.Now().Unix())
	}

	if !incremental {
		for _, dataset := range datasets {
			if err := s.destroyLocalMigrationSnapshots(ctx, dataset); err != nil {
				return fmt.Errorf("destroy_previous_migration_snapshots_%s_failed: %w", dataset, err)
			}
		}
	}

	for _, dataset := range datasets {
		snapName := fmt.Sprintf("%s-%s", migrationSnapPrefix, snapSuffix)

		mp.PhaseMessage = fmt.Sprintf("replicating_dataset: %s", dataset)
		s.updateTaskPhase(task.ID, *mp)

		currentPhase := mp.Phase
		taskID := task.ID
		progressFn := func(ds string, total, sent uint64) {
			s.writeTransferProgress(taskID, ds, currentPhase, total, sent)
		}

		if err := s.sendDatasetToNode(ctx, dataset, snapName, identity, privateKeyPath, incremental, progressFn, taskID); err != nil {
			if isExplicitMigrationCancellation(err) {
				return err
			}
			return fmt.Errorf("replicate_dataset_%s_failed: %w", dataset, err)
		}
	}

	return nil
}

func (s *Service) sendDatasetToNode(
	ctx context.Context,
	dataset string,
	snapName string,
	identity *clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
	incremental bool,
	progressFn func(dataset string, totalBytes, sentBytes uint64),
	taskID uint,
) error {
	parsedDataset, err := remoteexec.ParseZFSDataset(dataset)
	if err != nil {
		return fmt.Errorf("migration_dataset_invalid: %w", err)
	}
	parsedSnapshot, err := remoteexec.ParseZFSSnapshotName(snapName)
	if err != nil || !isMigrationOwnedSnapshot(parsedSnapshot.String()) {
		return fmt.Errorf("migration_snapshot_invalid")
	}
	dataset = parsedDataset.String()
	snapName = parsedSnapshot.String()
	commonSnap := ""
	if incremental {
		prevSnaps, snapErr := s.listMigrationSnapshots(ctx, dataset)
		if snapErr != nil {
			return fmt.Errorf("list_migration_snapshots_failed: %w", snapErr)
		}
		remoteSnaps, remoteErr := s.listRemoteMigrationSnapshots(ctx, dataset, identity, privateKeyPath)
		if remoteErr != nil {
			return fmt.Errorf("list_remote_migration_snapshots_failed: %w", remoteErr)
		}
		commonSnap = latestCommonMigrationSnapshot(prevSnaps, remoteSnaps)
		if commonSnap == "" {
			return fmt.Errorf("migration_incremental_common_snapshot_missing: %s", dataset)
		}
	}

	if !incremental {
		output, err := runClusterRemoteCommand(
			ctx, identity, privateKeyPath, "zfs.destroy", dataset,
			"zfs", "destroy", "-rf", dataset,
		)
		if err != nil {
			if isDatasetNotFound(err) {
				logger.L.Debug().
					Str("dataset", dataset).
					Str("host", identity.SSHHost).
					Msg("migration_target_dataset_already_absent")

			} else {
				return fmt.Errorf("target_dataset_destroy_failed_on_%s: %s: %w",
					identity.SSHHost, strings.TrimSpace(output), err)
			}
		}

		if verifyOut, verifyErr := runClusterRemoteCommand(
			ctx, identity, privateKeyPath, "zfs.list", dataset,
			"zfs", "list", "-H", dataset,
		); verifyErr == nil {
			return fmt.Errorf("target_dataset_still_exists_on_%s_after_destroy: %s",
				identity.SSHHost, strings.TrimSpace(verifyOut))
		}
	}

	fullSnap := dataset + "@" + snapName
	if _, err := s.GZFS.ZFS.Snapshot(ctx, dataset, snapName, true); err != nil {
		return fmt.Errorf("snapshot_failed: %w", err)
	}

	sendArgs := []string{"send", "-P", "-R", "-L", "-c", "-e"}
	if commonSnap != "" {
		sendArgs = append(sendArgs, "-i", "@"+commonSnap)
	}
	sendArgs = append(sendArgs, fullSnap)

	recvArgs, err := clusterRemoteCommandArgs(
		identity, privateKeyPath, true, "zfs.recv", dataset,
		"zfs", "recv", "-u", "-x", "mountpoint", "-o", "canmount=noauto", "-F", dataset,
	)
	if err != nil {
		return fmt.Errorf("migration_receive_command_invalid: %w", err)
	}

	sendCmd := exec.CommandContext(ctx, "zfs", sendArgs...)
	recvCmd := exec.CommandContext(ctx, "ssh", recvArgs...)

	pr, pw := io.Pipe()

	// countingWriter tracks bytes flowing through the pipe for real progress.
	var bytesSent uint64
	cw := &countingWriter{w: pw, bytesSent: &bytesSent}
	sendCmd.Stdout = cw
	recvCmd.Stdin = pr

	// Read zfs send -P first stderr line to capture the estimated total size.
	sendStderrPr, sendStderrPw := io.Pipe()
	sendCmd.Stderr = sendStderrPw
	var sendStderrLines []string
	var fullSendTotal uint64
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(sendStderrPr)
		for scanner.Scan() {
			line := scanner.Text()
			sendStderrLines = append(sendStderrLines, line)
			// Capture estimated total from first progress line only.
			if atomic.LoadUint64(&fullSendTotal) == 0 {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					if v, err := strconv.ParseUint(fields[2], 10, 64); err == nil && v > 0 {
						atomic.StoreUint64(&fullSendTotal, v)
					}
				}
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
				total := atomic.LoadUint64(&fullSendTotal)
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
					total := atomic.LoadUint64(&fullSendTotal)
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

	var recvStderr bytes.Buffer
	recvCmd.Stderr = &recvStderr

	if err := sendCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		sendStderrPw.Close()
		return fmt.Errorf("zfs_send_start_failed: %w", err)
	}
	if err := recvCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		sendStderrPw.Close()
		sendCmd.Wait()
		return fmt.Errorf("ssh_recv_start_failed: %w", err)
	}

	// Both commands must be started before the watcher can read or kill their
	// processes. Keep it alive until both commands have exited so cancellation
	// can still interrupt a receiver that is draining after zfs send completes.
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
					recvCmd.Process.Kill()
					return
				}
				cancelled.Store(true)
				logger.L.Warn().Uint("task_id", taskID).Str("dataset", dataset).
					Msg("migration_cancelled_during_transfer_killing_children")
				sendCmd.Process.Kill()
				recvCmd.Process.Kill()
				return
			}
		}
	}()

	var sendErr error
	done := make(chan struct{})
	go func() {
		sendErr = sendCmd.Wait()
		pw.Close()
		sendStderrPw.Close()
		close(done)
	}()

	recvErr := recvCmd.Wait()
	pr.Close()
	<-done
	close(transferDone)
	<-stderrDone
	<-progressDone
	<-cancelDone

	if pollErr := cancellationPollErr.Load(); pollErr != nil {
		return pollErr.(error)
	}

	// If the cancel goroutine killed our processes, return a clean cancel error.
	if cancelled.Load() {
		return fmt.Errorf("migration_cancelled")
	}

	if recvErr != nil {
		return fmt.Errorf("recv_failed_on_%s: %s: %w",
			identity.SSHHost, recvStderr.String(), recvErr)
	}
	if sendErr != nil {
		errStr := strings.Join(sendStderrLines, "\n")
		return fmt.Errorf("send_failed: %s: %w", errStr, sendErr)
	}

	return nil
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

func (s *Service) listRemoteMigrationSnapshots(
	ctx context.Context,
	dataset string,
	identity *clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
) ([]string, error) {
	parsedDataset, err := remoteexec.ParseZFSDataset(dataset)
	if err != nil {
		return nil, fmt.Errorf("migration_dataset_invalid: %w", err)
	}
	dataset = parsedDataset.String()
	output, err := runClusterRemoteCommand(
		ctx, identity, privateKeyPath, "zfs.list", dataset,
		"zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-r", dataset,
	)
	if err != nil {
		return nil, err
	}
	prefix := dataset + "@" + migrationSnapPrefix
	var snapshots []string
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		short := strings.TrimPrefix(name, dataset+"@")
		if isMigrationOwnedSnapshot(short) {
			snapshots = append(snapshots, short)
		}
	}
	return snapshots, nil
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

func migrationSnapshotPathsWithinRoot(root, output string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}

	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if !isMigrationSnapshotPathWithinRoot(root, name) {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
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

func (s *Service) destroyRemoteMigrationSnapshots(
	ctx context.Context,
	root string,
	identity *clusterModels.ClusterSSHIdentity,
	privateKeyPath string,
) error {
	if identity == nil {
		return fmt.Errorf("migration_snapshot_cleanup_target_identity_unavailable")
	}
	parsedRoot, err := remoteexec.ParseZFSDataset(root)
	if err != nil {
		return fmt.Errorf("migration_snapshot_cleanup_root_invalid: %w", err)
	}
	root = parsedRoot.String()
	output, err := runClusterRemoteCommand(
		ctx, identity, privateKeyPath, "zfs.list", root,
		"zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-r", root,
	)
	if err != nil {
		if isDatasetNotFound(errors.New(output + ": " + err.Error())) {
			return nil
		}
		return err
	}

	var cleanupErrs []error
	for _, snapshot := range migrationSnapshotPathsWithinRoot(root, output) {
		parsedSnapshot, parseErr := remoteexec.ParseZFSSnapshot(snapshot)
		if parseErr != nil || !parsedSnapshot.Dataset().Within(parsedRoot) {
			continue
		}
		destroyOutput, destroyErr := runClusterRemoteCommand(
			ctx, identity, privateKeyPath, "zfs.destroy", parsedSnapshot.Dataset().String(),
			"zfs", "destroy", parsedSnapshot.String(),
		)
		if destroyErr != nil && !isMigrationSnapshotNotFound(errors.New(destroyOutput+": "+destroyErr.Error())) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy_remote_migration_snapshot_%s_failed: %w", snapshot, destroyErr))
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

	var targetNode clusterModels.ClusterNode
	if err := s.DB.WithContext(ctx).Where("node_uuid = ?", payload.TargetNodeUUID).First(&targetNode).Error; err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("migration_cleanup_target_node_unavailable: %w", err))
		return errors.Join(cleanupErrs...)
	}
	identity, err := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("migration_cleanup_target_identity_unavailable: %w", err))
		return errors.Join(cleanupErrs...)
	}
	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("migration_cleanup_cluster_key_unavailable: %w", err))
		return errors.Join(cleanupErrs...)
	}
	for _, root := range roots {
		if err := s.destroyRemoteMigrationSnapshots(ctx, root, identity, privateKeyPath); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup_remote_migration_snapshots_%s_failed: %w", root, err))
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

	var sourceActive func() (bool, error)
	var stopSource func() error
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt == nil {
			return fmt.Errorf("vm_runtime_service_unavailable")
		}
		sourceActive = func() (bool, error) {
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
		}
		stopSource = func() error { return s.Libvirt.PerformAction(task.GuestID, "stop") }
	case taskModels.GuestTypeJail:
		if s.Jail == nil {
			return fmt.Errorf("jail_runtime_service_unavailable")
		}
		sourceActive = func() (bool, error) {
			active, err := s.Jail.IsJailRunning(task.GuestID)
			if err != nil {
				return false, fmt.Errorf("jail_state_check_failed: %w", err)
			}
			return active, nil
		}
		stopSource = func() error { return s.Jail.JailAction(int(task.GuestID), "stop") }
	default:
		return fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}

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

	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", mp.TargetNodeUUID).First(&targetNode).Error; err != nil {
		return fmt.Errorf("target_node_not_found: %w", err)
	}

	clusterToken, err := s.Cluster.AuthService.CreateInternalClusterJWT("migration")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	var url string
	if task.GuestType == taskModels.GuestTypeVM {
		url = fmt.Sprintf("https://%s/api/intra-cluster/migration/import-vm", targetNode.API)
	} else {
		url = fmt.Sprintf("https://%s/api/intra-cluster/migration/import-jail", targetNode.API)
	}

	body := map[string]any{
		"guestId":            task.GuestID,
		"operationToken":     strings.TrimSpace(operationToken),
		"startGuest":         *mp.OriginalRunning,
		"sourceDatasetRoots": append([]string(nil), mp.SourceDatasetRoots...),
	}

	bodyBytes, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return fmt.Errorf("marshal_import_payload_failed: %w", marshalErr)
	}

	respBody, respStatus, err := utils.HTTPPostJSONWithTimeout(url, bodyBytes, headers, 120*time.Second)
	if err != nil {
		return fmt.Errorf("import_on_target_failed: %w", err)
	}

	if respStatus >= 300 {
		return fmt.Errorf("import_on_target_returned_http_%d: %s", respStatus, string(respBody))
	}

	importResp, err := validateTargetMigrationImportReceipt(
		respBody, task.GuestID, operationToken, *mp.OriginalRunning, mp.SourceDatasetRoots,
	)
	if err != nil {
		return err
	}
	if len(importResp.Warnings) > 0 {
		mp.Warnings = importResp.Warnings
		s.updateTaskPhase(task.ID, *mp)
		logger.L.Info().Strs("warnings", importResp.Warnings).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_import_warnings")
	}

	return nil
}

func isMigrationOwnedSnapshot(shortName string) bool {
	name := strings.TrimSpace(shortName)
	suffix := strings.TrimPrefix(name, migrationSnapPrefix+"-")
	if suffix == name {
		return false
	}
	for _, phasePrefix := range []string{"initial-", "final-", "pre-migration-"} {
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
	raw []byte,
	guestID uint,
	operationToken string,
	startGuest bool,
	sourceDatasetRoots []string,
) (targetMigrationImportReceipt, error) {
	var receipt targetMigrationImportReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("import_on_target_receipt_invalid: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(receipt.Status), "success") ||
		receipt.GuestID != guestID ||
		strings.TrimSpace(receipt.OperationToken) != strings.TrimSpace(operationToken) ||
		receipt.StartGuest == nil || *receipt.StartGuest != startGuest ||
		!sameMigrationDatasetRootManifest(receipt.SourceDatasetRoots, sourceDatasetRoots) {
		return receipt, fmt.Errorf("import_on_target_receipt_mismatch")
	}
	return receipt, nil
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
			mountpoint, mountErr := zfsutil.FilesystemMountpoint(ds)
			if mountErr != nil {
				return fmt.Errorf("migration_cleanup_dataset_%s_mountpoint_invalid: %w", dataset, mountErr)
			}
			entry.mountpoint = mountpoint
		}
		resolved = append(resolved, entry)
	}

	if task.GuestType == taskModels.GuestTypeJail {
		for _, entry := range resolved {
			if unmountErr := mountutil.UnmountDescendants(
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
