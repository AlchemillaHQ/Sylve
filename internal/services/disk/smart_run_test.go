// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"context"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

func selfTestRunTestDisk() diskServiceInterfaces.DiskInfo {
	return diskServiceInterfaces.DiskInfo{
		Name:        "ada0",
		Description: "Test Disk",
		Serial:      "SERIAL-0",
		LunID:       "LUN-0",
		Type:        "SSD",
	}
}

func selfTestRunTestResult(hours uint64) diskServiceInterfaces.DiskSelfTestResult {
	return diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "completed",
		Outcome:       "passed",
		RemainingPct:  0,
		LifetimeHours: hours,
	}
}

func requireManualSelfTestEvent(t *testing.T, service *Service, condition string) models.DiskSmartSelfTestEvent {
	t.Helper()
	var events []models.DiskSmartSelfTestEvent
	if err := service.DB.Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Condition != condition || events[0].EventKey == "" || events[0].RunKey == "" || events[0].Source != smartSelfTestRunSourceManual || events[0].ScheduleID != 0 {
		t.Fatalf("events=%+v", events)
	}
	return events[0]
}

func TestAttachSelfTestRunTimes(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	oldResult := selfTestRunTestResult(10)
	before := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Results: []diskServiceInterfaces.DiskSelfTestResult{oldResult}},
	}
	if err := service.recordManualSelfTestRun(context.Background(), disk, "short", before, true, startedAt); err != nil {
		t.Fatal(err)
	}
	newResult := selfTestRunTestResult(11)
	after := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Results: []diskServiceInterfaces.DiskSelfTestResult{newResult, oldResult}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, after); err != nil {
		t.Fatal(err)
	}
	if after.Status.Results[0].StartedAt == nil || !after.Status.Results[0].StartedAt.Equal(startedAt) {
		t.Fatalf("new_result=%+v", after.Status.Results[0])
	}
	if after.Status.Results[1].StartedAt != nil || after.Status.Results[0].LifetimeHours != 11 || after.Status.Results[1].LifetimeHours != 10 {
		t.Fatalf("results=%+v", after.Status.Results)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultFingerprint != mappedSelfTestResultFingerprint(newResult) || run.CompletedAt == nil {
		t.Fatalf("run=%+v", run)
	}
}

func TestAttachSelfTestRunTimesIgnoresReorderedHistory(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	first := selfTestRunTestResult(10)
	second := selfTestRunTestResult(11)
	before := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Results: []diskServiceInterfaces.DiskSelfTestResult{first, second}},
	}
	if err := service.recordManualSelfTestRun(context.Background(), disk, "short", before, true, startedAt); err != nil {
		t.Fatal(err)
	}
	reordered := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{
			State:   smart.SelfTestStateRunning,
			Running: true,
			Results: []diskServiceInterfaces.DiskSelfTestResult{second, first},
		},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, reordered); err != nil {
		t.Fatal(err)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunRunning || run.ResultFingerprint != "" || run.CompletedAt != nil {
		t.Fatalf("run=%+v", run)
	}
	completed := selfTestRunTestResult(12)
	after := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Results: []diskServiceInterfaces.DiskSelfTestResult{completed, second, first}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, after); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultFingerprint != mappedSelfTestResultFingerprint(completed) || run.CompletedAt == nil {
		t.Fatalf("run=%+v", run)
	}
}

func TestAttachSelfTestRunTimesMatchesDuplicateResultsNewestFirst(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	diskKey := selfTestRunDiskKey(disk)
	result := selfTestRunTestResult(20)
	fingerprint := mappedSelfTestResultFingerprint(result)
	older := time.Date(2026, time.July, 8, 10, 0, 0, 0, time.UTC)
	newer := older.Add(24 * time.Hour)
	runs := []models.DiskSmartSelfTestRun{
		{RunKey: "older", DiskKey: diskKey, Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: older, Status: smartSelfTestRunPassed, ResultFingerprint: fingerprint},
		{RunKey: "newer", DiskKey: diskKey, Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: newer, Status: smartSelfTestRunPassed, ResultFingerprint: fingerprint},
	}
	if err := service.DB.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Results: []diskServiceInterfaces.DiskSelfTestResult{result, result}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		t.Fatal(err)
	}
	if info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(newer) {
		t.Fatalf("first=%+v", info.Status.Results[0])
	}
	if info.Status.Results[1].StartedAt == nil || !info.Status.Results[1].StartedAt.Equal(older) {
		t.Fatalf("second=%+v", info.Status.Results[1])
	}
}

