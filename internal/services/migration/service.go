// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

const (
	migrationSnapPrefix = "sylve-migrate"

	PhasePreflight         = "preflight"
	PhaseInitialReplicaton = "initial_replication"
	PhaseStopSource        = "stop_source"
	PhaseFinalSync         = "final_sync"
	PhaseStartTarget       = "start_target"
	PhasePolicyAdjustment  = "policy_adjustment"
	PhaseCleanupSource     = "cleanup_source"
	PhaseFinalize          = "finalize"
)

var (
	ErrMigrationInProgress   = fmt.Errorf("migration_in_progress")
	ErrGuestActiveTransition = fmt.Errorf("guest_has_active_transition")
	ErrTargetNodeOffline     = fmt.Errorf("target_node_offline")
	ErrTargetNodeSame        = fmt.Errorf("target_node_is_source")
	ErrTargetAlreadyHasGuest = fmt.Errorf("target_already_has_guest")
	ErrTargetPoolMissing     = fmt.Errorf("target_missing_pool")
	ErrSSHUnreachable        = fmt.Errorf("ssh_unreachable")
	ErrCancelNotAllowed      = fmt.Errorf("cancel_not_allowed_in_current_phase")
	ErrMigrationFailed       = fmt.Errorf("migration_failed")
)

type migrationPayload struct {
	TargetNodeUUID     string   `json:"targetNodeUuid"`
	TargetNodeHostname string   `json:"targetNodeHostname"`
	Phase              string   `json:"phase"`
	PhaseMessage       string   `json:"phaseMessage"`
	Warnings           []string `json:"warnings,omitempty"`
}

type guestWorkloadGurad interface {
	AcquireGuestLock(guestType string, guestID uint, operation string) (bool, string)
	ReleaseGuestLock(guestType string, guestID uint)
}

type migrationTransferProgress struct {
	Dataset    string
	TotalBytes uint64
	SentBytes  uint64
	Phase      string
}

type Service struct {
	DB            *gorm.DB
	TelemetryDB   *gorm.DB
	Cluster       *cluster.Service
	Libvirt       *libvirt.Service
	Jail          *jail.Service
	GZFS          *gzfs.Client
	WorkloadGuard guestWorkloadGurad

	activeMu    sync.Mutex
	active      map[uint]struct{}
	progressMu  sync.RWMutex
	progressMap map[uint]*migrationTransferProgress
}

func NewService(
	db *gorm.DB,
	telemetryDB *gorm.DB,
	clusterSvc *cluster.Service,
	libvirtSvc *libvirt.Service,
	jailSvc *jail.Service,
	gzfsClient *gzfs.Client,
	workloadGuard guestWorkloadGurad,
) *Service {
	return &Service{
		DB:            db,
		TelemetryDB:   telemetryDB,
		Cluster:       clusterSvc,
		Libvirt:       libvirtSvc,
		Jail:          jailSvc,
		GZFS:          gzfsClient,
		WorkloadGuard: workloadGuard,
		active:        make(map[uint]struct{}),
		progressMap:   make(map[uint]*migrationTransferProgress),
	}
}

func (s *Service) writeTransferProgress(taskID uint, dataset string, phase string, totalBytes, sentBytes uint64) {
	if taskID == 0 || dataset == "" {
		return
	}

	s.progressMu.Lock()
	s.progressMap[taskID] = &migrationTransferProgress{
		Dataset:    dataset,
		TotalBytes: totalBytes,
		SentBytes:  sentBytes,
		Phase:      phase,
	}
	s.progressMu.Unlock()

	pct := uint64(0)
	if totalBytes > 0 {
		pct = sentBytes * 100 / totalBytes
		if pct > 100 {
			pct = 100
		}
	}

	msg := fmt.Sprintf("%s: %s (%d%%)", phase, dataset, pct)
	s.updateTaskDB(taskID, map[string]any{"message": msg})
}

func (s *Service) getActiveTaskForGuest(guestType string, guestID uint) (*taskModels.GuestLifecycleTask, error) {
	var task taskModels.GuestLifecycleTask
	tx := s.DB.
		Where("guest_type = ? AND guest_id = ? AND status IN ?", guestType, guestID, []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		}).
		Order("created_at DESC").
		Order("id DESC").
		Limit(1).
		Find(&task)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, nil
	}
	return &task, nil
}

