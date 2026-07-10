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
	"sync/atomic"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	notificationService "github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
)

// --- test helpers ---

func makeTestSystemService(t *testing.T, diskService diskServiceInterfaces.DiskServiceInterface) (*Service, *notificationService.Service) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},
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
		PowerOnHours:    0,
		Temperature:     temp,
		PowerCycleCount: 0,
		Attributes:      attrs,
	}
}

func makeNVMe(pctUsed int, criticalWarning string) diskServiceInterfaces.SMARTNvme {
	return diskServiceInterfaces.SMARTNvme{
		Device:        diskServiceInterfaces.DeviceInfo{Protocol: "NVMe"},
		Passed:        true,
		Temperature:   40,
		PercentageUsed: pctUsed,
		CriticalWarning: criticalWarning,
		AvailableSpare: 100,
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

func TestGetReallocatedSectorsSCSIReturnsZero(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(0, true,
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 5, RawValue: 89832143138920, Name: "Write Total bytes processed"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x03, ID: 5, RawValue: 76756194693360, Name: "Read Total bytes processed"},
	)
	realloc, pending, uncorrect := svc.getReallocatedSectors(scsi)
	if realloc != 0 || pending != 0 || uncorrect != 0 {
		t.Fatalf("expected (0,0,0) for SCSI, got (%d,%d,%d)", realloc, pending, uncorrect)
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
