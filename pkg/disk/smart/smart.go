// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

/*
#cgo CFLAGS: -I.
#cgo LDFLAGS: -lcam
#include <stdlib.h>
#include <stdbool.h>
#include "libsmart.h"

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
*/
import "C"
import (
	"fmt"
	"strings"
	"unsafe"
)

type Attribute struct {
	ID        uint32
	Name      string
	Value     int
	Worst     int
	Threshold int
	RawValue  uint64
	RawBytes  []byte
	TextValue string
	IsText    bool
}

type DeviceInfo struct {
	Device          string
	Protocol        string
	Temperature     int
	PowerOnHours    int
	PowerCycleCount int
	Attributes      []Attribute
}

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
		return nil, fmt.Errorf("failed to read SMART data from %s", devicePath)
	}
	defer C.smart_free(sm)

	info := &DeviceInfo{
		Device:     strings.TrimPrefix(devicePath, "/dev/"),
		Attributes: make([]Attribute, 0, int(sm.count)),
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
			ID:        uint32(cAttr.id),
			Name:      C.GoString(cAttr.description),
			Threshold: thresh,
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
			}
		}

		info.Attributes = append(info.Attributes, attr)

		// Populate convenience fields
		switch attr.ID {
		case 9:
			info.PowerOnHours = int(attr.RawValue)
		case 12:
			info.PowerCycleCount = int(attr.RawValue)
		case 194:
			info.Temperature = int(attr.RawValue & 0xFF)
		}
	}

	return info, nil
}

func bytesToUint64(b []byte, bigEndian bool) uint64 {
	var res uint64
	length := len(b)

	if length >= 12 {
		start := 5
		for i := 0; i < 6; i++ {
			var val uint64
			if bigEndian {
				val = uint64(b[start+i])
				res = (res << 8) | val
			} else {
				val = uint64(b[start+i])
				res |= val << (8 * i)
			}
		}
		return res
	}

	for i := 0; i < length; i++ {
		var val uint64
		if bigEndian {
			// Big Endian: First byte is MSB
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