func (s *Service) ValidateMigration(ctx context.Context, req migrationIface.MigrateRequest) (*migrationIface.ValidateResult, error) {
	result := &migrationIface.ValidateResult{Allowed: true}

	detail := s.Cluster.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "local_node_id_unavailable")
		return result, nil
	}
	localNodeID := strings.TrimSpace(detail.NodeID)

	req.TargetNodeUUID = strings.TrimSpace(req.TargetNodeUUID)
	if req.TargetNodeUUID == localNodeID {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "target_is_source_node")
		return result, nil
	}

	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", req.TargetNodeUUID).First(&targetNode).Error; err != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "target_node_not_found")
		return result, nil
	}

	if strings.ToLower(targetNode.Status) != "online" {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "target_node_offline")
		return result, nil
	}

	var policy clusterModels.ReplicationPolicy
	res := s.DB.
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", req.GuestType, req.GuestID, true).
		Limit(1).
		Find(&policy)
	if res.Error != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, fmt.Sprintf("replication_policy_lookup_failed: %v", res.Error))
		return result, nil
	}
	if res.RowsAffected > 0 {
		switch policy.TransitionState {
		case clusterModels.ReplicationTransitionStateDemoting,
			clusterModels.ReplicationTransitionStateCatchup,
			clusterModels.ReplicationTransitionStatePromoting:
			result.Allowed = false
			result.Reasons = append(result.Reasons, fmt.Sprintf("guest_has_active_transition: %s", policy.TransitionState))
			return result, nil
		}
	}

	active, err := s.getActiveTaskForGuest(req.GuestType, req.GuestID)
	if err != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, fmt.Sprintf("active_task_lookup_failed: %v", err))
		return result, nil
	}
	if active != nil && active.Action == "migrate" {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "migration_already_in_progress")
		return result, nil
	}
	if active != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, fmt.Sprintf("guest_has_active_lifecycle_task: %s", active.Action))
		return result, nil
	}

	var runningReplicationCount int64
	if err := s.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("guest_type = ? AND guest_id = ? AND status = ?", req.GuestType, req.GuestID, "running").
		Count(&runningReplicationCount).Error; err != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, fmt.Sprintf("replication_event_lookup_failed: %v", err))
		return result, nil
	}
	if runningReplicationCount > 0 {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "guest_has_running_replication_event")
		return result, nil
	}

	if s.hasRunningBackupEventForGuest(req.GuestType, req.GuestID) {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "guest_has_running_backup_event")
		return result, nil
	}

	if req.GuestType == taskModels.GuestTypeVM {
		reasons := s.validateVMPreflight(ctx, req.GuestID, targetNode)
		for _, reason := range reasons {
			if strings.HasPrefix(strings.ToLower(reason), "warning_") {
				result.Warnings = append(result.Warnings, reason)
			} else {
				result.Allowed = false
				result.Reasons = append(result.Reasons, reason)
			}
		}
	} else if req.GuestType == taskModels.GuestTypeJail {
		reasons := s.validateJailPreflight(ctx, req.GuestID, targetNode)
		for _, reason := range reasons {
			if strings.HasPrefix(strings.ToLower(reason), "warning_") {
				result.Warnings = append(result.Warnings, reason)
			} else {
				result.Allowed = false
				result.Reasons = append(result.Reasons, reason)
			}
		}
	}

	return result, nil
}

