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
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const replicationJobQueueName = "zelta-replication-run"

const (
	defaultReplicationPruneKeepLast  = 64
	defaultReplicationLineageKeepOld = 2
)

type replicationJobPayload struct {
	PolicyID uint `json:"policy_id"`
}

type ReplicationEventProgress struct {
	Event           *clusterModels.ReplicationEvent `json:"event"`
	MovedBytes      *uint64                         `json:"movedBytes"`
	TotalBytes      *uint64                         `json:"totalBytes"`
	ProgressPercent *float64                        `json:"progressPercent"`
}

func (s *Service) registerReplicationJob() {
	db.QueueRegisterJSON(replicationJobQueueName, func(ctx context.Context, payload replicationJobPayload) error {
		if payload.PolicyID == 0 {
			return fmt.Errorf("invalid_policy_id_in_queue_payload")
		}

		policy, err := s.Cluster.GetReplicationPolicyByID(payload.PolicyID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_not_found")
			return err
		}

		if err := s.runReplicationPolicy(ctx, policy); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", payload.PolicyID).Msg("queued_replication_policy_failed")
			return err
		}
		return nil
	})
}

func (s *Service) EnqueueReplicationPolicyRun(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}
	if !s.acquireReplication(policyID) {
		return fmt.Errorf("replication_policy_already_running")
	}
	s.releaseReplication(policyID)

	_ = policy
	return db.EnqueueJSON(ctx, replicationJobQueueName, replicationJobPayload{PolicyID: policyID})
}

func (s *Service) StartReplicationScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastSSHSync := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.Cluster != nil && time.Since(lastSSHSync) > 30*time.Second {
				if err := s.Cluster.EnsureAndPublishLocalSSHIdentity(); err != nil {
					logger.L.Warn().Err(err).Msg("cluster_ssh_identity_sync_failed")
				}
				lastSSHSync = time.Now()
			}

			if err := s.selfFenceExpiredLeases(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("replication_self_fence_check_failed")
			}

			if err := s.runReplicationSchedulerTick(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("replication_scheduler_tick_failed")
			}

			if s.Cluster != nil && s.Cluster.Raft != nil && s.Cluster.Raft.State() == raft.Leader {
				if err := s.runFailoverControllerTick(ctx); err != nil {
					logger.L.Warn().Err(err).Msg("replication_failover_tick_failed")
				}
			}
		}
	}
}

func (s *Service) runReplicationSchedulerTick(ctx context.Context) error {
	if s.DB == nil || s.Cluster == nil {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Preload("Targets").Where("enabled = ? AND COALESCE(cron_expr, '') != ''", true).Find(&policies).Error; err != nil {
		return err
	}

	now := time.Now().UTC()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	for i := range policies {
		policy := policies[i]
		runnerNodeID := s.replicationRunnerNodeID(&policy)
		if runnerNodeID != "" && localNodeID != "" && runnerNodeID != localNodeID {
			continue
		}
		if runnerNodeID == "" && s.Cluster.Raft != nil && s.Cluster.Raft.State() != raft.Leader {
			continue
		}

		nextAt, err := nextRunTime(policy.CronExpr, now)
		if err != nil {
			_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(map[string]any{
				"last_status": "failed",
				"last_error":  "invalid_cron_expr",
				"next_run_at": nil,
			}).Error
			continue
		}

		if policy.NextRunAt == nil {
			_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Update("next_run_at", nextAt).Error
			continue
		}

		if now.Before(*policy.NextRunAt) {
			continue
		}

		if err := s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Update("next_run_at", nextAt).Error; err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("failed_to_update_replication_policy_next_run")
			continue
		}

		enqueueCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := db.EnqueueJSON(enqueueCtx, replicationJobQueueName, replicationJobPayload{PolicyID: policy.ID}); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("failed_to_enqueue_replication_policy")
		}
		cancel()
	}

	return nil
}

func (s *Service) replicationRunnerNodeID(policy *clusterModels.ReplicationPolicy) string {
	if policy == nil {
		return ""
	}
	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModePinned {
		return strings.TrimSpace(policy.SourceNodeID)
	}
	return strings.TrimSpace(policy.ActiveNodeID)
}

