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

func (s *Service) AcquireBackupJobOperation(payload clusterModels.BackupJobOperationAcquire) error {
	payload.RequireEnabledTarget = true
	if payload.AcquiredAt.IsZero() {
		payload.AcquiredAt = time.Now().UTC()
	}
	if !s.Standalone() {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
			return fmt.Errorf("backup_job_operation_holder_not_current_voter: %w", err)
		}
	}
	return s.apply("backup_job_operation", "acquire_v2", payload)
}

func (s *Service) TransitionBackupJobOperation(action string, payload clusterModels.BackupJobOperationTransition) error {
	action = strings.ToLower(strings.TrimSpace(action))
	payload.RequireEnabledTarget = true
	if action != "start" && action != "finish" && action != "abort" && action != "release" {
		return fmt.Errorf("invalid_backup_job_operation_action")
	}
	if payload.OccurredAt.IsZero() {
		payload.OccurredAt = time.Now().UTC()
	}
	if action == "start" {
		if !s.Standalone() {
			s.clusterJoinMu.Lock()
			defer s.clusterJoinMu.Unlock()
			if err := s.RequireCurrentRaftVoter(payload.HolderNodeID); err != nil {
				return fmt.Errorf("backup_job_operation_holder_not_current_voter: %w", err)
			}
		}
		action = "start_v2"
	}
	return s.apply("backup_job_operation", action, payload)
}

type BackupJobOperationRequest struct {
	Action     string                                     `json:"action"`
	Acquire    clusterModels.BackupJobOperationAcquire    `json:"acquire"`
	Transition clusterModels.BackupJobOperationTransition `json:"transition"`
}

func (s *Service) backupJobOperationCall(_ context.Context, _ string, req BackupJobOperationRequest) (struct{}, error) {
	if strings.EqualFold(strings.TrimSpace(req.Action), "acquire") {
		return struct{}{}, s.AcquireBackupJobOperation(req.Acquire)
	}
	return struct{}{}, s.TransitionBackupJobOperation(req.Action, req.Transition)
}
