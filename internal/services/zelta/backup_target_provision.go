// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

func (s *Service) claimBackupTargetProvision(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	s.backupTargetProvisionMu.Lock()
	defer s.backupTargetProvisionMu.Unlock()
	if s.activeTargetProvisionTokens == nil {
		s.activeTargetProvisionTokens = make(map[string]struct{})
	}
	if _, exists := s.activeTargetProvisionTokens[token]; exists {
		return false
	}
	s.activeTargetProvisionTokens[token] = struct{}{}
	return true
}

func (s *Service) releaseBackupTargetProvision(token string) {
	s.backupTargetProvisionMu.Lock()
	defer s.backupTargetProvisionMu.Unlock()
	delete(s.activeTargetProvisionTokens, strings.TrimSpace(token))
}

func (s *Service) executeBackupTargetProvisionOperation(
	ctx context.Context,
	operation *clusterModels.BackupTargetProvisionOperation,
) error {
	if s == nil || s.Cluster == nil || operation == nil {
		return fmt.Errorf("backup_target_provision_service_unavailable")
	}
	if !s.claimBackupTargetProvision(operation.Token) {
		return nil
	}
	defer s.releaseBackupTargetProvision(operation.Token)

	current, err := s.Cluster.GetBackupTargetProvisionOperation(operation.Token)
	if err != nil {
		return err
	}
	if current.State != clusterModels.BackupTargetProvisionStatePending {
		return nil
	}
	operation = current
	target, err := clusterModels.DecodeBackupTargetProvisionTarget(operation)
	if err != nil {
		return err
	}
	if err := s.ProvisionBackupTargetRoot(ctx, &target); err != nil {
		return err
	}
	return s.Cluster.CompleteBackupTargetProvision(operation, s.Cluster.Raft == nil)
}

// ReconcileBackupTargetProvisionOperations retries only leader-owned pending
// intents. Every remote action is idempotent and occurs outside Raft apply.
func (s *Service) ReconcileBackupTargetProvisionOperations(ctx context.Context) error {
	if s == nil || s.Cluster == nil {
		return nil
	}
	if s.Cluster.Raft != nil {
		if s.Cluster.Raft.State() != raft.Leader {
			return nil
		}
		if err := s.Cluster.Raft.Barrier(5 * time.Second).Error(); err != nil {
			return fmt.Errorf("backup_target_provision_barrier_failed: %w", err)
		}
	}
	operations, err := s.Cluster.ListPendingBackupTargetProvisionOperations()
	if err != nil {
		return err
	}
	var result error
	for i := range operations {
		if err := s.executeBackupTargetProvisionOperation(ctx, &operations[i]); err != nil {
			result = errors.Join(result, fmt.Errorf("reconcile_backup_target_provision_%s: %w", operations[i].Token, err))
		}
	}
	return result
}

func (s *Service) StartBackupTargetProvisionReconciler(ctx context.Context) {
	if err := s.ReconcileBackupTargetProvisionOperations(ctx); err != nil {
		logger.L.Warn().Err(err).Msg("backup_target_provision_reconcile_failed")
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ReconcileBackupTargetProvisionOperations(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("backup_target_provision_reconcile_failed")
			}
		}
	}
}