func (s *Service) runReplicationPolicy(ctx context.Context, policy *clusterModels.ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("invalid_policy")
	}
	if !s.acquireReplication(policy.ID) {
		return fmt.Errorf("replication_policy_already_running")
	}
	defer s.releaseReplication(policy.ID)

	localNodeID := ""
	if s.Cluster != nil {
		localNodeID = strings.TrimSpace(s.Cluster.LocalNodeID())
	}

	runner := s.replicationRunnerNodeID(policy)
	if runner != "" && localNodeID != "" && runner != localNodeID {
		return fmt.Errorf("policy_runner_mismatch")
	}

	sourceDatasets, err := s.replicationSourceDatasets(ctx, policy)
	if err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}
	if len(sourceDatasets) == 0 {
		runErr := fmt.Errorf("no_source_datasets_found")
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	identities, err := s.Cluster.ListClusterSSHIdentities()
	if err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}
	identityByNode := make(map[string]clusterModels.ClusterSSHIdentity, len(identities))
	for _, identity := range identities {
		identityByNode[strings.TrimSpace(identity.NodeUUID)] = identity
	}

	nodes, _ := s.Cluster.Nodes()
	statusByNode := make(map[string]string, len(nodes))
	for _, node := range nodes {
		statusByNode[strings.TrimSpace(node.NodeUUID)] = strings.TrimSpace(strings.ToLower(node.Status))
	}

	event := clusterModels.ReplicationEvent{
		PolicyID:     &policy.ID,
		EventType:    "replication",
		Status:       "running",
		SourceNodeID: localNodeID,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		StartedAt:    time.Now().UTC(),
		Message:      "replication_run_started",
	}
	if err := s.DB.Create(&event).Error; err != nil {
		s.updateReplicationPolicyResult(policy, err)
		return err
	}

	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		runErr := fmt.Errorf("cluster_ssh_private_key_path_failed: %w", err)
		s.finalizeReplicationEvent(&event, runErr)
		s.updateReplicationPolicyResult(policy, runErr)
		return runErr
	}

	targets := append([]clusterModels.ReplicationPolicyTarget{}, policy.Targets...)
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Weight == targets[j].Weight {
			return targets[i].NodeID < targets[j].NodeID
		}
		return targets[i].Weight > targets[j].Weight
	})

	var runErr error
	eligibleTargets := 0
	skippedOffline := 0
	skippedNoIdentity := 0
	attemptedTransfers := 0
	for _, target := range targets {
		targetNodeID := strings.TrimSpace(target.NodeID)
		if targetNodeID == "" || targetNodeID == localNodeID {
			continue
		}
		if status, ok := statusByNode[targetNodeID]; ok && status != "online" {
			skippedOffline++
			continue
		}

		identity, ok := identityByNode[targetNodeID]
		if !ok {
			skippedNoIdentity++
			continue
		}
		eligibleTargets++

		for _, sourceDataset := range sourceDatasets {
			attemptedTransfers++
			backupRoot, destSuffix := splitDatasetForTarget(sourceDataset)
			targetSpec := &clusterModels.BackupTarget{
				SSHHost:    fmt.Sprintf("%s@%s", strings.TrimSpace(identity.SSHUser), strings.TrimSpace(identity.SSHHost)),
				SSHPort:    identity.SSHPort,
				SSHKeyPath: privateKeyPath,
				BackupRoot: backupRoot,
				Enabled:    true,
			}
			event.TargetNodeID = targetNodeID
			event.Message = fmt.Sprintf("replicating_%s_to_%s", sourceDataset, targetNodeID)
			_ = s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
				"target_node_id": targetNodeID,
				"message":        event.Message,
			}).Error

			out, err := s.replicateWithEventProgress(ctx, targetSpec, sourceDataset, destSuffix, event.ID)
			if strings.TrimSpace(out) != "" {
				_ = s.AppendReplicationEventOutput(event.ID, out)
			}
			if err != nil {
				if isReplicationTargetModifiedError(err) {
					_ = s.AppendReplicationEventOutput(event.ID, "target_dataset_diverged_attempting_zelta_rotate")
					rotateOut, rotateErr := s.RotateWithTargetAndPrefix(ctx, targetSpec, sourceDataset, destSuffix, "ha")
					if strings.TrimSpace(rotateOut) != "" {
						_ = s.AppendReplicationEventOutput(event.ID, rotateOut)
					}
					if rotateErr != nil {
						runErr = fmt.Errorf(
							"replication_to_target_%s_failed_after_diverged_target_rotate_failed: %w (original: %v)",
							targetNodeID,
							rotateErr,
							err,
						)
						break
					}

					retryOut, retryErr := s.replicateWithEventProgress(ctx, targetSpec, sourceDataset, destSuffix, event.ID)
					if strings.TrimSpace(retryOut) != "" {
						_ = s.AppendReplicationEventOutput(event.ID, retryOut)
					}
					if retryErr != nil {
						runErr = fmt.Errorf(
							"replication_to_target_%s_failed_after_diverged_target_rotate: %w (original: %v)",
							targetNodeID,
							retryErr,
							err,
						)
						break
					}
				} else {
					runErr = fmt.Errorf("replication_to_target_%s_failed: %w", targetNodeID, err)
					break
				}
			}

			if retentionErr := s.applyReplicationRetention(ctx, targetSpec, sourceDataset, destSuffix, event.ID); retentionErr != nil {
				logger.L.Warn().
					Err(retentionErr).
					Uint("policy_id", policy.ID).
					Str("source_dataset", sourceDataset).
					Str("target_node_id", targetNodeID).
					Msg("replication_retention_post_run_failed")
				_ = s.AppendReplicationEventOutput(event.ID, fmt.Sprintf("replication_retention_warning: %v", retentionErr))
			}
		}

		if runErr != nil {
			break
		}
	}

	if runErr == nil {
		if eligibleTargets == 0 {
			runErr = fmt.Errorf("no_eligible_replication_targets (offline=%d missing_identity=%d)", skippedOffline, skippedNoIdentity)
		} else if attemptedTransfers == 0 {
			runErr = fmt.Errorf("no_replication_transfers_executed")
		}
	}

	s.finalizeReplicationEvent(&event, runErr)
	s.updateReplicationPolicyResult(policy, runErr)

	return runErr
}

