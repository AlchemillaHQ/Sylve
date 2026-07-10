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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

type manualSelfTestBackend struct {
	mu                sync.Mutex
	reads             atomic.Int32
	starts            []smart.SelfTestKind
	stops             int
	startLeavesStatus bool
	statusAfterStart  *smart.SelfTestStatus
	statusAfterStop   *smart.SelfTestStatus
	readStarted       chan struct{}
	readRelease       chan struct{}
	capabilities      smart.SelfTestCapabilities
	status            smart.SelfTestStatus
}

func (b *manualSelfTestBackend) Read(string) (*smart.SelfTestCapabilities, *smart.SelfTestStatus, error) {
	b.reads.Add(1)
	if b.readStarted != nil {
		select {
		case b.readStarted <- struct{}{}:
		default:
		}
	}
	if b.readRelease != nil {
		<-b.readRelease
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	capabilities := b.capabilities
	status := b.status
	status.Results = append([]smart.SelfTestEntry(nil), b.status.Results...)
	return &capabilities, &status, nil
}

func (b *manualSelfTestBackend) Start(_ string, kind smart.SelfTestKind) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts = append(b.starts, kind)
	if b.statusAfterStart != nil {
		b.status = *b.statusAfterStart
		b.status.Results = append([]smart.SelfTestEntry(nil), b.statusAfterStart.Results...)
		return nil
	}
	if b.startLeavesStatus {
		return nil
	}
	b.status.State = smart.SelfTestStateRunning
	b.status.ExecutionStatus = smart.SelfTestOutcomeInProgress
	b.status.Type = kind
	b.status.Running = true
	b.status.ProgressPct = 0
	b.status.ProgressKnown = true
	b.status.RemainingPct = 100
	b.status.RemainingKnown = true
	return nil
}

func (b *manualSelfTestBackend) Stop(string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stops++
	if b.statusAfterStop != nil {
		b.status = *b.statusAfterStop
		b.status.Results = append([]smart.SelfTestEntry(nil), b.statusAfterStop.Results...)
		return nil
	}
	b.status.State = smart.SelfTestStateIdle
	b.status.ExecutionStatus = smart.SelfTestOutcomeAborted
	b.status.Type = ""
	b.status.Running = false
	b.status.ProgressPct = -1
	b.status.ProgressKnown = false
	b.status.RemainingPct = -1
	b.status.RemainingKnown = false
	return nil
}

func newManualSelfTestService(backend selfTestBackend) *Service {
	return &Service{
		selfTestDriver:   backend,
		selfTestCache:    make(map[string]selfTestCacheEntry),
		selfTestCacheTTL: time.Hour,
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{{Name: "ada0", Type: "SSD"}}, nil
		},
	}
}

