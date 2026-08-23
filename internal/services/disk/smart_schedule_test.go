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
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/robfig/cron/v3"
)

type fakeScheduleSelfTestBackend struct {
	mu         sync.Mutex
	capability smart.SelfTestCapabilities
	status     smart.SelfTestStatus
	statuses   map[string]smart.SelfTestStatus
	readErr    error
	startErr   error
	stopErr    error
	starts     []smart.SelfTestKind
	devices    []string
	stops      int
}

type fakeScheduleEventEmitter struct {
	mu        sync.Mutex
	events    []notifier.EventInput
	started   chan struct{}
	release   <-chan struct{}
	startOnce sync.Once
	err       error
}

type fakeTargetedScheduleEventEmitter struct {
	mu           sync.Mutex
	targets      []string
	targetErrors map[string]error
	targetCalls  map[string]int
}

func (f *fakeScheduleEventEmitter) Emit(_ context.Context, input notifier.EventInput) (notifier.EmitResult, error) {
	f.mu.Lock()
	f.events = append(f.events, input)
	f.mu.Unlock()
	if f.started != nil {
		f.startOnce.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	return notifier.EmitResult{}, f.err
}

func (f *fakeTargetedScheduleEventEmitter) Emit(_ context.Context, input notifier.EventInput) (notifier.EmitResult, error) {
	return notifier.EmitResult{}, errors.New("unexpected_untargeted_emit")
}

func (f *fakeTargetedScheduleEventEmitter) DeliveryTargets(_ context.Context, _ notifier.EventInput) ([]string, error) {
	return append([]string(nil), f.targets...), nil
}

func (f *fakeTargetedScheduleEventEmitter) EmitTarget(_ context.Context, _ notifier.EventInput, target string) (notifier.EmitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.targetCalls == nil {
		f.targetCalls = map[string]int{}
	}
	f.targetCalls[target]++
	return notifier.EmitResult{}, f.targetErrors[target]
}

func (f *fakeScheduleSelfTestBackend) Read(device string) (*smart.SelfTestCapabilities, *smart.SelfTestStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, nil, f.readErr
	}
	capability := f.capability
	status := f.status
	if deviceStatus, ok := f.statuses[device]; ok {
		status = deviceStatus
	}
	status.Results = append([]smart.SelfTestEntry(nil), status.Results...)
	return &capability, &status, nil
}

func (f *fakeScheduleSelfTestBackend) Start(device string, kind smart.SelfTestKind) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.starts = append(f.starts, kind)
	f.devices = append(f.devices, device)
	return nil
}

func (f *fakeScheduleSelfTestBackend) Stop(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return f.stopErr
}

func makeSelfTestScheduleService(t *testing.T) (*Service, *fakeScheduleSelfTestBackend) {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	backend := &fakeScheduleSelfTestBackend{
		capability: smart.SelfTestCapabilities{
			Protocol:                "ATA",
			Supported:               true,
			Short:                   true,
			Extended:                true,
			Abort:                   true,
			ResultLog:               true,
			Progress:                true,
			ShortDurationMinutes:    2,
			ExtendedDurationMinutes: 120,
		},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			ChecksumValid:   true,
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "completed",
			ProgressPct:     -1,
			RemainingPct:    -1,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: -1, LifetimeHours: 10},
			},
		},
	}
	service := &Service{
		DB:             db,
		selfTestDriver: backend,
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{
				{Name: "ada0", Description: "Test Disk", Serial: "SERIAL-0", LunID: "LUN-0", Type: "SSD"},
				{Name: "ada1", Description: "Second Disk", Serial: "SERIAL-1", LunID: "LUN-1", Type: "HDD"},
			}, nil
		},
	}
	service.SetSelfTestSchedulerReady(true)
	service.selfTestEventEnqueue = func(ctx context.Context, job smartSelfTestEventJob) error {
		return service.handleSelfTestEventJob(ctx, job)
	}
	return service, backend
}

