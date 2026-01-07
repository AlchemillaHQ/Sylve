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

type NvmeCriticalWarningState struct {
	AvailableSpare       int `json:"availableSpare"`
	Temperature          int `json:"temperature"`
	DeviceReliability    int `json:"deviceReliability"`
	ReadOnly             int `json:"readOnly"`
	VolatileMemoryBackup int `json:"volatileMemoryBackup"`
}

type SMARTNvme struct {
	JSONFormatVersion         []int                    `json:"json_format_version"`
	Smartctl                  SmartctlInfo             `json:"smartctl"`
	LocalTime                 LocalTime                `json:"local_time"`
	Device                    DeviceInfo               `json:"device"`
	SmartStatus               SmartStatus              `json:"smart_status"`
	PowerOnTime               PowerOnTime              `json:"power_on_time"`
	PowerCycleCount           int                      `json:"power_cycle_count"`
	Temperature               Temperature              `json:"temperature"`
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

type SmartData struct {
	JSONFormatVersion []int        `json:"json_format_version"`
	Smartctl          SmartctlInfo `json:"smartctl"`
	LocalTime         LocalTime    `json:"local_time"`
	Device            DeviceInfo   `json:"device"`
	SmartStatus       SmartStatus  `json:"smart_status"`
	PowerOnTime       PowerOnTime  `json:"power_on_time"`
	PowerCycleCount   int          `json:"power_cycle_count"`
	Temperature       Temperature  `json:"temperature"`

	ATASmartAttributes  *ATASmartAttributes  `json:"ata_smart_attributes,omitempty"`
	SCSISmartAttributes *SCSISmartAttributes `json:"scsi_smart_attributes,omitempty"`
}

type SmartctlInfo struct {
	Version              []int             `json:"version"`
	PreRelease           bool              `json:"pre_release"`
	SVNRevision          string            `json:"svn_revision"`
	PlatformInfo         string            `json:"platform_info"`
	BuildInfo            string            `json:"build_info"`
	Argv                 []string          `json:"argv"`
	DriveDatabaseVersion DriveDatabaseInfo `json:"drive_database_version"`
	ExitStatus           int               `json:"exit_status"`
}

type DriveDatabaseInfo struct {
	String string `json:"string"`
}

type LocalTime struct {
	TimeT   int    `json:"time_t"`
	Asctime string `json:"asctime"`
}

type DeviceInfo struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

type SmartStatus struct {
	Passed bool `json:"passed"`
}

type PowerOnTime struct {
	Hours int `json:"hours"`
}

type Temperature struct {
	Current int `json:"current"`
}

type ATASmartAttributes struct {
	Revision int                 `json:"revision"`
	Table    []ATASmartAttribute `json:"table"`
}

type ATASmartAttribute struct {
	ID         int            `json:"id"`
	Name       string         `json:"name"`
	Value      int            `json:"value"`
	Worst      int            `json:"worst"`
	Thresh     int            `json:"thresh"`
	WhenFailed string         `json:"when_failed"`
	Flags      AttributeFlags `json:"flags"`
	Raw        RawData        `json:"raw"`
}

type AttributeFlags struct {
	Value         int    `json:"value"`
	String        string `json:"string"`
	Prefailure    bool   `json:"prefailure"`
	UpdatedOnline bool   `json:"updated_online"`
	Performance   bool   `json:"performance"`
	ErrorRate     bool   `json:"error_rate"`
	EventCount    bool   `json:"event_count"`
	AutoKeep      bool   `json:"auto_keep"`
}

type RawData struct {
	Value  int64  `json:"value"`
	String string `json:"string"`
}

type SCSISmartAttributes struct {
	Temperature int `json:"scsi_temperature,omitempty"`
}

type DiskServiceInterface interface {
	GetDiskDevices(ctx context.Context) ([]Disk, error)
	GetSmartData(disk DiskInfo) (any, error)
	GetWearOut(disk any) (float64, error)
	GetDiskSize(device string) (uint64, error)
	DestroyPartitionTable(device string) error
	IsDiskGPT(device string) bool
}
