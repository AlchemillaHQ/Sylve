// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

func TestMapSelfTestStateMarshalsEmptyResultsAsArray(t *testing.T) {
	state := mapSelfTestState(&smart.SelfTestStatus{})
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"results":[]`)) {
		t.Fatalf("state encoded with nullable results: %s", encoded)
	}
}

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
				LBAValid:      true,
				NSID:          7,
				NSIDValid:     true,
			},
		},
	}
	got := mapSelfTestStatusToInterface(status)
	if !got.InProgress || got.ProgressPct != 42 || !got.ChecksumValid || len(got.Entries) != 1 {
		t.Fatalf("log: %+v", got)
	}
	entry := got.Entries[0]
	if entry.Type != "extended" || entry.Status != "failed_read" || entry.LifetimeHours != 123 || entry.LBA != 456 || !entry.LBAValid || entry.NSID != 7 || !entry.NSIDValid {
		t.Fatalf("entry: %+v", entry)
	}
}

func TestMapNVMeLibSmartToInterface(t *testing.T) {
	info := &smart.DeviceInfo{
		Device:          "nda0",
		Protocol:        "NVMe",
		Passed:          true,
		HealthKnown:     true,
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
	if got.Device.Name != "nda0" || got.Device.InfoName != "/dev/nda0" || !got.Passed || !got.HealthKnown || got.PowerOnHours != 123 || got.PowerCycleCount != 7 || got.Temperature != 36 {
		t.Fatalf("identity: %+v", got)
	}
	if got.CriticalWarning != "0x1d" || got.CriticalWarningState.AvailableSpare != 1 || got.CriticalWarningState.Temperature != 0 || got.CriticalWarningState.DeviceReliability != 1 || got.CriticalWarningState.ReadOnly != 1 || got.CriticalWarningState.VolatileMemoryBackup != 1 {
		t.Fatalf("warning: %+v", got.CriticalWarningState)
	}
	if got.AvailableSpare != 98 || got.AvailableSpareThreshold != 10 || got.PercentageUsed != 4 || got.DataUnitsRead != 123456 || got.DataUnitsReadExact != "123456" || got.DataUnitsWritten != int(^uint(0)>>1) || got.DataUnitsWrittenExact != "340282366920938463463374607431768211455" || got.PowerCycleCountExact != "7" || got.PowerOnHoursExact != "123" || got.MediaErrors != 2 || got.Temperature1TransitionCnt != 3 {
		t.Fatalf("values: %+v", got)
	}
}

func TestMapLibSmartHealthAndRawValues(t *testing.T) {
	info := &smart.DeviceInfo{
		Device:      "da0",
		Protocol:    "SCSI",
		Passed:      false,
		HealthKnown: false,
		Attributes: []smart.Attribute{
			{Page: 2, ID: 5, Name: "Counter", Threshold: -1, RawValue: ^uint64(0)},
		},
	}
	got := mapLibSmartToInterface(info)
	if got.HealthKnown || got.Passed || len(got.Attributes) != 1 {
		t.Fatalf("data: %+v", got)
	}
	if got.Attributes[0].RawValue != int64(1<<63-1) || got.Attributes[0].RawString != "18446744073709551615" {
		t.Fatalf("attribute: %+v", got.Attributes[0])
	}
}

func TestKingstonWearOutWithUnknownHealth(t *testing.T) {
	defs := smart.LookupModelAttrs("KINGSTON SEDC500R960G", "SCEKJ2.8")
	for id, want := range map[uint32]string{231: "SSD_Life_Left", 232: "Read_Fail_Count", 233: "Flash_Writes_GiB"} {
		if defs[id].Name != want {
			t.Fatalf("attribute %d: name=%q want=%q", id, defs[id].Name, want)
		}
	}
	info := &smart.DeviceInfo{
		Device:        "da0",
		Model:         "KINGSTON SEDC500R960G",
		Firmware:      "SCEKJ2.8",
		Protocol:      "ATA",
		HealthKnown:   false,
		ChecksumValid: true,
		Attributes: []smart.Attribute{
			{ID: 231, Name: defs[231].Name, Value: 97, Worst: 97, RawValue: 97},
			{ID: 232, Name: defs[232].Name, Value: 100, Worst: 100},
			{ID: 233, Name: defs[233].Name, Value: 100, Worst: 100, RawValue: 132670},
		},
	}
	data := mapLibSmartToInterface(info)
	if data.Model != info.Model || data.Firmware != info.Firmware || data.HealthKnown || len(data.Attributes) != 3 || data.Attributes[0].Name != "SSD Life Left" {
		t.Fatalf("data: %+v", data)
	}
	service := &Service{}
	if got := service.formatWearOut("SSD", data); got != "3.00" {
		t.Fatalf("wearout=%q want=3.00", got)
	}
	data.ChecksumValid = false
	if got := service.formatWearOut("SSD", data); got != "Unknown" {
		t.Fatalf("wearout accepted invalid checksum: %q", got)
	}
}

func TestFormatWearOut(t *testing.T) {
	service := &Service{}
	if got := service.formatWearOut("HDD", nil); got != "N/A" {
		t.Fatalf("HDD: %q", got)
	}
	data := diskServiceInterfaces.SmartData{
		Device:     diskServiceInterfaces.DeviceInfo{Protocol: "SCSI"},
		Attributes: []diskServiceInterfaces.ATASmartAttribute{{Page: 0x11, ID: 1, RawValue: 2}},
	}
	if got := service.formatWearOut("SSD", data); got != "2.00" {
		t.Fatalf("SSD: %q", got)
	}
	if got := service.formatWearOut("SSD", nil); got != "Unknown" {
		t.Fatalf("missing SSD data: %q", got)
	}
	scsiExhausted := diskServiceInterfaces.SmartData{
		Device:     diskServiceInterfaces.DeviceInfo{Protocol: "SCSI"},
		Attributes: []diskServiceInterfaces.ATASmartAttribute{{Page: 0x11, ID: 1, RawValue: 100}},
	}
	if got := service.formatWearOut("SSD", scsiExhausted); got != "100.00" {
		t.Fatalf("exhausted SCSI SSD: %q", got)
	}
	ataExhausted := diskServiceInterfaces.SmartData{
		Device:        diskServiceInterfaces.DeviceInfo{Protocol: "ATA"},
		Model:         "CT500MX500SSD1",
		ChecksumValid: true,
		Attributes:    []diskServiceInterfaces.ATASmartAttribute{{ID: 202, Name: "Percent Lifetime Remain", Value: 0, RawValue: 100}},
	}
	if got := service.formatWearOut("SSD", ataExhausted); got != "100.00" {
		t.Fatalf("exhausted ATA SSD: %q", got)
	}
}

func TestATAWearOutShame(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		firmware string
		attrs    []diskServiceInterfaces.ATASmartAttribute
		want     string
	}{
		{
			name:  "Kingston counters without life remaining",
			model: "KINGSTON SEDC500R960G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{
				{ID: 232, Name: "Read Fail Count", Value: 100},
				{ID: 233, Name: "Flash Writes GiB", Value: 100, RawValue: 132670},
			},
			want: "Unknown",
		},
		{
			name:  "reserved space is not endurance",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 232, Name: "Available Reservd Space", Value: 90}},
			want:  "Unknown",
		},
		{
			name:  "temperature is not endurance",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, Name: "Temperature Celsius", Value: 71, RawValue: 29}},
			want:  "Unknown",
		},
		{
			name:  "unnamed vendor attribute",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Value: 100}},
			want:  "Unknown",
		},
		{
			name:  "unverified media wearout indicator",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Name: "Media Wearout Indicator", Value: 92}},
			want:  "Unknown",
		},
		{
			name:  "Samsung wear leveling",
			model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Name: "Wear_Leveling_Count", Value: 98, RawValue: 25}},
			want:  "2.00",
		},
		{
			name:  "unverified percentage used raw value",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 232, Name: "Percent Life Used", Value: 100, RawValue: 7}},
			want:  "Unknown",
		},
		{
			name:  "invalid Samsung normalized percentage",
			model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 255}},
			want:  "Unknown",
		},
		{
			name:  "missing Samsung normalized value",
			model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, State: smart.AttrStateNoNormVal}},
			want:  "Unknown",
		},
		{
			name: "Samsung da0 sample", model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 1, RawValue: 1720}}, want: "99.00",
		},
		{
			name: "Samsung da1 sample", model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 3, RawValue: 1746}}, want: "97.00",
		},
		{
			name: "new Samsung PRO", model: "Samsung SSD 860 PRO 512GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 100}}, want: "0.00",
		},
		{
			name: "exhausted Samsung QVO", model: "Samsung SSD 860 QVO 1TB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 0}}, want: "100.00",
		},
		{
			name: "negative Samsung normalized percentage", model: "Samsung SSD 860 EVO 500GB",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: -1}}, want: "Unknown",
		},
		{
			name: "Kingston raw is authoritative", model: "KINGSTON SEDC500R960G", firmware: "SCEKJ2.8",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, Value: 100, RawValue: 97}}, want: "3.00",
		},
		{
			name: "Kingston ignores other raw bytes", model: "KINGSTON SEDC500M1920G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, Value: 100, RawValue: 0xaa0061}}, want: "3.00",
		},
		{
			name: "Kingston does not need a normalized value", model: "KINGSTON SEDC500R960G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, State: smart.AttrStateNoNormVal, RawValue: 97}}, want: "3.00",
		},
		{
			name: "invalid Kingston raw percentage", model: "KINGSTON SEDC500R960G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, RawValue: 101}}, want: "Unknown",
		},
		{
			name: "negative Kingston raw value is not masked", model: "KINGSTON SEDC500R960G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 231, RawValue: -1}}, want: "Unknown",
		},
		{
			name: "Kingston NAND writes are not wear", model: "KINGSTON SEDC500R960G",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Value: 1, RawValue: 132670}}, want: "Unknown",
		},
		{
			name: "Intel 335 media wearout", model: "INTEL SSDSC2CT240A4",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Value: 92, RawValue: 12000}}, want: "8.00",
		},
		{
			name: "Intel S3500 firmware floors remaining life at one", model: "INTEL SSDSC2BB480G4",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Value: 1, RawValue: 15000}}, want: "99.00",
		},
		{
			name: "unlisted Intel is not matched by brand", model: "INTEL SSDSC2BB480G7",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Value: 1}}, want: "Unknown",
		},
		{
			name: "reported WD reversal uses normalized percentage used", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Name: "Media Wearout Indicator", Value: 1, RawValue: 0x0160011e0160}}, want: "1.00",
		},
		{
			name: "WD forum sample is three percent used", model: "WDC WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Name: "Media Wearout Indicator", Value: 3, RawValue: 0x030c0228030c}}, want: "3.00",
		},
		{
			name: "WD zero means unused endurance", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 0}}, want: "0.00",
		},
		{
			name: "WD hundred means exhausted endurance", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 100}}, want: "100.00",
		},
		{
			name: "invalid WD normalized percentage", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 101}}, want: "Unknown",
		},
		{
			name: "negative WD normalized percentage", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: -1}}, want: "Unknown",
		},
		{
			name: "missing WD normalized value", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, State: smart.AttrStateNoNormVal, RawValue: 0x030c0228030c}}, want: "Unknown",
		},
		{
			name: "unlisted WD firmware is not guessed either", model: "WDC WDS400T2B0A-00SM50", firmware: "OTHER",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Name: "Media Wearout Indicator", Value: 100}}, want: "Unknown",
		},
		{
			name: "firmware substring does not match", model: "WDC  WDS400T2B0A-00SM50", firmware: "411030WD-extra",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 1}}, want: "Unknown",
		},
		{
			name: "WD without firmware is not matched", model: "WDC  WDS400T2B0A-00SM50",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 3}}, want: "Unknown",
		},
		{
			name: "other WD models are not matched by firmware", model: "WDC  WDS200T2B0A-00SM50", firmware: "411030WD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 230, Value: 3}}, want: "Unknown",
		},
		{
			name: "Micron raw usage not normalized remaining", model: "MTFDDAK1T0TDL-1AW1ZABFA",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 202, Value: 98, RawValue: 2}}, want: "2.00",
		},
		{
			name: "Crucial raw usage can exceed rated endurance", model: "CT500MX500SSD1",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 202, Value: 0, RawValue: 110}}, want: "110.00",
		},
		{
			name: "invalid Micron raw percentage", model: "Micron_1300_MTFDDAK1T0TDL",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 202, Value: 0, RawValue: 256}}, want: "Unknown",
		},
		{
			name: "Micron sample without endurance attribute", model: "MTFDDAK1T0TDL-1AW1ZABFA",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 173, Value: 99, RawValue: 21}}, want: "Unknown",
		},
		{
			name: "unlisted model with a familiar name", model: "UNLISTED SSD",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 233, Name: "Media Wearout Indicator", Value: 1}}, want: "Unknown",
		},
		{
			name: "truncated inventory model is not enough", model: "Samsung SSD 860",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 1}}, want: "Unknown",
		},
		{
			name: "substring model does not match", model: "prefix Samsung SSD 860 EVO 500GB suffix",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 1}}, want: "Unknown",
		},
		{
			name: "ATA identify padding is trimmed", model: " Samsung SSD 860 EVO 500GB ",
			attrs: []diskServiceInterfaces.ATASmartAttribute{{ID: 177, Value: 3}}, want: "97.00",
		},
	}
	service := &Service{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := diskServiceInterfaces.SmartData{
				Device:        diskServiceInterfaces.DeviceInfo{Protocol: "ATA"},
				Model:         test.model,
				Firmware:      test.firmware,
				ChecksumValid: true,
				Attributes:    slices.Clone(test.attrs),
			}
			if got := service.formatWearOut("SSD", data); got != test.want {
				t.Fatalf("wearout=%q want=%q", got, test.want)
			}
			if test.want == "Unknown" {
				if _, err := service.GetWearOut(data); err == nil {
					t.Fatal("unavailable wearout must return an error, not a healthy zero")
				}
			}
			if !slices.Equal(data.Attributes, test.attrs) {
				t.Fatal("wearout decoding changed the SMART attributes")
			}
			data.ChecksumValid = false
			if got := service.formatWearOut("SSD", data); got != "Unknown" {
				t.Fatalf("wearout accepted invalid checksum: %q", got)
			}
		})
	}
}

func TestNVMeWearOut(t *testing.T) {
	service := &Service{}
	for _, used := range []int{0, 100, 150} {
		got, err := service.GetWearOut(diskServiceInterfaces.SMARTNvme{PercentageUsed: used})
		if err != nil || got != float64(used) {
			t.Fatalf("NVMe percentage used=%v want=%d err=%v", got, used, err)
		}
	}
}
