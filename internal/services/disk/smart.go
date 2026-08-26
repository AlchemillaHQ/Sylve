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
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

const defaultSelfTestCacheTTL = 2 * time.Second

var ErrInvalidPhysicalDisk = errors.New("invalid physical disk")
var ErrPhysicalDiskNotFound = errors.New("physical disk not found")
var ErrSelfTestTypeNotAllowed = errors.New("self-test type is not allowed")
var ErrSelfTestNotRunning = errors.New("no self-test is running")

type selfTestBackend interface {
	Read(device string) (*smart.SelfTestCapabilities, *smart.SelfTestStatus, error)
	Start(device string, kind smart.SelfTestKind) error
	Stop(device string) error
}

type librarySelfTestBackend struct{}

type selfTestCacheEntry struct {
	info      diskServiceInterfaces.DiskSelfTestInfo
	expiresAt time.Time
}

type activeSelfTestKind struct {
	kind      smart.SelfTestKind
	expiresAt time.Time
}

func (librarySelfTestBackend) Read(device string) (*smart.SelfTestCapabilities, *smart.SelfTestStatus, error) {
	dev, err := smart.OpenDevice(device)
	if err != nil {
		return nil, nil, err
	}
	defer dev.Close()

	capabilities, err := dev.SelfTestCapabilities()
	if err != nil {
		return nil, nil, err
	}
	if !capabilities.Supported {
		return capabilities, &smart.SelfTestStatus{
			Protocol:     capabilities.Protocol,
			NamespaceID:  capabilities.NamespaceID,
			State:        smart.SelfTestStateIdle,
			ProgressPct:  -1,
			RemainingPct: -1,
			Results:      []smart.SelfTestEntry{},
		}, nil
	}

	status, err := dev.SelfTestStatus()
	if err != nil {
		return nil, nil, err
	}
	return capabilities, status, nil
}

func (librarySelfTestBackend) Start(device string, kind smart.SelfTestKind) error {
	dev, err := smart.OpenDevice(device)
	if err != nil {
		return err
	}
	defer dev.Close()
	return dev.StartSelfTest(kind)
}

func (librarySelfTestBackend) Stop(device string) error {
	dev, err := smart.OpenDevice(device)
	if err != nil {
		return err
	}
	defer dev.Close()
	return dev.AbortSelfTest()
}

func nvmeAttributeInt(attribute smart.Attribute) int {
	maxInt := int(^uint(0) >> 1)
	if attribute.RawString != "" {
		value, err := strconv.ParseUint(attribute.RawString, 10, 64)
		if err != nil || value > uint64(maxInt) {
			return maxInt
		}
		return int(value)
	}
	if attribute.RawValue > uint64(maxInt) {
		return maxInt
	}
	return int(attribute.RawValue)
}

func nvmeAttributeDecimal(attribute smart.Attribute) string {
	if attribute.RawString != "" {
		return attribute.RawString
	}
	return strconv.FormatUint(attribute.RawValue, 10)
}

func smartAttributeRawValue(value uint64) int64 {
	const max = uint64(1<<63 - 1)
	if value > max {
		return int64(max)
	}
	return int64(value)
}

func smartAttributeRawString(attribute smart.Attribute) string {
	if attribute.RawString != "" {
		return attribute.RawString
	}
	if attribute.TextValue != "" {
		return attribute.TextValue
	}
	if attribute.RawValue > 1<<53-1 {
		return strconv.FormatUint(attribute.RawValue, 10)
	}
	return ""
}

func mapNVMeLibSmartToInterface(info *smart.DeviceInfo) diskServiceInterfaces.SMARTNvme {
	result := diskServiceInterfaces.SMARTNvme{
		Device: diskServiceInterfaces.DeviceInfo{
			Name:     info.Device,
			InfoName: "/dev/" + info.Device,
			Type:     "nvme",
			Protocol: info.Protocol,
		},
		Passed:          info.Passed,
		HealthKnown:     info.HealthKnown,
		PowerOnHours:    info.PowerOnHours,
		PowerCycleCount: info.PowerCycleCount,
		Temperature:     info.Temperature,
	}
	for _, attribute := range info.Attributes {
		value := nvmeAttributeInt(attribute)
		switch attribute.ID {
		case 0:
			warning := uint8(attribute.RawValue)
			result.CriticalWarning = fmt.Sprintf("0x%02x", warning)
			result.CriticalWarningState.AvailableSpare = int(warning & 0x01)
			result.CriticalWarningState.Temperature = int(warning>>1) & 0x01
			result.CriticalWarningState.DeviceReliability = int(warning>>2) & 0x01
			result.CriticalWarningState.ReadOnly = int(warning>>3) & 0x01
			result.CriticalWarningState.VolatileMemoryBackup = int(warning>>4) & 0x01
		case 3:
			result.AvailableSpare = value
		case 4:
			result.AvailableSpareThreshold = value
		case 5:
			result.PercentageUsed = value
		case 32:
			result.DataUnitsRead = value
			result.DataUnitsReadExact = nvmeAttributeDecimal(attribute)
		case 48:
			result.DataUnitsWritten = value
			result.DataUnitsWrittenExact = nvmeAttributeDecimal(attribute)
		case 64:
			result.HostReadCommands = value
			result.HostReadCommandsExact = nvmeAttributeDecimal(attribute)
		case 80:
			result.HostWriteCommands = value
			result.HostWriteCommandsExact = nvmeAttributeDecimal(attribute)
		case 96:
			result.ControllerBusyTime = value
			result.ControllerBusyTimeExact = nvmeAttributeDecimal(attribute)
		case 112:
			result.PowerCycleCount = value
			result.PowerCycleCountExact = nvmeAttributeDecimal(attribute)
		case 128:
			result.PowerOnHours = value
			result.PowerOnHoursExact = nvmeAttributeDecimal(attribute)
		case 144:
			result.UnsafeShutdowns = value
			result.UnsafeShutdownsExact = nvmeAttributeDecimal(attribute)
		case 160:
			result.MediaErrors = value
			result.MediaErrorsExact = nvmeAttributeDecimal(attribute)
		case 176:
			result.ErrorInfoLogEntries = value
			result.ErrorInfoLogEntriesExact = nvmeAttributeDecimal(attribute)
		case 192:
			result.WarningCompositeTempTime = value
		case 196:
			result.ErrorCompositeTempTime = value
		case 216:
			result.Temperature1TransitionCnt = value
		case 220:
			result.Temperature2TransitionCnt = value
		case 224:
			result.TotalTimeForTemperature1 = value
		case 228:
			result.TotalTimeForTemperature2 = value
		}
	}
	return result
}