func TestResolvePhysicalDisk(t *testing.T) {
	service := newManualSelfTestService(&manualSelfTestBackend{})
	for _, device := range []string{"ada0", "/dev/ada0", " ada0 "} {
		disk, err := service.resolvePhysicalDisk(device)
		if err != nil || disk.Name != "ada0" {
			t.Fatalf("device=%q disk=%+v err=%v", device, disk, err)
		}
	}
	for _, device := range []string{"", "/dev/", "/dev/../ada0", "ada0/../../da0", ".."} {
		if _, err := service.resolvePhysicalDisk(device); !errors.Is(err, ErrInvalidPhysicalDisk) {
			t.Fatalf("device=%q err=%v", device, err)
		}
	}
	if _, err := service.resolvePhysicalDisk("ada1"); !errors.Is(err, ErrPhysicalDiskNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestGetSelfTestInfoCachesAndCoalesces(t *testing.T) {
	backend := &manualSelfTestBackend{
		readStarted: make(chan struct{}, 1),
		readRelease: make(chan struct{}),
		capabilities: smart.SelfTestCapabilities{
			Protocol:                "ATA",
			Scope:                   "device",
			Supported:               true,
			Short:                   true,
			Extended:                true,
			Conveyance:              true,
			Abort:                   true,
			ResultLog:               true,
			Progress:                true,
			ShortDurationMinutes:    2,
			ExtendedDurationMinutes: 90,
		},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "completed",
			ProgressPct:     -1,
			RemainingPct:    -1,
			ChecksumValid:   true,
			Results: []smart.SelfTestEntry{{
				Protocol:            "ATA",
				Type:                "extended",
				Mode:                "background",
				Status:              "failed_read",
				Outcome:             smart.SelfTestOutcomeFailed,
				RemainingPct:        0,
				LifetimeHours:       123,
				LBA:                 456,
				LBAValid:            true,
				NSID:                7,
				NSIDValid:           true,
				SegmentNum:          2,
				SenseKey:            3,
				ASC:                 4,
				ASCQ:                5,
				StatusCodeType:      6,
				StatusCodeTypeValid: true,
				StatusCode:          7,
				StatusCodeValid:     true,
				Checkpoint:          8,
				ParameterCode:       9,
				VendorSpecific:      10,
			}},
		},
	}
	service := newManualSelfTestService(backend)

	const callers = 16
	results := make(chan *diskServiceInterfaces.DiskSelfTestInfo, callers)
	errorsSeen := make(chan error, callers)
	var wait sync.WaitGroup
	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			info, err := service.GetSelfTestInfo("/dev/ada0")
			results <- info
			errorsSeen <- err
		}()
	}
	<-backend.readStarted
	close(backend.readRelease)
	wait.Wait()
	close(results)
	close(errorsSeen)

	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	for info := range results {
		if info == nil || info.Device != "ada0" || !info.Capabilities.Short || info.Capabilities.ExtendedDurationMinutes != 90 {
			t.Fatalf("info=%+v", info)
		}
		if len(info.Status.Results) != 1 {
			t.Fatalf("results=%+v", info.Status.Results)
		}
		entry := info.Status.Results[0]
		if entry.Outcome != smart.SelfTestOutcomeFailed || entry.LifetimeHoursExact != "123" || entry.LBA != 456 || entry.LBAExact != "456" || !entry.LBAValid || entry.NSID != 7 || !entry.NSIDValid || entry.StatusCode != 7 || !entry.StatusCodeValid || entry.ParameterCode != 9 || entry.VendorSpecific != 10 {
			t.Fatalf("entry=%+v", entry)
		}
	}
	if reads := backend.reads.Load(); reads != 1 {
		t.Fatalf("reads=%d", reads)
	}
	if _, err := service.GetSelfTestInfo("ada0"); err != nil {
		t.Fatal(err)
	}
	if reads := backend.reads.Load(); reads != 1 {
		t.Fatalf("cached reads=%d", reads)
	}
}

func TestSelfTestCommandsInvalidateCache(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Extended: true, Conveyance: true, Abort: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateIdle, ProgressPct: -1, RemainingPct: -1},
	}
	service := newManualSelfTestService(backend)
	if _, err := service.GetSelfTestInfo("ada0"); err != nil {
		t.Fatal(err)
	}
	started, err := service.StartSelfTest("ada0", "short")
	if err != nil {
		t.Fatal(err)
	}
	if !started.Status.Running || started.Status.Type != "short" {
		t.Fatalf("started=%+v", started.Status)
	}
	if reads := backend.reads.Load(); reads != 3 {
		t.Fatalf("reads after start=%d", reads)
	}
	if _, err := service.GetSelfTestInfo("ada0"); err != nil {
		t.Fatal(err)
	}
	if reads := backend.reads.Load(); reads != 3 {
		t.Fatalf("cached reads after start=%d", reads)
	}
	stopped, err := service.StopSelfTest("/dev/ada0")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status.Running || stopped.Status.State != smart.SelfTestStateIdle {
		t.Fatalf("stopped=%+v", stopped.Status)
	}
	if reads := backend.reads.Load(); reads != 4 {
		t.Fatalf("reads after stop=%d", reads)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 1 || backend.starts[0] != smart.SelfTestKindShort || backend.stops != 1 {
		t.Fatalf("starts=%v stops=%d", backend.starts, backend.stops)
	}
}

