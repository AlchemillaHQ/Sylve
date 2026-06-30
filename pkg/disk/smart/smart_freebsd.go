// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package smart

/*
#cgo CFLAGS: -I.
#cgo LDFLAGS: -lcam
#include <stdlib.h>
#include <stdbool.h>
#include "libsmart.h"
#include "libsmart_priv.h"

bool do_debug = false;

static smart_attr_t get_attr_at(smart_map_t *map, int i) {
    return map->attr[i];
}

static int get_protocol(smart_map_t *map) {
    if (!map || !map->sb) return -1;
    return (int)map->sb->protocol;
}

static int get_threshold_for_id(smart_attr_t *attr, uint32_t target_id) {
    if (!attr || !attr->thresh) return 0;

    struct smart_map_s *thresh_map = attr->thresh;

    for (uint32_t i = 0; i < thresh_map->count; i++) {
        smart_attr_t t_attr = thresh_map->attr[i];

        if (t_attr.id == target_id) {
            // ATA Threshold Entry Structure (12 bytes):
            // Byte 1: Threshold Value
            if (t_attr.raw && t_attr.bytes >= 2) {
                unsigned char *b = (unsigned char *)t_attr.raw;
                return (int)b[1];
            }
        }
    }
    return 0;
}

static int check_ata_smart_checksum(smart_map_t *sm) {
    if (!sm || !sm->sb || !sm->sb->b || sm->sb->bsize < 512)
        return 0;

    unsigned char *buf = (unsigned char *)sm->sb->b;
    unsigned char sum = 0;
    for (int i = 0; i < 512; i++)
        sum += buf[i];
    return (sum == 0) ? 1 : 0;
}

static void * get_sm_buffer(smart_map_t *sm, size_t *size) {
    if (!sm || !sm->sb) return NULL;
    *size = sm->sb->bsize;
    return sm->sb->b;
}

static const char * get_device_model(smart_h h) {
    smart_t *s = (smart_t *)h;
    if (!s) return "";
    return s->info.device;
}
*/
import "C"
import (
	"fmt"
	"strings"
	"unsafe"
)

