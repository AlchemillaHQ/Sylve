// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd && cgo

package sysctl

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestFormatKelvin(t *testing.T) {
	cases := []struct {
		name   string
		value  int32
		format string
		want   float64
	}{
		{"deci-kelvin default precision", 3032, "IK", 30.05},
		{"explicit precision 2", 30050, "IK2", 27.35},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatKelvin(tc.value, tc.format)
			if !strings.HasSuffix(got, "C") {
				t.Fatalf("formatKelvin(%d, %q) = %q, expected a Celsius suffix", tc.value, tc.format, got)
			}

			parsed, err := strconv.ParseFloat(strings.TrimSuffix(got, "C"), 64)
			if err != nil {
				t.Fatalf("failed to parse %q: %v", got, err)
			}

			if math.Abs(parsed-tc.want) > 0.1 {
				t.Fatalf("formatKelvin(%d, %q) = %q, want ~%.2fC", tc.value, tc.format, got, tc.want)
			}
		})
	}
}
