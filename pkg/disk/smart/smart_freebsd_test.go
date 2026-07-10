// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package smart

import (
	"errors"
	"testing"
)

func TestClosedDeviceMethodsReturnError(t *testing.T) {
	var nilDevice *Device
	nilDevice.Close()

	d := &Device{device: "closed-test"}
	d.Close()
	checks := []struct {
		name string
		call func() error
	}{
		{"read", func() error { _, err := d.Read(); return err }},
		{"supported", func() error { _, err := d.Supported(); return err }},
		{"enable", d.Enable},
		{"self-test-log", func() error { _, err := d.ReadSelfTestLog(); return err }},
		{"self-test-capabilities", func() error { _, err := d.SelfTestCapabilities(); return err }},
		{"self-test-status", func() error { _, err := d.SelfTestStatus(); return err }},
		{"error-log", func() error { _, err := d.ReadErrorLog(); return err }},
		{"nvme-error-log", func() error { _, err := d.ReadNVMEErrorLog(); return err }},
		{"nvme-identify", func() error { _, err := d.ReadNVMeIdentifyCtrl(); return err }},
		{"nvme-identify-namespace", func() error { _, err := d.ReadNVMeIdentifyNamespace(1); return err }},
		{"sct-status", func() error { _, err := d.ReadSCTStatus(); return err }},
		{"sct-temp-history", func() error { _, err := d.ReadSCTTempHistory(); return err }},
		{"sct-feature", func() error { return d.SetSCTFeatureControl(0, 0, false) }},
		{"sct-erc", func() error { return d.SetSCTErrorRecoveryControl(true, 0) }},
		{"log-directory", func() error { _, err := d.ReadLogDirectory(); return err }},
		{"gpl-log-directory", func() error { _, err := d.ReadGPLLogDirectory(); return err }},
		{"extended-error-log", func() error { _, err := d.ReadExtendedErrorLog(); return err }},
		{"extended-self-test-log", func() error { _, err := d.ReadExtendedSelfTestLog(); return err }},
		{"device-statistics", func() error { _, err := d.ReadDeviceStatistics(); return err }},
		{"selective-self-test-log", func() error { _, err := d.ReadSelectiveSelfTestLog(); return err }},
		{"self-test", func() error { return d.SelfTest(SelfTestShort) }},
		{"vendor-self-test", func() error { return d.StartVendorSelfTest(0x90) }},
		{"abort", d.AbortSelfTest},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, ErrDeviceClosed) {
				t.Fatalf("got %v, want ErrDeviceClosed", err)
			}
		})
	}
}

func TestSelfTestDeviceLockKey(t *testing.T) {
	first := selfTestDeviceLockKey("NVMe", "model", "serial", "revision", "/dev/nda0", true)
	second := selfTestDeviceLockKey("NVMe", "model", "serial", "revision", "/dev/nda1", true)
	if first != second {
		t.Fatalf("controller keys differ: %q %q", first, second)
	}
	if got := selfTestDeviceLockKey("NVMe", "model", "serial", "revision", "/dev/nda1", false); got != "/dev/nda1" {
		t.Fatalf("independent operation key=%q", got)
	}
	if got := selfTestDeviceLockKey("NVMe", "model", "", "revision", "/dev/nda1", true); got != "/dev/nda1" {
		t.Fatalf("missing identity key=%q", got)
	}
}

func TestBytesToUint64(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		bigEndian bool
		want      uint64
	}{
		{
			name:      "little endian short payload",
			input:     []byte{0x34, 0x12},
			bigEndian: false,
			want:      0x1234,
		},
		{
			name:      "big endian short payload",
			input:     []byte{0x12, 0x34},
			bigEndian: true,
			want:      0x1234,
		},
		{
			name:      "little endian 8-byte payload",
			input:     []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			bigEndian: false,
			want:      0x0807060504030201,
		},
		{
			name:      "big endian 8-byte payload",
			input:     []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			bigEndian: true,
			want:      0x0102030405060708,
		},
		{
			name:      "long payload capped at 8 bytes",
			input:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			bigEndian: false,
			want:      0x0807060504030201,
		},
		{
			name:      "empty payload",
			input:     []byte{},
			bigEndian: false,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToUint64(tt.input, tt.bigEndian)
			if got != tt.want {
				t.Fatalf("unexpected value: got 0x%x, want 0x%x", got, tt.want)
			}
		})
	}
}