func mapLibSmartToInterface(info *smart.DeviceInfo) diskServiceInterfaces.SmartData {
	var data diskServiceInterfaces.SmartData
	diskType := strings.ToLower(info.Protocol)

	data.Device = diskServiceInterfaces.DeviceInfo{
		Name:     info.Device,
		InfoName: "/dev/" + info.Device,
		Type:     diskType,
		Protocol: info.Protocol,
	}

	data.Passed = info.Passed
	data.HealthKnown = info.HealthKnown
	data.ChecksumValid = info.ChecksumValid
	data.PowerOnHours = info.PowerOnHours
	data.PowerCycleCount = info.PowerCycleCount
	data.Temperature = info.Temperature
	data.SelfTestStatus = diskServiceInterfaces.DiskSelfTestStatus{
		Status:       info.SelfTestStatus.Status,
		RemainingPct: info.SelfTestStatus.RemainingPct,
	}
	data.SmartCapability = info.SmartCapability

	if len(info.SCSISelfTestResults) > 0 {
		data.SCSISelfTestResults = make([]diskServiceInterfaces.DiskSCSISelfTestEntry, len(info.SCSISelfTestResults))
		for i, e := range info.SCSISelfTestResults {
			lba := e.LBA
			if !e.LBAValid {
				lba = 0
			}
			data.SCSISelfTestResults[i] = diskServiceInterfaces.DiskSCSISelfTestEntry{
				Type:          e.Type,
				Status:        e.Status,
				LifetimeHours: e.LifetimeHours,
				LBA:           lba,
				LBAValid:      e.LBAValid,
				SenseKey:      e.SenseKey,
				ASC:           e.ASC,
				ASCQ:          e.ASCQ,
			}
		}
	}

	if len(info.Attributes) > 0 {
		data.Attributes = make([]diskServiceInterfaces.ATASmartAttribute, len(info.Attributes))
		modelAttrs := smart.LookupModelAttrs(info.Model, info.Firmware)

		for i, attr := range info.Attributes {
			var def *smart.AttrDef
			if d, ok := modelAttrs[attr.ID]; ok {
				def = &d
			} else if d2, ok := smart.LookupAttrDef(attr.ID); ok {
				def = &d2
			}
			state := smart.AtaAttrState(attr.Value, attr.Worst, attr.Threshold, def)
			whenFailed := ""
			switch state {
			case smart.AttrStateFailedNow:
				whenFailed = "FAILING_NOW"
			case smart.AttrStateFailedPast:
				whenFailed = "In_the_past"
			}
			rawString := smartAttributeRawString(attr)

			data.Attributes[i] = diskServiceInterfaces.ATASmartAttribute{
				Page:        int(attr.Page),
				ID:          int(attr.ID),
				Name:        strings.ReplaceAll(attr.Name, "_", " "),
				Value:       attr.Value,
				Worst:       attr.Worst,
				Thresh:      attr.Threshold,
				RawValue:    smartAttributeRawValue(attr.RawValue),
				RawString:   rawString,
				State:       state,
				WhenFailed:  whenFailed,
				PreFailure:  attr.Flags.PreFailure,
				Online:      attr.Flags.Online,
				Performance: attr.Flags.Performance,
				ErrorRate:   attr.Flags.ErrorRate,
				EventCount:  attr.Flags.EventCount,
				AutoKeep:    attr.Flags.SelfPreserving,
			}
		}
	}

	return data
}

func normalizePhysicalDeviceName(device string) (string, error) {
	device = strings.TrimSpace(device)
	if strings.HasPrefix(device, "/dev/") {
		device = strings.TrimPrefix(device, "/dev/")
	}
	if device == "" || device == "." || device == ".." || strings.ContainsAny(device, "/\\\x00") {
		return "", ErrInvalidPhysicalDisk
	}
	return device, nil
}

func (s *Service) resolvePhysicalDisk(device string) (diskServiceInterfaces.DiskInfo, error) {
	name, err := normalizePhysicalDeviceName(device)
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, err
	}

	disks, err := s.physicalDisks()
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, err
	}
	for _, disk := range disks {
		if disk.Name == name {
			return disk, nil
		}
	}
	return diskServiceInterfaces.DiskInfo{}, fmt.Errorf("%w: %s", ErrPhysicalDiskNotFound, name)
}

func (s *Service) resolvePhysicalDiskForRead(device string) (diskServiceInterfaces.DiskInfo, error) {
	name, err := normalizePhysicalDeviceName(device)
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, err
	}
	now := time.Now()
	s.physicalDiskCacheMu.Lock()
	if entry, ok := s.physicalDiskCache[name]; ok && now.Before(entry.expiresAt) {
		s.physicalDiskCacheMu.Unlock()
		return entry.disk, nil
	}
	disks, err := s.physicalDisks()
	if err != nil {
		s.physicalDiskCacheMu.Unlock()
		return diskServiceInterfaces.DiskInfo{}, err
	}
	s.physicalDiskCache = make(map[string]physicalDiskResolveCacheEntry, len(disks))
	var resolved diskServiceInterfaces.DiskInfo
	found := false
	for _, disk := range disks {
		s.physicalDiskCache[disk.Name] = physicalDiskResolveCacheEntry{
			disk:      disk,
			expiresAt: now.Add(physicalDiskResolveCacheTTL),
		}
		if disk.Name == name {
			resolved = disk
			found = true
		}
	}
	s.physicalDiskCacheMu.Unlock()
	if !found {
		return diskServiceInterfaces.DiskInfo{}, fmt.Errorf("%w: %s", ErrPhysicalDiskNotFound, name)
	}
	return resolved, nil
}

