// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const haSnapPrefix = "ha_"

func (s *Service) replicationZFSSend(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	snapPrefix string,
	onLine func(string),
) (string, error) {
	if target == nil {
		return "", fmt.Errorf("replication_target_required")
	}
	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return "", fmt.Errorf("source_dataset_required")
	}

	prefix := strings.TrimSpace(snapPrefix)
	if prefix == "" {
		prefix = "ha"
	}
	snapName := zeltaSnapshotName(prefix)

	targetPath := targetDatasetPath(target.BackupRoot, destSuffix)
	if targetPath == "" {
		return "", fmt.Errorf("replication_target_dataset_required")
	}

	var outputLog strings.Builder
	appendLine := func(line string) {
		cleaned := strings.TrimSpace(line)
		if cleaned == "" {
			return
		}
		if outputLog.Len() > 0 {
			outputLog.WriteByte('\n')
		}
		outputLog.WriteString(cleaned)
		if onLine != nil {
			onLine(cleaned)
		}
	}

	if err := s.ensureTargetParentDatasets(ctx, target, targetPath); err != nil {
		appendLine(fmt.Sprintf("ensure_target_parent_datasets_failed: %v", err))
		return "", err
	}

	appendLine(fmt.Sprintf("taking_snapshot: zfs snapshot -r %s@%s", sourceDataset, snapName))
	if err := s.snapshotDatasetRecursive(ctx, sourceDataset, snapName); err != nil {
		appendLine(fmt.Sprintf("snapshot_failed: %v", err))
		return "", fmt.Errorf("replication_snapshot_failed: %w", err)
	}

	commonSnap, err := s.findCommonReplicationSnapshot(ctx, target, sourceDataset, targetPath)
	if err != nil {
		appendLine(fmt.Sprintf("common_snapshot_lookup_failed: %v", err))
	}

	encrypted, encErr := s.isDatasetEncrypted(ctx, sourceDataset)
	if encErr != nil {
		logger.L.Warn().Err(encErr).Str("dataset", sourceDataset).Msg("replication_encryption_check_failed")
	}

	forceRecv := false
	for attempt := 0; attempt < 3; attempt++ {
		out, sendErr := s.runReplicationPipeline(ctx, target, sourceDataset, snapName, commonSnap, targetPath, encrypted, forceRecv)
		if strings.TrimSpace(out) != "" {
			appendLine(out)
		}
		if sendErr == nil {
			break
		}

		if isReplicationResumeStateError(sendErr) {
			appendLine("target_partial_receive_detected_aborting")
			abortOut, abortErr := s.abortTargetResumableReceiveState(ctx, target, destSuffix)
			if strings.TrimSpace(abortOut) != "" {
				appendLine(abortOut)
			}
			if abortErr != nil {
				return outputLog.String(), fmt.Errorf(
					"replication_failed_after_partial_receive_abort_failed: %w (original: %v)",
					abortErr,
					sendErr,
				)
			}
			appendLine("partial_receive_aborted_retrying")
			continue
		}

		if isReplicationTargetModifiedError(sendErr) && !forceRecv {
			appendLine("target_dataset_diverged_retrying_with_force_recv")
			forceRecv = true
			continue
		}

		if !forceRecv && attempt == 0 {
			lowerSend := strings.ToLower(sendErr.Error())
			lowerOut := strings.ToLower(out)
			if strings.Contains(lowerSend, "signal") ||
				strings.Contains(lowerSend, "broken pipe") ||
				strings.Contains(lowerSend, "exit status") ||
				strings.Contains(lowerOut, "cannot receive") ||
				strings.Contains(lowerOut, "failed to read from stream") {
				appendLine("replication_transfer_interrupted_retrying_with_force_recv")
				forceRecv = true
				continue
			}
		}

		if forceRecv && attempt < 2 {
			lowerSend := strings.ToLower(sendErr.Error())
			if strings.Contains(lowerSend, "has snapshots") ||
				strings.Contains(lowerSend, "must destroy") {
				appendLine("replication_target_has_orphan_snapshots_destroying_target")
				if destroyErr := s.destroyTargetDatasetBestEffort(ctx, target, targetPath); destroyErr != nil {
					appendLine(fmt.Sprintf("target_dataset_destroy_failed: %v", destroyErr))
					return outputLog.String(), fmt.Errorf(
						"replication_failed_target_has_orphan_snapshots_destroy_failed: %w (original: %v)",
						destroyErr,
						sendErr,
					)
				}
				appendLine("target_dataset_destroyed_retrying_full_send")
				commonSnap = ""
				forceRecv = true
				continue
			}
		}

		return outputLog.String(), sendErr
	}

	if err := s.ensureTargetReplicationReadonly(ctx, target, targetPath); err != nil {
		logger.L.Warn().
			Err(err).
			Str("target_path", targetPath).
			Msg("replication_target_readonly_hardening_failed")
		appendLine(fmt.Sprintf("readonly_hardening_warning: %v", err))
	}

	return outputLog.String(), nil
}

