// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	notificationService "github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type retryingDiskSmartEmitter struct {
	calls int
	err   error
}

func (e *retryingDiskSmartEmitter) Emit(context.Context, notifier.EventInput) (notifier.EmitResult, error) {
	e.calls++
	return notifier.EmitResult{}, e.err
}

// --- test helpers ---

func makeTestSystemService(t *testing.T, diskService diskServiceInterfaces.DiskServiceInterface) (*Service, *notificationService.Service) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},
		&models.DiskSmartSelfTestSchedule{},
	)

	notifSvc := notificationService.NewService(db)
	notifier.SetEmitter(notifSvc)
	notifSvc.SetDiskService(diskService)

	svc := &Service{
		DB:          db,
		DiskService: diskService,
	}

	return svc, notifSvc
}

func makeATA(temp int, passed bool, powerOnHours int, attrs ...diskServiceInterfaces.ATASmartAttribute) diskServiceInterfaces.SmartData {
	return diskServiceInterfaces.SmartData{
		Device:          diskServiceInterfaces.DeviceInfo{Protocol: "ATA"},
		Passed:          passed,
		HealthKnown:     true,
		ChecksumValid:   true,
		PowerOnHours:    powerOnHours,
		Temperature:     temp,
		PowerCycleCount: 0,
		Attributes:      attrs,
	}
}

func makeSCSI(temp int, passed bool, attrs ...diskServiceInterfaces.ATASmartAttribute) diskServiceInterfaces.SmartData {
	return diskServiceInterfaces.SmartData{
		Device:          diskServiceInterfaces.DeviceInfo{Protocol: "SCSI"},
		Passed:          passed,
		HealthKnown:     true,
		PowerOnHours:    0,
		Temperature:     temp,
		PowerCycleCount: 0,
		Attributes:      attrs,
	}
}

func makeNVMe(pctUsed int, criticalWarning string) diskServiceInterfaces.SMARTNvme {
	return diskServiceInterfaces.SMARTNvme{
		Device:                  diskServiceInterfaces.DeviceInfo{Protocol: "NVMe"},
		Passed:                  true,
		HealthKnown:             true,
		Temperature:             40,
		PercentageUsed:          pctUsed,
		CriticalWarning:         criticalWarning,
		AvailableSpare:          100,
		AvailableSpareThreshold: 10,
	}
}

func attr(id int, rawValue int64) diskServiceInterfaces.ATASmartAttribute {
	return diskServiceInterfaces.ATASmartAttribute{
		ID:       id,
		RawValue: rawValue,
	}
}

func attrPage(page, id int, rawValue int64, name string) diskServiceInterfaces.ATASmartAttribute {
	return diskServiceInterfaces.ATASmartAttribute{
		Page:     page,
		ID:       id,
		RawValue: rawValue,
		Name:     name,
	}
}

// --- getTemperature tests ---

func TestGetTemperatureATA(t *testing.T) {
	svc := &Service{}
	ata := makeATA(50, true, 100)
	if got := svc.getTemperature(ata); got != 50 {
		t.Fatalf("expected 50, got %d", got)
	}
}

func TestDiskSmartTargetKeyUsesOnlyStableIdentity(t *testing.T) {
	stable := diskServiceInterfaces.Disk{UUID: "D782E080-43C1-5ABC-9DEF-123456789ABC", IdentityStable: true, Device: "ada0"}
	if got := diskSmartTargetKey(stable); got != "d782e080-43c1-5abc-9def-123456789abc" {
		t.Fatalf("target=%q", got)
	}
	stable.Device = "ada9"
	if got := diskSmartTargetKey(stable); got != "d782e080-43c1-5abc-9def-123456789abc" {
		t.Fatalf("target=%q", got)
	}
	unstable := diskServiceInterfaces.Disk{UUID: stable.UUID, IdentityStable: false, Device: "da0"}
	if got := diskSmartTargetKey(unstable); got != "da0" {
		t.Fatalf("target=%q", got)
	}
}

type monitorAwareDiskService struct {
	diskServiceInterfaces.DiskServiceInterface
	regularCalls int
	monitorCalls int
}

func (m *monitorAwareDiskService) GetDiskDevices(context.Context) ([]diskServiceInterfaces.Disk, error) {
	m.regularCalls++
	return nil, errors.New("regular source used")
}

