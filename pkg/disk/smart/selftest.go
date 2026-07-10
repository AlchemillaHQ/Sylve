// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

type SelfTestKind string

const (
	SelfTestKindOffline           SelfTestKind = "offline"
	SelfTestKindDefault           SelfTestKind = "default"
	SelfTestKindShort             SelfTestKind = "short"
	SelfTestKindExtended          SelfTestKind = "extended"
	SelfTestKindConveyance        SelfTestKind = "conveyance"
	SelfTestKindSelective         SelfTestKind = "selective"
	SelfTestKindShortCaptive      SelfTestKind = "short_captive"
	SelfTestKindExtendedCaptive   SelfTestKind = "extended_captive"
	SelfTestKindConveyanceCaptive SelfTestKind = "conveyance_captive"
	SelfTestKindSelectiveCaptive  SelfTestKind = "selective_captive"
)

const (
	SelfTestStateIdle      = "idle"
	SelfTestStateRunning   = "running"
	SelfTestStateAmbiguous = "ambiguous"
)

const (
	SelfTestOutcomePassed     = "passed"
	SelfTestOutcomeAborted    = "aborted"
	SelfTestOutcomeFailed     = "failed"
	SelfTestOutcomeInProgress = "in_progress"
	SelfTestOutcomeUnknown    = "unknown"
)

var ErrSelfTestInProgress = errors.New("a drive self-test is already in progress")
var ErrInvalidSelfTestType = errors.New("invalid drive self-test type")
var ErrSelfTestConfigurationRequired = errors.New("drive self-test configuration is required")
var ErrInvalidSelectiveSpan = errors.New("invalid selective self-test span")
var ErrInvalidSelectiveOption = errors.New("invalid selective self-test option")
var ErrDeviceCapacityUnknown = errors.New("drive capacity is unavailable")

type SelectiveSpanMode uint8

const (
	SelectiveSpanRange SelectiveSpanMode = iota
	SelectiveSpanRedo
	SelectiveSpanNext
	SelectiveSpanContinue
)

type SelectiveAfterSelectMode uint8

const (
	SelectiveAfterSelectPreserve SelectiveAfterSelectMode = iota
	SelectiveAfterSelectDisable
	SelectiveAfterSelectEnable
)

type SelfTestCapabilities struct {
	Protocol                  string
	Scope                     string
	NamespaceID               uint32
	SingleOperation           bool
	Supported                 bool
	ExecutionSupportKnown     bool
	Offline                   bool
	Default                   bool
	Short                     bool
	Extended                  bool
	Conveyance                bool
	Selective                 bool
	ShortCaptive              bool
	ExtendedCaptive           bool
	ConveyanceCaptive         bool
	SelectiveCaptive          bool
	Abort                     bool
	ResultLog                 bool
	Progress                  bool
	OfflineDurationMinutes    int
	ShortDurationMinutes      int
	ExtendedDurationMinutes   int
	ConveyanceDurationMinutes int
}

func (c SelfTestCapabilities) Supports(kind SelfTestKind) bool {
	switch kind {
	case SelfTestKindOffline:
		return c.Offline
	case SelfTestKindDefault:
		return c.Default
	case SelfTestKindShort:
		return c.Short
	case SelfTestKindExtended:
		return c.Extended
	case SelfTestKindConveyance:
		return c.Conveyance
	case SelfTestKindSelective:
		return c.Selective
	case SelfTestKindShortCaptive:
		return c.ShortCaptive
	case SelfTestKindExtendedCaptive:
		return c.ExtendedCaptive
	case SelfTestKindConveyanceCaptive:
		return c.ConveyanceCaptive
	case SelfTestKindSelectiveCaptive:
		return c.SelectiveCaptive
	default:
		return false
	}
}

