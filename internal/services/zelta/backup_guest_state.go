// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

type backupGuestRestore func() error

const guestStateRecoveryTimeout = 15 * time.Second

func guestStateRecoveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), guestStateRecoveryTimeout)
}

// quiesceBackupGuest returns an inverse operation only when this backup
// actually stopped a running guest. A guest that was already stopped is left
// stopped, and every later caller error can safely invoke the returned inverse.
func (s *Service) quiesceBackupGuest(
	job *clusterModels.BackupJob,
	vmRID uint,
) (backupGuestRestore, bool, error) {
	return s.quiesceBackupGuestContext(context.Background(), job, vmRID)
}

func (s *Service) quiesceBackupGuestContext(
	ctx context.Context,
	job *clusterModels.BackupJob,
	vmRID uint,
) (backupGuestRestore, bool, error) {
	if job == nil || !job.StopBeforeBackup {
		return nil, false, nil
	}

	switch job.Mode {
	case clusterModels.BackupJobModeJail:
		if s.Jail == nil {
			return nil, false, fmt.Errorf("jail_service_unavailable")
		}
		ctID, err := s.Jail.GetJailCTIDFromDataset(job.JailRootDataset)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_get_jail_ctid: %w", err)
		}
		wasRunning, err := s.Jail.IsJailRunning(ctID)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_check_jail_state_before_backup: %w", err)
		}
		if !wasRunning {
			return nil, false, nil
		}
		if err := s.jailActionContext(ctx, int(ctID), "stop"); err != nil {
			return nil, false, fmt.Errorf("failed_to_stop_jail: %w", err)
		}
		return func() error {
			restoreCtx, cancel := guestStateRecoveryContext(ctx)
			defer cancel()
			return s.jailActionContext(restoreCtx, int(ctID), "start")
		}, true, nil

	case clusterModels.BackupJobModeVM:
		if vmRID == 0 {
			return nil, false, fmt.Errorf("invalid_vm_rid_for_stop")
		}
		if s.VM == nil {
			return nil, false, fmt.Errorf("vm_service_unavailable")
		}
		wasShutOff, err := s.VM.IsDomainShutOff(vmRID)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_check_vm_state_before_backup: %w", err)
		}
		if wasShutOff {
			return nil, false, nil
		}
		if err := s.stopVMIfPresentContext(ctx, vmRID); err != nil {
			return nil, false, fmt.Errorf("failed_to_stop_vm: %w", err)
		}
		return func() error {
			restoreCtx, cancel := guestStateRecoveryContext(ctx)
			defer cancel()
			return s.startVMIfPresentContext(restoreCtx, vmRID)
		}, true, nil
	}

	return nil, false, nil
}
