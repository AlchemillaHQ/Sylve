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

func (s *Service) AcquireBackupJobOperation(payload clusterModels.BackupJobOperationAcquire, bypassRaft bool) error {
	if payload.AcquiredAt.IsZero() {
		payload.AcquiredAt = time.Now().UTC()
	}
	if bypassRaft {
		return clusterModels.AcquireBackupJobOperationTxn(s.DB, &payload)
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_operation_acquire: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_operation", Action: "acquire", Data: data,
	})
}

func (s *Service) TransitionBackupJobOperation(
	action string,
	payload clusterModels.BackupJobOperationTransition,
	bypassRaft bool,
) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "start" && action != "finish" && action != "abort" && action != "release" {
		return fmt.Errorf("invalid_backup_job_operation_action")
	}
	if payload.OccurredAt.IsZero() {
		payload.OccurredAt = time.Now().UTC()
	}

	if bypassRaft {
		switch action {
		case "start":
			return clusterModels.StartBackupJobOperationTxn(s.DB, &payload)
		case "finish":
			return clusterModels.FinishBackupJobOperationTxn(s.DB, &payload)
		case "abort":
			return clusterModels.AbortBackupJobOperationTxn(s.DB, &payload)
		default:
			return clusterModels.ReleaseBackupJobOperationTxn(s.DB, &payload)
		}
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_operation_%s: %w", action, err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_operation", Action: action, Data: data,
	})
}
