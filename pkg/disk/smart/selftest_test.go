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
	"errors"
	"testing"
)

func TestValidateSelfTestStart(t *testing.T) {
	capabilities := SelfTestCapabilities{Short: true}
	if err := validateSelfTestStart(capabilities, SelfTestStatus{State: SelfTestStateIdle}, SelfTestKindShort); err != nil {
		t.Fatal(err)
	}
	if err := validateSelfTestStart(capabilities, SelfTestStatus{State: SelfTestStateRunning, Running: true}, SelfTestKindShort); !errors.Is(err, ErrSelfTestInProgress) {
		t.Fatalf("running: %v", err)
	}
	if err := validateSelfTestStart(capabilities, SelfTestStatus{State: SelfTestStateAmbiguous}, SelfTestKindShort); !errors.Is(err, ErrSelfTestInProgress) {
		t.Fatalf("ambiguous: %v", err)
	}
	if err := validateSelfTestStart(capabilities, SelfTestStatus{State: SelfTestStateIdle}, SelfTestKindExtended); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestBuildATASelectiveSelfTestLog(t *testing.T) {
	raw := make([]byte, 512)
	raw[400] = 0x5a
	binary.LittleEndian.PutUint16(raw[502:504], 0x001a)
	configured, err := buildATASelectiveSelfTestLog(raw, []SelectiveSpan{{Start: 10, End: 20}, {Start: 30, End: 40}}, SelectiveSelfTestOptions{ScanAfter: true, PendingTimeMinutes: 15}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(configured[0:2]) != 1 || binary.LittleEndian.Uint64(configured[2:10]) != 10 || binary.LittleEndian.Uint64(configured[10:18]) != 20 || binary.LittleEndian.Uint64(configured[18:26]) != 30 || binary.LittleEndian.Uint64(configured[26:34]) != 40 {
		t.Fatalf("spans: %x", configured[:82])
	}
	if configured[400] != 0x5a || binary.LittleEndian.Uint64(configured[492:500]) != 0 || binary.LittleEndian.Uint16(configured[500:502]) != 0 || binary.LittleEndian.Uint16(configured[502:504]) != 0x0002 || binary.LittleEndian.Uint16(configured[508:510]) != 14 {
		t.Fatalf("state: flags=%#x pending=%d", binary.LittleEndian.Uint16(configured[502:504]), binary.LittleEndian.Uint16(configured[508:510]))
	}
	var checksum byte
	for _, value := range configured {
		checksum += value
	}
	if checksum != 0 {
		t.Fatalf("checksum: %#x", checksum)
	}
	if _, err := buildATASelectiveSelfTestLog(raw, nil, SelectiveSelfTestOptions{}, 100); !errors.Is(err, ErrInvalidSelectiveSpan) {
		t.Fatalf("empty spans: %v", err)
	}
	if _, err := buildATASelectiveSelfTestLog(raw, []SelectiveSpan{{Start: 2, End: 1}}, SelectiveSelfTestOptions{}, 100); !errors.Is(err, ErrInvalidSelectiveSpan) {
		t.Fatalf("invalid range: %v", err)
	}
	if _, err := buildATASelectiveSelfTestLog(raw, []SelectiveSpan{{Start: 99, End: 100}}, SelectiveSelfTestOptions{}, 100); !errors.Is(err, ErrInvalidSelectiveSpan) {
		t.Fatalf("out of range: %v", err)
	}
	if _, err := buildATASelectiveSelfTestLog(raw, []SelectiveSpan{{Start: 0, End: 0}}, SelectiveSelfTestOptions{}, 0); !errors.Is(err, ErrDeviceCapacityUnknown) {
		t.Fatalf("unknown capacity: %v", err)
	}
}

func TestSelfTestKindCodes(t *testing.T) {
	tests := []struct {
		kind SelfTestKind
		code uint8
	}{
		{SelfTestKindOffline, SelfTestOffline},
		{SelfTestKindShort, SelfTestShort},
		{SelfTestKindExtended, SelfTestExtended},
		{SelfTestKindConveyance, SelfTestConveyance},
		{SelfTestKindSelective, SelfTestSelective},
		{SelfTestKindShortCaptive, SelfTestShortCaptive},
		{SelfTestKindExtendedCaptive, SelfTestExtendedCaptive},
		{SelfTestKindConveyanceCaptive, SelfTestConveyanceCaptive},
		{SelfTestKindSelectiveCaptive, SelfTestSelectiveCaptive},
	}
	for _, tt := range tests {
		code, ok := selfTestCode(tt.kind)
		if !ok || code != tt.code {
			t.Fatalf("kind %q: code=0x%02x ok=%v", tt.kind, code, ok)
		}
		kind, ok := selfTestKindFromCode(tt.code)
		if !ok || kind != tt.kind {
			t.Fatalf("code 0x%02x: kind=%q ok=%v", tt.code, kind, ok)
		}
	}
	if _, ok := selfTestCode("invalid"); ok {
		t.Fatal("invalid kind accepted")
	}
	if _, ok := selfTestKindFromCode(0xff); ok {
		t.Fatal("invalid code accepted")
	}
}

func TestParseATASelfTestCapabilities(t *testing.T) {
	raw := make([]byte, 512)
	raw[367] = 0x71
	raw[372] = 2
	raw[373] = 0xff
	raw[374] = 5
	binary.LittleEndian.PutUint16(raw[375:377], 300)
	c := parseATASelfTestCapabilities(raw, false)
	if !c.Supported || !c.Offline || !c.Short || !c.Extended || !c.Conveyance || !c.Selective || !c.Abort || !c.ResultLog || !c.Progress {
		t.Fatalf("capabilities: %+v", c)
	}
	if !c.ShortCaptive || !c.ExtendedCaptive || !c.ConveyanceCaptive || !c.SelectiveCaptive {
		t.Fatalf("captive capabilities: %+v", c)
	}
	if c.ShortDurationMinutes != 2 || c.ExtendedDurationMinutes != 300 || c.ConveyanceDurationMinutes != 5 {
		t.Fatalf("durations: %+v", c)
	}
	if !c.Supports(SelfTestKindSelective) || c.Supports("invalid") {
		t.Fatalf("supports: %+v", c)
	}
	if c.DurationMinutes(SelfTestKindExtendedCaptive) != 300 {
		t.Fatalf("extended duration: %+v", c)
	}
}

func TestParseATALegacyIdentifySelfTestCapability(t *testing.T) {
	raw := make([]byte, 512)
	c := parseATASelfTestCapabilities(raw, true)
	if !c.Supported || !c.Short || !c.Extended || c.Conveyance || c.Selective {
		t.Fatalf("capabilities: %+v", c)
	}
}

func TestParseNVMeSelfTestCapabilities(t *testing.T) {
	ctrl := &NVMeIdentifyCtrl{SelfTestSupported: true, SelfTestTimeMinutes: 90, SelfTestOptions: 1, NamespaceID: 7}
	c := parseNVMeSelfTestCapabilities(ctrl)
	if !c.Supported || c.Scope != "subsystem" || c.NamespaceID != 7 || !c.Short || !c.Extended || !c.Abort || !c.ResultLog || !c.Progress || c.ExtendedDurationMinutes != 90 {
		t.Fatalf("capabilities: %+v", c)
	}
	if parseNVMeSelfTestCapabilities(&NVMeIdentifyCtrl{}).Supported {
		t.Fatal("unsupported controller accepted")
	}
}

func TestParseNVMeIdentifySelfTestFields(t *testing.T) {
	raw := make([]byte, 4096)
	binary.LittleEndian.PutUint16(raw[256:258], 0x0010)
	binary.LittleEndian.PutUint16(raw[316:318], 120)
	raw[318] = 1
	ctrl := parseNVMeIdentifyCtrl(raw)
	if ctrl.OptionalAdminCommands != 0x0010 || !ctrl.SelfTestSupported || ctrl.SelfTestTimeMinutes != 120 || ctrl.SelfTestOptions != 1 {
		t.Fatalf("identify: %+v", ctrl)
	}
}

func TestParseNVMeIdentifyNamespace(t *testing.T) {
	raw := make([]byte, 4096)
	binary.LittleEndian.PutUint64(raw[0:8], 1000)
	binary.LittleEndian.PutUint64(raw[8:16], 900)
	binary.LittleEndian.PutUint64(raw[16:24], 500)
	raw[24] = 3
	raw[25] = 1
	raw[26] = 1
	raw[27] = 2
	raw[28] = 3
	raw[29] = 4
	raw[30] = 5
	raw[31] = 6
	raw[32] = 7
	binary.LittleEndian.PutUint64(raw[48:56], ^uint64(0))
	raw[56] = 1
	for i := range 16 {
		raw[104+i] = byte(i + 1)
	}
	for i := range 8 {
		raw[120+i] = byte(i + 17)
	}
	binary.LittleEndian.PutUint16(raw[128:130], 0)
	raw[130] = 9
	raw[131] = 2
	binary.LittleEndian.PutUint16(raw[132:134], 8)
	raw[134] = 12
	raw[135] = 1
	ns := parseNVMeIdentifyNamespace(raw)
	if ns.Size != 1000 || ns.Capacity != 900 || ns.Utilization != 500 || ns.Features != 3 || ns.FormattedLBA != 1 || ns.NVMCapacity != ^uint64(0) || ns.NVMCapacityString != "36893488147419103231" {
		t.Fatalf("namespace: %+v", ns)
	}
	if ns.NamespaceGUID[0] != 1 || ns.NamespaceGUID[15] != 16 || ns.IEEEExtendedUniqueID[0] != 17 || ns.IEEEExtendedUniqueID[7] != 24 {
		t.Fatalf("identifiers: %+v %+v", ns.NamespaceGUID, ns.IEEEExtendedUniqueID)
	}
	if len(ns.LBAFormats) != 2 || ns.LBAFormats[0].DataSize != 512 || ns.LBAFormats[0].RelativePerformance != 2 || ns.LBAFormats[1].MetadataSize != 8 || ns.LBAFormats[1].DataSize != 4096 || ns.LBAFormats[1].RelativePerformance != 1 {
		t.Fatalf("formats: %+v", ns.LBAFormats)
	}
}

func TestParseSCSIControlSelfTestMinutes(t *testing.T) {
	mode6 := make([]byte, 64)
	mode6[3] = 8
	mode6[12] = 0x0a
	mode6[13] = 0x0a
	binary.BigEndian.PutUint16(mode6[22:24], 601)
	minutes, ok := parseSCSIControlSelfTestMinutes(mode6)
	if !ok || minutes != 11 {
		t.Fatalf("mode sense 6: minutes=%d ok=%v", minutes, ok)
	}

	mode10 := make([]byte, 64)
	binary.BigEndian.PutUint16(mode10[6:8], 8)
	mode10[16] = 0x0a
	mode10[17] = 0x0a
	binary.BigEndian.PutUint16(mode10[26:28], 3600)
	minutes, ok = parseSCSIControlSelfTestMinutes(mode10)
	if !ok || minutes != 60 {
		t.Fatalf("mode sense 10: minutes=%d ok=%v", minutes, ok)
	}

	binary.BigEndian.PutUint16(mode10[26:28], 0xffff)
	if _, ok := parseSCSIControlSelfTestMinutes(mode10); ok {
		t.Fatal("sentinel duration accepted")
	}
	if !scsiControlNeedsExtendedInquiry(mode10) {
		t.Fatal("extended inquiry sentinel missed")
	}
	vpd := make([]byte, 64)
	vpd[1] = 0x86
	binary.BigEndian.PutUint16(vpd[2:4], 60)
	binary.BigEndian.PutUint16(vpd[10:12], 240)
	minutes, ok = parseSCSIExtendedInquirySelfTestMinutes(vpd)
	if !ok || minutes != 240 {
		t.Fatalf("extended inquiry: minutes=%d ok=%v", minutes, ok)
	}
}

func TestParseSCSISelfTestProgress(t *testing.T) {
	fixed := make([]byte, 18)
	fixed[0] = 0x70
	fixed[12] = 0x04
	fixed[13] = 0x09
	fixed[15] = 0x80
	binary.BigEndian.PutUint16(fixed[16:18], 0x8000)
	running, progress, known := parseSCSISelfTestProgress(fixed)
	if !running || !known || progress != 50 {
		t.Fatalf("fixed: running=%v progress=%d known=%v", running, progress, known)
	}

	descriptor := make([]byte, 16)
	descriptor[0] = 0x72
	descriptor[7] = 8
	descriptor[8] = 0x0a
	descriptor[9] = 6
	descriptor[11] = 0x04
	descriptor[12] = 0x09
	binary.BigEndian.PutUint16(descriptor[14:16], 0x4000)
	running, progress, known = parseSCSISelfTestProgress(descriptor)
	if !running || !known || progress != 25 {
		t.Fatalf("descriptor: running=%v progress=%d known=%v", running, progress, known)
	}

	if running, _, _ := parseSCSISelfTestProgress(make([]byte, 18)); running {
		t.Fatal("idle sense reported running")
	}
}

func TestSCSILogNormalizesRunningResult(t *testing.T) {
	raw := make([]byte, 24)
	raw[0] = 0x10
	binary.BigEndian.PutUint16(raw[2:4], 20)
	raw[8] = 0x2f
	log := parseSCSISelfTestLog(raw)
	if !log.InProgress || log.CurrentType != "short" || len(log.Entries) != 1 {
		t.Fatalf("log: %+v", log)
	}
	entry := log.Entries[0]
	if entry.Protocol != "SCSI" || entry.Type != "short" || entry.Mode != "background" || entry.Outcome != SelfTestOutcomeInProgress {
		t.Fatalf("entry: %+v", entry)
	}
}

func TestSelfTestEntriesPreserveFullLBA(t *testing.T) {
	nvme := make([]byte, 28)
	nvme[0] = 0x17
	nvme[2] = 0x02
	binary.LittleEndian.PutUint64(nvme[16:24], ^uint64(0))
	nvmeEntry := parseNVMESelfTestEntry(nvme)
	if !nvmeEntry.LBAValid || nvmeEntry.LBA != ^uint64(0) {
		t.Fatalf("NVMe entry: %+v", nvmeEntry)
	}

	scsi := make([]byte, 20)
	scsi[4] = 0x24
	binary.BigEndian.PutUint64(scsi[8:16], ^uint64(0)-1)
	scsiEntry := parseSCSISelfTestEntry(scsi)
	if !scsiEntry.LBAValid || scsiEntry.LBA != ^uint64(0)-1 {
		t.Fatalf("SCSI entry: %+v", scsiEntry)
	}
}

func TestStatusFromLogNormalizesProgress(t *testing.T) {
	capabilities := SelfTestCapabilities{Protocol: "NVMe", Extended: true, ExtendedDurationMinutes: 90}
	log := SelfTestLog{
		InProgress:    true,
		CurrentType:   "extended",
		ProgressPct:   37,
		ProgressKnown: true,
		Entries:       []SelfTestEntry{{Protocol: "NVMe", Type: "short", Outcome: SelfTestOutcomePassed}},
	}
	status := statusFromLog("NVMe", capabilities, log)
	if !status.Running || status.State != SelfTestStateRunning || status.ExecutionStatus != "in_progress" || status.Type != SelfTestKindExtended || !status.ProgressKnown || status.ProgressPct != 37 || !status.RemainingKnown || status.RemainingPct != 63 || status.EstimatedDurationMinutes != 90 || len(status.Results) != 1 {
		t.Fatalf("status: %+v", status)
	}
}

func TestSelfTestOutcome(t *testing.T) {
	tests := map[string]string{
		"completed":                SelfTestOutcomePassed,
		"aborted_reset":            SelfTestOutcomeAborted,
		"interrupted":              SelfTestOutcomeAborted,
		"failed_read":              SelfTestOutcomeFailed,
		"fatal":                    SelfTestOutcomeFailed,
		"completed_segment_failed": SelfTestOutcomeFailed,
		"in_progress":              SelfTestOutcomeInProgress,
		"reserved":                 SelfTestOutcomeUnknown,
	}
	for status, want := range tests {
		if got := selfTestOutcome(status); got != want {
			t.Fatalf("status %q: got %q want %q", status, got, want)
		}
	}
}

func TestSelfTestParsersRejectTruncatedInput(t *testing.T) {
	for size := 0; size < 512; size++ {
		raw := make([]byte, size)
		_ = parseATASelfTestCapabilities(raw, false)
		_, _ = parseSCSIControlSelfTestMinutes(raw)
		_ = scsiControlNeedsExtendedInquiry(raw)
		_, _ = parseSCSIExtendedInquirySelfTestMinutes(raw)
		_, _, _ = parseSCSISelfTestProgress(raw)
		_ = parseNVMESelfTestLog(raw)
		_ = parseSCSISelfTestLog(raw)
		_ = parseNVMeIdentifyCtrl(raw)
		_ = parseNVMeIdentifyNamespace(raw)
	}
}

func BenchmarkParseATASelfTestCapabilities(b *testing.B) {
	raw := make([]byte, 512)
	raw[367] = 0x71
	for b.Loop() {
		_ = parseATASelfTestCapabilities(raw, true)
	}
}

func BenchmarkParseNVMESelfTestLog(b *testing.B) {
	raw := make([]byte, 564)
	raw[0] = 1
	raw[1] = 50
	raw[4] = 0x10
	for b.Loop() {
		_ = parseNVMESelfTestLog(raw)
	}
}

func BenchmarkParseSCSISelfTestLog(b *testing.B) {
	raw := make([]byte, 404)
	raw[0] = 0x10
	binary.BigEndian.PutUint16(raw[2:4], 400)
	raw[8] = 0x20
	for b.Loop() {
		_ = parseSCSISelfTestLog(raw)
	}
}
