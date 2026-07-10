// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import "testing"

func TestDecodeSelfTestExecStatus(t *testing.T) {
	tests := []struct {
		name          string
		raw           uint64
		wantStatus    string
		wantRemainPct int
	}{
		{name: "completed", raw: 0x00, wantStatus: "completed", wantRemainPct: 0},
		{name: "aborted_by_host", raw: 0x10, wantStatus: "aborted_by_host", wantRemainPct: 0},
		{name: "aborted_50pct", raw: 0x15, wantStatus: "aborted_by_host", wantRemainPct: 50},
		{name: "interrupted", raw: 0x20, wantStatus: "interrupted", wantRemainPct: 0},
		{name: "fatal", raw: 0x30, wantStatus: "fatal", wantRemainPct: 0},
		{name: "failed_unknown", raw: 0x40, wantStatus: "failed_unknown", wantRemainPct: 0},
		{name: "failed_electrical", raw: 0x50, wantStatus: "failed_electrical", wantRemainPct: 0},
		{name: "failed_servo", raw: 0x60, wantStatus: "failed_servo", wantRemainPct: 0},
		{name: "failed_read", raw: 0x70, wantStatus: "failed_read", wantRemainPct: 0},
		{name: "failed_handling", raw: 0x80, wantStatus: "failed_handling", wantRemainPct: 0},
		{name: "in_progress", raw: 0xF0, wantStatus: "in_progress", wantRemainPct: 0},
		{name: "in_progress_90pct", raw: 0xF9, wantStatus: "in_progress", wantRemainPct: 90},
		{name: "in_progress_unknown_pct", raw: 0xFF, wantStatus: "in_progress", wantRemainPct: -1},
		{name: "completed_50pct", raw: 0x05, wantStatus: "completed", wantRemainPct: 50},
		{name: "reserved_nibble_A", raw: 0xA0, wantStatus: "reserved", wantRemainPct: 0},
		{name: "completed_100pct", raw: 0x0A, wantStatus: "completed", wantRemainPct: -1},
		{name: "completed_20pct", raw: 0x02, wantStatus: "completed", wantRemainPct: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecodeSelfTestExecStatus(tt.raw)
			if got.Status != tt.wantStatus {
				t.Errorf("Status: got %q want %q (raw=0x%02X)", got.Status, tt.wantStatus, tt.raw)
			}
			if got.RemainingPct != tt.wantRemainPct {
				t.Errorf("RemainingPct: got %d want %d (raw=0x%02X)", got.RemainingPct, tt.wantRemainPct, tt.raw)
			}
		})
	}
}

func TestAtaAttrState(t *testing.T) {
	tests := []struct {
		name      string
		current   int
		worst     int
		threshold int
		def       *AttrDef
		want      int
	}{
		{name: "threshold_zero_OK", current: 100, worst: 100, threshold: 0, def: nil, want: AttrStateOK},
		{name: "OK_normal", current: 100, worst: 90, threshold: 10, def: nil, want: AttrStateOK},
		{name: "OK_at_threshold", current: 11, worst: 11, threshold: 10, def: nil, want: AttrStateOK},
		{name: "FAILED_NOW", current: 5, worst: 10, threshold: 10, def: nil, want: AttrStateFailedNow},
		{name: "FAILED_NOW_equal", current: 10, worst: 10, threshold: 10, def: nil, want: AttrStateFailedNow},
		{name: "FAILED_PAST", current: 50, worst: 5, threshold: 10, def: nil, want: AttrStateFailedPast},
		{name: "FAILED_PAST_equal", current: 50, worst: 10, threshold: 10, def: nil, want: AttrStateFailedPast},
		{name: "NoWorstVal_suppresses_FailedPast", current: 100, worst: 5, threshold: 10,
			def: &AttrDef{NoWorstVal: true}, want: AttrStateOK},
		{name: "NoWorstVal_false_behaves_normally", current: 100, worst: 5, threshold: 10,
			def: &AttrDef{NoWorstVal: false}, want: AttrStateFailedPast},
		{name: "NoNormVal_returns_NoNormVal", current: 50, worst: 50, threshold: 10,
			def: &AttrDef{NoNormVal: true}, want: AttrStateNoNormVal},
		{name: "NoNormVal_even_with_threshold_zero", current: 50, worst: 50, threshold: 0,
			def: &AttrDef{NoNormVal: true}, want: AttrStateNoNormVal},
		{name: "nil_def_FailedPast", current: 100, worst: 5, threshold: 10, def: nil, want: AttrStateFailedPast},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AtaAttrState(tt.current, tt.worst, tt.threshold, tt.def)
			if got != tt.want {
				noWorstVal := tt.def != nil && tt.def.NoWorstVal
				noNormVal := tt.def != nil && tt.def.NoNormVal
				t.Errorf("got %d want %d (cur=%d worst=%d thresh=%d NoWorstVal=%v NoNormVal=%v)",
					got, tt.want, tt.current, tt.worst, tt.threshold, noWorstVal, noNormVal)
			}
		})
	}
}

func TestAtaAttrStateConstants(t *testing.T) {
	states := map[int]bool{
		AttrStateOK:          true,
		AttrStateFailedNow:   true,
		AttrStateFailedPast:  true,
		AttrStateNoThreshold: true,
		AttrStateNonExisting: true,
		AttrStateNoNormVal:   true,
	}
	if len(states) != 6 {
		t.Errorf("expected 6 distinct state constants, got %d", len(states))
	}
	if AttrStateOK != 0 {
		t.Errorf("AttrStateOK expected 0, got %d", AttrStateOK)
	}
	if AttrStateNoNormVal != 5 {
		t.Errorf("AttrStateNoNormVal expected 5, got %d", AttrStateNoNormVal)
	}
}
