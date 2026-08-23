// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import "testing"

func TestDiskSmartSelfTestKind(t *testing.T) {
	kind := KindForDiskSmart(DiskSmartSelfTestKindPrefix, " DA0 ")
	if kind != "system.disk.smart.selftest.da0" {
		t.Fatalf("unexpected_kind=%q", kind)
	}
	prefix, disk, ok := DiskNameFromSmartKind(kind)
	if !ok || prefix != DiskSmartSelfTestKindPrefix || disk != "da0" {
		t.Fatalf("unexpected_parse prefix=%q disk=%q ok=%t", prefix, disk, ok)
	}
	if !IsDiskSmartKind(kind) {
		t.Fatalf("self_test_kind_not_recognized=%q", kind)
	}
}