func splitDatasetForTarget(dataset string) (string, string) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return "zroot", "sylve"
	}

	idx := strings.Index(dataset, "/")
	if idx <= 0 || idx >= len(dataset)-1 {
		return dataset, ""
	}
	return dataset[:idx], dataset[idx+1:]
}

func targetDatasetPath(root, suffix string) string {
	root = normalizeDatasetPath(root)
	suffix = normalizeDatasetPath(suffix)
	if root == "" {
		return suffix
	}
	if suffix == "" {
		return root
	}
	return root + "/" + suffix
}

func (s *Service) applyReplicationRetention(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	eventID uint,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	retentionErrors := make([]string, 0)

	pruneCandidates, pruneOutput, pruneErr := s.PruneCandidatesWithTarget(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		defaultReplicationPruneKeepLast,
	)
	if strings.TrimSpace(pruneOutput) != "" {
		_ = s.AppendReplicationEventOutput(eventID, pruneOutput)
	}
	if pruneErr != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("source_prune_scan_failed: %v", pruneErr))
	} else if len(pruneCandidates) > 0 {
		if err := s.DestroySnapshots(ctx, pruneCandidates); err != nil {
			retentionErrors = append(retentionErrors, fmt.Sprintf("source_prune_destroy_failed: %v", err))
		} else {
			_ = s.AppendReplicationEventOutput(eventID, fmt.Sprintf("source_prune_completed: %d", len(pruneCandidates)))
		}
	}

	targetPruneCandidates, targetPruneOutput, targetPruneErr := s.PruneTargetCandidatesWithSource(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		defaultReplicationPruneKeepLast,
	)
	if strings.TrimSpace(targetPruneOutput) != "" {
		_ = s.AppendReplicationEventOutput(eventID, targetPruneOutput)
	}
	if targetPruneErr != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("target_prune_scan_failed: %v", targetPruneErr))
	} else if len(targetPruneCandidates) > 0 {
		if err := s.DestroyTargetSnapshotsByName(ctx, target, targetPruneCandidates); err != nil {
			retentionErrors = append(retentionErrors, fmt.Sprintf("target_prune_destroy_failed: %v", err))
		} else {
			_ = s.AppendReplicationEventOutput(eventID, fmt.Sprintf("target_prune_completed: %d", len(targetPruneCandidates)))
		}
	}

	if err := s.trimLocalReplicationLineageDatasets(ctx, sourceDataset, defaultReplicationLineageKeepOld); err != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("local_lineage_trim_failed: %v", err))
	}

	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	if err := s.trimRemoteReplicationLineageDatasets(ctx, target, targetDataset, defaultReplicationLineageKeepOld); err != nil {
		retentionErrors = append(retentionErrors, fmt.Sprintf("target_lineage_trim_failed: %v", err))
	}

	if len(retentionErrors) > 0 {
		return errors.New(strings.Join(retentionErrors, "; "))
	}

	return nil
}

func (s *Service) trimLocalReplicationLineageDatasets(
	ctx context.Context,
	rootDataset string,
	keepOutOfBand int,
) error {
	lineageDatasets, err := s.listLocalReplicationLineageDatasets(ctx, rootDataset)
	if err != nil {
		return err
	}

	staleDatasets := staleReplicationLineageDatasets(rootDataset, lineageDatasets, keepOutOfBand)
	for _, dataset := range staleDatasets {
		if err := s.destroyLocalDatasetWithRetry(ctx, dataset, true, 5, 500*time.Millisecond); err != nil {
			return fmt.Errorf("destroy_local_lineage_dataset_%s_failed: %w", dataset, err)
		}
	}

	return nil
}

