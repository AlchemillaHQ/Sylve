// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/hashicorp/raft"
)

func (s *Service) registerCalls(c *cluster.Service) {
	cluster.OnLeader(c, "replication-reassign-owner", s.reassignOwnerCall)
	cluster.OnLeader(c, "replication-failover-enqueue", s.failoverEnqueueCall)
	cluster.OnNode(c, "replication-run", func(ctx context.Context, _ string, req transitionRequest) (struct{}, error) {
		return struct{}{}, s.EnqueueReplicationPolicyRun(ctx, req.PolicyID)
	})
	cluster.OnNode(c, "replication-activate", s.activateCall)
	cluster.OnNode(c, "replication-demote", s.demoteCall)
	cluster.OnNode(c, "replication-catchup", s.catchupCall)
	cluster.OnNode(c, "replication-runtime-state", s.runtimeStateCall)
	cluster.OnNode(c, "replication-delete-cleanup", s.deleteCleanupCall)
	cluster.OnNode(c, "migration-interlock-applied", func(_ context.Context, _ string, req migrationInterlockRequest) (struct{}, error) {
		return struct{}{}, s.migrationInterlockApplied(req)
	})
}

type transitionRequest struct {
	PolicyID        uint   `json:"policyId"`
	OwnerEpoch      uint64 `json:"ownerEpoch,omitempty"`
	TransitionRunID string `json:"transitionRunId,omitempty"`
	DesiredRunning  *bool  `json:"desiredRunning,omitempty"`
	TargetNodeID    string `json:"targetNodeId,omitempty"`
	GenerationID    string `json:"generationId,omitempty"`
}

func (r transitionRequest) validate() error {
	if r.PolicyID == 0 || r.OwnerEpoch == 0 || strings.TrimSpace(r.TransitionRunID) == "" {
		return fmt.Errorf("invalid_request: policyId, ownerEpoch and transitionRunId are required")
	}
	return nil
}

func (s *Service) activateCall(ctx context.Context, _ string, req transitionRequest) (struct{}, error) {
	if err := req.validate(); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, s.ActivateReplicationPolicyForTransition(ctx, req.PolicyID, req.OwnerEpoch, req.TransitionRunID, req.DesiredRunning)
}

func (s *Service) demoteCall(ctx context.Context, _ string, req transitionRequest) (struct{}, error) {
	if err := req.validate(); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, s.DemoteReplicationPolicyForTransition(ctx, req.PolicyID, req.OwnerEpoch, req.TransitionRunID)
}

func (s *Service) catchupCall(ctx context.Context, _ string, req transitionRequest) (struct{}, error) {
	if err := req.validate(); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, s.CatchupReplicationPolicyToNodeForTransition(
		ctx, req.PolicyID, req.TargetNodeID, req.OwnerEpoch, req.TransitionRunID, req.GenerationID,
	)
}

func (s *Service) runtimeStateCall(ctx context.Context, _ string, req transitionRequest) (bool, error) {
	if err := req.validate(); err != nil {
		return false, err
	}
	return s.ReplicationPolicyRuntimeState(ctx, req.PolicyID, req.OwnerEpoch, req.TransitionRunID)
}

func (s *Service) deleteCleanupCall(ctx context.Context, _ string, req transitionRequest) (struct{}, error) {
	if req.PolicyID == 0 || req.OwnerEpoch == 0 {
		return struct{}{}, fmt.Errorf("invalid_request: policyId and ownerEpoch are required")
	}
	return struct{}{}, s.CleanupReplicationPolicyDeleteLocalBestEffort(ctx, req.PolicyID, req.OwnerEpoch)
}

type migrationInterlockRequest struct {
	GuestType    string `json:"guestType"`
	GuestID      uint   `json:"guestId"`
	State        string `json:"state"`
	Token        string `json:"token"`
	TargetNodeID string `json:"targetNodeId"`
}

func (s *Service) migrationInterlockApplied(req migrationInterlockRequest) error {
	var operation clusterModels.ReplicationGuestOperation
	if err := s.DB.Where("guest_type = ? AND guest_id = ?", req.GuestType, req.GuestID).
		First(&operation).Error; err != nil {
		return err
	}
	if operation.Operation != clusterModels.ReplicationGuestOperationMigration || operation.State != req.State ||
		strings.TrimSpace(operation.Token) != req.Token || strings.TrimSpace(operation.TargetNodeID) != req.TargetNodeID {
		return fmt.Errorf("migration_interlock_not_applied")
	}
	return nil
}

func (s *Service) callNode(ctx context.Context, nodeID, name string, req any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := cluster.CallNode[any, struct{}](ctx, s.Cluster, nodeID, name, req)
	return err
}

// Only for calls that are safe to repeat.
func (s *Service) callNodeWithRetry(ctx context.Context, nodeID, name string, req any, timeout time.Duration) error {
	var err error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err = s.callNode(ctx, nodeID, name, req, timeout); err == nil || ctx.Err() != nil {
			return err
		}
		if attempt < replicationControlForwardAttempts-1 {
			time.Sleep(replicationControlForwardBackoff * time.Duration(attempt+1))
		}
	}
	return err
}

func (s *Service) callLeader(ctx context.Context, name string, req any) error {
	ctx, cancel := context.WithTimeout(ctx, replicationControlDefaultTimeout)
	defer cancel()
	_, err := cluster.CallLeader[any, struct{}](ctx, s.Cluster, name, req)
	return err
}

// Only for calls that are safe to repeat.
func (s *Service) callLeaderWithRetry(ctx context.Context, name string, req any) error {
	var err error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err = s.callLeader(ctx, name, req); err == nil || !leaderCallUnanswered(err) {
			return err
		}
		if attempt < replicationControlForwardAttempts-1 {
			time.Sleep(replicationControlForwardBackoff * time.Duration(attempt+1))
		}
	}
	return err
}

func leaderCallUnanswered(err error) bool {
	return errors.Is(err, raft.ErrNotLeader) || errors.Is(err, raft.ErrRaftShutdown) ||
		errors.Is(err, raft.ErrLeadershipLost) || strings.Contains(err.Error(), "call_forward_failed")
}
