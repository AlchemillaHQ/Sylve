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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	HostInterfaceL3ConfirmationWindow = 60 * time.Second
	hostInterfaceL3SweepInterval      = time.Second
)

var (
	hostInterfaceL3Now   = time.Now
	hostInterfaceL3NewID = newHostInterfaceL3PendingID
)

func newHostInterfaceL3PendingID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func hostInterfaceL3PendingPhases() []string {
	return []string{
		networkModels.PendingApplyPhasePrepared,
		networkModels.PendingApplyPhaseApplied,
		networkModels.PendingApplyPhaseConfirmed,
	}
}

func (s *Service) loadHostInterfaceL3ByName(name string) (*networkModels.HostInterfaceL3, error) {
	var row networkModels.HostInterfaceL3
	err := s.DB.
		Preload("Addresses", func(db *gorm.DB) *gorm.DB {
			return db.Order("ordering asc, id asc")
		}).
		Where("interface = ?", name).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load host interface l3 row %s: %w", name, err)
	}
	return &row, nil
}

func (s *Service) checkStandardSwitchPortsForHostInterfaceL3(ports []string, previous []string) error {
	previousSet := make(map[string]struct{}, len(previous))
	for _, port := range previous {
		previousSet[port] = struct{}{}
	}

	for _, port := range ports {
		if _, existed := previousSet[port]; existed {
			continue
		}
		var count int64
		if err := s.DB.Model(&networkModels.HostInterfaceL3{}).
			Where("interface = ?", port).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check Host IP configuration for port %s: %w", port, err)
		}
		if count > 0 {
			return standardSwitchConflict(
				"standard_switch_port_has_host_ip",
				fmt.Errorf("port %s has Host IP configuration; remove it on the Interfaces page first", port),
			)
		}
	}

	return nil
}

func (s *Service) activeHostInterfaceL3Pending(name string) (*networkModels.PendingApply, error) {
	var target networkModels.PendingApplyTarget
	err := s.DB.
		Where("target_kind = ? AND target_id = ?", "interface", name).
		Order("id desc").
		First(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load host interface l3 pending target %s: %w", name, err)
	}

	var pending networkModels.PendingApply
	err = s.DB.
		Where("id = ? AND phase IN ?", target.PendingID, hostInterfaceL3PendingPhases()).
		First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load host interface l3 pending operation %s: %w", target.PendingID, err)
	}
	return &pending, nil
}

func (s *Service) loadHostInterfaceL3Pending(
	id string,
) (*networkModels.PendingApply, *networkModels.PendingApplyTarget, error) {
	var pending networkModels.PendingApply
	err := s.DB.Where("id = ?", id).First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load pending operation %s: %w", id, err)
	}

	var target networkModels.PendingApplyTarget
	err = s.DB.Where("pending_id = ?", id).First(&target).Error
	if err != nil {
		return nil, nil, fmt.Errorf("load pending target for %s: %w", id, err)
	}
	return &pending, &target, nil
}

func (s *Service) deleteHostInterfaceL3Pending(id string) {
	if err := s.DB.Where("pending_id = ?", id).Delete(&networkModels.PendingApplyTarget{}).Error; err != nil {
		logger.L.Warn().Err(err).Str("pendingId", id).Msg("host_interface_l3_pending_target_delete_failed")
	}
	if err := s.DB.Where("id = ?", id).Delete(&networkModels.PendingApply{}).Error; err != nil {
		logger.L.Warn().Err(err).Str("pendingId", id).Msg("host_interface_l3_pending_delete_failed")
	}
}

func hostInterfaceL3SpecFromRow(row *networkModels.HostInterfaceL3) networkModels.HostInterfaceL3Spec {
	spec := networkModels.HostInterfaceL3Spec{}
	if row == nil {
		return spec
	}

	mode := row.IPv6Mode
	spec.IPv6Mode = &mode
	spec.MTU = row.MTU
	spec.Metric = row.Metric
	for _, address := range row.Addresses {
		spec.Addresses = append(spec.Addresses, networkModels.HostInterfaceL3AddressSpec{
			Family:       address.Family,
			Address:      address.Address,
			PrefixLength: address.PrefixLength,
		})
	}
	return spec
}

