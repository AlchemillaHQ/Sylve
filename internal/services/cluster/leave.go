// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	LeavePhaseFenced   = "fenced"
	LeavePhaseRemoving = "removing"
	LeavePhaseCleaning = "cleaning"
)

const (
	PeerRemovalDependencyLifecycleTask = "lifecycle_task"
	PeerRemovalDependencyStateRepair   = "state_repair"
)

type ClusterLeaveStatus struct {
	Enabled     bool   `json:"enabled"`
	LeaveID     string `json:"leaveId"`
	Phase       string `json:"phase"`
	LeaderIP    string `json:"leaderIp"`
	LastError   string `json:"lastError"`
	Attempts    uint   `json:"attempts"`
	LocalNodeID string `json:"localNodeId"`
}

func validLeavePhase(phase string) bool {
	switch strings.TrimSpace(phase) {
	case "", LeavePhaseFenced, LeavePhaseRemoving, LeavePhaseCleaning:
		return true
	default:
		return false
	}
}

func (s *Service) InitializeLeaveRuntime() error {
	if s == nil || s.DB == nil || s.mutationGate == nil {
		return fmt.Errorf("cluster_leave_runtime_unavailable")
	}
	s.mutationGate.Close()
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return fmt.Errorf("cluster_leave_state_load_failed: %w", err)
	}
	phase := strings.TrimSpace(record.LeavePhase)
	if !validLeavePhase(phase) {
		return fmt.Errorf("cluster_leave_phase_invalid: %s", phase)
	}
	if phase == LeavePhaseCleaning {
		if err := s.FinalizeLocalDecluster(); err != nil {
			return err
		}
		return s.mutationGate.Open()
	}
	if phase == "" {
		return s.mutationGate.Open()
	}
	return nil
}

func (s *Service) LeaveStatus() (ClusterLeaveStatus, error) {
	var record clusterModels.Cluster
	if s == nil || s.DB == nil {
		return ClusterLeaveStatus{}, fmt.Errorf("cluster_service_not_initialized")
	}
	if err := s.DB.First(&record).Error; err != nil {
		return ClusterLeaveStatus{}, err
	}
	return ClusterLeaveStatus{
		Enabled:     record.Enabled,
		LeaveID:     strings.TrimSpace(record.LeaveID),
		Phase:       strings.TrimSpace(record.LeavePhase),
		LeaderIP:    strings.TrimSpace(record.LeaveLeaderIP),
		LastError:   strings.TrimSpace(record.LeaveLastError),
		Attempts:    record.LeaveAttempts,
		LocalNodeID: strings.TrimSpace(s.LocalNodeID()),
	}, nil
}

func (s *Service) captureLeavePeerAddresses() ([]byte, error) {
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return json.Marshal([]string{})
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("leave_peer_capture_failed: %w", err)
	}
	addresses := make([]string, 0, len(future.Configuration().Servers))
	seen := make(map[string]struct{}, len(future.Configuration().Servers))
	for _, server := range future.Configuration().Servers {
		address := strings.TrimSpace(string(server.Address))
		if address == "" {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return json.Marshal(addresses)
}

func (s *Service) LocalLeavePreflight(ctx context.Context, allowGuests bool) (GuestIdentityInventoryReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), localNodeID)
	if err != nil {
		return report, err
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return report, err
	}
	dependencies := make([]PeerRemovalDependency, 0)
	if !allowGuests {
		for _, entry := range report.Entries {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyGuest,
				entry.GuestID,
				entry.Name,
				entry.GuestType,
				"registered",
			)
		}
	}
	if s.DB.Migrator().HasTable(&taskModels.GuestLifecycleTask{}) {
		var tasks []taskModels.GuestLifecycleTask
		if err := s.DB.WithContext(ctx).
			Where("status IN ?", []string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).
			Order("id ASC").
			Find(&tasks).Error; err != nil {
			return report, fmt.Errorf("scan_active_lifecycle_tasks: %w", err)
		}
		for _, task := range tasks {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyLifecycleTask,
				task.ID,
				fmt.Sprintf("%s:%d", task.GuestType, task.GuestID),
				task.Action,
				task.Status,
			)
		}
	}
	if s.DB.Migrator().HasTable(&clusterModels.ReplicationGuestOperation{}) {
		var operations []clusterModels.ReplicationGuestOperation
		if err := s.DB.WithContext(ctx).
			Where("owner_node_id = ? OR target_node_id = ?", localNodeID, localNodeID).
			Order("guest_type ASC, guest_id ASC").
			Find(&operations).Error; err != nil {
			return report, fmt.Errorf("scan_active_guest_operations: %w", err)
		}
		for _, operation := range operations {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyGuestOperation,
				operation.Token,
				fmt.Sprintf("%s:%d", operation.GuestType, operation.GuestID),
				operation.Operation,
				operation.State,
			)
		}
	}
	if s.stateRepair.Load() {
		appendPeerRemovalDependency(
			&dependencies,
			PeerRemovalDependencyStateRepair,
			localNodeID,
			"",
			"local",
			"active",
		)
	}
	if len(dependencies) != 0 {
		return report, &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       localNodeID,
			Dependencies: dependencies,
		}}
	}
	return report, nil
}

