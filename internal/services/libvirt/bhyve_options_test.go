// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"reflect"
	"testing"
)

func TestNormalizeExtraBhyveOptions_NormalizesAndPreservesOrder(t *testing.T) {
	input := []string{
		"  -S  ",
		"-u\n\n -A ",
		" \n\t",
		"\n -B \r\n",
	}

	got := normalizeExtraBhyveOptions(input)
	want := []string{"-S", "-u", "-A", "-B"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected normalized options: got=%#v want=%#v", got, want)
	}
}

func TestNormalizeExtraBhyveOptions_EmptyInputReturnsEmptySlice(t *testing.T) {
	got := normalizeExtraBhyveOptions(nil)
	if got == nil {
		t.Fatalf("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got len=%d", len(got))
	}
}