func (s *Service) trimRemoteReplicationLineageDatasets(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset string,
	keepOutOfBand int,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}

	lineageDatasets, err := s.listRemoteLineageDatasets(ctx, target, remoteDataset)
	if err != nil {
		return err
	}

	staleDatasets := staleReplicationLineageDatasets(remoteDataset, lineageDatasets, keepOutOfBand)
	for _, dataset := range staleDatasets {
		script := fmt.Sprintf(
			`set -eu
ds=%q
if zfs list -H "$ds" >/dev/null 2>&1; then
  zfs destroy -r -f "$ds"
fi`,
			dataset,
		)
		if _, err := s.runTargetSSH(ctx, target, "sh", "-c", script); err != nil {
			return fmt.Errorf("destroy_remote_lineage_dataset_%s_failed: %w", dataset, err)
		}
	}

	return nil
}

func (s *Service) listLocalReplicationLineageDatasets(ctx context.Context, rootDataset string) ([]string, error) {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil, fmt.Errorf("root_dataset_required")
	}

	parent := rootDataset
	leaf := rootDataset
	if idx := strings.LastIndex(rootDataset, "/"); idx > 0 {
		parent = rootDataset[:idx]
		leaf = rootDataset[idx+1:]
	}
	baseLeaf := replicationLineageBaseLeaf(leaf)

	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	results := make([]string, 0)
	add := func(dataset string) {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			return
		}
		if _, ok := seen[dataset]; ok {
			return
		}
		seen[dataset] = struct{}{}
		results = append(results, dataset)
	}

	for _, dataset := range datasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			continue
		}
		if dataset == rootDataset {
			add(dataset)
			continue
		}
		if !strings.HasPrefix(dataset, parent+"/") {
			continue
		}
		if datasetDepth(dataset) != datasetDepth(rootDataset) {
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
		return []string{rootDataset}, nil
	}

	return results, nil
}

func staleReplicationLineageDatasets(rootDataset string, lineageDatasets []string, keepOutOfBand int) []string {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" || len(lineageDatasets) == 0 {
		return nil
	}

	if keepOutOfBand < 0 {
		keepOutOfBand = 0
	}

	rootLeaf := rootDataset
	if idx := strings.LastIndex(rootDataset, "/"); idx >= 0 && idx+1 < len(rootDataset) {
		rootLeaf = rootDataset[idx+1:]
	}
	baseLeaf := replicationLineageBaseLeaf(rootLeaf)

	outOfBand := make([]string, 0)
	for _, dataset := range lineageDatasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" || dataset == rootDataset {
			continue
		}

		leaf := dataset
		if idx := strings.LastIndex(dataset, "/"); idx >= 0 && idx+1 < len(dataset) {
			leaf = dataset[idx+1:]
		}

		if strings.HasPrefix(leaf, baseLeaf+"_zelta_") || strings.HasPrefix(leaf, baseLeaf+".pre_sylve_") {
			outOfBand = append(outOfBand, dataset)
		}
	}

	if len(outOfBand) <= keepOutOfBand {
		return nil
	}

	sort.SliceStable(outOfBand, func(i, j int) bool {
		return outOfBand[i] > outOfBand[j]
	})

	return outOfBand[keepOutOfBand:]
}

func replicationLineageBaseLeaf(leaf string) string {
	leaf = strings.TrimSpace(leaf)
	if idx := strings.Index(leaf, "_zelta_"); idx > 0 {
		return leaf[:idx]
	}
	if idx := strings.Index(leaf, ".pre_sylve_"); idx > 0 {
		return leaf[:idx]
	}
	return leaf
}

func isReplicationTargetModifiedError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "destination") &&
		strings.Contains(lower, "has been modified")
}

func (s *Service) replicateWithEventProgress(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	eventID uint,
) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")

	return runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		func(line string) {
			if err := s.AppendReplicationEventOutput(eventID, line); err != nil {
				logger.L.Warn().Uint("event_id", eventID).Err(err).Msg("append_replication_event_output_failed")
			}
		},
		"backup",
		"--json",
		"--snap-name",
		zeltaSnapshotName("ha"),
		sourceDataset,
		zeltaEndpoint,
	)
}

func (s *Service) replicationSourceDatasets(ctx context.Context, policy *clusterModels.ReplicationPolicy) ([]string, error) {
	if policy == nil {
		return nil, fmt.Errorf("policy_required")
	}

	switch strings.TrimSpace(policy.GuestType) {
	case clusterModels.ReplicationGuestTypeJail:
		dataset, err := s.resolveJailReplicationSourceDataset(policy.GuestID)
		if err != nil {
			return nil, err
		}
		return []string{dataset}, nil
	case clusterModels.ReplicationGuestTypeVM:
		return s.resolveVMBackupSourceDatasets(ctx, policy.GuestID, "")
	default:
		return nil, fmt.Errorf("invalid_guest_type")
	}
}

