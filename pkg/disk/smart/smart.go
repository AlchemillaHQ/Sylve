// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

type Attribute struct {
	Page      uint32
	ID        uint32
	Name      string
	Value     int
	Worst     int
	Threshold int
	RawValue  uint64
	RawBytes  []byte
	TextValue string
	IsText    bool
	Flags     AttrFlags
}

type AttrFlags struct {
	PreFailure     bool
	Online         bool
	Performance    bool
	ErrorRate      bool
	EventCount     bool
	SelfPreserving bool
}

const (
	AttrStateOK = iota
	AttrStateFailedNow
	AttrStateFailedPast
	AttrStateNoThreshold
)

func AtaAttrState(current, worst, threshold int) int {
	if threshold == 0 {
		return AttrStateOK
	}
	if current <= threshold {
		return AttrStateFailedNow
	}
	if worst <= threshold {
		return AttrStateFailedPast
	}
	return AttrStateOK
}

type SelfTestExecStatus struct {
	Status       string
	RemainingPct int
}

func DecodeSelfTestExecStatus(raw uint64) SelfTestExecStatus {
	b := uint8(raw)
	statusNibble := (b >> 4) & 0x0F
	remainNibble := b & 0x0F

	var status string
	switch {
	case b == 0xF0:
		status = "completed_unknown"
	case statusNibble == 0x0:
		status = "completed"
	case statusNibble == 0x1:
		status = "aborted_by_host"
	case statusNibble == 0x2:
		status = "interrupted"
	case statusNibble == 0x3:
		status = "fatal"
	case statusNibble == 0x4:
		status = "failed_unknown"
	case statusNibble == 0x5:
		status = "failed_electrical"
	case statusNibble == 0x6:
		status = "failed_servo"
	case statusNibble == 0x7:
		status = "failed_read"
	case statusNibble == 0x8:
		status = "failed_handling"
	case statusNibble == 0xF:
		status = "in_progress"
	default:
		status = "reserved"
	}

	return SelfTestExecStatus{
		Status:       status,
		RemainingPct: int(remainNibble) * 10,
	}
}

type SelfTestEntry struct {
	Type          string
	Status        string
	RemainingPct  int
	LifetimeHours int
	LBA           int64
	NSID          uint32
}

type SelfTestLog struct {
	Entries        []SelfTestEntry
	InProgress     bool
	ProgressPct    int
	ChecksumValid  bool
}

type SCSISelfTestEntry struct {
	Type          string
	Status        string
	LifetimeHours int
	LBA           uint64
	SenseKey      uint8
	ASC           uint8
	ASCQ          uint8
}

type ATAErrorEntry struct {
	ErrorData    uint16
	ExtendedData uint16
	LifetimeHours uint32
	LBA          uint64
	Status       uint8
	Error        uint8
	SectorCount  uint8
	Device       uint8
}

type ATAErrorLog struct {
	Entries       []ATAErrorEntry
	ChecksumValid bool
}

type NVMeErrorEntry struct {
	ErrorCount   uint64
	SQID         uint16
	CommandID    uint16
	StatusField  uint16
	ParamError   uint16
	LBA          uint64
	NamespaceID  uint32
}

type NVMeErrorLog struct {
	Entries []NVMeErrorEntry
}

type SCTStatus struct {
	FormatVersion      uint16
	SCTVersion         uint16
	SCTSpec            uint16
	StatusFlags        uint32
	DeviceState        uint8
	ExtStatusCode      uint16
	ActionCode         uint16
	FunctionCode       uint16
	LBACurrent         uint64
	CurrentTemp        int8
	MinTempCycle       int8
	MaxTempCycle       int8
	LifetimeMinTemp    int8
	LifetimeMaxTemp    int8
	MaxOpLimit         int8
	OverTempCount      uint32
	UnderTempCount     uint32
	SmartStatusPassed  bool
	MinERCTime         uint16
}

type SCTTempSample struct {
	Temperature int8
}

type SCTTempHistory struct {
	SamplingPeriod uint16
	Interval       uint16
	MaxOpLimit     int8
	OverLimit      int8
	MinOpLimit     int8
	UnderLimit     int8
	CBSize         uint16
	CBIndex        uint16
	Samples        []SCTTempSample
}

type NVMeIdentifyCtrl struct {
	SerialNumber     string
	ModelNumber      string
	FirmwareRev      string
	NVMeVersion      uint32
	PCIVendorID      uint16
	SubsystemVendorID uint16
	WCTemp           uint16
	CCTemp           uint16
	MNTMT            uint16
	MXTMT            uint16
	TotalCapacity    uint64
	UnallocCapacity  uint64
	NumNamespaces    uint32
	MaxDataXferSize  uint8
	AbortCmdLimit    uint8
	AsyncEventLimit  uint8
	FirmwareSlots    uint8
	ErrorLogEntries  uint8
	NumPowerStates   uint8
	SanitizeCaps     uint32
	VolatileWriteCache bool
	HostMemBufPreferred uint32
	HostMemBufMin     uint32
	SelfTestTimeMinutes uint16
	SelfTestOptions   uint8
	ControllerID     uint16
	IEEE             [3]uint8
}

type DeviceInfo struct {
	Device               string
	Protocol             string
	Passed               bool
	ChecksumValid        bool
	Temperature          int
	PowerOnHours         int
	PowerCycleCount      int
	SelfTestStatus       SelfTestExecStatus
	SmartCapability      uint64
	Attributes           []Attribute
	SCSISelfTestResults  []SCSISelfTestEntry
}