func Read(devicePath string) (*DeviceInfo, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read(handle)
	if sm == nil {
		C.smart_enable(handle)
		sm = C.smart_read(handle)
	}
	if sm == nil {
		return nil, fmt.Errorf("failed to read SMART data from %s", devicePath)
	}
	defer C.smart_free(sm)

	info := &DeviceInfo{
		Device:     strings.TrimPrefix(devicePath, "/dev/"),
		Passed:     true,
		Attributes: make([]Attribute, 0, int(sm.count)),
	}

	model := C.GoString(C.get_device_model(handle))
	modelAttrs := LookupModelAttrs(model)

	if isATA := C.get_protocol(sm) == C.SMART_PROTO_ATA; isATA {
		info.ChecksumValid = C.check_ata_smart_checksum(sm) != 0
	} else {
		info.ChecksumValid = true
	}

	protoVal := C.get_protocol(sm)
	isATA := false

	switch protoVal {
	case C.SMART_PROTO_ATA:
		info.Protocol = "ATA"
		isATA = true
	case C.SMART_PROTO_SCSI:
		info.Protocol = "SCSI"
	case C.SMART_PROTO_NVME:
		info.Protocol = "NVMe"
	default:
		info.Protocol = "Unknown"
	}

	count := int(sm.count)
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		thresh := int(C.get_threshold_for_id(&cAttr, cAttr.id))

		attr := Attribute{
			Page:      uint32(cAttr.page),
			ID:        uint32(cAttr.id),
			Name:      C.GoString(cAttr.description),
			Threshold: thresh,
		}

		if def, ok := modelAttrs[uint32(cAttr.id)]; ok {
			if def.Name != "" {
				attr.Name = def.Name
			}
		} else if attr.ID == 254 || attr.ID == 255 {
		} else if def, ok := LookupAttrDef(attr.ID); ok {
			if def.Name != "" {
				attr.Name = def.Name
			}
		}

		if cAttr.raw != nil && cAttr.bytes > 0 {
			attr.RawBytes = C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

			// Check Flags
			// 0x01 = Big Endian
			// 0x02 = String
			isBigEndian := (uint32(cAttr.flags) & 0x01) != 0
			attr.IsText = (uint32(cAttr.flags) & 0x02) != 0

			if attr.IsText {
				attr.TextValue = string(attr.RawBytes)
			} else {
				attr.RawValue = bytesToUint64(attr.RawBytes, isBigEndian)
			}

			// ATA Specific Parsing for Value/Worst
			// Standard ATA Attribute Layout (12 bytes):
			// [0] ID, [1-2] Flags, [3] Current, [4] Worst, [5-10] Raw
			if isATA && len(attr.RawBytes) >= 12 {
				attr.Value = int(attr.RawBytes[3])
				attr.Worst = int(attr.RawBytes[4])

				f := attr.RawBytes[1]
				attr.Flags = AttrFlags{
					PreFailure:     (f & 0x01) != 0,
					Online:         (f & 0x02) != 0,
					Performance:    (f & 0x04) != 0,
					ErrorRate:      (f & 0x08) != 0,
					EventCount:     (f & 0x10) != 0,
					SelfPreserving: (f & 0x20) != 0,
				}
			}
		}

		if attr.Page == 0xDA && attr.ID == 0 {
			info.Passed = (attr.RawValue == 0)
		}

		info.Attributes = append(info.Attributes, attr)

		if !isATA {
			if attr.Page == 0x0D && attr.ID == 0 {
				info.Temperature = int(attr.RawValue)
			}
			if attr.Page == 0x02 && attr.ID == 1 {
				t := int(attr.RawValue)
				if t > 273 {
					t -= 273
				}
				info.Temperature = t
			}
			if attr.Page == 0x10 && len(attr.RawBytes) >= 20 {
				e := parseSCSISelfTestEntry(attr.RawBytes)
				if e.Type != "" {
					info.SCSISelfTestResults = append(info.SCSISelfTestResults, e)
				}
			}
			continue
		}

		// Populate ATA convenience fields
		switch attr.ID {
		case 9:
			info.PowerOnHours = int(attr.RawValue)
		case 12:
			info.PowerCycleCount = int(attr.RawValue)
		case 254:
			info.SmartCapability = attr.RawValue
		case 255:
			info.SelfTestStatus = DecodeSelfTestExecStatus(attr.RawValue)
		}
	}

	if isATA {
		info.Temperature = findTemperature(info.Attributes)
	}

	return info, nil
}

func findTemperature(attrs []Attribute) int {
	ids := []int{194, 190}
	for _, id := range ids {
		for _, attr := range attrs {
			if int(attr.ID) != id {
				continue
			}
			t := int(attr.RawValue & 0xFF)
			if t > 0 && t < 128 {
				return t
			}
		}
	}
	return 0
}

func checksumOK(raw []byte) bool {
	if len(raw) < 512 {
		return false
	}
	var sum byte
	for i := 0; i < 512; i++ {
		sum += raw[i]
	}
	return sum == 0
}

func bytesToUint64(b []byte, bigEndian bool) uint64 {
	var res uint64
	length := len(b)

	if length == 12 && !bigEndian {
		start := 5
		for i := 0; i < 6; i++ {
			val := uint64(b[start+i])
			res |= val << (8 * i)
		}
		return res
	}

	for i := 0; i < length; i++ {
		var val uint64
		if bigEndian {
			val = uint64(b[i])
			res = (res << 8) | val
		} else {
			// Little Endian: First byte is LSB
			val = uint64(b[i])
			res |= val << (8 * i)
		}
	}

	return res
}

func SelfTest(devicePath string, testType uint8) error {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	rc := C.smart_self_test(handle, C.uint8_t(testType))
	if rc != 0 {
		return fmt.Errorf("self-test command failed with code %d", int(rc))
	}

	return nil
}

func AbortSelfTest(devicePath string) error {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	rc := C.smart_self_test(handle, 0x7F)
	if rc != 0 {
		return fmt.Errorf("abort self-test failed with code %d", int(rc))
	}

	return nil
}