func (m *monitorAwareDiskService) GetDiskDevicesForSMARTMonitor(context.Context) ([]diskServiceInterfaces.Disk, error) {
	m.monitorCalls++
	return []diskServiceInterfaces.Disk{{Device: "ada0", SmartReadPowerSkipped: true}}, nil
}

func TestDiskSmartMonitorUsesPowerAwareSource(t *testing.T) {
	source := &monitorAwareDiskService{}
	svc := &Service{DiskService: source}
	disks, err := svc.diskSmartMonitorDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || !disks[0].SmartReadPowerSkipped || source.monitorCalls != 1 || source.regularCalls != 0 {
		t.Fatalf("disks=%+v monitor=%d regular=%d", disks, source.monitorCalls, source.regularCalls)
	}
}

func TestDiskSmartMonitorPowerSkipPreservesAvailabilityState(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{unavailableCount: 2}
	stateByDevice := map[string]*diskSmartState{"ada0": state}
	var mu sync.Mutex

	svc.processDiskSmartSample(context.Background(), &mu, stateByDevice, diskServiceInterfaces.Disk{
		Device: "ada0", Type: "HDD", SmartReadPowerSkipped: true,
	}, false)
	if state.unavailableCount != 2 || state.unavailableAlerted {
		t.Fatalf("state=%+v", state)
	}

	svc.processDiskSmartSample(context.Background(), &mu, stateByDevice, diskServiceInterfaces.Disk{
		Device: "ada0", Type: "HDD", SmartData: makeATA(40, true, 10),
	}, false)
	if state.unavailableCount != 0 || state.unavailableAlerted {
		t.Fatalf("state=%+v", state)
	}
	var count int64
	if err := notifSvc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("notifications=%d", count)
	}
}

func TestGetTemperatureSCSIFromPage0x0D(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(0, true,
		attrPage(0x0D, 0, 46, "Temperature"),
	)
	if got := svc.getTemperature(scsi); got != 46 {
		t.Fatalf("expected temperature 46 from SCSI page 0x0D, got %d", got)
	}
}

func TestGetTemperatureSCSIMissingPage0x0D(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(0, true,
		attrPage(0x02, 5, 89832143138920, "Write Total bytes processed"),
	)
	if got := svc.getTemperature(scsi); got != 0 {
		t.Fatalf("expected 0 for SCSI without temperature page, got %d", got)
	}
}

func TestGetTemperatureNVMe(t *testing.T) {
	svc := &Service{}
	nvme := makeNVMe(50, "0x00")
	if got := svc.getTemperature(nvme); got != 40 {
		t.Fatalf("expected 40, got %d", got)
	}
}

