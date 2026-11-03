// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"runtime"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestParseBootROMValue_EmptyDefaultsToUEFI(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("empty defaults to uboot on arm64")
	}

	got, err := parseBootROMValue("")
	if err != nil {
		t.Fatalf("parseBootROMValue returned error: %v", err)
	}

	if got != vmModels.VMBootROMUEFI {
		t.Fatalf("parseBootROMValue empty = %q, want %q", got, vmModels.VMBootROMUEFI)
	}
}

func TestParseBootROMValue_InvalidValue(t *testing.T) {
	_, err := parseBootROMValue("broken")
	if err == nil {
		t.Fatal("expected parseBootROMValue to fail for invalid value")
	}

	if !strings.Contains(err.Error(), "invalid_boot_rom") {
		t.Fatalf("expected invalid_boot_rom error, got: %v", err)
	}
}

func TestParseBootROMValue_RejectsDeprecatedUEFICSM(t *testing.T) {
	_, err := parseBootROMValue("uefi_csm")
	if err == nil {
		t.Fatal("expected parseBootROMValue to fail for deprecated uefi_csm value")
	}

	if !strings.Contains(err.Error(), "invalid_boot_rom") {
		t.Fatalf("expected invalid_boot_rom error, got: %v", err)
	}
}

func TestParseBootROMValue_UEFIValid(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("uefi not available on arm64")
	}

	got, err := parseBootROMValue("uefi")
	if err != nil {
		t.Fatalf("parseBootROMValue(uefi) unexpected error: %v", err)
	}
	if got != vmModels.VMBootROMUEFI {
		t.Fatalf("parseBootROMValue(uefi) = %q, want %q", got, vmModels.VMBootROMUEFI)
	}
}

func TestParseBootROMValue_UBootRejectedOnAmd64(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("test is for amd64 only")
	}

	_, err := parseBootROMValue("uboot")
	if err == nil {
		t.Fatal("expected parseBootROMValue(uboot) to fail on amd64")
	}
	if !strings.Contains(err.Error(), "uboot_only_available_on_arm64") {
		t.Fatalf("expected uboot_only_available_on_arm64 error, got: %v", err)
	}
}

func TestParseBootROMValue_UEFIRejectedOnArm64(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("test is for arm64 only")
	}

	_, err := parseBootROMValue("uefi")
	if err == nil {
		t.Fatal("expected parseBootROMValue(uefi) to fail on arm64")
	}
	if !strings.Contains(err.Error(), "uefi_firmware_not_available_on_arm64") {
		t.Fatalf("expected uefi_firmware_not_available_on_arm64 error, got: %v", err)
	}
}

func TestParseBootROMValue_NoneAlwaysValid(t *testing.T) {
	got, err := parseBootROMValue("none")
	if err != nil {
		t.Fatalf("parseBootROMValue(none) unexpected error: %v", err)
	}
	if got != vmModels.VMBootROMNone {
		t.Fatalf("parseBootROMValue(none) = %q, want %q", got, vmModels.VMBootROMNone)
	}
}

func TestBuildBootROMLoader_NoneReturnsNil(t *testing.T) {
	loader := buildBootROMLoader(vmModels.VMBootROMNone, "/tmp/vm", 100)
	if loader != nil {
		t.Fatalf("expected nil loader for boot rom none, got: %#v", loader)
	}
}

func TestBuildBootROMLoader_UEFIHasVarsPath(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("uefi not used on arm64")
	}

	loader := buildBootROMLoader(vmModels.VMBootROMUEFI, "/tmp/vm", 42)
	if loader == nil {
		t.Fatal("expected non-nil loader for uefi")
	}
	if loader.Type != "pflash" {
		t.Fatalf("expected type pflash, got %q", loader.Type)
	}
	if loader.ReadOnly != "yes" {
		t.Fatalf("expected readonly yes, got %q", loader.ReadOnly)
	}
	if !strings.Contains(loader.Path, "BHYVE_UEFI.fd") {
		t.Fatalf("expected path to contain BHYVE_UEFI.fd, got %q", loader.Path)
	}
	if !strings.Contains(loader.Path, "42_vars.fd") {
		t.Fatalf("expected path to contain 42_vars.fd, got %q", loader.Path)
	}
	if !strings.Contains(loader.Path, ",") {
		t.Fatal("expected comma-separated CODE+VARS firmware path")
	}
}

func TestBuildBootROMLoader_UBootSinglePath(t *testing.T) {
	loader := buildBootROMLoader(vmModels.VMBootROMUBoot, "/tmp/vm", 42)
	if loader == nil {
		t.Fatal("expected non-nil loader for uboot")
	}
	if loader.Type != "pflash" {
		t.Fatalf("expected type pflash, got %q", loader.Type)
	}
	if loader.ReadOnly != "yes" {
		t.Fatalf("expected readonly yes, got %q", loader.ReadOnly)
	}
	if loader.Path != ubootFirmwarePath {
		t.Fatalf("expected path %q, got %q", ubootFirmwarePath, loader.Path)
	}
	if strings.Contains(loader.Path, ",") {
		t.Fatal("uboot firmware path should not contain comma (no VARS split)")
	}
	if strings.Contains(loader.Path, "_vars.fd") {
		t.Fatal("uboot firmware path should not contain _vars.fd")
	}
}

func TestHostUsesSplitFirmware(t *testing.T) {
	got := hostUsesSplitFirmware()

	switch runtime.GOARCH {
	case "amd64":
		if !got {
			t.Fatal("expected hostUsesSplitFirmware = true on amd64")
		}
	case "arm64":
		if got {
			t.Fatal("expected hostUsesSplitFirmware = false on arm64")
		}
	}
}

func TestBuildBootROMLoader_DefaultIsArchAware(t *testing.T) {
	// Passing empty string should normalize to the arch default
	loader := buildBootROMLoader("", "/tmp/vm", 1)
	if loader == nil {
		t.Fatal("expected non-nil loader for default boot rom")
	}

	switch runtime.GOARCH {
	case "amd64":
		if !strings.Contains(loader.Path, "BHYVE_UEFI.fd") {
			t.Fatalf("amd64: expected UEFI firmware path, got %q", loader.Path)
		}
	case "arm64":
		if loader.Path != ubootFirmwarePath {
			t.Fatalf("arm64: expected uboot firmware path %q, got %q", ubootFirmwarePath, loader.Path)
		}
	}
}
