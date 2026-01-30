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
	"math"
	"regexp"
	"strconv"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func getNVMeControlData(serial string) (diskServiceInterfaces.SMARTNvme, error) {
	output, err := utils.RunCommand("nvmecontrol", "devlist")
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
		output, err := utils.RunCommand("nvmecontrol", "identify", fmt.Sprintf("/dev/%s", nvmeDevice))
		if err != nil {
			return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("failed to get NVMe device info: %v", err)
		}

		serialRegex := regexp.MustCompile(`Serial Number:\s*(\S+)`)
		if matches := serialRegex.FindStringSubmatch(output); matches != nil {
			if matches[1] == serial {
				output, err := utils.RunCommand("nvmecontrol", "logpage", "-p", "2", nvmeDevice)
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
			smart.CriticalWarningState.Temperature = getInt(valStr)
		case inCriticalSection && key == "device reliability":
			smart.CriticalWarningState.DeviceReliability = getInt(valStr)
		case inCriticalSection && key == "read only":
			smart.CriticalWarningState.ReadOnly = getInt(valStr)
		case inCriticalSection && key == "volatile memory backup":
			smart.CriticalWarningState.VolatileMemoryBackup = getInt(valStr)

		case key == "temperature":
			if strings.Contains(valStr, "K") {
				smart.Temperature = getInt(valStr)
			}

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

	data.Passed = true
	data.PowerOnHours = info.PowerOnHours
	data.PowerCycleCount = info.PowerCycleCount
	data.Temperature = info.Temperature

	if len(info.Attributes) > 0 {
		data.Attributes = make([]diskServiceInterfaces.ATASmartAttribute, len(info.Attributes))

		for i, attr := range info.Attributes {
			data.Attributes[i] = diskServiceInterfaces.ATASmartAttribute{
				ID:        int(attr.ID),
				Name:      attr.Name,
				Value:     attr.Value,
				Worst:     attr.Worst,
				Thresh:    attr.Threshold,
				RawValue:  int64(attr.RawValue),
				RawString: attr.TextValue,
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
		const (
			MaxLifespanHours = 50000.0
			SectorPenalty    = 10.0
		)

		var wear177, wear232, wear233 *float64

		powerOnHours := float64(data.PowerOnHours)
		reallocatedSectors := 0

		for _, attr := range data.Attributes {
			switch attr.ID {
			case 5: // Reallocated Sector Count
				reallocatedSectors = int(attr.RawValue)

			case 177: // Wear Leveling Count
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear177 = &val
				}

			case 233: // Media Wearout Indicator
				if attr.Value > 0 {
					val := 100.0 - float64(attr.Value)
					wear233 = &val
				}

			case 232: // Available Reserved Space / Endurance Remaining
				// WD/Sandisk often store % Remaining in RawValue
				if attr.RawValue <= 100 {
					val := 100.0 - float64(attr.RawValue)
					wear232 = &val
				}
			}
		}

		// Priority 1: ID 232 (Endurance Remaining) - Usually the most explicit "Gas Gauge"
		if wear232 != nil {
			return *wear232, nil
		}

		// Priority 2: ID 233 (Media Wearout) - Common Intel Standard apparently?
		if wear233 != nil {
			return *wear233, nil
		}

		// Priority 3: ID 177 (Wear Leveling) - Common for generic SATA SSDs
		if wear177 != nil {
			return *wear177, nil
		}

		// Priority 4: Fallback Heuristic (Old HDDs or cheap SSDs)
		wearoutAge := (powerOnHours / MaxLifespanHours) * 100
		wearoutSectors := float64(reallocatedSectors) * SectorPenalty
		totalWearout := wearoutAge + wearoutSectors
		totalWearout = math.Min(math.Max(totalWearout, 0), 100)

		return totalWearout, nil
	}

	return 0, errors.New("unsupported SMART data type")
}