func TestNormalizeScheduledSelfTestType(t *testing.T) {
	tests := []struct {
		input string
		kind  smart.SelfTestKind
		name  string
		valid bool
	}{
		{input: "short", kind: smart.SelfTestKindShort, name: "short", valid: true},
		{input: "long", kind: smart.SelfTestKindExtended, name: "extended", valid: true},
		{input: "extended", kind: smart.SelfTestKindExtended, name: "extended", valid: true},
		{input: "offline", valid: false},
		{input: "short_captive", valid: false},
		{input: "selective", valid: false},
	}
	for _, test := range tests {
		kind, name, err := normalizeScheduledSelfTestType(test.input)
		if test.valid {
			if err != nil || kind != test.kind || name != test.name {
				t.Fatalf("%q: got (%q, %q, %v)", test.input, kind, name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidSelfTestSchedule) {
			t.Fatalf("%q: expected invalid schedule, got %v", test.input, err)
		}
	}
}

func TestParseSelfTestSchedule(t *testing.T) {
	tests := []struct {
		expr     string
		testType string
		valid    bool
	}{
		{expr: "0 2 * * *", testType: "short", valid: true},
		{expr: "0 2 * * 0", testType: "extended", valid: true},
		{expr: "* * * * *", testType: "short", valid: true},
		{expr: "* * * * *", testType: "extended", valid: true},
		{expr: "CRON_TZ=Asia/Kolkata 0 2 * * *", testType: "short", valid: false},
		{expr: "invalid", testType: "short", valid: false},
	}
	for _, test := range tests {
		_, err := parseSelfTestSchedule(test.expr, test.testType)
		if test.valid && err != nil {
			t.Fatalf("%s %s: %v", test.testType, test.expr, err)
		}
		if !test.valid && !errors.Is(err, ErrInvalidSelfTestSchedule) {
			t.Fatalf("%s %s: expected invalid schedule, got %v", test.testType, test.expr, err)
		}
	}
}

func TestNextSelfTestScheduleStoredUTC(t *testing.T) {
	location, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := cron.ParseStandard("0 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 10, 19, 0, 0, 0, time.UTC)
	next := nextSelfTestScheduleAt(schedule, now, location)
	want := time.Date(2026, time.July, 10, 20, 30, 0, 0, time.UTC)
	if !next.Equal(want) || next.Location() != time.UTC {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestSelfTestEventRelayActivityTracksPendingEvents(t *testing.T) {
	service := &Service{DB: testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestEvent{})}
	service.signalSelfTestEventRelay()
	version := service.selfTestEventRelayVersion.Load()
	service.refreshSelfTestEventRelay(context.Background(), version)
	if service.selfTestEventRelayActive.Load() {
		t.Fatal("relay should become idle without events")
	}
	event := models.DiskSmartSelfTestEvent{EventKey: "pending", RunKey: "pending", Source: smartSelfTestRunSourceManual, DiskKey: "disk", Device: "ada0", TestType: "short", Condition: "self_test_passed", Severity: "info", Title: "passed"}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	service.signalSelfTestEventRelay()
	version = service.selfTestEventRelayVersion.Load()
	service.refreshSelfTestEventRelay(context.Background(), version)
	if !service.selfTestEventRelayActive.Load() {
		t.Fatal("relay should remain active with a pending event")
	}
	if err := service.DB.Delete(&event).Error; err != nil {
		t.Fatal(err)
	}
	service.refreshSelfTestEventRelay(context.Background(), version)
	if service.selfTestEventRelayActive.Load() {
		t.Fatal("relay should become idle after delivery")
	}
}

func TestLatestCompletedSelfTestResult(t *testing.T) {
	status := &smart.SelfTestStatus{
		Results: []smart.SelfTestEntry{
			{Protocol: "SCSI", Type: "short", Status: "in_progress", Outcome: smart.SelfTestOutcomeInProgress},
			{Protocol: "SCSI", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 42},
		},
	}
	entry, fingerprint := latestCompletedSelfTestResult(status)
	if entry == nil || entry.Outcome != smart.SelfTestOutcomePassed || fingerprint == "" {
		t.Fatalf("unexpected result: entry=%+v fingerprint=%q", entry, fingerprint)
	}
}

func TestNewScheduledSelfTestResultIgnoresReorderedHistory(t *testing.T) {
	first := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 10}
	second := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 11}
	baselineStatus := &smart.SelfTestStatus{Protocol: "ATA", ChecksumValid: true, Results: []smart.SelfTestEntry{first, second}}
	_, baseline, baselineLog := latestScheduledSelfTestResult(baselineStatus, "short")
	reordered := &smart.SelfTestStatus{Protocol: "ATA", ChecksumValid: true, Results: []smart.SelfTestEntry{second, first}}
	if entry := newScheduledSelfTestResult(reordered, "short", baseline); entry != nil {
		t.Fatalf("entry=%+v", entry)
	}
	_, _, reorderedLog := latestScheduledSelfTestResult(reordered, "short")
	if selfTestFingerprintHasAdditionalOccurrence(reorderedLog, baselineLog) {
		t.Fatalf("baseline=%q reordered=%q", baselineLog, reorderedLog)
	}
	completed := smart.SelfTestEntry{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 12}
	after := &smart.SelfTestStatus{Protocol: "ATA", ChecksumValid: true, Results: []smart.SelfTestEntry{completed, second, first}}
	entry := newScheduledSelfTestResult(after, "short", baseline)
	if entry == nil || entry.LifetimeHours != 12 {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestScheduledSelfTestPreservesAbortedRunBeforeStart(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.July, 10, 15, 26, 47, 0, time.UTC)
	oldResult := diskServiceInterfaces.DiskSelfTestResult{
		Protocol:      "ATA",
		Type:          "short",
		Mode:          "offline",
		Status:        "completed",
		Outcome:       smart.SelfTestOutcomePassed,
		RemainingPct:  0,
		LifetimeHours: 10,
	}
	previous := models.DiskSmartSelfTestRun{
		RunKey:              "unfinished-scheduled-abort",
		DiskKey:             diskKey,
		Device:              disk.Name,
		TestType:            "short",
		Source:              smartSelfTestRunSourceScheduled,
		StartedAt:           startedAt,
		Status:              smartSelfTestRunRunning,
		BaselineFingerprint: mappedSelfTestResultLogFingerprint([]diskServiceInterfaces.DiskSelfTestResult{oldResult}, "short"),
		BaselineValid:       true,
	}
	if err := service.DB.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status = smart.SelfTestStatus{
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
	}
	backend.mu.Unlock()
	now := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC)
	queuedAt := now.Add(-time.Minute)
	schedule := models.DiskSmartSelfTestSchedule{
		DiskKey:     diskKey,
		Device:      disk.Name,
		Model:       disk.Description,
		Serial:      disk.Serial,
		TestType:    "short",
		CronExpr:    "0 2 * * *",
		Enabled:     true,
		QueuedAt:    &queuedAt,
		LastStatus:  smartSelfTestScheduleQueued,
		ProgressPct: -1,
	}
	if err := service.DB.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.startScheduledSelfTest(context.Background(), &schedule, now); err != nil {
		t.Fatal(err)
	}
	var repaired models.DiskSmartSelfTestRun
	if err := service.DB.First(&repaired, previous.ID).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.Status != smartSelfTestRunAborted || repaired.ResultData == "" {
		t.Fatalf("repaired=%+v", repaired)
	}
	var runs []models.DiskSmartSelfTestRun
	if err := service.DB.Order("started_at DESC").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Status != smartSelfTestRunRunning || runs[1].ID != previous.ID || runs[1].Status != smartSelfTestRunAborted {
		t.Fatalf("runs=%+v", runs)
	}
}

func TestScheduledSelfTestDoesNotStartWhenRunInsertFails(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC)
	queuedAt := now.Add(-time.Minute)
	schedule := models.DiskSmartSelfTestSchedule{
		DiskKey:     diskKey,
		Device:      disk.Name,
		Model:       disk.Description,
		Serial:      disk.Serial,
		TestType:    "short",
		CronExpr:    "0 2 * * *",
		Enabled:     true,
		QueuedAt:    &queuedAt,
		LastStatus:  smartSelfTestScheduleQueued,
		ProgressPct: -1,
	}
	if err := service.DB.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Exec(`CREATE TRIGGER reject_scheduled_smart_run BEFORE INSERT ON disk_smart_self_test_runs BEGIN SELECT RAISE(FAIL, 'rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.startScheduledSelfTest(context.Background(), &schedule, now); err == nil {
		t.Fatal("expected run insert error")
	}
	var stored models.DiskSmartSelfTestSchedule
	if err := service.DB.First(&stored, schedule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.LastStatus != smartSelfTestScheduleQueued || stored.QueuedAt == nil || !stored.QueuedAt.Equal(queuedAt) {
		t.Fatalf("stored=%+v", stored)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestSelfTestSchedulerJobReconcilesManualRun(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	oldResult := diskServiceInterfaces.DiskSelfTestResult{
		Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed,
		RemainingPct: 0, LifetimeHours: 10,
	}
	now := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC)
	run := models.DiskSmartSelfTestRun{
		RunKey:              "manual-scheduler-reconcile",
		DiskKey:             selfTestRunDiskKey(disk),
		Device:              disk.Name,
		TestType:            "short",
		Source:              smartSelfTestRunSourceManual,
		StartedAt:           now.Add(-time.Minute),
		Status:              smartSelfTestRunRunning,
		BaselineFingerprint: mappedSelfTestResultLogFingerprint([]diskServiceInterfaces.DiskSelfTestResult{oldResult}, "short"),
		BaselineValid:       true,
	}
	if err := service.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status = smart.SelfTestStatus{
		Protocol: "ATA", State: smart.SelfTestStateIdle, ExecutionStatus: "completed",
		ProgressPct: -1, RemainingPct: -1, ChecksumValid: true,
		Results: []smart.SelfTestEntry{
			{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 11},
			{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: 0, LifetimeHours: 10},
		},
	}
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&run, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.ResultData == "" {
		t.Fatalf("run=%+v", run)
	}
	var events []models.DiskSmartSelfTestEvent
	if err := service.DB.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventKey != run.RunKey+"|self_test_passed" || events[0].ScheduleID != 0 {
		t.Fatalf("events=%+v", events)
	}
}

func TestScheduledSelfTestTerminalStatus(t *testing.T) {
	tests := []struct {
		entry     *smart.SelfTestEntry
		execution string
		want      string
	}{
		{entry: &smart.SelfTestEntry{Outcome: smart.SelfTestOutcomePassed}, want: smartSelfTestSchedulePassed},
		{entry: &smart.SelfTestEntry{Outcome: smart.SelfTestOutcomeFailed}, want: smartSelfTestScheduleFailed},
		{entry: &smart.SelfTestEntry{Outcome: smart.SelfTestOutcomeAborted}, want: smartSelfTestScheduleAborted},
		{execution: "failed_read", want: smartSelfTestScheduleFailed},
		{execution: "aborted_by_host", want: smartSelfTestScheduleAborted},
		{execution: "unknown", want: smartSelfTestScheduleUnknown},
	}
	for _, test := range tests {
		if got := scheduledSelfTestTerminalStatus(test.entry, test.execution); got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

func TestSelfTestScheduleCRUD(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	ctx := context.Background()
	created, err := service.CreateSelfTestSchedule(ctx, SelfTestScheduleInput{
		Device:   "/dev/ada0",
		TestType: "short",
		CronExpr: "0 2 * * *",
		Enabled:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Device != "ada0" || created.DiskKey == "" || created.Model != "Test Disk" || created.NextRunAt == nil || created.NextRunAt.Location() != time.UTC {
		t.Fatalf("created: %+v", created)
	}
	schedules, err := service.ListSelfTestSchedules(ctx)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("list: %+v, %v", schedules, err)
	}
	updated, err := service.UpdateSelfTestSchedule(ctx, created.ID, SelfTestScheduleInput{
		Device:   "ada0",
		TestType: "extended",
		CronExpr: "0 3 * * 0",
		Enabled:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.TestType != "extended" || updated.CronExpr != "0 3 * * 0" || updated.Enabled || updated.NextRunAt == nil || updated.NextRunAt.Location() != time.UTC {
		t.Fatalf("updated: %+v", updated)
	}
	event := models.DiskSmartSelfTestEvent{EventKey: "delete", ScheduleID: created.ID, DiskKey: created.DiskKey, Device: created.Device, TestType: "short", Condition: "self_test_started", Severity: "info", Title: "started"}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSelfTestSchedule(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	schedules, err = service.ListSelfTestSchedules(ctx)
	if err != nil || len(schedules) != 0 {
		t.Fatalf("list after delete: %+v, %v", schedules, err)
	}
	var eventCount int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Where("schedule_id = ?", created.ID).Count(&eventCount).Error; err != nil || eventCount != 0 {
		t.Fatalf("events=%d err=%v", eventCount, err)
	}
}

func TestSelfTestScheduleCreateDisabled(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	created, err := service.CreateSelfTestSchedule(context.Background(), SelfTestScheduleInput{
		Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Enabled {
		t.Fatalf("created=%+v", created)
	}
	var record models.DiskSmartSelfTestSchedule
	if err := service.DB.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Enabled {
		t.Fatalf("record=%+v", record)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
}

func TestSelfTestScheduleMutationsShareDatabaseLease(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	second := &Service{DB: service.DB, selfTestDriver: backend, physicalDiskSource: service.physicalDiskSource}
	token, acquired, err := service.acquireSelfTestSchedulerLease(context.Background(), time.Now().UTC())
	if err != nil || !acquired {
		t.Fatalf("token=%q acquired=%t err=%v", token, acquired, err)
	}
	if _, err := second.CreateSelfTestSchedule(context.Background(), SelfTestScheduleInput{Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true}); !errors.Is(err, ErrSelfTestSchedulerBusy) {
		t.Fatalf("err=%v", err)
	}
	service.releaseSelfTestSchedulerLease(context.Background(), token)
	if _, err := second.CreateSelfTestSchedule(context.Background(), SelfTestScheduleInput{Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSelfTestScheduleCanDisableUnavailableDisk(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	created, err := service.CreateSelfTestSchedule(context.Background(), SelfTestScheduleInput{
		Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return nil, errors.New("inventory unavailable")
	}
	updated, err := service.UpdateSelfTestSchedule(context.Background(), created.ID, SelfTestScheduleInput{
		Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.LastStatus != smartSelfTestScheduleIdle {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestSelfTestScheduleRejectsAmbiguousDiskIdentity(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{
			{Name: "ada0", Description: "First", Serial: "DUPLICATE", Type: "SSD"},
			{Name: "ada1", Description: "Second", Serial: "DUPLICATE", Type: "SSD"},
		}, nil
	}
	_, err := service.CreateSelfTestSchedule(context.Background(), SelfTestScheduleInput{
		Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true,
	})
	if !errors.Is(err, ErrInvalidSelfTestSchedule) {
		t.Fatalf("err=%v", err)
	}
}

func TestSelfTestSchedulerRunsAndReconciles(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey:     key,
		Device:      "ada0",
		Model:       disk.Description,
		Serial:      disk.Serial,
		TestType:    "short",
		CronExpr:    "0 2 * * *",
		Enabled:     true,
		NextRunAt:   &due,
		LastStatus:  smartSelfTestScheduleIdle,
		ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	id := record.ID
	record = models.DiskSmartSelfTestSchedule{}
	if err := service.DB.First(&record, id).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleRunning || record.LastRunAt == nil || len(backend.starts) != 1 || backend.starts[0] != smart.SelfTestKindShort {
		t.Fatalf("started: record=%+v starts=%+v", record, backend.starts)
	}
	var run models.DiskSmartSelfTestRun
	if err := service.DB.Where("schedule_id = ?", record.ID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Source != smartSelfTestRunSourceScheduled || run.Status != smartSelfTestRunRunning || !run.StartedAt.Equal(now) {
		t.Fatalf("started_run=%+v", run)
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.status.ExecutionStatus = "in_progress"
	backend.status.ProgressPct = 40
	backend.status.ProgressKnown = true
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !record.RunningObserved || !record.ProgressKnown || record.ProgressPct != 40 {
		t.Fatalf("progress: %+v", record)
	}
	backend.mu.Lock()
	backend.status.Running = false
	backend.status.State = smart.SelfTestStateIdle
	backend.status.ExecutionStatus = "completed"
	backend.status.ProgressKnown = false
	backend.status.ProgressPct = -1
	backend.status.Results = []smart.SelfTestEntry{
		{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, RemainingPct: -1, LifetimeHours: 11},
	}
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestSchedulePassed || record.LastResultFingerprint == "" || !record.ProgressKnown || record.ProgressPct != 100 {
		t.Fatalf("completed: %+v", record)
	}
	if err := service.DB.First(&run, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != smartSelfTestRunPassed || run.CompletedAt == nil || run.ResultFingerprint == "" || run.ResultData == "" {
		t.Fatalf("completed_run=%+v", run)
	}
}

func TestSelfTestSchedulerRetriesPersistedNotification(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	notifier.SetEmitter(nil)
	defer notifier.SetEmitter(nil)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d", pending)
	}
	emitter := &fakeScheduleEventEmitter{}
	notifier.SetEmitter(emitter)
	if err := service.runSelfTestEventRelayBatch(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&pending).Error; err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if pending != 0 || len(emitter.events) != 1 || emitter.events[0].Kind != notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, key) {
		t.Fatalf("pending=%d events=%+v", pending, emitter.events)
	}
}

func TestSelfTestSchedulerJobSeparatesEnqueueFromExecution(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	var queued smartSelfTestSchedulerJob
	service.selfTestJobEnqueue = func(_ context.Context, job smartSelfTestSchedulerJob) error {
		queued = job
		return nil
	}
	if err := service.enqueueSelfTestSchedulerJob(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if queued.DispatchToken == "" || !queued.RequestedAt.Equal(now) || len(backend.starts) != 0 {
		t.Fatalf("job=%+v starts=%d", queued, len(backend.starts))
	}
	if err := service.handleSelfTestSchedulerJob(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	if len(backend.starts) != 1 || backend.starts[0] != smart.SelfTestKindShort {
		t.Fatalf("starts=%v", backend.starts)
	}
	if err := service.handleSelfTestSchedulerJob(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	if len(backend.starts) != 1 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestSelfTestSchedulerJobReservationRecovers(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	service.selfTestJobEnqueue = func(context.Context, smartSelfTestSchedulerJob) error {
		return errors.New("queue unavailable")
	}
	if err := service.enqueueSelfTestSchedulerJob(context.Background(), now); err == nil {
		t.Fatal("expected_enqueue_error")
	}
	var lease models.DiskSmartSelfTestSchedulerLease
	if err := service.DB.First(&lease, 1).Error; err != nil {
		t.Fatal(err)
	}
	if lease.DispatchToken != "" || lease.DispatchedAt != nil {
		t.Fatalf("lease=%+v", lease)
	}
	var jobs []smartSelfTestSchedulerJob
	service.selfTestJobEnqueue = func(_ context.Context, job smartSelfTestSchedulerJob) error {
		jobs = append(jobs, job)
		return nil
	}
	if err := service.enqueueSelfTestSchedulerJob(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.enqueueSelfTestSchedulerJob(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	if err := service.enqueueSelfTestSchedulerJob(context.Background(), now.Add(smartSelfTestDispatchLease+time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].DispatchToken == jobs[1].DispatchToken {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestSelfTestSchedulerJobRejectsNotReadyAndStale(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	lease := models.DiskSmartSelfTestSchedulerLease{ID: 1, DispatchToken: "not-ready", DispatchedAt: &now}
	if err := service.DB.Create(&lease).Error; err != nil {
		t.Fatal(err)
	}
	service.SetSelfTestSchedulerReady(false)
	if err := service.handleSelfTestSchedulerJob(context.Background(), smartSelfTestSchedulerJob{RequestedAt: now, DispatchToken: "not-ready"}); err != nil {
		t.Fatal(err)
	}
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
	stale := now.Add(-smartSelfTestDispatchLease - time.Second)
	if err := service.DB.Model(&models.DiskSmartSelfTestSchedulerLease{}).Where("id = ?", 1).
		Updates(map[string]any{"dispatch_token": "stale", "dispatched_at": stale}).Error; err != nil {
		t.Fatal(err)
	}
	service.SetSelfTestSchedulerReady(true)
	if err := service.handleSelfTestSchedulerJob(context.Background(), smartSelfTestSchedulerJob{RequestedAt: stale, DispatchToken: "stale"}); err != nil {
		t.Fatal(err)
	}
	lease = models.DiskSmartSelfTestSchedulerLease{}
	if err := service.DB.First(&lease, 1).Error; err != nil {
		t.Fatal(err)
	}
	if lease.DispatchToken != "" || lease.DispatchedAt != nil || len(backend.starts) != 0 {
		t.Fatalf("lease=%+v starts=%d", lease, len(backend.starts))
	}
}

func TestSelfTestEventDeliveryClaimsOnceAcrossServices(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	secondService := &Service{DB: service.DB}
	event := models.DiskSmartSelfTestEvent{
		ScheduleID: 1,
		DiskKey:    "disk-key",
		Device:     "ada0",
		TestType:   "short",
		Condition:  "self_test_passed",
		Severity:   "info",
		Title:      "Self-test passed",
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	emitter := &fakeScheduleEventEmitter{started: started, release: release}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	now := time.Now().UTC()
	errorsByWorker := make(chan error, 2)
	go func() { errorsByWorker <- service.runSelfTestEventRelayBatch(context.Background(), now) }()
	<-started
	go func() { errorsByWorker <- secondService.runSelfTestEventRelayBatch(context.Background(), now) }()
	close(release)
	for range 2 {
		if err := <-errorsByWorker; err != nil {
			t.Fatal(err)
		}
	}
	var pending int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&pending).Error; err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if pending != 0 || len(emitter.events) != 1 {
		t.Fatalf("pending=%d events=%d", pending, len(emitter.events))
	}
}

func TestSelfTestEventDeliveryReleasesFailedClaim(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	event := models.DiskSmartSelfTestEvent{
		ScheduleID: 1,
		DiskKey:    "disk-key",
		Device:     "ada0",
		TestType:   "short",
		Condition:  "self_test_failed",
		Severity:   "critical",
		Title:      "Self-test failed",
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	emitter := &fakeScheduleEventEmitter{err: errors.New("transport unavailable")}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	if err := service.runSelfTestEventRelayBatch(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ClaimToken != "" || stored.ClaimedAt != nil || stored.AttemptCount != 1 || stored.NextAttemptAt == nil {
		t.Fatalf("stored=%+v", stored)
	}
	emitter.err = nil
	if err := service.runSelfTestEventRelayBatch(context.Background(), stored.NextAttemptAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
}

func TestSelfTestEventRelayReleasesClaimAfterEnqueueFailure(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	event := models.DiskSmartSelfTestEvent{
		EventKey: "enqueue-failure", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0",
		TestType: "short", Condition: "self_test_failed", Severity: "critical", Title: "Self-test failed",
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	service.selfTestEventEnqueue = func(context.Context, smartSelfTestEventJob) error {
		return errors.New("queue unavailable")
	}
	if err := service.runSelfTestEventRelayBatch(context.Background(), time.Now().UTC()); err == nil {
		t.Fatal("expected_enqueue_error")
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ClaimToken != "" || stored.ClaimedAt != nil || stored.AttemptCount != 0 || stored.NextAttemptAt != nil {
		t.Fatalf("event=%+v", stored)
	}
}

func TestSelfTestEventHandlerAcknowledgesDatabaseFailure(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC()
	event := models.DiskSmartSelfTestEvent{
		EventKey: "retained-claim", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0",
		TestType: "short", Condition: "self_test_failed", Severity: "critical", Title: "Self-test failed",
		ClaimToken: "claim", ClaimedAt: &now,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.handleSelfTestEventJob(ctx, smartSelfTestEventJob{EventID: event.ID, ClaimToken: "claim"}); err != nil {
		t.Fatalf("err=%v", err)
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ClaimToken != "claim" || stored.ClaimedAt == nil {
		t.Fatalf("event=%+v", stored)
	}
}

func TestSelfTestEventHandlerClaimsExecutionOnce(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC()
	event := models.DiskSmartSelfTestEvent{
		EventKey: "single-execution", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0",
		TestType: "short", Condition: "self_test_passed", Severity: "info", Title: "Self-test passed",
		ClaimToken: "claim", ClaimedAt: &now,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	emitter := &fakeScheduleEventEmitter{started: started, release: release}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	job := smartSelfTestEventJob{EventID: event.ID, ClaimToken: "claim"}
	results := make(chan error, 2)
	go func() { results <- service.handleSelfTestEventJob(context.Background(), job) }()
	<-started
	go func() { results <- service.handleSelfTestEventJob(context.Background(), job) }()
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	deliveries := len(emitter.events)
	emitter.mu.Unlock()
	if deliveries != 1 {
		t.Fatalf("deliveries=%d", deliveries)
	}
}

func TestManualSelfTestEventUsesSharedDeliveryJob(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC()
	event := models.DiskSmartSelfTestEvent{
		EventKey: "manual-run|self_test_passed",
		RunKey:   "manual-run", Source: smartSelfTestRunSourceManual,
		DiskKey: "disk-key", Device: "ada0", TestType: "short", Condition: "self_test_passed",
		Severity: "info", Title: "Self-test passed", ClaimToken: "claim", ClaimedAt: &now,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	emitter := &fakeScheduleEventEmitter{}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	if err := service.handleSelfTestEventJob(context.Background(), smartSelfTestEventJob{EventID: event.ID, ClaimToken: "claim"}); err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if len(emitter.events) != 1 || emitter.events[0].Metadata["condition"] != "self_test_passed" || emitter.events[0].Metadata["source"] != smartSelfTestRunSourceManual || emitter.events[0].Metadata["run_key"] != "manual-run" {
		t.Fatalf("events=%+v", emitter.events)
	}
}

func TestSelfTestEventRetryStartsAfterDelivery(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	past := time.Now().UTC().Add(-time.Hour)
	event := models.DiskSmartSelfTestEvent{
		EventKey: "retry-after-delivery", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0",
		TestType: "short", Condition: "self_test_failed", Severity: "critical", Title: "Self-test failed",
		ClaimToken: "claim", ClaimedAt: &past,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	emitter := &fakeScheduleEventEmitter{err: errors.New("transport unavailable")}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	completedAfter := time.Now().UTC()
	if err := service.deliverClaimedSelfTestEvent(context.Background(), &event, "claim", past); err == nil {
		t.Fatal("expected_delivery_error")
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.NextAttemptAt == nil || !stored.NextAttemptAt.After(completedAfter) {
		t.Fatalf("next_attempt_at=%v completed_after=%v", stored.NextAttemptAt, completedAfter)
	}
}

func TestSelfTestEventDeliveryRetriesOnlyFailedTarget(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	event := models.DiskSmartSelfTestEvent{
		EventKey:   "target-retry",
		ScheduleID: 1,
		DiskKey:    "disk-key",
		Device:     "ada0",
		TestType:   "short",
		Condition:  "self_test_failed",
		Severity:   "critical",
		Title:      "Self-test failed",
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	emitter := &fakeTargetedScheduleEventEmitter{
		targets:      []string{"ntfy:1", "ntfy:2"},
		targetErrors: map[string]error{"ntfy:2": errors.New("unavailable")},
	}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	now := time.Now().UTC()
	if err := service.runSelfTestEventRelayBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	delete(emitter.targetErrors, "ntfy:2")
	if err := service.runSelfTestEventRelayBatch(context.Background(), stored.NextAttemptAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.targetCalls["ntfy:1"] != 1 || emitter.targetCalls["ntfy:2"] != 2 {
		t.Fatalf("calls=%v", emitter.targetCalls)
	}
}

func TestSelfTestEventCoalescesOlderLifecycleState(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	schedule := models.DiskSmartSelfTestSchedule{
		DiskKey:       "disk-key",
		Device:        "ada0",
		TestType:      "short",
		CronExpr:      "0 2 * * *",
		OccurrenceKey: "occurrence",
		LastStatus:    smartSelfTestScheduleRunning,
		ProgressPct:   -1,
	}
	started := newScheduledSelfTestEvent(schedule, "self_test_started", "info", "started", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &started); err != nil {
		t.Fatal(err)
	}
	schedule.LastStatus = smartSelfTestSchedulePassed
	passed := newScheduledSelfTestEvent(schedule, "self_test_passed", "info", "passed", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &passed); err != nil {
		t.Fatal(err)
	}
	var events []models.DiskSmartSelfTestEvent
	if err := service.DB.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Condition != "self_test_passed" {
		t.Fatalf("events=%+v", events)
	}
}

func TestSelfTestEventPreservesPreviousOccurrence(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	schedule := models.DiskSmartSelfTestSchedule{
		DiskKey:       "disk-key",
		Device:        "ada0",
		TestType:      "short",
		CronExpr:      "0 2 * * *",
		OccurrenceKey: "previous%_occurrence",
		LastStatus:    smartSelfTestScheduleFailed,
		ProgressPct:   -1,
	}
	previousStarted := newScheduledSelfTestEvent(schedule, "self_test_started", "info", "started", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &previousStarted); err != nil {
		t.Fatal(err)
	}
	failed := newScheduledSelfTestEvent(schedule, "self_test_failed", "critical", "failed", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &failed); err != nil {
		t.Fatal(err)
	}
	schedule.OccurrenceKey = "current_occurrence"
	schedule.LastStatus = smartSelfTestScheduleRunning
	started := newScheduledSelfTestEvent(schedule, "self_test_started", "info", "started", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &started); err != nil {
		t.Fatal(err)
	}
	var events []models.DiskSmartSelfTestEvent
	if err := service.DB.Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Condition != "self_test_failed" || events[1].Condition != "self_test_started" {
		t.Fatalf("events=%+v", events)
	}
}

func TestSelfTestEventOrderingUsesEarlierRetryTime(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	retryAt := now.Add(time.Hour)
	events := []models.DiskSmartSelfTestEvent{
		{
			EventKey: "previous", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0", TestType: "short",
			Condition: "self_test_failed", Severity: "critical", Title: "failed", NextAttemptAt: &retryAt,
		},
		{
			EventKey: "current", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0", TestType: "short",
			Condition: "self_test_started", Severity: "info", Title: "started",
		},
	}
	if err := service.DB.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestEventRelayBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var current models.DiskSmartSelfTestEvent
	if err := service.DB.First(&current, events[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.NextAttemptAt == nil || !current.NextAttemptAt.Equal(retryAt) || current.ClaimedAt != nil || current.ClaimToken != "" {
		t.Fatalf("event=%+v", current)
	}
}

func TestSelfTestTerminalEventWaitsForClaimedEarlierLifecycle(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC()
	schedule := models.DiskSmartSelfTestSchedule{
		DiskKey:       "disk-key",
		Device:        "ada0",
		TestType:      "short",
		CronExpr:      "0 2 * * *",
		OccurrenceKey: "occurrence",
		LastStatus:    smartSelfTestScheduleRunning,
		ProgressPct:   -1,
	}
	started := newScheduledSelfTestEvent(schedule, "self_test_started", "info", "started", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &started); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&started).Updates(map[string]any{"claim_token": "earlier", "claimed_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	schedule.LastStatus = smartSelfTestSchedulePassed
	passed := newScheduledSelfTestEvent(schedule, "self_test_passed", "info", "passed", "")
	if err := service.saveScheduledSelfTestWithEvent(context.Background(), &schedule, &passed); err != nil {
		t.Fatal(err)
	}
	emitter := &fakeScheduleEventEmitter{}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	if err := service.runSelfTestEventRelayBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	firstCount := len(emitter.events)
	emitter.mu.Unlock()
	if firstCount != 0 {
		t.Fatalf("events=%d", firstCount)
	}
	if err := service.deleteClaimedSelfTestEvent(context.Background(), &started, "earlier"); err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestEventRelayBatch(context.Background(), now.Add(smartSelfTestEventDispatchInterval+time.Second)); err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if len(emitter.events) != 1 || emitter.events[0].Metadata["condition"] != "self_test_passed" {
		t.Fatalf("events=%+v", emitter.events)
	}
}

func TestSelfTestEventBatchCleansStaleSupersededClaim(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	now := time.Now().UTC()
	stale := now.Add(-smartSelfTestEventClaimLease - time.Minute)
	event := models.DiskSmartSelfTestEvent{
		EventKey: "stale", ScheduleID: 1, DiskKey: "disk-key", Device: "ada0", TestType: "short",
		Condition: "self_test_started", Severity: "info", Title: "started", ClaimToken: "stale",
		ClaimedAt: &stale, SupersededAt: &stale,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestEventRelayBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := service.DB.Model(&models.DiskSmartSelfTestEvent{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestSelfTestEventDeadLettersAfterMaximumAttempts(t *testing.T) {
	service, _ := makeSelfTestScheduleService(t)
	event := models.DiskSmartSelfTestEvent{
		EventKey:     "dead-letter",
		ScheduleID:   1,
		DiskKey:      "disk-key",
		Device:       "ada0",
		TestType:     "short",
		Condition:    "self_test_failed",
		Severity:     "critical",
		Title:        "Self-test failed",
		AttemptCount: smartSelfTestEventMaxAttempts - 1,
	}
	if err := service.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	emitter := &fakeScheduleEventEmitter{err: errors.New("unavailable")}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(nil)
	now := time.Now().UTC()
	if err := service.runSelfTestEventRelayBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var stored models.DiskSmartSelfTestEvent
	if err := service.DB.First(&stored, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DeadLetteredAt == nil || stored.AttemptCount != smartSelfTestEventMaxAttempts {
		t.Fatalf("event=%+v", stored)
	}
	if err := service.runSelfTestEventRelayBatch(context.Background(), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if len(emitter.events) != 1 {
		t.Fatalf("events=%d", len(emitter.events))
	}
}

func TestSelfTestSchedulerSerializesQueuedTests(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	for _, device := range []string{"ada0", "ada1"} {
		disk, err := service.resolvePhysicalDisk(device)
		if err != nil {
			t.Fatal(err)
		}
		key, err := selfTestScheduleDiskKey(disk)
		if err != nil {
			t.Fatal(err)
		}
		due := now.Add(-time.Minute)
		record := models.DiskSmartSelfTestSchedule{
			DiskKey: key, Device: device, Model: disk.Description, Serial: disk.Serial,
			TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
			LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
		}
		if err := service.DB.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var running int64
	var queued int64
	service.DB.Model(&models.DiskSmartSelfTestSchedule{}).Where("last_status = ?", smartSelfTestScheduleRunning).Count(&running)
	service.DB.Model(&models.DiskSmartSelfTestSchedule{}).Where("last_status = ?", smartSelfTestScheduleQueued).Count(&queued)
	if running != 1 || queued != 1 || len(backend.starts) != 1 {
		t.Fatalf("running=%d queued=%d starts=%d", running, queued, len(backend.starts))
	}
}

func TestSelfTestSchedulerRotatesExternallyBusyQueue(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	queuedAt := now.Add(-time.Minute)
	next := now.Add(24 * time.Hour)
	for _, device := range []string{"ada0", "ada1"} {
		disk, err := service.resolvePhysicalDisk(device)
		if err != nil {
			t.Fatal(err)
		}
		key, err := selfTestScheduleDiskKey(disk)
		if err != nil {
			t.Fatal(err)
		}
		record := models.DiskSmartSelfTestSchedule{
			DiskKey: key, Device: device, Model: disk.Description, Serial: disk.Serial,
			TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
			QueuedAt: &queuedAt, LastStatus: smartSelfTestScheduleQueued, ProgressPct: -1,
		}
		if err := service.DB.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	backend.mu.Lock()
	busy := backend.status
	busy.Running = true
	busy.State = smart.SelfTestStateRunning
	busy.ExecutionStatus = "in_progress"
	backend.statuses = map[string]smart.SelfTestStatus{"ada0": busy}
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(backend.devices) != 1 || backend.devices[0] != "ada1" {
		t.Fatalf("devices=%v", backend.devices)
	}
}

func TestSelfTestSchedulerExpiresExternallyBusyQueue(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	queuedAt := now.Add(-14 * time.Minute)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		QueuedAt: &queuedAt, LastStatus: smartSelfTestScheduleQueued, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var first models.DiskSmartSelfTestSchedule
	if err := service.DB.First(&first, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.LastStatus != smartSelfTestScheduleQueued || first.QueuedAt == nil || !first.QueuedAt.Equal(queuedAt) {
		t.Fatalf("first=%+v", first)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var expired models.DiskSmartSelfTestSchedule
	if err := service.DB.First(&expired, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if expired.LastStatus != smartSelfTestScheduleMissed || expired.QueuedAt != nil || !strings.Contains(expired.LastError, "already running") {
		t.Fatalf("expired=%+v", expired)
	}
}

func TestSelfTestSchedulerKeepsQueuedRunAliveBehindScheduledRun(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	queuedAt := now.Add(-time.Hour)
	next := now.Add(24 * time.Hour)
	for i, device := range []string{"ada0", "ada1"} {
		disk, err := service.resolvePhysicalDisk(device)
		if err != nil {
			t.Fatal(err)
		}
		key, err := selfTestScheduleDiskKey(disk)
		if err != nil {
			t.Fatal(err)
		}
		status := smartSelfTestScheduleQueued
		var lastRunAt *time.Time
		if i == 0 {
			status = smartSelfTestScheduleRunning
			started := now.Add(-time.Minute)
			lastRunAt = &started
		}
		record := models.DiskSmartSelfTestSchedule{
			DiskKey: key, Device: device, Model: disk.Description, Serial: disk.Serial,
			TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
			QueuedAt: &queuedAt, LastRunAt: lastRunAt, LastStatus: status, ProgressPct: -1, EstimatedMinutes: 2,
		}
		if status == smartSelfTestScheduleRunning {
			record.QueuedAt = nil
		}
		if err := service.DB.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var queued models.DiskSmartSelfTestSchedule
	if err := service.DB.Where("device = ?", "ada1").First(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if queued.LastStatus != smartSelfTestScheduleQueued || queued.QueuedAt == nil || !queued.QueuedAt.Equal(queuedAt) || queued.QueueUpdatedAt == nil || !queued.QueueUpdatedAt.Equal(now) {
		t.Fatalf("queued=%+v", queued)
	}
}

func TestSelfTestSchedulerConcurrentTicksStartOnce(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	secondService := &Service{DB: service.DB, selfTestDriver: backend, physicalDiskSource: service.physicalDiskSource}
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, scheduler := range []*Service{service, secondService} {
		wait.Add(1)
		go func(service *Service) {
			defer wait.Done()
			errs <- service.runSelfTestSchedulerTick(context.Background(), now)
		}(scheduler)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(backend.starts) != 1 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
}

func TestSelfTestSchedulerSkipsStaleCatchup(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	id := record.ID
	record = models.DiskSmartSelfTestSchedule{}
	if err := service.DB.First(&record, id).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleMissed || len(backend.starts) != 0 || record.NextRunAt == nil || !record.NextRunAt.After(now) {
		t.Fatalf("missed: record=%+v starts=%d", record, len(backend.starts))
	}
}

func TestSelfTestSchedulerSkipsStaleQueuedRun(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	queuedAt := now.Add(-time.Hour)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		QueuedAt: &queuedAt, LastStatus: smartSelfTestScheduleQueued, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	id := record.ID
	record = models.DiskSmartSelfTestSchedule{}
	if err := service.DB.First(&record, id).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleMissed || record.QueuedAt != nil || len(backend.starts) != 0 {
		t.Fatalf("record=%+v starts=%d", record, len(backend.starts))
	}
}

func TestSelfTestSchedulerDisablesInvalidSchedule(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "invalid", Enabled: true, NextRunAt: &due,
		LastStatus: smartSelfTestScheduleIdle, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	id := record.ID
	record = models.DiskSmartSelfTestSchedule{}
	if err := service.DB.First(&record, id).Error; err != nil {
		t.Fatal(err)
	}
	if record.Enabled || record.LastStatus != smartSelfTestScheduleFailed || record.NextRunAt != nil || len(backend.starts) != 0 {
		t.Fatalf("record=%+v starts=%d", record, len(backend.starts))
	}
}

func TestSelfTestSchedulerReleasesUnavailableRun(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-7 * time.Hour)
	next := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleRunning, ProgressPct: -1, EstimatedMinutes: 2,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.readErr = errors.New("unavailable")
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleUnknown || record.LastError == "" {
		t.Fatalf("record: %+v", record)
	}
}

func TestSelfTestSchedulerDoesNotAssumePassWithoutResultLog(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-5 * time.Minute)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleRunning, ProgressPct: -1,
		EstimatedMinutes: 2, RunningObserved: true,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.capability.Protocol = "SCSI"
	backend.capability.ResultLog = false
	backend.status.Protocol = "SCSI"
	backend.status.State = smart.SelfTestStateIdle
	backend.status.Running = false
	backend.status.ExecutionStatus = ""
	backend.status.Results = nil
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleUnknown || !strings.Contains(record.LastError, "result log") {
		t.Fatalf("record=%+v", record)
	}
}

func TestSelfTestSchedulerRecoversStartingRun(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-time.Minute)
	next := now.Add(-time.Minute)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleStarting, ProgressPct: -1, EstimatedMinutes: 2,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.status.ExecutionStatus = "in_progress"
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleRunning || !record.RunningObserved || len(backend.starts) != 0 {
		t.Fatalf("record=%+v starts=%d", record, len(backend.starts))
	}
}

func TestSelfTestSchedulerDoesNotAttributeAnotherTestType(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	oldExtended := smart.SelfTestEntry{Protocol: "ATA", Type: "extended", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 10}
	baselineStatus := &smart.SelfTestStatus{Results: []smart.SelfTestEntry{oldExtended}}
	_, baseline, _ := latestScheduledSelfTestResult(baselineStatus, "extended")
	started := now.Add(-time.Minute)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "extended", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleRunning, ProgressPct: -1,
		EstimatedMinutes: 120, RunningObserved: true, BaselineFingerprint: baseline,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = false
	backend.status.State = smart.SelfTestStateIdle
	backend.status.Type = ""
	backend.status.ExecutionStatus = "completed"
	backend.status.Results = []smart.SelfTestEntry{
		{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed, LifetimeHours: 11},
		oldExtended,
	}
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleUnknown {
		t.Fatalf("record=%+v", record)
	}
}

func TestSelfTestSchedulerAbortsTimedOutRunOnce(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-7 * time.Hour)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleRunning, ProgressPct: -1, EstimatedMinutes: 2,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.status.ExecutionStatus = "in_progress"
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleUnknown || backend.stops != 1 {
		t.Fatalf("record=%+v stops=%d", record, backend.stops)
	}
}

func TestSelfTestSchedulerDoesNotRepeatFailedTimeoutAbort(t *testing.T) {
	service, backend := makeSelfTestScheduleService(t)
	now := time.Now().Truncate(time.Second)
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-7 * time.Hour)
	next := now.Add(24 * time.Hour)
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: disk.Name, Model: disk.Description, Serial: disk.Serial,
		TestType: "short", CronExpr: "0 2 * * *", Enabled: true, NextRunAt: &next,
		LastRunAt: &started, LastStatus: smartSelfTestScheduleRunning, ProgressPct: -1, EstimatedMinutes: 2,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = true
	backend.status.State = smart.SelfTestStateRunning
	backend.status.ExecutionStatus = "in_progress"
	backend.stopErr = errors.New("abort failed")
	backend.mu.Unlock()
	if err := service.runSelfTestSchedulerTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := service.runSelfTestSchedulerTick(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleUnknown || record.TimeoutAbortAttempted || backend.stops != 1 {
		t.Fatalf("record=%+v stops=%d", record, backend.stops)
	}
}
