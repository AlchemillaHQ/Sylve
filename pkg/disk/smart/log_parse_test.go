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

func setATAChecksum(sector []byte) {
	sector[511] = 0
	var sum byte
	for _, b := range sector[:511] {
		sum += b
	}
	sector[511] = -sum
}

func putStandardError(raw []byte, index int, marker byte, lba uint32, hours uint16) {
	offset := 2 + index*90
	err := raw[offset+60 : offset+90]
	err[1] = marker
	err[2] = marker + 1
	err[3] = byte(lba)
	err[4] = byte(lba >> 8)
	err[5] = byte(lba >> 16)
	err[6] = 0xaf
	err[7] = 0x51
	binary.LittleEndian.PutUint16(err[28:30], hours)
}

func TestParseATAErrorLogCircularOrder(t *testing.T) {
	raw := make([]byte, 512)
	raw[0] = 1
	raw[1] = 2
	binary.LittleEndian.PutUint16(raw[452:454], 2)
	putStandardError(raw, 0, 0x10, 0x010203, 10)
	putStandardError(raw, 1, 0x20, 0x332211, 0x1234)
	setATAChecksum(raw)

	log := parseATAErrorLog(raw)
	if !log.ChecksumValid || log.ErrorCount != 2 || len(log.Entries) != 2 {
		t.Fatalf("unexpected header: %+v", log)
	}
	if got := log.Entries[0]; got.Error != 0x20 || got.LBA != 0x332211 || got.LifetimeHours != 0x1234 {
		t.Fatalf("newest entry decoded incorrectly: %+v", got)
	}
	if got := log.Entries[1]; got.Error != 0x10 || got.LBA != 0x010203 {
		t.Fatalf("older entry decoded incorrectly: %+v", got)
	}
}

func TestParseSamsungFirmwareCorrections(t *testing.T) {
	raw := make([]byte, 512)
	raw[0], raw[1] = 1, 1
	raw[452], raw[453] = 0, 1
	putStandardError(raw, 0, 0x20, 0x123456, 0x3412)
	err := raw[2+60 : 2+90]
	err[28], err[29] = 0x12, 0x34
	setATAChecksum(raw)
	log := parseATAErrorLogWithBugs(raw, FirmwareBugSamsung)
	if log.ErrorCount != 1 || len(log.Entries) != 1 || log.Entries[0].LifetimeHours != 0x1234 {
		t.Fatalf("Samsung error-log fix: %+v", log)
	}

	selfTest := make([]byte, 512)
	binary.LittleEndian.PutUint16(selfTest[:2], 1)
	selfTest[509] = 1
	entry := selfTest[2:26]
	entry[0] = 0x70
	entry[1] = SelfTestExtended
	setATAChecksum(selfTest)
	selfLog := parseATAStandardSelfTestLogWithBugs(selfTest, FirmwareBugSamsung)
	if len(selfLog.Entries) != 1 || selfLog.Entries[0].Type != "extended" || selfLog.Entries[0].Status != "failed_read" {
		t.Fatalf("Samsung self-test fix: %+v", selfLog)
	}
}

func TestParseATALogDirectory(t *testing.T) {
	raw := make([]byte, 512)
	binary.LittleEndian.PutUint16(raw[2:4], 1)
	binary.LittleEndian.PutUint16(raw[2+6*2:2+7*2], 19)
	got := parseATALogDirectory(raw)
	if len(got) != 2 || got[0] != 0x01 || got[1] != 0x07 {
		t.Fatalf("directory addresses: %#v", got)
	}
	if got := parseATALogDirectory(raw[:511]); got != nil {
		t.Fatalf("short directory should be rejected: %#v", got)
	}
}

func putExtendedError(raw []byte, index int, marker byte, lba uint64, hours uint16) {
	page, pageEntry := index/4, index%4
	offset := page*512 + 4 + pageEntry*124
	entry := raw[offset : offset+124]
	for i := 72; i < 90; i++ {
		entry[i] = 0xee
	}
	err := entry[90:124]
	err[1] = marker
	err[2] = 0x34
	err[3] = 0x12
	err[4] = byte(lba)
	err[6] = byte(lba >> 8)
	err[8] = byte(lba >> 16)
	err[5] = byte(lba >> 24)
	err[7] = byte(lba >> 32)
	err[9] = byte(lba >> 40)
	err[10] = 0x40
	err[11] = 0x51
	binary.LittleEndian.PutUint16(err[32:34], hours)
}

