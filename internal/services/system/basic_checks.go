// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) IsSupportedArch() bool {
	supportedArchs := []string{"amd64", "arm64"}
	arch := runtime.GOARCH

	for _, a := range supportedArchs {
		if arch == a {
			return true
		}
	}

	return false
}

func (s *Service) CheckVirtualization() error {
	requiredPackages := []string{
		"libvirt",
		"bhyve-firmware",
		"u-boot-bhyve-arm64",
		"swtpm",
		"qemu-tools",
	}

	for _, p := range requiredPackages {
		var packageToCheck string
		switch p {
		case "u-boot-bhyve-arm64", "bhyve-firmware":
			if runtime.GOARCH == "arm64" {
				packageToCheck = "u-boot-bhyve-arm64"
			} else {
				packageToCheck = "bhyve-firmware"
			}
		default:
			packageToCheck = p
		}

		if !pkg.IsPackageInstalled(packageToCheck) {
			return fmt.Errorf("virt_required_package_%s_not_installed", packageToCheck)
		}
	}

	out, err := utils.RunCommand("kldload", "-nv", "vmm")
	if err != nil {
		return fmt.Errorf("virt_failed_to_load_vmm: %w", err)
	}

	if len(out) > 0 {
		if !strings.Contains(out, "Loaded vmm") && !strings.Contains(out, "is already loaded") {
			return fmt.Errorf("virt_unexpected_vmm_load_output: %s", out)
		}
	}

	return nil
}

func (s *Service) CheckJails() error {
	out, err := utils.RunCommand("sysctl", "kern.racct.enable")
	if err != nil {
		return fmt.Errorf("jails_failed_to_check_racct: %w", err)
	}

	if !strings.Contains(out, "kern.racct.enable: 1") {
		return fmt.Errorf("jails_racct_not_enabled")
	}

	out, err = utils.RunCommand("sysctl", "security.jail.enforce_statfs")
	if err != nil {
		return fmt.Errorf("jails_failed_to_check_enforce_statfs: %w", err)
	}

	if !strings.Contains(out, "security.jail.enforce_statfs: 2") {
		return fmt.Errorf("jails_enforce_statfs_not_enabled")
	}

	return nil
}

func (s *Service) CheckDHCPServer() error {
	if !pkg.IsPackageInstalled("dnsmasq") {
		return fmt.Errorf("dhcp_server_required_package_dnsmasq_not_installed")
	}

	return nil
}

func (s *Service) CheckSambaServer() error {
	output, err := utils.RunCommand("pkg", "info")
	if err != nil {
		return fmt.Errorf("failed to run pkg info: %w", err)
	}

	lines := strings.Split(output, "\n")
	sambaInstalled := false

	for _, line := range lines {
		if strings.HasPrefix(line, "samba4") {
			sambaInstalled = true
			break
		}
	}

	if !sambaInstalled {
		return fmt.Errorf("samba4XX_erquired_package_not_installed")
	}

	return nil
}
