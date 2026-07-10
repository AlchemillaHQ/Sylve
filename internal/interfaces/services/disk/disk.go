// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskServiceInterfaces

import (
	"context"
	"time"
)

type Partition struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Usage string `json:"usage"`
	Size  uint64 `json:"size"`
}

type Disk struct {
	UUID                  string           `json:"uuid"`
	IdentityStable        bool             `json:"identityStable"`
	Device                string           `json:"device"`
	Type                  string           `json:"type"`
	Usage                 string           `json:"usage"`
	Size                  uint64           `json:"size"`
	Model                 string           `json:"model"`
	Serial                string           `json:"serial"`
	GPT                   bool             `json:"gpt"`
	SmartData             any              `json:"smartData"`
	SmartReadPowerSkipped bool             `json:"-"`
	WearOut               string           `json:"wearOut"`
	SelfTestLog           *DiskSelfTestLog `json:"selfTestLog,omitempty"`
	Partitions            []Partition      `json:"partitions"`
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
	HealthKnown         bool                    `json:"health_known"`
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
	LBAValid      bool   `json:"lba_valid"`
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
	HealthKnown          bool       `json:"health_known"`
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
	LBAValid      bool   `json:"lba_valid"`
	NSID          uint32 `json:"nsid"`
	NSIDValid     bool   `json:"nsid_valid"`
}

type DiskSelfTestLog struct {
	Entries       []DiskSelfTestEntry `json:"entries"`
	InProgress    bool                `json:"in_progress"`
	ProgressPct   int                 `json:"progress_pct"`
	ChecksumValid bool                `json:"checksum_valid"`
}

type DiskSelfTestCapabilities struct {
	Protocol                  string `json:"protocol"`
	Scope                     string `json:"scope"`
	NamespaceID               uint32 `json:"namespace_id"`
	SingleOperation           bool   `json:"single_operation"`
	ExecutionSupportKnown     bool   `json:"execution_support_known"`
	Supported                 bool   `json:"supported"`
	Offline                   bool   `json:"offline"`
	Default                   bool   `json:"default"`
	Short                     bool   `json:"short"`
	Extended                  bool   `json:"extended"`
	Conveyance                bool   `json:"conveyance"`
	Selective                 bool   `json:"selective"`
	ShortCaptive              bool   `json:"short_captive"`
	ExtendedCaptive           bool   `json:"extended_captive"`
	ConveyanceCaptive         bool   `json:"conveyance_captive"`
	SelectiveCaptive          bool   `json:"selective_captive"`
	Abort                     bool   `json:"abort"`
	ResultLog                 bool   `json:"result_log"`
	Progress                  bool   `json:"progress"`
	OfflineDurationMinutes    int    `json:"offline_duration_minutes"`
	ShortDurationMinutes      int    `json:"short_duration_minutes"`
	ExtendedDurationMinutes   int    `json:"extended_duration_minutes"`
	ConveyanceDurationMinutes int    `json:"conveyance_duration_minutes"`
}

type DiskSelfTestResult struct {
	Protocol            string     `json:"protocol"`
	Type                string     `json:"type"`
	Mode                string     `json:"mode"`
	Status              string     `json:"status"`
	Outcome             string     `json:"outcome"`
	RemainingPct        int        `json:"remaining_pct"`
	LifetimeHours       uint64     `json:"lifetime_hours"`
	LifetimeHoursExact  string     `json:"lifetime_hours_exact"`
	LBA                 uint64     `json:"lba"`
	LBAExact            string     `json:"lba_exact"`
	LBAValid            bool       `json:"lba_valid"`
	NSID                uint32     `json:"nsid"`
	NSIDValid           bool       `json:"nsid_valid"`
	SegmentNum          uint8      `json:"segment_num"`
	SenseKey            uint8      `json:"sense_key"`
	ASC                 uint8      `json:"asc"`
	ASCQ                uint8      `json:"ascq"`
	StatusCodeType      uint8      `json:"status_code_type"`
	StatusCodeTypeValid bool       `json:"status_code_type_valid"`
	StatusCode          uint8      `json:"status_code"`
	StatusCodeValid     bool       `json:"status_code_valid"`
	Checkpoint          uint8      `json:"checkpoint"`
	ParameterCode       uint16     `json:"parameter_code"`
	VendorSpecific      uint8      `json:"vendor_specific"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
}

type DiskSelfTestState struct {
	Protocol                 string               `json:"protocol"`
	NamespaceID              uint32               `json:"namespace_id"`
	State                    string               `json:"state"`
	ExecutionStatus          string               `json:"execution_status"`
	Type                     string               `json:"type"`
	Running                  bool                 `json:"running"`
	ProgressPct              int                  `json:"progress_pct"`
	ProgressKnown            bool                 `json:"progress_known"`
	RemainingPct             int                  `json:"remaining_pct"`
	RemainingKnown           bool                 `json:"remaining_known"`
	EstimatedDurationMinutes int                  `json:"estimated_duration_minutes"`
	OfflineCollectionStatus  string               `json:"offline_collection_status"`
	OfflineCollectionRunning bool                 `json:"offline_collection_running"`
	ChecksumValid            bool                 `json:"checksum_valid"`
	Results                  []DiskSelfTestResult `json:"results"`
}

type DiskSelfTestInfo struct {
	Device       string                   `json:"device"`
	Capabilities DiskSelfTestCapabilities `json:"capabilities"`
	Status       DiskSelfTestState        `json:"status"`
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