func mapSelfTestCapabilities(capabilities *smart.SelfTestCapabilities) diskServiceInterfaces.DiskSelfTestCapabilities {
	if capabilities == nil {
		return diskServiceInterfaces.DiskSelfTestCapabilities{}
	}
	return diskServiceInterfaces.DiskSelfTestCapabilities{
		Protocol:                  capabilities.Protocol,
		Scope:                     capabilities.Scope,
		NamespaceID:               capabilities.NamespaceID,
		SingleOperation:           capabilities.SingleOperation,
		ExecutionSupportKnown:     capabilities.ExecutionSupportKnown,
		Supported:                 capabilities.Supported,
		Offline:                   capabilities.Offline,
		Default:                   capabilities.Default,
		Short:                     capabilities.Short,
		Extended:                  capabilities.Extended,
		Conveyance:                capabilities.Conveyance,
		Selective:                 capabilities.Selective,
		ShortCaptive:              capabilities.ShortCaptive,
		ExtendedCaptive:           capabilities.ExtendedCaptive,
		ConveyanceCaptive:         capabilities.ConveyanceCaptive,
		SelectiveCaptive:          capabilities.SelectiveCaptive,
		Abort:                     capabilities.Abort,
		ResultLog:                 capabilities.ResultLog,
		Progress:                  capabilities.Progress,
		OfflineDurationMinutes:    capabilities.OfflineDurationMinutes,
		ShortDurationMinutes:      capabilities.ShortDurationMinutes,
		ExtendedDurationMinutes:   capabilities.ExtendedDurationMinutes,
		ConveyanceDurationMinutes: capabilities.ConveyanceDurationMinutes,
	}
}

func mapSelfTestResult(entry smart.SelfTestEntry) diskServiceInterfaces.DiskSelfTestResult {
	lba := entry.LBA
	if !entry.LBAValid {
		lba = 0
	}
	nsid := entry.NSID
	if !entry.NSIDValid {
		nsid = 0
	}
	return diskServiceInterfaces.DiskSelfTestResult{
		Protocol:            entry.Protocol,
		Type:                entry.Type,
		Mode:                entry.Mode,
		Status:              entry.Status,
		Outcome:             entry.Outcome,
		RemainingPct:        entry.RemainingPct,
		LifetimeHours:       entry.LifetimeHours,
		LifetimeHoursExact:  strconv.FormatUint(entry.LifetimeHours, 10),
		LBA:                 lba,
		LBAExact:            strconv.FormatUint(lba, 10),
		LBAValid:            entry.LBAValid,
		NSID:                nsid,
		NSIDValid:           entry.NSIDValid,
		SegmentNum:          entry.SegmentNum,
		SenseKey:            entry.SenseKey,
		ASC:                 entry.ASC,
		ASCQ:                entry.ASCQ,
		StatusCodeType:      entry.StatusCodeType,
		StatusCodeTypeValid: entry.StatusCodeTypeValid,
		StatusCode:          entry.StatusCode,
		StatusCodeValid:     entry.StatusCodeValid,
		Checkpoint:          entry.Checkpoint,
		ParameterCode:       entry.ParameterCode,
		VendorSpecific:      entry.VendorSpecific,
	}
}

func mapSelfTestState(status *smart.SelfTestStatus) diskServiceInterfaces.DiskSelfTestState {
	if status == nil {
		return diskServiceInterfaces.DiskSelfTestState{
			ProgressPct:  -1,
			RemainingPct: -1,
			Results:      []diskServiceInterfaces.DiskSelfTestResult{},
		}
	}
	result := diskServiceInterfaces.DiskSelfTestState{
		Protocol:                 status.Protocol,
		NamespaceID:              status.NamespaceID,
		State:                    status.State,
		ExecutionStatus:          status.ExecutionStatus,
		Type:                     string(status.Type),
		Running:                  status.Running,
		ProgressPct:              status.ProgressPct,
		ProgressKnown:            status.ProgressKnown,
		RemainingPct:             status.RemainingPct,
		RemainingKnown:           status.RemainingKnown,
		EstimatedDurationMinutes: status.EstimatedDurationMinutes,
		OfflineCollectionStatus:  status.OfflineCollectionStatus,
		OfflineCollectionRunning: status.OfflineCollectionRunning,
		ChecksumValid:            status.ChecksumValid,
		Results:                  make([]diskServiceInterfaces.DiskSelfTestResult, len(status.Results)),
	}
	for i, entry := range status.Results {
		result.Results[i] = mapSelfTestResult(entry)
	}
	return result
}

func isAbortedSelfTestExecution(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "aborted") || value == "interrupted" || value == "cancelled"
}

func moveSelfTestResultToFront(results []diskServiceInterfaces.DiskSelfTestResult, index int) {
	if index <= 0 || index >= len(results) {
		return
	}
	result := results[index]
	copy(results[1:index+1], results[:index])
	results[0] = result
}

func promoteRunningSelfTestResult(info *diskServiceInterfaces.DiskSelfTestInfo) {
	if info == nil || !info.Status.Running && info.Status.State != smart.SelfTestStateAmbiguous {
		return
	}
	for i := range info.Status.Results {
		result := info.Status.Results[i]
		if result.Status != "in_progress" && result.Outcome != smart.SelfTestOutcomeInProgress {
			continue
		}
		if info.Status.Type != "" && !selfTestTypeMatches(info.Status.Type, result.Type) {
			continue
		}
		moveSelfTestResultToFront(info.Status.Results, i)
		return
	}
}