func TestPowerOnTimeFormats(t *testing.T) {
	raw := make([]byte, 12)
	raw[5], raw[6] = 0xd0, 0x07
	if got := parseRawValue(raw, AttrDef{Format: "min2hour"}, false); got != 33 {
		t.Fatalf("min2hour: got %d, want 33", got)
	}
	if got := parseRawValue(raw, AttrDef{Format: "halfmin2hour"}, false); got != 16 {
		t.Fatalf("halfmin2hour: got %d, want 16", got)
	}
	if got := parseRawValue(raw, AttrDef{Format: "sec2hour"}, false); got != 0 {
		t.Fatalf("sec2hour: got %d, want 0", got)
	}
	if hours, ok := ataPowerOnHours(uint64(123)|(uint64(60000)<<32), AttrDef{Format: "msec24hour32"}); !ok || hours != 123 {
		t.Fatalf("msec24hour32: got (%d, %v), want (123, true)", hours, ok)
	}
	if _, ok := ataPowerOnHours(0x01000000, AttrDef{Format: "raw48"}); ok {
		t.Fatal("accepted implausible power-on hours")
	}
}

func TestFindTemperature(t *testing.T) {
	tests := []struct {
		name       string
		attrs      []Attribute
		modelAttrs map[uint32]AttrDef
		want       int
		wantOk     bool
	}{
		{
			name: "ID_194_primary",
			attrs: []Attribute{
				{ID: 194, RawValue: 0x000000000000002B},
				{ID: 190, RawValue: 0x0000000000000028},
			},
			want: 43, wantOk: true,
		},
		{
			name: "ID_190_fallback",
			attrs: []Attribute{
				{ID: 190, RawValue: 0x0000000000000028},
			},
			want: 40, wantOk: true,
		},
		{
			name: "ID_9_as_temp",
			attrs: []Attribute{
				{ID: 9, RawValue: 0x0000000000000028},
			},
			modelAttrs: map[uint32]AttrDef{9: {Format: "tempminmax"}},
			want:       40, wantOk: true,
		},
		{
			name: "ID_9_filtered_when_byte_above_127",
			attrs: []Attribute{
				{ID: 9, RawValue: 0x0000000000000080},
			},
			modelAttrs: map[uint32]AttrDef{9: {Format: "tempminmax"}},
			want:       0, wantOk: false,
		},
		{
			name: "ID_231_not_temperature_source",
			attrs: []Attribute{
				{ID: 231, RawValue: 0x0000000000000019},
			},
			want: 0, wantOk: false,
		},
		{
			name: "ID_220_last_resort",
			attrs: []Attribute{
				{ID: 220, RawValue: 0x0000000000000021},
			},
			modelAttrs: map[uint32]AttrDef{220: {Format: "tempminmax"}},
			want:       33, wantOk: true,
		},
		{
			name: "no_temp_attrs",
			attrs: []Attribute{
				{ID: 1, RawValue: 0x1234},
				{ID: 5, RawValue: 0x5678},
			},
			want: 0, wantOk: false,
		},
		{
			name: "zero_celsius",
			attrs: []Attribute{
				{ID: 194, RawValue: 0x0000000000000000},
			},
			want: 0, wantOk: false,
		},
		{
			name:  "nil_attrs",
			attrs: nil,
			want:  0, wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findTemperature(tt.attrs, tt.modelAttrs)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("got (%d, %v) want (%d, %v)", got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

func TestParseSelectiveSelfTestLogDoesNotInventProgress(t *testing.T) {
	raw := make([]byte, 512)
	raw[0] = 1
	raw[2] = 10
	raw[10] = 20
	raw[492] = 0x34
	raw[493] = 0x12
	raw[500] = 3
	raw[502] = 0x1a
	raw[508] = 17
	setATAChecksum(raw)

	log := parseSelectiveSelfTestLog(raw)
	if !log.ChecksumValid || log.Revision != 1 {
		t.Fatalf("invalid selective header: %+v", log)
	}
	if log.ProgressPct != 0 || log.InProgress {
		t.Fatalf("selective log fabricated overall progress: %+v", log)
	}
	if log.SelectiveCurrentSpan != 3 || log.SelectiveCurrentLBA != 0x1234 {
		t.Fatalf("current selective position: %+v", log)
	}
	if !log.SelectiveScanEnabled || !log.SelectiveScanActive || !log.SelectiveScanPending || log.SelectivePendingTime != 17 {
		t.Fatalf("selective flags: %+v", log)
	}
}

func TestParseSCSIBackgroundScanPowerOnMinutes(t *testing.T) {
	raw := make([]byte, 24)
	raw[0] = 0x15
	raw[3] = 20
	raw[7] = 16
	raw[8], raw[9], raw[10], raw[11] = 0, 0, 0x1c, 0x20
	for i := 12; i < len(raw); i++ {
		raw[i] = 0xff
	}
	minutes, ok := parseSCSIBackgroundScanPowerOnMinutes(raw)
	if !ok || minutes != 7200 {
		t.Fatalf("got (%d, %v), want (7200, true)", minutes, ok)
	}
	if _, ok := parseSCSILogParamUint64(raw, 0); ok {
		t.Fatal("generic uint64 parser accepted an overflowing 16-byte parameter")
	}
}
