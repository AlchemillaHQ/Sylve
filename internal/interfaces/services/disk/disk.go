// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskServiceInterfaces

import "context"

type Partition struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Usage string `json:"usage"`
	Size  uint64 `json:"size"`
}

type Disk struct {
	UUID       string      `json:"uuid"`
	Device     string      `json:"device"`
	Type       string      `json:"type"`
	Usage      string      `json:"usage"`
	Size       uint64      `json:"size"`
	Model      string      `json:"model"`
	Serial     string      `json:"serial"`
	GPT        bool        `json:"gpt"`
	SmartData  any         `json:"smartData"`
	WearOut    string      `json:"wearOut"`
	Partitions []Partition `json:"partitions"`
}

type DeviceInfo struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

type SmartData struct {
	Device          DeviceInfo `json:"device"`
	Passed          bool       `json:"passed"`
	PowerOnHours    int        `json:"power_on_hours"`
	PowerCycleCount int        `json:"power_cycle_count"`
	Temperature     int        `json:"temperature"`

	Attributes []ATASmartAttribute `json:"attributes"`
}

type ATASmartAttribute struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Value  int    `json:"value"`
	Worst  int    `json:"worst"`
	Thresh int    `json:"thresh"`

	RawValue  int64  `json:"raw_value"`
	RawString string `json:"raw_string"`
}

type NvmeCriticalWarningState struct {
	AvailableSpare       int `json:"availableSpare"`
	Temperature          int `json:"temperature"`
	DeviceReliability    int `json:"deviceReliability"`
	ReadOnly             int `json:"readOnly"`
	VolatileMemoryBackup int `json:"volatileMemoryBackup"`
}

type SMARTNvme struct {
	Device          DeviceInfo `json:"device"`
	Passed          bool       `json:"passed"`
	PowerOnHours    int        `json:"power_on_hours"`
	PowerCycleCount int        `json:"power_cycle_count"`
	Temperature     int        `json:"temperature"`

	CriticalWarning           string                   `json:"criticalWarning"`
	CriticalWarningState      NvmeCriticalWarningState `json:"criticalWarningState"`
	AvailableSpare            int                      `json:"availableSpare"`
	AvailableSpareThreshold   int                      `json:"availableSpareThreshold"`
	PercentageUsed            int                      `json:"percentageUsed"`
	DataUnitsRead             int                      `json:"dataUnitsRead"`
	DataUnitsWritten          int                      `json:"dataUnitsWritten"`
	HostReadCommands          int                      `json:"hostReadCommands"`
	HostWriteCommands         int                      `json:"hostWriteCommands"`
	ControllerBusyTime        int                      `json:"controllerBusyTime"`
	UnsafeShutdowns           int                      `json:"unsafeShutdowns"`
	MediaErrors               int                      `json:"mediaErrors"`
	ErrorInfoLogEntries       int                      `json:"errorInfoLogEntries"`
	WarningCompositeTempTime  int                      `json:"warningCompositeTempTime"`
	ErrorCompositeTempTime    int                      `json:"errorCompositeTempTime"`
	Temperature1TransitionCnt int                      `json:"temperature1TransitionCnt"`
	Temperature2TransitionCnt int                      `json:"temperature2TransitionCnt"`
	TotalTimeForTemperature1  int                      `json:"totalTimeForTemperature1"`
	TotalTimeForTemperature2  int                      `json:"totalTimeForTemperature2"`
}

type DiskServiceInterface interface {
	GetDiskDevices(ctx context.Context) ([]Disk, error)
	GetSmartData(disk DiskInfo) (any, error)
	GetWearOut(disk any) (float64, error)
	GetDiskSize(device string) (uint64, error)
	DestroyPartitionTable(device string) error
	IsDiskGPT(device string) bool
}
