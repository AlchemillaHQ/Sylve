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
	"strings"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

type CommandStatus struct {
	Enabled        bool   `json:"enabled"`
	NodeID         string `json:"nodeId"`
	RaftIP         string `json:"raftIp"`
	RaftState      string `json:"raftState"`
	LeaderID       string `json:"leaderId,omitempty"`
	LeaderAddress  string `json:"leaderAddress,omitempty"`
	Voters         int    `json:"voters"`
	Nonvoters      int    `json:"nonvoters"`
	JoinPhase      string `json:"joinPhase,omitempty"`
	LeavePhase     string `json:"leavePhase,omitempty"`
	ReaddressPhase string `json:"readdressPhase,omitempty"`
	ReaddressError string `json:"readdressError,omitempty"`
	Partial        bool   `json:"partial"`
}

type CommandMember struct {
	Hostname     string `json:"hostname,omitempty"`
	NodeID       string `json:"nodeId"`
	Address      string `json:"address"`
	Status       string `json:"status"`
	Suffrage     string `json:"suffrage"`
	SylveVersion string `json:"sylveVersion,omitempty"`
	SylveCommit  string `json:"sylveCommit,omitempty"`
	IsLeader     bool   `json:"isLeader"`
	GuestCount   int    `json:"guestCount"`
}

func (s *Service) CommandStatus() (CommandStatus, error) {
	if s == nil || s.DB == nil {
		return CommandStatus{}, fmt.Errorf("cluster_service_unavailable")
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return CommandStatus{}, err
	}
	joinPhase := strings.TrimSpace(record.JoinPhase)
	if !record.HasIncompleteJoin() {
		joinPhase = ""
	}
	result := CommandStatus{
		Enabled:        record.Enabled,
		NodeID:         strings.TrimSpace(s.LocalNodeID()),
		RaftIP:         strings.TrimSpace(record.RaftIP),
		RaftState:      "standalone",
		JoinPhase:      joinPhase,
		LeavePhase:     strings.TrimSpace(record.LeavePhase),
		ReaddressPhase: strings.TrimSpace(record.ReaddressPhase),
		ReaddressError: strings.TrimSpace(record.ReaddressLastError),
	}
	if !record.Enabled {
		return result, nil
	}
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		result.RaftState = "unavailable"
		result.Partial = true
		return result, nil
	}
	result.RaftState = strings.ToLower(s.Raft.State().String())
	leaderAddress, leaderID := s.Raft.LeaderWithID()
	result.LeaderID = strings.TrimSpace(string(leaderID))
	result.LeaderAddress = strings.TrimSpace(string(leaderAddress))
	if result.LeaderID == "" || result.LeaderAddress == "" {
		result.Partial = true
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		result.Partial = true
		return result, nil
	}
	for _, server := range future.Configuration().Servers {
		if server.Suffrage == raft.Voter {
			result.Voters++
		} else {
			result.Nonvoters++
		}
	}
	return result, nil
}

func (s *Service) CommandMembers() ([]CommandMember, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return nil, err
	}
	if !record.Enabled {
		return []CommandMember{}, nil
	}
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return nil, fmt.Errorf("raft_unavailable")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	var cached []clusterModels.ClusterNode
	if err := s.DB.Find(&cached).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]clusterModels.ClusterNode, len(cached))
	for _, node := range cached {
		byID[strings.TrimSpace(node.NodeUUID)] = node
	}
	_, leaderID := s.Raft.LeaderWithID()
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	members := make([]CommandMember, 0, len(future.Configuration().Servers))
	for _, server := range future.Configuration().Servers {
		nodeID := strings.TrimSpace(string(server.ID))
		node := byID[nodeID]
		member := CommandMember{
			Hostname:     strings.TrimSpace(node.Hostname),
			NodeID:       nodeID,
			Address:      strings.TrimSpace(string(server.Address)),
			Status:       strings.TrimSpace(node.Status),
			Suffrage:     raftSuffrageName(server.Suffrage),
			SylveVersion: strings.TrimSpace(node.SylveVersion),
			SylveCommit:  strings.TrimSpace(node.SylveCommit),
			IsLeader:     server.ID == leaderID,
			GuestCount:   len(node.GuestIDs),
		}
		if member.Status == "" {
			member.Status = "unknown"
		}
		if nodeID == localNodeID {
			if member.Status == "unknown" {
				member.Status = nodeStatusOnline
			}
			if member.SylveVersion == "" {
				member.SylveVersion = cmd.Version
			}
			if member.SylveCommit == "" {
				member.SylveCommit = cmd.Commit
			}
			if member.Hostname == "" {
				if detail := s.Detail(); detail != nil {
					member.Hostname = detail.Hostname
				}
			}
		}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].IsLeader != members[j].IsLeader {
			return members[i].IsLeader
		}
		return members[i].NodeID < members[j].NodeID
	})
	return members, nil
}