func TestGetTemperatureNil(t *testing.T) {
	svc := &Service{}
	if got := svc.getTemperature(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
}

func TestGetTemperatureSCSIFallbackToConvenienceField(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(42, true)
	if got := svc.getTemperature(scsi); got != 42 {
		t.Fatalf("expected 42 from convenience field, got %d", got)
	}
}

// --- getReallocatedSectors tests ---

func TestGetReallocatedSectorsATA(t *testing.T) {
	svc := &Service{}
	ata := makeATA(0, true, 0,
		diskServiceInterfaces.ATASmartAttribute{ID: 5, RawValue: 74},
		diskServiceInterfaces.ATASmartAttribute{ID: 197, RawValue: 3},
		diskServiceInterfaces.ATASmartAttribute{ID: 198, RawValue: 1},
	)
	realloc, pending, uncorrect := svc.getReallocatedSectors(ata)
	if realloc != 74 {
		t.Fatalf("expected reallocated=74, got %d", realloc)
	}
	if pending != 3 {
		t.Fatalf("expected pending=3, got %d", pending)
	}
	if uncorrect != 1 {
		t.Fatalf("expected uncorrectable=1, got %d", uncorrect)
	}
}

func TestGetReallocatedSectorsSCSIReturnsUncorrectedErrors(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(0, true,
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 5, RawValue: 89832143138920, Name: "Write Total bytes processed"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 6, RawValue: 2, Name: "Write Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x03, ID: 6, RawValue: 3, Name: "Read Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x05, ID: 6, RawValue: 4, Name: "Verify Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x06, ID: 6, RawValue: 5, Name: "Non-medium Total uncorrected errors"},
	)
	realloc, pending, uncorrect := svc.getReallocatedSectors(scsi)
	if realloc != 0 || pending != 0 || uncorrect != 14 {
		t.Fatalf("expected (0,0,14) for SCSI, got (%d,%d,%d)", realloc, pending, uncorrect)
	}
}

func TestGetReallocatedSectorsNVMe(t *testing.T) {
	svc := &Service{}
	nvme := makeNVMe(50, "0x00")
	realloc, pending, uncorrect := svc.getReallocatedSectors(nvme)
	if realloc != 0 || pending != 0 || uncorrect != 0 {
		t.Fatalf("expected (0,0,0) for NVMe, got (%d,%d,%d)", realloc, pending, uncorrect)
	}
}

func TestGetReallocatedSectorsNil(t *testing.T) {
	svc := &Service{}
	realloc, pending, uncorrect := svc.getReallocatedSectors(nil)
	if realloc != 0 || pending != 0 || uncorrect != 0 {
		t.Fatalf("expected (0,0,0) for nil, got (%d,%d,%d)", realloc, pending, uncorrect)
	}
}

func TestGetReallocatedSectorsATAOnlyReallocated(t *testing.T) {
	svc := &Service{}
	ata := makeATA(0, true, 0,
		diskServiceInterfaces.ATASmartAttribute{ID: 5, RawValue: 10},
	)
	realloc, pending, uncorrect := svc.getReallocatedSectors(ata)
	if realloc != 10 {
		t.Fatalf("expected reallocated=10, got %d", realloc)
	}
	if pending != 0 {
		t.Fatalf("expected pending=0, got %d", pending)
	}
	if uncorrect != 0 {
		t.Fatalf("expected uncorrectable=0, got %d", uncorrect)
	}
}

// --- getSMARTPassed tests ---

func TestGetSMARTPassedATA(t *testing.T) {
	svc := &Service{}

	passed := makeATA(0, true, 0)
	if !svc.getSMARTPassed(passed) {
		t.Fatal("expected passed=true")
	}

	failed := makeATA(0, false, 0)
	if svc.getSMARTPassed(failed) {
		t.Fatal("expected passed=false")
	}
}

func TestGetSMARTPassedSCSI(t *testing.T) {
	svc := &Service{}

	passed := makeSCSI(0, true)
	if !svc.getSMARTPassed(passed) {
		t.Fatal("expected passed=true for SCSI")
	}

	failed := makeSCSI(0, false)
	if svc.getSMARTPassed(failed) {
		t.Fatal("expected passed=false for SCSI")
	}
}

func TestGetSMARTPassedNVMe(t *testing.T) {
	svc := &Service{}

	nvme := makeNVMe(50, "0x00")
	if !svc.getSMARTPassed(nvme) {
		t.Fatal("expected passed=true for NVMe")
	}
}

func TestGetSMARTPassedNil(t *testing.T) {
	svc := &Service{}
	if !svc.getSMARTPassed(nil) {
		t.Fatal("expected passed=true for nil (default safe)")
	}
}

func TestGetSMARTPassedUnknown(t *testing.T) {
	svc := &Service{}
	data := makeSCSI(0, false)
	data.HealthKnown = false
	if !svc.getSMARTPassed(data) {
		t.Fatal("unknown health reported as failed")
	}
}

func TestDiskSmartMonitorIgnoresUntrackedSelfTestResults(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(smartData any) (float64, error) { return 0, nil }}
	svc, notificationSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{
		Device:    "da0",
		SmartData: makeSCSI(0, true),
		SelfTestLog: &diskServiceInterfaces.DiskSelfTestLog{Entries: []diskServiceInterfaces.DiskSelfTestEntry{
			{Type: "short", Status: "completed", LifetimeHours: 100},
		}},
	}
	svc.evaluateSmartData(context.Background(), disk, state, true)
	disk.SelfTestLog.Entries = []diskServiceInterfaces.DiskSelfTestEntry{
		{Type: "extended", Status: "failed_first_segment", LifetimeHours: 200},
	}
	svc.evaluateSmartData(context.Background(), disk, state, false)
	var count int64
	if err := notificationSvc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("notifications=%d", count)
	}
}

// --- emit count helper for integration tests ---

