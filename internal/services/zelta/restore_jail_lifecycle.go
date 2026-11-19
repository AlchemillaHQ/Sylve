// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/pkg/storage/mount"
)

const (
	// A stopped jail can stay dying until its sockets leave TIME_WAIT, 60s by
	// default, and keeps its dataset busy until then.
	restoreJailQuiesceTimeout       = 2 * time.Minute
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

// waitForJailRestoreDatasetUnmount retries while the dataset is busy. A dying
// jail is only reported, never waited on by itself: a stuck one may never go,
// and it doesn't matter once the dataset unmounts, e.g. after a manual
// forced unmount.
func (s *Service) waitForJailRestoreDatasetUnmount(ctx context.Context, ctID uint, dataset string) error {
	unmount, mountpoint, err := s.resolveLocalDatasetNormalUnmount(ctx, dataset)
	var busyErr error
	for err == nil {
		err = unmount(ctx)
		if err == nil || isLocalDatasetNotMountedError(err) {
			return nil
		}
		if !isLocalDatasetBusyError(err) {
			break
		}
		busyErr, err = err, nil
		if !waitForRestoreJailRetry(ctx) {
			break
		}
	}

	if ctx.Err() == nil {
		return fmt.Errorf(
			"restore_jail_dataset_unmount_failed: ct_id=%d dataset=%s: %w",
			ctID,
			dataset,
			err,
		)
	}
	// Past the deadline, err is the kill of a zfs command that was still
	// running, which says nothing about the dataset.
	if busyErr == nil {
		return fmt.Errorf(
			"restore_jail_dataset_unmount_timeout: ct_id=%d dataset=%s: %w",
			ctID,
			dataset,
			ctx.Err(),
		)
	}
	return fmt.Errorf(
		"restore_jail_dataset_busy: ct_id=%d dataset=%s dying_jids=%s mounts_below=%s: %w",
		ctID,
		dataset,
		s.describeDyingJails(ctID),
		describeMountsBelow(mountpoint),
		busyErr,
	)
}

// describeDyingJails lists removed copies of the jail the kernel still holds;
// each keeps the dataset busy until it's released.
func (s *Service) describeDyingJails(ctID uint) string {
	jids, err := s.Jail.DyingJailIDs(ctID)
	if err != nil {
		return fmt.Sprintf("unknown(%v)", err)
	}
	if len(jids) == 0 {
		return "none"
	}
	described := make([]string, 0, len(jids))
	for _, jid := range jids {
		described = append(described, strconv.Itoa(jid))
	}
	return "[" + strings.Join(described, ",") + "]"
}

// describeMountsBelow lists what is still mounted on top of a busy dataset.
// With nothing listed here or as dying, a process most likely has files open
// there.
func describeMountsBelow(mountpoint string) string {
	if mountpoint == "" {
		return "unknown"
	}
	mounts, err := mount.Descendants(context.Background(), mountpoint)
	if err != nil {
		return fmt.Sprintf("unknown(%v)", err)
	}
	if len(mounts) == 0 {
		return "none"
	}
	described := make([]string, 0, len(mounts))
	for _, m := range mounts {
		described = append(described, fmt.Sprintf("%s(%s)", m.Target, m.FSType))
	}
	return "[" + strings.Join(described, ",") + "]"
}

func waitForRestoreJailRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(restoreJailQuiesceRetryInterval):
		return true
	}
}