func (c SelfTestCapabilities) DurationMinutes(kind SelfTestKind) int {
	switch kind {
	case SelfTestKindOffline:
		return c.OfflineDurationMinutes
	case SelfTestKindShort, SelfTestKindShortCaptive:
		return c.ShortDurationMinutes
	case SelfTestKindExtended, SelfTestKindExtendedCaptive:
		return c.ExtendedDurationMinutes
	case SelfTestKindConveyance, SelfTestKindConveyanceCaptive:
		return c.ConveyanceDurationMinutes
	default:
		return 0
	}
}

type SelfTestStatus struct {
	Protocol                 string
	NamespaceID              uint32
	State                    string
	ExecutionStatus          string
	Type                     SelfTestKind
	Running                  bool
	ProgressPct              int
	ProgressKnown            bool
	RemainingPct             int
	RemainingKnown           bool
	EstimatedDurationMinutes int
	OfflineCollectionStatus  string
	OfflineCollectionRunning bool
	ChecksumValid            bool
	Results                  []SelfTestEntry
}

type SelectiveSelfTestOptions struct {
	AfterSelect        SelectiveAfterSelectMode
	PendingTimeMinutes *uint16
	Captive            bool
}

func validateSelfTestStart(capabilities SelfTestCapabilities, status SelfTestStatus, kind SelfTestKind) error {
	if !capabilities.Supports(kind) && !scsiSelfTestCanAttempt(capabilities, kind) {
		return fmt.Errorf("%w: %s self-test", ErrUnsupportedFeature, kind)
	}
	if status.Running || status.State == SelfTestStateAmbiguous {
		return ErrSelfTestInProgress
	}
	return nil
}

func scsiSelfTestKind(kind SelfTestKind) bool {
	switch kind {
	case SelfTestKindDefault, SelfTestKindShort, SelfTestKindExtended, SelfTestKindShortCaptive, SelfTestKindExtendedCaptive:
		return true
	default:
		return false
	}
}

func scsiSelfTestCanAttempt(capabilities SelfTestCapabilities, kind SelfTestKind) bool {
	return capabilities.Protocol == "SCSI" && !capabilities.ExecutionSupportKnown && scsiSelfTestKind(kind)
}

func scsiSelfTestCapabilities(resultLog, executionKnown, executionSupported bool) SelfTestCapabilities {
	attemptable := !executionKnown || executionSupported
	capabilities := SelfTestCapabilities{
		Protocol:              "SCSI",
		Scope:                 "device",
		Supported:             attemptable,
		ExecutionSupportKnown: executionKnown,
		ResultLog:             resultLog,
	}
	if !attemptable {
		return capabilities
	}
	capabilities.Default = true
	capabilities.Short = true
	capabilities.Extended = true
	capabilities.ShortCaptive = true
	capabilities.ExtendedCaptive = true
	capabilities.Abort = true
	capabilities.Progress = true
	return capabilities
}

func selfTestKindFromCode(testType uint8) (SelfTestKind, bool) {
	switch testType {
	case SelfTestOffline:
		return SelfTestKindOffline, true
	case SelfTestShort:
		return SelfTestKindShort, true
	case SelfTestExtended:
		return SelfTestKindExtended, true
	case SelfTestConveyance:
		return SelfTestKindConveyance, true
	case SelfTestSelective:
		return SelfTestKindSelective, true
	case SelfTestShortCaptive:
		return SelfTestKindShortCaptive, true
	case SelfTestExtendedCaptive:
		return SelfTestKindExtendedCaptive, true
	case SelfTestConveyanceCaptive:
		return SelfTestKindConveyanceCaptive, true
	case SelfTestSelectiveCaptive:
		return SelfTestKindSelectiveCaptive, true
	default:
		return "", false
	}
}

