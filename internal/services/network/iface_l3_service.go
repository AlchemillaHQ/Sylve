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

func (s *Service) checkStandardSwitchPortsForHostInterfaceL3(ports []string) error {
	reservations, err := s.activeHostInterfaceL3Reservations()
	if err != nil {
		return err
	}

	for _, port := range ports {
		var count int64
		if err := s.DB.Model(&networkModels.HostInterfaceL3{}).
			Where("interface = ? OR vlan_parent = ?", port, port).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check Host IP configuration for port %s: %w", port, err)
		}
		if count > 0 {
			return standardSwitchConflict(
				"standard_switch_port_has_host_ip",
				fmt.Errorf("port %s is used by Host IP configuration directly or through a VLAN child; remove it on the Interfaces page first", port),
			)
		}
		if _, reserved := reservations[port]; reserved {
			return standardSwitchConflict(
				"standard_switch_port_has_host_ip",
				fmt.Errorf("port %s is reserved by a pending Host IP operation; confirm or revert it on the Interfaces page first", port),
			)
		}
	}

	return nil
}

func (s *Service) activeHostInterfaceL3Reservations() (map[string]struct{}, error) {
	var pending []networkModels.PendingApply
	if err := s.DB.Where(
		"kind IN ? AND phase = ?",
		[]string{networkModels.PendingApplyKindInterface, networkModels.PendingApplyKindDelete},
		networkModels.PendingApplyPhaseApplied,
	).Find(&pending).Error; err != nil {
		return nil, fmt.Errorf("load pending Host IP reservations: %w", err)
	}

	reserved := make(map[string]struct{}, len(pending)*2)
	for _, operation := range pending {
		if operation.Interface != "" {
			reserved[operation.Interface] = struct{}{}
		}
		if parent := operation.RuntimeSnapshot.VLANParent; parent != "" && parent != operation.Interface {
			reserved[parent] = struct{}{}
		}
	}
	return reserved, nil
}

func (s *Service) activeHostInterfaceL3Pending(name string) (*networkModels.PendingApply, error) {
	var pending networkModels.PendingApply
	err := s.DB.
		Where(
			"interface = ? AND kind IN ? AND phase IN ?",
			name,
			[]string{networkModels.PendingApplyKindInterface, networkModels.PendingApplyKindDelete},
			hostInterfaceL3PendingPhases(),
		).
		Order("created_at desc").
		First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load host interface l3 pending operation for %s: %w", name, err)
	}
	return &pending, nil
}

func (s *Service) loadHostInterfaceL3Pending(id string) (*networkModels.PendingApply, error) {
	var pending networkModels.PendingApply
	err := s.DB.Where("id = ?", id).First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}
	if err != nil {
		return nil, fmt.Errorf("load pending operation %s: %w", id, err)
	}
	if pending.Kind != networkModels.PendingApplyKindInterface && pending.Kind != networkModels.PendingApplyKindDelete {
		return nil, hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}
	return &pending, nil
}