func ReadSelfTestLog(devicePath string) (*SelfTestLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0x06), C.size_t(564))
	if sm == nil {
		return nil, fmt.Errorf("failed to read self-test log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &SelfTestLog{}
	count := int(sm.count)

	var bufSize C.size_t
	rawBuf := C.get_sm_buffer(sm, &bufSize)
	if rawBuf != nil && int(bufSize) >= 512 {
		raw := C.GoBytes(rawBuf, C.int(bufSize))
		log.ChecksumValid = checksumOK(raw)
	}

	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))

		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) == 0 {
			continue
		}

		entry := SelfTestEntry{}
		page := uint32(cAttr.page)

		if page == 0x06 && len(raw) == 24 {
			entry = parseATASelfTestEntry(raw)
		} else if page == 0x06 && len(raw) == 28 {
			entry = parseNVMESelfTestEntry(raw)
		} else if page == 0x06 && len(raw) == 1 && cAttr.id == 0 {
			log.InProgress = (raw[0] & 0x0F) != 0
			continue
		} else if page == 0x06 && len(raw) == 1 && cAttr.id == 1 {
			log.ProgressPct = int(raw[0] & 0x7F)
			continue
		}

		if entry.Type != "" {
			log.Entries = append(log.Entries, entry)
		}
	}

	return log, nil
}

func parseATASelfTestEntry(raw []byte) SelfTestEntry {
	e := SelfTestEntry{}

	switch raw[0] {
	case 0x00: e.Type = "offline"
	case 0x01: e.Type = "short"
	case 0x02: e.Type = "extended"
	case 0x03: e.Type = "conveyance"
	case 0x04: e.Type = "selective"
	case 0x7F: e.Type = "abort"
	case 0x81: e.Type = "short_captive"
	case 0x82: e.Type = "extended_captive"
	case 0x83: e.Type = "conveyance_captive"
	default:   e.Type = "unknown"
	}

	status := raw[1] >> 4
	switch status {
	case 0x0: e.Status = "completed"
	case 0x1: e.Status = "aborted_by_host"
	case 0x2: e.Status = "interrupted"
	case 0x3: e.Status = "fatal"
	case 0x4: e.Status = "failed_unknown"
	case 0x5: e.Status = "failed_electrical"
	case 0x6: e.Status = "failed_servo"
	case 0x7: e.Status = "failed_read"
	case 0x8: e.Status = "failed_handling"
	case 0xF: e.Status = "in_progress"
	default:  e.Status = "unknown"
	}

	e.RemainingPct = int(raw[1]&0x0F) * 10

	if len(raw) >= 4 {
		e.LifetimeHours = int(uint16(raw[2]) | uint16(raw[3])<<8)
	}

	if len(raw) == 26 {
		if len(raw) >= 16 {
			e.LBA = int64(uint64(raw[10]) | uint64(raw[11])<<8 |
				uint64(raw[12])<<16 | uint64(raw[13])<<24 |
				uint64(raw[14])<<32 | uint64(raw[15])<<40)
		}
	} else if len(raw) >= 8 {
		lba := uint64(raw[5]) | uint64(raw[6])<<8 | uint64(raw[7])<<16 | uint64(raw[8])<<24
		if lba >= 0xFFFFFFFF {
			lba = 0xFFFFFFFFFFFF
		}
		e.LBA = int64(lba)
	}

	return e
}

func parseNVMESelfTestEntry(raw []byte) SelfTestEntry {
	e := SelfTestEntry{}

	status := raw[0]
	op := (status >> 4) & 0x0F
	result := status & 0x0F

	switch op {
	case 0x1: e.Type = "short"
	case 0x2: e.Type = "extended"
	case 0xE: e.Type = "vendor"
	default:  e.Type = "unknown"
	}

	switch result {
	case 0x0: e.Status = "completed"
	case 0x1: e.Status = "aborted"
	case 0x2: e.Status = "aborted_reset"
	case 0x3: e.Status = "aborted_ns_removed"
	case 0x4: e.Status = "aborted_format"
	case 0x5: e.Status = "fatal"
	case 0x6: e.Status = "failed_unknown"
	case 0x7: e.Status = "failed_segments"
	case 0x8: e.Status = "aborted_unknown"
	case 0x9: e.Status = "aborted_sanitize"
	case 0xF: e.Status = "unused"
	default:  e.Status = "unknown"
	}

	if len(raw) >= 12 {
		e.LifetimeHours = int(uint64(raw[4]) | uint64(raw[5])<<8 | uint64(raw[6])<<16 | uint64(raw[7])<<24 |
			uint64(raw[8])<<32 | uint64(raw[9])<<40 | uint64(raw[10])<<48 | uint64(raw[11])<<56)
	}

	valid := raw[2]
	if valid&0x01 != 0 && len(raw) >= 16 {
		e.NSID = uint32(raw[12]) | uint32(raw[13])<<8 | uint32(raw[14])<<16 | uint32(raw[15])<<24
	}
	if valid&0x02 != 0 && len(raw) >= 24 {
		e.LBA = int64(uint64(raw[16]) | uint64(raw[17])<<8 | uint64(raw[18])<<16 | uint64(raw[19])<<24 |
			uint64(raw[20])<<32 | uint64(raw[21])<<40 | uint64(raw[22])<<48 | uint64(raw[23])<<56)
	}

	return e
}