type emitCounter struct {
	count atomic.Int32
}

func (c *emitCounter) Emit(ctx context.Context, input notifier.EventInput) (notifier.EmitResult, error) {
	c.count.Add(1)
	return notifier.EmitResult{}, nil
}

func (c *emitCounter) Count() int32 {
	return c.count.Load()
}

// --- evaluate wearout integration tests ---

type mockDiskServiceForWearout struct {
	wearoutFn func(smartData any) (float64, error)
}

func (m *mockDiskServiceForWearout) GetDiskDevices(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetSmartData(disk diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
	return nil, nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetWearOut(smartData any) (float64, error) {
	return m.wearoutFn(smartData)
}
func (m *mockDiskServiceForWearout) GetDiskSize(device string) (uint64, error) {
	return 0, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) DestroyPartitionTable(device string) error {
	return errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) IsDiskGPT(device string) bool { return false }
func (m *mockDiskServiceForWearout) RunSelfTest(disk diskServiceInterfaces.DiskInfo, testType string) error {
	return errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetNVMEErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskNVMEErrorLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetSCTStatus(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTStatus, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetSCTTempHistory(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTTempHistory, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) AbortSelfTest(disk diskServiceInterfaces.DiskInfo) error {
	return errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetExtendedSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetExtendedErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetLogDirectory(disk diskServiceInterfaces.DiskInfo) ([]uint8, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetDeviceStatistics(disk diskServiceInterfaces.DiskInfo) ([]diskServiceInterfaces.DiskAttribute, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) GetSelectiveSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) SetSCTFeatureControl(disk diskServiceInterfaces.DiskInfo, featureCode uint16, state uint16, persistent bool) error {
	return errors.New("not implemented")
}
func (m *mockDiskServiceForWearout) SetSCTErrorRecoveryControl(disk diskServiceInterfaces.DiskInfo, read bool, timeLimit uint16) error {
	return errors.New("not implemented")
}

func TestEvaluateWearoutSCSISilentlySkips(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, _ := makeTestSystemService(t, diskService)

	disk := diskServiceInterfaces.Disk{
		Device:    "da0",
		Type:      "SSD",
		SmartData: makeSCSI(0, true),
	}

	st := &diskSmartState{}
	svc.evaluateWearout(context.Background(), disk, st, false)

	if st.wearCriticalCount != 0 {
		t.Fatalf("expected wearCriticalCount=0, got %d", st.wearCriticalCount)
	}
	if st.wearWarningCount != 0 {
		t.Fatalf("expected wearWarningCount=0, got %d", st.wearWarningCount)
	}
}

// --- evaluate reallocated integration tests ---

func TestEvaluateReallocatedSCSINeverAlerts(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	scsi := makeSCSI(0, true,
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 5, RawValue: 89832143138920, Name: "Write Total bytes processed"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x03, ID: 5, RawValue: 76756194693360, Name: "Read Total bytes processed"},
	)

	disk := diskServiceInterfaces.Disk{
		Device:    "da3",
		Type:      "SSD",
		SmartData: scsi,
	}

	st := &diskSmartState{}

	// Two consecutive polls — should normally trigger, but SCSI guard prevents it
	svc.evaluateReallocated(context.Background(), disk, st, false)
	if st.reallocCount != 0 {
		t.Fatalf("expected reallocCount=0 for SCSI after first poll, got %d", st.reallocCount)
	}

	svc.evaluateReallocated(context.Background(), disk, st, false)
	if st.reallocCount != 0 {
		t.Fatalf("expected reallocCount=0 for SCSI after second poll, got %d", st.reallocCount)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no active notifications, got %d", active)
	}
}

func TestEvaluateReallocatedStableAttributesAlertAndRecover(t *testing.T) {
	tests := []struct {
		name          string
		id            int
		reallocated   string
		pending       string
		uncorrectable string
	}{
		{name: "reallocated", id: 5, reallocated: "4", pending: "0", uncorrectable: "0"},
		{name: "pending", id: 197, reallocated: "0", pending: "4", uncorrectable: "0"},
		{name: "uncorrectable", id: 198, reallocated: "0", pending: "0", uncorrectable: "4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diskService := &mockDiskServiceForWearout{
				wearoutFn: func(smartData any) (float64, error) {
					return 0, errors.New("unused")
				},
			}
			svc, notifSvc := makeTestSystemService(t, diskService)
			state := &diskSmartState{}
			disk := diskServiceInterfaces.Disk{
				Device:    "ada0",
				Type:      "HDD",
				SmartData: makeATA(30, true, 0, attr(test.id, 4)),
			}

			for range diskSmartConsecutiveTrigger {
				svc.evaluateReallocated(context.Background(), disk, state, false)
			}

			var notifications []models.Notification
			if err := notifSvc.DB.Order("id ASC").Find(&notifications).Error; err != nil {
				t.Fatal(err)
			}
			if len(notifications) != 1 || !state.sectorAlerted {
				t.Fatalf("notifications=%d state=%+v", len(notifications), state)
			}
			if notifications[0].Metadata["condition"] != "sector_issues" ||
				notifications[0].Metadata["reallocated"] != test.reallocated ||
				notifications[0].Metadata["pending"] != test.pending ||
				notifications[0].Metadata["uncorrectable"] != test.uncorrectable {
				t.Fatalf("notification=%+v", notifications[0])
			}

			disk.SmartData = makeATA(30, true, 0, attr(test.id, 0))
			for range diskSmartConsecutiveClear {
				svc.evaluateReallocated(context.Background(), disk, state, false)
			}
			if err := notifSvc.DB.Order("id ASC").Find(&notifications).Error; err != nil {
				t.Fatal(err)
			}
			if len(notifications) != 1 || state.sectorAlerted {
				t.Fatalf("notifications=%d state=%+v", len(notifications), state)
			}
			if notifications[0].Metadata["condition"] != "sector_issues_cleared" || notifications[0].OccurrenceCount != 2 {
				t.Fatalf("notification=%+v", notifications[0])
			}
		})
	}
}

// --- evaluate temperature integration tests ---

func TestEvaluateTemperatureATAAlertsAfterConsecutiveBadReadings(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ata := makeATA(60, true, 0) // 60C > warning threshold (55C)
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ata,
	}

	st := &diskSmartState{}

	// First reading: count=1, no alert yet
	svc.evaluateTemperature(context.Background(), disk, st, false)
	if st.tempWarningCount != 1 {
		t.Fatalf("expected tempWarningCount=1, got %d", st.tempWarningCount)
	}

	// Second reading: count=2, alert fires
	svc.evaluateTemperature(context.Background(), disk, st, false)
	if st.tempWarningCount != 2 {
		t.Fatalf("expected tempWarningCount=2, got %d", st.tempWarningCount)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected 1 active notification, got %d", active)
	}
	var notification models.Notification
	if err := notifSvc.DB.Where("kind = ?", notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0")).First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if notification.Metadata["condition"] != "temperature_warning" || notification.Fingerprint != "ada0|temperature" {
		t.Fatalf("unstable_temperature_condition: %+v", notification)
	}
}

func TestEvaluateTemperatureSCSIAlertsFromPage0x0D(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	scsi := makeSCSI(0, true,
		attrPage(0x0D, 0, 60, "Temperature"),
	)
	disk := diskServiceInterfaces.Disk{
		Device:    "da0",
		Type:      "SSD",
		SmartData: scsi,
	}

	st := &diskSmartState{}

	svc.evaluateTemperature(context.Background(), disk, st, false)
	svc.evaluateTemperature(context.Background(), disk, st, false)

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected 1 active notification for SCSI temperature alert, got %d", active)
	}
}