func (s *Service) runReplicationPipeline(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	snapName string,
	commonSnap string,
	targetPath string,
	encrypted bool,
	forceRecv bool,
) (string, error) {
	var sendArgs []string
	if encrypted {
		sendArgs = append(sendArgs, "send", "--raw", "-P", "-R")
	} else {
		sendArgs = append(sendArgs, "send", "-P", "-R", "-L", "-c", "-e")
	}
	if commonSnap != "" {
		sendArgs = append(sendArgs, "-i", "@"+commonSnap)
	}
	fullSnap := sourceDataset + "@" + snapName
	sendArgs = append(sendArgs, fullSnap)

	sshArgs := s.buildSSHArgs(target)
	recvArgs := make([]string, 0, len(sshArgs)+10)
	for _, a := range sshArgs {
		if a != "-n" {
			recvArgs = append(recvArgs, a)
		}
	}
	recvArgs = append(recvArgs, target.SSHHost, "zfs", "recv", "-u", "-x", "mountpoint", "-o", "canmount=noauto")
	if forceRecv {
		recvArgs = append(recvArgs, "-F")
	}
	recvArgs = append(recvArgs, targetPath)

	sendCmd := exec.CommandContext(ctx, "zfs", sendArgs...)
	recvCmd := exec.CommandContext(ctx, "ssh", recvArgs...)

	pr, pw := io.Pipe()
	sendCmd.Stdout = pw
	recvCmd.Stdin = pr

	var sendStderr bytes.Buffer
	var recvStderr bytes.Buffer
	sendCmd.Stderr = &sendStderr
	recvCmd.Stderr = &recvStderr

	if err := sendCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return "", fmt.Errorf("zfs_send_start_failed: %w", err)
	}
	if err := recvCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		sendCmd.Wait()
		return "", fmt.Errorf("ssh_recv_start_failed: %w", err)
	}

	var sendErr error
	done := make(chan struct{})
	go func() {
		sendErr = sendCmd.Wait()
		pw.Close()
		close(done)
	}()

	recvErr := recvCmd.Wait()
	pr.Close()
	<-done

	var combined strings.Builder
	sendOut := strings.TrimSpace(sendStderr.String())
	recvOut := strings.TrimSpace(recvStderr.String())
	if sendOut != "" {
		combined.WriteString(sendOut)
	}
	if recvOut != "" {
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		combined.WriteString(recvOut)
	}

	if recvErr != nil {
		return combined.String(), fmt.Errorf("%s: %w", combined.String(), recvErr)
	}
	if sendErr != nil {
		return combined.String(), fmt.Errorf("zfs_send_failed: %w", sendErr)
	}

	return combined.String(), nil
}

func (s *Service) findCommonReplicationSnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	targetPath string,
) (string, error) {
	localSnaps, err := s.listHaSnapshotsLocal(ctx, sourceDataset)
	if err != nil {
		return "", fmt.Errorf("list_local_ha_snapshots_failed: %w", err)
	}
	remoteSnaps, err := s.listHaSnapshotsRemote(ctx, target, targetPath)
	if err != nil {
		return "", fmt.Errorf("list_remote_ha_snapshots_failed: %w", err)
	}

	if len(localSnaps) == 0 || len(remoteSnaps) == 0 {
		return "", nil
	}

	remoteSet := make(map[string]struct{}, len(remoteSnaps))
	for _, snap := range remoteSnaps {
		remoteSet[snap] = struct{}{}
	}

	for i := len(localSnaps) - 1; i >= 0; i-- {
		if _, ok := remoteSet[localSnaps[i]]; ok {
			return localSnaps[i], nil
		}
	}

	return "", nil
}

func (s *Service) listHaSnapshotsLocal(ctx context.Context, dataset string) ([]string, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil, nil
	}

	output, err := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-s", "creation", dataset)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") ||
			strings.Contains(strings.ToLower(err.Error()), "no such") {
			return nil, nil
		}
		return nil, err
	}

	return filterHaSnapshots(output, dataset), nil
}

func (s *Service) listHaSnapshotsRemote(ctx context.Context, target *clusterModels.BackupTarget, dataset string) ([]string, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil, nil
	}
	if target == nil {
		return nil, fmt.Errorf("replication_target_required")
	}

	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-s", "creation", dataset)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") ||
			strings.Contains(strings.ToLower(err.Error()), "no such") {
			return nil, nil
		}
		return nil, err
	}

	return filterHaSnapshots(output, dataset), nil
}

func filterHaSnapshots(output, dataset string) []string {
	var snaps []string
	scan := bufio.NewScanner(strings.NewReader(output))
	prefix := dataset + "@" + haSnapPrefix
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		short := strings.TrimPrefix(line, dataset+"@")
		if short != "" {
			snaps = append(snaps, short)
		}
	}
	return snaps
}