func parseSCSISelfTestEntry(raw []byte) SCSISelfTestEntry {
	e := SCSISelfTestEntry{}

	code := (raw[4] >> 5) & 0x07
	result := raw[4] & 0x0F

	switch code {
	case 0: e.Type = "default"
	case 1: e.Type = "background_short"
	case 2: e.Type = "background_long"
	case 4: e.Type = "abort_background"
	case 5: e.Type = "foreground_short"
	case 6: e.Type = "foreground_long"
	default: e.Type = "unknown"
	}

	switch result {
	case 0: e.Status = "completed"
	case 1: e.Status = "aborted_by_user"
	case 2: e.Status = "aborted_reset"
	case 3: e.Status = "unknown_error"
	case 4: e.Status = "completed_segment_failed"
	case 5: e.Status = "failed_first_segment"
	case 6: e.Status = "failed_second_segment"
	case 7: e.Status = "failed_segment"
	case 15: e.Status = "in_progress"
	default: e.Status = "unknown"
	}

	if len(raw) >= 8 {
		e.LifetimeHours = int(uint16(raw[6])<<8 | uint16(raw[7]))
	}
	if len(raw) >= 16 {
		e.LBA = uint64(raw[8])<<56 | uint64(raw[9])<<48 |
			uint64(raw[10])<<40 | uint64(raw[11])<<32 |
			uint64(raw[12])<<24 | uint64(raw[13])<<16 |
			uint64(raw[14])<<8 | uint64(raw[15])
	}
	if len(raw) >= 19 {
		e.SenseKey = raw[16] & 0x0F
		e.ASC = raw[17]
		e.ASCQ = raw[18]
	}

	return e
}

func ReadErrorLog(devicePath string) (*ATAErrorLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_error_log(handle)
	if sm == nil {
		return nil, fmt.Errorf("failed to read error log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &ATAErrorLog{}
	count := int(sm.count)

	var elBufSize C.size_t
	elRawBuf := C.get_sm_buffer(sm, &elBufSize)
	if elRawBuf != nil && int(elBufSize) >= 512 {
		raw := C.GoBytes(elRawBuf, C.int(elBufSize))
		log.ChecksumValid = checksumOK(raw)
	}
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) < 90 {
			continue
		}

		entry := ATAErrorEntry{
			Error:        raw[61],
			Status:       raw[67],
			SectorCount:  raw[62],
			Device:       raw[66],
			ErrorData:    uint16(raw[61]) | uint16(raw[67])<<8,
			ExtendedData: uint16(raw[66]) | uint16(raw[62])<<8,
		}

		entry.LBA = uint64(raw[63]) | uint64(raw[64])<<8 | uint64(raw[65])<<16
		entry.LBA |= (uint64(raw[66]) & 0x0F) << 24

		entry.LifetimeHours = uint32(raw[88]) | uint32(raw[89])<<8

		log.Entries = append(log.Entries, entry)
	}

	return log, nil
}

