// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestParseBootROMValue_EmptyDefaultsToUEFI(t *testing.T) {
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

func TestBuildBootROMLoader_NoneReturnsNil(t *testing.T) {
	loader := buildBootROMLoader(vmModels.VMBootROMNone, "/tmp/vm", 100)
	if loader != nil {
		t.Fatalf("expected nil loader for boot rom none, got: %#v", loader)
	}
}
