// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

// RuntimeStateBypassRaft returns true only for persisted standalone mode.
// A cluster-enabled database never gains local write authority merely because
// the in-memory Raft runtime is temporarily unavailable.
func (s *Service) RuntimeStateBypassRaft() (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("cluster_service_unavailable")
	}
	// Older standalone test/utility databases may not contain cluster metadata.
	// Production databases always migrate this table; absence therefore has the
	// same legacy meaning as no configured cluster.
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
	if s.Raft == nil {
		return false, fmt.Errorf("cluster_enabled_raft_unavailable")
	}
	return false, nil
}

func (s *Service) requireRuntimeWriteAuthority(bypassRaft bool) error {
	actualBypass, err := s.RuntimeStateBypassRaft()
	if err != nil {
		return err
	}
	if bypassRaft && !actualBypass {
		return fmt.Errorf("cluster_enabled_local_runtime_write_forbidden")
	}
	if !bypassRaft {
		if s.Raft == nil {
			return fmt.Errorf("raft_not_initialized")
		}
		if s.Raft.State() != raft.Leader {
			return fmt.Errorf("not_leader")
		}
	}
	return nil
}

func (s *Service) applyRuntimeScheduleCommand(
	commandType, action string,
	payload any,
	bypassRaft bool,
	localApply func() error,
) error {
	if err := s.requireRuntimeWriteAuthority(bypassRaft); err != nil {
		return err
	}
	if bypassRaft {
		return localApply()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_runtime_schedule_command: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: commandType, Action: action, Data: data,
	})
}

func (s *Service) ApplyBackupJobScheduleDecision(
	decision clusterModels.BackupJobScheduleDecision,
	bypassRaft bool,
) error {
	return s.applyRuntimeScheduleCommand(
		"backup_job_schedule", "decide", decision, bypassRaft,
		func() error { return clusterModels.ApplyBackupJobScheduleDecisionTxn(s.DB, &decision) },
	)
}

func (s *Service) CompleteBackupJobRun(
	result clusterModels.BackupJobRunResult,
	bypassRaft bool,
) error {
	return s.applyRuntimeScheduleCommand(
		"backup_job_schedule", "complete", result, bypassRaft,
		func() error { return clusterModels.CompleteBackupJobRunTxn(s.DB, &result) },
	)
}

func (s *Service) ApplyReplicationPolicyScheduleDecision(
	decision clusterModels.ReplicationPolicyScheduleDecision,
	bypassRaft bool,
) error {
	return s.applyRuntimeScheduleCommand(
		"replication_policy_schedule", "decide", decision, bypassRaft,
		func() error { return clusterModels.ApplyReplicationPolicyScheduleDecisionTxn(s.DB, &decision) },
	)
}

func (s *Service) StartReplicationRun(
	transition clusterModels.ReplicationRunOperationTransition,
	bypassRaft bool,
) error {
	return s.applyRuntimeScheduleCommand(
		"replication_policy_schedule", "start", transition, bypassRaft,
		func() error { return clusterModels.StartReplicationRunOperationTxn(s.DB, &transition) },
	)
}

func (s *Service) CompleteReplicationPolicyRun(
	result clusterModels.ReplicationPolicyRunResult,
	bypassRaft bool,
) error {
	return s.applyRuntimeScheduleCommand(
		"replication_policy_schedule", "complete", result, bypassRaft,
		func() error { return clusterModels.CompleteReplicationPolicyRunTxn(s.DB, &result) },
	)
}