func TestReadSelfTestInfoRetainsStartedKindWhenDeviceOmitsIt(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "SCSI", Supported: true, Extended: true, ExtendedDurationMinutes: 60},
		status:       smart.SelfTestStatus{Protocol: "SCSI", State: smart.SelfTestStateRunning, Running: true, ProgressPct: -1, RemainingPct: -1},
	}
	service := newManualSelfTestService(backend)
	service.storeActiveSelfTestKind("ada0", smart.SelfTestKindExtended)
	info, err := service.readSelfTestInfo("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status.Type != "extended" || info.Status.EstimatedDurationMinutes != 60 {
		t.Fatalf("info=%+v", info)
	}
}

func TestReadSelfTestInfoPromotesTrailingActiveResult(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateRunning,
			ExecutionStatus: smart.SelfTestOutcomeInProgress,
			Type:            smart.SelfTestKindShort,
			Running:         true,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Status: "completed", Outcome: smart.SelfTestOutcomePassed},
				{Protocol: "ATA", Type: "short", Status: "in_progress", Outcome: smart.SelfTestOutcomeInProgress, RemainingPct: 80},
			},
		},
	}
	service := newManualSelfTestService(backend)
	info, err := service.readSelfTestInfo("ada0")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Status.Results) != 2 || info.Status.Results[0].Status != "in_progress" || info.Status.Results[1].Status != "completed" {
		t.Fatalf("results=%+v", info.Status.Results)
	}
}

func TestStartSelfTestNormalizesDelayedDeviceAcknowledgement(t *testing.T) {
	backend := &manualSelfTestBackend{
		startLeavesStatus: true,
		capabilities: smart.SelfTestCapabilities{
			Protocol:  "ATA",
			Supported: true,
			Short:     true,
			Abort:     true,
			Progress:  true,
		},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateIdle,
			ExecutionStatus: "aborted_by_host",
			ProgressPct:     -1,
			RemainingPct:    -1,
			Results: []smart.SelfTestEntry{
				{Protocol: "ATA", Type: "short", Status: "in_progress", Outcome: smart.SelfTestOutcomeInProgress, RemainingPct: 80},
			},
		},
	}
	service := newManualSelfTestService(backend)
	info, err := service.StartSelfTest("ada0", "short")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Status.Running || info.Status.State != smart.SelfTestStateRunning || info.Status.ExecutionStatus != smart.SelfTestOutcomeInProgress || info.Status.Type != "short" {
		t.Fatalf("status=%+v", info.Status)
	}
	if info.Status.ProgressKnown || info.Status.ProgressPct != -1 || info.Status.RemainingKnown || info.Status.RemainingPct != -1 {
		t.Fatalf("progress=%+v", info.Status)
	}
	if kind, ok := service.loadActiveSelfTestKind("ada0"); !ok || kind != smart.SelfTestKindShort {
		t.Fatalf("kind=%q active=%v", kind, ok)
	}
}

