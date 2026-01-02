// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func getSmartCtlData(device string) (diskServiceInterfaces.SmartData, error) {
	output, err := utils.RunCommandAllowExitCode(
		"smartctl",
		[]int{0, 32, 64, 128},
		"-A", "-H", "-j", "/dev/"+device,
	)

	if err != nil {
		return diskServiceInterfaces.SmartData{}, err
	}

	var parsed diskServiceInterfaces.SmartData
	err = json.Unmarshal([]byte(output), &parsed)

	if err != nil {
		return diskServiceInterfaces.SmartData{}, err
	}

	return parsed, nil
}

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
				parsedSMART := parseNVMeSMART(output)
				parsedSMART.Device = nvmeDevice

				return parsedSMART, nil
			}
		}
	}

	return diskServiceInterfaces.SMARTNvme{}, fmt.Errorf("NVMe device with serial %s not found", serial)
}

func parseNVMeSMART(output string) diskServiceInterfaces.SMARTNvme {
	var smart diskServiceInterfaces.SMARTNvme

	fields := map[string]*int{
		`Temperature:\s+(\d+)\s+K`:                  &smart.Temperature,
		`Available spare threshold:\s+(\d+)`:        &smart.AvailableSpareThreshold,
		`Percentage used:\s+(\d+)`:                  &smart.PercentageUsed,
		`Data units \(512,000 byte\) read:\s+(\d+)`: &smart.DataUnitsRead,
		`Data units written:\s+(\d+)`:               &smart.DataUnitsWritten,
		`Host read commands:\s+(\d+)`:               &smart.HostReadCommands,
		`Host write commands:\s+(\d+)`:              &smart.HostWriteCommands,
		`Controller busy time \(minutes\):\s+(\d+)`: &smart.ControllerBusyTime,
		`Power cycles:\s+(\d+)`:                     &smart.PowerCycles,
		`Power on hours:\s+(\d+)`:                   &smart.PowerOnHours,
		`Unsafe shutdowns:\s+(\d+)`:                 &smart.UnsafeShutdowns,
		`Media errors:\s+(\d+)`:                     &smart.MediaErrors,
		`No\. error info log entries:\s+(\d+)`:      &smart.ErrorInfoLogEntries,
		`Warning Temp Composite Time:\s+(\d+)`:      &smart.WarningCompositeTempTime,
		`Error Temp Composite Time:\s+(\d+)`:        &smart.ErrorCompositeTempTime,
		`Temperature 1 Transition Count:\s+(\d+)`:   &smart.Temperature1TransitionCnt,
		`Temperature 2 Transition Count:\s+(\d+)`:   &smart.Temperature2TransitionCnt,
		`Total Time For Temperature 1:\s+(\d+)`:     &smart.TotalTimeForTemperature1,
		`Total Time For Temperature 2:\s+(\d+)`:     &smart.TotalTimeForTemperature2,
	}

	for pattern, field := range fields {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(output)
		if len(match) > 1 {
			value, err := strconv.Atoi(strings.TrimSpace(match[1]))
			if err == nil {
				*field = value
			}
		}
	}

	re := regexp.MustCompile(`Critical Warning State:\s+(0x[0-9A-Fa-f]+)`)
	match := re.FindStringSubmatch(output)
	if len(match) > 1 {
		smart.CriticalWarning = match[1]
	}

	criticalWarningFields := map[string]*int{
		`Critical Warning State:\s+\S+\n\s+Available spare:\s+(\d+)`:        &smart.CriticalWarningState.AvailableSpare,
		`Critical Warning State:\s+\S+\n\s+Temperature:\s+(\d+)`:            &smart.CriticalWarningState.Temperature,
		`Critical Warning State:\s+\S+\n\s+Device reliability:\s+(\d+)`:     &smart.CriticalWarningState.DeviceReliability,
		`Critical Warning State:\s+\S+\n\s+Read only:\s+(\d+)`:              &smart.CriticalWarningState.ReadOnly,
		`Critical Warning State:\s+\S+\n\s+Volatile memory backup:\s+(\d+)`: &smart.CriticalWarningState.VolatileMemoryBackup,
	}

	for pattern, field := range criticalWarningFields {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(output)
		if len(match) > 1 {
			value, err := strconv.Atoi(strings.TrimSpace(match[1]))
			if err == nil {
				*field = value
			}
		}
	}

	re = regexp.MustCompile(`Temperature:\s+\d+\s+K.*\nAvailable spare:\s+(\d+)`)
	match = re.FindStringSubmatch(output)
	if len(match) > 1 {
		value, err := strconv.Atoi(strings.TrimSpace(match[1]))
		if err == nil {
			smart.AvailableSpare = value
		}
	}

	return smart
}