func (s *Service) deleteHostInterfaceL3Pending(id string) {
	err := s.DB.Where("id = ?", id).Delete(&networkModels.PendingApply{}).Error
	if err != nil {
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

func hostInterfaceL3VerificationState(
	change hostInterfaceL3PlannedChange,
	applied networkModels.HostInterfaceL3AppliedState,
) networkModels.HostInterfaceL3AppliedState {
	expected := change.Intended
	expected.MTU = change.Plan.MTU
	expected.Metric = change.Plan.Metric
	if change.Plan.RestoreIPv6Flags != nil {
		expected.IPv6Disabled = nil
	} else {
		expected.IPv6Disabled = change.Plan.DisableIPv6
	}
	if expected.Up == nil {
		expected.Up = applied.Up
	}
	return expected
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
		ID:                     id,
		Interface:              name,
		Kind:                   networkModels.PendingApplyKindInterface,
		Phase:                  networkModels.PendingApplyPhasePrepared,
		CandidatePayload:       change.Spec,
		CandidateAppliedState:  change.Intended,
		RuntimeSnapshot:        change.Baseline,
		IdentityMAC:            change.IdentityMAC,
		CandidateRevision:      previousRevision + 1,
		RuntimeRestoreRequired: true,
		CreatedAt:              hostInterfaceL3Now(),
	}

	if err := s.DB.Create(&pending).Error; err != nil {
		return entry, fmt.Errorf("persist pending host interface l3 operation: %w", err)
	}

	targetState := networkModels.HostInterfaceL3AppliedState{}
	if current != nil {
		targetState = current.AppliedState
	}

	applied, applyErr := applyHostInterfaceL3Plan(change.Plan)
	verificationState := hostInterfaceL3VerificationState(change, applied)
	if applyErr == nil {
		applyErr = waitForHostInterfaceL3DAD(change.Plan, verificationState)
	}
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Plan(change.Plan, verificationState)
	}
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Removals(name, networkModels.HostInterfaceL3AppliedState{
			Addresses: change.Plan.Remove,
		}, networkModels.HostInterfaceL3Baseline{}, hostInterfaceL3ReAddedHostKeys(change.Plan.Addresses))
	}
	if applyErr != nil {
		revertErr := revertHostInterfaceL3Runtime(name, change.IdentityMAC, change.Baseline, targetState, applied)
		if revertErr != nil {
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
		revertErr := revertHostInterfaceL3Runtime(name, change.IdentityMAC, change.Baseline, targetState, applied)
		if revertErr != nil {
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

	expectedMAC := strings.TrimSpace(current.IdentityMAC)
	runtimeMutation := false
	runtimeSnapshot := current.AdoptionBaseline
	if live, err := syncIfaceGet(name); err != nil {
		if !isInterfaceMissingError(err) {
			return entry, fmt.Errorf("inspect interface %s: %w", name, err)
		}
	} else {
		if expectedMAC == "" {
			expectedMAC = strings.TrimSpace(live.Ether)
		}
		if expectedMAC != "" &&
			strings.EqualFold(expectedMAC, strings.TrimSpace(live.Ether)) &&
			live.VLANParent == current.VLANParent &&
			live.VLANTag == int(current.VLANTag) {
			runtimeMutation = true
			runtimeSnapshot = captureHostInterfaceL3Baseline(live)
		}
	}

	id, err := hostInterfaceL3NewID()
	if err != nil {
		return entry, fmt.Errorf("generate pending id: %w", err)
	}

	pending := networkModels.PendingApply{
		ID:                     id,
		Interface:              name,
		Kind:                   networkModels.PendingApplyKindDelete,
		Phase:                  networkModels.PendingApplyPhasePrepared,
		CandidateAppliedState:  networkModels.HostInterfaceL3AppliedState{},
		RuntimeSnapshot:        runtimeSnapshot,
		IdentityMAC:            expectedMAC,
		CandidateRevision:      current.Revision + 1,
		RuntimeRestoreRequired: runtimeMutation,
		CreatedAt:              hostInterfaceL3Now(),
	}

	if err := s.DB.Create(&pending).Error; err != nil {
		return entry, fmt.Errorf("persist pending host interface l3 delete: %w", err)
	}

	if runtimeMutation {
		revertErr := revertHostInterfaceL3Runtime(
			name,
			expectedMAC,
			current.AdoptionBaseline,
			networkModels.HostInterfaceL3AppliedState{},
			current.AppliedState,
		)
		if revertErr != nil {
			restoreErr := revertHostInterfaceL3Runtime(
				name,
				expectedMAC,
				runtimeSnapshot,
				current.AppliedState,
				networkModels.HostInterfaceL3AppliedState{},
			)
			if restoreErr != nil {
				return entry, errors.Join(fmt.Errorf("remove host interface l3: %w", revertErr), restoreErr)
			}
			s.deleteHostInterfaceL3Pending(id)
			return entry, fmt.Errorf("remove host interface l3: %w", revertErr)
		}
		if verifyErr := verifyHostInterfaceL3Removals(name, current.AppliedState, current.AdoptionBaseline, nil); verifyErr != nil {
			restoreErr := revertHostInterfaceL3Runtime(
				name,
				expectedMAC,
				runtimeSnapshot,
				current.AppliedState,
				networkModels.HostInterfaceL3AppliedState{},
			)
			if restoreErr != nil {
				return entry, errors.Join(fmt.Errorf("remove host interface l3: %w", verifyErr), restoreErr)
			}
			s.deleteHostInterfaceL3Pending(id)
			return entry, fmt.Errorf("remove host interface l3: %w", verifyErr)
		}
	}

	deadline := hostInterfaceL3Now().Add(HostInterfaceL3ConfirmationWindow)
	if err := s.DB.Model(&networkModels.PendingApply{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"phase":    networkModels.PendingApplyPhaseApplied,
			"deadline": deadline,
		}).Error; err != nil {
		var revertErr error
		if runtimeMutation {
			revertErr = revertHostInterfaceL3Runtime(
				name,
				expectedMAC,
				runtimeSnapshot,
				current.AppliedState,
				networkModels.HostInterfaceL3AppliedState{},
			)
		}
		if revertErr != nil {
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
	}, nil
}

func (s *Service) ConfirmHostInterfaceL3(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pending, err := s.loadHostInterfaceL3Pending(id)
	if err != nil {
		return err
	}

	if pending.Phase == networkModels.PendingApplyPhasePrepared {
		return hostInterfaceL3PendingConflict(fmt.Errorf("operation %s has not finished applying", id))
	}
	if pending.Phase != networkModels.PendingApplyPhaseApplied {
		return hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}
	if hostInterfaceL3Now().After(pending.Deadline) {
		revertErr := s.revertHostInterfaceL3PendingLocked(pending)
		if revertErr == nil {
			s.deleteHostInterfaceL3Pending(id)
		}
		return errors.Join(
			hostInterfaceL3Conflict("host_interface_l3_confirmation_expired", nil),
			revertErr,
		)
	}
	if err := s.verifyHostInterfaceL3PendingRuntime(pending); err != nil {
		return err
	}
	return s.promoteHostInterfaceL3Pending(pending)
}

func (s *Service) RevertHostInterfaceL3(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pending, err := s.loadHostInterfaceL3Pending(id)
	if err != nil {
		return err
	}
	if pending.Phase != networkModels.PendingApplyPhasePrepared && pending.Phase != networkModels.PendingApplyPhaseApplied {
		return hostInterfaceL3NotFound(fmt.Errorf("pending operation %s", id))
	}

	revertErr := s.revertHostInterfaceL3PendingLocked(pending)
	if revertErr != nil {
		return revertErr
	}
	s.deleteHostInterfaceL3Pending(id)
	return nil
}

func (s *Service) revertHostInterfaceL3PendingLocked(
	pending *networkModels.PendingApply,
) error {
	if !pending.RuntimeRestoreRequired {
		return nil
	}
	name := pending.Interface

	row, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return err
	}

	targetState := networkModels.HostInterfaceL3AppliedState{}
	if row != nil {
		targetState = row.AppliedState
	}
	return revertHostInterfaceL3Runtime(
		name,
		pending.IdentityMAC,
		pending.RuntimeSnapshot,
		targetState,
		pending.CandidateAppliedState,
	)
}

func (s *Service) verifyHostInterfaceL3PendingRuntime(
	pending *networkModels.PendingApply,
) error {
	name := pending.Interface
	row, err := s.loadHostInterfaceL3ByName(name)
	if err != nil {
		return err
	}

	if pending.Kind == networkModels.PendingApplyKindDelete {
		if !pending.RuntimeRestoreRequired || row == nil {
			return nil
		}
		return verifyHostInterfaceL3Restore(
			name,
			pending.IdentityMAC,
			row.AdoptionBaseline,
			networkModels.HostInterfaceL3AppliedState{},
			row.AppliedState,
		)
	}
	if err := s.validateHostInterfaceL3PendingTarget(name, pending); err != nil {
		return err
	}

	active := networkModels.HostInterfaceL3AppliedState{}
	baseline := pending.RuntimeSnapshot
	if row != nil {
		active = row.AppliedState
		baseline = row.AdoptionBaseline
	}
	candidate := pending.CandidateAppliedState
	plan := hostInterfaceL3ApplyPlan{
		Interface:          name,
		ExpectedMAC:        pending.IdentityMAC,
		ExpectedVLANParent: pending.RuntimeSnapshot.VLANParent,
		ExpectedVLANTag:    pending.RuntimeSnapshot.VLANTag,
	}
	if err := verifyHostInterfaceL3Plan(plan, candidate); err != nil {
		return err
	}

	readded := make(map[string]struct{}, len(candidate.Addresses))
	for _, address := range candidate.Addresses {
		readded[address.Family+"|"+hostInterfaceL3AddressIP(address.Address)] = struct{}{}
	}
	if err := verifyHostInterfaceL3Removals(
		name,
		networkModels.HostInterfaceL3AppliedState{Addresses: active.Addresses},
		networkModels.HostInterfaceL3Baseline{},
		readded,
	); err != nil {
		return err
	}

	live, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return hostInterfaceL3Conflict("host_interface_l3_missing_interface", err)
		}
		return fmt.Errorf("verify pending operation on %s: %w", name, err)
	}
	if err := ensureHostInterfaceL3Identity(live, pending.IdentityMAC); err != nil {
		return err
	}
	if candidate.MTU == nil && active.MTU != nil && baseline.MTU != nil && uint(live.MTU) != *baseline.MTU {
		return fmt.Errorf("verify pending operation on %s: MTU is %d, expected %d", name, live.MTU, *baseline.MTU)
	}
	if candidate.Metric == nil && active.Metric != nil && baseline.Metric != nil && uint(live.Metric) != *baseline.Metric {
		return fmt.Errorf("verify pending operation on %s: metric is %d, expected %d", name, live.Metric, *baseline.Metric)
	}
	if candidate.IPv6Disabled == nil && active.IPv6Disabled != nil {
		if baseline.ND6Flags != nil && live.ND6.Raw != *baseline.ND6Flags {
			return fmt.Errorf("verify pending operation on %s: ND6 options are %#x, expected %#x", name, live.ND6.Raw, *baseline.ND6Flags)
		}
	}
	if candidate.Up == nil && active.Up != nil && baseline.Up != nil && interfaceIsUp(live) != *baseline.Up {
		return fmt.Errorf("verify pending operation on %s: link state is incorrect", name)
	}

	return nil
}

