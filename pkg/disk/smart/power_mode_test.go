// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package smart

import "testing"

func TestATAPowerMode(t *testing.T) {
	tests := []struct {
		mode     ATAPowerMode
		name     string
		lowPower bool
	}{
		{ATAPowerModeSleep, "sleep", true},
		{ATAPowerModeStandby, "standby", true},
		{ATAPowerModeStandbyY, "standby_y", true},
		{ATAPowerModeActive, "active", false},
		{ATAPowerModeActiveAlt, "active", false},
		{ATAPowerModeIdle, "idle", false},
		{ATAPowerModeIdleA, "idle_a", false},
		{ATAPowerModeIdleB, "idle_b", false},
		{ATAPowerModeIdleC, "idle_c", false},
		{ATAPowerModeActiveOrIdle, "active_or_idle", false},
		{ATAPowerModeUnknown, "unknown", false},
		{ATAPowerMode(0x12), "unknown", false},
	}
	for _, test := range tests {
		if got := test.mode.String(); got != test.name {
			t.Fatalf("mode=%d name=%q", test.mode, got)
		}
		if got := test.mode.IsStandbyOrSleeping(); got != test.lowPower {
			t.Fatalf("mode=%d lowPower=%v", test.mode, got)
		}
	}
}

func TestSCSIPowerMode(t *testing.T) {
	tests := []struct {
		mode     SCSIPowerMode
		name     string
		lowPower bool
	}{
		{SCSIPowerModeActive, "active", false},
		{SCSIPowerModeLowPower, "low_power", true},
		{SCSIPowerModeIdle, "idle", false},
		{SCSIPowerModeStandby, "standby", true},
		{SCSIPowerModeStandbyY, "standby_y", true},
		{SCSIPowerModeSleep, "sleep", true},
		{SCSIPowerModeUnknown, "unknown", false},
		{SCSIPowerMode(100), "unknown", false},
	}
	for _, test := range tests {
		if got := test.mode.String(); got != test.name {
			t.Fatalf("mode=%d name=%q", test.mode, got)
		}
		if got := test.mode.IsStandbyOrSleeping(); got != test.lowPower {
			t.Fatalf("mode=%d lowPower=%v", test.mode, got)
		}
	}
}