func TestEvaluateTemperatureNoAlertWhenNormal(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ata := makeATA(30, true, 0) // 30C < warning threshold (55C)
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ata,
	}

	st := &diskSmartState{}

	for i := 0; i < 10; i++ {
		svc.evaluateTemperature(context.Background(), disk, st, false)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no notifications for normal temperature, got %d", active)
	}
}

func TestEvaluateTemperatureHonorsFractionalThreshold(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, errors.New("unused") }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	rule := models.NotificationKindRule{
		Kind:      notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0"),
		UIEnabled: true,
		Config:    `{"warningCelsius":55.5,"criticalCelsius":65.5}`,
	}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "ada0", Type: "SSD", SmartData: makeATA(55, true, 0)}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateTemperature(context.Background(), disk, state, false)
	}
	if count, err := notifSvc.CountActive(context.Background()); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	disk.SmartData = makeATA(56, true, 0)
	for range diskSmartConsecutiveTrigger {
		svc.evaluateTemperature(context.Background(), disk, state, false)
	}
	if count, err := notifSvc.CountActive(context.Background()); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestEvaluateTemperatureRecoveryReplacesAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "ada0", Type: "SSD", SmartData: makeATA(60, true, 0)}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateTemperature(context.Background(), disk, state, false)
	}
	disk.SmartData = makeATA(30, true, 0)
	for range diskSmartConsecutiveClear {
		svc.evaluateTemperature(context.Background(), disk, state, false)
	}
	var notifications []models.Notification
	if err := notifSvc.DB.Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Metadata["condition"] != "temperature_normal" || notifications[0].OccurrenceCount != 2 || state.temperatureAlert != "" {
		t.Fatalf("notifications=%+v state=%+v", notifications, state)
	}
}