func (s *Service) validateHostInterfaceL3PendingTarget(
	name string,
	pending *networkModels.PendingApply,
) error {
	live, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return hostInterfaceL3Conflict("host_interface_l3_missing_interface", err)
		}
		return fmt.Errorf("inspect pending Host IP target %s: %w", name, err)
	}
	if err := ensureHostInterfaceL3Identity(live, pending.IdentityMAC); err != nil {
		return err
	}
	if err := ensureHostInterfaceL3VLANIdentity(
		live,
		pending.RuntimeSnapshot.VLANParent,
		pending.RuntimeSnapshot.VLANTag,
	); err != nil {
		return err
	}
	if code := s.hostInterfaceL3EligibilityCode(live); code != "" {
		return hostInterfaceL3Conflict(code, nil)
	}
	liveInterfaces, err := hostInterfaceL3ListInterfaces()
	if err != nil {
		return fmt.Errorf("inspect interfaces for %s: %w", name, err)
	}
	if err := s.hostInterfaceL3MembershipGuardWithInterfaces(name, liveInterfaces); err != nil {
		return err
	}
	mtu := uint(live.MTU)
	if err := s.validateHostInterfaceL3VLANBoundaries(name, live, &mtu, liveInterfaces); err != nil {
		return err
	}
	return nil
}

