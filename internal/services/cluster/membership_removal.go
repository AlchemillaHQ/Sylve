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

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

type RemoveMembershipRequest struct {
	LeaveID      string                       `json:"leaveId"`
	NodeID       string                       `json:"nodeId"`
	Inventory    GuestIdentityInventoryReport `json:"inventory"`
	RetainGuests bool                         `json:"retainGuests,omitempty"`
}

type LeaveInventoryMismatch struct {
	NodeID    string                       `json:"nodeId"`
	Submitted GuestIdentityInventoryReport `json:"submitted"`
	Claimed   GuestIdentityInventoryReport `json:"claimed"`
}

type LeaveInventoryMismatchError struct{ Mismatch LeaveInventoryMismatch }

func (e *LeaveInventoryMismatchError) Error() string { return "cluster_leave_inventory_claim_mismatch" }

func (s *Service) compareLeaveInventoryWithClaims(nodeID string, submitted GuestIdentityInventoryReport) error {
	s.replicatedStateMu.RLock()
	var claims []clusterModels.GuestIdentityClaim
	err := s.DB.Where("owner_node_id = ?", nodeID).Order("guest_id ASC").Find(&claims).Error
	s.replicatedStateMu.RUnlock()
	if err != nil {
		return fmt.Errorf("cluster_leave_claim_list_failed: %w", err)
	}
	entries := make([]GuestIdentityInventoryEntry, len(claims))
	for i, claim := range claims {
		entries[i] = guestIdentityClaimInventoryEntry(claim)
	}
	claimed := BuildGuestIdentityInventoryReport(entries)
	if claimed.Digest != submitted.Digest {
		return &LeaveInventoryMismatchError{Mismatch: LeaveInventoryMismatch{NodeID: nodeID, Submitted: submitted, Claimed: claimed}}
	}
	return nil
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
	if !request.RetainGuests && len(canonical.Entries) != 0 {
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
		if request.RetainGuests {
			return s.releaseDepartedGuestIdentityClaimsLocked(nodeID)
		}
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
	if request.RetainGuests {
		if err := s.compareLeaveInventoryWithClaims(nodeID, canonical); err != nil {
			return err
		}
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(nodeID)
	if err != nil {
		return err
	}
	if blocked := blockingPeerRemovalDependencies(dependencies, request.RetainGuests); len(blocked) != 0 {
		return &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       nodeID,
			Dependencies: blocked,
		}}
	}
	if request.RetainGuests && len(canonical.Entries) != 0 {
		if err := s.applyGuestIdentityRaftAction("record_departure", clusterModels.GuestIdentityDepartureCommand{
			NodeID: nodeID, LeaveID: strings.TrimSpace(request.LeaveID), InventoryDigest: canonical.Digest,
		}); err != nil {
			return fmt.Errorf("cluster_leave_departure_record_failed: %w", err)
		}
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
	if request.RetainGuests && len(canonical.Entries) != 0 {
		if err := s.releaseDepartedGuestIdentityClaimsLocked(nodeID); err != nil {
			return fmt.Errorf("cluster_leave_guest_id_release_pending: %w", err)
		}
	}
	return nil
}