func (s *Service) ensureTargetParentDatasets(ctx context.Context, target *clusterModels.BackupTarget, targetPath string) error {
	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return nil
	}

	idx := strings.LastIndex(targetPath, "/")
	if idx <= 0 || idx >= len(targetPath)-1 {
		return nil
	}
	parent := targetPath[:idx]

	exists, _, err := s.remoteDatasetExists(ctx, target, parent)
	if err != nil {
		return fmt.Errorf("check_target_parent_dataset_exists_failed: %w", err)
	}
	if exists {
		return nil
	}

	_, createErr := s.runTargetSSH(ctx, target, "zfs", "create", "-p", "-o", "canmount=noauto", parent)
	if createErr != nil {
		if strings.Contains(strings.ToLower(createErr.Error()), "already exists") {
			return nil
		}
		return fmt.Errorf("create_target_parent_dataset_failed: %w", createErr)
	}

	logger.L.Info().
		Str("parent", parent).
		Str("target", target.SSHHost).
		Msg("replication_target_parent_dataset_created")

	return nil
}

func (s *Service) ensureTargetReplicationReadonly(ctx context.Context, target *clusterModels.BackupTarget, targetPath string) error {
	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return nil
	}

	script := fmt.Sprintf(
		`set -eu
root_ds=%q
zfs set readonly=on "$root_ds"
zfs list -H -o name -r -t filesystem,volume "$root_ds" 2>/dev/null | while read -r ds; do
  [ "$ds" = "$root_ds" ] && continue
  zfs set readonly=on "$ds"
done`,
		targetPath,
	)

	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}

	return nil
}

func (s *Service) snapshotDatasetRecursive(ctx context.Context, dataset, snapName string) error {
	dsSnap := normalizeDatasetPath(dataset) + "@" + snapName
	output, err := utils.RunCommandWithContext(ctx, "zfs", "snapshot", "-r", dsSnap)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func (s *Service) destroyLocalSnapshotBestEffort(ctx context.Context, dataset, snapName string) error {
	dsSnap := normalizeDatasetPath(dataset) + "@" + snapName
	output, err := utils.RunCommandWithContext(ctx, "zfs", "destroy", dsSnap)
	if err != nil {
		lower := strings.ToLower(err.Error() + " " + output)
		if strings.Contains(lower, "dataset does not exist") ||
			strings.Contains(lower, "no such") ||
			strings.Contains(lower, "snapshot not found") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func (s *Service) isDatasetEncrypted(ctx context.Context, dataset string) (bool, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return false, fmt.Errorf("dataset_required")
	}

	output, err := utils.RunCommandWithContext(ctx, "zfs", "get", "-H", "-o", "value", "encryption", dataset)
	if err != nil {
		return false, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}

	val := strings.TrimSpace(output)
	return val != "" && val != "off", nil
}

func (s *Service) retainReplicationSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	keep int,
) error {
	if keep <= 0 {
		keep = defaultReplicationPruneKeepLast
	}

	sourceSnaps, err := s.listHaSnapshotsLocal(ctx, sourceDataset)
	if err != nil {
		return fmt.Errorf("list_source_snapshots_failed: %w", err)
	}

	targetPath := targetDatasetPath(target.BackupRoot, destSuffix)
	targetSnaps, err := s.listHaSnapshotsRemote(ctx, target, targetPath)
	if err != nil {
		return fmt.Errorf("list_target_snapshots_failed: %w", err)
	}

	common := intersectSnapshotNames(sourceSnaps, targetSnaps)
	if len(common) <= keep {
		return nil
	}

	stale := common[:len(common)-keep]
	var errs []string
	for _, snap := range stale {
		if err := s.destroyLocalSnapshotBestEffort(ctx, sourceDataset, snap); err != nil {
			errs = append(errs, fmt.Sprintf("destroy_source_%s_failed: %v", snap, err))
		}
	}

	if target != nil {
		for _, snap := range stale {
			if err := s.destroyRemoteSnapshotBestEffort(ctx, target, targetPath, snap); err != nil {
				errs = append(errs, fmt.Sprintf("destroy_target_%s_failed: %v", snap, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("replication_retention_failed: %s", strings.Join(errs, "; "))
	}

	return nil
}

func intersectSnapshotNames(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}

	var common []string
	for _, s := range a {
		if _, ok := set[s]; ok {
			common = append(common, s)
		}
	}

	sort.Strings(common)
	return common
}

func (s *Service) destroyRemoteSnapshotBestEffort(ctx context.Context, target *clusterModels.BackupTarget, dataset, snapName string) error {
	if target == nil {
		return nil
	}

	snap := normalizeDatasetPath(dataset) + "@" + snapName
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "destroy", snap)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		lower := strings.ToLower(err.Error() + " " + output)
		if strings.Contains(lower, "dataset does not exist") ||
			strings.Contains(lower, "no such") ||
			strings.Contains(lower, "snapshot not found") ||
			strings.Contains(lower, "does not exist") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}

	return nil
}

func (s *Service) destroyTargetDatasetBestEffort(ctx context.Context, target *clusterModels.BackupTarget, dataset string) error {
	if target == nil {
		return nil
	}
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "destroy", "-r", dataset)
	output, err := utils.RunCommandWithContext(timeoutCtx, "ssh", sshArgs...)
	if err != nil {
		lower := strings.ToLower(err.Error() + " " + output)
		if strings.Contains(lower, "dataset does not exist") ||
			strings.Contains(lower, "no such") ||
			strings.Contains(lower, "does not exist") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return nil
}
