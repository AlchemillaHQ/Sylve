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
	"runtime"
	"strings"

	"github.com/alchemillahq/sylve/internal/assets/csmwrap"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	uefiFirmwarePath  = "/usr/local/share/uefi-firmware/BHYVE_UEFI.fd"
	ubootFirmwarePath = "/usr/local/share/u-boot/u-boot-bhyve-arm64/u-boot.bin"
)

func hostLibvirtArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

func hostFirmwarePath() string {
	switch runtime.GOARCH {
	case "arm64":
		return ubootFirmwarePath
	default:
		return uefiFirmwarePath
	}
}

func hostUsesSplitFirmware() bool {
	return runtime.GOARCH != "arm64"
}

func availableBootROMs() []vmModels.VMBootROM {
	if runtime.GOARCH == "arm64" {
		return []vmModels.VMBootROM{vmModels.VMBootROMUBoot, vmModels.VMBootROMNone}
	}
	if runtime.GOARCH == "amd64" {
		return []vmModels.VMBootROM{vmModels.VMBootROMUEFI, vmModels.VMBootROMUEFICSMWrap, vmModels.VMBootROMNone}
	}
	return []vmModels.VMBootROM{vmModels.VMBootROMUEFI, vmModels.VMBootROMNone}
}

func normalizeBootROMValue(value vmModels.VMBootROM) vmModels.VMBootROM {
	switch strings.TrimSpace(strings.ToLower(string(value))) {
	case "":
		if runtime.GOARCH == "arm64" {
			return vmModels.VMBootROMUBoot
		}
		return vmModels.VMBootROMUEFI
	case string(vmModels.VMBootROMUEFI):
		return vmModels.VMBootROMUEFI
	case string(vmModels.VMBootROMUEFICSMWrap):
		return vmModels.VMBootROMUEFICSMWrap
	case string(vmModels.VMBootROMUBoot):
		return vmModels.VMBootROMUBoot
	case string(vmModels.VMBootROMNone):
		return vmModels.VMBootROMNone
	default:
		return vmModels.VMBootROM(strings.TrimSpace(strings.ToLower(string(value))))
	}
}

func parseBootROMValue(value string) (vmModels.VMBootROM, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))

	normalized := normalizeBootROMValue(vmModels.VMBootROM(trimmed))

	switch normalized {
	case vmModels.VMBootROMUEFICSMWrap:
		if runtime.GOARCH != "amd64" {
			return "", fmt.Errorf("csmwrap_only_available_on_amd64")
		}
		return normalized, nil
	case vmModels.VMBootROMNone:
		return normalized, nil
	case vmModels.VMBootROMUEFI:
		if runtime.GOARCH == "arm64" {
			return "", fmt.Errorf("uefi_firmware_not_available_on_arm64")
		}
		return normalized, nil
	case vmModels.VMBootROMUBoot:
		if runtime.GOARCH != "arm64" {
			return "", fmt.Errorf("uboot_only_available_on_arm64")
		}
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid_boot_rom: %s", trimmed)
	}
}

func buildBootROMLoader(bootROM vmModels.VMBootROM, vmPath string, rid uint) *libvirtServiceInterfaces.Loader {
	switch normalizeBootROMValue(bootROM) {
	case vmModels.VMBootROMNone:
		return nil
	case vmModels.VMBootROMUBoot:
		return &libvirtServiceInterfaces.Loader{
			ReadOnly: "yes",
			Type:     "pflash",
			Path:     ubootFirmwarePath,
		}
	default:
		path := uefiFirmwarePath + "," + filepath.Join(vmPath, vmVarsFilename(rid, bootROM))
		if normalizeBootROMValue(bootROM) == vmModels.VMBootROMUEFICSMWrap {
			path += ",fwcfg=qemu"
		}
		return &libvirtServiceInterfaces.Loader{
			ReadOnly: "yes",
			Type:     "pflash",
			Path:     path,
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

	// u-boot is a single firmware binary — no per-VM VARS file to prepare
	if normalized == vmModels.VMBootROMUBoot {
		return nil
	}

	if strings.TrimSpace(vmPath) == "" {
		return fmt.Errorf("invalid_vm_path")
	}

	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed_to_ensure_vm_path_for_boot_rom: %w", err)
	}

	varsPath := filepath.Join(vmPath, vmVarsFilename(rid, bootROM))
	if _, err := os.Stat(varsPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed_to_stat_uefi_vars: %w", err)
		}

		if err := writeVMBootVars(rid, bootROM, vmPath); err != nil {
			return fmt.Errorf("failed_to_prepare_uefi_vars: %w", err)
		}
	}

	return nil
}

