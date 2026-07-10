// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"testing"

	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

func TestMapSelfTestStatusToInterface(t *testing.T) {
	status := &smart.SelfTestStatus{
		Running:       true,
		ProgressPct:   42,
		ChecksumValid: true,
		Results: []smart.SelfTestEntry{
			{
				Type:          "extended",
				Status:        "failed_read",
				RemainingPct:  0,
				LifetimeHours: 123,
				LBA:           456,
				NSID:          7,
			},
		},
	}
	got := mapSelfTestStatusToInterface(status)
	if !got.InProgress || got.ProgressPct != 42 || !got.ChecksumValid || len(got.Entries) != 1 {
		t.Fatalf("log: %+v", got)
	}
	entry := got.Entries[0]
	if entry.Type != "extended" || entry.Status != "failed_read" || entry.LifetimeHours != 123 || entry.LBA != 456 || entry.NSID != 7 {
		t.Fatalf("entry: %+v", entry)
	}
}

func TestMapNVMeLibSmartToInterface(t *testing.T) {
	info := &smart.DeviceInfo{
		Device:          "nda0",
		Protocol:        "NVMe",
		Passed:          true,
		PowerOnHours:    123,
		PowerCycleCount: 7,
		Temperature:     36,
		Attributes: []smart.Attribute{
			{ID: 0, RawValue: 0x1d},
			{ID: 3, RawValue: 98},
			{ID: 4, RawValue: 10},
			{ID: 5, RawValue: 4},
			{ID: 32, RawString: "123456"},
			{ID: 48, RawString: "340282366920938463463374607431768211455"},
			{ID: 112, RawString: "7"},
			{ID: 128, RawString: "123"},
			{ID: 160, RawValue: 2},
			{ID: 216, RawValue: 3},
		},
	}
	got := mapNVMeLibSmartToInterface(info)
	if got.Device.Name != "nda0" || got.Device.InfoName != "/dev/nda0" || !got.Passed || got.PowerOnHours != 123 || got.PowerCycleCount != 7 || got.Temperature != 36 {
		t.Fatalf("identity: %+v", got)
	}
	if got.CriticalWarning != "0x1d" || got.CriticalWarningState.AvailableSpare != 1 || got.CriticalWarningState.Temperature != 0 || got.CriticalWarningState.DeviceReliability != 1 || got.CriticalWarningState.ReadOnly != 1 || got.CriticalWarningState.VolatileMemoryBackup != 1 {
		t.Fatalf("warning: %+v", got.CriticalWarningState)
	}
	if got.AvailableSpare != 98 || got.AvailableSpareThreshold != 10 || got.PercentageUsed != 4 || got.DataUnitsRead != 123456 || got.DataUnitsReadExact != "123456" || got.DataUnitsWritten != int(^uint(0)>>1) || got.DataUnitsWrittenExact != "340282366920938463463374607431768211455" || got.PowerCycleCountExact != "7" || got.PowerOnHoursExact != "123" || got.MediaErrors != 2 || got.Temperature1TransitionCnt != 3 {
		t.Fatalf("values: %+v", got)
	}
}
