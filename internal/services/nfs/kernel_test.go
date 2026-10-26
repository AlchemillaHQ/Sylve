// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"testing"
	"unsafe"
)

func TestDeleteExportCompatibilityABI(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit FreeBSD ABI assertions")
	}
	args := deleteExportArgs{}
	if unsafe.Sizeof(args) != 160 || unsafe.Offsetof(args.Address) != 96 || unsafe.Offsetof(args.NumFlavors) != 136 {
		t.Fatalf("o2export_args no longer matches FreeBSD sys/mount.h: size=%d address=%d flavors=%d", unsafe.Sizeof(args), unsafe.Offsetof(args.Address), unsafe.Offsetof(args.NumFlavors))
	}
}