func reconcileAbortedSelfTestInfo(info *diskServiceInterfaces.DiskSelfTestInfo, testType string) {
	if info == nil {
		return
	}
	status := &info.Status
	executionStatus := strings.ToLower(strings.TrimSpace(status.ExecutionStatus))
	if testType == "" {
		testType = status.Type
	}
	resultIndex := -1
	for i := range status.Results {
		result := &status.Results[i]
		resultStatus := strings.ToLower(strings.TrimSpace(result.Status))
		resultOutcome := strings.ToLower(strings.TrimSpace(result.Outcome))
		if resultStatus != "in_progress" && resultOutcome != smart.SelfTestOutcomeInProgress {
			continue
		}
		if testType != "" && !selfTestTypeMatches(testType, result.Type) {
			continue
		}
		resultIndex = i
		break
	}
	staleExecution := status.Running || status.State == smart.SelfTestStateAmbiguous || executionStatus == smart.SelfTestOutcomeInProgress
	if !staleExecution && resultIndex < 0 {
		return
	}
	abortStatus := executionStatus
	if !isAbortedSelfTestExecution(abortStatus) {
		abortStatus = "aborted_by_host"
	}
	status.State = smart.SelfTestStateIdle
	status.ExecutionStatus = abortStatus
	status.Type = ""
	status.Running = false
	status.ProgressPct = -1
	status.ProgressKnown = false
	status.RemainingPct = -1
	status.RemainingKnown = false
	status.EstimatedDurationMinutes = 0
	if status.OfflineCollectionRunning {
		status.OfflineCollectionStatus = "aborted_by_host"
		status.OfflineCollectionRunning = false
	}
	if resultIndex >= 0 {
		result := &status.Results[resultIndex]
		result.Status = abortStatus
		result.Outcome = smart.SelfTestOutcomeAborted
		moveSelfTestResultToFront(status.Results, resultIndex)
	}
}

func reconcileStartedSelfTestInfo(info *diskServiceInterfaces.DiskSelfTestInfo, kind smart.SelfTestKind) {
	if info == nil || info.Status.Running || info.Status.State == smart.SelfTestStateAmbiguous {
		return
	}
	info.Status.State = smart.SelfTestStateRunning
	info.Status.ExecutionStatus = smart.SelfTestOutcomeInProgress
	info.Status.Type = string(kind)
	info.Status.Running = true
	info.Status.ProgressPct = -1
	info.Status.ProgressKnown = false
	info.Status.RemainingPct = -1
	info.Status.RemainingKnown = false
}

func cloneSelfTestInfo(info diskServiceInterfaces.DiskSelfTestInfo) diskServiceInterfaces.DiskSelfTestInfo {
	info.Status.Results = append([]diskServiceInterfaces.DiskSelfTestResult(nil), info.Status.Results...)
	if info.Status.Results == nil {
		info.Status.Results = []diskServiceInterfaces.DiskSelfTestResult{}
	}
	return info
}

func (s *Service) selfTestBackend() selfTestBackend {
	if s.selfTestDriver != nil {
		return s.selfTestDriver
	}
	return librarySelfTestBackend{}
}

func (s *Service) selfTestDeviceMutex(device string) *sync.Mutex {
	lock, _ := s.selfTestDeviceLock.LoadOrStore(device, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *Service) cachedSelfTestInfo(device string) (diskServiceInterfaces.DiskSelfTestInfo, bool) {
	s.selfTestCacheMu.Lock()
	defer s.selfTestCacheMu.Unlock()
	entry, ok := s.selfTestCache[device]
	if !ok || !time.Now().Before(entry.expiresAt) {
		if ok {
			delete(s.selfTestCache, device)
		}
		return diskServiceInterfaces.DiskSelfTestInfo{}, false
	}
	return cloneSelfTestInfo(entry.info), true
}

func (s *Service) storeSelfTestInfo(device string, info diskServiceInterfaces.DiskSelfTestInfo) {
	ttl := s.selfTestCacheTTL
	if ttl <= 0 {
		ttl = defaultSelfTestCacheTTL
	}
	s.selfTestCacheMu.Lock()
	if s.selfTestCache == nil {
		s.selfTestCache = make(map[string]selfTestCacheEntry)
	}
	s.selfTestCache[device] = selfTestCacheEntry{
		info:      cloneSelfTestInfo(info),
		expiresAt: time.Now().Add(ttl),
	}
	s.selfTestCacheMu.Unlock()
}

func (s *Service) invalidateSelfTestInfo(device string) {
	s.selfTestCacheMu.Lock()
	delete(s.selfTestCache, device)
	s.selfTestCacheMu.Unlock()
	s.selfTestReadGroup.Forget(device)
}

func (s *Service) readSelfTestInfo(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	capabilities, status, err := s.selfTestBackend().Read(device)
	if err != nil {
		return nil, err
	}
	if status != nil {
		if kind, ok := s.loadActiveSelfTestKind(device); ok {
			if status.Running && status.Type == "" {
				status.Type = kind
				if capabilities != nil {
					status.EstimatedDurationMinutes = capabilities.DurationMinutes(kind)
				}
			}
			if !status.Running && status.ExecutionStatus != "" && status.ExecutionStatus != smart.SelfTestOutcomeInProgress {
				s.selfTestActiveKinds.Delete(device)
			}
		}
	}
	info := diskServiceInterfaces.DiskSelfTestInfo{
		Device:       device,
		Capabilities: mapSelfTestCapabilities(capabilities),
		Status:       mapSelfTestState(status),
	}
	promoteRunningSelfTestResult(&info)
	return &info, nil
}

func (s *Service) storeActiveSelfTestKind(device string, kind smart.SelfTestKind) {
	duration := 6 * time.Hour
	if kind == smart.SelfTestKindExtended || kind == smart.SelfTestKindExtendedCaptive {
		duration = 48 * time.Hour
	}
	s.selfTestActiveKinds.Store(device, activeSelfTestKind{kind: kind, expiresAt: time.Now().Add(duration)})
}

func (s *Service) loadActiveSelfTestKind(device string) (smart.SelfTestKind, bool) {
	value, ok := s.selfTestActiveKinds.Load(device)
	if !ok {
		return "", false
	}
	active, valid := value.(activeSelfTestKind)
	if !valid || !time.Now().Before(active.expiresAt) {
		s.selfTestActiveKinds.Delete(device)
		return "", false
	}
	return active.kind, true
}

func (s *Service) getSelfTestInfoForDevice(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	if info, ok := s.cachedSelfTestInfo(device); ok {
		return &info, nil
	}

	value, err, _ := s.selfTestReadGroup.Do(device, func() (any, error) {
		if info, ok := s.cachedSelfTestInfo(device); ok {
			return info, nil
		}
		lock := s.selfTestDeviceMutex(device)
		lock.Lock()
		defer lock.Unlock()
		if info, ok := s.cachedSelfTestInfo(device); ok {
			return info, nil
		}
		info, err := s.readSelfTestInfo(device)
		if err != nil {
			return nil, err
		}
		s.storeSelfTestInfo(device, *info)
		return cloneSelfTestInfo(*info), nil
	})
	if err != nil {
		return nil, err
	}
	info := value.(diskServiceInterfaces.DiskSelfTestInfo)
	return &info, nil
}

func (s *Service) GetSelfTestInfo(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	disk, err := s.resolvePhysicalDiskForRead(device)
	if err != nil {
		return nil, err
	}
	info, err := s.getSelfTestInfoForDevice(disk.Name)
	if err != nil {
		return nil, err
	}
	if err := s.attachSelfTestRunTimes(context.Background(), disk, info); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_history_read_failed")
	}
	return info, nil
}