func (s *Service) validateVMPreflight(ctx context.Context, rid uint, targetNode clusterModels.ClusterNode) []string {
	var reasons []string

	var vm vmModels.VM
	preloads := []string{"Storages", "Storages.Dataset", "Networks"}
	if err := s.DB.Preload(preloads[0]).Preload(preloads[1]).Preload(preloads[2]).Where("rid = ?", rid).First(&vm).Error; err != nil {
		return []string{fmt.Sprintf("vm_not_found: %v", err)}
	}

	pools := make(map[string]bool)
	for _, storage := range vm.Storages {
		pool := strings.TrimSpace(storage.Pool)
		if pool != "" {
			pools[pool] = true
		}
	}

	identity, sshErr := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if sshErr != nil {
		reasons = append(reasons, fmt.Sprintf("target_ssh_identity_unavailable: %v", sshErr))
		return reasons
	}

	privateKeyPath, keyErr := s.Cluster.ClusterSSHPrivateKeyPath()
	if keyErr != nil {
		reasons = append(reasons, fmt.Sprintf("cluster_ssh_key_unavailable: %v", keyErr))
		return reasons
	}

	for pool := range pools {
		exists, err := s.remotePoolExists(ctx, identity, pool)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("target_pool_check_failed_%s: %v", pool, err))
			continue
		}
		if !exists {
			reasons = append(reasons, fmt.Sprintf("target_missing_pool: %s", pool))
			continue
		}

		guestDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, rid)
		datasetExists, dsErr := s.remoteDatasetExists(ctx, identity, privateKeyPath, guestDataset)
		if dsErr != nil {
			reasons = append(reasons, fmt.Sprintf("target_guest_check_failed_%s: %v", pool, dsErr))
		} else if datasetExists {
			reasons = append(reasons, fmt.Sprintf("warning_stale_dataset_on_target: %s", guestDataset))
		}
	}

	for i, net := range vm.Networks {
		bridge, err := s.resolveNetworkBridgeName(strings.TrimSpace(net.SwitchType), net.SwitchID)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("network_%d_switch_lookup_failed: %v", i+1, err))
			continue
		}
		if bridge == "" {
			continue
		}

		bridgeExists, bridgeErr := s.remoteBridgeExists(ctx, identity, privateKeyPath, bridge)
		if bridgeErr != nil {
			reasons = append(reasons, fmt.Sprintf("network_%d_bridge_check_failed_%s: %v", i+1, bridge, bridgeErr))
			continue
		}
		if !bridgeExists {
			reasons = append(reasons, fmt.Sprintf("warning_target_missing_bridge: %s", bridge))
		}
	}

	return reasons
}

func (s *Service) validateJailPreflight(ctx context.Context, ctID uint, targetNode clusterModels.ClusterNode) []string {
	var reasons []string

	identity, sshErr := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if sshErr != nil {
		reasons = append(reasons, fmt.Sprintf("target_ssh_identity_unavailable: %v", sshErr))
		return reasons
	}

	privateKeyPath, keyErr := s.Cluster.ClusterSSHPrivateKeyPath()
	if keyErr != nil {
		reasons = append(reasons, fmt.Sprintf("cluster_ssh_key_unavailable: %v", keyErr))
		return reasons
	}

	var jailStorages []string
	if err := s.DB.Raw("SELECT DISTINCT pool FROM jail_storages WHERE jid = (SELECT id FROM jails WHERE ct_id = ?)", ctID).Scan(&jailStorages).Error; err != nil {
		return []string{fmt.Sprintf("jail_not_found: %v", err)}
	}

	type jailNetworkRow struct {
		SwitchID   uint
		SwitchType string
	}
	var jailNetworks []jailNetworkRow
	if err := s.DB.Raw(`
		SELECT jn.switch_id, jn.switch_type
		FROM jail_networks jn
		JOIN jails j ON jn.jail_id = j.id
		WHERE j.ct_id = ?
	`, ctID).Scan(&jailNetworks).Error; err != nil {
		logger.L.Debug().Err(err).Uint("ct_id", ctID).Msg("failed_to_query_jail_networks_during_validation")
	}

	for _, pool := range jailStorages {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}
		exists, err := s.remotePoolExists(ctx, identity, pool)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("target_pool_check_failed_%s: %v", pool, err))
			continue
		}
		if !exists {
			reasons = append(reasons, fmt.Sprintf("target_missing_pool: %s", pool))
			continue
		}

		guestDataset := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
		datasetExists, dsErr := s.remoteDatasetExists(ctx, identity, privateKeyPath, guestDataset)
		if dsErr != nil {
			reasons = append(reasons, fmt.Sprintf("target_guest_check_failed_%s: %v", pool, dsErr))
		} else if datasetExists {
			reasons = append(reasons, fmt.Sprintf("warning_stale_dataset_on_target: %s", guestDataset))
		}
	}

	for i, net := range jailNetworks {
		bridge, err := s.resolveNetworkBridgeName(strings.TrimSpace(net.SwitchType), net.SwitchID)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("network_%d_switch_lookup_failed: %v", i+1, err))
			continue
		}
		if bridge == "" {
			continue
		}

		bridgeExists, bridgeErr := s.remoteBridgeExists(ctx, identity, privateKeyPath, bridge)
		if bridgeErr != nil {
			reasons = append(reasons, fmt.Sprintf("network_%d_bridge_check_failed_%s: %v", i+1, bridge, bridgeErr))
			continue
		}
		if !bridgeExists {
			reasons = append(reasons, fmt.Sprintf("warning_target_missing_bridge: %s", bridge))
		}
	}

	return reasons
}

