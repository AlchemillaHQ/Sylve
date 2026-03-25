// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) GetVMLogs(rid uint) (string, error) {
	if rid == 0 {
		return "", fmt.Errorf("invalid_rid")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("vm_not_found: %w", err)
		}
		return "", fmt.Errorf("failed_to_get_vm: %w", err)
	}

	logFilePath := filepath.Join("/var/log/libvirt/bhyve", fmt.Sprintf("%d.log", vm.RID))

	logs, err := utils.ReadLastLines(logFilePath, 512)
	if err != nil {
		return "", fmt.Errorf("failed_to_read_vm_logs: %w", err)
	}

	return logs, nil
}
