// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import "testing"

func TestAtaAttrStateMissingThreshold(t *testing.T) {
	got := AtaAttrState(100, 100, -1, nil)
	if got != AttrStateNoThreshold {
		t.Fatalf("got %d want %d", got, AttrStateNoThreshold)
	}
}