func (s *Service) ExecuteMigration(ctx context.Context, taskID uint) error {
	task := taskModels.GuestLifecycleTask{}
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.Status == taskModels.LifecycleTaskStatusSuccess || task.Status == taskModels.LifecycleTaskStatusFailed {
		return nil
	}

	var mp migrationPayload
	if strings.TrimSpace(task.Payload) != "" {
		if err := json.Unmarshal([]byte(task.Payload), &mp); err != nil {
			s.updateTaskFailed(taskID, fmt.Sprintf("invalid_payload: %v", err))
			return err
		}
	}

	now := time.Now().UTC()
	s.updateTaskDB(taskID, map[string]any{
		"status":     taskModels.LifecycleTaskStatusRunning,
		"started_at": now,
		"message":    "migration_starting",
	})

	mp.Phase = PhasePreflight
	mp.PhaseMessage = "validating_migration_prerequisites"
	s.updateTaskPhase(taskID, mp)

	if err := s.checkCancelled(taskID); err != nil {
		return s.handleCancel(taskID, mp)
	}

	if err := s.phasePreflight(ctx, &mp, task); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	if s.WorkloadGuard != nil {
		if acquired, existing := s.WorkloadGuard.AcquireGuestLock(task.GuestType, task.GuestID, fmt.Sprintf("migration:%d", taskID)); !acquired {
			reason := fmt.Sprintf("guest_locked_by: %s", existing)
			s.updateTaskFailed(taskID, reason)
			return fmt.Errorf("%s", reason)
		}
		defer s.WorkloadGuard.ReleaseGuestLock(task.GuestType, task.GuestID)
	}

	if err := s.checkCancelled(taskID); err != nil {
		return s.handleCancel(taskID, mp)
	}

	mp.Phase = PhaseInitialReplicaton
	mp.PhaseMessage = "replicating_datasets_to_target"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseInitialReplication(ctx, &mp, task); err != nil {
		if strings.Contains(err.Error(), "cancelled") {
			return s.handleCancel(taskID, mp)
		}
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	if err := s.checkCancelled(taskID); err != nil {
		return s.handleCancel(taskID, mp)
	}

	mp.Phase = PhaseStopSource
	mp.PhaseMessage = "stopping_guest_on_source"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseStopSource(ctx, &mp, task); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	mp.Phase = PhaseFinalSync
	mp.PhaseMessage = "performing_final_incremental_sync"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseFinalSync(ctx, &mp, task); err != nil {
		if strings.Contains(err.Error(), "cancelled") {
			return s.handleCancel(taskID, mp)
		}
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	mp.Phase = PhaseStartTarget
	mp.PhaseMessage = "starting_guest_on_target"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseStartTarget(ctx, &mp, task); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	mp.Phase = PhasePolicyAdjustment
	mp.PhaseMessage = "adjusting_cluster_policies"
	s.updateTaskPhase(taskID, mp)

	if err := s.phasePolicyAdjustment(ctx, &mp, task); err != nil {
		logger.L.Warn().Err(err).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_policy_adjustment_failed")
	}

	mp.Phase = PhaseCleanupSource
	mp.PhaseMessage = "cleaning_up_source_guest"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseCleanupSource(ctx, &mp, task); err != nil {
		logger.L.Warn().Err(err).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_source_cleanup_failed")
	}

	mp.Phase = PhaseFinalize
	mp.PhaseMessage = "finalizing_migration"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseFinalize(ctx, &mp, task); err != nil {
		logger.L.Warn().Err(err).Str("guest_type", task.GuestType).Uint("guest_id", task.GuestID).Msg("migration_finalize_failed")
	}

	finishedAt := time.Now().UTC()
	s.updateTaskDB(taskID, map[string]any{
		"status":      taskModels.LifecycleTaskStatusSuccess,
		"finished_at": finishedAt,
		"message":     "migration_completed",
	})

	return nil
}

func (s *Service) CancelMigration(ctx context.Context, taskID uint) error {
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.Action != "migrate" {
		return fmt.Errorf("not_a_migration_task")
	}

	var mp migrationPayload
	if strings.TrimSpace(task.Payload) != "" {
		if err := json.Unmarshal([]byte(task.Payload), &mp); err != nil {
			return err
		}
	}

	switch mp.Phase {
	case PhasePreflight, PhaseInitialReplicaton, PhaseStopSource:
		now := time.Now().UTC()
		return s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(map[string]any{
			"override_requested": true,
			"updated_at":         now,
			"message":            "cancellation_requested",
		}).Error
	default:
		return ErrCancelNotAllowed
	}
}

func (s *Service) checkCancelled(taskID uint) error {
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}
	if task.OverrideRequested {
		return fmt.Errorf("migration_cancelled")
	}
	return nil
}

func (s *Service) handleCancel(taskID uint, mp migrationPayload) error {
	mp.PhaseMessage = "migration_cancelled"
	s.updateTaskPhase(taskID, mp)

	finishedAt := time.Now().UTC()
	return s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(map[string]any{
		"status":      taskModels.LifecycleTaskStatusFailed,
		"finished_at": finishedAt,
		"message":     "migration_cancelled",
		"error":       "cancelled_by_user",
	}).Error
}

func (s *Service) updateTaskPhase(taskID uint, mp migrationPayload) {
	b, err := json.Marshal(mp)
	if err != nil {
		return
	}
	s.updateTaskDB(taskID, map[string]any{
		"message": mp.PhaseMessage,
		"payload": string(b),
	})
}

func (s *Service) updateTaskFailed(taskID uint, errMsg string) {
	finishedAt := time.Now().UTC()
	s.updateTaskDB(taskID, map[string]any{
		"status":      taskModels.LifecycleTaskStatusFailed,
		"finished_at": finishedAt,
		"message":     "migration_failed",
		"error":       errMsg,
	})
}

func (s *Service) updateTaskDB(taskID uint, updates map[string]any) {
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		logger.L.Warn().Err(err).Uint("task_id", taskID).Msg("migration_task_update_failed")
	}
}