func (s *Service) resolveJailReplicationSourceDataset(ctID uint) (string, error) {
	if ctID == 0 {
		return "", fmt.Errorf("invalid_jail_ctid")
	}

	var jail jailModels.Jail
	if err := s.DB.Preload("Storages").Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		return "", err
	}

	pool := ""
	for _, storage := range jail.Storages {
		if storage.IsBase {
			pool = strings.TrimSpace(storage.Pool)
			break
		}
	}
	if pool == "" && len(jail.Storages) > 0 {
		pool = strings.TrimSpace(jail.Storages[0].Pool)
	}
	if pool == "" {
		return "", fmt.Errorf("jail_pool_not_found")
	}

	return fmt.Sprintf("%s/sylve/jails/%d", pool, ctID), nil
}

func (s *Service) updateReplicationPolicyResult(policy *clusterModels.ReplicationPolicy, runErr error) {
	if policy == nil || policy.ID == 0 {
		return
	}

	now := time.Now().UTC()
	next := (*time.Time)(nil)
	if policy.Enabled {
		if n, err := nextRunTime(policy.CronExpr, now); err == nil {
			next = &n
		}
	}

	updates := map[string]any{
		"last_run_at": now,
		"last_status": "success",
		"last_error":  "",
		"next_run_at": next,
	}
	if runErr != nil {
		updates["last_status"] = "failed"
		updates["last_error"] = runErr.Error()
	}

	_ = s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(updates).Error
}

func (s *Service) finalizeReplicationEvent(event *clusterModels.ReplicationEvent, runErr error) {
	if event == nil || event.ID == 0 {
		return
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	if runErr != nil {
		event.Status = "failed"
		event.Error = runErr.Error()
		event.Message = "replication_run_failed"
	} else {
		event.Status = "success"
		event.Error = ""
		event.Message = "replication_run_completed"
	}

	_ = s.DB.Model(&clusterModels.ReplicationEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
		"status":       event.Status,
		"error":        event.Error,
		"message":      event.Message,
		"completed_at": event.CompletedAt,
	}).Error
}

func (s *Service) AppendReplicationEventOutput(eventID uint, chunk string) error {
	chunk = strings.TrimSpace(chunk)
	if eventID == 0 || chunk == "" {
		return nil
	}
	return s.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("id = ?", eventID).
		Update("output", gorm.Expr("COALESCE(output, '') || ?", chunk+"\n")).Error
}

func (s *Service) GetReplicationEventProgress(_ context.Context, id uint) (*ReplicationEventProgress, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_event_id")
	}

	var event clusterModels.ReplicationEvent
	if err := s.DB.First(&event, id).Error; err != nil {
		return nil, err
	}

	out := &ReplicationEventProgress{
		Event:      &event,
		TotalBytes: parseTotalBytesFromOutput(event.Output),
		MovedBytes: parseMovedBytesFromOutput(event.Output),
	}

	if out.TotalBytes != nil && out.MovedBytes != nil && *out.TotalBytes > 0 {
		pct := (float64(*out.MovedBytes) / float64(*out.TotalBytes)) * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		out.ProgressPercent = &pct
	}

	return out, nil
}

func (s *Service) acquireReplication(policyID uint) bool {
	s.replicationMu.Lock()
	defer s.replicationMu.Unlock()
	if _, exists := s.runningReplication[policyID]; exists {
		return false
	}
	s.runningReplication[policyID] = struct{}{}
	return true
}

func (s *Service) releaseReplication(policyID uint) {
	s.replicationMu.Lock()
	defer s.replicationMu.Unlock()
	delete(s.runningReplication, policyID)
}

