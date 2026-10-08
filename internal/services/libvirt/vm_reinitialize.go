// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"os"
	"strconv"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/digitalocean/go-libvirt"
)

type vmConfigConnection interface {
	DomainLookupByName(string) (libvirt.Domain, error)
	DomainDefineXML(string) (libvirt.Domain, error)
}

// ReinitializeVMConfig restores only an absent libvirt definition. Unlike
// CreateLvVm, it must not clear runtime state, provision disks, or flash images.
func (s *Service) ReinitializeVMConfig(rid uint, ctx context.Context) error {
	return s.reinitializeVMConfig(rid, ctx, func() (vmConfigConnection, error) {
		return s.ensureConnection()
	})
}

func (s *Service) reinitializeVMConfig(
	rid uint,
	ctx context.Context,
	connect func() (vmConfigConnection, error),
) error {
	if rid == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, false)
	if err != nil {
		return err
	}
	if err := s.ensureNoActiveVMLifecycleTask(rid); err != nil {
		return err
	}

	var identityClaim *clusterServiceInterfaces.GuestIdentityReservation
	if s.guestIdentityCoordinator != nil {
		claim, err := s.guestIdentityCoordinator.GuestIdentityClaim(ctx, clusterModels.ReplicationGuestTypeVM, rid, "")
		if err != nil {
			return err
		}
		defer s.guestIdentityCoordinator.CancelGuestIdentityClaim(claim)
		if err := s.guestIdentityCoordinator.ValidateGuestIdentityClaim(ctx, claim); err != nil {
			return err
		}
		identityClaim = &claim
	}

	conn, err := connect()
	if err != nil {
		return fmt.Errorf("libvirt_connection_unavailable: %w", err)
	}
	if err := requireVMConfigDomainAbsent(conn, rid); err != nil {
		return err
	}

	unlockSwitchLifecycle, err := s.lockVMStandardSwitchLifecycle(vm.ID, true)
	if err != nil {
		return err
	}
	defer unlockSwitchLifecycle()

	vmPath, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_config_directory: %w", err)
	}
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed_to_create_vm_config_directory: %w", err)
	}
	if err := s.ensureVMBootROMArtifacts(rid, vm.BootROM, vmPath); err != nil {
		return fmt.Errorf("failed_to_prepare_boot_rom_artifacts: %w", err)
	}
	if vmHasCloudInitConfiguration(vm) {
		if err := s.CreateCloudInitISO(vm); err != nil {
			return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
		}
	}

	xml, err := s.CreateVmXML(vm, vmPath)
	if err != nil {
		return fmt.Errorf("failed_to_generate_vm_xml: %w", err)
	}

	if err := s.requireVMMutationOwnership(rid); err != nil {
		return err
	}
	if err := s.ensureNoActiveVMLifecycleTask(rid); err != nil {
		return err
	}
	if identityClaim != nil {
		if err := s.guestIdentityCoordinator.ValidateGuestIdentityClaim(ctx, *identityClaim); err != nil {
			return err
		}
	}
	// An administrator may have restored the definition while XML was generated.
	if err := requireVMConfigDomainAbsent(conn, rid); err != nil {
		return err
	}
	if _, err := conn.DomainDefineXML(xml); err != nil {
		return fmt.Errorf("failed_to_define_vm_domain: %w", err)
	}

	s.emitLeftPanelRefresh("vm_config_reinitialized")
	return nil
}

func requireVMConfigDomainAbsent(conn vmConfigConnection, rid uint) error {
	_, err := conn.DomainLookupByName(strconv.FormatUint(uint64(rid), 10))
	if err == nil {
		return fmt.Errorf("vm_not_orphaned")
	}
	if !libvirtServiceInterfaces.IsDomainNotFoundError(err) {
		return fmt.Errorf("vm_orphan_check_unavailable: %w", err)
	}
	return nil
}

func (s *Service) ensureNoActiveVMLifecycleTask(rid uint) error {
	var count int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).
		Where("guest_type = ? AND guest_id = ? AND status IN ?", taskModels.GuestTypeVM, rid, []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_vm_lifecycle_tasks: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("lifecycle_task_in_progress")
	}
	return nil
}