func TestParseATAExtendedErrorLogOffsetsAndWrap(t *testing.T) {
	raw := make([]byte, 2*512)
	raw[0] = 1
	binary.LittleEndian.PutUint16(raw[2:4], 5)
	binary.LittleEndian.PutUint16(raw[500:502], 3)
	putExtendedError(raw, 4, 0x44, 0x665544332211, 0x1234)
	putExtendedError(raw, 3, 0x33, 0x010203040506, 0x2345)
	putExtendedError(raw, 2, 0x22, 0x111213141516, 0x3456)
	setATAChecksum(raw[:512])
	setATAChecksum(raw[512:])

	log := parseATAExtendedErrorLog(raw)
	if !log.ChecksumValid || log.ErrorCount != 3 || len(log.Entries) != 3 {
		t.Fatalf("unexpected header: %+v", log)
	}
	got := log.Entries[0]
	if got.Error != 0x44 || got.LBA != 0x665544332211 || got.LifetimeHours != 0x1234 || got.SectorCount16 != 0x1234 {
		t.Fatalf("extended completion registers decoded incorrectly: %+v", got)
	}
	if log.Entries[1].Error != 0x33 || log.Entries[2].Error != 0x22 {
		t.Fatalf("wrong circular order: %+v", log.Entries)
	}
}

func TestParseATAExtendedErrorLogXErrorLBA(t *testing.T) {
	raw := make([]byte, 512)
	raw[0] = 1
	binary.LittleEndian.PutUint16(raw[2:4], 1)
	binary.LittleEndian.PutUint16(raw[500:502], 1)
	putExtendedError(raw, 0, 1, 0x665544332211, 1)
	setATAChecksum(raw)
	log := parseATAExtendedErrorLogWithBugs(raw, FirmwareBugXErrorLBA)
	if len(log.Entries) != 1 || log.Entries[0].LBA != 0x663355224411 {
		t.Fatalf("xerrorlba correction: %+v", log.Entries)
	}
}

func TestParseATASelfTestLogsCircularOrder(t *testing.T) {
	standard := make([]byte, 512)
	binary.LittleEndian.PutUint16(standard[0:2], 1)
	standard[508] = 2
	standard[2] = SelfTestShort
	standard[3] = 0x00
	standard[26] = SelfTestExtended
	standard[27] = 0x70
	binary.LittleEndian.PutUint16(standard[28:30], 321)
	binary.LittleEndian.PutUint32(standard[31:35], 0x12345678)
	setATAChecksum(standard)
	log := parseATAStandardSelfTestLog(standard)
	if len(log.Entries) != 2 || log.Entries[0].Type != "extended" || log.Entries[0].LBA != 0x12345678 {
		t.Fatalf("standard self-test order/fields: %+v", log.Entries)
	}

	extended := make([]byte, 2*512)
	extended[0] = 1
	binary.LittleEndian.PutUint16(extended[2:4], 20)
	latest := extended[512+4 : 512+4+26]
	latest[0] = SelfTestExtended
	latest[1] = 0x50
	latest[5], latest[6], latest[7] = 1, 2, 3
	latest[8], latest[9], latest[10] = 4, 5, 6
	older := extended[4+18*26 : 4+19*26]
	older[0] = SelfTestShort
	setATAChecksum(extended[:512])
	setATAChecksum(extended[512:])
	extLog := parseATAExtendedSelfTestLog(extended)
	if len(extLog.Entries) != 2 || extLog.Entries[0].LBA != 0x060504030201 || extLog.Entries[1].Type != "short" {
		t.Fatalf("extended self-test order/fields: %+v", extLog.Entries)
	}
}

func TestParseATASelfTestLogProgress(t *testing.T) {
	standard := make([]byte, 512)
	binary.LittleEndian.PutUint16(standard[0:2], 1)
	standard[508] = 1
	standard[2] = SelfTestShort
	standard[3] = 0xf3
	setATAChecksum(standard)
	standardLog := parseATAStandardSelfTestLog(standard)
	if !standardLog.InProgress || standardLog.CurrentType != "short" || !standardLog.ProgressKnown || standardLog.ProgressPct != 70 {
		t.Fatalf("standard log: %+v", standardLog)
	}

	extended := make([]byte, 512)
	extended[0] = 1
	binary.LittleEndian.PutUint16(extended[2:4], 1)
	extended[4] = SelfTestExtended
	extended[5] = 0xf8
	setATAChecksum(extended)
	extendedLog := parseATAExtendedSelfTestLog(extended)
	if !extendedLog.InProgress || extendedLog.CurrentType != "extended" || !extendedLog.ProgressKnown || extendedLog.ProgressPct != 20 {
		t.Fatalf("extended log: %+v", extendedLog)
	}
}

