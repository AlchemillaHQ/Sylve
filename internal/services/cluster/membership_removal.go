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
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

type RemoveMembershipRequest struct {
	LeaveID   string                       `json:"leaveId"`
	NodeID    string                       `json:"nodeId"`
	Inventory GuestIdentityInventoryReport `json:"inventory"`
}

func (s *Service) RemoveMembership(
	ctx context.Context,
	request RemoveMembershipRequest,
	issuerNodeID string,
) error {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()

	nodeID := strings.TrimSpace(request.NodeID)
	if nodeID == "" {
		return fmt.Errorf("peer_node_id_required")
	}
	if strings.TrimSpace(request.LeaveID) == "" {
		return fmt.Errorf("cluster_leave_id_required")
	}
	if strings.TrimSpace(issuerNodeID) != nodeID {
		return fmt.Errorf("cluster_leave_issuer_mismatch")
	}
	canonical, err := canonicalSubmittedGuestIdentityInventory(nodeID, request.Inventory)
	if err != nil {
		return err
	}
	if len(canonical.Entries) != 0 {
		dependencies := make([]PeerRemovalDependency, 0, len(canonical.Entries))
		for _, entry := range canonical.Entries {
			appendPeerRemovalDependency(
				&dependencies,
				PeerRemovalDependencyGuest,
				entry.GuestID,
				entry.Name,
				entry.GuestType,
				"registered",
			)
		}
		return &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       nodeID,
			Dependencies: dependencies,
		}}
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed_to_get_raft_configuration: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if server.ID == raft.ServerID(strings.TrimSpace(s.LocalNodeID())) {
		return fmt.Errorf("leader_cannot_remove_self")
	}
	if err := s.checkUniformVersionsLocked(ctx, nil, ""); err != nil {
		return err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("peer_removal_leader_barrier_failed: %w", err)
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("leadership_changed")
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(nodeID)
	if err != nil {
		return err
	}
	if len(dependencies) != 0 {
		return &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       nodeID,
			Dependencies: dependencies,
		}}
	}
	if err := s.Raft.RemoveServer(server.ID, 0, raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("failed_to_remove_peer: %w", err)
	}
	if err := s.DeleteClusterSSHIdentity(nodeID, false); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_ssh_identity_removal_deferred")
	}
	if err := s.ClearClusterNode(nodeID); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_node_health_cleanup_deferred")
	}
	s.EmitLeftPanelRefreshClusterWide("cluster_membership_changed")
	return nil
}