func (s *Service) reconcileSelfTestRunsBeforeStart(ctx context.Context, disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	info, err := s.readSelfTestInfo(disk.Name)
	if err != nil {
		return nil, err
	}
	raw := cloneSelfTestInfo(*info)
	if err := s.attachSelfTestRunTimes(ctx, disk, info); err != nil {
		return nil, err
	}
	return &raw, nil
}

func allowedSelfTestKind(testType string) (smart.SelfTestKind, error) {
	switch strings.ToLower(strings.TrimSpace(testType)) {
	case "short":
		return smart.SelfTestKindShort, nil
	case "extended":
		return smart.SelfTestKindExtended, nil
	case "conveyance":
		return smart.SelfTestKindConveyance, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrSelfTestTypeNotAllowed, testType)
	}
}

func internalSelfTestKind(testType string) (smart.SelfTestKind, error) {
	switch strings.ToLower(strings.TrimSpace(testType)) {
	case "offline":
		return smart.SelfTestKindOffline, nil
	case "default":
		return smart.SelfTestKindDefault, nil
	case "short":
		return smart.SelfTestKindShort, nil
	case "long", "extended":
		return smart.SelfTestKindExtended, nil
	case "conveyance":
		return smart.SelfTestKindConveyance, nil
	case "short_captive":
		return smart.SelfTestKindShortCaptive, nil
	case "extended_captive":
		return smart.SelfTestKindExtendedCaptive, nil
	case "conveyance_captive":
		return smart.SelfTestKindConveyanceCaptive, nil
	case "selective", "selective_captive":
		return "", smart.ErrSelfTestConfigurationRequired
	default:
		return "", fmt.Errorf("%w: %s", ErrSelfTestTypeNotAllowed, testType)
	}
}

func (s *Service) startSelfTestKind(ctx context.Context, device string, kind smart.SelfTestKind) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	disk, err := s.resolvePhysicalDisk(device)
	if err != nil {
		return nil, err
	}

	leaseToken, err := s.acquireSelfTestMutationLease(ctx)
	if err != nil {
		return nil, err
	}
	defer s.releaseSelfTestMutationLease(leaseToken)
	s.selfTestScheduleMu.Lock()
	defer s.selfTestScheduleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if active, err := s.reservedSelfTestScheduleForDisk(ctx, disk); err != nil {
		return nil, err
	} else if active != nil {
		return nil, ErrSelfTestScheduleRunning
	}
	lock := s.selfTestDeviceMutex(disk.Name)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	historyCtx := context.WithoutCancel(ctx)
	before, err := s.reconcileSelfTestRunsBeforeStart(historyCtx, disk)
	if err != nil {
		return nil, err
	}
	if before.Status.Running || before.Status.State == smart.SelfTestStateAmbiguous {
		return nil, smart.ErrSelfTestInProgress
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	startedAt := time.Now().UTC()
	baselineValid := !strings.EqualFold(before.Status.Protocol, "ATA") || before.Status.ChecksumValid
	if err := s.recordManualSelfTestRun(historyCtx, disk, string(kind), before, baselineValid, startedAt); err != nil {
		return nil, err
	}
	runKey := manualSelfTestRunKey(selfTestRunDiskKey(disk), startedAt)
	if err := ctx.Err(); err != nil {
		if discardErr := s.discardSelfTestRun(historyCtx, runKey); discardErr != nil {
			return nil, errors.Join(err, discardErr)
		}
		return nil, err
	}
	s.invalidateSelfTestInfo(disk.Name)
	if err := s.selfTestBackend().Start(disk.Name, kind); err != nil {
		if discardErr := s.discardSelfTestRun(historyCtx, runKey); discardErr != nil {
			return nil, errors.Join(err, discardErr)
		}
		return nil, err
	}
	if err := s.supersedeUnfinishedSelfTestRuns(historyCtx, selfTestRunDiskKey(disk), runKey, startedAt); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_supersede_failed")
	}
	s.storeActiveSelfTestKind(disk.Name, kind)
	info, readErr := s.readSelfTestInfo(disk.Name)
	if readErr != nil {
		info = &diskServiceInterfaces.DiskSelfTestInfo{
			Device: disk.Name,
			Status: diskServiceInterfaces.DiskSelfTestState{
				State:           smart.SelfTestStateRunning,
				ExecutionStatus: smart.SelfTestOutcomeInProgress,
				Type:            string(kind),
				Running:         true,
				ProgressPct:     -1,
				RemainingPct:    -1,
				Results:         []diskServiceInterfaces.DiskSelfTestResult{},
			},
		}
	}
	immediateResult := false
	if readErr == nil && baselineValid {
		current := mappedSelfTestResultLogFingerprint(info.Status.Results, string(kind))
		baseline := mappedSelfTestResultLogFingerprint(before.Status.Results, string(kind))
		immediateResult = current != baseline && newMappedSelfTestResult(info.Status.Results, string(kind), baseline) != nil
	}
	if !immediateResult {
		reconcileStartedSelfTestInfo(info, kind)
		s.storeActiveSelfTestKind(disk.Name, kind)
	}
	s.storeSelfTestInfo(disk.Name, *info)
	if err := s.attachSelfTestRunTimes(historyCtx, disk, info); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_history_read_failed")
	}
	return info, nil
}

