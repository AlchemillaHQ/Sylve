// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"testing"

	"github.com/alchemillahq/sylve/installer/backend/internal/engine"
)

func TestX86VirtFeature(t *testing.T) {
	cases := []struct{ name, dmesg, want string }{
		{"intel", "CPU: Intel\n  Features2=0x7ffafbff<SSE3,VMX,AVX>\n", "Intel VT-x"},
		{"amd", "CPU: AMD\n  AMD Features2=0x8003f7<LAHF,SVM,CR8>\n", "AMD-V (SVM)"},
		{"neither", "CPU: plain\n  Features2=0x1<SSE3>\n", ""},
	}
	for _, tc := range cases {
		if got := x86VirtFeature(tc.dmesg); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestArm64VirtFeature(t *testing.T) {
	cases := []struct{ name, dmesg, want string }{
		{
			"el2 and gicv3",
			"CPU  0: ARM Neoverse-N1\n" +
				"  Processor Features 0 = <FP,AdvSIMD,EL2,GIC,EL0,EL1>\n" +
				"gic0: <ARM Generic Interrupt Controller v3.0> iomem 0x0-0xffff\n",
			"ARM64 virtualization (EL2 + GICv3)",
		},
		{
			"el2 only",
			"  Processor Features 0 = <FP,AdvSIMD,EL2,EL0,EL1>\n",
			"ARM64 EL2 (no GICv3)",
		},
		{
			"gic only",
			"  Processor Features 0 = <FP,AdvSIMD,GIC,EL0,EL1>\n",
			"ARM64 GICv3 (no EL2)",
		},
		{
			"sel2 does not match el2",
			"  Processor Features 0 = <FP,AdvSIMD,SEL2,EL0,EL1>\n",
			"",
		},
		{"nothing", "  Processor Features 0 = <FP,AdvSIMD>\n", ""},
	}
	for _, tc := range cases {
		if got := arm64VirtFeature(tc.dmesg); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestPreflightNeedsInstallableDisks(t *testing.T) {
	boot := engine.Disk{Name: "nda0", Boot: true}
	target := engine.Disk{Name: "nda1"}
	if _, ok := diskSummary([]engine.Disk{boot}); ok {
		t.Fatal("installer media counted as an install target")
	}
	if _, ok := diskSummary([]engine.Disk{boot, target}); !ok {
		t.Fatal("install target was not detected")
	}
}
