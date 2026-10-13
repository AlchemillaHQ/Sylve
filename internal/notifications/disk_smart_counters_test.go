// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import "testing"

func TestParseDiskSmartCounter(t *testing.T) {
	for _, value := range []string{"0", "0004", "9223372036854775808", "340282366920938463463374607431768211455"} {
		if _, valid := ParseDiskSmartCounter(value); !valid {
			t.Fatalf("rejected unsigned counter %q", value)
		}
	}
	for _, value := range []string{"", "-1", "+1", "1.5", "1e3", " 4", "4 ", "invalid", "340282366920938463463374607431768211456"} {
		if _, valid := ParseDiskSmartCounter(value); valid {
			t.Fatalf("accepted invalid counter %q", value)
		}
	}
}