func TestParseATAVendorSelfTestTypes(t *testing.T) {
	for _, code := range []byte{0x40, 0x7e, 0x90, 0xff} {
		entry := parseATASelfTestEntry([]byte{code, 0})
		if entry.Type != "vendor_specific" {
			t.Fatalf("code %#x: %+v", code, entry)
		}
	}
	for _, code := range []byte{0x05, 0x80, 0x85, 0x8f} {
		entry := parseATASelfTestEntry([]byte{code, 0})
		if entry.Type != "unknown" {
			t.Fatalf("code %#x: %+v", code, entry)
		}
	}
}

func TestParseATASelfTestModes(t *testing.T) {
	tests := []struct {
		code     byte
		wantType string
		wantMode string
	}{
		{code: 0x00, wantType: "offline", wantMode: "offline"},
		{code: 0x01, wantType: "short", wantMode: "offline"},
		{code: 0x02, wantType: "extended", wantMode: "offline"},
		{code: 0x03, wantType: "conveyance", wantMode: "offline"},
		{code: 0x04, wantType: "selective", wantMode: "offline"},
		{code: 0x7f, wantType: "abort", wantMode: "offline"},
		{code: 0x81, wantType: "short_captive", wantMode: "captive"},
		{code: 0x82, wantType: "extended_captive", wantMode: "captive"},
		{code: 0x83, wantType: "conveyance_captive", wantMode: "captive"},
		{code: 0x84, wantType: "selective_captive", wantMode: "captive"},
	}
	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			entry := parseATASelfTestEntry([]byte{tt.code, 0})
			if entry.Type != tt.wantType || entry.Mode != tt.wantMode {
				t.Fatalf("entry=%+v", entry)
			}
		})
	}
}

func TestParseNVMeAndSCSISelfTestLogs(t *testing.T) {
	nvme := make([]byte, 4+28)
	nvme[0] = 1
	nvme[1] = 37
	entry := nvme[4:]
	entry[0] = 0x20
	entry[2] = 0x03
	binary.LittleEndian.PutUint64(entry[4:12], 99)
	binary.LittleEndian.PutUint32(entry[12:16], 7)
	binary.LittleEndian.PutUint64(entry[16:24], 0x1234)
	nvmeLog := parseNVMESelfTestLog(nvme)
	if !nvmeLog.InProgress || !nvmeLog.ProgressKnown || nvmeLog.CurrentType != "short" || nvmeLog.ProgressPct != 37 || len(nvmeLog.Entries) != 1 {
		t.Fatalf("NVMe log: %+v", nvmeLog)
	}
	if got := nvmeLog.Entries[0]; got.Protocol != "NVMe" || got.Type != "extended" || got.Outcome != SelfTestOutcomePassed || !got.NSIDValid || got.NSID != 7 || !got.LBAValid || got.LBA != 0x1234 || got.LifetimeHours != 99 {
		t.Fatalf("NVMe entry: %+v", got)
	}

	scsi := make([]byte, 404)
	scsi[0] = 0x10
	binary.BigEndian.PutUint16(scsi[2:4], 400)
	scsi[4], scsi[5] = 0, 1
	scsi[8] = 1 << 5
	scsi[9] = 3
	binary.BigEndian.PutUint16(scsi[10:12], 123)
	binary.BigEndian.PutUint64(scsi[12:20], 0x9876)
	scsi[23] = 0xff
	scsiLog := parseSCSISelfTestLog(scsi)
	if !scsiLog.ChecksumValid || len(scsiLog.Entries) != 1 {
		t.Fatalf("SCSI log: %+v", scsiLog)
	}
	if got := scsiLog.Entries[0]; got.Protocol != "SCSI" || got.Type != "short" || got.Mode != "background" || got.Outcome != SelfTestOutcomePassed || got.LifetimeHours != 123 || got.LBA != 0x9876 {
		t.Fatalf("SCSI entry: %+v", got)
	}
}