func (s *Service) runFailoverControllerTick(ctx context.Context) error {
	if s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return nil
	}

	nodes, err := s.Cluster.Nodes()
	if err != nil {
		return err
	}
	nodeByID := make(map[string]clusterModels.ClusterNode, len(nodes))
	for _, node := range nodes {
		nodeByID[strings.TrimSpace(node.NodeUUID)] = node
	}

	policies, err := s.Cluster.ListReplicationPolicies()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for i := range policies {
		policy := policies[i]
		if !policy.Enabled {
			continue
		}

		owner := strings.TrimSpace(policy.ActiveNodeID)
		if owner == "" {
			owner = strings.TrimSpace(policy.SourceNodeID)
		}
		if owner == "" {
			resolvedOwner, resolveErr := s.Cluster.ResolveReplicationGuestOwnerNode(policy.GuestType, policy.GuestID)
			if resolveErr != nil {
				logger.L.Warn().Err(resolveErr).Uint("policy_id", policy.ID).Msg("resolve_replication_owner_failed")
				continue
			}
			resolvedOwner = strings.TrimSpace(resolvedOwner)
			if resolvedOwner == "" {
				continue
			}

			req := s.replicationPolicyToReq(&policy)
			if strings.TrimSpace(req.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
				req.SourceNodeID = resolvedOwner
			}
			req.ActiveNodeID = resolvedOwner
			if err := s.Cluster.ProposeReplicationPolicyUpdate(policy.ID, req, false); err != nil {
				logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("set_initial_replication_owner_failed")
				continue
			}

			owner = resolvedOwner
			policy.SourceNodeID = req.SourceNodeID
			policy.ActiveNodeID = resolvedOwner
		}

		node, ok := nodeByID[owner]
		status := "offline"
		if ok {
			status = strings.ToLower(strings.TrimSpace(node.Status))
		}

		if status == "online" {
			s.downMisses[policy.ID] = 0
			lease := clusterModels.ReplicationLease{
				PolicyID:    policy.ID,
				GuestType:   policy.GuestType,
				GuestID:     policy.GuestID,
				OwnerNodeID: owner,
				ExpiresAt:   now.Add(10 * time.Second),
				Version:     uint64(now.UnixNano()),
				LastReason:  "leader_renew",
				LastActor:   s.Cluster.LocalNodeID(),
			}
			if err := s.Cluster.UpsertReplicationLease(lease, false); err != nil {
				logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("replication_lease_renew_failed")
			}

			if policy.FailbackMode == clusterModels.ReplicationFailbackAuto &&
				strings.TrimSpace(policy.SourceNodeID) != "" &&
				strings.TrimSpace(policy.SourceNodeID) != owner {
				sourceNode, ok := nodeByID[strings.TrimSpace(policy.SourceNodeID)]
				if ok && strings.ToLower(strings.TrimSpace(sourceNode.Status)) == "online" {
					if err := s.failoverPolicyToNode(ctx, &policy, strings.TrimSpace(policy.SourceNodeID), "auto_failback"); err != nil {
						logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("auto_failback_failed")
					}
				}
			}
			continue
		}

		s.downMisses[policy.ID]++
		if s.downMisses[policy.ID] < 3 {
			continue
		}

		targetNodeID, selectErr := s.selectFailoverTarget(&policy, owner, nodeByID)
		if selectErr != nil {
			_, _ = s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
				PolicyID:     &policy.ID,
				EventType:    "failover",
				Status:       "failed",
				Message:      "no_healthy_failover_target",
				Error:        selectErr.Error(),
				SourceNodeID: owner,
				GuestType:    policy.GuestType,
				GuestID:      policy.GuestID,
				StartedAt:    now,
				CompletedAt:  &now,
			}, false)
			continue
		}

		if err := s.failoverPolicyToNode(ctx, &policy, targetNodeID, "node_down_failover"); err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Str("target", targetNodeID).Msg("policy_failover_failed")
			continue
		}

		s.downMisses[policy.ID] = 0
	}

	return nil
}

func (s *Service) selectFailoverTarget(policy *clusterModels.ReplicationPolicy, currentOwner string, nodes map[string]clusterModels.ClusterNode) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("policy_required")
	}

	targets := append([]clusterModels.ReplicationPolicyTarget{}, policy.Targets...)
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Weight == targets[j].Weight {
			ni := nodes[strings.TrimSpace(targets[i].NodeID)]
			nj := nodes[strings.TrimSpace(targets[j].NodeID)]
			li := ni.CPUUsage + ni.MemoryUsage + ni.DiskUsage
			lj := nj.CPUUsage + nj.MemoryUsage + nj.DiskUsage
			if li == lj {
				return targets[i].NodeID < targets[j].NodeID
			}
			return li < lj
		}
		return targets[i].Weight > targets[j].Weight
	})

	for _, target := range targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" || nodeID == currentOwner {
			continue
		}
		node, ok := nodes[nodeID]
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(node.Status)) != "online" {
			continue
		}
		return nodeID, nil
	}

	return "", fmt.Errorf("no_healthy_target_nodes")
}

func (s *Service) failoverPolicyToNode(ctx context.Context, policy *clusterModels.ReplicationPolicy, targetNodeID string, reason string) error {
	if policy == nil || targetNodeID == "" {
		return fmt.Errorf("invalid_failover_input")
	}

	previousOwner := strings.TrimSpace(policy.ActiveNodeID)
	if previousOwner == "" {
		previousOwner = strings.TrimSpace(policy.SourceNodeID)
	}

	req := s.replicationPolicyToReq(policy)

	if strings.TrimSpace(policy.SourceMode) == clusterModels.ReplicationSourceModeFollowActive {
		req.SourceNodeID = targetNodeID
	}
	req.ActiveNodeID = targetNodeID

	policy.ActiveNodeID = targetNodeID
	if err := s.Cluster.ProposeReplicationPolicyUpdate(policy.ID, req, false); err != nil {
		return err
	}

	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: targetNodeID,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Second),
		Version:     uint64(time.Now().UTC().UnixNano()),
		LastReason:  reason,
		LastActor:   s.Cluster.LocalNodeID(),
	}
	if err := s.Cluster.UpsertReplicationLease(lease, false); err != nil {
		return err
	}

	_, _ = s.Cluster.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
		PolicyID:     &policy.ID,
		EventType:    "failover",
		Status:       "running",
		Message:      reason,
		SourceNodeID: previousOwner,
		TargetNodeID: targetNodeID,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		StartedAt:    time.Now().UTC(),
	}, false)

	if strings.TrimSpace(targetNodeID) == strings.TrimSpace(s.Cluster.LocalNodeID()) {
		return s.ActivateReplicationPolicy(ctx, policy.ID)
	}
	return s.forwardActivateReplicationPolicy(targetNodeID, policy.ID)
}