func ReadNVMEErrorLog(devicePath string) (*NVMeErrorLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0x01), C.size_t(4096))
	if sm == nil {
		return nil, fmt.Errorf("failed to read NVMe error log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &NVMeErrorLog{}
	count := int(sm.count)
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) < 28 {
			continue
		}

		log.Entries = append(log.Entries, NVMeErrorEntry{
			ErrorCount:  uint64(raw[0]) | uint64(raw[1])<<8 | uint64(raw[2])<<16 | uint64(raw[3])<<24 |
				uint64(raw[4])<<32 | uint64(raw[5])<<40 | uint64(raw[6])<<48 | uint64(raw[7])<<56,
			SQID:        uint16(raw[8]) | uint16(raw[9])<<8,
			CommandID:   uint16(raw[10]) | uint16(raw[11])<<8,
			StatusField: uint16(raw[12]) | uint16(raw[13])<<8,
			ParamError:  uint16(raw[14]) | uint16(raw[15])<<8,
			LBA:         uint64(raw[16]) | uint64(raw[17])<<8 | uint64(raw[18])<<16 |
				uint64(raw[19])<<24 | uint64(raw[20])<<32 | uint64(raw[21])<<40 |
				uint64(raw[22])<<48 | uint64(raw[23])<<56,
			NamespaceID: uint32(raw[24]) | uint32(raw[25])<<8 | uint32(raw[26])<<16 | uint32(raw[27])<<24,
		})
	}

	return log, nil
}