func TestParseSCSISelfTestLogValidation(t *testing.T) {
	valid := make([]byte, 404)
	valid[0] = 0x10
	binary.BigEndian.PutUint16(valid[2:4], 400)
	if log := parseSCSISelfTestLog(valid); !log.ChecksumValid {
		t.Fatalf("valid log rejected: %+v", log)
	}

	tests := map[string][]byte{
		"short":        append([]byte(nil), valid[:403]...),
		"wrong_page":   append([]byte(nil), valid...),
		"wrong_length": append([]byte(nil), valid...),
	}
	tests["wrong_page"][0] = 0x11
	binary.BigEndian.PutUint16(tests["wrong_length"][2:4], 399)
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			log := parseSCSISelfTestLog(raw)
			if log.ChecksumValid || len(log.Entries) != 0 {
				t.Fatalf("malformed log accepted: %+v", log)
			}
		})
	}
}

func TestParseSCSILBAValidity(t *testing.T) {
	for _, result := range []byte{1, 2, 3, 7, 8, 14} {
		raw := make([]byte, 20)
		raw[4] = result
		binary.BigEndian.PutUint64(raw[8:16], 123)
		entry := parseSCSISelfTestEntry(raw)
		if !entry.LBAValid {
			t.Fatalf("result %#x: %+v", result, entry)
		}
	}
	for _, result := range []byte{0, 15} {
		raw := make([]byte, 20)
		raw[4] = result
		binary.BigEndian.PutUint64(raw[8:16], 123)
		entry := parseSCSISelfTestEntry(raw)
		if entry.LBAValid {
			t.Fatalf("result %#x: %+v", result, entry)
		}
	}
	raw := make([]byte, 20)
	raw[4] = 1
	binary.BigEndian.PutUint64(raw[8:16], ^uint64(0))
	if entry := parseSCSISelfTestEntry(raw); entry.LBAValid {
		t.Fatalf("sentinel LBA accepted: %+v", entry)
	}
}

func TestParseSCSISelfTestEntryPreservesParameterAndVendorData(t *testing.T) {
	raw := make([]byte, 20)
	binary.BigEndian.PutUint16(raw[0:2], 0x1234)
	raw[4] = 1 << 5
	raw[19] = 0xab
	entry := parseSCSISelfTestEntry(raw)
	if entry.ParameterCode != 0x1234 || entry.VendorSpecific != 0xab {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestParseNVMeErrorLog(t *testing.T) {
	raw := make([]byte, 2*64)
	entry := raw[64:]
	binary.LittleEndian.PutUint64(entry[0:8], 99)
	binary.LittleEndian.PutUint16(entry[8:10], 7)
	binary.LittleEndian.PutUint16(entry[10:12], 8)
	binary.LittleEndian.PutUint16(entry[12:14], 9)
	binary.LittleEndian.PutUint16(entry[14:16], 10)
	binary.LittleEndian.PutUint64(entry[16:24], 0x12345678)
	binary.LittleEndian.PutUint32(entry[24:28], 11)

	log := parseNVMeErrorLog(raw)
	if log.Capacity != 2 || len(log.Entries) != 1 {
		t.Fatalf("NVMe error log header: %+v", log)
	}
	got := log.Entries[0]
	if got.ErrorCount != 99 || got.SQID != 7 || got.CommandID != 8 || got.StatusField != 9 ||
		got.ParamError != 10 || got.LBA != 0x12345678 || got.NamespaceID != 11 {
		t.Fatalf("NVMe error entry: %+v", got)
	}
}

func TestSelfTestEntryParsersRejectShortInput(t *testing.T) {
	for length := 0; length < 3; length++ {
		raw := make([]byte, length)
		_ = parseATASelfTestEntry(raw)
		_ = parseNVMESelfTestEntry(raw)
		_ = parseSCSISelfTestEntry(raw)
	}
}

func BenchmarkParseATAExtendedErrorLog(b *testing.B) {
	raw := make([]byte, 512)
	raw[0] = 1
	binary.LittleEndian.PutUint16(raw[2:4], 4)
	binary.LittleEndian.PutUint16(raw[500:502], 4)
	for i := 0; i < 4; i++ {
		putExtendedError(raw, i, byte(i+1), uint64(i+1), uint16(i+1))
	}
	setATAChecksum(raw)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = parseATAExtendedErrorLog(raw)
	}
}
