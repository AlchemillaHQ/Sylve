// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	restoreJailQuiesceTimeout       = 10 * time.Second
	restoreJailQuiesceRetryInterval = 250 * time.Millisecond
)

type restoreRuntimeGuard struct {
	guestID   uint
	dataset   string
	remount   func() error
	remounted bool
}

// restoreAfterFailure mounts the jail's original dataset back once, so the
// restore fence can restart the jail.
func (g *restoreRuntimeGuard) restoreAfterFailure(primary error) (error, error) {
	if g == nil || primary == nil || g.remounted {
		return primary, nil
	}
	g.remounted = true
	if err := g.remount(); err != nil {
		remountErr := fmt.Errorf(
			"restore_jail_remount_failed: guest_id=%d dataset=%s: %w",
			g.guestID,
			g.dataset,
			err,
		)
		return errors.Join(primary, remountErr), remountErr
	}
	return primary, nil
}

// The jail restore fence has already stopped the jail; its dataset still has to
// be unmounted before the promotion renames it.
func (s *Service) prepareInPlaceJailRestore(
	ctx context.Context,
	ctID uint,
	dataset string,
) (*restoreRuntimeGuard, error) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	quiesceCtx, cancel := context.WithTimeout(ctx, restoreJailQuiesceTimeout)
	defer cancel()
	if err := s.waitForJailRestoreDatasetUnmount(quiesceCtx, ctID, dataset); err != nil {
		return nil, err
	}
	return &restoreRuntimeGuard{
		guestID: ctID,
		dataset: dataset,
		remount: func() error {
			recoveryCtx, cancel := restoreRecoveryContext()
			defer cancel()
			return s.mountLocalDataset(recoveryCtx, dataset)
		},
	}, nil
}

func (s *Service) waitForJailRestoreStopped(ctx context.Context, ctID uint) error {
	for {
		running, err := s.Jail.IsJailRunning(ctID)
		if err != nil {
			return fmt.Errorf("restore_jail_state_check_failed: ct_id=%d: %w", ctID, err)
		}
		if !running {
			return nil
		}
		if !waitForRestoreJailRetry(ctx) {
			return fmt.Errorf("restore_jail_stop_timeout: ct_id=%d: %w", ctID, ctx.Err())
		}
	}
}

func (s *Service) waitForJailRestoreDatasetUnmount(ctx context.Context, ctID uint, dataset string) error {
	for {
		err := s.unmountLocalDatasetNormally(ctx, dataset)
		if err == nil || isLocalDatasetNotMountedError(err) {
			return nil
		}
		if !isLocalDatasetBusyError(err) {
			return fmt.Errorf(
				"restore_jail_dataset_unmount_failed: ct_id=%d dataset=%s: %w",
				ctID,
				dataset,
				err,
			)
		}
		if !waitForRestoreJailRetry(ctx) {
			return fmt.Errorf(
				"restore_jail_dataset_busy: ct_id=%d dataset=%s: %w",
				ctID,
				dataset,
				err,
			)
		}
	}
}

func waitForRestoreJailRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(restoreJailQuiesceRetryInterval):
		return true
	}
}
