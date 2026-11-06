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
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

var (
	ErrPeerNotFound = errors.New("peer_not_found")
	ErrRemoveSelf   = errors.New("peer_removal_target_is_local")
)

type RaftMembership struct {
	NodeID   string `json:"nodeId"`
	Address  string `json:"address"`
	Suffrage string `json:"suffrage"`
	IsLeader bool   `json:"isLeader"`
}

type MembershipStatus struct {
	NodeID        string `json:"nodeId"`
	Present       bool   `json:"present"`
	Address       string `json:"address,omitempty"`
	Suffrage      string `json:"suffrage,omitempty"`
	LeaderID      string `json:"leaderId"`
	LeaderAddress string `json:"leaderAddress"`
}

func resolveRaftMember(configuration raft.Configuration, nodeID string) (raft.Server, bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return raft.Server{}, false, fmt.Errorf("node_id_required")
	}
	var match raft.Server
	matches := 0
	for _, server := range configuration.Servers {
		if strings.TrimSpace(string(server.ID)) == nodeID {
			match = server
			matches++
		}
	}
	if matches > 1 {
		return raft.Server{}, false, fmt.Errorf("duplicate_raft_member: node_id=%s", nodeID)
	}
	return match, matches == 1, nil
}

func (s *Service) ResolveCurrentRaftMember(nodeID string) (RaftMembership, error) {
	if s != nil && s.raftMembershipForNode != nil {
		return s.raftMembershipForNode(strings.TrimSpace(nodeID))
	}
	if s == nil || s.Raft() == nil || s.Raft().State() == raft.Shutdown {
		return RaftMembership{}, fmt.Errorf("raft_unavailable")
	}
	future := s.Raft().GetConfiguration()
	if err := future.Error(); err != nil {
		return RaftMembership{}, fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		return RaftMembership{}, err
	}
	if !present {
		return RaftMembership{}, fmt.Errorf("raft_member_not_found: node_id=%s", strings.TrimSpace(nodeID))
	}
	_, leaderID := s.Raft().LeaderWithID()
	return RaftMembership{
		NodeID:   strings.TrimSpace(string(server.ID)),
		Address:  strings.TrimSpace(string(server.Address)),
		Suffrage: raftSuffrageName(server.Suffrage),
		IsLeader: server.ID == leaderID,
	}, nil
}

// A member's status goes only to a caller with its cert, whose pin is
// callerPin; any caller may learn that a node isn't one.
func (s *Service) AuthoritativeMembershipStatus(nodeID string, callerPin [sha256.Size]byte) (MembershipStatus, error) {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	status := MembershipStatus{NodeID: strings.TrimSpace(nodeID)}
	if status.NodeID == "" {
		return status, fmt.Errorf("node_id_required")
	}
	if s.Raft() == nil || s.Raft().State() != raft.Leader {
		if s.Raft() != nil {
			leaderAddress, leaderID := s.Raft().LeaderWithID()
			status.LeaderID = strings.TrimSpace(string(leaderID))
			status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
		}
		return status, fmt.Errorf("not_leader")
	}
	leaderAddress, leaderID := s.Raft().LeaderWithID()
	if strings.TrimSpace(string(leaderID)) == "" || strings.TrimSpace(string(leaderAddress)) == "" {
		return status, fmt.Errorf("leader_identity_unavailable")
	}
	future := s.Raft().GetConfiguration()
	if err := future.Error(); err != nil {
		return status, fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), status.NodeID)
	if err != nil {
		return status, err
	}
	status.LeaderID = strings.TrimSpace(string(leaderID))
	status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
	if present {
		if holder, ok := s.PinNode(callerPin); !ok || holder != status.NodeID {
			return status, fmt.Errorf("membership_status_cert_mismatch")
		}
		status.Present = true
		status.Address = strings.TrimSpace(string(server.Address))
		status.Suffrage = raftSuffrageName(server.Suffrage)
		return status, nil
	}
	// Told it's gone, a node resets itself, so only a leader that still leads
	// says so.
	if err := s.Raft().VerifyLeader().Error(); err != nil {
		return status, fmt.Errorf("leadership_unconfirmed: %w", err)
	}
	return status, nil
}

func (s *Service) RemoveMember(ctx context.Context, nodeID string, keepGuests bool) error {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return fmt.Errorf("peer_node_id_required")
	}
	if nodeID == s.LocalNodeID() {
		return ErrRemoveSelf
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	r := s.Raft()
	if r == nil || r.State() != raft.Leader {
		return fmt.Errorf("not_leader: %w", raft.ErrNotLeader)
	}
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		return err
	}
	if !present {
		return ErrPeerNotFound
	}
	// Read the dependencies from a state that has every committed entry.
	if err := r.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("peer_removal_leader_barrier_failed: %w", err)
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(ctx, nodeID)
	if err != nil {
		return err
	}
	if blocked := blockingPeerRemovalDependencies(dependencies, keepGuests); len(blocked) != 0 {
		return &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{NodeID: nodeID, Dependencies: blocked}}
	}
	if err := r.RemoveServer(server.ID, 0, raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("failed_to_remove_peer: %w", err)
	}
	// The removal stands; the departed-member sweep retries what fails here.
	if err := s.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: nodeID}); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_member_cleanup_deferred")
	}
	if err := s.ClearClusterNode(nodeID); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_node_health_cleanup_deferred")
	}
	s.EmitLeftPanelRefreshClusterWide("cluster_membership_changed")
	return nil
}

// It runs under the lock join admission claims under.
func (s *Service) forgetDepartedMembers(ctx context.Context) error {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	r := s.Raft()
	if r == nil || r.State() != raft.Leader {
		return nil
	}
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	var owners, identities []string
	if err := s.DB.WithContext(ctx).Model(&clusterModels.GuestIdentityClaim{}).Distinct("owner_node_id").Pluck("owner_node_id", &owners).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Model(&clusterModels.ClusterSSHIdentity{}).Pluck("node_uuid", &identities).Error; err != nil {
		return err
	}
	nodeIDs := append(owners, identities...)
	slices.Sort(nodeIDs)
	var errs []error
	for _, nodeID := range slices.Compact(nodeIDs) {
		if _, present, err := resolveRaftMember(future.Configuration(), nodeID); err != nil || present {
			continue
		}
		if err := s.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: nodeID}); err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}
