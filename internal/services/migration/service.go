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
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
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
	ErrTargetNodeOffline     = fmt.Errorf("target_node_offline")
	ErrTargetAlreadyHasGuest = fmt.Errorf("target_already_has_guest")
	ErrSSHUnreachable        = fmt.Errorf("ssh_unreachable")
	ErrCancelNotAllowed      = fmt.Errorf("cancel_not_allowed_in_current_phase")
)

type migrationPayload struct {
	TargetNodeUUID     string   `json:"targetNodeUuid"`
	OperationToken     string   `json:"operationToken,omitempty"`
	OriginalRunning    *bool    `json:"originalRunning,omitempty"`
	Phase              string   `json:"phase"`
	PhaseMessage       string   `json:"phaseMessage"`
	Warnings           []string `json:"warnings,omitempty"`
	SourceDatasetRoots []string `json:"sourceDatasetRoots,omitempty"`
}

type guestWorkloadGurad interface {
	AcquireGuestLock(guestType string, guestID uint, operation string) (bool, string)
	ReleaseGuestLock(guestType string, guestID uint)
	AcquireGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, targetNodeID string, taskID uint, token string) error
	WaitGuestMigrationInterlockAcquired(ctx context.Context, guestType string, guestID uint, targetNodeID string, token string) error
	SealGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, token string) error
	WaitGuestMigrationInterlockApplied(ctx context.Context, guestType string, guestID uint, targetNodeID string, token string) error
	AbortGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, token string) error
	CompleteGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, targetNodeID string, token string) error
	MigrateGuestOwnership(ctx context.Context, guestType string, guestID uint, newOwnerNodeID string, operationToken ...string) error
}

type Service struct {
	DB            *gorm.DB
	TelemetryDB   *gorm.DB
	Cluster       *cluster.Service
	Libvirt       *libvirt.Service
	Jail          *jail.Service
	GZFS          *gzfs.Client
	WorkloadGuard guestWorkloadGurad

	activeMu  sync.Mutex
	active    map[uint]struct{}
	cutoverMu sync.Mutex
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
	}
}

func (s *Service) writeTransferProgress(taskID uint, dataset string, phase string, totalBytes, sentBytes uint64) {
	if taskID == 0 || dataset == "" {
		return
	}

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

	if err := s.requireReplicationDisabledForMigration(ctx, req.GuestType, req.GuestID); err != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, err.Error())
		return result, nil
	}

	if err := s.requireTargetGuestRecordAbsent(ctx, targetNode, req.GuestID); err != nil {
		result.Allowed = false
		result.Reasons = append(result.Reasons, err.Error())
		return result, nil
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
	if err := s.DB.
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("CPUPinning").
		Where("rid = ?", rid).First(&vm).Error; err != nil {
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

	reasons = append(reasons, s.vmConfigPreflightReasons(vm, targetNode)...)
	reasons = append(reasons, s.vmTargetPreflightReasons(ctx, vm, targetNode)...)

	return reasons
}

func collectVMISOUUIDs(storages []vmModels.Storage) ([]string, map[string]string) {
	uuids := make([]string, 0)
	nameByUUID := make(map[string]string)
	seen := make(map[string]struct{})

	for _, st := range storages {
		if !st.Enable {
			continue
		}
		if st.Type != vmModels.VMStorageTypeDiskImage {
			continue
		}
		uuid := strings.TrimSpace(st.DownloadUUID)
		if uuid == "" {
			continue
		}
		if _, ok := seen[uuid]; ok {
			continue
		}
		seen[uuid] = struct{}{}
		uuids = append(uuids, uuid)

		name := strings.TrimSpace(st.Name)
		if name == "" {
			name = uuid
		}
		nameByUUID[uuid] = name
	}

	return uuids, nameByUUID
}

func (s *Service) vmConfigPreflightReasons(vm vmModels.VM, targetNode clusterModels.ClusterNode) []string {
	var reasons []string

	if len(vm.PCIDevices) > 0 {
		reasons = append(reasons, "warning_pci_passthrough_not_migrated")
	}
	if len(vm.CPUPinning) > 0 {
		reasons = append(reasons, "warning_cpu_pinning_reset")
	}

	if targetNode.Memory > 0 && vm.RAM > 0 {
		usage := targetNode.MemoryUsage
		if usage < 0 {
			usage = 0
		}
		if usage > 100 {
			usage = 100
		}
		freeBytes := targetNode.Memory - uint64(float64(targetNode.Memory)*usage/100.0)
		if freeBytes < uint64(vm.RAM) {
			reasons = append(reasons, fmt.Sprintf("warning_target_insufficient_memory: needs %d bytes, ~%d free", vm.RAM, freeBytes))
		}
	}

	return reasons
}

func (s *Service) resolveNetworkSwitchInfo(switchType string, switchID any) (name string, bridge string, err error) {
	switch strings.ToLower(strings.TrimSpace(switchType)) {
	case "manual":
		var sw networkModels.ManualSwitch
		if err := s.DB.Where("id = ?", switchID).First(&sw).Error; err != nil {
			return "", "", err
		}
		return strings.TrimSpace(sw.Name), strings.TrimSpace(sw.Bridge), nil
	case "standard", "":
		var sw networkModels.StandardSwitch
		if err := s.DB.Where("id = ?", switchID).First(&sw).Error; err != nil {
			return "", "", err
		}
		return strings.TrimSpace(sw.Name), strings.TrimSpace(sw.BridgeName), nil
	default:
		return "", "", nil
	}
}

type vmTargetSwitch struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Bridge string `json:"bridge"`
}

