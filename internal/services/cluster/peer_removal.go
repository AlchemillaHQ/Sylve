// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

const (
	PeerRemovalDependencyGuest                = "guest"
	PeerRemovalDependencyBackupJob            = "backup_job"
	PeerRemovalDependencyReplicationPolicy    = "replication_policy"
	PeerRemovalDependencyReplicationLease     = "replication_lease"
	PeerRemovalDependencyBackupOperation      = "backup_operation"
	PeerRemovalDependencyReplicationOperation = "replication_operation"
	PeerRemovalDependencyRestoreOperation     = "restore_operation"
	PeerRemovalDependencyGuestOperation       = "guest_operation"
	PeerRemovalDependencyRunnerRebind         = "runner_rebind"
)

type PeerRemovalDependency struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role,omitempty"`
	State string `json:"state,omitempty"`
}

type PeerRemovalConflict struct {
	NodeID       string                  `json:"nodeId"`
	Dependencies []PeerRemovalDependency `json:"dependencies"`
}

type PeerRemovalBlockedError struct {
	Conflict PeerRemovalConflict
}

func (e *PeerRemovalBlockedError) Error() string {
	if e == nil {
		return "peer_removal_blocked"
	}
	return fmt.Sprintf(
		"peer_removal_blocked: node_id=%s dependencies=%d",
		e.Conflict.NodeID,
		len(e.Conflict.Dependencies),
	)
}

func appendPeerRemovalDependency(
	dependencies *[]PeerRemovalDependency,
	kind string,
	id any,
	name string,
	role string,
	state string,
) {
	*dependencies = append(*dependencies, PeerRemovalDependency{
		Kind:  kind,
		ID:    fmt.Sprint(id),
		Name:  strings.TrimSpace(name),
		Role:  strings.TrimSpace(role),
		State: strings.TrimSpace(state),
	})
}

func (s *Service) peerRemovalDependencies(nodeID string) ([]PeerRemovalDependency, error) {
	return s.replicatedPeerRemovalDependencies(nodeID)
}

