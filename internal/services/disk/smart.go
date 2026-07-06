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
	"regexp"
	"strconv"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func getNVMeControlData(serial string) (diskServiceInterfaces.SMARTNvme, error) {
	output, err := utils.RunCommand("/sbin/nvmecontrol", "devlist")
	if err != nil {
		return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("failed to get NVMe device list: %v", err)
	}

	var nvmeDevices []string
	lines := strings.Split(output, "\n")
	nvmeRegex := regexp.MustCompile(`^(nvme\d+):`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if matches := nvmeRegex.FindStringSubmatch(line); matches != nil {
			nvmeDevices = append(nvmeDevices, matches[1])
		}
	}

	for _, nvmeDevice := range nvmeDevices {
		output, err := utils.RunCommand("/sbin/nvmecontrol", "identify", fmt.Sprintf("/dev/%s", nvmeDevice))
		if err != nil {
			return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("failed to get NVMe device info: %v", err)
		}

		serialRegex := regexp.MustCompile(`Serial Number:\s*(\S+)`)
		if matches := serialRegex.FindStringSubmatch(output); matches != nil {
			if matches[1] == serial {
				output, err := utils.RunCommand("/sbin/nvmecontrol", "logpage", "-p", "2", nvmeDevice)
				if err != nil {
					return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("failed to get NVMe device logpage: %v", err)
				}

				output = utils.RemoveEmptyLines(output)
				parsedSMART := parseNVMeSMART(output, nvmeDevice)

				return parsedSMART, nil
			}
		}
	}

	return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("NVMe device with serial %s not found", serial)
}

func kelvinToCelsius(v int) int {
	if v > 150 {
		return v - 273
	}
	return v
}

func parseNVMeSMART(output string, device string) diskServiceInterfaces.SMARTNvme {
	var smart diskServiceInterfaces.SMARTNvme

	smart.Device = diskServiceInterfaces.DeviceInfo{
		Name:     device,
		InfoName: device,
		Type:     "nvme",
		Protocol: "NVMe",
	}
	smart.Passed = true

	lines := strings.Split(output, "\n")
	inCriticalSection := false

	getInt := func(s string) int {
		fields := strings.Fields(s)
		if len(fields) > 0 {
			val, err := strconv.Atoi(fields[0])
			if err == nil {
				return val
			}
		}
		return 0
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "SMART/Health") || strings.HasPrefix(line, "===") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		valStr := strings.TrimSpace(parts[1])

		if key == "critical warning state" {
			smart.CriticalWarning = valStr
			inCriticalSection = true
			continue
		}

		if strings.Contains(key, "percentage used") || strings.Contains(key, "data units") {
			inCriticalSection = false
		}

		switch {
		case inCriticalSection && key == "available spare":
			smart.CriticalWarningState.AvailableSpare = getInt(valStr)
		case inCriticalSection && key == "temperature":
			smart.CriticalWarningState.Temperature = kelvinToCelsius(getInt(valStr))
		case inCriticalSection && key == "device reliability":
			smart.CriticalWarningState.DeviceReliability = getInt(valStr)
		case inCriticalSection && key == "read only":
			smart.CriticalWarningState.ReadOnly = getInt(valStr)
		case inCriticalSection && key == "volatile memory backup":
			smart.CriticalWarningState.VolatileMemoryBackup = getInt(valStr)
			inCriticalSection = false

		case key == "temperature":
			smart.Temperature = kelvinToCelsius(getInt(valStr))

		case key == "available spare":
			smart.AvailableSpare = getInt(valStr)

		case key == "available spare threshold":
			smart.AvailableSpareThreshold = getInt(valStr)

		case key == "percentage used":
			smart.PercentageUsed = getInt(valStr)

		case strings.Contains(key, "data units") && strings.Contains(key, "read"):
			smart.DataUnitsRead = getInt(valStr)

		case strings.Contains(key, "data units") && strings.Contains(key, "written"):
			smart.DataUnitsWritten = getInt(valStr)

		case key == "host read commands":
			smart.HostReadCommands = getInt(valStr)

		case key == "host write commands":
			smart.HostWriteCommands = getInt(valStr)

		case strings.Contains(key, "controller busy time"):
			smart.ControllerBusyTime = getInt(valStr)

		case key == "power cycles":
			smart.PowerCycleCount = getInt(valStr)

		case key == "power on hours":
			smart.PowerOnHours = getInt(valStr)

		case key == "unsafe shutdowns":
			smart.UnsafeShutdowns = getInt(valStr)

		case key == "media errors":
			smart.MediaErrors = getInt(valStr)

		case key == "no. error info log entries":
			smart.ErrorInfoLogEntries = getInt(valStr)

		case key == "warning temp composite time":
			smart.WarningCompositeTempTime = getInt(valStr)

		case key == "error temp composite time":
			smart.ErrorCompositeTempTime = getInt(valStr)

		case key == "temperature 1 transition count":
			smart.Temperature1TransitionCnt = getInt(valStr)

		case key == "temperature 2 transition count":
			smart.Temperature2TransitionCnt = getInt(valStr)

		case key == "total time for temperature 1":
			smart.TotalTimeForTemperature1 = getInt(valStr)

		case key == "total time for temperature 2":
			smart.TotalTimeForTemperature2 = getInt(valStr)
		}
	}

	return smart
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
			data.SCSISelfTestResults[i] = diskServiceInterfaces.DiskSCSISelfTestEntry{
				Type:          e.Type,
				Status:        e.Status,
				LifetimeHours: e.LifetimeHours,
				LBA:           e.LBA,
				SenseKey:      e.SenseKey,
				ASC:           e.ASC,
				ASCQ:          e.ASCQ,
			}
		}
	}

	if len(info.Attributes) > 0 {
		data.Attributes = make([]diskServiceInterfaces.ATASmartAttribute, len(info.Attributes))

		for i, attr := range info.Attributes {
			state := smart.AtaAttrState(attr.Value, attr.Worst, attr.Threshold)
			whenFailed := ""
			switch state {
			case smart.AttrStateFailedNow:
				whenFailed = "FAILING_NOW"
			case smart.AttrStateFailedPast:
				whenFailed = "In_the_past"
			}

			data.Attributes[i] = diskServiceInterfaces.ATASmartAttribute{
				Page:      int(attr.Page),
				ID:        int(attr.ID),
				Name:      strings.ReplaceAll(attr.Name, "_", " "),
				Value:     attr.Value,
				Worst:     attr.Worst,
				Thresh:    attr.Threshold,
				RawValue:  int64(attr.RawValue),
				RawString: attr.TextValue,
				State:     state,
				WhenFailed: whenFailed,
				PreFailure:     attr.Flags.PreFailure,
				Online:         attr.Flags.Online,
				Performance:    attr.Flags.Performance,
				ErrorRate:      attr.Flags.ErrorRate,
				EventCount:     attr.Flags.EventCount,
				AutoKeep:       attr.Flags.SelfPreserving,
			}
		}
	}

	return data
}