type vmTargetProbe struct {
	RID        uint             `json:"rid"`
	MediaUUIDs []string         `json:"mediaUuids"`
	VNCPort    int              `json:"vncPort"`
	Switches   []vmTargetSwitch `json:"switches"`
	FsDatasets []string         `json:"fsDatasets"`
}

type vmTargetResult struct {
	MissingMedia      []string `json:"missingMedia"`
	VNCPortInUse      bool     `json:"vncPortInUse"`
	MissingSwitches   []string `json:"missingSwitches"`
	MissingFsDatasets []string `json:"missingFsDatasets"`
}

func (s *Service) requireTargetGuestRecordAbsent(
	ctx context.Context,
	targetNode clusterModels.ClusterNode,
	guestID uint,
) error {
	entry, err := s.remoteTargetGuestRecord(ctx, targetNode, guestID)
	if err != nil {
		return fmt.Errorf("target_guest_record_check_failed: %w", err)
	}
	if entry == nil {
		return nil
	}

	guestType := strings.ToLower(strings.TrimSpace(entry.GuestType))
	if guestType == "" {
		guestType = "unknown"
	}
	return fmt.Errorf(
		"%w: guest_id=%d guest_type=%s",
		ErrTargetAlreadyHasGuest,
		guestID,
		guestType,
	)
}