func (s *Service) SaveHostInterfaceL3(
	name string,
	request networkServiceInterfaces.HostInterfaceL3UpdateRequest,
) (networkServiceInterfaces.HostInterfaceL3PendingEntry, error) {
	var entry networkServiceInterfaces.HostInterfaceL3PendingEntry

	name = strings.TrimSpace(name)
	if name == "" {
		return entry, invalidHostInterfaceL3("host_interface_l3_invalid_interface", nil)
	}

	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	current, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return entry, err
	}

	previousRevision := uint64(0)
	if current != nil {
		previousRevision = current.Revision
	}
	if request.ExpectedRevision != previousRevision {
		return entry, hostInterfaceL3Conflict(
			"host_interface_l3_revision_mismatch",
			fmt.Errorf("expected revision %d, current is %d", request.ExpectedRevision, previousRevision),
		)
	}

	if pending, err := s.activeHostInterfaceL3Pending(name); err != nil {
		return entry, err
	} else if pending != nil {
		return entry, hostInterfaceL3PendingConflict(fmt.Errorf("operation %s is awaiting confirmation", pending.ID))
	}

	change, err := s.planHostInterfaceL3Change(name, current, request)
	if err != nil {
		return entry, err
	}

	id, err := hostInterfaceL3NewID()
	if err != nil {
		return entry, fmt.Errorf("generate pending id: %w", err)
	}

	pending := networkModels.PendingApply{
		ID:                    id,
		Kind:                  networkModels.PendingApplyKindInterface,
		Phase:                 networkModels.PendingApplyPhasePrepared,
		ActivePayload:         hostInterfaceL3SpecFromRow(current),
		CandidatePayload:      change.Spec,
		CandidateAppliedState: change.Intended,
		RuntimeSnapshot:       change.Baseline,
		Reservations:          []string{"interface:" + name},
		IdentityMAC:           change.IdentityMAC,
		Origin:                "api",
		CreatedAt:             hostInterfaceL3Now(),
	}
	target := networkModels.PendingApplyTarget{
		PendingID:         id,
		TargetKind:        "interface",
		TargetID:          name,
		PreviousRevision:  previousRevision,
		CandidateRevision: previousRevision + 1,
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pending).Error; err != nil {
			return err
		}
		return tx.Create(&target).Error
	})
	if err != nil {
		return entry, fmt.Errorf("persist pending host interface l3 operation: %w", err)
	}

	targetState := networkModels.HostInterfaceL3AppliedState{}
	if current != nil {
		targetState = current.AppliedState
	}

	applied, applyErr := applyHostInterfaceL3Plan(change.Plan)
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Plan(change.Plan, applied)
	}
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Removals(name, networkModels.HostInterfaceL3AppliedState{
			Addresses: change.Plan.Remove,
		}, networkModels.HostInterfaceL3Baseline{}, hostInterfaceL3ReAddedHostKeys(change.Plan.Addresses))
	}
	if applyErr == nil {
		applyErr = waitForHostInterfaceL3DAD(change.Plan, applied)
	}
	if applyErr != nil {
		revertErr := revertHostInterfaceL3Runtime(name, change.Baseline, targetState, applied)
		if revertErr != nil {
			// Keep the prepared record: startup recovery performs the rollback.
			return entry, errors.Join(fmt.Errorf("apply host interface l3: %w", applyErr), revertErr)
		}
		s.deleteHostInterfaceL3Pending(id)
		return entry, fmt.Errorf("apply host interface l3: %w", applyErr)
	}

	deadline := hostInterfaceL3Now().Add(HostInterfaceL3ConfirmationWindow)
	if err := s.DB.Model(&networkModels.PendingApply{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"phase":    networkModels.PendingApplyPhaseApplied,
			"deadline": deadline,
		}).Error; err != nil {
		revertErr := revertHostInterfaceL3Runtime(name, change.Baseline, targetState, applied)
		if revertErr != nil {
			// Keep the prepared record: startup recovery performs the rollback.
			return entry, errors.Join(fmt.Errorf("persist applied host interface l3 operation: %w", err), revertErr)
		}
		s.deleteHostInterfaceL3Pending(id)
		return entry, fmt.Errorf("persist applied host interface l3 operation: %w", err)
	}

	return networkServiceInterfaces.HostInterfaceL3PendingEntry{
		ID:        id,
		Interface: name,
		Kind:      networkModels.PendingApplyKindInterface,
		Phase:     networkModels.PendingApplyPhaseApplied,
		Deadline:  deadline,
		Origin:    "api",
	}, nil
}