func (s *Service) GetSmartData(disk diskServiceInterfaces.DiskInfo) (interface{}, error) {
	if disk.Type == "NVMe" {
		return getNVMeControlData(disk.Serial)
	}

	smartInfo, err := smart.Read(disk.Name)
	if err != nil {
		return nil, err
	}

	return mapLibSmartToInterface(smartInfo), nil
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

func (s *Service) RunSelfTest(disk diskServiceInterfaces.DiskInfo, testType string) error {
	var tt uint8
	switch testType {
	case "short":
		tt = 0x01
	case "long", "extended":
		tt = 0x02
	case "conveyance":
		tt = 0x03
	case "abort":
		tt = 0x7F
	default:
		return fmt.Errorf("unknown self-test type: %s", testType)
	}

	if disk.Type == "NVMe" {
		switch tt {
		case 0x01:
			tt = 0x01
		case 0x02:
			tt = 0x02
		case 0x7F:
			tt = 0x0F
		default:
			tt = 0x01
		}
		if testType == "conveyance" {
			return fmt.Errorf("conveyance self-test not supported for NVMe")
		}
	}

	return smart.SelfTest(disk.Name, tt)
}

func (s *Service) GetSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	log, err := smart.ReadSelfTestLog(disk.Name)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskSelfTestLog{
		InProgress:    log.InProgress,
		ProgressPct:   log.ProgressPct,
		ChecksumValid: log.ChecksumValid,
		Entries:       make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
	}

	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskSelfTestEntry{
			Type:          e.Type,
			Status:        e.Status,
			RemainingPct:  e.RemainingPct,
			LifetimeHours: e.LifetimeHours,
			LBA:           e.LBA,
			NSID:          e.NSID,
		}
	}

	return result, nil
}

func (s *Service) GetErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	log, err := smart.ReadErrorLog(disk.Name)
	if err != nil {
		return nil, err
	}

	result := &diskServiceInterfaces.DiskErrorLog{
		ChecksumValid: log.ChecksumValid,
		Entries: make([]diskServiceInterfaces.DiskErrorEntry, len(log.Entries)),
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
		Entries: make([]diskServiceInterfaces.DiskErrorEntry, len(log.Entries)),
	}
	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskErrorEntry{
			ErrorData:    e.ErrorData,
			ExtendedData: e.ExtendedData,
			LifetimeHours: e.LifetimeHours,
			LBA:          e.LBA,
			Status:       e.Status,
			Error:        e.Error,
			SectorCount:  e.SectorCount,
			Device:       e.Device,
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
		Entries:    make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
		InProgress: log.InProgress,
	}
	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskSelfTestEntry{
			Type:          e.Type,
			Status:        e.Status,
			RemainingPct:  e.RemainingPct,
			LifetimeHours: e.LifetimeHours,
			LBA:           e.LBA,
			NSID:          e.NSID,
		}
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
		Entries:    make([]diskServiceInterfaces.DiskSelfTestEntry, len(log.Entries)),
		InProgress: log.InProgress,
	}
	for i, e := range log.Entries {
		result.Entries[i] = diskServiceInterfaces.DiskSelfTestEntry{
			Type:          e.Type,
			Status:        e.Status,
			RemainingPct:  e.RemainingPct,
			LifetimeHours: e.LifetimeHours,
			LBA:           e.LBA,
			NSID:          e.NSID,
		}
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