func TestMergeSelfTestRunResultsPreservesMissingDuplicate(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	result := selfTestRunTestResult(20)
	fingerprint := mappedSelfTestResultFingerprint(result)
	data, err := encodeSelfTestRunResult(result)
	if err != nil {
		t.Fatal(err)
	}
	older := time.Date(2026, time.July, 8, 10, 0, 0, 0, time.UTC)
	newer := older.Add(24 * time.Hour)
	runs := []models.DiskSmartSelfTestRun{
		{RunKey: "older", DiskKey: selfTestRunDiskKey(disk), Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: older, Status: smartSelfTestRunPassed, ResultFingerprint: fingerprint, ResultData: data},
		{RunKey: "newer", DiskKey: selfTestRunDiskKey(disk), Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: newer, Status: smartSelfTestRunPassed, ResultFingerprint: fingerprint, ResultData: data},
	}
	if err := service.DB.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Protocol: "ATA", Results: []diskServiceInterfaces.DiskSelfTestResult{result}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(newer) || info.Status.Results[1].StartedAt == nil || !info.Status.Results[1].StartedAt.Equal(older) {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}

func TestSelfTestRunRetention(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < smartSelfTestRunRetention+1; i++ {
		startedAt := base.Add(time.Duration(i) * time.Minute)
		if err := service.recordManualSelfTestRun(context.Background(), disk, "short", nil, false, startedAt); err != nil {
			t.Fatal(err)
		}
	}
	var runs []models.DiskSmartSelfTestRun
	if err := service.DB.Order("started_at ASC").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != smartSelfTestRunRetention || !runs[0].StartedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("count=%d first=%v", len(runs), runs[0].StartedAt)
	}
}