func TestStartSelfTestRejectsRunningDeviceTest(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true},
		status: smart.SelfTestStatus{
			Protocol:        "ATA",
			State:           smart.SelfTestStateRunning,
			ExecutionStatus: smart.SelfTestOutcomeInProgress,
			Type:            smart.SelfTestKindShort,
			Running:         true,
		},
	}
	service := newManualSelfTestService(backend)
	if _, err := service.StartSelfTest("ada0", "short"); !errors.Is(err, smart.ErrSelfTestInProgress) {
		t.Fatalf("err=%v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestManualSelfTestCannotReplaceActiveSchedule(t *testing.T) {
	backend := &manualSelfTestBackend{
		capabilities: smart.SelfTestCapabilities{Protocol: "ATA", Supported: true, Short: true, Abort: true},
		status:       smart.SelfTestStatus{Protocol: "ATA", State: smart.SelfTestStateRunning, Type: smart.SelfTestKindShort, Running: true, ProgressPct: 10, ProgressKnown: true},
	}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{{Name: "ada0", Type: "SSD", Serial: "SERIAL-0", LunID: "LUN-0"}}, nil
	}
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true, LastStatus: smartSelfTestScheduleRunning, ProgressPct: 10,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartSelfTest("ada0", "short"); !errors.Is(err, ErrSelfTestScheduleRunning) {
		t.Fatalf("err=%v", err)
	}
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
	if _, err := service.StopSelfTest("ada0"); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&record, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastStatus != smartSelfTestScheduleAborted || backend.stops != 1 {
		t.Fatalf("record=%+v stops=%d", record, backend.stops)
	}
}

func TestManualSelfTestCannotReplaceQueuedSchedule(t *testing.T) {
	backend := &manualSelfTestBackend{}
	service := newManualSelfTestService(backend)
	service.DB = testutil.NewSQLiteTestDB(t, &models.DiskSmartSelfTestSchedule{}, &models.DiskSmartSelfTestEvent{}, &models.DiskSmartSelfTestRun{}, &models.DiskSmartSelfTestSchedulerLease{})
	service.physicalDiskSource = func() ([]diskServiceInterfaces.DiskInfo, error) {
		return []diskServiceInterfaces.DiskInfo{{Name: "ada0", Type: "SSD", Serial: "SERIAL-0", LunID: "LUN-0"}}, nil
	}
	disk, err := service.resolvePhysicalDisk("ada0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		t.Fatal(err)
	}
	queuedAt := time.Now().UTC()
	record := models.DiskSmartSelfTestSchedule{
		DiskKey: key, Device: "ada0", TestType: "short", CronExpr: "0 2 * * *", Enabled: true, LastStatus: smartSelfTestScheduleQueued, QueuedAt: &queuedAt, ProgressPct: -1,
	}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartSelfTest("ada0", "short"); !errors.Is(err, ErrSelfTestScheduleRunning) {
		t.Fatalf("err=%v", err)
	}
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
}

func TestStartSelfTestContextDoesNotStartAfterCancellation(t *testing.T) {
	backend := &manualSelfTestBackend{}
	service := newManualSelfTestService(backend)
	lock := service.selfTestDeviceMutex("ada0")
	lock.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.StartSelfTestContext(ctx, "ada0", "short")
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	lock.Unlock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%d", len(backend.starts))
	}
}