func ReadSCTStatus(devicePath string) (*SCTStatus, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0xE0), C.size_t(512))
	if sm == nil {
		return nil, fmt.Errorf("failed to read SCT status from %s", devicePath)
	}
	defer C.smart_free(sm)

	status := &SCTStatus{}
	count := int(sm.count)
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

		switch cAttr.id {
		case 0:
			if len(raw) >= 2 {
				status.FormatVersion = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 1:
			if len(raw) >= 1 {
				status.DeviceState = raw[0]
			}
		case 2:
			if len(raw) >= 1 {
				status.CurrentTemp = int8(raw[0])
			}
		case 3:
			if len(raw) >= 2 {
				status.MinTempCycle = int8(raw[0])
				status.MaxTempCycle = int8(raw[1])
			}
		case 4:
			if len(raw) >= 2 {
				status.LifetimeMinTemp = int8(raw[0])
				status.LifetimeMaxTemp = int8(raw[1])
			}
		case 5:
			if len(raw) >= 4 {
				status.OverTempCount = uint32(raw[0]) | uint32(raw[1])<<8 |
					uint32(raw[2])<<16 | uint32(raw[3])<<24
			}
		case 6:
			if len(raw) >= 4 {
				status.UnderTempCount = uint32(raw[0]) | uint32(raw[1])<<8 |
					uint32(raw[2])<<16 | uint32(raw[3])<<24
			}
		case 7:
			if len(raw) >= 2 {
				sv := uint16(raw[0]) | uint16(raw[1])<<8
				status.SmartStatusPassed = (sv == 0xc24f)
			}
		case 8:
			if len(raw) >= 1 {
				status.MaxOpLimit = int8(raw[0])
			}
		case 9:
			if len(raw) >= 2 {
				status.SCTVersion = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 10:
			if len(raw) >= 2 {
				status.SCTSpec = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 11:
			if len(raw) >= 4 {
				status.StatusFlags = uint32(raw[0]) | uint32(raw[1])<<8 |
					uint32(raw[2])<<16 | uint32(raw[3])<<24
			}
		case 12:
			if len(raw) >= 2 {
				status.ExtStatusCode = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 13:
			if len(raw) >= 2 {
				status.ActionCode = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 14:
			if len(raw) >= 2 {
				status.FunctionCode = uint16(raw[0]) | uint16(raw[1])<<8
			}
		case 15:
			if len(raw) >= 8 {
				status.LBACurrent = uint64(raw[0]) | uint64(raw[1])<<8 |
					uint64(raw[2])<<16 | uint64(raw[3])<<24 |
					uint64(raw[4])<<32 | uint64(raw[5])<<40 |
					uint64(raw[6])<<48 | uint64(raw[7])<<56
			}
		case 16:
			if len(raw) >= 2 {
				status.MinERCTime = uint16(raw[0]) | uint16(raw[1])<<8
			}
		}
	}

	return status, nil
}

func ReadSCTTempHistory(devicePath string) (*SCTTempHistory, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_sct_temp_history(handle)
	if sm == nil {
		return nil, fmt.Errorf("failed to read SCT temperature history from %s", devicePath)
	}
	defer C.smart_free(sm)

	cAttr := C.get_attr_at(sm, C.int(0))
	raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

	hist := &SCTTempHistory{}
	if len(raw) >= 10 {
		hist.SamplingPeriod = uint16(raw[2]) | uint16(raw[3])<<8
		hist.Interval = uint16(raw[4]) | uint16(raw[5])<<8
		hist.MaxOpLimit = int8(raw[6])
		hist.OverLimit = int8(raw[7])
		hist.MinOpLimit = int8(raw[8])
		hist.UnderLimit = int8(raw[9])
	}

	if len(raw) >= 34 {
		hist.CBSize = uint16(raw[30]) | uint16(raw[31])<<8
		hist.CBIndex = uint16(raw[32]) | uint16(raw[33])<<8

		nSamples := int(hist.CBSize)
		if nSamples > len(raw)-34 {
			nSamples = len(raw) - 34
		}
		if nSamples > 478 {
			nSamples = 478
		}

		if nSamples > 0 && int(hist.CBIndex) < nSamples {
			hist.Samples = make([]SCTTempSample, nSamples)
			for j := 0; j < nSamples; j++ {
				idx := (int(hist.CBIndex) + 1 + j) % nSamples
				hist.Samples[j] = SCTTempSample{
					Temperature: int8(raw[34+idx]),
				}
			}
		}
	}

	return hist, nil
}

func ReadNVMeIdentifyCtrl(devicePath string) (*NVMeIdentifyCtrl, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	buf := make([]byte, 4096)

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	rc := C.smart_nvme_identify_ctrl(handle, unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
	if rc != 0 {
		return nil, fmt.Errorf("nvme identify controller failed with code %d", int(rc))
	}

	result := &NVMeIdentifyCtrl{}
	if len(buf) >= 2 {
		result.PCIVendorID = uint16(buf[0]) | uint16(buf[1])<<8
	}
	if len(buf) >= 4 {
		result.SubsystemVendorID = uint16(buf[2]) | uint16(buf[3])<<8
	}
	if len(buf) >= 24 {
		result.SerialNumber = strings.TrimRight(string(buf[4:24]), "\x00 ")
	}
	if len(buf) >= 64 {
		result.ModelNumber = strings.TrimRight(string(buf[24:64]), "\x00 ")
	}
	if len(buf) >= 72 {
		result.FirmwareRev = strings.TrimRight(string(buf[64:72]), "\x00 ")
	}
	if len(buf) >= 78 {
		result.NVMeVersion = uint32(buf[76]) | uint32(buf[77])<<8
	}
	if len(buf) >= 80 {
		result.MaxDataXferSize = buf[77]
	}
	if len(buf) >= 82 {
		result.ControllerID = uint16(buf[78]) | uint16(buf[79])<<8
	}
	if len(buf) >= 84 {
		result.NVMeVersion = uint32(buf[80]) | uint32(buf[81])<<8 |
			uint32(buf[82])<<16 | uint32(buf[83])<<24
	}
	if len(buf) >= 260 {
		result.AbortCmdLimit = buf[258]
		result.AsyncEventLimit = buf[259]
	}
	if len(buf) >= 264 {
		result.FirmwareSlots = buf[260] & 0x07
		result.ErrorLogEntries = buf[262]
		result.NumPowerStates = buf[263]
	}
	if len(buf) >= 268 {
		result.WCTemp = uint16(buf[266]) | uint16(buf[267])<<8
	}
	if len(buf) >= 270 {
		result.CCTemp = uint16(buf[268]) | uint16(buf[269])<<8
	}
	if len(buf) >= 276 {
		result.HostMemBufPreferred = uint32(buf[272]) | uint32(buf[273])<<8 |
			uint32(buf[274])<<16 | uint32(buf[275])<<24
	}
	if len(buf) >= 280 {
		result.HostMemBufMin = uint32(buf[276]) | uint32(buf[277])<<8 |
			uint32(buf[278])<<16 | uint32(buf[279])<<24
	}
	if len(buf) >= 296 {
		result.TotalCapacity = uint64(buf[280]) | uint64(buf[281])<<8 |
			uint64(buf[282])<<16 | uint64(buf[283])<<24 |
			uint64(buf[284])<<32 | uint64(buf[285])<<40 |
			uint64(buf[286])<<48 | uint64(buf[287])<<56
	}
	if len(buf) >= 312 {
		result.UnallocCapacity = uint64(buf[296]) | uint64(buf[297])<<8 |
			uint64(buf[298])<<16 | uint64(buf[299])<<24 |
			uint64(buf[300])<<32 | uint64(buf[301])<<40 |
			uint64(buf[302])<<48 | uint64(buf[303])<<56
	}
	if len(buf) >= 318 {
		result.SelfTestTimeMinutes = uint16(buf[316]) | uint16(buf[317])<<8
	}
	if len(buf) >= 320 {
		result.SelfTestOptions = buf[318]
	}
	if len(buf) >= 328 {
		result.SanitizeCaps = uint32(buf[324]) | uint32(buf[325])<<8 |
			uint32(buf[326])<<16 | uint32(buf[327])<<24
	}
	if len(buf) >= 332 {
		result.VolatileWriteCache = (buf[325] & 0x02) != 0
	}
	if len(buf) >= 328 {
		result.MNTMT = uint16(buf[324]) | uint16(buf[325])<<8
	}
	if len(buf) >= 330 {
		result.MXTMT = uint16(buf[326]) | uint16(buf[327])<<8
	}
	if len(buf) >= 524 {
		result.NumNamespaces = uint32(buf[516]) | uint32(buf[517])<<8 |
			uint32(buf[518])<<16 | uint32(buf[519])<<24
	}
	if len(buf) >= 80 {
		result.IEEE = [3]uint8{buf[73], buf[74], buf[75]}
	}

	return result, nil
}

func ReadLogDirectory(devicePath string) ([]uint8, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log_directory(handle)
	if sm == nil {
		return nil, fmt.Errorf("failed to read log directory from %s", devicePath)
	}
	defer C.smart_free(sm)

	cAttr := C.get_attr_at(sm, C.int(0))
	raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
	if len(raw) < 2 {
		return nil, fmt.Errorf("log directory too short")
	}

	nEntries := int(uint16(raw[0]) | uint16(raw[1])<<8)
	if nEntries > len(raw)-2 {
		nEntries = len(raw) - 2
	}
	if nEntries > 255 {
		nEntries = 255
	}

	dir := make([]uint8, nEntries)
	copy(dir, raw[2:2+nEntries])

	return dir, nil
}

func ReadExtendedErrorLog(devicePath string) (*ATAErrorLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0x03), C.size_t(512))
	if sm == nil {
		return nil, fmt.Errorf("failed to read extended error log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &ATAErrorLog{}
	count := int(sm.count)

	var eelBufSize C.size_t
	eelRawBuf := C.get_sm_buffer(sm, &eelBufSize)
	if eelRawBuf != nil && int(eelBufSize) >= 512 {
		raw := C.GoBytes(eelRawBuf, C.int(eelBufSize))
		log.ChecksumValid = checksumOK(raw)
	}
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) < 90 {
			continue
		}

		entry := ATAErrorEntry{
			Error:        raw[61],
			Status:       raw[67],
			SectorCount:  raw[62],
			Device:       raw[66],
			ErrorData:    uint16(raw[61]) | uint16(raw[67])<<8,
			ExtendedData: uint16(raw[66]) | uint16(raw[62])<<8,
		}
		entry.LBA = uint64(raw[63]) | uint64(raw[64])<<8 | uint64(raw[65])<<16
		entry.LBA |= (uint64(raw[66]) & 0x0F) << 24
		entry.LifetimeHours = uint32(raw[88]) | uint32(raw[89])<<8

		log.Entries = append(log.Entries, entry)
	}

	return log, nil
}

func ReadExtendedSelfTestLog(devicePath string) (*SelfTestLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0x07), C.size_t(564))
	if sm == nil {
		return nil, fmt.Errorf("failed to read extended self-test log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &SelfTestLog{}
	count := int(sm.count)

	var estBufSize C.size_t
	estRawBuf := C.get_sm_buffer(sm, &estBufSize)
	if estRawBuf != nil && int(estBufSize) >= 512 {
		raw := C.GoBytes(estRawBuf, C.int(estBufSize))
		log.ChecksumValid = checksumOK(raw)
	}
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) == 0 {
			continue
		}

		entry := SelfTestEntry{}
		page := uint32(cAttr.page)

		if page == 0x07 && (len(raw) == 24 || len(raw) == 26) {
			entry = parseATASelfTestEntry(raw)
		} else if page == 0x07 && len(raw) == 28 {
			entry = parseNVMESelfTestEntry(raw)
		}

		if entry.Type != "" {
			log.Entries = append(log.Entries, entry)
		}
	}

	return log, nil
}