func (s *Service) persistLeaveIntent(leaveID, leaderIP, phase string, peerAddresses []byte) error {
	if !validLeavePhase(phase) || strings.TrimSpace(phase) == "" {
		return fmt.Errorf("cluster_leave_phase_invalid")
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var record clusterModels.Cluster
		if err := tx.First(&record).Error; err != nil {
			return err
		}
		activeID := strings.TrimSpace(record.LeaveID)
		if activeID != "" && activeID != strings.TrimSpace(leaveID) {
			return fmt.Errorf("cluster_leave_already_in_progress")
		}
		record.LeaveID = strings.TrimSpace(leaveID)
		record.LeaveLeaderIP = strings.TrimSpace(leaderIP)
		record.LeavePhase = strings.TrimSpace(phase)
		record.LeavePeerAddrs = append([]byte(nil), peerAddresses...)
		record.LeaveLastError = ""
		return tx.Save(&record).Error
	})
}

func (s *Service) updateLeavePhase(phase string, attemptErr error) error {
	if !validLeavePhase(phase) || strings.TrimSpace(phase) == "" {
		return fmt.Errorf("cluster_leave_phase_invalid")
	}
	updates := map[string]any{
		"leave_phase":    strings.TrimSpace(phase),
		"leave_attempts": gorm.Expr("leave_attempts + 1"),
	}
	if attemptErr == nil {
		updates["leave_last_error"] = ""
	} else {
		updates["leave_last_error"] = attemptErr.Error()
	}
	var record clusterModels.Cluster
	if err := s.DB.Select("id").First(&record).Error; err != nil {
		return err
	}
	return s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(updates).Error
}

func (s *Service) clearLeaveIntentAndReopen() error {
	var record clusterModels.Cluster
	if err := s.DB.Select("id").First(&record).Error; err != nil {
		return err
	}
	err := s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"leave_id":         "",
		"leave_phase":      "",
		"leave_leader_ip":  "",
		"leave_peer_addrs": nil,
		"leave_last_error": "",
		"leave_attempts":   0,
	}).Error
	if err != nil {
		return err
	}
	return s.mutationGate.Open()
}

func (s *Service) FinalizeLocalDecluster() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	s.mutationGate.Close()
	if s.Raft != nil {
		_ = s.Raft.Shutdown().Error()
	}
	if s.Transport != nil {
		_ = s.Transport.Close()
	}
	if s.DB.Migrator().HasTable(&taskModels.GuestLifecycleTask{}) {
		now := time.Now()
		if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).
			Where("status IN ?", []string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).
			Updates(map[string]any{
				"status":      taskModels.LifecycleTaskStatusFailed,
				"error":       "cluster_declustered",
				"message":     "cluster_declustered",
				"finished_at": &now,
			}).Error; err != nil {
			return fmt.Errorf("cluster_leave_task_cleanup_failed: %w", err)
		}
	}
	if err := s.CleanRaftDir(); err != nil {
		return err
	}
	if err := s.CleanLocalClusterSSHKeys(); err != nil {
		return err
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := clearClusteredDataTx(tx); err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM cluster_nodes").Error; err != nil {
			return fmt.Errorf("failed_to_clean_cluster_nodes: %w", err)
		}
		return markDeclusteredTx(tx)
	}); err != nil {
		return err
	}
	s.joinComplete.Store(false)
	return nil
}
