// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/network/reconcile"
)

var ErrSwitchSyncInProgress = errors.New("switch_sync_in_progress")

func (s *Service) SwitchSyncStatus() reconcile.Status {
	s.switchStatusMutex.RLock()
	defer s.switchStatusMutex.RUnlock()
	return s.switchStatus.Clone()
}

func (s *Service) setSwitchSyncStatus(status reconcile.Status) {
	s.switchStatusMutex.Lock()
	defer s.switchStatusMutex.Unlock()
	s.switchStatus = status
}

// StartSwitchNetworkReconciliation preserves the initial startup ordering, but
// does not make startup wait for backoff. Crash rollback recovery deliberately
// stays outside this loop: repeating it could revert a new pending user edit.
func (s *Service) StartSwitchNetworkReconciliation(ctx context.Context) {
	if err := s.reconcileSwitchNetwork(ctx, true); err != nil {
		logger.L.Error().Err(err).Msg("switch_network_reconciliation_start_failed")
	}
}

// SyncSwitchNetwork explicitly re-applies stored desired state, including when
// an unchanged edit would be a no-op. Concurrent triggers are rejected rather
// than starting competing retry loops.
func (s *Service) SyncSwitchNetwork(ctx context.Context) error {
	return s.reconcileSwitchNetwork(ctx, false)
}

func (s *Service) reconcileSwitchNetwork(ctx context.Context, background bool) error {
	if !s.switchReconcileMutex.TryLock() {
		return ErrSwitchSyncInProgress
	}
	ctx, cancel := context.WithTimeout(ctx, reconcile.RetryWindow)
	var status reconcile.Status
	previous := s.SwitchSyncStatus()
	delays := s.switchRetryDelays
	if delays == nil {
		delays = reconcile.Backoff()
	}
	attempt := func(ctx context.Context, number int) error {
		now := time.Now().UTC()
		status.State = "running"
		status.Attempts = number
		status.LastAttemptAt = &now
		s.setSwitchSyncStatus(status)

		failures, syncErr := s.syncStandardSwitches(ctx)
		var hostErr error
		if ctx.Err() == nil {
			hostErr = s.reconcileHostInterfaceL3(ctx)
			if hostErr != nil {
				failures = append(failures, reconcile.Failure{Phase: "host_interface_l3", Error: hostErr.Error()})
			}
		} else {
			hostErr = ctx.Err()
		}
		var err error
		if syncErr != nil {
			err = fmt.Errorf("sync standard switches: %w", syncErr)
		}
		if hostErr != nil {
			err = errors.Join(err, fmt.Errorf("reconcile host interface l3: %w", hostErr))
		}
		status.Failures = failures
		status.Error = ""
		if err != nil {
			status.State = "retrying"
			status.Error = err.Error()
			if number <= len(delays) {
				logger.L.Warn().Err(err).Int("attempt", number).Msg("switch_network_reconciliation_retry")
			}
		}
		s.setSwitchSyncStatus(status)
		return err
	}
	finish := func(err error) {
		defer cancel()
		defer s.switchReconcileMutex.Unlock()
		now := time.Now().UTC()
		status.CompletedAt = &now
		status.State = "converged"
		if err != nil {
			status.State = "degraded"
			status.Error = err.Error()
			logger.L.Error().Err(err).Int("attempts", status.Attempts).Msg("switch_network_reconciliation_exhausted")
		} else if previous.State == "degraded" || status.Attempts > 1 {
			logger.L.Info().Int("attempts", status.Attempts).Msg("switch_network_reconciliation_converged")
		}
		s.setSwitchSyncStatus(status)
	}

	// Count the initial synchronous pass as attempt one, not as an additional
	// attempt outside the retry budget.
	var firstErr error
	if background {
		firstErr = attempt(ctx, 1)
		if firstErr == nil {
			finish(nil)
			return nil
		}
	}
	run := func() error {
		err := reconcile.Retry(ctx, delays, func(ctx context.Context, number int) error {
			if background && number == 1 {
				return firstErr
			}
			return attempt(ctx, number)
		})
		finish(err)
		return err
	}
	if background {
		go run()
		return nil
	}
	return run()
}