func ReadDeviceStatistics(devicePath string) ([]Attribute, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_gpl_log(handle, C.uint8_t(0x04), C.uint8_t(0x00), C.size_t(512))
	if sm == nil {
		return nil, fmt.Errorf("failed to read device statistics from %s", devicePath)
	}
	defer C.smart_free(sm)

	cAttr := C.get_attr_at(sm, C.int(0))
	raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

	var attrs []Attribute
	offset := 0
	for offset+8 <= len(raw) {
		logAddr := raw[offset]
		pageNum := raw[offset+1]
		offset += 8

		if logAddr == 0x00 || offset >= len(raw) {
			break
		}

		pageSM := C.smart_read_gpl_log(handle, C.uint8_t(logAddr), C.uint8_t(pageNum), C.size_t(512))
		if pageSM == nil {
			continue
		}
		pageRaw := C.GoBytes(C.get_attr_at(pageSM, C.int(0)).raw, C.int(512))
		C.smart_free(pageSM)

		if len(pageRaw) < 8 {
			continue
		}

		nEntries := int(uint16(pageRaw[6]) | uint16(pageRaw[7])<<8)
		for j := 0; j < nEntries && 8+(j*12)+12 <= len(pageRaw); j++ {
			entry := pageRaw[8+(j*12) : 8+((j+1)*12)]
			attr := Attribute{
				Page:      uint32(logAddr),
				ID:        uint32(j),
				Name:      fmt.Sprintf("devstat_%02x_%02x_%d", logAddr, pageNum, j),
				RawBytes:  entry,
				RawValue:  bytesToUint64(entry[4:10], false),
				Threshold: int(entry[2]),
			}
			if int(entry[0])&0x80 != 0 {
				attr.Value = int(entry[0])
			}
			attrs = append(attrs, attr)
		}
	}

	return attrs, nil
}

