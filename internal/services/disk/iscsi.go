// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"fmt"
	"strings"
	"unicode"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func isDADisk(device string) bool {
	if !strings.HasPrefix(device, "da") || len(device) == 2 {
		return false
	}
	for _, char := range device[2:] {
		if !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

func hasAmbiguousSCSIDisk(disks []diskServiceInterfaces.DiskInfo) bool {
	for _, disk := range disks {
		if isDADisk(disk.Name) && !disk.IsISCSI {
			return true
		}
	}
	return false
}

func parseISCSICAMDevices(output string) map[string]struct{} {
	iscsiBuses := make(map[string]struct{})
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.HasPrefix(fields[0], "scbus") && fields[1] == "on" && strings.HasPrefix(strings.ToLower(fields[2]), "iscsi") {
			iscsiBuses[strings.TrimSuffix(fields[0], ":")] = struct{}{}
		}
	}

	devices := make(map[string]struct{})
	for _, line := range lines {
		for bus := range iscsiBuses {
			if !strings.Contains(line, " at "+bus+" ") {
				continue
			}
			open := strings.LastIndex(line, "(")
			close := strings.LastIndex(line, ")")
			if open < 0 || close <= open {
				continue
			}
			for _, device := range strings.Split(line[open+1:close], ",") {
				device = strings.TrimSpace(device)
				if isDADisk(device) {
					devices[device] = struct{}{}
				}
			}
		}
	}
	return devices
}

func (s *Service) iscsiDevices() (map[string]struct{}, error) {
	if s.iscsiDeviceSource != nil {
		return s.iscsiDeviceSource()
	}
	output, err := utils.RunCommand("/sbin/camcontrol", "devlist", "-v")
	if err != nil {
		return nil, fmt.Errorf("list CAM devices: %w", err)
	}
	return parseISCSICAMDevices(output), nil
}