func (s *Service) getNodeSSHIdentity(nodeUUID string) (*clusterModels.ClusterSSHIdentity, error) {
	var identity clusterModels.ClusterSSHIdentity
	if err := s.DB.Where("node_uuid = ?", nodeUUID).First(&identity).Error; err != nil {
		return nil, err
	}
	return &identity, nil
}

func (s *Service) hasRunningBackupEventForGuest(guestType string, guestID uint) bool {
	var runningEvents []clusterModels.BackupEvent
	if err := s.DB.
		Where("status = ?", "running").
		Find(&runningEvents).Error; err != nil {
		return false
	}

	for _, event := range runningEvents {
		if s.backupEventReferencesGuest(event, guestType, guestID) {
			return true
		}
	}

	return false
}

func (s *Service) backupEventReferencesGuest(event clusterModels.BackupEvent, guestType string, guestID uint) bool {
	if guestType == taskModels.GuestTypeVM && event.Mode == clusterModels.BackupJobModeVM {
		vmSuffix := fmt.Sprintf("virtual-machines/%d", guestID)
		if strings.Contains(event.SourceDataset, vmSuffix) {
			return true
		}
	}

	if guestType == taskModels.GuestTypeJail && event.Mode == clusterModels.BackupJobModeJail {
		jailSuffix := fmt.Sprintf("jails/%d", guestID)
		if strings.Contains(event.SourceDataset, jailSuffix) || strings.Contains(event.TargetEndpoint, jailSuffix) {
			return true
		}
	}

	return false
}

func (s *Service) remotePoolExists(ctx context.Context, identity *clusterModels.ClusterSSHIdentity, pool string) (bool, error) {
	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return false, err
	}

	sshArgs := buildClusterSSHArgs(identity, privateKeyPath)
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost), "zpool", "list", "-H", "-o", "name", pool)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		combined := strings.ToLower(strings.TrimSpace(output + " " + err.Error()))
		if strings.Contains(combined, "no such pool") {
			return false, nil
		}
		return false, err
	}

	return strings.TrimSpace(output) == pool, nil
}

