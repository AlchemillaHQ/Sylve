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
	"os"
	"testing"
	"time"
)

func TestHardwareReadOnly(t *testing.T) {
	devicePath := os.Getenv("SYLVE_SMART_TEST_DEVICE")
	if devicePath == "" {
		t.Skip("set SYLVE_SMART_TEST_DEVICE to run hardware integration tests")
	}

	started := time.Now()
	d, err := OpenDevice(devicePath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	t.Logf("open: %s", time.Since(started))

	supported, err := d.Supported()
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatalf("SMART is not supported by %s", devicePath)
	}

	capabilities, err := d.SelfTestCapabilities()
	if err != nil {
		t.Fatalf("self-test capabilities: %v", err)
	}
	if !capabilities.Supported || !capabilities.Short || !capabilities.Extended {
		t.Fatalf("incomplete self-test capabilities: %+v", capabilities)
	}
	status, err := d.SelfTestStatus()
	if err != nil {
		t.Fatalf("self-test status: %v", err)
	}
	t.Logf("self-test: protocol=%s state=%s execution=%s progress=%d progress_known=%v short_minutes=%d extended_minutes=%d results=%d",
		capabilities.Protocol, status.State, status.ExecutionStatus, status.ProgressPct,
		status.ProgressKnown, capabilities.ShortDurationMinutes,
		capabilities.ExtendedDurationMinutes, len(status.Results))

	started = time.Now()
	info, err := d.Read()
	if err != nil {
		t.Fatal(err)
	}
	if info.Protocol == "Unknown" || info.Model == "" {
		t.Fatalf("incomplete device identity: %+v", info)
	}
	if !info.HealthKnown {
		t.Fatalf("device health was reported as an implicit pass: %+v", info)
	}
	t.Logf("SMART data: %s", time.Since(started))

	started = time.Now()
	selfTests, err := d.ReadSelfTestLog()
	if err != nil {
		t.Fatalf("self-test result log: %v", err)
	}
	t.Logf("self-test log: %s", time.Since(started))
	t.Logf("%s %s: protocol=%s health_known=%v passed=%v attributes=%d self_tests=%d",
		info.Model, info.Firmware, info.Protocol, info.HealthKnown, info.Passed,
		len(info.Attributes), len(selfTests.Entries))

	switch info.Protocol {
	case "ATA":
		if info.SectorCount == 0 {
			t.Fatal("ATA sector count is unavailable")
		}
		if !info.ChecksumValid || !selfTests.ChecksumValid {
			t.Fatalf("invalid ATA SMART checksum: data=%v self-tests=%v", info.ChecksumValid, selfTests.ChecksumValid)
		}
		errorLog, err := d.ReadErrorLog()
		if err != nil {
			t.Fatalf("ATA summary error log: %v", err)
		}
		if !errorLog.ChecksumValid {
			t.Fatal("invalid ATA summary error-log checksum")
		}
		smartDirectory, err := d.ReadLogDirectory()
		if err != nil {
			t.Fatalf("ATA SMART log directory: %v", err)
		}
		started = time.Now()
		directory, err := d.ReadGPLLogDirectory()
		if err != nil {
			t.Fatalf("ATA GPL log directory: %v", err)
		}
		t.Logf("GPL directory: %s (extended error=%d sectors, extended self-test=%d sectors)",
			time.Since(started), d.gplDirectory[0x03], d.gplDirectory[0x07])
		hasLog := func(address uint8) bool {
			for _, candidate := range directory {
				if candidate == address {
					return true
				}
			}
			return false
		}
		hasSMARTLog := func(address uint8) bool {
			for _, candidate := range smartDirectory {
				if candidate == address {
					return true
				}
			}
			return false
		}
		if hasLog(0x03) {
			started = time.Now()
			log, err := d.ReadExtendedErrorLog()
			if err != nil || !log.ChecksumValid {
				t.Fatalf("ATA extended error log: checksum=%v error=%v", log != nil && log.ChecksumValid, err)
			}
			t.Logf("extended error log: %s", time.Since(started))
		}
		if hasLog(0x07) {
			started = time.Now()
			log, err := d.ReadExtendedSelfTestLog()
			if err != nil || !log.ChecksumValid {
				t.Fatalf("ATA extended self-test log: checksum=%v error=%v", log != nil && log.ChecksumValid, err)
			}
			t.Logf("extended self-test log: %s", time.Since(started))
		}
		if hasLog(0x09) {
			log, err := d.ReadSelectiveSelfTestLog()
			if err != nil || !log.ChecksumValid {
				t.Fatalf("ATA selective self-test log: checksum=%v error=%v", log != nil && log.ChecksumValid, err)
			}
		}
		if hasLog(0x04) {
			started = time.Now()
			if _, err := d.ReadDeviceStatistics(); err != nil {
				t.Fatalf("ATA device statistics: %v", err)
			}
			t.Logf("device statistics: %s", time.Since(started))
		}
		if info.SCTSupported && hasSMARTLog(0xe0) {
			started = time.Now()
			if _, err := d.ReadSCTStatus(); err != nil {
				t.Logf("SCT status unsupported: %v", err)
			} else {
				t.Logf("SCT status: %s", time.Since(started))
			}
		} else if !info.SCTSupported {
			if _, err := d.ReadSCTStatus(); !errors.Is(err, ErrUnsupportedFeature) {
				t.Fatalf("SCT unsupported result: %v", err)
			}
		}
	case "SCSI":
		for _, attribute := range info.Attributes {
			switch attribute.Page {
			case 0x10, 0x15, 0x18, 0x2f:
				t.Fatalf("internal SCSI page exposed as an attribute: page=%#x id=%#x bytes=%d", attribute.Page, attribute.ID, len(attribute.RawBytes))
			}
		}
		if info.SCSISelfTestLog == nil {
			t.Fatal("SCSI self-test log was not retained from the device read")
		}
		if len(info.SCSISelfTestResults) != len(info.SCSISelfTestLog.Entries) {
			t.Fatalf("inconsistent SCSI self-test results: legacy=%d normalized=%d", len(info.SCSISelfTestResults), len(info.SCSISelfTestLog.Entries))
		}
	case "NVMe":
		if _, err := d.ReadNVMeErrorLog(); err != nil {
			t.Fatalf("NVMe error log: %v", err)
		}
		if _, err := d.ReadNVMeIdentifyCtrl(); err != nil {
			t.Fatalf("NVMe identify controller: %v", err)
		}
	}
}

