// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"strings"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) WolTasks() {
	for {
		var wols []utilitiesModels.WoL
		if err := s.DB.Find(&wols, "status = ?", "pending").Error; err != nil {
			logger.L.Error().Msgf("failed_to_find_pending_wol_tasks: %v", err)
		} else {
			for _, wol := range wols {
				vm, err := s.FindVmByMac(wol.Mac)
				if err != nil {
					var status string

					if strings.Contains(err.Error(), "vm_wol_disabled") {
						logger.L.Debug().Msgf("Wake-on-LAN is disabled for VM: %s (%d)", vm.Name, vm.RID)
						status = "wol_disabled"
					} else if strings.Contains(err.Error(), "record not found") {
						logger.L.Debug().Msgf("Failed to find VM associated with MAC: %s", wol.Mac)
						status = "vm_not_found"
					}

					if err := s.DB.Model(&wol).Update("status", status).Error; err != nil {
						logger.L.Error().Msgf("failed_to_update_wol_status: %v", err)
					}

					continue
				}

				err = s.LvVMAction(vm, "start")

				if err != nil {
					message := fmt.Sprintf("failed_to_start_vm: %s", err.Error())
					if err := s.DB.Model(&wol).Update("status", message).Error; err != nil {
						logger.L.Error().Msgf("failed_to_update_wol_status: %v", err)
					}
				} else {
					if err := s.DB.Model(&wol).Update("status", "completed").Error; err != nil {
						logger.L.Error().Msgf("failed_to_update_wol_status: %v", err)
					}
				}
			}
		}

		time.Sleep(2 * time.Second)
	}
}