func TestManualSelfTestPersistsAndReturnsStartTime(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true, ResultLog: true},
		status: smart.SelfTestStatus{
			Protocol:      "ATA",
			State:         smart.SelfTestStateIdle,
			ChecksumValid: true,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
			},
		},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if _, err := service.StartSelfTest("ada0", "short"); err != nil {
		t.Fatal(err)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Source != smartSelfTestRunSourceManual || run.TestType != "short" || !run.BaselineValid || run.Status != smartSelfTestRunRunning {
		t.Fatalf("run=%+v", run)
	}
	backend.mu.Lock()
	backend.status.Running = false
	backend.status.State = smart.SelfTestStateIdle
	backend.status.ExecutionStatus = "completed"
	backend.status.Results = []smart.SelfTestEntry{
		{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 11},
		{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
	}
	backend.mu.Unlock()
	service.invalidateSelfTestInfo("ada0")
	info, err := service.GetSelfTestInfo("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(run.StartedAt) || info.Status.Results[1].StartedAt != nil {
		t.Fatalf("results=%+v run=%+v", info.Status.Results, run)
	}
}

func TestManualSelfTestAbortReconcilesStaleDeviceResult(t *testing.T) {
	oldResult := smart.SelfTestEntry{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "completed",
		Outcome:       smart.SelfTestOutcomePassed,
		RemainingPct:  0,
		LifetimeHours: 10,
	}
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{
			Protocol:  "ATA",
			Supported: true,
			Short:     true,
			Abort:     true,
			ResultLog: true,
			Progress:  true,
		},
		status: smart.SelfTestStatus{
			Protocol:      "ATA",
			State:         smart.SelfTestStateIdle,
			ProgressPct:   -1,
			RemainingPct:  -1,
			ChecksumValid: true,
			Results:       []smart.SelfTestEntry{oldResult},
		},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if _, err := service.StartSelfTest("ada0", "short"); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status = smart.SelfTestStatus{
		Protocol:                 "ATA",
		State:                    smart.SelfTestStateRunning,
		ExecutionStatus:          smart.SelfTestOutcomeInProgress,
		Type:                     smart.SelfTestKindShort,
		Running:                  true,
		ProgressPct:              20,
		ProgressKnown:            true,
		RemainingPct:             80,
		RemainingKnown:           true,
		EstimatedDurationMinutes: 2,
		ChecksumValid:            true,
		Results: []smart.SelfTestEntry{
			oldResult,
			{
				Protocol:      "ATA",
				Type:          "short",
				Mode:          "offline",
				Status:        "in_progress",
				Outcome:       smart.SelfTestOutcomeInProgress,
				RemainingPct:  80,
				LifetimeHours: 11,
			},
		},
	}
	backend.mu.Unlock()
	stopped, err := service.StopSelfTest("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status.Running || stopped.Status.State != smart.SelfTestStateIdle || stopped.Status.ExecutionStatus != smart.SelfTestOutcomeAborted {
		t.Fatalf("status=%+v", stopped.Status)
	}
	if stopped.Status.ProgressKnown || stopped.Status.ProgressPct != -1 || stopped.Status.RemainingKnown || stopped.Status.RemainingPct != -1 {
		t.Fatalf("progress=%+v", stopped.Status)
	}
	if len(stopped.Status.Results) != 2 {
		t.Fatalf("results=%+v", stopped.Status.Results)
	}
	result := stopped.Status.Results[0]
	if result.Status != smart.SelfTestOutcomeAborted || result.Outcome != smart.SelfTestOutcomeAborted || result.RemainingPct != 80 || result.StartedAt == nil {
		t.Fatalf("result=%+v", result)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunAborted || run.ResultFingerprint != mappedSelfTestResultFingerprint(result) || run.ResultData == "" || run.CompletedAt == nil {
		t.Fatalf("run=%+v", run)
	}
	backend.mu.Lock()
	backend.status.Results[1].RemainingPct = 100
	backend.mu.Unlock()
	startedAgain, err := service.StartSelfTest("ada0", "short")
	if err != nil {
		t.Fatal(err)
	}
	if len(startedAgain.Status.Results) != 3 || startedAgain.Status.Results[0].Outcome != smart.SelfTestOutcomeInProgress || startedAgain.Status.Results[1].Outcome != smart.SelfTestOutcomeAborted || startedAgain.Status.Results[1].RemainingPct != 80 {
		t.Fatalf("started_again=%+v", startedAgain.Status.Results)
	}
	if startedAgain.Status.Results[0].StartedAt == nil || startedAgain.Status.Results[1].StartedAt == nil || !startedAgain.Status.Results[0].StartedAt.After(*startedAgain.Status.Results[1].StartedAt) {
		t.Fatalf("started_again=%+v", startedAgain.Status.Results)
	}
	cached, err := service.GetSelfTestInfo("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if len(cached.Status.Results) != 3 || cached.Status.Results[0].Outcome != smart.SelfTestOutcomeInProgress || cached.Status.Results[1].Outcome != smart.SelfTestOutcomeAborted {
		t.Fatalf("cached=%+v", cached.Status.Results)
	}
	var runs []models.DiskSmartSelfTestRun
	if err := service.DB.Order("started_at DESC").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Status != smartSelfTestRunRunning || runs[1].Status != smartSelfTestRunAborted || runs[1].ResultData == "" {
		t.Fatalf("runs=%+v", runs)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_aborted")
	if event.EventKey != runs[1].RunKey+"|self_test_aborted" || event.RunKey != runs[1].RunKey || event.Source != smartSelfTestRunSourceManual {
		t.Fatalf("event=%+v run=%+v", event, runs[1])
	}
}

func TestStartSelfTestReconcilesUnfinishedAbortedRun(t *testing.T) {
	disk := selfTestRunTestDisk()
	oldResult := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "completed",
		Outcome:       smart.SelfTestOutcomePassed,
		RemainingPct:  0,
		LifetimeHours: 10,
	}
	startedAt := time.Date(2026, time.July, 10, 15, 26, 47, 0, time.UTC)
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true, ResultLog: true},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "aborted_by_host",
			ProgressPct:     -1,
			RemainingPct:    -1,
			ChecksumValid:   true,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "in_progress", Outcome: smart.SelfTestOutcomeInProgress, RemainingPct: 80, LifetimeHours: 11},
			},
		},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{disk}, nil
	}
	run := models.DiskSmartSelfTestRun{
		RunKey:              "unfinished-abort",
		DiskKey:             selfTestRunDiskKey(disk),
		Device:              disk.Name,
		TestType:            "short",
		Source:              smartSelfTestRunSourceManual,
		StartedAt:           startedAt,
		Status:              smartSelfTestRunRunning,
		BaselineFingerprint: mappedSelfTestResultLogFingerprint([]diskServiceInterfaces.DiskSelfTestResult{oldResult}, "short"),
		BaselineValid:       true,
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	info, err := service.StartSelfTest("ada0", "short")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 3 || info.Status.Results[0].Outcome != smart.SelfTestOutcomeInProgress || info.Status.Results[1].Outcome != smart.SelfTestOutcomeAborted || info.Status.Results[1].RemainingPct != 80 || info.Status.Results[1].StartedAt == nil || !info.Status.Results[1].StartedAt.Equal(startedAt) {
		t.Fatalf("results=%+v", info.Status.Results)
	}
	var repaired models.DiskSmartSelfTestRun
	if err := service.DB.First(&repaired, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.Status != smartSelfTestRunAborted || repaired.ResultData == "" || repaired.ResultFingerprint != mappedSelfTestResultFingerprint(info.Status.Results[1]) {
		t.Fatalf("repaired=%+v", repaired)
	}
}

func TestExternalAbortDoesNotRewriteLegacyPassedRun(t *testing.T) {
	disk := selfTestRunTestDisk()
	oldResult := selfTestRunTestResult(10)
	startedAt := time.Date(2026, time.July, 10, 15, 26, 47, 0, time.UTC)
	completedAt := startedAt.Add(time.Minute)
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true, ResultLog: true},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "aborted_by_host",
			ProgressPct:     -1,
			RemainingPct:    -1,
			ChecksumValid:   true,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "in_progress", Outcome: smart.SelfTestOutcomeInProgress, RemainingPct: 80, LifetimeHours: 11},
			},
		},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{disk}, nil
	}
	run := models.DiskSmartSelfTestRun{
		RunKey:            "legacy-pass",
		DiskKey:           selfTestRunDiskKey(disk),
		Device:            disk.Name,
		TestType:          "short",
		Source:            smartSelfTestRunSourceManual,
		StartedAt:         startedAt,
		CompletedAt:       &completedAt,
		Status:            smartSelfTestRunPassed,
		ResultFingerprint: mappedSelfTestResultFingerprint(oldResult),
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	info, err := service.GetSelfTestInfo("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].Outcome != smart.SelfTestOutcomePassed || info.Status.Results[1].Outcome != smart.SelfTestOutcomeAborted {
		t.Fatalf("results=%+v", info.Status.Results)
	}
	var stored models.DiskSmartSelfTestRun
	if err := service.DB.First(&stored, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != smartSelfTestRunPassed || stored.ResultData != "" || stored.ResultFingerprint != run.ResultFingerprint {
		t.Fatalf("stored=%+v", stored)
	}
	var eventCount int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("events=%d", eventCount)
	}
}

