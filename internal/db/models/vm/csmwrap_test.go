// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vmModels

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestCSMWrapBootModeNormalization(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &VM{}, &VMTemplate{})
	vm := VM{RID: 4010, Name: "csmwrap", BootROM: " UEFI_CSMWRAP "}
	template := VMTemplate{Name: "csmwrap-template", BootROM: " UEFI_CSMWRAP "}
	for _, model := range []any{&vm, &template} {
		if err := db.Create(model).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	if vm.BootROM != VMBootROMUEFICSMWrap || template.BootROM != VMBootROMUEFICSMWrap {
		t.Fatal("helper mode did not survive database round trip")
	}
}
