// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"testing"
)

type testDownload struct {
	ID  uint `gorm:"primaryKey"`
	URL string
}

func TestGetValNil(t *testing.T) {
	got := GetVal(nil)
	if got != 0 {
		t.Fatalf("expected 0 for nil pointer, got %d", got)
	}
}

func TestGetValNonNil(t *testing.T) {
	id := uint(42)

	got := GetVal(&id)
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}