func (s *Service) replicatedPeerRemovalDependencies(nodeID string) ([]PeerRemovalDependency, error) {
	nodeID = strings.TrimSpace(nodeID)
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	if nodeID == "" {
		return nil, fmt.Errorf("peer_node_id_required")
	}

	dependencies := make([]PeerRemovalDependency, 0)

	if s.DB.Migrator().HasTable(&clusterModels.BackupJob{}) {
		var jobs []clusterModels.BackupJob
		if err := s.DB.Where("runner_node_id = ?", nodeID).Order("id ASC").Find(&jobs).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_backup_jobs: %w", err)
		}
		for _, job := range jobs {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyBackupJob,
				job.ID,
				job.Name,
				"runner",
				"",
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.ReplicationPolicy{}) {
		var policies []clusterModels.ReplicationPolicy
		query := s.DB.Order("id ASC")
		if s.DB.Migrator().HasTable(&clusterModels.ReplicationPolicyTarget{}) {
			query = query.Preload("Targets")
		}
		if err := query.Find(&policies).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_replication_policies: %w", err)
		}
		for _, policy := range policies {
			roles := make([]string, 0, 6)
			if strings.TrimSpace(policy.ActiveNodeID) == nodeID {
				roles = append(roles, "active_owner")
			}
			if strings.TrimSpace(policy.SourceNodeID) == nodeID {
				roles = append(roles, "source")
			}
			if strings.TrimSpace(policy.TransitionSourceNodeID) == nodeID {
				roles = append(roles, "transition_source")
			}
			if strings.TrimSpace(policy.TransitionTargetNodeID) == nodeID {
				roles = append(roles, "transition_target")
			}
			if strings.TrimSpace(policy.TransitionOriginalSourceNodeID) == nodeID {
				roles = append(roles, "transition_original_source")
			}
			for _, target := range policy.Targets {
				if strings.TrimSpace(target.NodeID) == nodeID {
					roles = append(roles, "target")
					break
				}
			}
			if len(roles) == 0 {
				continue
			}
			sort.Strings(roles)
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyReplicationPolicy,
				policy.ID,
				policy.Name,
				strings.Join(roles, ","),
				policy.TransitionState,
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.ReplicationLease{}) {
		var leases []clusterModels.ReplicationLease
		if err := s.DB.Where("owner_node_id = ?", nodeID).Order("policy_id ASC").Find(&leases).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_replication_leases: %w", err)
		}
		for _, lease := range leases {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyReplicationLease,
				lease.PolicyID,
				"",
				"owner",
				"",
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.BackupJobOperation{}) {
		var operations []clusterModels.BackupJobOperation
		if err := s.DB.Where("holder_node_id = ?", nodeID).Order("job_id ASC").Find(&operations).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_backup_operations: %w", err)
		}
		for _, operation := range operations {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyBackupOperation,
				operation.Token,
				"",
				"holder",
				operation.State,
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.ReplicationRunOperation{}) {
		var operations []clusterModels.ReplicationRunOperation
		if err := s.DB.Where("holder_node_id = ?", nodeID).Order("policy_id ASC").Find(&operations).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_replication_operations: %w", err)
		}
		for _, operation := range operations {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyReplicationOperation,
				operation.Token,
				"",
				"holder",
				operation.State,
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.BackupTargetRestoreOperation{}) {
		var operations []clusterModels.BackupTargetRestoreOperation
		if err := s.DB.
			Where("holder_node_id = ? AND state <> ?", nodeID, clusterModels.BackupTargetRestoreOperationCompleted).
			Order("token ASC").
			Find(&operations).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_restore_operations: %w", err)
		}
		for _, operation := range operations {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyRestoreOperation,
				operation.Token,
				operation.DestinationDataset,
				"holder",
				operation.State,
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.ReplicationGuestOperation{}) {
		var operations []clusterModels.ReplicationGuestOperation
		if err := s.DB.
			Where("owner_node_id = ? OR target_node_id = ?", nodeID, nodeID).
			Order("guest_type ASC, guest_id ASC").
			Find(&operations).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_guest_operations: %w", err)
		}
		for _, operation := range operations {
			roles := make([]string, 0, 2)
			if strings.TrimSpace(operation.OwnerNodeID) == nodeID {
				roles = append(roles, "owner")
			}
			if strings.TrimSpace(operation.TargetNodeID) == nodeID {
				roles = append(roles, "target")
			}
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyGuestOperation,
				operation.Token,
				fmt.Sprintf("%s:%d", operation.GuestType, operation.GuestID),
				strings.Join(roles, ","),
				operation.State,
			)
		}
	}

	if s.DB.Migrator().HasTable(&clusterModels.BackupJobRunnerRebind{}) {
		var rebinds []clusterModels.BackupJobRunnerRebind
		terminalStates := []string{
			clusterModels.BackupJobRunnerRebindStateCompleted,
			clusterModels.BackupJobRunnerRebindStateCompletedWithRepairs,
			clusterModels.BackupJobRunnerRebindStateAborted,
		}
		if err := s.DB.
			Where("(old_runner_node_id = ? OR new_runner_node_id = ?) AND state NOT IN ?", nodeID, nodeID, terminalStates).
			Order("token ASC").
			Find(&rebinds).Error; err != nil {
			return nil, fmt.Errorf("scan_peer_runner_rebinds: %w", err)
		}
		for _, rebind := range rebinds {
			role := "old_runner"
			if strings.TrimSpace(rebind.NewRunnerNodeID) == nodeID {
				role = "new_runner"
			}
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyRunnerRebind,
				rebind.Token,
				fmt.Sprintf("%s:%d", rebind.GuestType, rebind.GuestID),
				role,
				rebind.State,
			)
		}
	}

	sort.SliceStable(dependencies, func(i, j int) bool {
		left := dependencies[i]
		right := dependencies[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.ID != right.ID {
			leftID, leftErr := strconv.ParseUint(left.ID, 10, 64)
			rightID, rightErr := strconv.ParseUint(right.ID, 10, 64)
			if leftErr == nil && rightErr == nil && leftID != rightID {
				return leftID < rightID
			}
			return left.ID < right.ID
		}
		if left.Role != right.Role {
			return left.Role < right.Role
		}
		return left.State < right.State
	})
	return dependencies, nil
}