func (s *Service) StartSelfTestContext(ctx context.Context, device, testType string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	kind, err := allowedSelfTestKind(testType)
	if err != nil {
		return nil, err
	}
	return s.startSelfTestKind(ctx, device, kind)
}

func (s *Service) StartSelfTest(device, testType string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	return s.StartSelfTestContext(context.Background(), device, testType)
}

func (s *Service) StopSelfTest(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	disk, err := s.resolvePhysicalDisk(device)
	if err != nil {
		return nil, err
	}

	leaseToken, err := s.acquireSelfTestMutationLease(context.Background())
	if err != nil {
		return nil, err
	}
	defer s.releaseSelfTestMutationLease(leaseToken)
	s.selfTestScheduleMu.Lock()
	activeSchedule, err := s.activeSelfTestScheduleForDisk(context.Background(), disk)
	if err != nil {
		s.selfTestScheduleMu.Unlock()
		return nil, err
	}
	lock := s.selfTestDeviceMutex(disk.Name)
	lock.Lock()
	abortedType := ""
	if kind, ok := s.loadActiveSelfTestKind(disk.Name); ok {
		abortedType = string(kind)
	} else if activeSchedule != nil {
		abortedType = activeSchedule.TestType
	}
	s.invalidateSelfTestInfo(disk.Name)
	if err := s.selfTestBackend().Stop(disk.Name); err != nil {
		lock.Unlock()
		s.selfTestScheduleMu.Unlock()
		return nil, err
	}
	s.selfTestActiveKinds.Delete(disk.Name)
	info, readErr := s.readSelfTestInfo(disk.Name)
	if readErr != nil {
		info = &diskServiceInterfaces.DiskSelfTestInfo{
			Device: disk.Name,
			Status: diskServiceInterfaces.DiskSelfTestState{
				State:           smart.SelfTestStateIdle,
				ExecutionStatus: "abort_requested",
				ProgressPct:     -1,
				RemainingPct:    -1,
				Results:         []diskServiceInterfaces.DiskSelfTestResult{},
			},
		}
	}
	historyCtx := context.Background()
	finalStatus := ""
	var finalResult *diskServiceInterfaces.DiskSelfTestResult
	if readErr == nil {
		result, resultErr := s.newActiveSelfTestResult(historyCtx, disk, info)
		if resultErr != nil {
			logger.L.Error().Err(resultErr).Str("device", disk.Name).Msg("disk_self_test_run_result_read_failed")
		} else if result != nil {
			finalResult = result
			finalStatus = mappedSelfTestRunStatus(*result)
		}
	}
	if finalResult == nil {
		reconcileAbortedSelfTestInfo(info, abortedType)
		finalStatus = smartSelfTestRunAborted
		for i := range info.Status.Results {
			result := &info.Status.Results[i]
			if strings.ToLower(strings.TrimSpace(result.Outcome)) == smart.SelfTestOutcomeAborted && (abortedType == "" || selfTestTypeMatches(abortedType, result.Type)) {
				finalResult = result
				break
			}
		}
	}
	if err := s.finishActiveSelfTestRun(historyCtx, disk, finalStatus, time.Now().UTC(), finalResult); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_update_failed")
	}
	s.storeSelfTestInfo(disk.Name, *info)
	if err := s.attachSelfTestRunTimes(historyCtx, disk, info); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_history_read_failed")
	}
	if finalStatus == smartSelfTestRunAborted {
		if err := s.markSelfTestScheduleManuallyAborted(context.Background(), activeSchedule); err != nil {
			lock.Unlock()
			s.selfTestScheduleMu.Unlock()
			return nil, err
		}
	}
	lock.Unlock()
	s.selfTestScheduleMu.Unlock()
	return info, nil
}

func (s *Service) getSmartData(disk diskServiceInterfaces.DiskInfo, includeSelfTestLog bool) (interface{}, *diskServiceInterfaces.DiskSelfTestLog, error) {
	dev, err := smart.OpenDevice(disk.Name)
	if err != nil {
		return nil, nil, err
	}
	defer dev.Close()

	smartInfo, err := dev.Read()
	if err != nil {
		return nil, nil, err
	}

	var selfTestLog *smart.SelfTestLog
	if includeSelfTestLog {
		selfTestLog = smartInfo.SCSISelfTestLog
		if selfTestLog == nil {
			selfTestLog, err = dev.ReadSelfTestLog()
			if err != nil {
				selfTestLog = nil
			}
		}
	}

	var result any
	if smartInfo.Protocol == "NVMe" {
		result = mapNVMeLibSmartToInterface(smartInfo)
	} else {
		result = mapLibSmartToInterface(smartInfo)
	}
	var logResult *diskServiceInterfaces.DiskSelfTestLog
	if selfTestLog != nil {
		lr := mapSelfTestLogToInterface(selfTestLog)
		logResult = &lr
	}
	return result, logResult, nil
}

