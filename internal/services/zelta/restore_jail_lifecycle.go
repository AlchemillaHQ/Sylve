// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"errors"
	"fmt"

	"github.com/alchemillahq/sylve/internal/logger"
)

type restoreRuntimeGuard struct {
	guestType        string
	guestID          uint
	dataset          string
	restart          func() error
	restartAttempted bool
}

func (g *restoreRuntimeGuard) restoreAfterFailure(primary error) (error, error) {
	if g == nil || primary == nil || g.restart == nil || g.restartAttempted {
		return primary, nil
	}
	g.restartAttempted = true
	logger.L.Info().
		Str("guest_type", g.guestType).
		Uint("guest_id", g.guestID).
		Str("dataset", g.dataset).
		Msg("restoring_guest_running_state_after_failed_restore")
	if err := g.restart(); err != nil {
		restartErr := fmt.Errorf(
			"restore_%s_restart_failed: guest_id=%d dataset=%s: %w",
			g.guestType,
			g.guestID,
			g.dataset,
			err,
		)
		return errors.Join(primary, restartErr), restartErr
	}
	return primary, nil
}

func (s *Service) prepareInPlaceJailRestore(dataset string) (*restoreRuntimeGuard, error) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return nil, fmt.Errorf("restore_jail_dataset_required")
	}
	if s == nil || s.Jail == nil {
		return nil, fmt.Errorf("restore_jail_service_unavailable: dataset=%s", dataset)
	}

	ctID, err := s.Jail.GetJailCTIDFromDataset(dataset)
	if err != nil {
		return nil, fmt.Errorf(
			"restore_jail_identity_lookup_failed: dataset=%s: %w",
			dataset,
			err,
		)
	}
	if ctID == 0 {
		return nil, fmt.Errorf("restore_jail_identity_invalid: dataset=%s", dataset)
	}

	wasRunning, err := s.Jail.IsJailRunning(ctID)
	if err != nil {
		return nil, fmt.Errorf(
			"restore_jail_state_check_failed: dataset=%s ct_id=%d: %w",
			dataset,
			ctID,
			err,
		)
	}

	guard := &restoreRuntimeGuard{
		guestType: "jail",
		guestID:   ctID,
		dataset:   dataset,
	}
	if !wasRunning {
		return guard, nil
	}

	logger.L.Info().
		Uint("ct_id", ctID).
		Str("dataset", dataset).
		Msg("stopping_jail_before_restore_cutover")
	if err := s.Jail.JailAction(int(ctID), "stop"); err != nil {
		return nil, fmt.Errorf(
			"restore_jail_stop_failed: dataset=%s ct_id=%d: %w",
			dataset,
			ctID,
			err,
		)
	}
	guard.restart = func() error {
		return s.Jail.JailAction(int(ctID), "start")
	}
	return guard, nil
}

func (s *Service) prepareInPlaceVMRestore(vmID uint, dataset string) (*restoreRuntimeGuard, error) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if vmID == 0 {
		return nil, fmt.Errorf("restore_vm_identity_invalid")
	}
	if s == nil || s.VM == nil {
		return nil, fmt.Errorf("restore_vm_service_unavailable: guest_id=%d", vmID)
	}

	wasShutOff, err := s.VM.IsDomainShutOff(vmID)
	if err != nil {
		if isVMDomainNotFoundError(err) {
			return &restoreRuntimeGuard{guestType: "vm", guestID: vmID, dataset: dataset}, nil
		}
		return nil, fmt.Errorf("restore_vm_state_check_failed: guest_id=%d: %w", vmID, err)
	}
	guard := &restoreRuntimeGuard{guestType: "vm", guestID: vmID, dataset: dataset}
	if wasShutOff {
		return guard, nil
	}
	if err := s.stopVMIfPresent(vmID); err != nil {
		return nil, fmt.Errorf("restore_vm_stop_failed: guest_id=%d: %w", vmID, err)
	}
	guard.restart = func() error {
		return s.startVMIfPresent(vmID)
	}
	return guard, nil
}

// runInPlaceJailRestoreCutover preserves the runtime state of an existing jail
// until the ZFS cutover succeeds. Identity and runtime-state checks are part of
// the safety boundary: an in-place restore must never replace a dataset while
// Sylve cannot prove that the corresponding jail is stopped.
//
// A successful cutover deliberately leaves the jail stopped so restored
// metadata and networking can be reconciled safely. When the cutover fails,
// promoteRestoredDataset has retained or restored the original dataset, so a
// jail stopped by this attempt is restarted before the error is returned.
func (s *Service) runInPlaceJailRestoreCutover(
	dataset string,
	cutover func() (string, error),
) (string, error) {
	if cutover == nil {
		return "", fmt.Errorf("restore_jail_cutover_required: dataset=%s", dataset)
	}
	guard, err := s.prepareInPlaceJailRestore(dataset)
	if err != nil {
		return "", err
	}

	backupDataset, cutoverErr := cutover()
	if cutoverErr == nil {
		return backupDataset, nil
	}

	primaryErr := fmt.Errorf(
		"rename_restore_failed: could not promote restored dataset into %s: %w",
		dataset,
		cutoverErr,
	)
	primaryErr, _ = guard.restoreAfterFailure(primaryErr)
	return "", primaryErr
}
