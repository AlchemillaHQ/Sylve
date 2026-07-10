// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import (
	"encoding/binary"
	"testing"
)

func TestParseSCSIInformationalExceptionUsesParameterZero(t *testing.T) {
	raw := make([]byte, 22)
	raw[0] = 0x2f
	binary.BigEndian.PutUint16(raw[2:4], uint16(len(raw)-4))
	raw[7] = 5
	raw[8] = 0
	raw[9] = 0
	raw[10] = 35
	raw[11] = 70
	raw[12] = 45
	raw[13] = 0
	raw[14] = 1
	raw[16] = 4
	raw[17] = 0x5d
	raw[18] = 0x53
	info, ok := parseSCSIInformationalException(raw)
	if !ok || info.ASC != 0 || info.ASCQ != 0 || !info.CurrentTemperatureKnown || info.CurrentTemperature != 35 || !info.TripTemperatureKnown || info.TripTemperature != 70 {
		t.Fatalf("information: %+v valid=%v", info, ok)
	}
	if !scsiHealthNeedsRequestSense(info, ok) {
		t.Fatal("clean parameter did not request sense fallback")
	}
}

func TestParseSCSIInformationalExceptionRejectsInvalidInput(t *testing.T) {
	raw := make([]byte, 12)
	raw[0] = 0x2f
	binary.BigEndian.PutUint16(raw[2:4], 8)
	raw[5] = 1
	raw[7] = 4
	if _, ok := parseSCSIInformationalException(raw); ok {
		t.Fatal("nonzero parameter code accepted")
	}
	for size := 0; size < 12; size++ {
		_, _ = parseSCSIInformationalException(raw[:size])
	}
}

func TestParseSCSISenseCode(t *testing.T) {
	fixed := make([]byte, 18)
	fixed[0] = 0x70
	fixed[12] = 0x5d
	fixed[13] = 0x10
	asc, ascq, ok := parseSCSISenseCode(fixed)
	if !ok || asc != 0x5d || ascq != 0x10 {
		t.Fatalf("fixed: asc=%#x ascq=%#x valid=%v", asc, ascq, ok)
	}
	descriptor := []byte{0x72, 0, 0x0b, 0x06}
	asc, ascq, ok = parseSCSISenseCode(descriptor)
	if !ok || asc != 0x0b || ascq != 0x06 {
		t.Fatalf("descriptor: asc=%#x ascq=%#x valid=%v", asc, ascq, ok)
	}
	asc, ascq, ok = parseSCSISenseCode(make([]byte, 18))
	if !ok || asc != 0 || ascq != 0 {
		t.Fatalf("empty: asc=%#x ascq=%#x valid=%v", asc, ascq, ok)
	}
	if _, _, ok := parseSCSISenseCode([]byte{0x70}); ok {
		t.Fatal("truncated fixed sense accepted")
	}
	if _, _, ok := parseSCSISenseCode([]byte{0}); ok {
		t.Fatal("truncated empty sense accepted")
	}
}

func TestParseSCSIPowerMode(t *testing.T) {
	tests := []struct {
		ascq uint8
		mode SCSIPowerMode
	}{
		{0x00, SCSIPowerModeLowPower},
		{0x01, SCSIPowerModeIdle},
		{0x02, SCSIPowerModeStandby},
		{0x03, SCSIPowerModeIdle},
		{0x04, SCSIPowerModeStandby},
		{0x05, SCSIPowerModeIdle},
		{0x06, SCSIPowerModeIdle},
		{0x07, SCSIPowerModeIdle},
		{0x08, SCSIPowerModeIdle},
		{0x09, SCSIPowerModeStandbyY},
		{0x0a, SCSIPowerModeStandbyY},
		{0x41, SCSIPowerModeActive},
		{0x42, SCSIPowerModeIdle},
		{0x43, SCSIPowerModeStandby},
		{0x45, SCSIPowerModeSleep},
		{0x47, SCSIPowerModeUnknown},
	}
	for _, test := range tests {
		fixed := make([]byte, 18)
		fixed[0] = 0x70
		fixed[12] = 0x5e
		fixed[13] = test.ascq
		mode, ok := parseSCSIPowerMode(fixed)
		if !ok || mode != test.mode {
			t.Fatalf("fixed ascq=%#x mode=%v valid=%v", test.ascq, mode, ok)
		}
		descriptor := []byte{0x72, 0, 0x5e, test.ascq}
		mode, ok = parseSCSIPowerMode(descriptor)
		if !ok || mode != test.mode {
			t.Fatalf("descriptor ascq=%#x mode=%v valid=%v", test.ascq, mode, ok)
		}
	}

	active := make([]byte, 18)
	active[0] = 0x70
	mode, ok := parseSCSIPowerMode(active)
	if !ok || mode != SCSIPowerModeActive {
		t.Fatalf("active mode=%v valid=%v", mode, ok)
	}
	if _, ok := parseSCSIPowerMode([]byte{0x70}); ok {
		t.Fatal("truncated sense accepted")
	}
}

func TestSCSIHealthFromCode(t *testing.T) {
	tests := []struct {
		asc    uint8
		ascq   uint8
		known  bool
		passed bool
	}{
		{0, 0, true, true},
		{0x0b, 0x06, true, false},
		{0x0b, 0x15, false, false},
		{0x5d, 0x00, true, false},
		{0x5d, 0x1d, true, false},
		{0x5d, 0x53, true, false},
		{0x5d, 0x5d, false, false},
		{0x5d, 0x73, true, false},
		{0x04, 0x09, false, false},
	}
	for _, test := range tests {
		known, passed := scsiHealthFromCode(test.asc, test.ascq)
		if known != test.known || passed != test.passed {
			t.Fatalf("asc=%#x ascq=%#x: known=%v passed=%v", test.asc, test.ascq, known, passed)
		}
	}
}

func BenchmarkParseSCSIInformationalException(b *testing.B) {
	raw := make([]byte, 12)
	raw[0] = 0x2f
	binary.BigEndian.PutUint16(raw[2:4], 8)
	raw[7] = 4
	for b.Loop() {
		_, _ = parseSCSIInformationalException(raw)
	}
}
