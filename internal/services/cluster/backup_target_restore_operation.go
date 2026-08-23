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
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func (s *Service) AcquireBackupTargetRestoreOperation(
	payload clusterModels.BackupTargetRestoreOperationAcquire,
	bypassRaft bool,
) error {
	payload.RequireEnabledTarget = true
	if payload.AcquiredAt.IsZero() {
		payload.AcquiredAt = time.Now().UTC()
	}
	if bypassRaft {
		return clusterModels.AcquireBackupTargetRestoreOperationTxn(s.DB, &payload)
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
		return fmt.Errorf("backup_restore_holder_not_current_voter: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_restore_operation_acquire: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_target_restore_operation", Action: "acquire", Data: data,
	})
}

func (s *Service) TransitionBackupTargetRestoreOperation(
	action string,
	payload clusterModels.BackupTargetRestoreOperationTransition,
	bypassRaft bool,
) error {
	action = strings.ToLower(strings.TrimSpace(action))
	payload.RequireEnabledTarget = true
	switch action {
	case "start", "finish", "requeue", "abort", "release":
	default:
		return fmt.Errorf("invalid_backup_target_restore_operation_action")
	}
	if payload.OccurredAt.IsZero() {
		payload.OccurredAt = time.Now().UTC()
	}

	if bypassRaft {
		switch action {
		case "start":
			return clusterModels.StartBackupTargetRestoreOperationTxn(s.DB, &payload)
		case "finish":
			return clusterModels.FinishBackupTargetRestoreOperationTxn(s.DB, &payload)
		case "requeue":
			return clusterModels.RequeueBackupTargetRestoreOperationTxn(s.DB, &payload)
		case "abort":
			return clusterModels.AbortBackupTargetRestoreOperationTxn(s.DB, &payload)
		default:
			return clusterModels.ReleaseBackupTargetRestoreOperationTxn(s.DB, &payload)
		}
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	if action == "start" || action == "requeue" {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
			return fmt.Errorf("backup_restore_holder_not_current_voter: %w", err)
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_restore_operation_%s: %w", action, err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_target_restore_operation", Action: action, Data: data,
	})
}