func TestSelfTestRunSnapshotRestoresMissingProtocolResult(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	if err := service.recordManualSelfTestRun(context.Background(), disk, "short", nil, false, startedAt); err != nil {
		t.Fatal(err)
	}
	entry := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "SCSI",
		Type:          "short",
		Mode:          "background",
		Status:        "aborted_by_host",
		Outcome:       smart.SelfTestOutcomeAborted,
		RemainingPct:  60,
		LifetimeHours: 100,
		SenseKey:      4,
		ASC:           9,
		ASCQ:          1,
	}
	if err := service.finishActiveSelfTestRun(context.Background(), disk, smartSelfTestRunAborted, startedAt.Add(time.Minute), &entry); err != nil {
		t.Fatal(err)
	}
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Protocol: "SCSI", State: "idle", Results: []diskServiceInterfaces.DiskSelfTestResult{}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 1 || info.Status.Results[0].Outcome != smart.SelfTestOutcomeAborted || info.Status.Results[0].SenseKey != 4 || info.Status.Results[0].ASC != 9 || info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(startedAt) {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}

func TestSelfTestRunSnapshotReplacesPartialProtocolResult(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	entry := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "SCSI",
		Type:          "short",
		Mode:          "background",
		Status:        "failed",
		Outcome:       smart.SelfTestOutcomeFailed,
		RemainingPct:  0,
		LifetimeHours: 20,
		SenseKey:      5,
		ASC:           0x24,
		ASCQ:          1,
		ParameterCode: 7,
	}
	data, err := encodeSelfTestRunResult(entry)
	if err != nil {
		t.Fatal(err)
	}
	run := models.DiskSmartSelfTestRun{
		RunKey:            "scsi-partial",
		DiskKey:           selfTestRunDiskKey(disk),
		Device:            disk.Name,
		TestType:          "short",
		Source:            smartSelfTestRunSourceManual,
		StartedAt:         startedAt,
		Status:            smartSelfTestRunFailed,
		ResultFingerprint: mappedSelfTestResultFingerprint(entry),
		ResultData:        data,
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	partial := entry
	partial.Mode = ""
	partial.SenseKey = 0
	partial.ASC = 0
	partial.ASCQ = 0
	partial.ParameterCode = 0
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{Protocol: "SCSI", State: smart.SelfTestStateIdle, Results: []diskServiceInterfaces.DiskSelfTestResult{partial}},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 1 || info.Status.Results[0].Mode != "background" || info.Status.Results[0].SenseKey != 5 || info.Status.Results[0].ASC != 0x24 || info.Status.Results[0].ASCQ != 1 || info.Status.Results[0].ParameterCode != 7 {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}

func TestAbortedSelfTestSnapshotReplacesIdlePendingDescriptor(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	if err := service.recordManualSelfTestRun(context.Background(), disk, "short", nil, false, startedAt); err != nil {
		t.Fatal(err)
	}
	entry := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "aborted_by_host",
		Outcome:       smart.SelfTestOutcomeAborted,
		RemainingPct:  80,
		LifetimeHours: 11,
	}
	if err := service.finishActiveSelfTestRun(context.Background(), disk, smartSelfTestRunAborted, startedAt.Add(time.Minute), &entry); err != nil {
		t.Fatal(err)
	}
	pending := entry
	pending.Status = "in_progress"
	pending.Outcome = smart.SelfTestOutcomeInProgress
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: diskServiceInterfaces.DiskSelfTestState{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "completed",
			Results:         []diskServiceInterfaces.DiskSelfTestResult{pending},
		},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 1 || info.Status.Results[0].Outcome != smart.SelfTestOutcomeAborted || info.Status.Results[0].Status != "aborted_by_host" || info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(startedAt) {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}

func TestManualSelfTestImmediateResultUsesPreStartBaseline(t *testing.T) {
	oldResult := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10}
	completed := smart.SelfTestStatus{
		Protocol:        "ATA",
		State:           smart.SelfTestStateIdle,
		ExecutionStatus: "failed",
		ProgressPct:     -1,
		RemainingPct:    -1,
		ChecksumValid:   true,
		Results: []smart.SelfTestEntry{
			{Protocol: "ATA", Type: "short", Mode: "offline", Status: "failed", Outcome: smart.SelfTestOutcomeFailed, RemainingPct: 0, LifetimeHours: 11},
			oldResult,
		},
	}
	backend := &manualSelfTestBackend{
		capabilities:     smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, ResultLog: true},
		status:           smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ProgressPct: -1, RemainingPct: -1, ChecksumValid: true, Results: []smart.SelfTestEntry{oldResult}},
		statusAfterStart: &completed,
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	info, err := service.StartSelfTest("ada0", "short")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status.Running || len(info.Status.Results) != 2 || info.Status.Results[0].Outcome != smart.SelfTestOutcomeFailed || info.Status.Results[0].StartedAt == nil {
		t.Fatalf("info=%+v", info)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunFailed || run.ResultData == "" || run.CompletedAt == nil {
		t.Fatalf("run=%+v", run)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_failed")
	if event.EventKey != run.RunKey+"|self_test_failed" {
		t.Fatalf("event=%+v run=%+v", event, run)
	}
}

func TestManualSelfTestStartUsesFreshPreMutationHistory(t *testing.T) {
	oldResult := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10}
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true, ResultLog: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ProgressPct: -1, RemainingPct: -1, ChecksumValid: true, Results: []smart.SelfTestEntry{oldResult}},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if _, err := service.StartSelfTest("ada0", "short"); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status = smart.SelfTestStatus{
		Protocol:        "ATA",
		State:           smart.SelfTestStateIdle,
		ExecutionStatus: "completed",
		ProgressPct:     -1,
		RemainingPct:    -1,
		ChecksumValid:   true,
		Results: []smart.SelfTestEntry{
			{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 11},
			oldResult,
		},
	}
	backend.mu.Unlock()
	if _, err := service.StartSelfTest("ada0", "short"); err != nil {
		t.Fatal(err)
	}
	var runs []models.DiskSmartSelfTestRun
	if err := service.DB.Order("started_at DESC").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Status != smartSelfTestRunRunning || runs[1].Status != smartSelfTestRunPassed || runs[1].ResultData == "" {
		t.Fatalf("runs=%+v", runs)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_passed")
	if event.EventKey != runs[1].RunKey+"|self_test_passed" {
		t.Fatalf("event=%+v run=%+v", event, runs[1])
	}
}