func (s *Service) resolveGuestDatasets(ctx context.Context, guestType string, guestID uint) ([]string, error) {
	switch guestType {
	case taskModels.GuestTypeVM:
		return s.resolveVMDatasets(ctx, guestID)
	case taskModels.GuestTypeJail:
		return s.resolveJailDatasets(ctx, guestID)
	default:
		return nil, fmt.Errorf("unsupported_guest_type: %s", guestType)
	}
}

func (s *Service) resolveVMDatasets(ctx context.Context, rid uint) ([]string, error) {
	var vm vmModels.VM
	if err := s.DB.Preload("Storages").Preload("Storages.Dataset").Where("rid = ?", rid).First(&vm).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var datasets []string

	for _, st := range vm.Storages {
		pool := strings.TrimSpace(st.Pool)
		if pool == "" {
			continue
		}
		root := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, rid)
		if seen[root] {
			continue
		}
		seen[root] = true
		datasets = append(datasets, root)
	}

	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return datasets, nil
	}

	list, listErr := s.GZFS.ZFS.List(ctx, true)
	if listErr == nil {
		suffix := fmt.Sprintf("/sylve/virtual-machines/%d", rid)
		for _, ds := range list {
			name := ds.Name
			if strings.Contains(name, suffix) && !seen[name] {
				seen[name] = true
				datasets = append(datasets, name)
			}
		}
	}

	return datasets, nil
}

func (s *Service) resolveJailDatasets(ctx context.Context, ctID uint) ([]string, error) {
	var pools []string
	if err := s.DB.Raw("SELECT DISTINCT pool FROM jail_storages WHERE jid = (SELECT id FROM jails WHERE ct_id = ?)", ctID).Scan(&pools).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var datasets []string

	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}
		root := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
		if seen[root] {
			continue
		}
		seen[root] = true
		datasets = append(datasets, root)
	}

	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return datasets, nil
	}

	list, listErr := s.GZFS.ZFS.List(ctx, true)
	if listErr == nil {
		suffix := fmt.Sprintf("/sylve/jails/%d", ctID)
		for _, ds := range list {
			name := ds.Name
			if strings.Contains(name, suffix) && !seen[name] {
				seen[name] = true
				datasets = append(datasets, name)
			}
		}
	}

	return datasets, nil
}

func (s *Service) resolveNetworkBridgeName(switchType string, switchID any) (string, error) {
	switch strings.ToLower(switchType) {
	case "standard":
		var sw networkModels.StandardSwitch
		if err := s.DB.Where("id = ?", switchID).First(&sw).Error; err != nil {
			return "", err
		}
		return strings.TrimSpace(sw.BridgeName), nil
	case "manual":
		var sw networkModels.ManualSwitch
		if err := s.DB.Where("id = ?", switchID).First(&sw).Error; err != nil {
			return "", err
		}
		return strings.TrimSpace(sw.Bridge), nil
	default:
		return "", nil
	}
}

func (s *Service) remoteDatasetExists(ctx context.Context, identity *clusterModels.ClusterSSHIdentity, privateKeyPath string, dataset string) (bool, error) {
	sshArgs := buildClusterSSHArgs(identity, privateKeyPath)
	sshArgs = append(sshArgs,
		fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost),
		"zfs", "list", "-H", dataset,
	)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		if strings.Contains(strings.ToLower(output), "dataset does not exist") ||
			strings.Contains(strings.ToLower(output), "cannot open") {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (s *Service) remoteBridgeExists(ctx context.Context, identity *clusterModels.ClusterSSHIdentity, privateKeyPath string, bridge string) (bool, error) {
	sshArgs := buildClusterSSHArgs(identity, privateKeyPath)
	sshArgs = append(sshArgs,
		fmt.Sprintf("%s@%s", identity.SSHUser, identity.SSHHost),
		"/sbin/ifconfig", bridge,
	)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		combined := strings.ToLower(strings.TrimSpace(output + " " + err.Error()))
		if strings.Contains(combined, "does not exist") ||
			strings.Contains(combined, "not found") ||
			strings.Contains(combined, "no such interface") {
			return false, nil
		}
		return false, fmt.Errorf("%s: %s", strings.TrimSpace(output), err)
	}
	return true, nil
}