func (s *Service) GetSmartData(disk diskServiceInterfaces.DiskInfo) (interface{}, *diskServiceInterfaces.DiskSelfTestLog, error) {
	return s.getSmartData(disk, true)
}

func mapSelfTestLogToInterface(log *smart.SelfTestLog) diskServiceInterfaces.DiskSelfTestLog {
	result := diskServiceInterfaces.DiskSelfTestLog{
		InProgress:    log.InProgress,
		ProgressPct:   log.ProgressPct,
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
	}
	for i, e := range log.Entries {
		result.Entries[i] = mapSelfTestEntryToInterface(e)
	}
	return result
}

func mapSelfTestEntryToInterface(entry smart.SelfTestEntry) diskServiceInterfaces.DiskSelfTestEntry {
	lba := entry.LBA
	if !entry.LBAValid {
		lba = 0
	}
	nsid := entry.NSID
	if !entry.NSIDValid {
		nsid = 0
	}
	return diskServiceInterfaces.DiskSelfTestEntry{
		Type:          entry.Type,
		Status:        entry.Status,
		RemainingPct:  entry.RemainingPct,
		LifetimeHours: entry.LifetimeHours,
		LBA:           lba,
		LBAValid:      entry.LBAValid,
		NSID:          nsid,
		NSIDValid:     entry.NSIDValid,
	}
}

func mapSelfTestStatusToInterface(status *smart.SelfTestStatus) diskServiceInterfaces.DiskSelfTestLog {
	result := diskServiceInterfaces.DiskSelfTestLog{
		InProgress:    status.Running,
		ProgressPct:   status.ProgressPct,
		ChecksumValid: status.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskSelfTestEntry, len(status.Results)),
	}
	for i, e := range status.Results {
		result.Entries[i] = mapSelfTestEntryToInterface(e)
	}
	return result
}

func (s *Service) GetWearOut(smartData any) (float64, error) {
	if smartData == nil {
		return 0, errors.New("no SMART data available")
	}

	if nvmeData, ok := smartData.(diskServiceInterfaces.SMARTNvme); ok {
		return float64(nvmeData.PercentageUsed), nil
	}

	if data, ok := smartData.(diskServiceInterfaces.SmartData); ok {
		protocol := strings.ToUpper(strings.TrimSpace(data.Device.Protocol))
		if protocol == "ATA" && !data.ChecksumValid {
			return 0, errors.New("SMART attribute checksum is invalid")
		}
		var wear177, wear202, wear230, wear231, wear232, wear233, wearScsi *float64

		for _, attr := range data.Attributes {
			if protocol == "SCSI" {
				if attr.Page == 0x11 && attr.ID == 1 && attr.RawValue >= 0 && attr.RawValue <= 100 {
					val := float64(attr.RawValue)
					wearScsi = &val
				}
				continue
			}
			switch attr.ID {
			case 177:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear177 = &val
				}
			case 202:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear202 = &val
				}
			case 230:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear230 = &val
				}
			case 231:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear231 = &val
				}
			case 232:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear232 = &val
				}
			case 233:
				if attr.Value >= 0 && attr.Value <= 100 {
					val := 100.0 - float64(attr.Value)
					wear233 = &val
				}
			}
		}

		if wearScsi != nil {
			return *wearScsi, nil
		}
		if wear202 != nil {
			return *wear202, nil
		}
		if wear231 != nil {
			return *wear231, nil
		}
		if wear232 != nil {
			return *wear232, nil
		}
		if wear233 != nil {
			return *wear233, nil
		}
		if wear230 != nil {
			return *wear230, nil
		}
		if wear177 != nil {
			return *wear177, nil
		}

		return 0, errors.New("no SSD wearout indicators found")
	}

	return 0, errors.New("unsupported SMART data type")
}

func (s *Service) formatWearOut(diskType string, smartData any) string {
	switch strings.ToUpper(diskType) {
	case "HDD":
		return "N/A"
	case "SSD", "NVME":
		wearOut, err := s.GetWearOut(smartData)
		if err != nil {
			return "Unknown"
		}
		return fmt.Sprintf("%.2f", wearOut)
	default:
		return "Unknown"
	}
}

func (s *Service) RunSelfTest(disk diskServiceInterfaces.DiskInfo, testType string) error {
	if strings.EqualFold(strings.TrimSpace(testType), "abort") {
		_, err := s.StopSelfTest(disk.Name)
		return err
	}
	kind, err := internalSelfTestKind(testType)
	if err != nil {
		return err
	}
	_, err = s.startSelfTestKind(context.Background(), disk.Name, kind)
	return err
}

func (s *Service) GetSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	dev, err := smart.OpenDevice(disk.Name)
	if err != nil {
		return nil, err
	}
	defer dev.Close()

	status, err := dev.SelfTestStatus()
	if err != nil {
		return nil, err
	}

	result := mapSelfTestStatusToInterface(status)
	return &result, nil
}

func (s *Service) GetErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	log, err := smart.ReadErrorLog(disk.Name)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskErrorLog{
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskErrorEntry, len(log.Entries)),
	}

	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskErrorEntry{
			ErrorData:     e.ErrorData,
			ExtendedData:  e.ExtendedData,
			LifetimeHours: e.LifetimeHours,
			LBA:           e.LBA,
			Status:        e.Status,
			Error:         e.Error,
			SectorCount:   e.SectorCount,
			Device:        e.Device,
		}
	}

	return result, nil
}

