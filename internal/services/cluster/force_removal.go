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
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

type ForceRemovePeerRequest struct {
	NodeID                 string `json:"nodeId"`
	TargetExternallyFenced bool   `json:"targetExternallyFenced"`
}

type ForceLocalResetRequest struct {
	NodeID                       string `json:"nodeId"`
	RemoteMembershipAcknowledged bool   `json:"remoteMembershipAcknowledged"`
	WorkloadsExternallyFenced    bool   `json:"workloadsExternallyFenced"`
}

type ClusterConsensusError struct {
	Cause error
}

func (e *ClusterConsensusError) Error() string {
	if e == nil || e.Cause == nil {
		return "cluster_consensus_unavailable"
	}
	return fmt.Sprintf("cluster_consensus_unavailable: %v", e.Cause)
}

func (e *ClusterConsensusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (s *Service) ForceRemovePeer(ctx context.Context, request ForceRemovePeerRequest) (ClusterLeaveResult, error) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	defer release()

	nodeID := strings.TrimSpace(request.NodeID)
	if nodeID == "" {
		return ClusterLeaveResult{}, fmt.Errorf("peer_node_id_required")
	}
	if !request.TargetExternallyFenced {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_force_target_fence_ack_required")
	}
	if nodeID == strings.TrimSpace(s.LocalNodeID()) {
		return ClusterLeaveResult{}, fmt.Errorf("peer_removal_target_is_local")
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if s.Raft == nil {
		return ClusterLeaveResult{}, &ClusterConsensusError{Cause: fmt.Errorf("raft_unavailable")}
	}
	if s.Raft.State() != raft.Leader {
		leaderAddress, leaderID := s.Raft.LeaderWithID()
		if strings.TrimSpace(string(leaderAddress)) == "" && strings.TrimSpace(string(leaderID)) == "" {
			return ClusterLeaveResult{}, &ClusterConsensusError{Cause: fmt.Errorf("leader_unavailable")}
		}
		return ClusterLeaveResult{}, fmt.Errorf("not_leader")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return ClusterLeaveResult{}, fmt.Errorf("failed_to_get_raft_configuration: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if !present {
		return ClusterLeaveResult{}, fmt.Errorf("peer_not_found")
	}
	if err := s.checkUniformVersionsLocked(ctx, nil, nodeID); err != nil {
		return ClusterLeaveResult{}, err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return ClusterLeaveResult{}, &ClusterConsensusError{Cause: err}
	}
	if s.Raft.State() != raft.Leader {
		return ClusterLeaveResult{}, fmt.Errorf("not_leader")
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(nodeID)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if len(dependencies) != 0 {
		return ClusterLeaveResult{}, &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       nodeID,
			Dependencies: dependencies,
		}}
	}
	if err := s.Raft.RemoveServer(server.ID, 0, raftApplyTimeout).Error(); err != nil {
		return ClusterLeaveResult{}, &ClusterConsensusError{Cause: err}
	}
	if err := s.DeleteClusterSSHIdentity(nodeID, false); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_ssh_identity_removal_deferred")
	}
	if err := s.ClearClusterNode(nodeID); err != nil {
		logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_node_health_cleanup_deferred")
	}
	return ClusterLeaveResult{MembershipRemoved: true}, nil
}

func (s *Service) ForceLocalReset(ctx context.Context, request ForceLocalResetRequest) (ClusterLeaveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	if strings.TrimSpace(request.NodeID) != localNodeID || localNodeID == "" {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_force_local_node_id_mismatch")
	}
	if !request.RemoteMembershipAcknowledged {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_force_membership_ack_required")
	}
	if !request.WorkloadsExternallyFenced {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_force_workload_fence_ack_required")
	}
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	status, err := s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	wasFenced := status.Phase != ""
	drainCtx, cancel := context.WithTimeout(ctx, leaveDrainTimeout)
	err = s.DrainMutations(drainCtx)
	cancel()
	if err != nil {
		if !wasFenced {
			_ = s.ReopenMutations()
		}
		return ClusterLeaveResult{}, &ClusterLeaveError{
			Code:  "cluster_leave_active_mutations",
			Cause: err,
		}
	}

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	status, err = s.LeaveStatus()
	if err != nil {
		if !wasFenced {
			_ = s.ReopenMutations()
		}
		return ClusterLeaveResult{}, err
	}
	if status.Phase == "" {
		if err := s.persistLeaveIntent(uuid.NewString(), "", LeavePhaseCleaning, []byte("[]")); err != nil {
			_ = s.ReopenMutations()
			return ClusterLeaveResult{}, err
		}
	} else if status.Phase != LeavePhaseCleaning {
		if err := s.updateLeavePhase(LeavePhaseCleaning, nil); err != nil {
			return ClusterLeaveResult{}, err
		}
	}
	if err := s.FinalizeLocalDecluster(); err != nil {
		_ = s.updateLeavePhase(LeavePhaseCleaning, err)
		status, _ = s.LeaveStatus()
		return ClusterLeaveResult{Status: status}, err
	}
	status, err = s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	s.notifyLeaveComplete()
	return ClusterLeaveResult{Status: status, CleanupAcknowledged: true}, nil
}
