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
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
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

// countingWriter wraps an io.Writer and atomically tracks bytes written.
type countingWriter struct {
	w         io.Writer
	bytesSent *uint64
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

	identity, err := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if err != nil {
		return fmt.Errorf("target_ssh_identity_unavailable: %w", err)
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_key_unavailable: %w", err)
	}

	sshArgs := buildClusterSSHArgs(identity, privateKeyPath)
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost), "zfs", "version")
	if _, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...); err != nil {
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
	datasets, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		return fmt.Errorf("resolve_datasets_failed: %w", err)
	}
	if len(datasets) == 0 {
		return fmt.Errorf("no_datasets_found_for_guest")
	}

	datasets = filterParentDatasets(datasets)

	if task.GuestType == taskModels.GuestTypeVM {
		if err := s.Libvirt.WriteVMJson(task.GuestID); err != nil {
			logger.L.Warn().Err(err).Uint("rid", task.GuestID).Msg("migration_writevmjson_flush_failed")
		}
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
			snapList, listErr := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset, true)
			if listErr != nil {
				continue
			}
			for _, snap := range snapList {
				if snap == nil {
					continue
				}
				fullName := snap.Name
				atIdx := strings.LastIndex(fullName, "@")
				if atIdx < 0 {
					continue
				}
				shortName := fullName[atIdx+1:]
				if shortName == "" {
					continue
				}
				if strings.HasPrefix(shortName, "ha_") || strings.HasPrefix(shortName, "bk_") || strings.HasPrefix(shortName, migrationSnapPrefix) {
					snap.Destroy(ctx, false, false)
				}
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
			if strings.Contains(err.Error(), "cancelled") {
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
	commonSnap := ""
	if incremental {
		prevSnaps, snapErr := s.listMigrationSnapshots(ctx, dataset)
		if snapErr != nil {
			return fmt.Errorf("list_migration_snapshots_failed: %w", snapErr)
		}
		if len(prevSnaps) >= 1 {
			commonSnap = prevSnaps[len(prevSnaps)-1]
		}
	}

	if !incremental {
		cleanArgs := buildClusterSSHArgs(identity, privateKeyPath)
		destroyArgs := make([]string, 0, len(cleanArgs))
		for _, a := range cleanArgs {
			if a != "-n" {
				destroyArgs = append(destroyArgs, a)
			}
		}
		destroyArgs = append(destroyArgs,
			fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost),
			"zfs", "destroy", "-rf", dataset,
		)

		output, err := utils.RunCommandWithContext(ctx, "ssh", destroyArgs...)
		if err != nil {
			if isDatasetNotFound(err) {
				logger.L.Warn().
					Str("dataset", dataset).
					Str("host", identity.SSHHost).
					Err(err).
					Msg("target_dataset_destroy_failed_due_to_not_found")

			} else {
				return fmt.Errorf("target_dataset_destroy_failed_on_%s: %s: %w",
					identity.SSHHost, strings.TrimSpace(output), err)
			}
		}

		verifyArgs := make([]string, 0, len(cleanArgs))
		for _, a := range cleanArgs {
			if a != "-n" {
				verifyArgs = append(verifyArgs, a)
			}
		}
		verifyArgs = append(verifyArgs,
			fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost),
			"zfs", "list", "-H", dataset,
		)

		if verifyOut, verifyErr := utils.RunCommandWithContext(ctx, "ssh", verifyArgs...); verifyErr == nil {
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

	sshArgs := buildClusterSSHArgs(identity, privateKeyPath)
	recvArgs := make([]string, 0, len(sshArgs)+10)
	for _, a := range sshArgs {
		if a != "-n" {
			recvArgs = append(recvArgs, a)
		}
	}
	recvArgs = append(recvArgs,
		fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost),
		"zfs", "recv", "-u", "-x", "mountpoint", "-o", "canmount=noauto", "-F", dataset,
	)

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

	// Cancel watcher — polls the task's override_requested flag.
	cancelDone := make(chan struct{})
	var cancelled atomic.Bool
	go func() {
		defer close(cancelDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stderrDone:
				return
			case <-ticker.C:
				if s.checkCancelled(taskID) == nil {
					continue
				}
				cancelled.Store(true)
				logger.L.Warn().Uint("task_id", taskID).Str("dataset", dataset).
					Msg("migration_cancelled_during_transfer_killing_children")
				if sendCmd.Process != nil {
					sendCmd.Process.Kill()
				}
				if recvCmd.Process != nil {
					recvCmd.Process.Kill()
				}
				return
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
	<-stderrDone
	<-progressDone
	<-cancelDone

	// If the cancel goroutine killed our processes, return a clean cancel error.
	if cancelled.Load() {
		return fmt.Errorf("migration_transfer_cancelled")
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

// parseSendProgress captures the estimated total size from the first zfs send
// -P progress line. The moving-offset format differs between FreeBSD and Linux,
// so we use countingWriter polling instead for real-time percentage updates.
func (s *Service) parseSendProgress(line string, dataset string, progressFn func(dataset string, totalBytes, sentBytes uint64), fullSendTotal *uint64) {
	if *fullSendTotal > 0 {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return
	}
	if v, err := strconv.ParseUint(fields[2], 10, 64); err == nil && v > 0 {
		*fullSendTotal = v
	}
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
			if short != "" {
				snaps = append(snaps, short)
			}
		}
	}

	return snaps, nil
}

func (s *Service) phaseStopSource(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	isRunning := false

	switch task.GuestType {
	case taskModels.GuestTypeVM:
		state, err := s.Libvirt.GetDomainState(int(task.GuestID))
		if err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "domain not found") {
				return fmt.Errorf("vm_state_check_failed: %w", err)
			}
		}
		if state == 1 {
			isRunning = true
		}
	case taskModels.GuestTypeJail:
		active, err := s.Jail.IsJailActive(task.GuestID)
		if err == nil && active {
			isRunning = true
		}
	}

	if !isRunning {
		mp.PhaseMessage = "guest_already_stopped"
		s.updateTaskPhase(task.ID, *mp)
		return nil
	}

	var stopErr error
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		stopErr = s.Libvirt.PerformAction(task.GuestID, "stop")
	case taskModels.GuestTypeJail:
		stopErr = s.Jail.JailAction(int(task.GuestID), "stop")
	default:
		return fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}

	if stopErr != nil {
		return fmt.Errorf("stop_guest_failed: %w", stopErr)
	}

	time.Sleep(2 * time.Second)

	return nil
}