// remoteTargetGuestRecord reads the target node's authenticated, DB-only
// identity inventory. VM RIDs and jail CTIDs deliberately share one numeric
// namespace, so either record type blocks migration of the requested ID.
func (s *Service) remoteTargetGuestRecord(
	ctx context.Context,
	targetNode clusterModels.ClusterNode,
	guestID uint,
) (*cluster.GuestIdentityInventoryEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.Cluster == nil || s.Cluster.AuthService == nil {
		return nil, fmt.Errorf("cluster_auth_unavailable")
	}

	targetNodeID := strings.TrimSpace(targetNode.NodeUUID)
	targetAPI := strings.TrimSpace(targetNode.API)
	if targetNodeID == "" || targetAPI == "" {
		return nil, fmt.Errorf("target_identity_inventory_endpoint_unavailable")
	}

	clusterToken, err := s.Cluster.AuthService.CreateInternalClusterJWT("migration", "")
	if err != nil {
		return nil, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	body, statusCode, err := utils.HTTPGetJSONReadContext(
		ctx,
		fmt.Sprintf("https://%s/api/intra-cluster/guest-identity-inventory", targetAPI),
		map[string]string{
			"Accept":          "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("target_identity_inventory_request_failed_http_%d: %w", statusCode, err)
	}

	var response struct {
		Status  string                                 `json:"status"`
		Message string                                 `json:"message"`
		Error   string                                 `json:"error"`
		Data    cluster.GuestIdentityInventorySnapshot `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("target_identity_inventory_decode_failed: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Status), "success") {
		return nil, fmt.Errorf(
			"target_identity_inventory_non_success: message=%s error=%s",
			strings.TrimSpace(response.Message),
			strings.TrimSpace(response.Error),
		)
	}
	if strings.TrimSpace(response.Data.NodeID) != targetNodeID {
		return nil, fmt.Errorf(
			"target_identity_inventory_node_mismatch: expected=%s actual=%s",
			targetNodeID,
			strings.TrimSpace(response.Data.NodeID),
		)
	}
	canonical := cluster.BuildGuestIdentityInventoryReport(response.Data.Report.Entries)
	if !reflect.DeepEqual(response.Data.Report.Entries, canonical.Entries) {
		return nil, fmt.Errorf("target_identity_inventory_entries_not_canonical")
	}
	if response.Data.Report.Digest != canonical.Digest {
		return nil, fmt.Errorf("target_identity_inventory_digest_mismatch")
	}

	for i := range response.Data.Report.Entries {
		entry := &response.Data.Report.Entries[i]
		if strings.TrimSpace(entry.NodeID) != targetNodeID {
			return nil, fmt.Errorf(
				"target_identity_inventory_entry_node_mismatch: expected=%s actual=%s guest_id=%d",
				targetNodeID,
				strings.TrimSpace(entry.NodeID),
				entry.GuestID,
			)
		}
		if entry.GuestID == guestID {
			return entry, nil
		}
	}
	return nil, nil
}

func (s *Service) buildVMTargetProbe(vm vmModels.VM) (vmTargetProbe, map[string]string) {
	uuids, nameByUUID := collectVMISOUUIDs(vm.Storages)

	probe := vmTargetProbe{
		RID:        vm.RID,
		MediaUUIDs: uuids,
	}
	if vm.VNCEnabled {
		probe.VNCPort = vm.VNCPort
	}

	seenSwitch := make(map[string]struct{})
	for _, net := range vm.Networks {
		if !net.Enable {
			continue
		}
		name, bridge, resErr := s.resolveNetworkSwitchInfo(net.SwitchType, net.SwitchID)
		if resErr != nil {
			logger.L.Debug().Err(resErr).Uint("rid", vm.RID).Msg("failed_to_resolve_source_switch_for_preflight")
			continue
		}
		if name == "" && bridge == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(net.SwitchType)) + "|" + name + "|" + bridge
		if _, ok := seenSwitch[key]; ok {
			continue
		}
		seenSwitch[key] = struct{}{}
		probe.Switches = append(probe.Switches, vmTargetSwitch{
			Name:   name,
			Type:   strings.TrimSpace(net.SwitchType),
			Bridge: bridge,
		})
	}

	seenFs := make(map[string]struct{})
	for _, st := range vm.Storages {
		if !st.Enable || st.Type != vmModels.VMStorageTypeFilesystem {
			continue
		}
		ds := strings.TrimSpace(st.Dataset.Name)
		if ds == "" {
			continue
		}
		if _, ok := seenFs[ds]; ok {
			continue
		}
		seenFs[ds] = struct{}{}
		probe.FsDatasets = append(probe.FsDatasets, ds)
	}

	return probe, nameByUUID
}

func (s *Service) vmTargetPreflightReasons(ctx context.Context, vm vmModels.VM, targetNode clusterModels.ClusterNode) []string {
	probe, nameByUUID := s.buildVMTargetProbe(vm)
	if len(probe.MediaUUIDs) == 0 && len(probe.Switches) == 0 && len(probe.FsDatasets) == 0 && probe.VNCPort == 0 {
		return nil
	}

	result, unsupported, err := s.remoteCheckVMTarget(ctx, targetNode, probe)
	if err != nil {
		return []string{fmt.Sprintf("target_check_failed: %v", err)}
	}
	if unsupported {
		return []string{"target_check_unsupported"}
	}

	var reasons []string
	for _, uuid := range result.MissingMedia {
		name := strings.TrimSpace(nameByUUID[uuid])
		if name == "" {
			name = uuid
		}
		reasons = append(reasons, fmt.Sprintf("warning_target_missing_iso: %s", name))
	}
	for _, sw := range result.MissingSwitches {
		reasons = append(reasons, fmt.Sprintf("warning_target_missing_switch: %s", sw))
	}
	for _, ds := range result.MissingFsDatasets {
		reasons = append(reasons, fmt.Sprintf("warning_9p_share_not_migrated: %s", ds))
	}
	if result.VNCPortInUse {
		reasons = append(reasons, fmt.Sprintf("warning_target_vnc_port_in_use: %d", probe.VNCPort))
	}

	return reasons
}

func (s *Service) remoteCheckVMTarget(ctx context.Context, targetNode clusterModels.ClusterNode, probe vmTargetProbe) (vmTargetResult, bool, error) {
	if s.Cluster == nil || s.Cluster.AuthService == nil {
		return vmTargetResult{}, false, fmt.Errorf("cluster_auth_unavailable")
	}

	clusterToken, tokenErr := s.Cluster.AuthService.CreateInternalClusterJWT("migration", "")
	if tokenErr != nil {
		return vmTargetResult{}, false, fmt.Errorf("create_cluster_token_failed: %w", tokenErr)
	}

	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	url := fmt.Sprintf("https://%s/api/intra-cluster/migration/check-vm-target", targetNode.API)
	body, marshalErr := json.Marshal(probe)
	if marshalErr != nil {
		return vmTargetResult{}, false, fmt.Errorf("marshal_target_check_payload_failed: %w", marshalErr)
	}

	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		respBody, respStatus, reqErr := utils.HTTPPostJSONWithTimeout(url, body, headers, 10*time.Second)

		if respStatus == 404 {
			return vmTargetResult{}, true, nil
		}
		if respStatus >= 300 {
			return vmTargetResult{}, false, fmt.Errorf("target_check_returned_http_%d: %s", respStatus, string(respBody))
		}

		if reqErr != nil {
			lastErr = reqErr
			if attempt < attempts-1 {
				select {
				case <-ctx.Done():
					return vmTargetResult{}, false, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
				}
			}
			continue
		}

		var parsed struct {
			Data vmTargetResult `json:"data"`
		}
		if jsonErr := json.Unmarshal(respBody, &parsed); jsonErr != nil {
			return vmTargetResult{}, false, fmt.Errorf("target_check_parse_failed: %w", jsonErr)
		}

		return parsed.Data, false, nil
	}

	return vmTargetResult{}, false, fmt.Errorf("target_check_request_failed: %w", lastErr)
}

func (s *Service) validateJailPreflight(ctx context.Context, ctID uint, targetNode clusterModels.ClusterNode) []string {
	var reasons []string
	var sourceJail jailModels.Jail
	if err := s.DB.Select("id").Where("ct_id = ?", ctID).First(&sourceJail).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []string{"jail_not_found"}
		}
		return []string{fmt.Sprintf("jail_lookup_failed: %v", err)}
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

	var jailStorages []string
	if err := s.DB.Raw("SELECT DISTINCT pool FROM jail_storages WHERE jid = ?", sourceJail.ID).Scan(&jailStorages).Error; err != nil {
		return []string{fmt.Sprintf("jail_storage_lookup_failed: %v", err)}
	}

	type jailNetworkRow struct {
		SwitchID   uint
		SwitchType string
	}
	var jailNetworks []jailNetworkRow
	if err := s.DB.Raw(`
		SELECT jn.switch_id, jn.switch_type
		FROM jail_networks jn
		WHERE jn.jid = ?
	`, sourceJail.ID).Scan(&jailNetworks).Error; err != nil {
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

func hardMigrationPreflightReasons(reasons []string) []string {
	hard := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(reason)), "warning_") {
			continue
		}
		hard = append(hard, reason)
	}
	return hard
}

// phaseFinalCutoverRevalidation repeats target capability checks after the
// potentially long initial transfer and proves that every expected staged root
// exists on both sides. Only after this succeeds may the durable guard seal and
// the source be stopped.
func (s *Service) phaseFinalCutoverRevalidation(
	ctx context.Context,
	mp *migrationPayload,
	task taskModels.GuestLifecycleTask,
	roots []string,
) error {
	if ctx == nil || mp == nil || len(roots) == 0 || s.Cluster == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("migration_cutover_revalidation_unavailable")
	}

	var targetNode clusterModels.ClusterNode
	if err := s.DB.Where("node_uuid = ?", mp.TargetNodeUUID).First(&targetNode).Error; err != nil {
		return fmt.Errorf("target_node_not_found: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(targetNode.Status), "online") {
		return ErrTargetNodeOffline
	}
	if err := s.requireTargetGuestRecordAbsent(ctx, targetNode, task.GuestID); err != nil {
		return fmt.Errorf("migration_cutover_target_revalidation_failed: %w", err)
	}
	identity, err := s.getNodeSSHIdentity(targetNode.NodeUUID)
	if err != nil {
		return fmt.Errorf("target_ssh_identity_unavailable: %w", err)
	}
	privateKeyPath, err := s.Cluster.ClusterSSHPrivateKeyPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_key_unavailable: %w", err)
	}

	var reasons []string
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		var vm vmModels.VM
		if err := s.DB.Preload("Storages").Preload("Storages.Dataset").
			Preload("Networks").Preload("CPUPinning").
			Where("rid = ?", task.GuestID).First(&vm).Error; err != nil {
			return fmt.Errorf("vm_not_found_for_cutover_revalidation: %w", err)
		}
		reasons = append(reasons, s.vmConfigPreflightReasons(vm, targetNode)...)
		reasons = append(reasons, s.vmTargetPreflightReasons(ctx, vm, targetNode)...)
	case taskModels.GuestTypeJail:
		reasons = append(reasons, s.validateJailPreflight(ctx, task.GuestID, targetNode)...)
	default:
		return fmt.Errorf("unsupported_guest_type: %s", task.GuestType)
	}
	if hard := hardMigrationPreflightReasons(reasons); len(hard) != 0 {
		return fmt.Errorf("migration_cutover_target_revalidation_failed: %s", strings.Join(hard, "; "))
	}

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if !isCanonicalMigrationGuestDataset(root, task.GuestType, task.GuestID) {
			return fmt.Errorf("migration_cutover_dataset_root_invalid: %s", root)
		}
		if _, err := s.GZFS.ZFS.Get(ctx, root, false); err != nil {
			return fmt.Errorf("migration_cutover_source_dataset_unavailable_%s: %w", root, err)
		}
		pool := strings.SplitN(root, "/", 2)[0]
		exists, err := s.remotePoolExists(ctx, identity, pool)
		if err != nil {
			return fmt.Errorf("migration_cutover_target_pool_check_failed_%s: %w", pool, err)
		}
		if !exists {
			return fmt.Errorf("migration_cutover_target_pool_missing: %s", pool)
		}
		exists, err = s.remoteDatasetExists(ctx, identity, privateKeyPath, root)
		if err != nil {
			return fmt.Errorf("migration_cutover_target_dataset_check_failed_%s: %w", root, err)
		}
		if !exists {
			return fmt.Errorf("migration_cutover_target_dataset_missing: %s", root)
		}
	}

	return nil
}

func (s *Service) ExecuteMigration(ctx context.Context, taskID uint) (retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.beginMigrationExecution(taskID) {
		return &migrationRecoveryPendingError{cause: ErrMigrationInProgress}
	}
	defer s.endMigrationExecution(taskID)

	task := taskModels.GuestLifecycleTask{}
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}
	// A late lifecycle delivery must not reopen or overwrite a task that was
	// already closed by receipt-based finalize recovery.
	if task.Status == taskModels.LifecycleTaskStatusSuccess {
		return nil
	}

	var mp migrationPayload
	if strings.TrimSpace(task.Payload) != "" {
		if err := json.Unmarshal([]byte(task.Payload), &mp); err != nil {
			s.updateTaskFailed(taskID, fmt.Sprintf("invalid_payload: %v", err))
			return err
		}
	}
	// A finalize checkpoint with no remaining cutover operation is historical
	// completion recovery, not a new migration attempt. Reconcile it before the
	// current-policy gate: once Complete removed the durable guard, a user may
	// legitimately re-enable replication before this task result is persisted.
	if handled, err := s.reconcileOperationAbsentFinalizeTask(ctx, task, mp); handled {
		if err != nil {
			s.updateTaskRecoveryPending(taskID, err.Error())
			return &migrationRecoveryPendingError{cause: err}
		}
		return nil
	}
	// Protection is authoritative even when the task payload or cluster
	// identity is damaged. New or still-guarded migrations fail closed for a
	// protected guest.
	if err := s.requireReplicationDisabledForMigration(ctx, task.GuestType, task.GuestID); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	sourceNodeID := ""
	if s.Cluster != nil {
		sourceNodeID = strings.TrimSpace(s.Cluster.LocalNodeID())
	}
	if sourceNodeID == "" {
		err := fmt.Errorf("local_node_id_unavailable")
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	operationToken, err := bindMigrationOperationToken(&mp, sourceNodeID, taskID)
	if err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	operation, err := s.exactMigrationOperationForTask(task, mp.TargetNodeUUID, operationToken)
	if err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	interlockSealed := operation != nil && operation.State == clusterModels.ReplicationGuestOperationCutover
	if operation != nil && strings.TrimSpace(mp.TargetNodeUUID) == "" {
		mp.TargetNodeUUID = strings.TrimSpace(operation.TargetNodeID)
	}
	defer func() {
		if !interlockSealed || retErr == nil {
			return
		}
		s.updateTaskRecoveryPending(taskID, retErr.Error())
		retErr = &migrationRecoveryPendingError{cause: retErr}
	}()

	if task.Status == taskModels.LifecycleTaskStatusSuccess && !interlockSealed {
		return nil
	}
	if task.Status == taskModels.LifecycleTaskStatusFailed && !interlockSealed {
		return nil
	}
	if operation != nil && operation.State != clusterModels.ReplicationGuestOperationPreCutover &&
		operation.State != clusterModels.ReplicationGuestOperationCutover {
		return fmt.Errorf("invalid_migration_operation_state: %s", operation.State)
	}
	if strings.TrimSpace(mp.TargetNodeUUID) == "" {
		err := fmt.Errorf("migration_target_node_required")
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	if s.WorkloadGuard == nil {
		err := fmt.Errorf("migration_workload_guard_unavailable")
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	if acquired, existing := s.WorkloadGuard.AcquireGuestLock(
		task.GuestType, task.GuestID, fmt.Sprintf("migration:%d", taskID),
	); !acquired {
		reason := fmt.Sprintf("guest_locked_by: %s", existing)
		s.updateTaskFailed(taskID, reason)
		return fmt.Errorf("%s", reason)
	}
	defer s.WorkloadGuard.ReleaseGuestLock(task.GuestType, task.GuestID)

	if interlockSealed {
		s.updateTaskDB(taskID, map[string]any{
			"status":      taskModels.LifecycleTaskStatusRunning,
			"finished_at": nil,
			"message":     "migration_recovery_resuming",
		})
		if err := s.WorkloadGuard.WaitGuestMigrationInterlockApplied(
			ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, operationToken,
		); err != nil {
			return fmt.Errorf("migration_interlock_recovery_barrier_failed: %w", err)
		}
		if len(mp.SourceDatasetRoots) == 0 {
			return fmt.Errorf("migration_recovery_source_dataset_roots_missing")
		}
		return s.executeSealedMigration(ctx, task, &mp, operationToken)
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

	if err := s.checkCancelled(taskID); err != nil {
		return s.handleCancel(taskID, mp)
	}
	if err := s.WorkloadGuard.AcquireGuestMigrationInterlock(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, taskID, operationToken,
	); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_interlock_acquire_failed: %w", err)
	}
	defer func() {
		if interlockSealed {
			return
		}
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.abortPreCutoverInterlockConvergently(
			abortCtx, task.GuestType, task.GuestID, operationToken,
		); err != nil {
			logger.L.Error().Err(err).
				Str("guest_type", task.GuestType).
				Uint("guest_id", task.GuestID).
				Uint("task_id", taskID).
				Msg("migration_pre_cutover_interlock_abort_failed")
		}
	}()
	if err := s.WorkloadGuard.WaitGuestMigrationInterlockAcquired(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, operationToken,
	); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_interlock_acquire_barrier_failed: %w", err)
	}
	if err := s.checkCancelled(taskID); err != nil {
		return s.handleCancel(taskID, mp)
	}

	mp.Phase = PhaseInitialReplicaton
	mp.PhaseMessage = "replicating_datasets_to_target"
	s.updateTaskPhase(taskID, mp)

	if err := s.phaseInitialReplication(ctx, &mp, task); err != nil {
		if isExplicitMigrationCancellation(err) {
			return s.handleCancel(taskID, mp)
		}
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	roots, err := s.resolveGuestDatasets(ctx, task.GuestType, task.GuestID)
	if err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_cutover_dataset_resolution_failed: %w", err)
	}
	roots = filterParentDatasets(roots)
	if len(roots) == 0 {
		err := fmt.Errorf("migration_cutover_source_dataset_roots_missing")
		s.updateTaskFailed(taskID, err.Error())
		return err
	}
	mp.SourceDatasetRoots = append([]string(nil), roots...)
	mp.PhaseMessage = "revalidating_target_before_cutover"
	if err := s.persistTaskPhase(taskID, mp); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_cutover_checkpoint_persist_failed: %w", err)
	}
	if err := s.phaseFinalCutoverRevalidation(ctx, &mp, task, roots); err != nil {
		s.updateTaskFailed(taskID, err.Error())
		return err
	}

	s.cutoverMu.Lock()
	if err := s.checkCancelled(taskID); err != nil {
		s.cutoverMu.Unlock()
		return s.handleCancel(taskID, mp)
	}
	if err := s.WorkloadGuard.SealGuestMigrationInterlock(
		ctx, task.GuestType, task.GuestID, operationToken,
	); err != nil {
		s.cutoverMu.Unlock()
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_interlock_seal_failed: %w", err)
	}
	interlockSealed = true
	if err := s.WorkloadGuard.WaitGuestMigrationInterlockApplied(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, operationToken,
	); err != nil {
		s.cutoverMu.Unlock()
		s.updateTaskFailed(taskID, err.Error())
		return fmt.Errorf("migration_interlock_apply_barrier_failed: %w", err)
	}
	mp.Phase = PhaseStopSource
	mp.PhaseMessage = "stopping_guest_on_source"
	if err := s.persistTaskPhase(taskID, mp); err != nil {
		s.cutoverMu.Unlock()
		return fmt.Errorf("migration_stop_source_checkpoint_failed: %w", err)
	}
	s.cutoverMu.Unlock()

	return s.executeSealedMigration(ctx, task, &mp, operationToken)
}

func bindMigrationOperationToken(mp *migrationPayload, localNodeID string, taskID uint) (string, error) {
	localNodeID = strings.TrimSpace(localNodeID)
	if mp == nil || localNodeID == "" || taskID == 0 {
		return "", fmt.Errorf("migration_operation_token_binding_invalid")
	}
	expected := fmt.Sprintf("migration:%s:%d", localNodeID, taskID)
	if strings.TrimSpace(mp.OperationToken) != "" && mp.OperationToken != expected {
		return "", fmt.Errorf("migration_operation_token_mismatch")
	}
	mp.OperationToken = expected
	return expected, nil
}

func (s *Service) executeSealedMigration(
	ctx context.Context,
	task taskModels.GuestLifecycleTask,
	mp *migrationPayload,
	operationToken string,
) error {
	if mp == nil || len(mp.SourceDatasetRoots) == 0 {
		return fmt.Errorf("migration_source_dataset_roots_missing")
	}
	if strings.TrimSpace(operationToken) == "" || mp.OperationToken != operationToken {
		return fmt.Errorf("migration_operation_token_mismatch")
	}

	if migrationPhaseAtOrBefore(mp.Phase, PhaseStopSource) {
		mp.Phase = PhaseStopSource
		mp.PhaseMessage = "stopping_guest_on_source"
		if err := s.persistTaskPhase(task.ID, *mp); err != nil {
			return fmt.Errorf("migration_stop_source_checkpoint_failed: %w", err)
		}
		if err := s.phaseStopSource(ctx, mp, task); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return err
		}
	}

	if migrationPhaseAtOrBefore(mp.Phase, PhaseFinalSync) {
		mp.Phase = PhaseFinalSync
		mp.PhaseMessage = "performing_final_incremental_sync"
		if err := s.persistTaskPhase(task.ID, *mp); err != nil {
			return fmt.Errorf("migration_final_sync_checkpoint_failed: %w", err)
		}
		if err := s.phaseFinalSync(ctx, mp, task); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return err
		}
	}

	if migrationPhaseAtOrBefore(mp.Phase, PhaseStartTarget) {
		mp.Phase = PhaseStartTarget
		mp.PhaseMessage = "importing_stopped_guest_on_target"
		if mp.OriginalRunning != nil && *mp.OriginalRunning {
			mp.PhaseMessage = "starting_guest_on_target"
		}
		if err := s.persistTaskPhase(task.ID, *mp); err != nil {
			return fmt.Errorf("migration_start_target_checkpoint_failed: %w", err)
		}
		if err := s.phaseStartTarget(ctx, mp, task, operationToken); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return err
		}
	}

	if migrationPhaseAtOrBefore(mp.Phase, PhasePolicyAdjustment) {
		mp.Phase = PhasePolicyAdjustment
		mp.PhaseMessage = "adjusting_cluster_policies"
		if err := s.persistTaskPhase(task.ID, *mp); err != nil {
			return fmt.Errorf("migration_policy_adjustment_checkpoint_failed: %w", err)
		}
		if err := s.phasePolicyAdjustment(ctx, mp, task, operationToken); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return fmt.Errorf("migration_policy_adjustment_failed: %w", err)
		}
	}

	if migrationPhaseAtOrBefore(mp.Phase, PhaseCleanupSource) {
		mp.Phase = PhaseCleanupSource
		mp.PhaseMessage = "cleaning_up_source_guest"
		if err := s.persistTaskPhase(task.ID, *mp); err != nil {
			return fmt.Errorf("migration_source_cleanup_checkpoint_failed: %w", err)
		}
		if err := s.phaseCleanupSource(ctx, mp, task); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return fmt.Errorf("migration_source_cleanup_failed: %w", err)
		}
		if err := s.verifyMigrationSourceCleanup(ctx, task.GuestType, task.GuestID, mp.SourceDatasetRoots); err != nil {
			s.updateTaskFailed(task.ID, err.Error())
			return fmt.Errorf("migration_source_cleanup_unverified: %w", err)
		}
	}

	mp.Phase = PhaseFinalize
	mp.PhaseMessage = "finalizing_migration"
	if err := s.persistTaskPhase(task.ID, *mp); err != nil {
		return fmt.Errorf("migration_finalize_checkpoint_failed: %w", err)
	}
	if err := s.WorkloadGuard.CompleteGuestMigrationInterlock(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, operationToken,
	); err != nil {
		s.updateTaskFailed(task.ID, err.Error())
		return fmt.Errorf("migration_interlock_complete_failed: %w", err)
	}
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.waitForExactMigrationOperationAbsent(
		releaseCtx, task.GuestType, task.GuestID, operationToken,
	); err != nil {
		return fmt.Errorf("migration_interlock_complete_unconfirmed: %w", err)
	}

	finishedAt := time.Now().UTC()
	s.updateTaskDB(task.ID, map[string]any{
		"status":      taskModels.LifecycleTaskStatusSuccess,
		"finished_at": finishedAt,
		"message":     "migration_completed",
		"error":       "",
	})
	return nil
}

func migrationPhaseAtOrBefore(current, target string) bool {
	order := map[string]int{
		PhaseStopSource:       0,
		PhaseFinalSync:        1,
		PhaseStartTarget:      2,
		PhasePolicyAdjustment: 3,
		PhaseCleanupSource:    4,
		PhaseFinalize:         5,
	}
	currentRank, ok := order[current]
	if !ok {
		currentRank = 0
	}
	targetRank, ok := order[target]
	return ok && currentRank <= targetRank
}

func (s *Service) CancelMigration(ctx context.Context, taskID uint) error {
	s.cutoverMu.Lock()
	defer s.cutoverMu.Unlock()

	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}

	if task.Action != "migrate" {
		return fmt.Errorf("not_a_migration_task")
	}
	if task.Status == taskModels.LifecycleTaskStatusSuccess || task.Status == taskModels.LifecycleTaskStatusFailed {
		return ErrCancelNotAllowed
	}

	var mp migrationPayload
	if strings.TrimSpace(task.Payload) != "" {
		if err := json.Unmarshal([]byte(task.Payload), &mp); err != nil {
			return err
		}
	}

	var operation clusterModels.ReplicationGuestOperation
	operationResult := s.DB.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", task.GuestType, task.GuestID).
		Limit(1).
		Find(&operation)
	if operationResult.Error != nil {
		return fmt.Errorf("migration_cancellation_guard_lookup_failed: %w", operationResult.Error)
	}
	if operationResult.RowsAffected == 1 {
		expectedToken := fmt.Sprintf("migration:%s:%d", strings.TrimSpace(operation.OwnerNodeID), task.ID)
		if operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
			operation.TaskID != task.ID || strings.TrimSpace(operation.OwnerNodeID) == "" ||
			strings.TrimSpace(operation.Token) != expectedToken ||
			strings.TrimSpace(operation.TargetNodeID) != strings.TrimSpace(mp.TargetNodeUUID) {
			return fmt.Errorf("migration_cancellation_guard_mismatch")
		}
		if operation.State == clusterModels.ReplicationGuestOperationCutover {
			return ErrCancelNotAllowed
		}
		if operation.State != clusterModels.ReplicationGuestOperationPreCutover {
			return fmt.Errorf("migration_cancellation_guard_state_invalid: %s", operation.State)
		}
	}

	switch mp.Phase {
	case "", PhasePreflight, PhaseInitialReplicaton:
		now := time.Now().UTC()
		result := s.DB.WithContext(ctx).Model(&taskModels.GuestLifecycleTask{}).
			Where("id = ? AND status IN ?", taskID, []string{
				taskModels.LifecycleTaskStatusQueued,
				taskModels.LifecycleTaskStatusRunning,
			}).Updates(map[string]any{
			"override_requested": true,
			"updated_at":         now,
			"message":            "cancellation_requested",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCancelNotAllowed
		}
		return nil
	default:
		return ErrCancelNotAllowed
	}
}

func (s *Service) checkCancelled(taskID uint) error {
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task, taskID).Error; err != nil {
		return err
	}
	if !task.OverrideRequested {
		return nil
	}
	var mp migrationPayload
	if strings.TrimSpace(task.Payload) != "" {
		if err := json.Unmarshal([]byte(task.Payload), &mp); err != nil {
			return err
		}
	}
	switch mp.Phase {
	case "", PhasePreflight, PhaseInitialReplicaton:
		return fmt.Errorf("migration_cancelled")
	default:
		return nil
	}
}

func (s *Service) handleCancel(taskID uint, mp migrationPayload) error {
	mp.PhaseMessage = "migration_cancelled"
	s.updateTaskPhase(taskID, mp)

	finishedAt := time.Now().UTC()
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(map[string]any{
		"status":      taskModels.LifecycleTaskStatusFailed,
		"finished_at": finishedAt,
		"message":     "migration_cancelled",
		"error":       "cancelled_by_user",
	}).Error; err != nil {
		return err
	}
	return &migrationTaskResultPersistedError{cause: fmt.Errorf("migration_cancelled")}
}

func (s *Service) updateTaskPhase(taskID uint, mp migrationPayload) {
	if err := s.persistTaskPhase(taskID, mp); err != nil {
		logger.L.Warn().Err(err).Uint("task_id", taskID).Msg("migration_task_phase_update_failed")
	}
}

func (s *Service) persistTaskPhase(taskID uint, mp migrationPayload) error {
	if len(mp.SourceDatasetRoots) != 0 &&
		(strings.TrimSpace(mp.OperationToken) == "" ||
			mp.OperationToken != strings.TrimSpace(mp.OperationToken)) {
		return fmt.Errorf("migration_operation_token_required")
	}
	b, err := json.Marshal(mp)
	if err != nil {
		return err
	}
	result := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(map[string]any{
		"message": mp.PhaseMessage,
		"payload": string(b),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("migration_task_not_found")
	}
	return nil
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

func (s *Service) updateTaskRecoveryPending(taskID uint, errMsg string) {
	s.updateTaskDB(taskID, map[string]any{
		"status":      taskModels.LifecycleTaskStatusRunning,
		"finished_at": nil,
		"message":     "migration_recovery_pending",
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
		if isCanonicalMigrationGuestDataset(event.SourceDataset, guestType, guestID) {
			return true
		}
	}

	if guestType == taskModels.GuestTypeJail && event.Mode == clusterModels.BackupJobModeJail {
		if isCanonicalMigrationGuestDataset(event.SourceDataset, guestType, guestID) ||
			isCanonicalMigrationGuestDataset(event.TargetEndpoint, guestType, guestID) {
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

// isCanonicalMigrationGuestDataset matches only Sylve's canonical per-guest
// dataset root or one of its descendants. Splitting the path into components
// keeps guest 1 distinct from adjacent IDs such as 10 and 11.
//
// Snapshot suffixes are ignored for ownership matching so both
// "pool/sylve/.../1@snap" and "pool/sylve/.../1/child@snap" belong to guest 1.
// A remote endpoint prefix such as "host:pool" is also valid as the first
// component because the canonical Sylve hierarchy still begins immediately
// below it.
func isCanonicalMigrationGuestDataset(name string, guestType string, guestID uint) bool {
	if guestID == 0 {
		return false
	}

	guestDir := ""
	switch strings.ToLower(strings.TrimSpace(guestType)) {
	case taskModels.GuestTypeVM:
		guestDir = "virtual-machines"
	case taskModels.GuestTypeJail:
		guestDir = "jails"
	default:
		return false
	}

	datasetName := strings.TrimSpace(name)
	if snapshotAt := strings.IndexByte(datasetName, '@'); snapshotAt >= 0 {
		datasetName = datasetName[:snapshotAt]
	}
	parts := strings.Split(datasetName, "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] != "sylve" || parts[2] != guestDir {
		return false
	}
	if parts[3] != strconv.FormatUint(uint64(guestID), 10) {
		return false
	}

	root := strings.Join(parts[:4], "/")
	return datasetName == root || strings.HasPrefix(datasetName, root+"/")
}

func (s *Service) verifyMigrationSourceCleanup(ctx context.Context, guestType string, guestID uint, expectedRoots ...[]string) error {
	if guestID == 0 || s.DB == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("migration_source_cleanup_verification_unavailable")
	}
	var metadataCount int64
	switch guestType {
	case taskModels.GuestTypeVM:
		if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", guestID).Count(&metadataCount).Error; err != nil {
			return err
		}
	case taskModels.GuestTypeJail:
		if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", guestID).Count(&metadataCount).Error; err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported_guest_type: %s", guestType)
	}
	if metadataCount != 0 {
		return fmt.Errorf("migration_source_guest_metadata_still_present")
	}
	if len(expectedRoots) != 0 {
		if err := verifyMigrationDatasetRootsAbsent(ctx, guestType, guestID, expectedRoots[0]); err != nil {
			return err
		}
	}

	datasets, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		return fmt.Errorf("migration_source_dataset_verification_failed: %w", err)
	}
	residue := make([]string, 0)
	for _, dataset := range datasets {
		if dataset == nil {
			continue
		}
		name := strings.TrimSpace(dataset.Name)
		if isCanonicalMigrationGuestDataset(name, guestType, guestID) {
			residue = append(residue, name)
		}
	}
	if len(residue) != 0 {
		sort.Strings(residue)
		return fmt.Errorf("migration_source_datasets_still_present: %s", strings.Join(residue, ","))
	}
	return nil
}

func verifyMigrationDatasetRootsAbsent(ctx context.Context, guestType string, guestID uint, roots []string) error {
	roots = filterParentDatasets(append([]string(nil), roots...))
	if len(roots) == 0 {
		return fmt.Errorf("migration_source_dataset_roots_missing")
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if !isCanonicalMigrationGuestDataset(root, guestType, guestID) {
			return fmt.Errorf("migration_source_dataset_root_invalid: %s", root)
		}
		pool := strings.SplitN(root, "/", 2)[0]
		if err := requireMigrationSourcePoolImported(ctx, pool); err != nil {
			return err
		}
		output, err := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", root)
		if err == nil {
			return fmt.Errorf("migration_source_datasets_still_present: %s", strings.TrimSpace(output))
		}
		combined := strings.ToLower(strings.TrimSpace(output + " " + err.Error()))
		if !strings.Contains(combined, "dataset does not exist") {
			return fmt.Errorf("migration_source_dataset_absence_unproven_%s: %w", root, err)
		}
		// Close the export race around the failed dataset lookup. An exported or
		// otherwise unavailable pool must retain the cutover guard.
		if err := requireMigrationSourcePoolImported(ctx, pool); err != nil {
			return err
		}
	}
	return nil
}

func requireMigrationSourcePoolImported(ctx context.Context, pool string) error {
	pool = strings.TrimSpace(pool)
	if pool == "" || strings.Contains(pool, ":") {
		return fmt.Errorf("migration_source_pool_name_invalid: %s", pool)
	}
	output, err := utils.RunCommandWithContext(ctx, "zpool", "list", "-H", "-o", "name", pool)
	if err != nil || strings.TrimSpace(output) != pool {
		if err == nil {
			err = fmt.Errorf("unexpected_pool_result: %s", strings.TrimSpace(output))
		}
		return fmt.Errorf("migration_source_pool_unavailable_%s: %w", pool, err)
	}
	return nil
}

func (s *Service) resolveVMDatasets(ctx context.Context, rid uint) ([]string, error) {
	var vm vmModels.VM
	if err := s.DB.Preload("Storages").Preload("Storages.Dataset").Where("rid = ?", rid).First(&vm).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var datasets []string

	for _, st := range vm.Storages {
		if st.Type == vmModels.VMStorageTypeDiskImage {
			continue
		}
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
		for _, ds := range list {
			if ds == nil {
				continue
			}
			name := strings.TrimSpace(ds.Name)
			if isCanonicalMigrationGuestDataset(name, taskModels.GuestTypeVM, rid) && !seen[name] {
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
		for _, ds := range list {
			if ds == nil {
				continue
			}
			name := strings.TrimSpace(ds.Name)
			if isCanonicalMigrationGuestDataset(name, taskModels.GuestTypeJail, ctID) && !seen[name] {
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