func (s *Service) DeleteHostInterfaceL3(
	name string,
	expectedRevision uint64,
) (networkServiceInterfaces.HostInterfaceL3PendingEntry, error) {
	var entry networkServiceInterfaces.HostInterfaceL3PendingEntry

	name = strings.TrimSpace(name)
	if name == "" {
		return entry, invalidHostInterfaceL3("host_interface_l3_invalid_interface", nil)
	}

	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	current, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return entry, err
	}
	if current == nil {
		return entry, hostInterfaceL3NotFound(fmt.Errorf("no Host IP configuration for %s", name))
	}
	if expectedRevision != current.Revision {
		return entry, hostInterfaceL3Conflict(
			"host_interface_l3_revision_mismatch",
			fmt.Errorf("expected revision %d, current is %d", expectedRevision, current.Revision),
		)
	}

	if pending, err := s.activeHostInterfaceL3Pending(name); err != nil {
		return entry, err
	} else if pending != nil {
		return entry, hostInterfaceL3PendingConflict(fmt.Errorf("operation %s is awaiting confirmation", pending.ID))
	}

	if live, err := syncIfaceGet(name); err != nil {
		if !isInterfaceMissingError(err) {
			return entry, fmt.Errorf("inspect interface %s: %w", name, err)
		}
	} else if live != nil && strings.TrimSpace(current.IdentityMAC) != "" &&
		strings.TrimSpace(live.Ether) != "" &&
		!strings.EqualFold(current.IdentityMAC, live.Ether) {
		return entry, hostInterfaceL3Conflict(
			"host_interface_l3_identity_mismatch",
			fmt.Errorf("interface %s has MAC %s, expected %s", name, live.Ether, current.IdentityMAC),
		)
	}

	id, err := hostInterfaceL3NewID()
	if err != nil {
		return entry, fmt.Errorf("generate pending id: %w", err)
	}

	pending := networkModels.PendingApply{
		ID:              id,
		Kind:            networkModels.PendingApplyKindDelete,
		Phase:           networkModels.PendingApplyPhasePrepared,
		ActivePayload:   hostInterfaceL3SpecFromRow(current),
		RuntimeSnapshot: current.AdoptionBaseline,
		Reservations:    []string{"interface:" + name},
		Origin:          "api",
		CreatedAt:       hostInterfaceL3Now(),
	}
	target := networkModels.PendingApplyTarget{
		PendingID:         id,
		TargetKind:        "interface",
		TargetID:          name,
		PreviousRevision:  current.Revision,
		CandidateRevision: current.Revision + 1,
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pending).Error; err != nil {
			return err
		}
		return tx.Create(&target).Error
	})
	if err != nil {
		return entry, fmt.Errorf("persist pending host interface l3 delete: %w", err)
	}

	revertErr := revertHostInterfaceL3Runtime(
		name,
		current.AdoptionBaseline,
		networkModels.HostInterfaceL3AppliedState{},
		current.AppliedState,
	)
	if revertErr != nil {
		_ = revertHostInterfaceL3Runtime(name, current.AdoptionBaseline, current.AppliedState, networkModels.HostInterfaceL3AppliedState{})
		s.deleteHostInterfaceL3Pending(id)
		return entry, fmt.Errorf("remove host interface l3: %w", revertErr)
	}
	if verifyErr := verifyHostInterfaceL3Removals(name, current.AppliedState, current.AdoptionBaseline, nil); verifyErr != nil {
		restoreErr := revertHostInterfaceL3Runtime(
			name,
			current.AdoptionBaseline,
			current.AppliedState,
			networkModels.HostInterfaceL3AppliedState{},
		)
		if restoreErr != nil {
			return entry, errors.Join(fmt.Errorf("remove host interface l3: %w", verifyErr), restoreErr)
		}
		s.deleteHostInterfaceL3Pending(id)
		return entry, fmt.Errorf("remove host interface l3: %w", verifyErr)
	}

	deadline := hostInterfaceL3Now().Add(HostInterfaceL3ConfirmationWindow)
	if err := s.DB.Model(&networkModels.PendingApply{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"phase":    networkModels.PendingApplyPhaseApplied,
			"deadline": deadline,
		}).Error; err != nil {
		revertErr := revertHostInterfaceL3Runtime(
			name,
			current.AdoptionBaseline,
			current.AppliedState,
			networkModels.HostInterfaceL3AppliedState{},
		)
		if revertErr != nil {
			// Keep the prepared record: startup recovery restores the runtime.
			return entry, errors.Join(fmt.Errorf("persist applied host interface l3 delete: %w", err), revertErr)
		}
		s.deleteHostInterfaceL3Pending(id)
		return entry, fmt.Errorf("persist applied host interface l3 delete: %w", err)
	}

	return networkServiceInterfaces.HostInterfaceL3PendingEntry{
		ID:        id,
		Interface: name,
		Kind:      networkModels.PendingApplyKindDelete,
		Phase:     networkModels.PendingApplyPhaseApplied,
		Deadline:  deadline,
		Origin:    "api",
	}, nil
}

