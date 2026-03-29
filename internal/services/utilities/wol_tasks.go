// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const wolProcessQueueName = "utils-wol-process"

type wolGuestKind string

const (
	wolGuestVM   wolGuestKind = "vm"
	wolGuestJail wolGuestKind = "jail"
)

type wolGuestCandidate struct {
	kind wolGuestKind
	vm   vmModels.VM
	jail jailModels.Jail
}

type wolProcessPayload struct {
	WoLID uint `json:"wolId"`
}

func (s *Service) registerWoLJobs() {
	db.QueueRegisterJSON(wolProcessQueueName, s.processQueuedWolTask)
}

func (s *Service) enqueueWoLTask(ctx context.Context, wolID uint) error {
	if wolID == 0 {
		return fmt.Errorf("invalid_wol_id")
	}

	return db.EnqueueJSON(ctx, wolProcessQueueName, wolProcessPayload{WoLID: wolID})
}

func (s *Service) processQueuedWolTask(ctx context.Context, payload wolProcessPayload) error {
	if payload.WoLID == 0 {
		return fmt.Errorf("invalid_wol_payload_id")
	}

	var wol utilitiesModels.WoL
	if err := s.DB.WithContext(ctx).First(&wol, payload.WoLID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.L.Warn().Uint("wol_id", payload.WoLID).Msg("wol_record_not_found_for_queue_payload")
			return nil
		}
		return fmt.Errorf("failed_to_fetch_wol_record: %w", err)
	}

	if strings.TrimSpace(wol.Status) != "pending" {
		return nil
	}

	status := s.processWolTask(wol)
	if status == "" {
		status = "failed_to_resolve_guest: empty_status"
	}

	if err := s.DB.WithContext(ctx).Model(&wol).Update("status", status).Error; err != nil {
		return fmt.Errorf("failed_to_update_wol_status: %w", err)
	}

	return nil
}

func (s *Service) processWolTask(wol utilitiesModels.WoL) string {
	candidates, err := s.resolveWoLCandidates(wol.Mac)
	if err != nil {
		logger.L.Warn().Err(err).Str("mac", wol.Mac).Msg("failed_to_resolve_guest_for_wol")
		return fmt.Sprintf("failed_to_resolve_guest: %s", err.Error())
	}

	if len(candidates) == 0 {
		logger.L.Debug().Msgf("No guest associated with MAC: %s", wol.Mac)
		return "guest_not_found"
	}

	eligible := make([]wolGuestCandidate, 0, len(candidates))
	for _, c := range candidates {
		switch c.kind {
		case wolGuestVM:
			if c.vm.WoL {
				eligible = append(eligible, c)
			}
		case wolGuestJail:
			if c.jail.WoL {
				eligible = append(eligible, c)
			}
		}
	}

	if len(eligible) == 0 {
		logger.L.Debug().Msgf("Wake-on-LAN is disabled for guests matching MAC: %s", wol.Mac)
		return "wol_disabled"
	}

	if len(eligible) > 1 {
		logger.L.Warn().Str("mac", wol.Mac).Int("eligible", len(eligible)).Msg("ambiguous_wol_mac")
		return "ambiguous_mac"
	}

	selected := eligible[0]
	switch selected.kind {
	case wolGuestVM:
		if err := s.startVMForWoL(selected.vm); err != nil {
			return fmt.Sprintf("failed_to_start_vm: %s", err.Error())
		}
	case wolGuestJail:
		if err := s.startJailForWoL(selected.jail); err != nil {
			return fmt.Sprintf("failed_to_start_jail: %s", err.Error())
		}
	default:
		return "failed_to_resolve_guest: unknown_guest_type"
	}

	return "completed"
}

func (s *Service) resolveWoLCandidates(mac string) ([]wolGuestCandidate, error) {
	vms, err := s.findVMsByMac(mac)
	if err != nil {
		return nil, err
	}

	jails, err := s.findJailsByMac(mac)
	if err != nil {
		return nil, err
	}

	candidates := make([]wolGuestCandidate, 0, len(vms)+len(jails))
	for _, vm := range vms {
		candidates = append(candidates, wolGuestCandidate{
			kind: wolGuestVM,
			vm:   vm,
		})
	}
	for _, jail := range jails {
		candidates = append(candidates, wolGuestCandidate{
			kind: wolGuestJail,
			jail: jail,
		})
	}

	return candidates, nil
}

func (s *Service) startVMForWoL(vm vmModels.VM) error {
	if s.wolStartVMFn != nil {
		return s.wolStartVMFn(vm)
	}

	if s.VMService == nil {
		return fmt.Errorf("vm_service_unavailable")
	}

	return s.VMService.LvVMAction(vm, "start")
}

func (s *Service) startJailForWoL(jail jailModels.Jail) error {
	if s.wolStartJailFn != nil {
		return s.wolStartJailFn(int(jail.CTID))
	}

	if s.JailService == nil {
		return fmt.Errorf("jail_service_unavailable")
	}

	return s.JailService.JailAction(int(jail.CTID), "start")
}

func (s *Service) findVMsByMac(mac string) ([]vmModels.VM, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return []vmModels.VM{}, nil
	}

	var vmIDs []uint
	err := s.DB.
		Session(&gorm.Session{SkipHooks: true}).
		Model(&vmModels.Network{}).
		Joins("LEFT JOIN objects ON vm_networks.mac_id = objects.id").
		Joins("LEFT JOIN object_entries ON object_entries.object_id = objects.id").
		Where("LOWER(object_entries.value) = ? OR LOWER(vm_networks.mac) = ?", mac, mac).
		Distinct("vm_networks.vm_id").
		Pluck("vm_networks.vm_id", &vmIDs).Error
	if err != nil {
		return nil, fmt.Errorf("failed_to_find_vm_networks: %w", err)
	}

	if len(vmIDs) == 0 {
		return []vmModels.VM{}, nil
	}

	var vms []vmModels.VM
	if err := s.DB.
		Where("id IN ?", vmIDs).
		Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_find_vms: %w", err)
	}

	return vms, nil
}

func (s *Service) findJailsByMac(mac string) ([]jailModels.Jail, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return []jailModels.Jail{}, nil
	}

	var jailIDs []uint
	err := s.DB.
		Session(&gorm.Session{SkipHooks: true}).
		Model(&jailModels.Network{}).
		Joins("LEFT JOIN objects ON jail_networks.mac_id = objects.id").
		Joins("LEFT JOIN object_entries ON object_entries.object_id = objects.id").
		Where("LOWER(object_entries.value) = ?", mac).
		Distinct("jail_networks.jid").
		Pluck("jail_networks.jid", &jailIDs).Error
	if err != nil {
		return nil, fmt.Errorf("failed_to_find_jail_networks: %w", err)
	}

	if len(jailIDs) == 0 {
		return []jailModels.Jail{}, nil
	}

	var jails []jailModels.Jail
	if err := s.DB.
		Where("id IN ?", jailIDs).
		Find(&jails).Error; err != nil {
		return nil, fmt.Errorf("failed_to_find_jails: %w", err)
	}

	return jails, nil
}