func TestHardwareShortSelfTest(t *testing.T) {
	devicePath := os.Getenv("SYLVE_SMART_TEST_DEVICE")
	if devicePath == "" || os.Getenv("SYLVE_SMART_RUN_SELF_TEST") != "1" {
		t.Skip("set SYLVE_SMART_TEST_DEVICE and SYLVE_SMART_RUN_SELF_TEST=1")
	}

	d, err := OpenDevice(devicePath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.SelfTest(SelfTestShort); err != nil {
		t.Fatalf("start short self-test: %v", err)
	}
	if err := d.AbortSelfTest(); err != nil {
		t.Fatalf("abort short self-test: %v", err)
	}
	if _, err := d.ReadSelfTestLog(); err != nil {
		t.Fatalf("read self-test results after abort: %v", err)
	}
	status, err := d.SelfTestStatus()
	if err != nil {
		t.Fatalf("read self-test status after abort: %v", err)
	}
	if status.Running {
		t.Fatalf("self-test still running after abort: %+v", status)
	}
}

func TestHardwareCompletedShortSelfTest(t *testing.T) {
	devicePath := os.Getenv("SYLVE_SMART_TEST_DEVICE")
	if devicePath == "" || os.Getenv("SYLVE_SMART_WAIT_SELF_TEST") != "1" {
		t.Skip("set SYLVE_SMART_TEST_DEVICE and SYLVE_SMART_WAIT_SELF_TEST=1")
	}

	d, err := OpenDevice(devicePath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	capabilities, err := d.SelfTestCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Short {
		t.Fatalf("short self-test unsupported: %+v", capabilities)
	}
	duration := capabilities.ShortDurationMinutes
	if duration <= 0 {
		duration = 10
	}
	deadline := time.Now().Add(time.Duration(duration+5) * time.Minute)
	before, err := d.SelfTestStatus()
	if err != nil {
		t.Fatal(err)
	}
	beforeCount := len(before.Results)
	var beforeResult SelfTestEntry
	if beforeCount != 0 {
		beforeResult = before.Results[0]
	}
	if err := d.StartSelfTest(SelfTestKindShort); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	running := true
	observedRunning := false
	defer func() {
		if running {
			_ = d.AbortSelfTest()
		}
	}()

	lastProgress := -2
	for {
		status, err := d.SelfTestStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.ProgressPct != lastProgress {
			t.Logf("self-test state=%s execution=%s progress=%d progress_known=%v remaining=%d remaining_known=%v",
				status.State, status.ExecutionStatus, status.ProgressPct,
				status.ProgressKnown, status.RemainingPct, status.RemainingKnown)
			lastProgress = status.ProgressPct
		}
		if status.Running {
			observedRunning = true
		}
		if !status.Running {
			resultChanged := len(status.Results) != beforeCount
			if len(status.Results) != 0 && (beforeCount == 0 || status.Results[0] != beforeResult) {
				resultChanged = true
			}
			if !observedRunning && !resultChanged {
				if time.Since(startedAt) > 30*time.Second {
					t.Fatal("short self-test never became observable")
				}
				time.Sleep(time.Second)
				continue
			}
			running = false
			if len(status.Results) == 0 {
				t.Fatal("completed self-test has no result")
			}
			result := status.Results[0]
			t.Logf("self-test result: %+v", result)
			if result.Outcome != SelfTestOutcomePassed {
				t.Fatalf("short self-test did not pass: %+v", result)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("short self-test exceeded %d minutes", duration+5)
		}
		time.Sleep(5 * time.Second)
	}
}
