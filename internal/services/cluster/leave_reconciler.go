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
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
)

const leaveReconcileInterval = 3 * time.Second

func (s *Service) reconcileLocalLeave(ctx context.Context) {
	status, err := s.LeaveStatus()
	if err != nil || status.Phase == "" {
		return
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := s.advanceLocalLeave(attemptCtx, true); err != nil {
		logger.L.Debug().Err(err).Str("leave_id", status.LeaveID).Msg("cluster_leave_reconcile_deferred")
	}
}

func (s *Service) startLeaveReconciler(ctx context.Context) {
	go func() {
		for {
			s.reconcileLocalLeave(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(leaveReconcileInterval):
			}
		}
	}()
}