func (s *Service) ConfirmHostInterfaceL3(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pending, target, err := s.loadHostInterfaceL3Pending(id)
	if err != nil {
		return err
	}

	switch pending.Phase {
	case networkModels.PendingApplyPhaseConfirmed:
	case networkModels.PendingApplyPhaseApplied:
		if hostInterfaceL3Now().After(pending.Deadline) {
			revertErr := s.revertHostInterfaceL3PendingLocked(pending, target)
			s.deleteHostInterfaceL3Pending(id)
			return errors.Join(
				hostInterfaceL3Conflict("host_interface_l3_confirmation_expired", nil),
				revertErr,
			)
		}
	case networkModels.PendingApplyPhasePrepared:
		return hostInterfaceL3PendingConflict(fmt.Errorf("operation %s has not finished applying", id))
	default:
		return hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}

	if err := s.DB.Model(&networkModels.PendingApply{}).
		Where("id = ?", id).
		Update("phase", networkModels.PendingApplyPhaseConfirmed).Error; err != nil {
		return fmt.Errorf("mark pending operation %s confirmed: %w", id, err)
	}

	if err := s.promoteHostInterfaceL3Pending(pending, target); err != nil {
		return err
	}

	s.deleteHostInterfaceL3Pending(id)
	return nil
}

func (s *Service) RevertHostInterfaceL3(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pending, target, err := s.loadHostInterfaceL3Pending(id)
	if err != nil {
		return err
	}
	if pending.Phase == networkModels.PendingApplyPhaseConfirmed {
		return hostInterfaceL3PendingConflict(fmt.Errorf("operation %s is already confirmed", id))
	}

	revertErr := s.revertHostInterfaceL3PendingLocked(pending, target)
	if revertErr != nil {
		// Keep the pending record; the sweeper and startup recovery retry the
		// rollback instead of leaving the runtime changed with no journal.
		return revertErr
	}
	s.deleteHostInterfaceL3Pending(id)
	return nil
}

func (s *Service) revertHostInterfaceL3PendingLocked(
	pending *networkModels.PendingApply,
	target *networkModels.PendingApplyTarget,
) error {
	name := target.TargetID

	row, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return err
	}

	targetState := networkModels.HostInterfaceL3AppliedState{}
	if row != nil {
		targetState = row.AppliedState
	}
	candidateState := pending.CandidateAppliedState
	if pending.Kind == networkModels.PendingApplyKindDelete {
		candidateState = networkModels.HostInterfaceL3AppliedState{}
	}

	return revertHostInterfaceL3Runtime(name, pending.RuntimeSnapshot, targetState, candidateState)
}