func (s *Service) GetNVMEErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskNVMEErrorLog, error) {
	log, err := smart.ReadNVMEErrorLog(disk.Name)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskNVMEErrorLog{
		Entries: make([]diskServiceInterfaces.DiskNVMEErrorEntry, len(log.Entries)),
	}

	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskNVMEErrorEntry{
			ErrorCount:  e.ErrorCount,
			SQID:        e.SQID,
			CommandID:   e.CommandID,
			StatusField: e.StatusField,
			ParamError:  e.ParamError,
			LBA:         e.LBA,
			NamespaceID: e.NamespaceID,
		}
	}

	return result, nil
}

func (s *Service) GetSCTStatus(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTStatus, error) {
	st, err := smart.ReadSCTStatus(disk.Name)
	if err != nil {
		return nil, err
	}

	return &diskServiceInterfaces.DiskSCTStatus{
		FormatVersion:     st.FormatVersion,
		SCTVersion:        st.SCTVersion,
		SCTSpec:           st.SCTSpec,
		StatusFlags:       st.StatusFlags,
		DeviceState:       st.DeviceState,
		ExtStatusCode:     st.ExtStatusCode,
		ActionCode:        st.ActionCode,
		FunctionCode:      st.FunctionCode,
		LBACurrent:        st.LBACurrent,
		CurrentTemp:       st.CurrentTemp,
		MinTempCycle:      st.MinTempCycle,
		MaxTempCycle:      st.MaxTempCycle,
		LifetimeMinTemp:   st.LifetimeMinTemp,
		LifetimeMaxTemp:   st.LifetimeMaxTemp,
		MaxOpLimit:        st.MaxOpLimit,
		OverTempCount:     st.OverTempCount,
		UnderTempCount:    st.UnderTempCount,
		SmartStatusPassed: st.SmartStatusPassed,
		MinERCTime:        st.MinERCTime,
	}, nil
}

func (s *Service) GetSCTTempHistory(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTTempHistory, error) {
	ht, err := smart.ReadSCTTempHistory(disk.Name)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskSCTTempHistory{
		SamplingPeriod: ht.SamplingPeriod,
		Interval:       ht.Interval,
		MaxOpLimit:     ht.MaxOpLimit,
		OverLimit:      ht.OverLimit,
		MinOpLimit:     ht.MinOpLimit,
		UnderLimit:     ht.UnderLimit,
		CBSize:         ht.CBSize,
		CBIndex:        ht.CBIndex,
		Samples:        make([]diskServiceInterfaces.DiskSCTTempSample, len(ht.Samples)),
	}

	for i, s := range ht.Samples {
		result.Samples[i] = diskServiceInterfaces.DiskSCTTempSample{
			Temperature: s.Temperature,
		}
	}

	return result, nil
}

func (s *Service) AbortSelfTest(disk diskServiceInterfaces.DiskInfo) error {
	_, err := s.StopSelfTest(disk.Name)
	return err
}

func (s *Service) GetLogDirectory(disk diskServiceInterfaces.DiskInfo) ([]uint8, error) {
	dev := "/dev/" + disk.Name
	return smart.ReadLogDirectory(dev)
}

func (s *Service) GetExtendedErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	dev := "/dev/" + disk.Name
	log, err := smart.ReadExtendedErrorLog(dev)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskErrorLog{
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskErrorEntry, len(log.Entries)),
	}
	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskErrorEntry{
			ErrorData:     e.ErrorData,
			ExtendedData:  e.ExtendedData,
			LifetimeHours: e.LifetimeHours,
			LBA:           e.LBA,
			Status:        e.Status,
			Error:         e.Error,
			SectorCount:   e.SectorCount,
			Device:        e.Device,
		}
	}

	return result, nil
}

func (s *Service) GetExtendedSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	dev := "/dev/" + disk.Name
	log, err := smart.ReadExtendedSelfTestLog(dev)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskSelfTestLog{
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
		InProgress:    log.InProgress,
	}
	for i, e := range log.Entries {
		result.Entries[i] = mapSelfTestEntryToInterface(e)
	}

	return result, nil
}

func (s *Service) GetDeviceStatistics(disk diskServiceInterfaces.DiskInfo) ([]diskServiceInterfaces.DiskAttribute, error) {
	dev := "/dev/" + disk.Name
	attrs, err := smart.ReadDeviceStatistics(dev)
	if err != nil {
		return nil, err
	}

	result := make([]diskServiceInterfaces.DiskAttribute, len(attrs))
	for i, a := range attrs {
		result[i] = diskServiceInterfaces.DiskAttribute{
			Page:      a.Page,
			ID:        a.ID,
			Name:      a.Name,
			Value:     a.Value,
			Worst:     a.Worst,
			Threshold: a.Threshold,
			RawValue:  a.RawValue,
			RawString: a.RawString,
		}
	}

	return result, nil
}

func (s *Service) GetSelectiveSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	dev := "/dev/" + disk.Name
	log, err := smart.ReadSelectiveSelfTestLog(dev)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskSelfTestLog{
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
		InProgress:    log.InProgress,
	}
	for i, e := range log.Entries {
		result.Entries[i] = mapSelfTestEntryToInterface(e)
	}

	return result, nil
}

func (s *Service) SetSCTFeatureControl(disk diskServiceInterfaces.DiskInfo, featureCode uint16, state uint16, persistent bool) error {
	dev := "/dev/" + disk.Name
	return smart.SetSCTFeatureControl(dev, featureCode, state, persistent)
}

func (s *Service) SetSCTErrorRecoveryControl(disk diskServiceInterfaces.DiskInfo, read bool, timeLimit uint16) error {
	dev := "/dev/" + disk.Name
	return smart.SetSCTErrorRecoveryControl(dev, read, timeLimit)
}