func selfTestCode(kind SelfTestKind) (uint8, bool) {
	switch kind {
	case SelfTestKindOffline:
		return SelfTestOffline, true
	case SelfTestKindDefault:
		return SelfTestOffline, true
	case SelfTestKindShort:
		return SelfTestShort, true
	case SelfTestKindExtended:
		return SelfTestExtended, true
	case SelfTestKindConveyance:
		return SelfTestConveyance, true
	case SelfTestKindSelective:
		return SelfTestSelective, true
	case SelfTestKindShortCaptive:
		return SelfTestShortCaptive, true
	case SelfTestKindExtendedCaptive:
		return SelfTestExtendedCaptive, true
	case SelfTestKindConveyanceCaptive:
		return SelfTestConveyanceCaptive, true
	case SelfTestKindSelectiveCaptive:
		return SelfTestSelectiveCaptive, true
	default:
		return 0, false
	}
}

func validVendorSelfTestCode(code uint8) bool {
	return code >= 0x40 && code <= 0x7e || code >= 0x90
}

func buildATASelectiveSelfTestLog(raw []byte, spans []SelectiveSpan, options SelectiveSelfTestOptions, executionStatus string, sectorCount uint64) ([]byte, error) {
	if len(raw) < 512 || len(spans) == 0 || len(spans) > 5 {
		return nil, ErrInvalidSelectiveSpan
	}
	if sectorCount == 0 {
		return nil, ErrDeviceCapacityUnknown
	}
	if options.AfterSelect > SelectiveAfterSelectEnable {
		return nil, ErrInvalidSelectiveOption
	}
	out := make([]byte, 512)
	copy(out, raw[:512])
	binary.LittleEndian.PutUint16(out[0:2], 1)
	var resolved [5]SelectiveSpan
	for i, span := range spans {
		mode := span.Mode
		if mode == SelectiveSpanContinue {
			if executionStatus == "aborted_by_host" || executionStatus == "interrupted" {
				mode = SelectiveSpanRedo
			} else {
				mode = SelectiveSpanNext
			}
		}
		if mode > SelectiveSpanContinue {
			return nil, ErrInvalidSelectiveOption
		}
		start, end := span.Start, span.End
		previousOffset := 2 + i*16
		previousStart := binary.LittleEndian.Uint64(raw[previousOffset : previousOffset+8])
		previousEnd := binary.LittleEndian.Uint64(raw[previousOffset+8 : previousOffset+16])
		switch mode {
		case SelectiveSpanRedo:
			start = previousStart
			end = previousEnd
			if span.Size > 0 {
				if span.Size-1 > ^uint64(0)-start {
					end = ^uint64(0)
				} else {
					end = start + span.Size - 1
				}
			}
		case SelectiveSpanNext:
			if previousEnd == 0 {
				start = 0
				end = 0
				break
			}
			if previousEnd == ^uint64(0) || previousEnd+1 >= sectorCount {
				start = 0
			} else {
				start = previousEnd + 1
			}
			size := span.Size
			if size == 0 {
				if previousStart > previousEnd {
					return nil, ErrInvalidSelectiveSpan
				}
				if previousEnd-previousStart == ^uint64(0) {
					return nil, ErrInvalidSelectiveSpan
				}
				size = previousEnd - previousStart + 1
			}
			if size-1 > ^uint64(0)-start {
				end = ^uint64(0)
			} else {
				end = start + size - 1
			}
			if span.Size == 0 && end >= sectorCount {
				count := 1 + (sectorCount-1)/size
				newSize := 1 + (sectorCount-1)/count
				start = sectorCount - newSize
				end = sectorCount - 1
			}
		case SelectiveSpanRange:
			if span.Size != 0 {
				return nil, ErrInvalidSelectiveOption
			}
		}
		if start < sectorCount && end >= sectorCount {
			end = sectorCount - 1
		}
		if start > end || end >= sectorCount {
			return nil, fmt.Errorf("%w: %d-%d", ErrInvalidSelectiveSpan, start, end)
		}
		resolved[i] = SelectiveSpan{Start: start, End: end, Mode: mode}
	}
	for i := 0; i < 5; i++ {
		offset := 2 + i*16
		clear(out[offset : offset+16])
	}
	for i, span := range resolved[:len(spans)] {
		offset := 2 + i*16
		binary.LittleEndian.PutUint64(out[offset:offset+8], span.Start)
		binary.LittleEndian.PutUint64(out[offset+8:offset+16], span.End)
	}
	clear(out[492:502])
	flags := binary.LittleEndian.Uint16(out[502:504]) &^ 0x0018
	switch options.AfterSelect {
	case SelectiveAfterSelectDisable:
		flags &^= 0x0002
	case SelectiveAfterSelectEnable:
		flags |= 0x0002
	}
	binary.LittleEndian.PutUint16(out[502:504], flags)
	if options.PendingTimeMinutes != nil {
		binary.LittleEndian.PutUint16(out[508:510], *options.PendingTimeMinutes)
	}
	out[511] = 0
	var checksum byte
	for _, value := range out {
		checksum += value
	}
	out[511] = -checksum
	return out, nil
}

