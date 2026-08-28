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
	"strings"

	"github.com/hashicorp/raft"
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
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return RaftMembership{}, fmt.Errorf("raft_unavailable")
	}
	future := s.Raft.GetConfiguration()
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
	_, leaderID := s.Raft.LeaderWithID()
	return RaftMembership{
		NodeID:   strings.TrimSpace(string(server.ID)),
		Address:  strings.TrimSpace(string(server.Address)),
		Suffrage: raftSuffrageName(server.Suffrage),
		IsLeader: server.ID == leaderID,
	}, nil
}

func (s *Service) AuthoritativeMembershipStatus(nodeID string) (MembershipStatus, error) {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	return s.authoritativeMembershipStatusLocked(nodeID)
}

func (s *Service) authoritativeMembershipStatusLocked(nodeID string) (MembershipStatus, error) {
	status := MembershipStatus{NodeID: strings.TrimSpace(nodeID)}
	if status.NodeID == "" {
		return status, fmt.Errorf("node_id_required")
	}
	if s == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		if s != nil && s.Raft != nil {
			leaderAddress, leaderID := s.Raft.LeaderWithID()
			status.LeaderID = strings.TrimSpace(string(leaderID))
			status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
		}
		return status, fmt.Errorf("not_leader")
	}
	leaderAddress, leaderID := s.Raft.LeaderWithID()
	if strings.TrimSpace(string(leaderID)) == "" || strings.TrimSpace(string(leaderAddress)) == "" {
		return status, fmt.Errorf("leader_identity_unavailable")
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return status, fmt.Errorf("membership_barrier_failed: %w", err)
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return status, fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	currentLeaderAddress, currentLeaderID := s.Raft.LeaderWithID()
	if s.Raft.State() != raft.Leader || currentLeaderID != leaderID || currentLeaderAddress != leaderAddress {
		return status, fmt.Errorf("leadership_changed")
	}
	server, present, err := resolveRaftMember(future.Configuration(), status.NodeID)
	if err != nil {
		return status, err
	}
	status.Present = present
	status.LeaderID = strings.TrimSpace(string(leaderID))
	status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
	if present {
		status.Address = strings.TrimSpace(string(server.Address))
		status.Suffrage = raftSuffrageName(server.Suffrage)
	}
	return status, nil
}
