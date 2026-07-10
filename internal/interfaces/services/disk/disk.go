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
	UUID        string           `json:"uuid"`
	Device      string           `json:"device"`
	Type        string           `json:"type"`
	Usage       string           `json:"usage"`
	Size        uint64           `json:"size"`
	Model       string           `json:"model"`
	Serial      string           `json:"serial"`
	GPT         bool             `json:"gpt"`
	SmartData   any              `json:"smartData"`
	WearOut     string           `json:"wearOut"`
	SelfTestLog *DiskSelfTestLog `json:"selfTestLog,omitempty"`
	Partitions  []Partition      `json:"partitions"`
}

type DeviceInfo struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

type DiskSelfTestStatus struct {
	Status       string `json:"status"`
	RemainingPct int    `json:"remaining_pct"`
}

type SmartData struct {
	Device              DeviceInfo              `json:"device"`
	Passed              bool                    `json:"passed"`
	ChecksumValid       bool                    `json:"checksum_valid"`
	PowerOnHours        int                     `json:"power_on_hours"`
	PowerCycleCount     int                     `json:"power_cycle_count"`
	Temperature         int                     `json:"temperature"`
	SelfTestStatus      DiskSelfTestStatus      `json:"self_test_status"`
	SmartCapability     uint64                  `json:"smart_capability"`
	SCSISelfTestResults []DiskSCSISelfTestEntry `json:"scsi_self_test_results,omitempty"`

	Attributes []ATASmartAttribute `json:"attributes"`
}

type DiskSCSISelfTestEntry struct {
	Type          string `json:"type"`
	Status        string `json:"status"`
	LifetimeHours uint64 `json:"lifetime_hours"`
	LBA           uint64 `json:"lba"`
	SenseKey      uint8  `json:"sense_key"`
	ASC           uint8  `json:"asc"`
	ASCQ          uint8  `json:"ascq"`
}

type ATASmartAttribute struct {
	Page   int    `json:"page"`
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Value  int    `json:"value"`
	Worst  int    `json:"worst"`
	Thresh int    `json:"thresh"`

	RawValue  int64  `json:"raw_value"`
	RawString string `json:"raw_string"`

	State       int    `json:"state"`
	WhenFailed  string `json:"when_failed"`
	PreFailure  bool   `json:"pre_failure"`
	Online      bool   `json:"online"`
	Performance bool   `json:"performance"`
	ErrorRate   bool   `json:"error_rate"`
	EventCount  bool   `json:"event_count"`
	AutoKeep    bool   `json:"auto_keep"`
}

type NvmeCriticalWarningState struct {
	AvailableSpare       int `json:"availableSpare"`
	Temperature          int `json:"temperature"`
	DeviceReliability    int `json:"deviceReliability"`
	ReadOnly             int `json:"readOnly"`
	VolatileMemoryBackup int `json:"volatileMemoryBackup"`
}

type SMARTNvme struct {
	Device               DeviceInfo `json:"device"`
	Passed               bool       `json:"passed"`
	PowerOnHours         int        `json:"power_on_hours"`
	PowerOnHoursExact    string     `json:"power_on_hours_exact"`
	PowerCycleCount      int        `json:"power_cycle_count"`
	PowerCycleCountExact string     `json:"power_cycle_count_exact"`
	Temperature          int        `json:"temperature"`

	CriticalWarning           string                   `json:"criticalWarning"`
	CriticalWarningState      NvmeCriticalWarningState `json:"criticalWarningState"`
	AvailableSpare            int                      `json:"availableSpare"`
	AvailableSpareThreshold   int                      `json:"availableSpareThreshold"`
	PercentageUsed            int                      `json:"percentageUsed"`
	DataUnitsRead             int                      `json:"dataUnitsRead"`
	DataUnitsReadExact        string                   `json:"dataUnitsReadExact"`
	DataUnitsWritten          int                      `json:"dataUnitsWritten"`
	DataUnitsWrittenExact     string                   `json:"dataUnitsWrittenExact"`
	HostReadCommands          int                      `json:"hostReadCommands"`
	HostReadCommandsExact     string                   `json:"hostReadCommandsExact"`
	HostWriteCommands         int                      `json:"hostWriteCommands"`
	HostWriteCommandsExact    string                   `json:"hostWriteCommandsExact"`
	ControllerBusyTime        int                      `json:"controllerBusyTime"`
	ControllerBusyTimeExact   string                   `json:"controllerBusyTimeExact"`
	UnsafeShutdowns           int                      `json:"unsafeShutdowns"`
	UnsafeShutdownsExact      string                   `json:"unsafeShutdownsExact"`
	MediaErrors               int                      `json:"mediaErrors"`
	MediaErrorsExact          string                   `json:"mediaErrorsExact"`
	ErrorInfoLogEntries       int                      `json:"errorInfoLogEntries"`
	ErrorInfoLogEntriesExact  string                   `json:"errorInfoLogEntriesExact"`
	WarningCompositeTempTime  int                      `json:"warningCompositeTempTime"`
	ErrorCompositeTempTime    int                      `json:"errorCompositeTempTime"`
	Temperature1TransitionCnt int                      `json:"temperature1TransitionCnt"`
	Temperature2TransitionCnt int                      `json:"temperature2TransitionCnt"`
	TotalTimeForTemperature1  int                      `json:"totalTimeForTemperature1"`
	TotalTimeForTemperature2  int                      `json:"totalTimeForTemperature2"`
}

