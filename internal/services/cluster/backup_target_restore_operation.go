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
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func (s *Service) AcquireBackupTargetRestoreOperation(payload clusterModels.BackupTargetRestoreOperationAcquire) error {
	payload.RequireEnabledTarget = true
	if payload.AcquiredAt.IsZero() {
		payload.AcquiredAt = time.Now().UTC()
	}
	if !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
			return fmt.Errorf("backup_restore_holder_not_current_voter: %w", err)
		}
	}
	return s.apply("backup_target_restore_operation", "acquire_v2", payload)
}

func (s *Service) TransitionBackupTargetRestoreOperation(
	action string,
	payload clusterModels.BackupTargetRestoreOperationTransition,
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
	if (action == "start" || action == "requeue") && !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
			return fmt.Errorf("backup_restore_holder_not_current_voter: %w", err)
		}
	}
	if action == "start" {
		action = "start_v2"
	}
	return s.apply("backup_target_restore_operation", action, payload)
}

type BackupTargetRestoreOperationRequest struct {
	Action     string                                               `json:"action"`
	Acquire    clusterModels.BackupTargetRestoreOperationAcquire    `json:"acquire"`
	Transition clusterModels.BackupTargetRestoreOperationTransition `json:"transition"`
}

func (s *Service) backupTargetRestoreOperationCall(
	_ context.Context,
	_ string,
	req BackupTargetRestoreOperationRequest,
) (struct{}, error) {
	if strings.EqualFold(strings.TrimSpace(req.Action), "acquire") {
		return struct{}{}, s.AcquireBackupTargetRestoreOperation(req.Acquire)
	}
	return struct{}{}, s.TransitionBackupTargetRestoreOperation(req.Action, req.Transition)
}