func TestStopSelfTestPreservesResultCompletedBeforeAbort(t *testing.T) {
	oldResult := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10}
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true, ResultLog: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ProgressPct: -1, RemainingPct: -1, ChecksumValid: true, Results: []smart.SelfTestEntry{oldResult}},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if _, err := service.StartSelfTest("ada0", "short"); err != nil {
		t.Fatal(err)
	}
	completed := smart.SelfTestStatus{
		Protocol:        "ATA",
		State:           smart.SelfTestStateIdle,
		ExecutionStatus: "completed",
		ProgressPct:     -1,
		RemainingPct:    -1,
		ChecksumValid:   true,
		Results: []smart.SelfTestEntry{
			{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 11},
			oldResult,
		},
	}
	backend.mu.Lock()
	backend.statusAfterStop = &completed
	backend.mu.Unlock()
	info, err := service.StopSelfTest("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].Outcome != smart.SelfTestOutcomePassed || info.Status.Results[0].StartedAt == nil {
		t.Fatalf("info=%+v", info)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultData == "" {
		t.Fatalf("run=%+v", run)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_passed")
	if event.EventKey != run.RunKey+"|self_test_passed" {
		t.Fatalf("event=%+v run=%+v", event, run)
	}
}

func TestManualSelfTestDoesNotStartWithoutHistoryPersistence(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ChecksumValid: true},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if _, err := service.StartSelfTest("ada0", "short"); err == nil {
		t.Fatal("expected history persistence error")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestManualSelfTestDoesNotStartWhenRunInsertFails(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ChecksumValid: true},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{selfTestRunTestDisk()}, nil
	}
	if err := service.DB.Exec(`CREATE TRIGGER reject_smart_run BEFORE INSERT ON disk_smart_self_test_runs BEGIN SELECT RAISE(FAIL, 'rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartSelfTest("ada0", "short"); err == nil {
		t.Fatal("expected run insert error")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestScheduledSelfTestResultCannotBeDowngraded(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestEvent{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	schedule := models.DiskSmartSelfTestSchedule{ID: 1, TestType: "short", LastRunAt: &startedAt}
	if err := service.recordScheduledSelfTestRun(context.Background(), disk, schedule, startedAt); err != nil {
		t.Fatal(err)
	}
	entry := selfTestRunTestResult(20)
	if err := service.finishScheduledSelfTestRun(context.Background(), schedule, &entry, smartSelfTestRunPassed, startedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.finishScheduledSelfTestRun(context.Background(), schedule, nil, smartSelfTestRunUnknown, startedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultData == "" || run.ResultFingerprint != mappedSelfTestResultFingerprint(entry) {
		t.Fatalf("run=%+v", run)
	}
}

func TestManualSelfTestTrackerResumesCompletedRun(t *testing.T) {
	disk := selfTestRunTestDisk()
	oldResult := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "completed",
		Outcome:       smart.SelfTestOutcomePassed,
		RemainingPct:  0,
		LifetimeHours: 10,
	}
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, ResultLog: true},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "completed",
			ProgressPct:     -1,
			RemainingPct:    -1,
			ChecksumValid:   true,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 11},
				{Protocol: "ATA", Type: "short", Mode: "offline", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
			},
		},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{disk}, nil
	}
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	run := models.DiskSmartSelfTestRun{
		RunKey:              "manual-resume",
		DiskKey:             selfTestRunDiskKey(disk),
		Device:              disk.Name,
		TestType:            "short",
		Source:              smartSelfTestRunSourceManual,
		StartedAt:           startedAt,
		Status:              smartSelfTestRunRunning,
		BaselineFingerprint: mappedSelfTestResultLogFingerprint([]diskServiceInterfaces.DiskSelfTestResult{oldResult}, "short"),
		BaselineValid:       true,
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	running, err := service.hasRunningSelfTestRuns(context.Background())
	if err != nil || !running {
		t.Fatalf("running=%t err=%v", running, err)
	}
	if err := service.reconcileManualSelfTestRuns(context.Background(), startedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&run, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultData == "" {
		t.Fatalf("run=%+v", run)
	}
	running, err = service.hasRunningSelfTestRuns(context.Background())
	if err != nil || running {
		t.Fatalf("running=%t err=%v", running, err)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_passed")
	if event.EventKey != run.RunKey+"|self_test_passed" {
		t.Fatalf("event=%+v run=%+v", event, run)
	}
}

func TestManualSelfTestTrackerExpiresUnavailableRun(t *testing.T) {
	service := newManualSelfTestService(&manualSelfTestBackend{})
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{}, nil
	}
	now := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	run := models.DiskSmartSelfTestRun{
		RunKey:        "manual-expired",
		DiskKey:       "missing-disk",
		Device:        "ada9",
		TestType:      "short",
		Source:        smartSelfTestRunSourceManual,
		StartedAt:     now.Add(-7 * time.Hour),
		Status:        smartSelfTestRunRunning,
		BaselineValid: true,
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileManualSelfTestRuns(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&run, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunUnknown || run.ResultData == "" {
		t.Fatalf("run=%+v", run)
	}
	event := requireManualSelfTestEvent(t, service, "self_test_completed_unknown")
	if event.EventKey != run.RunKey+"|self_test_completed_unknown" {
		t.Fatalf("event=%+v run=%+v", event, run)
	}
}

func TestManualSelfTestTransitionRollsBackWithoutEvent(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{})}
	disk := selfTestRunTestDisk()
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	if err := service.recordManualSelfTestRun(context.Background(), disk, "short", nil, false, startedAt); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Exec(`CREATE TRIGGER reject_manual_smart_event BEFORE INSERT ON disk_smart_self_test_events BEGIN SELECT RAISE(FAIL, 'rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	entry := selfTestRunTestResult(20)
	if err := service.finishActiveSelfTestRun(context.Background(), disk, smartSelfTestRunPassed, startedAt.Add(time.Minute), &entry); err == nil {
		t.Fatal("expected event insert error")
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunRunning || run.ResultFingerprint != "" || run.ResultData != "" {
		t.Fatalf("run=%+v", run)
	}
	if err := service.DB.Exec(`DROP TRIGGER reject_manual_smart_event`).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.finishActiveSelfTestRun(context.Background(), disk, smartSelfTestRunPassed, startedAt.Add(time.Minute), &entry); err != nil {
		t.Fatal(err)
	}
	requireManualSelfTestEvent(t, service, "self_test_passed")
}

func TestSelfTestHistorySurvivesTerminalEventFailure(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{})}
	disk := selfTestRunTestDisk()
	diskKey := selfTestRunDiskKey(disk)
	startedAt := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)
	abortedAt := startedAt.Add(-time.Minute)
	aborted := diskServiceInterfaces.DiskSelfTestResult{
		Protocol: "ATA", Type: "short", Mode: "offline", Status: "aborted_by_host", Outcome: smart.SelfTestOutcomeAborted, RemainingPct: 80, LifetimeHours: 19,
	}
	abortedData, err := encodeSelfTestRunResult(aborted)
	if err != nil {
		t.Fatal(err)
	}
	runs := []models.DiskSmartSelfTestRun{
		{RunKey: "aborted", DiskKey: diskKey, Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: abortedAt, CompletedAt: &startedAt, Status: smartSelfTestRunAborted, ResultFingerprint: mappedSelfTestResultFingerprint(aborted), ResultData: abortedData},
		{RunKey: "running", DiskKey: diskKey, Device: disk.Name, TestType: "short", Source: smartSelfTestRunSourceManual, StartedAt: startedAt, Status: smartSelfTestRunRunning, BaselineValid: true},
	}
	if err := service.DB.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Exec(`CREATE TRIGGER reject_manual_smart_event BEFORE INSERT ON disk_smart_self_test_events BEGIN SELECT RAISE(FAIL, 'rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	info := &diskServiceInterfaces.DiskSelfTestInfo{
		Status: diskServiceInterfaces.DiskSelfTestState{
			Protocol: "ATA", State: smart.SelfTestStateIdle, ChecksumValid: true,
			Results: []diskServiceInterfaces.DiskSelfTestResult{selfTestRunTestResult(20)},
		},
	}
	if err := service.attachSelfTestRunTimes(context.Background(), disk, info); err == nil {
		t.Fatal("expected event insert error")
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].Outcome != smart.SelfTestOutcomeAborted || info.Status.Results[0].StartedAt == nil || !info.Status.Results[0].StartedAt.Equal(abortedAt) {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}