type DiskServiceInterface interface {
	GetDiskDevices(ctx context.Context) ([]Disk, error)
	GetSmartData(disk DiskInfo) (any, *DiskSelfTestLog, error)
	GetWearOut(disk any) (float64, error)
	GetDiskSize(device string) (uint64, error)
	DestroyPartitionTable(device string) error
	IsDiskGPT(device string) bool
	RunSelfTest(disk DiskInfo, testType string) error
	AbortSelfTest(disk DiskInfo) error
	GetSelfTestLog(disk DiskInfo) (*DiskSelfTestLog, error)
	GetExtendedSelfTestLog(disk DiskInfo) (*DiskSelfTestLog, error)
	GetErrorLog(disk DiskInfo) (*DiskErrorLog, error)
	GetExtendedErrorLog(disk DiskInfo) (*DiskErrorLog, error)
	GetNVMEErrorLog(disk DiskInfo) (*DiskNVMEErrorLog, error)
	GetSCTStatus(disk DiskInfo) (*DiskSCTStatus, error)
	GetSCTTempHistory(disk DiskInfo) (*DiskSCTTempHistory, error)
	GetLogDirectory(disk DiskInfo) ([]uint8, error)
	GetDeviceStatistics(disk DiskInfo) ([]DiskAttribute, error)
	GetSelectiveSelfTestLog(disk DiskInfo) (*DiskSelfTestLog, error)
	SetSCTFeatureControl(disk DiskInfo, featureCode uint16, state uint16, persistent bool) error
	SetSCTErrorRecoveryControl(disk DiskInfo, read bool, timeLimit uint16) error
}

type DiskSCTStatus struct {
	FormatVersion     uint16 `json:"format_version"`
	SCTVersion        uint16 `json:"sct_version"`
	SCTSpec           uint16 `json:"sct_spec"`
	StatusFlags       uint32 `json:"status_flags"`
	DeviceState       uint8  `json:"device_state"`
	ExtStatusCode     uint16 `json:"ext_status_code"`
	ActionCode        uint16 `json:"action_code"`
	FunctionCode      uint16 `json:"function_code"`
	LBACurrent        uint64 `json:"lba_current"`
	CurrentTemp       int8   `json:"current_temp"`
	MinTempCycle      int8   `json:"min_temp_cycle"`
	MaxTempCycle      int8   `json:"max_temp_cycle"`
	LifetimeMinTemp   int8   `json:"lifetime_min_temp"`
	LifetimeMaxTemp   int8   `json:"lifetime_max_temp"`
	MaxOpLimit        int8   `json:"max_op_limit"`
	OverTempCount     uint32 `json:"over_temp_count"`
	UnderTempCount    uint32 `json:"under_temp_count"`
	SmartStatusPassed bool   `json:"smart_status_passed"`
	MinERCTime        uint16 `json:"min_erc_time"`
}

type DiskSCTTempSample struct {
	Temperature int8 `json:"temperature"`
}

type DiskSCTTempHistory struct {
	SamplingPeriod uint16              `json:"sampling_period"`
	Interval       uint16              `json:"interval"`
	MaxOpLimit     int8                `json:"max_op_limit"`
	OverLimit      int8                `json:"over_limit"`
	MinOpLimit     int8                `json:"min_op_limit"`
	UnderLimit     int8                `json:"under_limit"`
	CBSize         uint16              `json:"cb_size"`
	CBIndex        uint16              `json:"cb_index"`
	Samples        []DiskSCTTempSample `json:"samples"`
}

type DiskNVMEErrorEntry struct {
	ErrorCount  uint64 `json:"error_count"`
	SQID        uint16 `json:"sqid"`
	CommandID   uint16 `json:"command_id"`
	StatusField uint16 `json:"status_field"`
	ParamError  uint16 `json:"param_error"`
	LBA         uint64 `json:"lba"`
	NamespaceID uint32 `json:"namespace_id"`
}

type DiskNVMEErrorLog struct {
	Entries []DiskNVMEErrorEntry `json:"entries"`
}

type DiskErrorEntry struct {
	ErrorData     uint16 `json:"error_data"`
	ExtendedData  uint16 `json:"extended_data"`
	LifetimeHours uint32 `json:"lifetime_hours"`
	LBA           uint64 `json:"lba"`
	Status        uint8  `json:"status"`
	Error         uint8  `json:"error"`
	SectorCount   uint8  `json:"sector_count"`
	Device        uint8  `json:"device"`
}

type DiskErrorLog struct {
	Entries       []DiskErrorEntry `json:"entries"`
	ChecksumValid bool             `json:"checksum_valid"`
}

type DiskSelfTestEntry struct {
	Type          string `json:"type"`
	Status        string `json:"status"`
	RemainingPct  int    `json:"remaining_pct"`
	LifetimeHours uint64 `json:"lifetime_hours"`
	LBA           uint64 `json:"lba"`
	NSID          uint32 `json:"nsid"`
}

type DiskSelfTestLog struct {
	Entries       []DiskSelfTestEntry `json:"entries"`
	InProgress    bool                `json:"in_progress"`
	ProgressPct   int                 `json:"progress_pct"`
	ChecksumValid bool                `json:"checksum_valid"`
}

type DiskAttribute struct {
	Page      uint32 `json:"page"`
	ID        uint32 `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Worst     int    `json:"worst"`
	Threshold int    `json:"threshold"`
	RawValue  uint64 `json:"raw_value"`
	RawString string `json:"raw_string,omitempty"`
}