func (s *Service) forwardActivateReplicationPolicy(nodeID string, policyID uint) error {
	targetAPI, err := s.resolveReplicationNodeAPI(nodeID)
	if err != nil {
		return err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.Cluster.AuthService.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/cluster/replication/internal/activate", targetAPI)
	return utils.HTTPPostJSON(url, map[string]any{
		"policyId": policyID,
	}, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
}

func (s *Service) resolveReplicationNodeAPI(nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("replication_target_node_required")
	}

	nodes, err := s.Cluster.Nodes()
	if err == nil {
		for _, node := range nodes {
			if strings.TrimSpace(node.NodeUUID) == nodeID && strings.TrimSpace(node.API) != "" {
				return strings.TrimSpace(node.API), nil
			}
		}
	}

	if s.Cluster.Raft != nil {
		fut := s.Cluster.Raft.GetConfiguration()
		if fut.Error() == nil {
			for _, server := range fut.Configuration().Servers {
				if string(server.ID) != nodeID {
					continue
				}
				host, _, splitErr := net.SplitHostPort(string(server.Address))
				if splitErr != nil {
					host = string(server.Address)
				}
				host = strings.TrimSpace(host)
				if host == "" {
					continue
				}
				return net.JoinHostPort(host, strconv.Itoa(config.ParsedConfig.Port)), nil
			}
		}
	}

	return "", fmt.Errorf("replication_target_node_not_found")
}

func (s *Service) replicationPolicyToReq(policy *clusterModels.ReplicationPolicy) clusterServiceInterfaces.ReplicationPolicyReq {
	req := clusterServiceInterfaces.ReplicationPolicyReq{
		Name:         policy.Name,
		GuestType:    policy.GuestType,
		GuestID:      policy.GuestID,
		SourceNodeID: policy.SourceNodeID,
		SourceMode:   policy.SourceMode,
		FailbackMode: policy.FailbackMode,
		CronExpr:     policy.CronExpr,
		Enabled:      &policy.Enabled,
		Targets:      make([]clusterServiceInterfaces.ReplicationPolicyTargetReq, 0, len(policy.Targets)),
	}

	for _, target := range policy.Targets {
		req.Targets = append(req.Targets, clusterServiceInterfaces.ReplicationPolicyTargetReq{
			NodeID: target.NodeID,
			Weight: target.Weight,
		})
	}

	return req
}

func (s *Service) ActivateReplicationPolicy(ctx context.Context, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return err
	}

	switch strings.TrimSpace(policy.GuestType) {
	case clusterModels.ReplicationGuestTypeJail:
		return s.activateReplicationJail(ctx, policy.GuestID)
	case clusterModels.ReplicationGuestTypeVM:
		return s.activateReplicationVM(ctx, policy.GuestID)
	default:
		return fmt.Errorf("invalid_guest_type")
	}
}

func (s *Service) activateReplicationJail(ctx context.Context, ctID uint) error {
	dataset, err := s.findLocalGuestDataset(ctx, clusterModels.ReplicationGuestTypeJail, ctID)
	if err != nil {
		return err
	}
	if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
		return err
	}

	var jailCount int64
	if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", ctID).Count(&jailCount).Error; err != nil {
		return err
	}

	if jailCount == 0 {
		if dataset == "" {
			return fmt.Errorf("jail_dataset_not_found")
		}

		if err := s.reconcileRestoredJailFromDatasetWithOptions(ctx, dataset, true); err != nil {
			return err
		}
	}

	return s.Jail.JailAction(int(ctID), "start")
}

func (s *Service) activateReplicationVM(ctx context.Context, rid uint) error {
	dataset, err := s.findLocalGuestDataset(ctx, clusterModels.ReplicationGuestTypeVM, rid)
	if err != nil {
		return err
	}
	if err := s.prepareReplicatedDatasetForActivation(ctx, dataset); err != nil {
		return err
	}

	vm, err := s.findVMByRID(rid)
	if err != nil {
		return err
	}

	if vm == nil {
		if dataset == "" {
			return fmt.Errorf("vm_dataset_not_found")
		}
		if err := s.reconcileRestoredVMFromDatasetWithOptions(ctx, dataset, true); err != nil {
			return err
		}
		vm, err = s.findVMByRID(rid)
		if err != nil {
			return err
		}
		if vm == nil {
			return fmt.Errorf("vm_definition_not_found_after_reconcile")
		}
	}

	return s.VM.LvVMAction(*vm, "start")
}