func TestEvaluateWearoutRecoveryReplacesAlert(t *testing.T) {
	wearout := 85.0
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return wearout, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "ada0", Type: "SSD", SmartData: makeATA(30, true, 0)}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateWearout(context.Background(), disk, state, false)
	}
	wearout = 10
	for range diskSmartConsecutiveClear {
		svc.evaluateWearout(context.Background(), disk, state, false)
	}
	var notifications []models.Notification
	if err := notifSvc.DB.Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Metadata["condition"] != "wearout_normal" || notifications[0].OccurrenceCount != 2 || state.wearoutAlert != "" {
		t.Fatalf("notifications=%+v state=%+v", notifications, state)
	}
}

func TestEvaluateNvmeRecoveryReplacesAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "nda0", Type: "NVMe"}
	warning := makeNVMe(10, "0x01")
	for range diskSmartConsecutiveTrigger {
		svc.evaluateNvme(context.Background(), disk, &warning, state, false)
	}
	normal := makeNVMe(10, "0x00")
	for range diskSmartConsecutiveClear {
		svc.evaluateNvme(context.Background(), disk, &normal, state, false)
	}
	var notifications []models.Notification
	if err := notifSvc.DB.Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Metadata["condition"] != "nvme_recovered" || notifications[0].OccurrenceCount != 2 || state.nvmeAlerted {
		t.Fatalf("notifications=%+v state=%+v", notifications, state)
	}
}

func TestEvaluateNvmePreservesExactMediaErrorCounter(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "nda0", Type: "NVMe"}
	warning := makeNVMe(10, "0x00")
	warning.MediaErrorsExact = "340282366920938463463374607431768211455"
	for range diskSmartConsecutiveTrigger {
		svc.evaluateNvme(context.Background(), disk, &warning, state, false)
	}
	var notification models.Notification
	if err := notifSvc.DB.First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if notification.Metadata["media_errors"] != warning.MediaErrorsExact || state.nvmeMediaErrors != warning.MediaErrorsExact {
		t.Fatalf("notification=%+v state=%+v", notification, state)
	}
}

func TestEvaluateSmartAvailabilityRecoveryReplacesAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, nil }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	stateByDevice := map[string]*diskSmartState{"ada0": {}}
	var mu sync.Mutex
	for range diskSmartConsecutiveUnavailable {
		svc.handleMissingSmart(context.Background(), &mu, stateByDevice, "ada0", "ada0")
	}
	state := stateByDevice["ada0"]
	if !state.unavailableAlerted {
		t.Fatal("availability_alert_not_latched")
	}

	disk := diskServiceInterfaces.Disk{Device: "ada0", Type: "HDD", SmartData: makeATA(30, true, 0)}
	svc.evaluateSmartData(context.Background(), disk, state, false)

	var notifications []models.Notification
	if err := notifSvc.DB.Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Metadata["condition"] != "smart_available" || notifications[0].Fingerprint != "ada0|availability" || notifications[0].OccurrenceCount != 2 || state.unavailableAlerted || state.unavailableCount != 0 {
		t.Fatalf("notifications=%+v state=%+v", notifications, state)
	}
}

// --- evaluate health integration tests ---

func TestEvaluateHealthATAAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ata := makeATA(30, false, 0) // health FAILED
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ata,
	}

	st := &diskSmartState{}

	svc.evaluateHealth(context.Background(), disk, st, false)
	svc.evaluateHealth(context.Background(), disk, st, false)

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected 1 active notification for health fail, got %d", active)
	}
	var notification models.Notification
	if err := notifSvc.DB.Where("kind = ?", notifier.KindForDiskSmart(notifier.DiskSmartHealthKindPrefix, "ada0")).First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if notification.Metadata["condition"] != "health_failed" || notification.Fingerprint != "ada0|health" {
		t.Fatalf("unstable_health_condition: %+v", notification)
	}
}

func TestEvaluateHealthSCSIAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	scsi := makeSCSI(0, false)
	disk := diskServiceInterfaces.Disk{
		Device:    "da3",
		Type:      "SSD",
		SmartData: scsi,
	}

	st := &diskSmartState{}

	svc.evaluateHealth(context.Background(), disk, st, false)
	svc.evaluateHealth(context.Background(), disk, st, false)

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected 1 active notification for SCSI health fail, got %d", active)
	}
}

func TestEvaluateHealthNormalNoAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ata := makeATA(30, true, 0)
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ata,
	}

	st := &diskSmartState{}

	for i := 0; i < 10; i++ {
		svc.evaluateHealth(context.Background(), disk, st, false)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no notifications for healthy disk, got %d", active)
	}
}

func TestEvaluateHealthRetriesFailedNotificationDelivery(t *testing.T) {
	diskService := &mockDiskServiceForWearout{wearoutFn: func(any) (float64, error) { return 0, errors.New("unused") }}
	svc, notifSvc := makeTestSystemService(t, diskService)
	emitter := &retryingDiskSmartEmitter{err: errors.New("delivery failed")}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(notifSvc)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "ada0", Type: "SSD", SmartData: makeATA(30, false, 0)}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateHealth(context.Background(), disk, state, false)
	}
	if state.healthAlerted || emitter.calls != 1 {
		t.Fatalf("state=%+v calls=%d", state, emitter.calls)
	}
	emitter.err = nil
	svc.evaluateHealth(context.Background(), disk, state, false)
	if !state.healthAlerted || emitter.calls != 2 {
		t.Fatalf("state=%+v calls=%d", state, emitter.calls)
	}
}

func TestEvaluateHealthUnknownDoesNotAdvanceOrRecover(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: makeATA(30, false, 0),
	}

	for range diskSmartConsecutiveTrigger {
		svc.evaluateHealth(context.Background(), disk, state, false)
	}
	if !state.healthAlerted {
		t.Fatalf("state=%+v", state)
	}

	unknown := makeATA(30, false, 0)
	unknown.HealthKnown = false
	disk.SmartData = unknown
	for range diskSmartConsecutiveClear + 2 {
		svc.evaluateHealth(context.Background(), disk, state, false)
	}
	if !state.healthAlerted || state.healthFailCount != 0 || state.healthNormalCount != 0 {
		t.Fatalf("state=%+v", state)
	}

	var notifications []models.Notification
	if err := notifSvc.DB.Order("id ASC").Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Metadata["condition"] != "health_failed" {
		t.Fatalf("notifications=%+v", notifications)
	}

	disk.SmartData = makeATA(30, true, 0)
	for range diskSmartConsecutiveClear {
		svc.evaluateHealth(context.Background(), disk, state, false)
	}
	if err := notifSvc.DB.Order("id ASC").Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || state.healthAlerted {
		t.Fatalf("notifications=%+v state=%+v", notifications, state)
	}
	if notifications[0].Metadata["condition"] != "health_recovered" || notifications[0].OccurrenceCount != 2 {
		t.Fatalf("notification=%+v", notifications[0])
	}
}

func TestEvaluateHealthUnknownBreaksFailureStreak(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: makeATA(30, false, 0),
	}

	svc.evaluateHealth(context.Background(), disk, state, false)
	unknown := makeATA(30, false, 0)
	unknown.HealthKnown = false
	disk.SmartData = unknown
	svc.evaluateHealth(context.Background(), disk, state, false)
	disk.SmartData = makeATA(30, false, 0)
	svc.evaluateHealth(context.Background(), disk, state, false)

	var count int64
	if err := notifSvc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || state.healthFailCount != 1 {
		t.Fatalf("count=%d state=%+v", count, state)
	}

	svc.evaluateHealth(context.Background(), disk, state, false)
	if err := notifSvc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || !state.healthAlerted {
		t.Fatalf("count=%d state=%+v", count, state)
	}
}

