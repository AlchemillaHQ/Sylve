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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	diskService "github.com/alchemillahq/sylve/internal/services/disk"
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
		&models.DiskSmartCounterAcknowledgement{},
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

var historicalCounterFixtures = []struct {
	key    string
	sample func(int64) any
}{
	{notifier.DiskSmartCounterReallocated, func(value int64) any { return makeATA(30, true, 0, attr(5, value)) }},
	{notifier.DiskSmartCounterNvmeMediaErrors, func(value int64) any {
		data := makeNVMe(30, "0x00")
		data.MediaErrors = int(value)
		return data
	}},
	{notifier.DiskSmartCounterScsiWriteErrors, func(value int64) any { return makeSCSI(30, true, attrPage(0x02, 6, value, "")) }},
	{notifier.DiskSmartCounterScsiReadErrors, func(value int64) any { return makeSCSI(30, true, attrPage(0x03, 6, value, "")) }},
	{notifier.DiskSmartCounterScsiVerifyErrors, func(value int64) any { return makeSCSI(30, true, attrPage(0x05, 6, value, "")) }},
	{notifier.DiskSmartCounterScsiNonMediumErrors, func(value int64) any { return makeSCSI(30, true, attrPage(0x06, 6, value, "")) }},
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

func TestGetTemperatureSCSIUsesCanonicalTemperature(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(46, true,
		attrPage(0x0D, 0, 46, "Temperature"),
	)
	if got := svc.getTemperature(scsi); got != 46 {
		t.Fatalf("expected canonical SCSI temperature 46, got %d", got)
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

func TestGetTemperatureSCSIUnavailableSentinel(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(0, true,
		attrPage(0x0D, 0, 0xff, "Temperature"),
	)
	if got := svc.getTemperature(scsi); got != 0 {
		t.Fatalf("expected unavailable SCSI temperature, got %d", got)
	}
}

func TestGetTemperatureSCSIUnavailableSentinelUsesFallback(t *testing.T) {
	svc := &Service{}
	scsi := makeSCSI(42, true,
		attrPage(0x0D, 0, 0xff, "Temperature"),
	)
	if got := svc.getTemperature(scsi); got != 42 {
		t.Fatalf("expected canonical fallback temperature 42, got %d", got)
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

func TestDiskSmartSectorSamplesATA(t *testing.T) {
	ata := makeATA(0, true, 0,
		diskServiceInterfaces.ATASmartAttribute{ID: 5, RawValue: 74},
		diskServiceInterfaces.ATASmartAttribute{ID: 197, RawValue: 3},
		diskServiceInterfaces.ATASmartAttribute{ID: 198, RawValue: 1},
	)
	pending, uncorrect, pendingKnown, uncorrectableKnown := getActiveSectorIssues(ata)
	if value := diskSmartCounterSamples(ata)[notifier.DiskSmartCounterReallocated]; value != "74" {
		t.Fatalf("expected reallocated=74, got %s", value)
	}
	if !pendingKnown || !uncorrectableKnown {
		t.Fatal("expected known active sector counts")
	}
	if pending != 3 {
		t.Fatalf("expected pending=3, got %d", pending)
	}
	if uncorrect != 1 {
		t.Fatalf("expected uncorrectable=1, got %d", uncorrect)
	}
}

func TestDiskSmartCounterSamplesSCSI(t *testing.T) {
	scsi := makeSCSI(0, true,
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 5, RawValue: 89832143138920, Name: "Write Total bytes processed"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x02, ID: 6, RawValue: 2, Name: "Write Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x03, ID: 6, RawValue: 3, Name: "Read Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x05, ID: 6, RawValue: 4, Name: "Verify Total uncorrected errors"},
		diskServiceInterfaces.ATASmartAttribute{Page: 0x06, ID: 6, RawValue: 5, Name: "Non-medium Total uncorrected errors"},
	)
	samples := diskSmartCounterSamples(scsi)
	for key, expected := range map[string]string{
		notifier.DiskSmartCounterScsiWriteErrors:     "2",
		notifier.DiskSmartCounterScsiReadErrors:      "3",
		notifier.DiskSmartCounterScsiVerifyErrors:    "4",
		notifier.DiskSmartCounterScsiNonMediumErrors: "5",
	} {
		if samples[key] != expected {
			t.Fatalf("%s: expected %s, got %s", key, expected, samples[key])
		}
	}
	pending, uncorrect, pendingKnown, uncorrectableKnown := getActiveSectorIssues(scsi)
	if pending != 0 || uncorrect != 0 || pendingKnown || uncorrectableKnown {
		t.Fatal("SCSI historical errors were treated as active ATA faults")
	}
}

func TestActiveSectorIssuesNVMe(t *testing.T) {
	nvme := makeNVMe(50, "0x00")
	pending, uncorrect, pendingKnown, uncorrectableKnown := getActiveSectorIssues(nvme)
	if pending != 0 || uncorrect != 0 || pendingKnown || uncorrectableKnown {
		t.Fatal("expected no ATA faults for NVMe")
	}
}

func TestDiskSmartSectorSamplesNil(t *testing.T) {
	pending, uncorrect, pendingKnown, uncorrectableKnown := getActiveSectorIssues(nil)
	if pending != 0 || uncorrect != 0 || pendingKnown || uncorrectableKnown || len(diskSmartCounterSamples(nil)) != 0 {
		t.Fatal("missing SMART data was treated as known")
	}
}

func TestDiskSmartSectorSamplesOnlyReallocated(t *testing.T) {
	ata := makeATA(0, true, 0,
		diskServiceInterfaces.ATASmartAttribute{ID: 5, RawValue: 10},
	)
	pending, uncorrect, pendingKnown, uncorrectableKnown := getActiveSectorIssues(ata)
	if value := diskSmartCounterSamples(ata)[notifier.DiskSmartCounterReallocated]; value != "10" {
		t.Fatalf("expected reallocated=10, got %s", value)
	}
	if pendingKnown || uncorrectableKnown {
		t.Fatal("missing active counters were treated as known zeros")
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

func TestEvaluateWearoutSupportedWDDoesNotFalseAlert(t *testing.T) {
	svc, notifSvc := makeTestSystemService(t, &diskService.Service{})
	data := makeATA(32, true, 19613,
		diskServiceInterfaces.ATASmartAttribute{ID: 230, Name: "Media Wearout Indicator", Value: 3, RawValue: 0x030c0228030c},
	)
	data.Model = "WDC  WDS400T2B0A-00SM50"
	data.Firmware = "411030WD"
	disk := diskServiceInterfaces.Disk{Device: "da4", Type: "SSD", SmartData: data}
	st := &diskSmartState{}
	for i := 0; i < diskSmartConsecutiveTrigger+diskSmartConsecutiveClear; i++ {
		svc.evaluateWearout(context.Background(), disk, st, false)
	}
	if st.wearoutPct != 3 || st.wearoutAlert != "" || st.wearCriticalCount != 0 || st.wearWarningCount != 0 {
		t.Fatalf("WD percentage used was misinterpreted: %+v", st)
	}
	_, total, err := notifSvc.List(context.Background(), notificationService.ListScopeAll, 100, 0)
	if err != nil || total != 0 {
		t.Fatalf("WD three percent wear produced notifications: total=%d err=%v", total, err)
	}
}

func TestEvaluateWearoutUnlistedWDDoesNotAlertOrRecover(t *testing.T) {
	svc, notifSvc := makeTestSystemService(t, &diskService.Service{})
	data := makeATA(32, true, 19613,
		diskServiceInterfaces.ATASmartAttribute{ID: 230, Name: "Media Wearout Indicator", Value: 1, RawValue: 0x0160011e0160},
	)
	data.Model = "WDC  WDS400T2B0A-00SM50"
	data.Firmware = "OTHER"
	disk := diskServiceInterfaces.Disk{Device: "da4", Type: "SSD", SmartData: data}
	for _, alert := range []string{"", "critical"} {
		st := &diskSmartState{wearoutAlert: alert}
		for i := 0; i < diskSmartConsecutiveTrigger+diskSmartConsecutiveClear; i++ {
			svc.evaluateWearout(context.Background(), disk, st, false)
		}
		if st.wearoutAlert != alert || st.wearCriticalCount != 0 || st.wearWarningCount != 0 || st.wearNormalCount != 0 {
			t.Fatalf("unknown wearout changed alert state: %+v", st)
		}
	}
	_, total, err := notifSvc.List(context.Background(), notificationService.ListScopeAll, 100, 0)
	if err != nil || total != 0 {
		t.Fatalf("unknown wearout produced notifications: total=%d err=%v", total, err)
	}
}

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
func (m *mockDiskServiceForWearout) IsDiskGPT(device string, sectorSize int) bool { return false }
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

func TestSCSIBytesProcessedDoNotTriggerSectorWarnings(t *testing.T) {
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

	svc.evaluateSmartData(context.Background(), disk, st, false)
	if st.sectorCount != 0 {
		t.Fatalf("expected sectorCount=0 for SCSI after first poll, got %d", st.sectorCount)
	}

	svc.evaluateSmartData(context.Background(), disk, st, false)
	if st.sectorCount != 0 {
		t.Fatalf("expected sectorCount=0 for SCSI after second poll, got %d", st.sectorCount)
	}

	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected no active notifications, got %d", active)
	}
}

func TestHistoricalCounterMonitorRestartAndConfirmedGrowth(t *testing.T) {
	for _, fixture := range historicalCounterFixtures {
		t.Run(fixture.key, func(t *testing.T) {
			ctx := context.Background()
			svc, notifications := makeTestSystemService(t, &diskService.Service{})
			disk := diskServiceInterfaces.Disk{
				Device: "da5", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true,
				SmartData: fixture.sample(4),
			}
			state := &diskSmartState{}
			svc.evaluateSmartData(ctx, disk, state, true)
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			counter, _ := notifier.DiskSmartCounterByKey(fixture.key)
			if err != nil || total != 1 || items[0].Metadata["counter_key"] != fixture.key ||
				items[0].Title != "Disk da5 has "+strings.ToLower(counter.Label) || items[0].Body != "Current count: 4." {
				t.Fatalf("initial counter: items=%+v total=%d err=%v", items, total, err)
			}
			if err := notifications.Dismiss(ctx, items[0].ID); err != nil {
				t.Fatal(err)
			}
			notifications = notificationService.NewService(svc.DB)
			notifier.SetEmitter(notifications)
			state = &diskSmartState{}
			disk.Device = "da7"
			svc.evaluateSmartData(ctx, disk, state, true)
			for _, count := range []int64{4, 3, 0, 4, 5, 4, 5} {
				disk.SmartData = fixture.sample(count)
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			if active, err := notifications.CountActive(ctx); err != nil || active != 0 || state.counters[fixture.key].notifiedValue.String() != "4" {
				t.Fatalf("acknowledged or isolated growth reopened: active=%d state=%+v err=%v", active, state, err)
			}
			svc.evaluateSmartData(ctx, disk, state, false)
			items, total, err = notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 1 || items[0].Title != "Disk da7 "+strings.ToLower(counter.Label)+" increased" ||
				items[0].Body != "Increased from 4 to 5 (+1)." ||
				items[0].Metadata["previous_counter_value"] != "4" || items[0].Metadata["counter_value"] != "5" || items[0].Metadata["counter_delta"] != "1" {
				t.Fatalf("confirmed growth: items=%+v total=%d err=%v", items, total, err)
			}
			if err := notifications.Dismiss(ctx, items[0].ID); err != nil {
				t.Fatal(err)
			}
			state = &diskSmartState{}
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			if active, err := notifications.CountActive(ctx); err != nil || active != 0 {
				t.Fatalf("updated baseline did not survive restart: active=%d err=%v", active, err)
			}
		})
	}
}

func TestHistoricalCounterGrowthRequiresConsecutiveReadings(t *testing.T) {
	for _, fixture := range historicalCounterFixtures {
		t.Run(fixture.key, func(t *testing.T) {
			ctx := context.Background()
			svc, notifications := makeTestSystemService(t, &diskService.Service{})
			disk := diskServiceInterfaces.Disk{Device: "da5", SmartData: fixture.sample(4)}
			state := &diskSmartState{}
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			for _, count := range []int64{5, 4, 5} {
				disk.SmartData = fixture.sample(count)
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, _, err := notifications.List(ctx, notificationService.ListScopeAll, 100, 0)
			if err != nil || len(items) != 1 || items[0].OccurrenceCount != 1 {
				t.Fatalf("isolated growth alerted: items=%+v err=%v", items, err)
			}
			disk.SmartData = fixture.sample(6)
			svc.evaluateSmartData(ctx, disk, state, false)
			items, _, err = notifications.List(ctx, notificationService.ListScopeAll, 100, 0)
			if err != nil || len(items) != 1 || items[0].OccurrenceCount != 2 || items[0].Metadata["counter_value"] != "6" {
				t.Fatalf("sustained growth did not alert: items=%+v err=%v", items, err)
			}
		})
	}
}

func TestHistoricalCounterMissingAndInvalidSamplesBreakGrowthStreak(t *testing.T) {
	for _, fixture := range historicalCounterFixtures {
		t.Run(fixture.key, func(t *testing.T) {
			ctx := context.Background()
			svc, notifications := makeTestSystemService(t, &diskService.Service{})
			state := &diskSmartState{}
			disk := diskServiceInterfaces.Disk{Device: "da5", SmartData: fixture.sample(4)}
			svc.evaluateDiskSmartCounters(ctx, disk, state, false)
			for _, unknown := range []any{nil, fixture.sample(-1)} {
				disk.SmartData = unknown
				svc.evaluateDiskSmartCounters(ctx, disk, state, false)
				disk.SmartData = fixture.sample(4)
				svc.evaluateDiskSmartCounters(ctx, disk, state, false)
				if active, err := notifications.CountActive(ctx); err != nil || active != 0 {
					t.Fatalf("unknown sample advanced streak: active=%d err=%v", active, err)
				}
			}
			svc.evaluateDiskSmartCounters(ctx, disk, state, false)
			disk.SmartData = fixture.sample(0)
			for range diskSmartConsecutiveClear {
				svc.evaluateDiskSmartCounters(ctx, disk, state, false)
			}
			var stored models.Notification
			if err := notifications.DB.First(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.Metadata["condition"] != notifier.DiskSmartCounterCondition || stored.OccurrenceCount != 1 || stored.Severity != models.NotificationSeverityWarning {
				t.Fatalf("historical counter reported recovery after reset: %+v", stored)
			}
		})
	}
}

func TestHistoricalCounterMonitorRetriesFailedDelivery(t *testing.T) {
	ctx := context.Background()
	svc, notifications := makeTestSystemService(t, &diskService.Service{})
	emitter := &retryingDiskSmartEmitter{err: errors.New("delivery failed")}
	notifier.SetEmitter(emitter)
	defer notifier.SetEmitter(notifications)
	state := &diskSmartState{}
	disk := diskServiceInterfaces.Disk{Device: "da5", SmartData: makeATA(30, true, 0, attr(5, 4))}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateDiskSmartCounters(ctx, disk, state, false)
	}
	if emitter.calls != 1 || state.counters[notifier.DiskSmartCounterReallocated].notifiedValue != nil {
		t.Fatalf("failed emission advanced state: calls=%d state=%+v", emitter.calls, state)
	}
	emitter.err = nil
	svc.evaluateDiskSmartCounters(ctx, disk, state, false)
	if emitter.calls != 2 || state.counters[notifier.DiskSmartCounterReallocated].notifiedValue.String() != "4" {
		t.Fatalf("delivery was not retried: calls=%d state=%+v", emitter.calls, state)
	}
}

func TestPowerSkippedSMARTReadInterruptsHistoricalCounterStreak(t *testing.T) {
	ctx := context.Background()
	svc, notifications := makeTestSystemService(t, &diskService.Service{})
	disk := diskServiceInterfaces.Disk{Device: "da5", SmartData: makeATA(30, true, 0, attr(5, 4))}
	states := make(map[string]*diskSmartState)
	var mu sync.Mutex
	svc.processDiskSmartSample(ctx, &mu, states, disk, false)
	disk.SmartReadPowerSkipped = true
	svc.processDiskSmartSample(ctx, &mu, states, disk, false)
	disk.SmartReadPowerSkipped = false
	svc.processDiskSmartSample(ctx, &mu, states, disk, false)
	if active, err := notifications.CountActive(ctx); err != nil || active != 0 {
		t.Fatalf("skipped read advanced streak: active=%d err=%v", active, err)
	}
	svc.processDiskSmartSample(ctx, &mu, states, disk, false)
	if active, err := notifications.CountActive(ctx); err != nil || active != 1 {
		t.Fatalf("confirmed count did not alert: active=%d err=%v", active, err)
	}
}

func TestSCSICounterGrowthCannotBeMaskedByAnotherCounter(t *testing.T) {
	ctx := context.Background()
	svc, notifications := makeTestSystemService(t, &diskService.Service{})
	disk := diskServiceInterfaces.Disk{
		Device: "da5", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true,
		SmartData: makeSCSI(30, true, attrPage(0x03, 6, 4, ""), attrPage(0x02, 6, 7, "")),
	}
	state := &diskSmartState{}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(ctx, disk, state, false)
	}
	if dismissed, err := notifications.DismissAll(ctx); err != nil || dismissed != 2 {
		t.Fatalf("dismissed=%d err=%v", dismissed, err)
	}
	disk.SmartData = makeSCSI(30, true, attrPage(0x03, 6, 3, ""), attrPage(0x02, 6, 8, ""))
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(ctx, disk, state, false)
	}
	items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
	if err != nil || total != 1 || items[0].Metadata["counter_key"] != notifier.DiskSmartCounterScsiWriteErrors || items[0].Metadata["counter_delta"] != "1" {
		t.Fatalf("growth masked by unchanged sum: items=%+v total=%d err=%v", items, total, err)
	}
}

func TestNvmeHistoricalCounterAcknowledgementDoesNotSilenceActiveFaults(t *testing.T) {
	for _, fault := range []string{"critical warning", "low spare"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			svc, notifications := makeTestSystemService(t, &diskService.Service{})
			data := makeNVMe(30, "0x00")
			data.MediaErrors = 4
			if fault == "critical warning" {
				data.CriticalWarning = "0x04"
			} else {
				data.AvailableSpare = 5
				data.AvailableSpareThreshold = 10
			}
			disk := diskServiceInterfaces.Disk{
				Device: "nda0", Type: "NVMe", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true, SmartData: data,
			}
			state := &diskSmartState{}
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 2 {
				t.Fatalf("mixed sample did not split warnings: items=%+v total=%d err=%v", items, total, err)
			}
			for _, item := range items {
				if item.Metadata["condition"] == notifier.DiskSmartCounterCondition {
					if err := notifications.Dismiss(ctx, item.ID); err != nil {
						t.Fatal(err)
					}
				} else if item.Metadata["condition"] != "nvme_warning" || item.Metadata["acknowledgement"] != "" {
					t.Fatalf("active fault acknowledgeable: %+v", item)
				}
			}
			state = &diskSmartState{}
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err = notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 1 || items[0].Metadata["condition"] != "nvme_warning" {
				t.Fatalf("baseline silenced active fault: items=%+v total=%d err=%v", items, total, err)
			}
			data.CriticalWarning = "0x00"
			data.AvailableSpare = 100
			disk.SmartData = data
			for range diskSmartConsecutiveClear {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err = notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 1 || items[0].Metadata["condition"] != "nvme_recovered" {
				t.Fatalf("historical counter blocked active recovery: items=%+v total=%d err=%v", items, total, err)
			}
		})
	}
}

func TestSCSICounterMonitorPreservesUnsignedPrecision(t *testing.T) {
	ctx := context.Background()
	svc, notifications := makeTestSystemService(t, &diskService.Service{})
	const before = "9223372036854775808"
	const after = "9223372036854775809"
	attribute := attrPage(0x03, 6, 1<<63-1, "Read Total uncorrected errors")
	attribute.RawString = before
	disk := diskServiceInterfaces.Disk{
		Device: "da5", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true,
		SmartData: makeSCSI(30, true, attribute),
	}
	state := &diskSmartState{}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(ctx, disk, state, false)
	}
	if dismissed, err := notifications.DismissAll(ctx); err != nil || dismissed != 1 {
		t.Fatalf("dismissed=%d err=%v", dismissed, err)
	}
	attribute.RawString = after
	disk.SmartData = makeSCSI(30, true, attribute)
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(ctx, disk, state, false)
	}
	items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
	if err != nil || total != 1 || items[0].Metadata["counter_value"] != after || items[0].Metadata["previous_counter_value"] != before || items[0].Metadata["counter_delta"] != "1" {
		t.Fatalf("SCSI counter lost precision: items=%+v total=%d err=%v", items, total, err)
	}
}

func TestNewNvmeActiveFaultsBypassHistoricalBaseline(t *testing.T) {
	ctx := context.Background()
	svc, notifications := makeTestSystemService(t, &diskService.Service{})
	data := makeNVMe(30, "0x04")
	data.MediaErrors = 4
	data.AvailableSpare = 15
	data.AvailableSpareThreshold = 10
	disk := diskServiceInterfaces.Disk{
		Device: "nda0", Type: "NVMe", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true, SmartData: data,
	}
	state := &diskSmartState{}
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(ctx, disk, state, false)
	}
	if dismissed, err := notifications.DismissAll(ctx); err != nil || dismissed != 2 {
		t.Fatalf("dismissed=%d err=%v", dismissed, err)
	}
	data.CriticalWarning = "0x08"
	disk.SmartData = data
	svc.evaluateSmartData(ctx, disk, state, false)
	items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
	if err != nil || total != 1 || items[0].Metadata["critical_warning"] != "0x08" || items[0].Metadata["acknowledgement"] != "" {
		t.Fatalf("new critical bit suppressed: items=%+v total=%d err=%v", items, total, err)
	}
	data.AvailableSpareThreshold = 20
	disk.SmartData = data
	svc.evaluateSmartData(ctx, disk, state, false)
	items, total, err = notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
	if err != nil || total != 1 || items[0].OccurrenceCount != 3 || !strings.Contains(items[0].Body, "available_spare=15%") {
		t.Fatalf("new low-spare fault suppressed: items=%+v total=%d err=%v", items, total, err)
	}
}

func TestEvaluateSectorIssuesStableAttributesAlertAndRecover(t *testing.T) {
	tests := []struct {
		name          string
		id            int
		pending       string
		uncorrectable string
	}{
		{name: "pending", id: 197, pending: "4", uncorrectable: "0"},
		{name: "uncorrectable", id: 198, pending: "0", uncorrectable: "4"},
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
				svc.evaluateSectorIssues(context.Background(), disk, state, false)
			}

			var notifications []models.Notification
			if err := notifSvc.DB.Order("id ASC").Find(&notifications).Error; err != nil {
				t.Fatal(err)
			}
			if len(notifications) != 1 || !state.sectorAlerted {
				t.Fatalf("notifications=%d state=%+v", len(notifications), state)
			}
			if notifications[0].Metadata["condition"] != "sector_issues" ||
				notifications[0].Metadata["pending"] != test.pending ||
				notifications[0].Metadata["uncorrectable"] != test.uncorrectable {
				t.Fatalf("notification=%+v", notifications[0])
			}

			disk.SmartData = makeATA(30, true, 0, attr(test.id, 0))
			for range diskSmartConsecutiveClear {
				svc.evaluateSectorIssues(context.Background(), disk, state, false)
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

func TestHistoricalCounterAcknowledgementDoesNotSilenceActiveSectors(t *testing.T) {
	for _, id := range []int{197, 198} {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			ctx := context.Background()
			svc, notifications := makeTestSystemService(t, &diskService.Service{})
			disk := diskServiceInterfaces.Disk{
				Device: "da5", UUID: "d782e080-43c1-5abc-9def-123456789abc", IdentityStable: true,
				SmartData: makeATA(30, true, 0, attr(5, 4)),
			}
			state := &diskSmartState{}
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			if dismissed, err := notifications.DismissAll(ctx); err != nil || dismissed != 1 {
				t.Fatalf("dismissed=%d err=%v", dismissed, err)
			}
			state = &diskSmartState{}
			disk.SmartData = makeATA(30, true, 0, attr(5, 4), attr(id, 4))
			for range diskSmartConsecutiveTrigger {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 1 || items[0].Metadata["condition"] != "sector_issues" || items[0].Metadata["acknowledgement"] != "" {
				t.Fatalf("active sector fault suppressed: items=%+v total=%d err=%v", items, total, err)
			}
			for _, data := range []any{nil, makeATA(30, true, 0, attr(5, 4)), makeATA(30, true, 0, attr(id, -1))} {
				disk.SmartData = data
				for range diskSmartConsecutiveClear {
					svc.evaluateSmartData(ctx, disk, state, false)
				}
				items, total, err := notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
				if err != nil || total != 1 || items[0].Severity != models.NotificationSeverityWarning || !state.sectorAlerted {
					t.Fatalf("missing counter recovered: items=%+v state=%+v err=%v", items, state, err)
				}
			}
			disk.SmartData = makeATA(30, true, 0, attr(5, 4), attr(id, 0))
			for range diskSmartConsecutiveClear {
				svc.evaluateSmartData(ctx, disk, state, false)
			}
			items, total, err = notifications.List(ctx, notificationService.ListScopeActive, 100, 0)
			if err != nil || total != 1 || items[0].Metadata["condition"] != "sector_issues_cleared" || items[0].Severity != models.NotificationSeverityInfo || state.sectorAlerted {
				t.Fatalf("confirmed zero did not recover: items=%+v state=%+v err=%v", items, state, err)
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

func TestEvaluateTemperatureSCSIAlertsFromCanonicalTemperature(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(smartData any) (float64, error) {
			return 0, errors.New("wearout not available for SCSI protocol")
		},
	}
	svc, notifSvc := makeTestSystemService(t, diskService)

	scsi := makeSCSI(60, true,
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

func TestEvaluateTemperatureSCSIUnavailableSentinelDoesNotAlert(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(any) (float64, error) { return 0, errors.New("unused") },
	}
	svc, notifSvc := makeTestSystemService(t, diskService)
	disk := diskServiceInterfaces.Disk{
		Device: "da0",
		Type:   "SSD",
		SmartData: makeSCSI(0, true,
			attrPage(0x0D, 0, 0xff, "Temperature"),
		),
	}
	st := &diskSmartState{}

	for range diskSmartConsecutiveTrigger + 1 {
		svc.evaluateTemperature(context.Background(), disk, st, false)
	}
	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active != 0 || st.tempCriticalCount != 0 || st.tempWarningCount != 0 || st.tempNormalCount != 0 {
		t.Fatalf("active=%d state=%+v", active, st)
	}
}

func TestEvaluateTemperatureUnknownInterruptsConsecutiveReadings(t *testing.T) {
	diskService := &mockDiskServiceForWearout{
		wearoutFn: func(any) (float64, error) { return 0, errors.New("unused") },
	}
	svc, notifSvc := makeTestSystemService(t, diskService)
	st := &diskSmartState{}
	hot := diskServiceInterfaces.Disk{Device: "da0", Type: "SSD", SmartData: makeSCSI(60, true)}
	unknown := diskServiceInterfaces.Disk{Device: "da0", Type: "SSD", SmartData: makeSCSI(0, true)}

	svc.evaluateTemperature(context.Background(), hot, st, false)
	svc.evaluateTemperature(context.Background(), unknown, st, false)
	svc.evaluateTemperature(context.Background(), hot, st, false)
	active, err := notifSvc.CountActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active != 0 || st.tempWarningCount != 1 {
		t.Fatalf("active=%d state=%+v", active, st)
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
	disk.SmartData = warning
	for range diskSmartConsecutiveTrigger {
		svc.evaluateSmartData(context.Background(), disk, state, false)
	}
	var notification models.Notification
	if err := notifSvc.DB.First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if notification.Metadata["counter_value"] != warning.MediaErrorsExact || state.counters[notifier.DiskSmartCounterNvmeMediaErrors].notifiedValue.String() != warning.MediaErrorsExact {
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
