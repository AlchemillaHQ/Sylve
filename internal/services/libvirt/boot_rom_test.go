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

func TestParseBootROMValue(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		onlyOn  string // run only on this GOARCH
		skipOn  string // skip on this GOARCH
		want    vmModels.VMBootROM
		wantErr string
	}{
		{name: "empty defaults to uefi", input: "", skipOn: "arm64", want: vmModels.VMBootROMUEFI},
		{name: "uefi", input: "uefi", skipOn: "arm64", want: vmModels.VMBootROMUEFI},
		{name: "none", input: "none", want: vmModels.VMBootROMNone},
		{name: "invalid value", input: "broken", wantErr: "invalid_boot_rom"},
		{name: "deprecated uefi_csm", input: "uefi_csm", wantErr: "invalid_boot_rom"},
		{name: "uboot rejected on amd64", input: "uboot", onlyOn: "amd64", wantErr: "uboot_only_available_on_arm64"},
		{name: "uefi rejected on arm64", input: "uefi", onlyOn: "arm64", wantErr: "uefi_firmware_not_available_on_arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.onlyOn != "" && runtime.GOARCH != tt.onlyOn) || runtime.GOARCH == tt.skipOn {
				t.Skipf("not applicable on %s", runtime.GOARCH)
			}
			got, err := parseBootROMValue(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseBootROMValue(%q) error = %v, want %s", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseBootROMValue(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}
}