func (s *Service) promoteHostInterfaceL3Pending(
	pending *networkModels.PendingApply,
) error {
	name := pending.Interface
	vlanParent := pending.RuntimeSnapshot.VLANParent
	vlanTag := pending.RuntimeSnapshot.VLANTag
	identityMAC := strings.TrimSpace(pending.IdentityMAC)

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
			return tx.Where("id = ?", pending.ID).Delete(&networkModels.PendingApply{}).Error
		}

		if !found {
			row.AdoptionBaseline = pending.RuntimeSnapshot
		}
		row.VLANParent = vlanParent
		row.VLANTag = vlanTag
		if row.IdentityMAC == "" {
			row.IdentityMAC = identityMAC
		}
		row.MTU = pending.CandidatePayload.MTU
		row.Metric = pending.CandidatePayload.Metric
		row.IPv6Mode = networkModels.HostInterfaceL3IPv6ModeInherit
		if pending.CandidatePayload.IPv6Mode != nil {
			row.IPv6Mode = *pending.CandidatePayload.IPv6Mode
		}
		row.AppliedState = pending.CandidateAppliedState
		row.Revision = pending.CandidateRevision

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

		return tx.Where("id = ?", pending.ID).Delete(&networkModels.PendingApply{}).Error
	})
}

func (s *Service) GetHostInterfaceL3Pending() ([]networkServiceInterfaces.HostInterfaceL3PendingEntry, error) {
	entries := make([]networkServiceInterfaces.HostInterfaceL3PendingEntry, 0)

	var pending []networkModels.PendingApply
	if err := s.DB.Where(
		"kind IN ? AND phase IN ?",
		[]string{networkModels.PendingApplyKindInterface, networkModels.PendingApplyKindDelete},
		hostInterfaceL3PendingPhases(),
	).
		Order("created_at asc").
		Find(&pending).Error; err != nil {
		return nil, fmt.Errorf("list pending host interface l3 operations: %w", err)
	}
	if len(pending) == 0 {
		return entries, nil
	}

	for _, operation := range pending {
		entries = append(entries, networkServiceInterfaces.HostInterfaceL3PendingEntry{
			ID:        operation.ID,
			Interface: operation.Interface,
			Kind:      operation.Kind,
			Phase:     operation.Phase,
			Deadline:  operation.Deadline,
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
	if live, err := syncIfaceGet(name); err == nil && hostInterfaceL3PrefixOwnershipChanged(row, live) {
		return hostInterfaceL3Conflict(networkServiceInterfaces.HostInterfaceL3ConflictPrefixOwnerChanged, nil)
	}

	change, err := s.planHostInterfaceL3Change(name, row, request)
	if err != nil {
		return err
	}

	applied, applyErr := applyHostInterfaceL3Plan(change.Plan)
	verificationState := hostInterfaceL3VerificationState(change, applied)
	if applyErr == nil {
		applyErr = waitForHostInterfaceL3DAD(change.Plan, verificationState)
	}
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Plan(change.Plan, verificationState)
	}
	if applyErr == nil {
		applyErr = verifyHostInterfaceL3Removals(name, networkModels.HostInterfaceL3AppliedState{
			Addresses: change.Plan.Remove,
		}, networkModels.HostInterfaceL3Baseline{}, hostInterfaceL3ReAddedHostKeys(change.Plan.Addresses))
	}
	if applyErr != nil {
		return errors.Join(
			fmt.Errorf("reapply host interface l3: %w", applyErr),
			revertHostInterfaceL3Runtime(name, change.IdentityMAC, change.Baseline, row.AppliedState, applied),
		)
	}

	previousState := row.AppliedState
	row.AppliedState = change.Intended
	if err := s.DB.Save(row).Error; err != nil {
		return errors.Join(
			fmt.Errorf("persist reapplied host interface l3 state: %w", err),
			revertHostInterfaceL3Runtime(name, change.IdentityMAC, change.Baseline, previousState, applied),
		)
	}

	return nil
}

func (s *Service) RecoverHostInterfaceL3() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var pending []networkModels.PendingApply
	if err := s.DB.
		Where("kind IN ?", []string{networkModels.PendingApplyKindInterface, networkModels.PendingApplyKindDelete}).
		Order("created_at asc").
		Find(&pending).Error; err != nil {
		return fmt.Errorf("load pending host interface l3 operations: %w", err)
	}

	var recoverErrors []error
	for _, operation := range pending {
		if operation.Phase != networkModels.PendingApplyPhasePrepared &&
			operation.Phase != networkModels.PendingApplyPhaseApplied {
			recoverErrors = append(recoverErrors, fmt.Errorf(
				"pending Host IP operation %s has unknown phase %q",
				operation.ID,
				operation.Phase,
			))
			continue
		}
		if err := s.revertHostInterfaceL3PendingLocked(&operation); err != nil {
			recoverErrors = append(recoverErrors, err)
			continue
		}
		s.deleteHostInterfaceL3Pending(operation.ID)
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
		Where(
			"kind IN ? AND phase = ? AND deadline <= ?",
			[]string{networkModels.PendingApplyKindInterface, networkModels.PendingApplyKindDelete},
			networkModels.PendingApplyPhaseApplied,
			now,
		).
		Find(&pending).Error; err != nil {
		return fmt.Errorf("load expiring host interface l3 operations: %w", err)
	}

	var expireErrors []error
	for _, operation := range pending {
		if err := s.revertHostInterfaceL3PendingLocked(&operation); err != nil {
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
