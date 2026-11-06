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
	"errors"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

// RuntimeStateBypassRaft returns true only for persisted standalone mode.
// A cluster-enabled database never gains local write authority merely because
// the in-memory Raft runtime is temporarily unavailable.
func (s *Service) RuntimeStateBypassRaft() (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("cluster_service_unavailable")
	}
	// A running Raft means the node is clustered or becoming so, even while
	// its cluster row isn't enabled yet: writes go through Raft from then on.
	if s.Raft() != nil {
		return false, nil
	}
	// A database without the cluster table has never been clustered.
	if !s.DB.Migrator().HasTable(&clusterModels.Cluster{}) {
		return true, nil
	}
	var state clusterModels.Cluster
	err := s.DB.Order("id ASC").Limit(1).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("cluster_state_unavailable: %w", err)
	}
	if !state.Enabled {
		return true, nil
	}
	return false, fmt.Errorf("cluster_enabled_raft_unavailable: %w", errConsensusUnavailable)
}

func (s *Service) ApplyBackupJobScheduleDecision(decision clusterModels.BackupJobScheduleDecision) error {
	if !s.Standalone() && decision.ClaimToken != "" {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(decision.HolderNodeID); err != nil {
			return fmt.Errorf("backup_schedule_holder_not_current_voter: %w", err)
		}
	}
	return s.apply("backup_job_schedule", "decide_v2", decision)
}

func (s *Service) CompleteBackupJobRun(result clusterModels.BackupJobRunResult) error {
	return s.apply("backup_job_schedule", "complete", result)
}

func (s *Service) ApplyReplicationPolicyScheduleDecision(decision clusterModels.ReplicationPolicyScheduleDecision) error {
	if !s.Standalone() && decision.ClaimToken != "" {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(decision.HolderNodeID); err != nil {
			return fmt.Errorf("replication_schedule_holder_not_current_voter: %w", err)
		}
	}
	return s.apply("replication_policy_schedule", "decide", decision)
}

func (s *Service) StartReplicationRun(transition clusterModels.ReplicationRunOperationTransition) error {
	if !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(transition.HolderNodeID); err != nil {
			return fmt.Errorf("replication_run_holder_not_current_voter: %w", err)
		}
	}
	return s.apply("replication_policy_schedule", "start", transition)
}

func (s *Service) CompleteReplicationPolicyRun(result clusterModels.ReplicationPolicyRunResult) error {
	return s.apply("replication_policy_schedule", "complete", result)
}

func ValidReplicationRunClaim(decision clusterModels.ReplicationPolicyScheduleDecision) bool {
	return decision.PolicyID != 0 && decision.ExpectedOwnerEpoch != 0 && !decision.DecidedAt.IsZero() &&
		strings.TrimSpace(decision.ClaimToken) != "" && strings.TrimSpace(decision.HolderNodeID) != "" &&
		decision.OccurrenceAt != nil && !decision.SetRuntime && decision.LastRunAt == nil &&
		strings.TrimSpace(decision.LastStatus) == "" && strings.TrimSpace(decision.LastError) == ""
}

func (s *Service) replicationRunClaimCall(
	_ context.Context,
	_ string,
	decision clusterModels.ReplicationPolicyScheduleDecision,
) (struct{}, error) {
	if !ValidReplicationRunClaim(decision) {
		return struct{}{}, fmt.Errorf("replication_run_claim_invalid")
	}
	return struct{}{}, s.ApplyReplicationPolicyScheduleDecision(decision)
}