func parseATASelfTestCapabilities(raw []byte, identifySupported bool) SelfTestCapabilities {
	c := SelfTestCapabilities{Protocol: "ATA", Scope: "device"}
	if len(raw) < 377 {
		return c
	}
	c.ExecutionSupportKnown = true
	offline := raw[367]
	c.Offline = offline&0x01 != 0
	selfTestSupported := identifySupported || offline&0x10 != 0
	c.Supported = selfTestSupported || c.Offline
	c.Short = selfTestSupported
	c.Extended = selfTestSupported
	c.Conveyance = offline&0x20 != 0
	c.Selective = offline&0x40 != 0
	c.ShortCaptive = c.Short
	c.ExtendedCaptive = c.Extended
	c.ConveyanceCaptive = c.Conveyance
	c.SelectiveCaptive = c.Selective
	c.Abort = c.Offline || selfTestSupported
	c.ResultLog = selfTestSupported
	c.Progress = selfTestSupported
	offlineSeconds := int(binary.LittleEndian.Uint16(raw[364:366]))
	if offlineSeconds > 0 {
		c.OfflineDurationMinutes = (offlineSeconds + 59) / 60
	}
	c.ShortDurationMinutes = int(raw[372])
	c.ExtendedDurationMinutes = int(raw[373])
	extendedDuration := binary.LittleEndian.Uint16(raw[375:377])
	if raw[373] == 0xff && extendedDuration != 0 && extendedDuration != 0xffff {
		c.ExtendedDurationMinutes = int(extendedDuration)
	}
	c.ConveyanceDurationMinutes = int(raw[374])
	return c
}

func parseNVMeSelfTestCapabilities(ctrl *NVMeIdentifyCtrl) SelfTestCapabilities {
	c := SelfTestCapabilities{Protocol: "NVMe"}
	if ctrl == nil {
		return c
	}
	c.ExecutionSupportKnown = true
	if !ctrl.SelfTestSupported {
		return c
	}
	c.NamespaceID = ctrl.NamespaceID
	c.Scope = "namespace"
	c.SingleOperation = ctrl.SelfTestOptions&0x01 != 0
	c.Supported = true
	c.Short = true
	c.Extended = true
	c.Abort = true
	c.ResultLog = true
	c.Progress = true
	c.ExtendedDurationMinutes = int(ctrl.SelfTestTimeMinutes)
	return c
}

func parseSCSIControlSelfTestMinutes(raw []byte) (int, bool) {
	parse := func(offset int) (int, bool) {
		if offset < 0 || offset+12 > len(raw) || raw[offset]&0x3f != 0x0a || int(raw[offset+1]) < 10 {
			return 0, false
		}
		seconds := int(binary.BigEndian.Uint16(raw[offset+10 : offset+12]))
		if seconds <= 0 || seconds == 0xffff {
			return 0, false
		}
		return (seconds + 59) / 60, true
	}
	if len(raw) >= 4 {
		if minutes, ok := parse(4 + int(raw[3])); ok {
			return minutes, true
		}
	}
	if len(raw) >= 8 {
		if minutes, ok := parse(8 + int(binary.BigEndian.Uint16(raw[6:8]))); ok {
			return minutes, true
		}
	}
	return 0, false
}

