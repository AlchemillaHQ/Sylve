// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

type ATAPowerMode int16

const (
	ATAPowerModeUnknown      ATAPowerMode = -2
	ATAPowerModeSleep        ATAPowerMode = -1
	ATAPowerModeStandby      ATAPowerMode = 0x00
	ATAPowerModeStandbyY     ATAPowerMode = 0x01
	ATAPowerModeActive       ATAPowerMode = 0x40
	ATAPowerModeActiveAlt    ATAPowerMode = 0x41
	ATAPowerModeIdle         ATAPowerMode = 0x80
	ATAPowerModeIdleA        ATAPowerMode = 0x81
	ATAPowerModeIdleB        ATAPowerMode = 0x82
	ATAPowerModeIdleC        ATAPowerMode = 0x83
	ATAPowerModeActiveOrIdle ATAPowerMode = 0xff
)

func (m ATAPowerMode) IsStandbyOrSleeping() bool {
	return m == ATAPowerModeSleep || m == ATAPowerModeStandby || m == ATAPowerModeStandbyY
}

func (m ATAPowerMode) String() string {
	switch m {
	case ATAPowerModeSleep:
		return "sleep"
	case ATAPowerModeStandby:
		return "standby"
	case ATAPowerModeStandbyY:
		return "standby_y"
	case ATAPowerModeActive, ATAPowerModeActiveAlt:
		return "active"
	case ATAPowerModeIdle:
		return "idle"
	case ATAPowerModeIdleA:
		return "idle_a"
	case ATAPowerModeIdleB:
		return "idle_b"
	case ATAPowerModeIdleC:
		return "idle_c"
	case ATAPowerModeActiveOrIdle:
		return "active_or_idle"
	default:
		return "unknown"
	}
}

type SCSIPowerMode int8

const (
	SCSIPowerModeUnknown SCSIPowerMode = iota - 1
	SCSIPowerModeActive
	SCSIPowerModeLowPower
	SCSIPowerModeIdle
	SCSIPowerModeStandby
	SCSIPowerModeStandbyY
	SCSIPowerModeSleep
)

func (m SCSIPowerMode) IsStandbyOrSleeping() bool {
	return m == SCSIPowerModeLowPower || m == SCSIPowerModeStandby || m == SCSIPowerModeStandbyY || m == SCSIPowerModeSleep
}

func (m SCSIPowerMode) String() string {
	switch m {
	case SCSIPowerModeActive:
		return "active"
	case SCSIPowerModeLowPower:
		return "low_power"
	case SCSIPowerModeIdle:
		return "idle"
	case SCSIPowerModeStandby:
		return "standby"
	case SCSIPowerModeStandbyY:
		return "standby_y"
	case SCSIPowerModeSleep:
		return "sleep"
	default:
		return "unknown"
	}
}

const (
	SelfTestOffline           uint8 = 0x00
	SelfTestShort             uint8 = 0x01
	SelfTestExtended          uint8 = 0x02
	SelfTestConveyance        uint8 = 0x03
	SelfTestSelective         uint8 = 0x04
	SelfTestAbort             uint8 = 0x7f
	SelfTestShortCaptive      uint8 = 0x81
	SelfTestExtendedCaptive   uint8 = 0x82
	SelfTestConveyanceCaptive uint8 = 0x83
	SelfTestSelectiveCaptive  uint8 = 0x84
)

type Attribute struct {
	Page      uint32
	ID        uint32
	Name      string
	Value     int
	Worst     int
	Threshold int
	RawValue  uint64
	RawString string
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
	AttrStateNonExisting
	AttrStateNoNormVal
)

