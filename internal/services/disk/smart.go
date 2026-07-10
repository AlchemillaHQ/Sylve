// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

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

func (s *Service) GetSmartData(disk diskServiceInterfaces.DiskInfo) (interface{}, *diskServiceInterfaces.DiskSelfTestLog, error) {
	dev, err := smart.OpenDevice(disk.Name)
	if err != nil {
		return nil, nil, err
	}
	defer dev.Close()

	smartInfo, err := dev.Read()
	if err != nil {
		return nil, nil, err
	}

	selfTestLog := smartInfo.SCSISelfTestLog
	if selfTestLog == nil {
		selfTestLog, err = dev.ReadSelfTestLog()
		if err != nil {
			selfTestLog = nil
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
		var wear177, wear202, wear230, wear231, wear232, wear233, wearScsi *float64

		for _, attr := range data.Attributes {
			if attr.Page == 0x11 && attr.ID == 1 && attr.Value > 0 {
				val := 100.0 - float64(attr.Value)
				wearScsi = &val
			}
			switch attr.ID {
			case 177:
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear177 = &val
				}
			case 202:
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear202 = &val
				}
			case 230:
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear230 = &val
				}
			case 231:
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear231 = &val
				}
			case 232:
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear232 = &val
				}
			case 233:
				if attr.Value > 0 {
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
	var kind smart.SelfTestKind
	switch testType {
	case "short":
		kind = smart.SelfTestKindShort
	case "long", "extended":
		kind = smart.SelfTestKindExtended
	case "conveyance":
		kind = smart.SelfTestKindConveyance
	case "selective":
		kind = smart.SelfTestKindSelective
	case "offline":
		kind = smart.SelfTestKindOffline
	case "default":
		kind = smart.SelfTestKindDefault
	case "short_captive":
		kind = smart.SelfTestKindShortCaptive
	case "extended_captive":
		kind = smart.SelfTestKindExtendedCaptive
	case "abort":
		return smart.AbortSelfTest(disk.Name)
	default:
		return fmt.Errorf("unknown self-test type: %s", testType)
	}
	return smart.StartSelfTest(disk.Name, kind)
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
	dev := "/dev/" + disk.Name
	return smart.AbortSelfTest(dev)
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