func scsiControlNeedsExtendedInquiry(raw []byte) bool {
	check := func(offset int) bool {
		return offset >= 0 && offset+12 <= len(raw) && raw[offset]&0x3f == 0x0a && int(raw[offset+1]) >= 10 && binary.BigEndian.Uint16(raw[offset+10:offset+12]) == 0xffff
	}
	if len(raw) >= 4 && check(4+int(raw[3])) {
		return true
	}
	return len(raw) >= 8 && check(8+int(binary.BigEndian.Uint16(raw[6:8])))
}

func parseSCSIExtendedInquirySelfTestMinutes(raw []byte) (int, bool) {
	if len(raw) < 12 || raw[1] != 0x86 || int(binary.BigEndian.Uint16(raw[2:4])) < 8 {
		return 0, false
	}
	minutes := int(binary.BigEndian.Uint16(raw[10:12]))
	if minutes <= 0 || minutes == 0xffff {
		return 0, false
	}
	return minutes, true
}

func parseSCSISelfTestProgress(raw []byte) (bool, int, bool) {
	if len(raw) < 8 {
		return false, 0, false
	}
	switch raw[0] & 0x7f {
	case 0x70, 0x71:
		if len(raw) < 18 || raw[12] != 0x04 || raw[13] != 0x09 {
			return false, 0, false
		}
		if raw[15]&0x80 == 0 {
			return true, 0, false
		}
		value := int(binary.BigEndian.Uint16(raw[16:18]))
		return true, value * 100 / 0xffff, true
	case 0x72, 0x73:
		if raw[2] != 0x04 || raw[3] != 0x09 {
			return false, 0, false
		}
		end := 8 + int(raw[7])
		if end > len(raw) {
			end = len(raw)
		}
		for offset := 8; offset+2 <= end; {
			length := int(raw[offset+1])
			next := offset + 2 + length
			if next > end {
				break
			}
			if raw[offset] == 0x02 && length >= 6 && (raw[1]&0x0f == 0x00 || raw[1]&0x0f == 0x02) && raw[offset+4]&0x80 != 0 {
				value := int(binary.BigEndian.Uint16(raw[offset+5 : offset+7]))
				return true, value * 100 / 0xffff, true
			}
			if raw[offset] == 0x0a && length >= 6 {
				value := int(binary.BigEndian.Uint16(raw[offset+6 : offset+8]))
				return true, value * 100 / 0xffff, true
			}
			offset = next
		}
		return true, 0, false
	}
	return false, 0, false
}

func applySCSISelfTestSense(log *SelfTestLog, raw []byte) {
	if log == nil {
		return
	}
	running, progress, known := parseSCSISelfTestProgress(raw)
	log.InProgress = running
	log.ProgressPct = 0
	log.ProgressKnown = false
	if running {
		log.ProgressPct = progress
		log.ProgressKnown = known
		return
	}
	log.CurrentType = ""
}

func selfTestOutcome(status string) string {
	switch {
	case status == "completed":
		return SelfTestOutcomePassed
	case status == "in_progress":
		return SelfTestOutcomeInProgress
	case strings.HasPrefix(status, "aborted"), status == "interrupted":
		return SelfTestOutcomeAborted
	case strings.HasPrefix(status, "failed"), status == "fatal", status == "unknown_error", status == "completed_segment_failed":
		return SelfTestOutcomeFailed
	default:
		return SelfTestOutcomeUnknown
	}
}