func (s *Service) prepareReplicatedDatasetForActivation(ctx context.Context, rootDataset string) error {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil
	}

	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return err
	}

	seen := map[string]struct{}{
		rootDataset: {},
	}
	subtree := []string{rootDataset}
	prefix := rootDataset + "/"

	for _, candidate := range datasets {
		ds := normalizeDatasetPath(candidate)
		if ds == "" || ds == rootDataset {
			continue
		}
		if !strings.HasPrefix(ds, prefix) {
			continue
		}
		if _, ok := seen[ds]; ok {
			continue
		}
		seen[ds] = struct{}{}
		subtree = append(subtree, ds)
	}

	sort.SliceStable(subtree, func(i, j int) bool {
		di := strings.Count(subtree[i], "/")
		dj := strings.Count(subtree[j], "/")
		if di == dj {
			return subtree[i] < subtree[j]
		}
		return di < dj
	})

	for idx, dataset := range subtree {
		ds, err := s.getLocalDataset(ctx, dataset)
		if err != nil {
			return fmt.Errorf("failed_to_open_replication_dataset_%s: %w", dataset, err)
		}
		if ds == nil {
			continue
		}

		if err := ds.SetProperties(ctx, "readonly", "off", "canmount", "on"); err != nil {
			return fmt.Errorf("failed_to_set_replication_dataset_properties_%s: %w", dataset, err)
		}

		if idx == 0 {
			if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", "mountpoint", dataset); err != nil {
				return fmt.Errorf("failed_to_inherit_replication_dataset_mountpoint_%s: %w", dataset, err)
			}
		}

		if err := ds.Mount(ctx, false); err != nil {
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, "already mounted") {
				return fmt.Errorf("failed_to_mount_replication_dataset_%s: %w", dataset, err)
			}
		}
	}

	return nil
}

func (s *Service) findLocalGuestDataset(ctx context.Context, guestType string, guestID uint) (string, error) {
	datasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		return "", err
	}

	seen := map[string]struct{}{}
	candidates := make([]string, 0)
	for _, dataset := range datasets {
		kind, id := inferRestoreDatasetKind(dataset)
		if kind != guestType || id != guestID {
			continue
		}

		normalized := normalizeDatasetPath(dataset)
		if kind == clusterModels.ReplicationGuestTypeVM {
			root := vmDatasetRoot(normalized)
			if root != "" {
				normalized = root
			}
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}

	sort.Strings(candidates)
	if len(candidates) == 0 {
		return "", nil
	}
	return candidates[0], nil
}

func (s *Service) selfFenceExpiredLeases(_ context.Context) error {
	if s.Cluster == nil {
		return nil
	}

	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return nil
	}

	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, policy := range policies {
		running, err := s.isLocalProtectedGuestRunning(strings.TrimSpace(policy.GuestType), policy.GuestID)
		if err != nil || !running {
			continue
		}

		var lease clusterModels.ReplicationLease
		leaseErr := s.DB.Where("policy_id = ?", policy.ID).First(&lease).Error

		fenceReason := ""
		if leaseErr == gorm.ErrRecordNotFound {
			fenceReason = "lease_missing"
		} else if leaseErr != nil {
			continue
		} else if strings.TrimSpace(lease.OwnerNodeID) != localNodeID {
			fenceReason = "lease_not_owned"
		} else if now.After(lease.ExpiresAt) {
			fenceReason = "lease_expired"
		}

		if fenceReason == "" {
			continue
		}

		switch strings.TrimSpace(policy.GuestType) {
		case clusterModels.ReplicationGuestTypeJail:
			_ = s.Jail.JailAction(int(policy.GuestID), "stop")
		case clusterModels.ReplicationGuestTypeVM:
			_ = s.stopVMIfPresent(policy.GuestID)
		}
	}

	return nil
}

func (s *Service) isLocalProtectedGuestRunning(guestType string, guestID uint) (bool, error) {
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		vm, err := s.findVMByRID(guestID)
		if err != nil || vm == nil {
			return false, err
		}
		inactive, err := s.VM.IsDomainInactive(guestID)
		if err != nil {
			return false, err
		}
		return !inactive, nil
	case clusterModels.ReplicationGuestTypeJail:
		out, err := utils.RunCommand("jls", "-j", fmt.Sprintf("%d", guestID), "jid")
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(out) != "", nil
	default:
		return false, nil
	}
}