func AtaAttrState(current, worst, threshold int, def *AttrDef) int {
	if def != nil && def.NoNormVal {
		return AttrStateNoNormVal
	}
	if threshold < 0 {
		return AttrStateNoThreshold
	}
	if threshold == 0 {
		return AttrStateOK
	}
	if current <= threshold {
		return AttrStateFailedNow
	}
	if def == nil || !def.NoWorstVal {
		if worst <= threshold {
			return AttrStateFailedPast
		}
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
	switch statusNibble {
	case 0x0:
		status = "completed"
	case 0x1:
		status = "aborted_by_host"
	case 0x2:
		status = "interrupted"
	case 0x3:
		status = "fatal"
	case 0x4:
		status = "failed_unknown"
	case 0x5:
		status = "failed_electrical"
	case 0x6:
		status = "failed_servo"
	case 0x7:
		status = "failed_read"
	case 0x8:
		status = "failed_handling"
	case 0x9, 0xA, 0xB, 0xC, 0xD, 0xE:
		status = "reserved"
	case 0xF:
		status = "in_progress"
	default:
		status = "reserved"
	}

	rc := -1
	if remainNibble <= 9 {
		rc = int(remainNibble) * 10
	}
	return SelfTestExecStatus{
		Status:       status,
		RemainingPct: rc,
	}
}

type SelfTestEntry struct {
	Protocol            string
	Type                string
	Mode                string
	Status              string
	Outcome             string
	RemainingPct        int
	LifetimeHours       uint64
	LBA                 uint64
	LBAValid            bool
	NSID                uint32
	NSIDValid           bool
	SegmentNum          uint8
	SenseKey            uint8
	ASC                 uint8
	ASCQ                uint8
	StatusCodeType      uint8
	StatusCodeTypeValid bool
	StatusCode          uint8
	StatusCodeValid     bool
	Checkpoint          uint8
	ParameterCode       uint16
	VendorSpecific      uint8
}

type SelectiveSpan struct {
	Start uint64
	End   uint64
	Size  uint64
	Mode  SelectiveSpanMode
}

type SelfTestLog struct {
	Entries              []SelfTestEntry
	Revision             uint16
	InProgress           bool
	CurrentType          string
	ProgressPct          int
	ProgressKnown        bool
	ChecksumValid        bool
	SelectiveSpans       [5]SelectiveSpan
	SelectiveCurrentLBA  uint64
	SelectiveCurrentSpan uint16
	SelectiveFlags       uint16
	SelectivePendingTime uint16
	SelectiveScanEnabled bool
	SelectiveScanPending bool
	SelectiveScanActive  bool
}

type SCSISelfTestEntry struct {
	Type           string
	Mode           string
	Status         string
	LifetimeHours  uint64
	LBA            uint64
	LBAValid       bool
	SenseKey       uint8
	ASC            uint8
	ASCQ           uint8
	SegmentNumber  uint8
	ParameterCode  uint16
	VendorSpecific uint8
}

type ATAErrorEntry struct {
	ErrorData     uint16
	ExtendedData  uint16
	LifetimeHours uint32
	LBA           uint64
	Status        uint8
	Error         uint8
	SectorCount   uint8
	SectorCount16 uint16
	Device        uint8
}

type ATAErrorLog struct {
	Entries       []ATAErrorEntry
	Revision      uint16
	ErrorCount    uint32
	ChecksumValid bool
}

type NVMeErrorEntry struct {
	ErrorCount  uint64
	SQID        uint16
	CommandID   uint16
	StatusField uint16
	ParamError  uint16
	LBA         uint64
	NamespaceID uint32
}

type NVMeErrorLog struct {
	Entries  []NVMeErrorEntry
	Capacity int
}

type SCTStatus struct {
	FormatVersion     uint16
	SCTVersion        uint16
	SCTSpec           uint16
	StatusFlags       uint32
	DeviceState       uint8
	ExtStatusCode     uint16
	ActionCode        uint16
	FunctionCode      uint16
	LBACurrent        uint64
	CurrentTemp       int8
	MinTempCycle      int8
	MaxTempCycle      int8
	LifetimeMinTemp   int8
	LifetimeMaxTemp   int8
	MaxOpLimit        int8
	OverTempCount     uint32
	UnderTempCount    uint32
	SmartStatusPassed bool
	SmartStatusKnown  bool
	MinERCTime        uint16
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
	SerialNumber          string
	ModelNumber           string
	FirmwareRev           string
	NVMeVersion           uint32
	PCIVendorID           uint16
	SubsystemVendorID     uint16
	WCTemp                uint16
	CCTemp                uint16
	MNTMT                 uint16
	MXTMT                 uint16
	TotalCapacity         uint64
	TotalCapacityString   string
	UnallocCapacity       uint64
	UnallocCapacityString string
	NumNamespaces         uint32
	MaxDataXferSize       uint8
	AbortCmdLimit         uint8
	AsyncEventLimit       uint8
	FirmwareSlots         uint8
	ErrorLogEntries       uint16
	NumPowerStates        uint16
	SanitizeCaps          uint32
	VolatileWriteCache    bool
	HostMemBufPreferred   uint32
	HostMemBufMin         uint32
	SelfTestTimeMinutes   uint16
	SelfTestOptions       uint8
	OptionalAdminCommands uint16
	SelfTestSupported     bool
	NamespaceID           uint32
	ControllerID          uint16
	IEEE                  [3]uint8
}

type NVMeLBAFormat struct {
	MetadataSize        uint16
	DataSizeExponent    uint8
	DataSize            uint64
	RelativePerformance uint8
}

type NVMeIdentifyNamespace struct {
	NamespaceID             uint32
	Size                    uint64
	Capacity                uint64
	Utilization             uint64
	Features                uint8
	FormattedLBA            uint8
	MetadataCapabilities    uint8
	DataProtectionCaps      uint8
	DataProtectionSettings  uint8
	MultipathCapabilities   uint8
	ReservationCapabilities uint8
	FormatProgressIndicator uint8
	NVMCapacity             uint64
	NVMCapacityString       string
	NamespaceGUID           [16]uint8
	IEEEExtendedUniqueID    [8]uint8
	LBAFormats              []NVMeLBAFormat
}

type DeviceInfo struct {
	Device              string
	Vendor              string
	Model               string
	Serial              string
	Firmware            string
	ModelFamily         string
	DriveDBWarning      string
	FirmwareBugs        FirmwareBug
	SCTSupported        bool
	SectorCount         uint64
	Protocol            string
	Passed              bool
	HealthKnown         bool
	ChecksumValid       bool
	Temperature         int
	PowerOnHours        int
	PowerCycleCount     int
	SelfTestStatus      SelfTestExecStatus
	SmartCapability     uint64
	Attributes          []Attribute
	SCSISelfTestLog     *SelfTestLog
	SCSISelfTestResults []SCSISelfTestEntry
}