func TestRunSelfTestPreservesAdvancedKinds(t *testing.T) {
	backend := &manualSelfTestBackend{startLeavesStatus: true}
	service := newManualSelfTestService(backend)
	disk := diskServiceInterfaces.DiskInfo{Name: "ada0"}
	tests := []struct {
		input string
		kind  smart.SelfTestKind
	}{
		{input: "offline", kind: smart.SelfTestKindOffline},
		{input: "default", kind: smart.SelfTestKindDefault},
		{input: "short", kind: smart.SelfTestKindShort},
		{input: "long", kind: smart.SelfTestKindExtended},
		{input: "extended", kind: smart.SelfTestKindExtended},
		{input: "conveyance", kind: smart.SelfTestKindConveyance},
		{input: "short_captive", kind: smart.SelfTestKindShortCaptive},
		{input: "extended_captive", kind: smart.SelfTestKindExtendedCaptive},
		{input: "conveyance_captive", kind: smart.SelfTestKindConveyanceCaptive},
	}
	for _, test := range tests {
		if err := service.RunSelfTest(disk, test.input); err != nil {
			t.Fatalf("input=%s err=%v", test.input, err)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != len(tests) {
		t.Fatalf("starts=%v", backend.starts)
	}
	for i, test := range tests {
		if backend.starts[i] != test.kind {
			t.Fatalf("index=%d got=%s want=%s", i, backend.starts[i], test.kind)
		}
	}
	if err := service.RunSelfTest(disk, "selective"); !errors.Is(err, smart.ErrSelfTestConfigurationRequired) {
		t.Fatalf("err=%v", err)
	}
}

func TestStartSelfTestRejectsUnsafeKinds(t *testing.T) {
	backend := &manualSelfTestBackend{}
	service := newManualSelfTestService(backend)
	for _, testType := range []string{"", "long", "offline", "default", "selective", "short_captive", "extended_captive", "conveyance_captive", "selective_captive", "abort"} {
		if _, err := service.StartSelfTest("ada0", testType); !errors.Is(err, ErrSelfTestTypeNotAllowed) {
			t.Fatalf("testType=%q err=%v", testType, err)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.starts) != 0 {
		t.Fatalf("starts=%v", backend.starts)
	}
}

func TestGetDiskDevicesWithoutSMARTSkipsReader(t *testing.T) {
	var reads atomic.Int32
	service := &Service{
		smartFailCache: make(map[string]smartFailureCacheEntry),
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{{
				Name:        "ada0",
				Type:        "SSD",
				Description: "Test disk",
				MediaSize:   1024,
				Partitions: []diskServiceInterfaces.PartitionInfo{{
					Name: "ada0p1",
					Size: 512,
				}},
			}}, nil
		},
		diskGPTSource: func(string) bool { return true },
		smartDataSource: func(diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
			reads.Add(1)
			return diskServiceInterfaces.SmartData{HealthKnown: true, Passed: true}, nil, nil
		},
	}
	disks, err := service.GetDiskDevicesWithoutSMART(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 0 || len(disks) != 1 || disks[0].SmartData != nil || disks[0].SelfTestLog != nil {
		t.Fatalf("reads=%d disks=%+v", reads.Load(), disks)
	}
	if !disks[0].GPT || disks[0].WearOut != "Unknown" || len(disks[0].Partitions) != 1 {
		t.Fatalf("disk=%+v", disks[0])
	}
	if _, err := service.GetDiskDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatalf("reads=%d", reads.Load())
	}
}

func TestGetDiskDevicesForSMARTMonitorSkipsStandbyATA(t *testing.T) {
	var probes atomic.Int32
	var reads atomic.Int32
	mode := smart.ATAPowerModeStandby
	var probeErr error
	service := &Service{
		smartFailCache: make(map[string]smartFailureCacheEntry),
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{{
				Name:       "ada0",
				Type:       "HDD",
				MediaSize:  1024,
				Partitions: []diskServiceInterfaces.PartitionInfo{{Name: "ada0p1", Size: 512}},
			}}, nil
		},
		diskGPTSource: func(string) bool { return true },
		ataPowerModeSource: func(string) (smart.ATAPowerMode, error) {
			probes.Add(1)
			return mode, probeErr
		},
		scsiPowerModeSource: func(string) (smart.SCSIPowerMode, error) {
			return smart.SCSIPowerModeUnknown, smart.ErrUnsupportedFeature
		},
		smartDataSource: func(diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
			reads.Add(1)
			return diskServiceInterfaces.SmartData{Device: diskServiceInterfaces.DeviceInfo{Protocol: "ATA"}, HealthKnown: true, Passed: true}, nil, nil
		},
	}

	disks, err := service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || !disks[0].SmartReadPowerSkipped || disks[0].SmartData != nil || reads.Load() != 0 || probes.Load() != 1 {
		t.Fatalf("disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}

	mode = smart.ATAPowerModeActiveOrIdle
	disks, err = service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartReadPowerSkipped || disks[0].SmartData == nil || reads.Load() != 1 || probes.Load() != 2 {
		t.Fatalf("disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}

	mode = smart.ATAPowerModeStandby
	disks, err = service.GetDiskDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartReadPowerSkipped || disks[0].SmartData == nil || reads.Load() != 2 || probes.Load() != 2 {
		t.Fatalf("disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}

	probeErr = smart.ErrUnsupportedFeature
	disks, err = service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartReadPowerSkipped || disks[0].SmartData == nil || reads.Load() != 3 || probes.Load() != 3 {
		t.Fatalf("disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}

	probeErr = smart.ErrControllerTimeout
	disks, err = service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartReadPowerSkipped || disks[0].SmartData != nil || reads.Load() != 3 || probes.Load() != 4 {
		t.Fatalf("disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}

	probeErr = nil
	disks, err = service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartData != nil || reads.Load() != 3 || probes.Load() != 4 {
		t.Fatalf("cached disks=%+v reads=%d probes=%d", disks, reads.Load(), probes.Load())
	}
}

func TestGetDiskDevicesForSMARTMonitorSkipsStandbySCSI(t *testing.T) {
	var ataProbes atomic.Int32
	var scsiProbes atomic.Int32
	var reads atomic.Int32
	mode := smart.SCSIPowerModeStandby
	service := &Service{
		smartFailCache: make(map[string]smartFailureCacheEntry),
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{{Name: "da0", Type: "HDD", MediaSize: 1024}}, nil
		},
		ataPowerModeSource: func(string) (smart.ATAPowerMode, error) {
			ataProbes.Add(1)
			return smart.ATAPowerModeUnknown, smart.ErrUnsupportedFeature
		},
		scsiPowerModeSource: func(string) (smart.SCSIPowerMode, error) {
			scsiProbes.Add(1)
			return mode, nil
		},
		smartDataSource: func(diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
			reads.Add(1)
			return diskServiceInterfaces.SmartData{Device: diskServiceInterfaces.DeviceInfo{Protocol: "SCSI"}, HealthKnown: true, Passed: true}, nil, nil
		},
	}

	disks, err := service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || !disks[0].SmartReadPowerSkipped || disks[0].SmartData != nil || ataProbes.Load() != 1 || scsiProbes.Load() != 1 || reads.Load() != 0 {
		t.Fatalf("disks=%+v ata=%d scsi=%d reads=%d", disks, ataProbes.Load(), scsiProbes.Load(), reads.Load())
	}

	mode = smart.SCSIPowerModeActive
	disks, err = service.GetDiskDevicesForSMARTMonitor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].SmartReadPowerSkipped || disks[0].SmartData == nil || ataProbes.Load() != 2 || scsiProbes.Load() != 2 || reads.Load() != 1 {
		t.Fatalf("disks=%+v ata=%d scsi=%d reads=%d", disks, ataProbes.Load(), scsiProbes.Load(), reads.Load())
	}
}

func TestSmartFailureCacheTracksDiskIdentityAndInventory(t *testing.T) {
	service := &Service{smartFailCache: make(map[string]smartFailureCacheEntry)}
	now := time.Now().UTC()
	original := diskServiceInterfaces.DiskInfo{Name: "ada0", Serial: "first", LunID: "lun", Description: "disk", MediaSize: 1024}
	replacement := original
	replacement.Serial = "second"
	service.recordSmartFailure(original, now)
	if !service.smartReadSuppressed(original, now.Add(time.Minute)) {
		t.Fatal("original_failure_was_not_cached")
	}
	if service.smartReadSuppressed(replacement, now.Add(time.Minute)) {
		t.Fatal("replacement_inherited_failure_cache")
	}
	service.recordSmartFailure(original, now)
	service.recordSmartFailure(diskServiceInterfaces.DiskInfo{Name: "ada1", Serial: "other"}, now)
	service.pruneSmartFailureCache([]diskServiceInterfaces.DiskInfo{replacement})
	service.smartFailMu.Lock()
	defer service.smartFailMu.Unlock()
	if len(service.smartFailCache) != 0 {
		t.Fatalf("cache=%v", service.smartFailCache)
	}
}
