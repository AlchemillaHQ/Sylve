// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sysinfo

import "testing"

func TestVirtualBoxUEFI(t *testing.T) {
	for _, tc := range []struct {
		product, bootMethod string
		want                bool
	}{
		{"VirtualBox", "UEFI", true},
		{"VirtualBox\n", "UEFI\n", true},
		{"VirtualBox", "BIOS", false},
		{"VirtualBox", "", false},
		{"", "UEFI", false},
		{"BHYVE", "UEFI", false},
		{"KVM", "UEFI", false},
		{"VMware Virtual Platform", "UEFI", false},
		{"System Product Name", "UEFI", false},
	} {
		t.Run(tc.product+"/"+tc.bootMethod, func(t *testing.T) {
			if got := virtualBoxUEFI(tc.product, tc.bootMethod); got != tc.want {
				t.Fatalf("virtualBoxUEFI(%q, %q) = %v, want %v", tc.product, tc.bootMethod, got, tc.want)
			}
		})
	}
}