func (s *Service) phaseStartTarget(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", mp.TargetNodeUUID).First(&targetNode).Error; err != nil {
		return fmt.Errorf("target_node_not_found: %w", err)
	}

	clusterToken, err := s.Cluster.AuthService.CreateInternalClusterJWT("migration", "")
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
		"guestId": task.GuestID,
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

	var importResp struct {
		Status   string   `json:"status"`
		Message  string   `json:"message"`
		Warnings []string `json:"warnings"`
	}
	if jsonErr := json.Unmarshal(respBody, &importResp); jsonErr != nil {
		logger.L.Warn().Err(jsonErr).Str("body", string(respBody)).Msg("failed_to_parse_import_response")
	} else if len(importResp.Warnings) > 0 {
		mp.Warnings = importResp.Warnings
		s.updateTaskPhase(task.ID, *mp)
		logger.L.Info().Strs("warnings", importResp.Warnings).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_import_warnings")
	}

	return nil
}

func (s *Service) phaseCleanupSource(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	datasets, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		return fmt.Errorf("resolve_datasets_for_cleanup_failed: %w", err)
	}

	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.Libvirt != nil {
			if retireErr := s.Libvirt.RetireVMLocalMetadata(task.GuestID, false); retireErr != nil {
				return fmt.Errorf("retire_vm_metadata_failed: %w", retireErr)
			}
		}

		datasets = filterParentDatasets(datasets)
		for _, dataset := range datasets {
			ds, getErr := s.GZFS.ZFS.Get(ctx, dataset, false)
			if getErr != nil {
				if !isDatasetNotFound(getErr) {
					logger.L.Warn().Err(getErr).Str("dataset", dataset).Msg("migration_cleanup_get_dataset_failed")
				}
				continue
			}
			if ds == nil {
				continue
			}
			if destroyErr := ds.Destroy(ctx, true, false); destroyErr != nil {
				logger.L.Warn().Err(destroyErr).Str("dataset", dataset).Msg("migration_cleanup_destroy_dataset_failed")
			}
		}

	case taskModels.GuestTypeJail:
		datasets = filterParentDatasets(datasets)
		for _, dataset := range datasets {
			ds, getErr := s.GZFS.ZFS.Get(ctx, dataset, false)
			if getErr != nil {
				if !isDatasetNotFound(getErr) {
					logger.L.Warn().Err(getErr).Str("dataset", dataset).Msg("migration_cleanup_get_jail_dataset_failed")
				}
				continue
			}
			if ds == nil {
				continue
			}
			if destroyErr := ds.Destroy(ctx, true, false); destroyErr != nil {
				logger.L.Warn().Err(destroyErr).Str("dataset", dataset).Msg("migration_cleanup_destroy_jail_dataset_failed")
			}
		}

		if s.Jail != nil {
			if deleteErr := s.Jail.DeleteJail(ctx, task.GuestID, false, false); deleteErr != nil {
				return fmt.Errorf("delete_jail_metadata_failed: %w", deleteErr)
			}
		}
	}

	return nil
}

func (s *Service) phaseFinalize(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	datasets, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		return fmt.Errorf("resolve_datasets_for_finalize_failed: %w", err)
	}

	datasets = filterParentDatasets(datasets)

	finalSnap := fmt.Sprintf("%s-pre-migration-%d", migrationSnapPrefix, time.Now().Unix())
	for _, dataset := range datasets {
		if _, snapErr := s.GZFS.ZFS.Snapshot(ctx, dataset, finalSnap, true); snapErr != nil {
			if !isDatasetNotFound(snapErr) {
				// Non-critical: source dataset may already be gone
			}
		}
	}

	for _, dataset := range datasets {
		snapList, listErr := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset, true)
		if listErr != nil {
			continue
		}
		for _, snap := range snapList {
			if snap == nil {
				continue
			}
			fullName := snap.Name
			atIdx := strings.LastIndex(fullName, "@")
			if atIdx < 0 {
				continue
			}
			shortName := fullName[atIdx+1:]
			if shortName == "" || !strings.HasPrefix(shortName, migrationSnapPrefix) {
				continue
			}
			if shortName == finalSnap {
				continue
			}
			if destroyErr := snap.Destroy(ctx, false, false); destroyErr != nil {
				logger.L.Warn().
					Str("snapshot", fullName).
					Err(destroyErr).
					Msg("migration_phase_finalize_cleanup_destroy_failed")
			}
		}
	}

	return nil
}

func isDatasetNotFound(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())

	logger.L.Debug().
		Err(err).
		Msg("checking_if_error_is_dataset_not_found")

	return strings.Contains(lower, "dataset does not exist") ||
		strings.Contains(lower, "no such")
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