func (s *Service) promoteHostInterfaceL3Pending(
	pending *networkModels.PendingApply,
	target *networkModels.PendingApplyTarget,
) error {
	name := target.TargetID

	var vlanParent string
	var vlanTag uint16
	identityMAC := strings.TrimSpace(pending.IdentityMAC)
	if live, err := syncIfaceGet(name); err == nil && live != nil {
		vlanParent = live.VLANParent
		vlanTag = uint16(live.VLANTag)
		if identityMAC == "" {
			identityMAC = live.Ether
		}
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var row networkModels.HostInterfaceL3
		err := tx.Where("interface = ?", name).First(&row).Error
		found := true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			found = false
			row = networkModels.HostInterfaceL3{Interface: name}
		} else if err != nil {
			return err
		}

		if pending.Kind == networkModels.PendingApplyKindDelete {
			if found {
				if err := tx.Where("interface_l3_id = ?", row.ID).
					Delete(&networkModels.HostInterfaceL3Address{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&row).Error; err != nil {
					return err
				}
			}
			return nil
		}

		if !found || row.AdoptionBaseline == (networkModels.HostInterfaceL3Baseline{}) {
			row.AdoptionBaseline = pending.RuntimeSnapshot
		}
		row.Lifecycle = networkModels.HostInterfaceL3LifecycleExternal
		row.VLANParent = vlanParent
		row.VLANTag = vlanTag
		if row.IdentityMAC == "" {
			row.IdentityMAC = identityMAC
		}
		if row.MTUBaseline == nil {
			// Keep the adoption-time baseline; later edits must not overwrite it.
			row.MTUBaseline = pending.RuntimeSnapshot.MTU
		}
		row.MTU = pending.CandidatePayload.MTU
		row.Metric = pending.CandidatePayload.Metric
		row.IPv6Mode = networkModels.HostInterfaceL3IPv6ModeInherit
		if pending.CandidatePayload.IPv6Mode != nil {
			row.IPv6Mode = *pending.CandidatePayload.IPv6Mode
		}
		row.AppliedState = pending.CandidateAppliedState
		row.Revision = target.CandidateRevision

		if err := tx.Save(&row).Error; err != nil {
			return err
		}

		if err := tx.Where("interface_l3_id = ?", row.ID).
			Delete(&networkModels.HostInterfaceL3Address{}).Error; err != nil {
			return err
		}
		for ordering, address := range pending.CandidatePayload.Addresses {
			if err := tx.Create(&networkModels.HostInterfaceL3Address{
				InterfaceL3ID: row.ID,
				Family:        address.Family,
				Address:       address.Address,
				PrefixLength:  address.PrefixLength,
				Ordering:      ordering,
			}).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Service) GetHostInterfaceL3Pending() ([]networkServiceInterfaces.HostInterfaceL3PendingEntry, error) {
	entries := make([]networkServiceInterfaces.HostInterfaceL3PendingEntry, 0)

	var pending []networkModels.PendingApply
	if err := s.DB.Where("phase IN ?", hostInterfaceL3PendingPhases()).
		Order("created_at asc").
		Find(&pending).Error; err != nil {
		return nil, fmt.Errorf("list pending host interface l3 operations: %w", err)
	}
	if len(pending) == 0 {
		return entries, nil
	}

	for _, operation := range pending {
		var target networkModels.PendingApplyTarget
		if err := s.DB.Where("pending_id = ?", operation.ID).First(&target).Error; err != nil {
			continue
		}
		entries = append(entries, networkServiceInterfaces.HostInterfaceL3PendingEntry{
			ID:        operation.ID,
			Interface: target.TargetID,
			Kind:      operation.Kind,
			Phase:     operation.Phase,
			Deadline:  operation.Deadline,
			Origin:    operation.Origin,
		})
	}

	return entries, nil
}

func (s *Service) ReapplyHostInterfaceL3(name string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	return s.reapplyHostInterfaceL3Locked(name)
}

func (s *Service) reapplyHostInterfaceL3Locked(name string) error {
	row, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return err
	}
	if row == nil {
		return hostInterfaceL3NotFound(fmt.Errorf("no Host IP configuration for %s", name))
	}
	if pending, err := s.activeHostInterfaceL3Pending(name); err != nil {
		return err
	} else if pending != nil {
		return hostInterfaceL3PendingConflict(fmt.Errorf("operation %s is awaiting confirmation", pending.ID))
	}

	spec := hostInterfaceL3SpecFromRow(row)
	request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{
		IPv6Mode:         spec.IPv6Mode,
		MTU:              spec.MTU,
		Metric:           spec.Metric,
		ExpectedRevision: row.Revision,
	}
	for _, address := range spec.Addresses {
		request.Addresses = append(request.Addresses, networkServiceInterfaces.HostInterfaceL3AddressInput{
			Address: fmt.Sprintf("%s/%d", address.Address, address.PrefixLength),
		})
	}

	change, err := s.planHostInterfaceL3Change(name, row, request)
	if err != nil {
		return err
	}

	applied, applyErr := applyHostInterfaceL3Plan(change.Plan)
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Plan(change.Plan, applied)
	}
	if applyErr == nil {
		applyErr = waitForHostInterfaceL3DAD(change.Plan, applied)
	}
	if applyErr != nil {
		return errors.Join(
			fmt.Errorf("reapply host interface l3: %w", applyErr),
			revertHostInterfaceL3Runtime(name, change.Baseline, row.AppliedState, applied),
		)
	}

	row.AppliedState = networkModels.HostInterfaceL3AppliedState{
		Addresses:    change.Intended.Addresses,
		MTU:          row.AppliedState.MTU,
		Metric:       row.AppliedState.Metric,
		IPv6Disabled: row.AppliedState.IPv6Disabled,
		Up:           row.AppliedState.Up,
	}
	if err := s.DB.Save(row).Error; err != nil {
		return fmt.Errorf("persist reapplied host interface l3 state: %w", err)
	}

	return nil
}