func (s *Service) GetSmartData(disk diskServiceInterfaces.DiskInfo) (interface{}, error) {
	if disk.Type == "HDD" {
		return getSmartCtlData(disk.Name)
	} else if disk.Type == "SSD" {
		return getSmartCtlData(disk.Name)
	} else if disk.Type == "NVMe" {
		return getNVMeControlData(disk.Serial)
	}

	return nil, nil
}

func (s *Service) GetWearOut(smartData any) (float64, error) {
	if smartData == nil {
		return 0, errors.New("no SMART data available")
	}

	var smartType string

	switch smartData.(type) {
	case diskServiceInterfaces.SMARTNvme:
		smartType = "nvme"
	case diskServiceInterfaces.SmartData:
		smartType = "smartctl"
	default:
		return 0, errors.New("unsupported SMART data type")
	}

	if smartType == "smartctl" {
		data := smartData.(diskServiceInterfaces.SmartData)

		const (
			MaxLifespanHours = 100000.0
			ErrorThreshold   = 1e10
			ShockThreshold   = 5000.0
			MaxWrites        = 3e13
			SectorPenalty    = 5.0
		)

		powerOnHours := float64(data.PowerOnTime.Hours)
		reallocatedSectors := 0
		seekErrors := 0.0
		readErrors := 0.0
		gSenseErrors := float64(data.PowerCycleCount)
		totalWrites := 0.0

		if data.ATASmartAttributes != nil {
			for _, attr := range data.ATASmartAttributes.Table {
				switch attr.ID {
				case 5:
					reallocatedSectors = int(attr.Raw.Value)
				case 7:
					seekErrors = float64(attr.Raw.Value)
				case 1:
					readErrors = float64(attr.Raw.Value)
				case 241:
					totalWrites = float64(attr.Raw.Value)
				}
			}
		}

		// Wearout percentage formula (Best-Case Scenario):
		//
		// Wearout% = (Power-On Hours / Max Lifespan Hours) * 100
		//          + (Reallocated Sectors * Sector Penalty)
		//          + MIN((Seek Errors + Read Errors) / Error Threshold * 10, 10)
		//          + MIN((G-Sense Errors / Shock Threshold) * 5, 5)
		//          + MIN((Total LBAs Written / Max Writes) * 5, 5)
		//
		// Where:
		// - Max Lifespan Hours = 100,000 (expected HDD lifespan in best case)
		// - Sector Penalty = 5% per reallocated sector
		// - Error Threshold = 10 billion (max seek + read errors before considering failure risk)
		// - Shock Threshold = 5,000 (number of shocks before considering wear impact)
		// - Max Writes = 30 trillion LBAs (maximum expected HDD writes)
		//
		// The MIN() function ensures that individual wear contributions do not exceed predefined caps.

		wearoutAge := (powerOnHours / MaxLifespanHours) * 100
		wearoutSectors := float64(reallocatedSectors) * SectorPenalty
		wearoutMechanical := math.Min((seekErrors+readErrors)/ErrorThreshold*10, 10)
		wearoutShock := math.Min((gSenseErrors/ShockThreshold)*5, 5)
		wearoutWrites := math.Min((totalWrites/MaxWrites)*5, 5)

		totalWearout := wearoutAge + wearoutSectors + wearoutMechanical + wearoutShock + wearoutWrites
		totalWearout = math.Min(math.Max(totalWearout, 0), 100)

		return totalWearout, nil
	}

	if smartType == "nvme" {
		data := smartData.(diskServiceInterfaces.SMARTNvme)
		return float64(data.PercentageUsed), nil
	}

	return 0, errors.New("unable to determine wearout")
}