func SetSCTFeatureControl(devicePath string, featureCode uint16, state uint16, persistent bool) error {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	cmd := make([]byte, 512)
	cmd[0] = 4
	cmd[1] = 0
	cmd[2] = 1
	cmd[3] = 0
	cmd[4] = byte(featureCode)
	cmd[5] = byte(featureCode >> 8)
	cmd[6] = byte(state)
	cmd[7] = byte(state >> 8)
	if persistent {
		cmd[8] = 0x01
	}

	rc := C.smart_write_smart_log(handle, C.uint8_t(0xE0), unsafe.Pointer(&cmd[0]), C.size_t(512))
	if rc != 0 {
		return fmt.Errorf("SCT feature control set failed with code %d", int(rc))
	}

	return nil
}

func ReadSelectiveSelfTestLog(devicePath string) (*SelfTestLog, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	sm := C.smart_read_log(handle, C.uint8_t(0x09), C.size_t(512))
	if sm == nil {
		return nil, fmt.Errorf("failed to read selective self-test log from %s", devicePath)
	}
	defer C.smart_free(sm)

	log := &SelfTestLog{}
	count := int(sm.count)
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		if len(raw) == 0 {
			continue
		}

		entry := SelfTestEntry{}
		page := uint32(cAttr.page)

		if page == 0x09 && len(raw) == 24 {
			entry = parseATASelfTestEntry(raw)
		} else if page == 0x09 && len(raw) == 28 {
			entry = parseNVMESelfTestEntry(raw)
		}

		if entry.Type != "" {
			log.Entries = append(log.Entries, entry)
		}
	}

	return log, nil
}

func SetSCTErrorRecoveryControl(devicePath string, read bool, timeLimit uint16) error {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return fmt.Errorf("failed to open device %s", devicePath)
	}
	defer C.smart_close(handle)

	cmd := make([]byte, 512)
	cmd[0] = 3
	cmd[1] = 0
	cmd[2] = 1
	cmd[3] = 0
	if read {
		cmd[4] = 1
		cmd[5] = 0
	} else {
		cmd[4] = 2
		cmd[5] = 0
	}
	cmd[6] = byte(timeLimit)
	cmd[7] = byte(timeLimit >> 8)

	rc := C.smart_write_smart_log(handle, C.uint8_t(0xE0), unsafe.Pointer(&cmd[0]), C.size_t(512))
	if rc != 0 {
		return fmt.Errorf("SCT error recovery control set failed with code %d", int(rc))
	}

	return nil
}