func (s *Service) RecoverHostInterfaceL3() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var pending []networkModels.PendingApply
	if err := s.DB.Order("created_at asc").Find(&pending).Error; err != nil {
		return fmt.Errorf("load pending host interface l3 operations: %w", err)
	}

	var recoverErrors []error
	for _, operation := range pending {
		var target networkModels.PendingApplyTarget
		if err := s.DB.Where("pending_id = ?", operation.ID).First(&target).Error; err != nil {
			recoverErrors = append(recoverErrors, fmt.Errorf("load target for %s: %w", operation.ID, err))
			continue
		}

		row, err := s.loadHostInterfaceL3ByName(target.TargetID)
		if err != nil {
			recoverErrors = append(recoverErrors, err)
			continue
		}

		switch operation.Phase {
		case networkModels.PendingApplyPhaseConfirmed:
			if row != nil && row.Revision == target.CandidateRevision {
				s.deleteHostInterfaceL3Pending(operation.ID)
				continue
			}
			if err := s.promoteHostInterfaceL3Pending(&operation, &target); err != nil {
				recoverErrors = append(recoverErrors, err)
				continue
			}
			s.deleteHostInterfaceL3Pending(operation.ID)
		case networkModels.PendingApplyPhasePrepared:
			targetState := networkModels.HostInterfaceL3AppliedState{}
			if row != nil {
				targetState = row.AppliedState
			}
			if err := revertHostInterfaceL3Runtime(
				target.TargetID,
				operation.RuntimeSnapshot,
				targetState,
				operation.CandidateAppliedState,
			); err != nil {
				recoverErrors = append(recoverErrors, err)
				continue
			}
			s.deleteHostInterfaceL3Pending(operation.ID)
		case networkModels.PendingApplyPhaseApplied:
			if hostInterfaceL3Now().After(operation.Deadline) {
				if err := s.revertHostInterfaceL3PendingLocked(&operation, &target); err != nil {
					recoverErrors = append(recoverErrors, err)
					continue
				}
				s.deleteHostInterfaceL3Pending(operation.ID)
			}
		default:
			s.deleteHostInterfaceL3Pending(operation.ID)
		}
	}

	return errors.Join(recoverErrors...)
}

func (s *Service) ReconcileHostInterfaceL3() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var rows []networkModels.HostInterfaceL3
	if err := s.DB.Order("interface asc").Find(&rows).Error; err != nil {
		return fmt.Errorf("load host interface l3 rows: %w", err)
	}

	var reconcileErrors []error
	for _, row := range rows {
		if pending, err := s.activeHostInterfaceL3Pending(row.Interface); err != nil {
			reconcileErrors = append(reconcileErrors, err)
			continue
		} else if pending != nil {
			continue
		}
		if err := s.reapplyHostInterfaceL3Locked(row.Interface); err != nil {
			var hostErr *hostInterfaceL3Error
			if errors.As(err, &hostErr) {
				// Expected conflicts (missing interface, eligibility, identity)
				// are surfaced by the list endpoint, not as startup errors.
				logger.L.Debug().Err(err).Str("interface", row.Interface).Msg("host_interface_l3_reconcile_conflict")
				continue
			}
			reconcileErrors = append(reconcileErrors, err)
		}
	}

	return errors.Join(reconcileErrors...)
}

func (s *Service) ExpireHostInterfaceL3Pending(now time.Time) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var pending []networkModels.PendingApply
	if err := s.DB.
		Where("phase = ? AND deadline <= ?", networkModels.PendingApplyPhaseApplied, now).
		Find(&pending).Error; err != nil {
		return fmt.Errorf("load expiring host interface l3 operations: %w", err)
	}

	var expireErrors []error
	for _, operation := range pending {
		var target networkModels.PendingApplyTarget
		if err := s.DB.Where("pending_id = ?", operation.ID).First(&target).Error; err != nil {
			expireErrors = append(expireErrors, err)
			continue
		}
		if err := s.revertHostInterfaceL3PendingLocked(&operation, &target); err != nil {
			expireErrors = append(expireErrors, err)
			continue
		}
		s.deleteHostInterfaceL3Pending(operation.ID)
	}

	return errors.Join(expireErrors...)
}

func (s *Service) StartHostInterfaceL3Sweeper(ctx context.Context) {
	ticker := time.NewTicker(hostInterfaceL3SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ExpireHostInterfaceL3Pending(hostInterfaceL3Now()); err != nil {
				logger.L.Debug().Err(err).Msg("host_interface_l3_sweeper_failed")
			}
		}
	}
}