func vmVarsFilename(rid uint, bootROM vmModels.VMBootROM) string {
	if normalizeBootROMValue(bootROM) == vmModels.VMBootROMUEFICSMWrap {
		return fmt.Sprintf("%d_csmwrap_vars.fd", rid)
	}
	return fmt.Sprintf("%d_vars.fd", rid)
}

func writeVMBootVars(rid uint, bootROM vmModels.VMBootROM, vmPath string) error {
	return utils.CopyFile("/usr/local/share/uefi-firmware/BHYVE_UEFI_VARS.fd",
		filepath.Join(vmPath, vmVarsFilename(rid, bootROM)))
}

// ValidateVMBootConfiguration is also used by restore and target preflight,
// before provisioning resources or committing imported VM records.
func ValidateVMBootConfiguration(vm vmModels.VM) error {
	if normalizeBootROMValue(vm.BootROM) != vmModels.VMBootROMUEFICSMWrap {
		return nil
	}
	if _, err := csmwrap.Info(); err != nil {
		return err
	}
	if err := validateCSMWrapCPUs(vm.CPUSockets, vm.CPUCores, vm.CPUThreads); err != nil {
		return err
	}
	if parseUsedPCIIndices(normalizeExtraBhyveOptions(vm.ExtraBhyveOptions))[csmwrapPCISlot] {
		return fmt.Errorf("csmwrap_pci_slot_in_use: %d", csmwrapPCISlot)
	}
	return nil
}

func validateCSMWrapCPUs(sockets, cores, threads int) error {
	if err := validateCPUHardwareRequest(libvirtServiceInterfaces.ModifyCPURequest{
		CPUSockets: sockets, CPUCores: cores, CPUThreads: threads,
	}); err != nil {
		return err
	}
	if sockets*cores*threads < 2 {
		return fmt.Errorf("csmwrap_requires_two_vcpus")
	}
	return nil
}

func VMBootRequirement(vm vmModels.VM) *libvirtServiceInterfaces.VMBootRequirement {
	if normalizeBootROMValue(vm.BootROM) != vmModels.VMBootROMUEFICSMWrap {
		return nil
	}
	return &libvirtServiceInterfaces.VMBootRequirement{
		BootROM:    vm.BootROM,
		CPUSockets: vm.CPUSockets, CPUCores: vm.CPUCores, CPUThreads: vm.CPUThreads,
	}
}

func CheckVMBootRequirement(requirement *libvirtServiceInterfaces.VMBootRequirement) error {
	if requirement == nil {
		return nil
	}
	mode, err := parseBootROMValue(string(requirement.BootROM))
	if err != nil {
		return err
	}
	if mode != vmModels.VMBootROMUEFICSMWrap {
		return fmt.Errorf("invalid_boot_rom_requirement")
	}
	vm := vmModels.VM{
		BootROM:    mode,
		CPUSockets: requirement.CPUSockets, CPUCores: requirement.CPUCores, CPUThreads: requirement.CPUThreads,
	}
	if err := ValidateVMBootConfiguration(vm); err != nil {
		return err
	}
	maxCPUs, err := utils.GetVMMMaxCPUs()
	if err != nil {
		return err
	}
	if int64(vm.CPUSockets*vm.CPUCores*vm.CPUThreads) > maxCPUs {
		return fmt.Errorf("vm_vcpu_limit_exceeded")
	}
	if stat, err := os.Stat(uefiFirmwarePath); err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 {
		return fmt.Errorf("csmwrap_uefi_firmware_unavailable")
	}
	if stat, err := os.Stat("/usr/local/share/uefi-firmware/BHYVE_UEFI_VARS.fd"); err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 {
		return fmt.Errorf("csmwrap_uefi_vars_unavailable")
	}
	return nil
}
