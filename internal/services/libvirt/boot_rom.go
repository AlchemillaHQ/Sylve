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
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/assets"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

const (
	uefiFirmwarePath = "/usr/local/share/uefi-firmware/BHYVE_UEFI.fd"
	csmROMAssetPath  = "roms/uefi-legacy-csm-rom.bin"
	csmROMFileName   = "uefi-legacy-csm-rom.bin"
)

func normalizeBootROMValue(value vmModels.VMBootROM) vmModels.VMBootROM {
	switch strings.TrimSpace(strings.ToLower(string(value))) {
	case "", string(vmModels.VMBootROMUEFI):
		return vmModels.VMBootROMUEFI
	case string(vmModels.VMBootROMUEFICSM):
		return vmModels.VMBootROMUEFICSM
	case string(vmModels.VMBootROMNone):
		return vmModels.VMBootROMNone
	default:
		return vmModels.VMBootROM(strings.TrimSpace(strings.ToLower(string(value))))
	}
}

func parseBootROMValue(value string) (vmModels.VMBootROM, error) {
	normalized := normalizeBootROMValue(vmModels.VMBootROM(value))
	switch normalized {
	case vmModels.VMBootROMUEFI, vmModels.VMBootROMUEFICSM, vmModels.VMBootROMNone:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid_boot_rom: %s", strings.TrimSpace(value))
	}
}

func buildBootROMLoader(bootROM vmModels.VMBootROM, vmPath string, rid uint) *libvirtServiceInterfaces.Loader {
	switch normalizeBootROMValue(bootROM) {
	case vmModels.VMBootROMNone:
		return nil
	case vmModels.VMBootROMUEFICSM:
		return &libvirtServiceInterfaces.Loader{
			ReadOnly: "yes",
			Type:     "pflash",
			Path:     fmt.Sprintf("%s/%s,%s/%d_vars.fd", vmPath, csmROMFileName, vmPath, rid),
		}
	default:
		return &libvirtServiceInterfaces.Loader{
			ReadOnly: "yes",
			Type:     "pflash",
			Path:     fmt.Sprintf("%s,%s/%d_vars.fd", uefiFirmwarePath, vmPath, rid),
		}
	}
}

func (s *Service) ensureVMBootROMArtifacts(rid uint, bootROM vmModels.VMBootROM, vmPath string) error {
	if rid == 0 {
		return fmt.Errorf("invalid_rid")
	}

	normalized := normalizeBootROMValue(bootROM)
	if normalized == vmModels.VMBootROMNone {
		return nil
	}

	if strings.TrimSpace(vmPath) == "" {
		return fmt.Errorf("invalid_vm_path")
	}

	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed_to_ensure_vm_path_for_boot_rom: %w", err)
	}

	varsPath := filepath.Join(vmPath, fmt.Sprintf("%d_vars.fd", rid))
	if _, err := os.Stat(varsPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed_to_stat_uefi_vars: %w", err)
		}

		if err := s.ResetUEFIVars(rid); err != nil {
			return fmt.Errorf("failed_to_prepare_uefi_vars: %w", err)
		}
	}

	if normalized != vmModels.VMBootROMUEFICSM {
		return nil
	}

	romPath := filepath.Join(vmPath, csmROMFileName)
	if _, err := os.Stat(romPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed_to_stat_vm_csm_rom: %w", err)
		}

		romBytes, readErr := assets.RomFiles.ReadFile(csmROMAssetPath)
		if readErr != nil {
			return fmt.Errorf("failed_to_read_embedded_csm_rom: %w", readErr)
		}

		if writeErr := os.WriteFile(romPath, romBytes, 0644); writeErr != nil {
			return fmt.Errorf("failed_to_write_vm_csm_rom: %w", writeErr)
		}
	}

	return nil
}