func statusFromLog(protocol string, capabilities SelfTestCapabilities, log SelfTestLog) SelfTestStatus {
	status := SelfTestStatus{
		Protocol:      protocol,
		State:         SelfTestStateIdle,
		ProgressPct:   -1,
		RemainingPct:  -1,
		Results:       log.Entries,
		ProgressKnown: log.ProgressKnown,
		ChecksumValid: log.ChecksumValid,
	}
	if log.InProgress {
		status.State = SelfTestStateRunning
		status.Running = true
		status.Type = SelfTestKind(log.CurrentType)
		if log.ProgressKnown {
			status.ProgressPct = log.ProgressPct
			status.RemainingPct = 100 - log.ProgressPct
			status.RemainingKnown = true
		}
		status.EstimatedDurationMinutes = capabilities.DurationMinutes(status.Type)
		status.ExecutionStatus = "in_progress"
	} else {
		for _, entry := range log.Entries {
			if entry.Outcome == SelfTestOutcomeInProgress || entry.Status == "in_progress" {
				continue
			}
			status.ExecutionStatus = entry.Status
			break
		}
	}
	return status
}

func applyATAInProgressResultType(status *SelfTestStatus, capabilities SelfTestCapabilities) {
	if status == nil || status.Type != "" || status.State != SelfTestStateRunning && status.State != SelfTestStateAmbiguous {
		return
	}
	for i := range status.Results {
		entry := status.Results[i]
		if entry.Status != "in_progress" && entry.Outcome != SelfTestOutcomeInProgress || entry.Type == "" {
			continue
		}
		status.Type = SelfTestKind(entry.Type)
		status.EstimatedDurationMinutes = capabilities.DurationMinutes(status.Type)
		return
	}
}

func decodeATAOfflineCollectionStatus(raw byte) (string, bool) {
	value := raw & 0x7f
	switch value {
	case 0x00:
		return "never_started", false
	case 0x02:
		return "completed", false
	case 0x03:
		if raw == 0x03 {
			return "in_progress", true
		}
		return "reserved", false
	case 0x04:
		return "suspended", false
	case 0x05:
		return "aborted_by_host", false
	case 0x06:
		return "fatal", false
	default:
		if value >= 0x40 {
			return "vendor_specific", false
		}
		return "reserved", false
	}
}

func ataSelfTestStatusFromData(raw []byte, capabilities SelfTestCapabilities, bugs FirmwareBug) SelfTestStatus {
	status := SelfTestStatus{
		Protocol:      "ATA",
		State:         SelfTestStateIdle,
		ProgressPct:   -1,
		RemainingPct:  -1,
		ChecksumValid: false,
	}
	if len(raw) <= 363 {
		return status
	}
	status.OfflineCollectionStatus, status.OfflineCollectionRunning = decodeATAOfflineCollectionStatus(raw[362])
	exec := DecodeSelfTestExecStatus(uint64(raw[363]))
	if bugs&FirmwareBugSamsung3 != 0 && raw[363] == 0xf0 {
		exec.Status = "ambiguous_completed_or_in_progress"
		exec.RemainingPct = -1
	}
	status.ExecutionStatus = exec.Status
	switch exec.Status {
	case "in_progress":
		status.State = SelfTestStateRunning
		status.Running = true
		if exec.RemainingPct >= 0 {
			status.RemainingPct = exec.RemainingPct
			status.RemainingKnown = true
			status.ProgressPct = 100 - exec.RemainingPct
			status.ProgressKnown = true
		}
	case "ambiguous_completed_or_in_progress":
		status.State = SelfTestStateAmbiguous
	default:
		if status.OfflineCollectionRunning {
			status.State = SelfTestStateRunning
			status.Running = true
			status.Type = SelfTestKindOffline
			status.ExecutionStatus = SelfTestOutcomeInProgress
			status.EstimatedDurationMinutes = capabilities.OfflineDurationMinutes
		}
	}
	return status
}