// --- warmup tests ---

func TestWarmupSuppressesAllNotifications(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	// Disk that would trigger on every check
	ata := makeATA(70, false, 0) // high temp + health failed
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ata,
	}

	st := &diskSmartState{}

	// Run multiple polls in warmup mode
	for i := 0; i < 5; i++ {
		svc.evaluateSmartData(context.Background(), disk, st, true)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no notifications during warmup, got %d", active)
	}
}

// --- consecutive trigger / clear mechanics ---

func TestConsecutiveTriggerRequiresTwoBadReadings(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ataHealthy := makeATA(30, true, 0)
	ataFailing := makeATA(30, false, 0)
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ataHealthy,
	}

	st := &diskSmartState{}

	// Start with healthy readings
	for i := 0; i < 3; i++ {
		disk.SmartData = ataHealthy
		svc.evaluateSmartData(context.Background(), disk, st, false)
	}

	// First bad reading
	disk.SmartData = ataFailing
	svc.evaluateSmartData(context.Background(), disk, st, false)

	active, _ := notifSvc.CountActive(context.Background())
	if active != 0 {
		t.Fatalf("expected no alert after 1 bad reading, got %d", active)
	}

	// Second consecutive bad reading — alert fires
	svc.evaluateSmartData(context.Background(), disk, st, false)
	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected 1 alert after 2 consecutive bad readings, got %d", active)
	}

	// Interruption: good reading resets counter
	disk.SmartData = ataHealthy
	svc.evaluateSmartData(context.Background(), disk, st, false)

	// Another single bad reading should NOT re-alert
	disk.SmartData = ataFailing
	svc.evaluateSmartData(context.Background(), disk, st, false)

	active, _ = notifSvc.CountActive(context.Background())
	if active != 1 {
		t.Fatalf("expected still 1 active after interrupted bad streak, got %d", active)
	}
}

func TestConsecutiveClearRequiresThreeGoodReadings(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("unused")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	ataHealthy := makeATA(30, true, 0)
	ataTempHigh := makeATA(60, true, 0) // above warning threshold
	disk := diskServiceInterfaces.Disk{
		Device:    "ada0",
		Type:      "SSD",
		SmartData: ataTempHigh,
	}

	st := &diskSmartState{}

	// Trigger a warning by driving temp high twice
	svc.evaluateSmartData(context.Background(), disk, st, false)
	svc.evaluateSmartData(context.Background(), disk, st, false)

	active, _ := notifSvc.CountActive(context.Background())
	if active != 1 {
		t.Fatalf("expected 1 active high-temp notification, got %d", active)
	}

	// Now return to normal — 1 good reading should NOT clear (clear=3)
	disk.SmartData = ataHealthy
	svc.evaluateSmartData(context.Background(), disk, st, false)
	if st.tempNormalCount != 1 {
		t.Fatalf("expected tempNormalCount=1, got %d", st.tempNormalCount)
	}

	// After diskSmartConsecutiveClear (3) good readings, clear notification fires
	svc.evaluateSmartData(context.Background(), disk, st, false)
	svc.evaluateSmartData(context.Background(), disk, st, false)

	// The original warning notification should have been dismissed/replaced
	// by the recovery notification; or at minimum normalization counter progressed
	if st.tempNormalCount != 3 {
		t.Fatalf("expected tempNormalCount=3, got %d", st.tempNormalCount)
	}
}

// --- SCSI wearout does not fire with mock that returns error ---

func TestSCSIDiskWearoutEndToEndNoAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	scsi := makeSCSI(40, true)
	disk := diskServiceInterfaces.Disk{
		Device:    "da3",
		Type:      "SSD",
		SmartData: scsi,
	}

	st := &diskSmartState{}

	for i := 0; i < 5; i++ {
		svc.evaluateSmartData(context.Background(), disk, st, false)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no wearout notifications for SCSI disk, got %d", active)
	}
	if st.wearCriticalCount != 0 || st.wearWarningCount != 0 {
		t.Fatalf("expected all wearout counters at 0, got crit=%d warn=%d", st.wearCriticalCount, st.wearWarningCount)
	}
}
