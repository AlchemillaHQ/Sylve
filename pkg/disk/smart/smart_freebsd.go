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
#include <errno.h>
#include "libsmart.h"
#include "libsmart_priv.h"

bool do_debug = false;

int smart_get_last_err(smart_h h);

static smart_attr_t get_attr_at(smart_map_t *map, int i) {
    return map->attr[i];
}

static int get_protocol(smart_map_t *map) {
    if (!map || !map->sb) return -1;
    return (int)map->sb->protocol;
}

static int get_threshold_for_id(smart_attr_t *attr, uint32_t target_id) {
    if (!attr || !attr->thresh) return -1;

    struct smart_map_s *thresh_map = attr->thresh;

    for (uint32_t i = 0; i < thresh_map->count; i++) {
        smart_attr_t t_attr = thresh_map->attr[i];

        if (t_attr.id == target_id) {
            if (t_attr.raw && t_attr.bytes >= 2) {
                unsigned char *b = (unsigned char *)t_attr.raw;
                return (int)b[1];
            }
        }
    }
    return -1;
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

static const char * get_device_vendor(smart_h h) {
    smart_t *s = (smart_t *)h;
    if (!s) return "";
    return s->info.vendor;
}

static const char * get_device_serial(smart_h h) {
    smart_t *s = (smart_t *)h;
    if (!s) return "";
    return s->info.serial;
}

static const char * get_device_rev(smart_h h) {
    smart_t *s = (smart_t *)h;
    if (!s) return "";
    return s->info.rev;
}

static smart_protocol_e get_device_proto(smart_h h) {
    smart_t *s = (smart_t *)h;
    if (!s) return SMART_PROTO_MAX;
    return s->protocol;
}

static bool get_device_sct_supported(smart_h h) {
    smart_t *s = (smart_t *)h;
    return s && s->info.sct_supported;
}

static bool get_device_self_test_supported(smart_h h) {
    smart_t *s = (smart_t *)h;
    return s && s->info.self_test_supported;
}

static uint32_t get_device_nvme_nsid(smart_h h) {
    smart_t *s = (smart_t *)h;
    return s ? s->info.nvme_nsid : 0;
}

static uint64_t get_device_sector_count(smart_h h) {
    smart_t *s = (smart_t *)h;
    return s ? s->info.sector_count : 0;
}

static void * get_scsi_page_buffer(smart_h h, smart_map_t *sm, uint32_t page, size_t *size) {
    smart_t *s = (smart_t *)h;
    size_t offset = 0;
    unsigned char *buf;
    if (size) *size = 0;
    if (!s || !sm || !sm->sb || !sm->sb->b || !s->pg_list || !size)
        return NULL;
    if (s->protocol != SMART_PROTO_SCSI)
        return NULL;
    buf = (unsigned char *)sm->sb->b;
    for (uint32_t i = 0; i < s->pg_list->pg_count; i++) {
        size_t allocated = s->pg_list->pages[i].bytes;
        if (offset > sm->sb->bsize || allocated > sm->sb->bsize - offset)
            return NULL;
        if (s->pg_list->pages[i].id == page) {
            size_t actual;
            if (allocated < 4)
                return NULL;
            if ((buf[offset] & 0x3f) != page)
                return NULL;
            actual = 4 + ((size_t)buf[offset + 2] << 8) + buf[offset + 3];
            if (actual > allocated)
                return NULL;
            *size = actual;
            return buf + offset;
        }
        offset += allocated;
    }
    return NULL;
}
*/
import "C"
import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"unsafe"
)

var ErrControllerTimeout = errors.New("controller timeout")
var ErrControllerAborted = errors.New("controller aborted")
var ErrDeviceClosed = errors.New("SMART device is closed")
var ErrUnsupportedFeature = errors.New("SMART feature is not supported by this device")

func IsControllerError(err error) bool {
	return errors.Is(err, ErrControllerTimeout) || errors.Is(err, ErrControllerAborted)
}

func wrapDeviceError(h C.smart_h, err error, devicePath string) error {
	if err == nil {
		return nil
	}
	lastErr := int(C.smart_get_last_err(h))
	switch lastErr {
	case int(C.ETIMEDOUT):
		return fmt.Errorf("%w: %s: %v", ErrControllerTimeout, devicePath, err)
	case int(C.ECONNABORTED):
		return fmt.Errorf("%w: %s: %v", ErrControllerAborted, devicePath, err)
	}
	return err
}

func scsiPageBytes(h C.smart_h, sm *C.smart_map_t, page uint32) []byte {
	var size C.size_t
	buf := C.get_scsi_page_buffer(h, sm, C.uint32_t(page), &size)
	if buf == nil || size == 0 {
		return nil
	}
	return C.GoBytes(buf, C.int(size))
}

func readSCSIHealthLocked(h C.smart_h, devicePath string, info scsiInformationalException, valid bool) (bool, bool, error) {
	if !scsiHealthNeedsRequestSense(info, valid) {
		known, passed := scsiHealthFromCode(info.ASC, info.ASCQ)
		return known, passed, nil
	}
	raw := make([]byte, 252)
	if rc := C.smart_scsi_request_sense(h, unsafe.Pointer(&raw[0]), C.size_t(len(raw))); rc != 0 {
		return false, false, wrapDeviceError(h, fmt.Errorf("SCSI request sense failed on %s with code %d", devicePath, int(rc)), devicePath)
	}
	asc, ascq, ok := parseSCSISenseCode(raw)
	if !ok {
		return false, false, fmt.Errorf("invalid SCSI request sense response from %s", devicePath)
	}
	known, passed := scsiHealthFromCode(asc, ascq)
	return known, passed, nil
}

type Device struct {
	mu                sync.Mutex
	h                 C.smart_h
	device            string
	selfTestMu        *sync.Mutex
	selfTestCaps      SelfTestCapabilities
	selfTestCapsKnown bool
	gplDirectory      map[uint8]uint16
	driveMatch        DriveMatch
	driveMatched      bool
}

var selfTestDeviceLocks sync.Map
var selfTestCapabilityCache sync.Map

func selfTestDeviceLock(device string) *sync.Mutex {
	lock, _ := selfTestDeviceLocks.LoadOrStore(device, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func selfTestCapabilityKey(h C.smart_h, devicePath string) string {
	serial := C.GoString(C.get_device_serial(h))
	if serial == "" {
		serial = devicePath
	}
	return protocolName(C.get_device_proto(h)) + "\x00" + C.GoString(C.get_device_model(h)) + "\x00" + serial + "\x00" + C.GoString(C.get_device_rev(h))
}

func (d *Device) lockHandle() (C.smart_h, string, error) {
	if d == nil {
		return nil, "", ErrDeviceClosed
	}
	d.mu.Lock()
	if d.h == nil {
		d.mu.Unlock()
		return nil, "/dev/" + d.device, ErrDeviceClosed
	}
	return d.h, "/dev/" + d.device, nil
}

func OpenDevice(devicePath string) (*Device, error) {
	if !strings.HasPrefix(devicePath, "/dev/") {
		devicePath = "/dev/" + devicePath
	}

	cPath := C.CString(devicePath)
	defer C.free(unsafe.Pointer(cPath))

	handle := C.smart_open(C.SMART_PROTO_AUTO, cPath)
	if handle == nil {
		return nil, fmt.Errorf("failed to open device %s", devicePath)
	}

	match, matched := LookupDrive(
		C.GoString(C.get_device_model(handle)),
		C.GoString(C.get_device_rev(handle)),
	)
	return &Device{
		h:            handle,
		device:       strings.TrimPrefix(devicePath, "/dev/"),
		selfTestMu:   selfTestDeviceLock(devicePath),
		driveMatch:   match,
		driveMatched: matched,
	}, nil
}

func (d *Device) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.h != nil {
		C.smart_close(d.h)
		d.h = nil
		d.selfTestCapsKnown = false
		d.gplDirectory = nil
	}
}

func (d *Device) Supported() (bool, error) {
	h, _, err := d.lockHandle()
	if err != nil {
		return false, err
	}
	defer d.mu.Unlock()
	return bool(C.smart_supported(h)), nil
}

func Supported(devicePath string) (bool, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return false, err
	}
	defer d.Close()
	return d.Supported()
}

func (d *Device) Enable() error {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	if rc := C.smart_enable(h); rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("failed to enable SMART on %s: code %d", devicePath, int(rc)), devicePath)
	}
	return nil
}

func Enable(devicePath string) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Enable()
}

func (d *Device) Read() (*DeviceInfo, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	sm := C.smart_read(h)
	if sm == nil {
		lastErr := int(C.smart_get_last_err(h))
		switch lastErr {
		case int(C.ETIMEDOUT):
			return nil, fmt.Errorf("%w: %s", ErrControllerTimeout, devicePath)
		case int(C.ECONNABORTED):
			return nil, fmt.Errorf("%w: %s", ErrControllerAborted, devicePath)
		}
		return nil, fmt.Errorf("failed to read SMART data from %s", devicePath)
	}
	defer C.smart_free(sm)

	model := C.GoString(C.get_device_model(h))
	rev := C.GoString(C.get_device_rev(h))
	modelAttrs := d.driveMatch.AttrOverrides
	if !d.driveMatched {
		modelAttrs = map[uint32]AttrDef{}
	}

	info := &DeviceInfo{
		Device:         d.device,
		Vendor:         C.GoString(C.get_device_vendor(h)),
		Model:          model,
		Serial:         C.GoString(C.get_device_serial(h)),
		Firmware:       rev,
		ModelFamily:    d.driveMatch.Family,
		DriveDBWarning: d.driveMatch.Warning,
		FirmwareBugs:   d.driveMatch.FirmwareBugs,
		SCTSupported:   bool(C.get_device_sct_supported(h)),
		SectorCount:    uint64(C.get_device_sector_count(h)),
		Attributes:     make([]Attribute, 0, int(sm.count)),
	}

	protoVal := C.get_protocol(sm)
	isATA := protoVal == C.SMART_PROTO_ATA

	if isATA {
		info.ChecksumValid = C.check_ata_smart_checksum(sm) != 0
	} else {
		info.ChecksumValid = true
	}

	switch protoVal {
	case C.SMART_PROTO_ATA:
		info.Protocol = "ATA"
	case C.SMART_PROTO_SCSI:
		info.Protocol = "SCSI"
	case C.SMART_PROTO_NVME:
		info.Protocol = "NVMe"
	default:
		info.Protocol = "Unknown"
	}

	var scsiInfo scsiInformationalException
	var scsiInfoValid bool
	var scsiSelfTestRaw []byte
	var scsiSolidStateRaw []byte
	var scsiBackgroundScanRaw []byte
	if protoVal == C.SMART_PROTO_SCSI {
		scsiInfo, scsiInfoValid = parseSCSIInformationalException(scsiPageBytes(h, sm, 0x2f))
		scsiSelfTestRaw = scsiPageBytes(h, sm, 0x10)
		scsiSolidStateRaw = scsiPageBytes(h, sm, 0x11)
		scsiBackgroundScanRaw = scsiPageBytes(h, sm, 0x15)
	}

	count := int(sm.count)
	scsiTemperatureKnown := false
	for i := 0; i < count; i++ {
		cAttr := C.get_attr_at(sm, C.int(i))
		thresh := int(C.get_threshold_for_id(&cAttr, cAttr.id))
		if thresh < 0 {
			thresh = -1
		}
		var def AttrDef
		var hasDef bool

		attr := Attribute{
			Page:      uint32(cAttr.page),
			ID:        uint32(cAttr.id),
			Name:      C.GoString(cAttr.description),
			Threshold: thresh,
		}
		if protoVal == C.SMART_PROTO_SCSI {
			switch attr.Page {
			case 0x10, 0x11, 0x15, 0x18, 0x2f:
				continue
			}
		}

		if isATA {
			if def, hasDef = modelAttrs[uint32(cAttr.id)]; hasDef {
				if def.Name != "" {
					attr.Name = def.Name
				}
			} else if attr.ID == 254 || attr.ID == 255 {
			} else if def, hasDef = LookupAttrDef(attr.ID); hasDef {
				if def.Name != "" {
					attr.Name = def.Name
				}
			}
		}

		if cAttr.raw != nil && cAttr.bytes > 0 {
			attr.RawBytes = C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

			isBigEndian := (uint32(cAttr.flags) & 0x01) != 0
			attr.IsText = (uint32(cAttr.flags) & 0x02) != 0

			if attr.IsText {
				attr.TextValue = string(attr.RawBytes)
			} else if isATA && len(attr.RawBytes) >= 11 {
				attr.RawValue = parseRawValue(attr.RawBytes, def, isBigEndian)
			} else {
				attr.RawValue = bytesToUint64(attr.RawBytes, isBigEndian)
				if len(attr.RawBytes) > 8 {
					attr.RawString = bytesToDecimalString(attr.RawBytes, isBigEndian)
				}
			}

			if isATA && len(attr.RawBytes) >= 12 {
				attr.Value = int(attr.RawBytes[3])
				attr.Worst = int(attr.RawBytes[4])

				f := uint16(attr.RawBytes[1]) | uint16(attr.RawBytes[2])<<8
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

		if attr.Page == 0xDA && attr.ID == 0 && attr.RawValue <= 1 {
			info.HealthKnown = true
			info.Passed = attr.RawValue == 0
		}

		info.Attributes = append(info.Attributes, attr)

		if !isATA {
			if attr.Page == 0x0D && attr.ID == 0 {
				if attr.RawValue != 0xff {
					info.Temperature = int(attr.RawValue)
					scsiTemperatureKnown = true
				}
			}
			if attr.Page == 0x02 && attr.ID == 0 && info.Protocol == "NVMe" {
				info.HealthKnown = true
				info.Passed = attr.RawValue == 0
			}
			if attr.Page == 0x02 && attr.ID == 1 && info.Protocol == "NVMe" {
				t := int(attr.RawValue)
				if t > 0 {
					info.Temperature = t - 273
				}
			}
			if attr.Page == 0x0E && attr.ID == 4 {
				info.PowerCycleCount = int(attr.RawValue)
			}
			if attr.Page == 0x02 && attr.ID == 112 && info.Protocol == "NVMe" {
				info.PowerCycleCount = int(attr.RawValue)
			}
			if attr.Page == 0x02 && attr.ID == 128 && info.Protocol == "NVMe" {
				info.PowerOnHours = int(attr.RawValue)
			}
			continue
		}

		switch attr.ID {
		case 9:
			if hours, ok := ataPowerOnHours(attr.RawValue, def); ok {
				info.PowerOnHours = hours
			}
		case 12:
			info.PowerCycleCount = int(attr.RawValue)
		case 254:
			info.SmartCapability = attr.RawValue
		case 255:
			info.SelfTestStatus = DecodeSelfTestExecStatus(attr.RawValue)
			if d.driveMatch.FirmwareBugs&FirmwareBugSamsung3 != 0 && uint8(attr.RawValue) == 0xf0 {
				info.SelfTestStatus.Status = "ambiguous_completed_or_in_progress"
				info.SelfTestStatus.RemainingPct = -1
			}
		}
	}

	if isATA {
		if temp, ok := findTemperature(info.Attributes, modelAttrs); ok {
			info.Temperature = temp
		}
	}

	if info.Protocol == "SCSI" {
		known, passed, healthErr := readSCSIHealthLocked(h, devicePath, scsiInfo, scsiInfoValid)
		if healthErr != nil {
			return nil, healthErr
		}
		info.HealthKnown = known
		info.Passed = passed
		if !scsiTemperatureKnown && scsiInfoValid && scsiInfo.CurrentTemperatureKnown {
			info.Temperature = int(scsiInfo.CurrentTemperature)
		}
		if minutes, ok := parseSCSIBackgroundScanPowerOnMinutes(scsiBackgroundScanRaw); ok {
			info.PowerOnHours = int(minutes / 60)
		}
		if pct, ok := parseSCSILogParamUint64(scsiSolidStateRaw, 0x0001); ok {
			used := int(pct)
			if used > 100 {
				used = 100
			}
			info.Attributes = append(info.Attributes, Attribute{
				Page:     0x11,
				ID:       1,
				Name:     "Percentage Used Endurance Indicator",
				Value:    100 - used,
				RawValue: pct,
			})
		}
		if len(scsiSelfTestRaw) != 0 {
			selfTestLog := parseSCSISelfTestLog(scsiSelfTestRaw)
			info.SCSISelfTestLog = &selfTestLog
			if len(selfTestLog.Entries) != 0 {
				info.SCSISelfTestResults = make([]SCSISelfTestEntry, len(selfTestLog.Entries))
				for i, entry := range selfTestLog.Entries {
					info.SCSISelfTestResults[i] = SCSISelfTestEntry{
						Type:          entry.Type,
						Mode:          entry.Mode,
						Status:        entry.Status,
						LifetimeHours: entry.LifetimeHours,
						LBA:           entry.LBA,
						LBAValid:      entry.LBAValid,
						SenseKey:      entry.SenseKey,
						ASC:           entry.ASC,
						ASCQ:          entry.ASCQ,
						SegmentNumber: entry.SegmentNum,
					}
				}
			}
		}
	}

	return info, nil
}

func (d *Device) ReadSelfTestLog() (*SelfTestLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	return d.readSelfTestLogLocked(h, devicePath)
}

func (d *Device) readSelfTestLogLocked(h C.smart_h, devicePath string) (*SelfTestLog, error) {
	proto := C.get_device_proto(h)
	size := 0
	switch proto {
	case C.SMART_PROTO_ATA:
		size = 512
	case C.SMART_PROTO_SCSI:
		size = 0x194
	case C.SMART_PROTO_NVME:
		size = 564
	default:
		return nil, fmt.Errorf("%w: self-test log on %s", ErrUnsupportedFeature, devicePath)
	}
	raw := make([]byte, size)
	if rc := C.smart_read_self_test_log(h, unsafe.Pointer(&raw[0]), C.size_t(len(raw))); rc != 0 {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read self-test log from %s: code %d", devicePath, int(rc)), devicePath)
	}
	switch proto {
	case C.SMART_PROTO_ATA:
		log := parseATAStandardSelfTestLogWithBugs(raw, d.driveMatch.FirmwareBugs)
		return &log, nil
	case C.SMART_PROTO_SCSI:
		log := parseSCSISelfTestLog(raw)
		return &log, nil
	default:
		log := parseNVMESelfTestLog(raw)
		return &log, nil
	}
}

func findTemperature(attrs []Attribute, modelAttrs map[uint32]AttrDef) (int, bool) {
	ids := []int{194, 190, 9, 220}
	for _, id := range ids {
		for _, attr := range attrs {
			if int(attr.ID) != id {
				continue
			}

			def, hasDef := modelAttrs[uint32(id)]
			if !hasDef {
				def, hasDef = LookupAttrDef(attr.ID)
			}
			format := ""
			if hasDef {
				format = def.Format
			}
			base := format
			if idx := strings.IndexByte(base, ':'); idx >= 0 {
				base = base[:idx]
			}

			switch {
			case id == 194 || id == 190:
				if base != "" && base != "raw48" && base != "tempminmax" && base != "temp10x" {
					continue
				}
			case base != "tempminmax" && base != "temp10x":
				continue
			}

			t := int(attr.RawValue & 0xFF)
			if t <= 0 || t >= 128 {
				continue
			}
			return t, true
		}
	}
	return 0, false
}

func parseSCSILogParamUint64(raw []byte, targetCode uint16) (uint64, bool) {
	data, ok := findSCSILogParam(raw, targetCode)
	if !ok || len(data) > 8 {
		return 0, false
	}
	var val uint64
	for _, b := range data {
		val = (val << 8) | uint64(b)
	}
	return val, true
}

func parseSCSIBackgroundScanPowerOnMinutes(raw []byte) (uint64, bool) {
	data, ok := findSCSILogParam(raw, 0)
	if !ok || len(data) < 4 {
		return 0, false
	}
	return uint64(binary.BigEndian.Uint32(data[:4])), true
}

func findSCSILogParam(raw []byte, targetCode uint16) ([]byte, bool) {
	if len(raw) < 4 {
		return nil, false
	}
	pageLen := int(binary.BigEndian.Uint16(raw[2:4]))
	end := 4 + pageLen
	if pageLen <= 0 || end > len(raw) {
		end = len(raw)
	}
	offset := 4
	for offset+4 <= end {
		code := binary.BigEndian.Uint16(raw[offset : offset+2])
		paramLen := int(raw[offset+3])
		dataEnd := offset + 4 + paramLen
		if dataEnd > end {
			break
		}
		if code == targetCode {
			return raw[offset+4 : dataEnd], true
		}
		offset = dataEnd
	}
	return nil, false
}

func bytesToUint64(b []byte, bigEndian bool) uint64 {
	var res uint64
	n := len(b)
	if n > 8 {
		if bigEndian {
			b = b[n-8:]
		}
		n = 8
	}
	for i := 0; i < n; i++ {
		if bigEndian {
			res = (res << 8) | uint64(b[i])
		} else {
			res |= uint64(b[i]) << (8 * i)
		}
	}
	return res
}

func bytesToDecimalString(b []byte, bigEndian bool) string {
	if len(b) == 0 {
		return "0"
	}
	if bigEndian {
		return new(big.Int).SetBytes(b).String()
	}

	be := make([]byte, len(b))
	for i := range b {
		be[len(b)-1-i] = b[i]
	}
	return new(big.Int).SetBytes(be).String()
}

func attrRawByte(raw []byte, code byte) byte {
	switch {
	case code >= '0' && code <= '5':
		idx := 5 + int(code-'0')
		if idx >= 0 && idx < len(raw) {
			return raw[idx]
		}
	case code == 'v':
		if len(raw) > 3 {
			return raw[3]
		}
	case code == 'w':
		if len(raw) > 4 {
			return raw[4]
		}
	case code == 'r':
		if len(raw) > 11 {
			return raw[11]
		}
	case code == 'z':
		return 0
	}
	return 0
}

func rawValueFromOrder(raw []byte, order string) uint64 {
	var v uint64
	for i := 0; i < len(order) && i < 8; i++ {
		v = (v << 8) | uint64(attrRawByte(raw, order[i]))
	}
	return v
}

func parseRawValue(raw []byte, def AttrDef, bigEndian bool) uint64 {
	format := def.Format
	if format == "" {
		format = "raw48"
	}

	base := format
	order := ""
	if idx := strings.IndexByte(format, ':'); idx >= 0 {
		base = format[:idx]
		order = format[idx+1:]
	}

	if order != "" {
		return rawValueFromOrder(raw, order)
	}

	switch base {
	case "raw64":
		return rawValueFromOrder(raw, "543210wv")
	case "hex64":
		return rawValueFromOrder(raw, "543210wv")
	case "raw56":
		return rawValueFromOrder(raw, "r543210")
	case "hex56", "raw24/raw32", "raw24_div_raw32", "msec24hour32", "msec24_hour32":
		return rawValueFromOrder(raw, "r543210")
	case "tempminmax":
		return uint64(attrRawByte(raw, '0'))
	case "temp10x":
		return (rawValueFromOrder(raw, "543210") + 5) / 10
	case "sec2hour":
		return rawValueFromOrder(raw, "543210") / 3600
	case "min2hour":
		return rawValueFromOrder(raw, "543210") / 60
	case "halfmin2hour":
		return rawValueFromOrder(raw, "543210") / 120
	case "raw8":
		return uint64(attrRawByte(raw, '0'))
	case "raw16", "raw16(raw16)", "raw16(avg16)":
		return rawValueFromOrder(raw, "10")
	case "raw24(raw8)":
		return rawValueFromOrder(raw, "210")
	case "raw24/raw24":
		return rawValueFromOrder(raw, "210")
	case "hex48", "raw48":
		return rawValueFromOrder(raw, "543210")
	}
	if len(raw) >= 11 {
		return rawValueFromOrder(raw, "543210")
	}
	return bytesToUint64(raw, bigEndian)
}

func ataPowerOnHours(raw uint64, def AttrDef) (int, bool) {
	base := def.Format
	if idx := strings.IndexByte(base, ':'); idx >= 0 {
		base = base[:idx]
	}
	if base == "msec24hour32" || base == "msec24_hour32" {
		raw &= 0xffffffff
	}
	if raw > 0x00ffffff {
		return 0, false
	}
	return int(raw), true
}

func protocolName(proto C.smart_protocol_e) string {
	switch proto {
	case C.SMART_PROTO_ATA:
		return "ATA"
	case C.SMART_PROTO_SCSI:
		return "SCSI"
	case C.SMART_PROTO_NVME:
		return "NVMe"
	default:
		return "Unknown"
	}
}

func readATASelfTestDataLocked(h C.smart_h, devicePath string) ([]byte, error) {
	raw := make([]byte, 512)
	if rc := C.smart_read_ata_data(h, unsafe.Pointer(&raw[0]), C.size_t(len(raw))); rc != 0 {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read ATA self-test data from %s: code %d", devicePath, int(rc)), devicePath)
	}
	return raw, nil
}

func (d *Device) selfTestCapabilitiesLocked(h C.smart_h, devicePath string, ataRaw []byte) (SelfTestCapabilities, error) {
	if d.selfTestCapsKnown {
		return d.selfTestCaps, nil
	}
	cacheKey := selfTestCapabilityKey(h, devicePath)
	if cached, ok := selfTestCapabilityCache.Load(cacheKey); ok {
		d.selfTestCaps = cached.(SelfTestCapabilities)
		d.selfTestCapsKnown = true
		return d.selfTestCaps, nil
	}
	proto := C.get_device_proto(h)
	var capabilities SelfTestCapabilities
	switch proto {
	case C.SMART_PROTO_ATA:
		if ataRaw == nil {
			var err error
			ataRaw, err = readATASelfTestDataLocked(h, devicePath)
			if err != nil {
				return capabilities, err
			}
		}
		capabilities = parseATASelfTestCapabilities(ataRaw, bool(C.get_device_self_test_supported(h)))
	case C.SMART_PROTO_SCSI:
		capabilities.Protocol = "SCSI"
		capabilities.Scope = "device"
		capabilities.Supported = bool(C.smart_log_page_supported(h, C.uint32_t(0x10)))
		if !capabilities.Supported {
			switch int(C.smart_get_last_err(h)) {
			case int(C.ETIMEDOUT):
				return capabilities, fmt.Errorf("%w: %s", ErrControllerTimeout, devicePath)
			case int(C.ECONNABORTED):
				return capabilities, fmt.Errorf("%w: %s", ErrControllerAborted, devicePath)
			}
		}
		if capabilities.Supported {
			capabilities.Default = true
			capabilities.Short = true
			capabilities.Extended = true
			capabilities.ShortCaptive = true
			capabilities.ExtendedCaptive = true
			capabilities.Abort = true
			capabilities.ResultLog = true
			capabilities.Progress = true
			raw := make([]byte, 64)
			rc := C.smart_scsi_control_mode_page(h, unsafe.Pointer(&raw[0]), C.size_t(len(raw)))
			if rc == 0 {
				if minutes, ok := parseSCSIControlSelfTestMinutes(raw); ok {
					capabilities.ExtendedDurationMinutes = minutes
				} else if scsiControlNeedsExtendedInquiry(raw) {
					inquiry := make([]byte, 64)
					if inquiryRC := C.smart_scsi_extended_inquiry(h, unsafe.Pointer(&inquiry[0]), C.size_t(len(inquiry))); inquiryRC == 0 {
						if minutes, ok := parseSCSIExtendedInquirySelfTestMinutes(inquiry); ok {
							capabilities.ExtendedDurationMinutes = minutes
						}
					} else if int(inquiryRC) == int(C.ETIMEDOUT) || int(inquiryRC) == int(C.ECONNABORTED) {
						return capabilities, wrapDeviceError(h, fmt.Errorf("failed to read SCSI extended self-test capabilities from %s: code %d", devicePath, int(inquiryRC)), devicePath)
					}
				}
			} else if int(rc) == int(C.ETIMEDOUT) || int(rc) == int(C.ECONNABORTED) {
				return capabilities, wrapDeviceError(h, fmt.Errorf("failed to read SCSI self-test capabilities from %s: code %d", devicePath, int(rc)), devicePath)
			}
		}
	case C.SMART_PROTO_NVME:
		buf := make([]byte, 4096)
		if rc := C.smart_nvme_identify_ctrl(h, unsafe.Pointer(&buf[0]), C.size_t(len(buf))); rc != 0 {
			return capabilities, wrapDeviceError(h, fmt.Errorf("NVMe identify controller failed with code %d", int(rc)), devicePath)
		}
		ctrl := parseNVMeIdentifyCtrl(buf)
		ctrl.NamespaceID = uint32(C.get_device_nvme_nsid(h))
		capabilities = parseNVMeSelfTestCapabilities(ctrl)
	default:
		capabilities.Protocol = "Unknown"
	}
	d.selfTestCaps = capabilities
	d.selfTestCapsKnown = true
	selfTestCapabilityCache.Store(cacheKey, capabilities)
	return capabilities, nil
}

func (d *Device) SelfTestCapabilities() (*SelfTestCapabilities, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	capabilities, err := d.selfTestCapabilitiesLocked(h, devicePath, nil)
	if err != nil {
		return nil, err
	}
	return &capabilities, nil
}

func ReadSelfTestCapabilities(devicePath string) (*SelfTestCapabilities, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.SelfTestCapabilities()
}

func (d *Device) selfTestStatusLocked(h C.smart_h, devicePath string) (*SelfTestStatus, error) {
	proto := C.get_device_proto(h)
	switch proto {
	case C.SMART_PROTO_ATA:
		raw, err := readATASelfTestDataLocked(h, devicePath)
		if err != nil {
			return nil, err
		}
		capabilities, err := d.selfTestCapabilitiesLocked(h, devicePath, raw)
		if err != nil {
			return nil, err
		}
		if !capabilities.Supported {
			return nil, fmt.Errorf("%w: self-tests on %s", ErrUnsupportedFeature, devicePath)
		}
		exec := DecodeSelfTestExecStatus(uint64(raw[363]))
		if d.driveMatch.FirmwareBugs&FirmwareBugSamsung3 != 0 && raw[363] == 0xf0 {
			exec.Status = "ambiguous_completed_or_in_progress"
			exec.RemainingPct = -1
		}
		status := &SelfTestStatus{Protocol: "ATA", State: SelfTestStateIdle, ExecutionStatus: exec.Status, ProgressPct: -1, RemainingPct: -1}
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
		}
		if capabilities.ResultLog {
			log, logErr := d.readSelfTestLogLocked(h, devicePath)
			if logErr == nil {
				status.Results = log.Entries
				status.ChecksumValid = log.ChecksumValid
			} else if IsControllerError(logErr) {
				return nil, logErr
			}
		}
		return status, nil
	case C.SMART_PROTO_SCSI:
		capabilities, err := d.selfTestCapabilitiesLocked(h, devicePath, nil)
		if err != nil {
			return nil, err
		}
		if !capabilities.Supported {
			return nil, fmt.Errorf("%w: self-tests on %s", ErrUnsupportedFeature, devicePath)
		}
		log, err := d.readSelfTestLogLocked(h, devicePath)
		if err != nil {
			return nil, err
		}
		sense := make([]byte, 252)
		rc := C.smart_scsi_request_sense(h, unsafe.Pointer(&sense[0]), C.size_t(len(sense)))
		if rc == 0 {
			running, progress, known := parseSCSISelfTestProgress(sense)
			if running {
				log.InProgress = true
				log.ProgressPct = progress
				log.ProgressKnown = known
			}
		} else if int(rc) == int(C.ETIMEDOUT) || int(rc) == int(C.ECONNABORTED) {
			return nil, wrapDeviceError(h, fmt.Errorf("failed to read SCSI self-test status from %s: code %d", devicePath, int(rc)), devicePath)
		}
		status := statusFromLog("SCSI", capabilities, *log)
		return &status, nil
	case C.SMART_PROTO_NVME:
		capabilities, err := d.selfTestCapabilitiesLocked(h, devicePath, nil)
		if err != nil {
			return nil, err
		}
		if !capabilities.Supported {
			return nil, fmt.Errorf("%w: self-tests on %s", ErrUnsupportedFeature, devicePath)
		}
		log, err := d.readSelfTestLogLocked(h, devicePath)
		if err != nil {
			return nil, err
		}
		status := statusFromLog("NVMe", capabilities, *log)
		status.NamespaceID = capabilities.NamespaceID
		return &status, nil
	default:
		return nil, fmt.Errorf("%w: self-tests on %s", ErrUnsupportedFeature, devicePath)
	}
}

func (d *Device) SelfTestStatus() (*SelfTestStatus, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	return d.selfTestStatusLocked(h, devicePath)
}

func ReadSelfTestStatus(devicePath string) (*SelfTestStatus, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.SelfTestStatus()
}

func (d *Device) StartSelfTest(kind SelfTestKind) error {
	if d == nil || d.selfTestMu == nil {
		return ErrDeviceClosed
	}
	if kind == SelfTestKindSelective || kind == SelfTestKindSelectiveCaptive {
		return fmt.Errorf("%w: %s", ErrSelfTestConfigurationRequired, kind)
	}
	code, ok := selfTestCode(kind)
	if !ok {
		return fmt.Errorf("%w: %s", ErrInvalidSelfTestType, kind)
	}
	d.selfTestMu.Lock()
	defer d.selfTestMu.Unlock()
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	status, err := d.selfTestStatusLocked(h, devicePath)
	if err != nil {
		return err
	}
	capabilities := d.selfTestCaps
	validationKind := kind
	if validationKind == SelfTestKindOffline && capabilities.Protocol == "SCSI" {
		validationKind = SelfTestKindDefault
	}
	if err := validateSelfTestStart(capabilities, *status, validationKind); err != nil {
		return fmt.Errorf("%w: %s", err, devicePath)
	}
	if rc := C.smart_self_test(h, C.uint8_t(code)); rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("start %s self-test on %s failed with code %d", kind, devicePath, int(rc)), devicePath)
	}
	return nil
}

func (d *Device) StartSelectiveSelfTest(spans []SelectiveSpan, options SelectiveSelfTestOptions) error {
	if d == nil || d.selfTestMu == nil {
		return ErrDeviceClosed
	}
	kind := SelfTestKindSelective
	code := uint8(SelfTestSelective)
	if options.Captive {
		kind = SelfTestKindSelectiveCaptive
		code = SelfTestSelectiveCaptive
	}
	d.selfTestMu.Lock()
	defer d.selfTestMu.Unlock()
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	status, err := d.selfTestStatusLocked(h, devicePath)
	if err != nil {
		return err
	}
	capabilities := d.selfTestCaps
	if err := validateSelfTestStart(capabilities, *status, kind); err != nil {
		return fmt.Errorf("%w: %s", err, devicePath)
	}
	sm := C.smart_read_log(h, C.uint8_t(0x09), C.size_t(512))
	if sm == nil {
		return wrapDeviceError(h, fmt.Errorf("failed to read selective self-test log from %s", devicePath), devicePath)
	}
	var size C.size_t
	buf := C.get_sm_buffer(sm, &size)
	if buf == nil || size < 512 {
		C.smart_free(sm)
		return fmt.Errorf("selective self-test log from %s is too short: %d bytes", devicePath, int(size))
	}
	raw := C.GoBytes(buf, 512)
	C.smart_free(sm)
	configured, err := buildATASelectiveSelfTestLog(raw, spans, options, uint64(C.get_device_sector_count(h)))
	if err != nil {
		return err
	}
	if rc := C.smart_write_smart_log(h, C.uint8_t(0x09), unsafe.Pointer(&configured[0]), C.size_t(len(configured))); rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("write selective self-test log on %s failed with code %d", devicePath, int(rc)), devicePath)
	}
	if rc := C.smart_self_test(h, C.uint8_t(code)); rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("start %s self-test on %s failed with code %d", kind, devicePath, int(rc)), devicePath)
	}
	return nil
}

func StartSelectiveSelfTest(devicePath string, spans []SelectiveSpan, options SelectiveSelfTestOptions) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.StartSelectiveSelfTest(spans, options)
}

func StartSelfTest(devicePath string, kind SelfTestKind) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.StartSelfTest(kind)
}

func SelfTest(devicePath string, testType uint8) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.SelfTest(testType)
}

func (d *Device) SelfTest(testType uint8) error {
	if testType == SelfTestAbort || testType == 0x0f {
		return d.AbortSelfTest()
	}
	kind, ok := selfTestKindFromCode(testType)
	if !ok {
		return fmt.Errorf("%w: 0x%02x", ErrInvalidSelfTestType, testType)
	}
	return d.StartSelfTest(kind)
}

func (d *Device) AbortSelfTest() error {
	if d == nil || d.selfTestMu == nil {
		return ErrDeviceClosed
	}
	d.selfTestMu.Lock()
	defer d.selfTestMu.Unlock()
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	capabilities, err := d.selfTestCapabilitiesLocked(h, devicePath, nil)
	if err != nil {
		return err
	}
	if !capabilities.Abort {
		return fmt.Errorf("%w: abort self-test on %s", ErrUnsupportedFeature, devicePath)
	}
	if rc := C.smart_self_test(h, C.ATA_SELF_TEST_ABORT); rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("abort self-test on %s failed with code %d", devicePath, int(rc)), devicePath)
	}
	return nil
}

func AbortSelfTest(devicePath string) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.AbortSelfTest()
}

func ReadErrorLog(devicePath string) (*ATAErrorLog, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadErrorLog()
}

func (d *Device) ReadErrorLog() (*ATAErrorLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	sm := C.smart_read_error_log(h)
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read error log from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	var elBufSize C.size_t
	elRawBuf := C.get_sm_buffer(sm, &elBufSize)
	if elRawBuf == nil || int(elBufSize) < 512 {
		return nil, fmt.Errorf("error log from %s is too short: %d bytes", devicePath, int(elBufSize))
	}
	raw := C.GoBytes(elRawBuf, C.int(elBufSize))
	log := parseATAErrorLogWithBugs(raw, d.driveMatch.FirmwareBugs)
	return &log, nil
}

func ReadNVMeErrorLog(devicePath string) (*NVMeErrorLog, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadNVMeErrorLog()
}

func ReadNVMEErrorLog(devicePath string) (*NVMeErrorLog, error) {
	return ReadNVMeErrorLog(devicePath)
}

func (d *Device) ReadNVMeErrorLog() (*NVMeErrorLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	if C.get_device_proto(h) != C.SMART_PROTO_NVME {
		return nil, fmt.Errorf("NVMe error log is not available on %s", devicePath)
	}

	identify := make([]byte, 4096)
	if rc := C.smart_nvme_identify_ctrl(h, unsafe.Pointer(&identify[0]), C.size_t(len(identify))); rc != 0 {
		return nil, wrapDeviceError(h, fmt.Errorf("NVMe identify controller failed with code %d", int(rc)), devicePath)
	}
	entryCount := int(identify[262]) + 1

	sm := C.smart_read_log(h, C.uint8_t(0x01), C.size_t(entryCount*64))
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read NVMe error log from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	var bufSize C.size_t
	rawBuf := C.get_sm_buffer(sm, &bufSize)
	if rawBuf == nil || int(bufSize) < entryCount*64 {
		return nil, fmt.Errorf("NVMe error log from %s is too short: %d bytes", devicePath, int(bufSize))
	}
	raw := C.GoBytes(rawBuf, C.int(bufSize))
	log := parseNVMeErrorLog(raw)
	return &log, nil
}

func (d *Device) ReadNVMEErrorLog() (*NVMeErrorLog, error) {
	return d.ReadNVMeErrorLog()
}

func ReadSCTStatus(devicePath string) (*SCTStatus, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadSCTStatus()
}

func (d *Device) ReadSCTStatus() (*SCTStatus, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	return readSCTStatusLocked(h, devicePath)
}

func readSCTStatusLocked(h C.smart_h, devicePath string) (*SCTStatus, error) {
	if !bool(C.get_device_sct_supported(h)) {
		return nil, fmt.Errorf("%w: SCT on %s", ErrUnsupportedFeature, devicePath)
	}
	sm := C.smart_read_log(h, C.uint8_t(0xE0), C.size_t(512))
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read SCT status from %s", devicePath), devicePath)
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
				if sv == 0x2cf4 {
					status.SmartStatusPassed = false
					status.SmartStatusKnown = true
				} else if sv == 0xc24f {
					status.SmartStatusPassed = true
					status.SmartStatusKnown = true
				}
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

	if status.FormatVersion != 2 && status.FormatVersion != 3 {
		return nil, fmt.Errorf("unsupported SCT status format version %d", status.FormatVersion)
	}

	return status, nil
}

func ReadSCTTempHistory(devicePath string) (*SCTTempHistory, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadSCTTempHistory()
}

func (d *Device) ReadSCTTempHistory() (*SCTTempHistory, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	if !bool(C.get_device_sct_supported(h)) {
		return nil, fmt.Errorf("%w: SCT on %s", ErrUnsupportedFeature, devicePath)
	}

	sm := C.smart_read_sct_temp_history(h)
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read SCT temperature history from %s", devicePath), devicePath)
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

	if len(raw) < 34 {
		return nil, fmt.Errorf("SCT temperature history too short: %d bytes", len(raw))
	}

	hist.CBSize = uint16(raw[30]) | uint16(raw[31])<<8
	hist.CBIndex = uint16(raw[32]) | uint16(raw[33])<<8
	realSize := int(hist.CBSize)
	if realSize <= 0 || realSize > len(raw)-34 || hist.CBIndex >= hist.CBSize {
		return nil, fmt.Errorf("invalid SCT temperature history circular buffer size=%d index=%d", hist.CBSize, hist.CBIndex)
	}
	if realSize > 478 {
		return nil, fmt.Errorf("invalid SCT temperature history circular buffer size=%d", hist.CBSize)
	}

	hist.Samples = make([]SCTTempSample, realSize)
	for j := 0; j < realSize; j++ {
		idx := (int(hist.CBIndex) - j) % realSize
		if idx < 0 {
			idx += realSize
		}
		hist.Samples[j] = SCTTempSample{
			Temperature: int8(raw[34+idx]),
		}
	}

	return hist, nil
}

func ReadNVMeIdentifyCtrl(devicePath string) (*NVMeIdentifyCtrl, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadNVMeIdentifyCtrl()
}

func (d *Device) ReadNVMeIdentifyCtrl() (*NVMeIdentifyCtrl, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	buf := make([]byte, 4096)

	rc := C.smart_nvme_identify_ctrl(h, unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
	if rc != 0 {
		return nil, wrapDeviceError(h, fmt.Errorf("nvme identify controller failed with code %d", int(rc)), devicePath)
	}
	result := parseNVMeIdentifyCtrl(buf)
	result.NamespaceID = uint32(C.get_device_nvme_nsid(h))
	return result, nil
}

func parseNVMeIdentifyCtrl(buf []byte) *NVMeIdentifyCtrl {
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
		result.MaxDataXferSize = buf[77]
	}
	if len(buf) >= 80 {
		result.ControllerID = uint16(buf[78]) | uint16(buf[79])<<8
	}
	if len(buf) >= 84 {
		result.NVMeVersion = uint32(buf[80]) | uint32(buf[81])<<8 |
			uint32(buf[82])<<16 | uint32(buf[83])<<24
	}
	if len(buf) >= 260 {
		result.OptionalAdminCommands = uint16(buf[256]) | uint16(buf[257])<<8
		result.SelfTestSupported = result.OptionalAdminCommands&0x0010 != 0
		result.AbortCmdLimit = buf[258]
		result.AsyncEventLimit = buf[259]
	}
	if len(buf) >= 264 {
		result.FirmwareSlots = (buf[260] >> 1) & 0x07
		result.ErrorLogEntries = uint16(buf[262]) + 1
		result.NumPowerStates = uint16(buf[263]) + 1
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
		result.TotalCapacityString = bytesToDecimalString(buf[280:296], false)
		result.TotalCapacity = uint64(buf[280]) | uint64(buf[281])<<8 |
			uint64(buf[282])<<16 | uint64(buf[283])<<24 |
			uint64(buf[284])<<32 | uint64(buf[285])<<40 |
			uint64(buf[286])<<48 | uint64(buf[287])<<56
	}
	if len(buf) >= 312 {
		result.UnallocCapacityString = bytesToDecimalString(buf[296:312], false)
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
	if len(buf) >= 332 {
		result.MNTMT = uint16(buf[324]) | uint16(buf[325])<<8
		result.MXTMT = uint16(buf[326]) | uint16(buf[327])<<8
		result.SanitizeCaps = uint32(buf[328]) | uint32(buf[329])<<8 |
			uint32(buf[330])<<16 | uint32(buf[331])<<24
	}
	if len(buf) >= 524 {
		result.NumNamespaces = uint32(buf[516]) | uint32(buf[517])<<8 |
			uint32(buf[518])<<16 | uint32(buf[519])<<24
	}
	if len(buf) >= 526 {
		result.VolatileWriteCache = (buf[525] & 0x01) != 0
	}
	if len(buf) >= 80 {
		result.IEEE = [3]uint8{buf[73], buf[74], buf[75]}
	}

	return result
}

func ReadNVMeIdentifyNamespace(devicePath string, namespaceID uint32) (*NVMeIdentifyNamespace, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadNVMeIdentifyNamespace(namespaceID)
}

func (d *Device) ReadNVMeIdentifyNamespace(namespaceID uint32) (*NVMeIdentifyNamespace, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	if namespaceID == 0 {
		namespaceID = uint32(C.get_device_nvme_nsid(h))
	}
	if namespaceID == 0 {
		return nil, fmt.Errorf("NVMe namespace ID is unavailable for %s", devicePath)
	}
	raw := make([]byte, 4096)
	if rc := C.smart_nvme_identify_ns(h, C.uint32_t(namespaceID), unsafe.Pointer(&raw[0]), C.size_t(len(raw))); rc != 0 {
		return nil, wrapDeviceError(h, fmt.Errorf("NVMe identify namespace %d failed with code %d", namespaceID, int(rc)), devicePath)
	}
	result := parseNVMeIdentifyNamespace(raw)
	result.NamespaceID = namespaceID
	return result, nil
}

func parseNVMeIdentifyNamespace(raw []byte) *NVMeIdentifyNamespace {
	result := &NVMeIdentifyNamespace{}
	if len(raw) >= 24 {
		result.Size = binary.LittleEndian.Uint64(raw[0:8])
		result.Capacity = binary.LittleEndian.Uint64(raw[8:16])
		result.Utilization = binary.LittleEndian.Uint64(raw[16:24])
	}
	if len(raw) >= 34 {
		result.Features = raw[24]
		result.FormattedLBA = raw[26]
		result.MetadataCapabilities = raw[27]
		result.DataProtectionCaps = raw[28]
		result.DataProtectionSettings = raw[29]
		result.MultipathCapabilities = raw[30]
		result.ReservationCapabilities = raw[31]
		result.FormatProgressIndicator = raw[32]
	}
	if len(raw) >= 64 {
		result.NVMCapacity = binary.LittleEndian.Uint64(raw[48:56])
		result.NVMCapacityString = bytesToDecimalString(raw[48:64], false)
	}
	if len(raw) >= 120 {
		copy(result.NamespaceGUID[:], raw[104:120])
	}
	if len(raw) >= 128 {
		copy(result.IEEEExtendedUniqueID[:], raw[120:128])
	}
	if len(raw) >= 129 {
		count := int(raw[25]) + 1
		if count > 16 {
			count = 16
		}
		available := (len(raw) - 128) / 4
		if count > available {
			count = available
		}
		if count > 0 {
			result.LBAFormats = make([]NVMeLBAFormat, count)
			for i := 0; i < count; i++ {
				offset := 128 + i*4
				exponent := raw[offset+2]
				format := NVMeLBAFormat{
					MetadataSize:        binary.LittleEndian.Uint16(raw[offset : offset+2]),
					DataSizeExponent:    exponent,
					RelativePerformance: raw[offset+3] & 0x03,
				}
				if exponent < 64 {
					format.DataSize = uint64(1) << exponent
				}
				result.LBAFormats[i] = format
			}
		}
	}
	return result
}

func ReadLogDirectory(devicePath string) ([]uint8, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadLogDirectory()
}

func (d *Device) ReadLogDirectory() ([]uint8, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	sm := C.smart_read_log_directory(h)
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read log directory from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	cAttr := C.get_attr_at(sm, C.int(0))
	raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
	if len(raw) < 512 {
		return nil, fmt.Errorf("log directory too short: %d bytes", len(raw))
	}

	return parseATALogDirectory(raw), nil
}

func ReadGPLLogDirectory(devicePath string) ([]uint8, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadGPLLogDirectory()
}

func (d *Device) ReadGPLLogDirectory() ([]uint8, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	counts, err := d.gplDirectoryLocked(h, devicePath)
	if err != nil {
		return nil, err
	}
	addresses := make([]uint8, 0, len(counts))
	for address := 1; address <= 255; address++ {
		if counts[uint8(address)] != 0 {
			addresses = append(addresses, uint8(address))
		}
	}
	return addresses, nil
}

func (d *Device) gplDirectoryLocked(h C.smart_h, devicePath string) (map[uint8]uint16, error) {
	if d.gplDirectory != nil {
		return d.gplDirectory, nil
	}
	if d.driveMatch.FirmwareBugs&FirmwareBugNoLogDir != 0 {
		d.gplDirectory = map[uint8]uint16{}
		return d.gplDirectory, nil
	}
	sm := C.smart_read_gpl_log(h, C.uint8_t(0x00), C.uint16_t(0), C.size_t(512))
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read GPL directory from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	var bufSize C.size_t
	rawBuf := C.get_sm_buffer(sm, &bufSize)
	if rawBuf == nil || int(bufSize) < 512 {
		return nil, fmt.Errorf("GPL directory from %s is too short: %d bytes", devicePath, int(bufSize))
	}
	raw := C.GoBytes(rawBuf, C.int(bufSize))
	d.gplDirectory = parseATALogDirectoryCounts(raw)
	return d.gplDirectory, nil
}

func ReadExtendedErrorLog(devicePath string) (*ATAErrorLog, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadExtendedErrorLog()
}

func (d *Device) ReadExtendedErrorLog() (*ATAErrorLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	directory, err := d.gplDirectoryLocked(h, devicePath)
	if err != nil {
		return nil, err
	}
	sectors := directory[0x03]
	if sectors == 0 {
		sectors = 1
	}

	sm := C.smart_read_gpl_log(h, C.uint8_t(0x03), C.uint16_t(0), C.size_t(sectors)*512)
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read extended error log from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	var bufSize C.size_t
	rawBuf := C.get_sm_buffer(sm, &bufSize)
	if rawBuf == nil || int(bufSize) < 512 {
		return nil, fmt.Errorf("extended error log buffer too small: %d bytes", int(bufSize))
	}
	raw := C.GoBytes(rawBuf, C.int(bufSize))
	result := parseATAExtendedErrorLogWithBugs(raw, d.driveMatch.FirmwareBugs)
	return &result, nil
}

func ReadExtendedSelfTestLog(devicePath string) (*SelfTestLog, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadExtendedSelfTestLog()
}

func (d *Device) ReadExtendedSelfTestLog() (*SelfTestLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()
	directory, err := d.gplDirectoryLocked(h, devicePath)
	if err != nil {
		return nil, err
	}
	sectors := directory[0x07]
	if sectors == 0 {
		sectors = 1
	}

	sm := C.smart_read_gpl_log(h, C.uint8_t(0x07), C.uint16_t(0), C.size_t(sectors)*512)
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read extended self-test log from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	var estBufSize C.size_t
	estRawBuf := C.get_sm_buffer(sm, &estBufSize)
	if estRawBuf == nil || int(estBufSize) < 512 {
		return nil, fmt.Errorf("extended self-test log buffer too small: %d bytes", int(estBufSize))
	}
	raw := C.GoBytes(estRawBuf, C.int(estBufSize))
	log := parseATAExtendedSelfTestLog(raw)
	return &log, nil
}

type deviceStatDef struct {
	name string
	size int
}

var deviceStatDefs = map[uint8]map[int]deviceStatDef{
	0x01: {
		1:  {"General Statistics: Power-on Resets", 4},
		2:  {"General Statistics: Power-on Hours", 4},
		3:  {"General Statistics: Logical Sectors Written", 6},
		4:  {"General Statistics: Number of Write Commands", 6},
		5:  {"General Statistics: Logical Sectors Read", 6},
		6:  {"General Statistics: Number of Read Commands", 6},
		7:  {"General Statistics: Date and Time Timestamp", 6},
		8:  {"General Statistics: Pending Error Count", 4},
		9:  {"General Statistics: Workload Utilization", 2},
		10: {"General Statistics: Utilization Usage Rate", 6},
		11: {"General Statistics: Resource Availability", 7},
		12: {"General Statistics: Random Write Resources Used", 1},
	},
	0x02: {
		1: {"Free-Fall Statistics: Number of Free-Fall Events Detected", 4},
		2: {"Free-Fall Statistics: Overlimit Shock Events", 4},
	},
	0x03: {
		1: {"Rotating Media Statistics: Spindle Motor Power-on Hours", 4},
		2: {"Rotating Media Statistics: Head Flying Hours", 4},
		3: {"Rotating Media Statistics: Head Load Events", 4},
		4: {"Rotating Media Statistics: Number of Reallocated Logical Sectors", 4},
		5: {"Rotating Media Statistics: Read Recovery Attempts", 4},
		6: {"Rotating Media Statistics: Number of Mechanical Start Failures", 4},
		7: {"Rotating Media Statistics: Number of Realloc Candidate Logical Sectors", 4},
		8: {"Rotating Media Statistics: Number of High Priority Unload Events", 4},
	},
	0x04: {
		1: {"General Errors Statistics: Number of Reported Uncorrectable Errors", 4},
		2: {"General Errors Statistics: Resets Between Command Acceptance and Completion", 4},
		3: {"General Errors Statistics: Physical Element Status Changed", 4},
	},
	0x05: {
		1:  {"Temperature Statistics: Current Temperature", -1},
		2:  {"Temperature Statistics: Average Short Term Temperature", -1},
		3:  {"Temperature Statistics: Average Long Term Temperature", -1},
		4:  {"Temperature Statistics: Highest Temperature", -1},
		5:  {"Temperature Statistics: Lowest Temperature", -1},
		6:  {"Temperature Statistics: Highest Average Short Term Temperature", -1},
		7:  {"Temperature Statistics: Lowest Average Short Term Temperature", -1},
		8:  {"Temperature Statistics: Highest Average Long Term Temperature", -1},
		9:  {"Temperature Statistics: Lowest Average Long Term Temperature", -1},
		10: {"Temperature Statistics: Time in Over-Temperature", 4},
		11: {"Temperature Statistics: Specified Maximum Operating Temperature", -1},
		12: {"Temperature Statistics: Time in Under-Temperature", 4},
		13: {"Temperature Statistics: Specified Minimum Operating Temperature", -1},
	},
	0x06: {
		1: {"Transport Statistics: Number of Hardware Resets", 4},
		2: {"Transport Statistics: Number of ASR Events", 4},
		3: {"Transport Statistics: Number of Interface CRC Errors", 4},
	},
	0x07: {
		1: {"Solid State Device Statistics: Percentage Used Endurance Indicator", 1},
	},
}

func deviceStatInfo(page uint8, index int) (deviceStatDef, bool) {
	pageDefs, ok := deviceStatDefs[page]
	if !ok {
		return deviceStatDef{}, false
	}
	def, ok := pageDefs[index]
	return def, ok
}

func decodeDeviceStatValue(entry []byte, size int) (uint64, string, int) {
	if size < 0 {
		v := int(int8(entry[0]))
		return uint64(entry[0]), fmt.Sprintf("%d", v), v
	}
	if size > 7 {
		size = 7
	}
	var value uint64
	for i := 0; i < size && i < len(entry); i++ {
		value |= uint64(entry[i]) << (8 * i)
	}
	return value, "", int(value)
}

func ensureSCTCommandReadyLocked(h C.smart_h, devicePath string) error {
	status, err := readSCTStatusLocked(h, devicePath)
	if err != nil {
		return err
	}
	if status.ExtStatusCode == 0xffff {
		return fmt.Errorf("SCT command already in progress")
	}
	return nil
}

func verifySCTCommandLocked(h C.smart_h, devicePath string, actionCode, functionCode uint16) error {
	status, err := readSCTStatusLocked(h, devicePath)
	if err != nil {
		return err
	}
	if status.ExtStatusCode != 0 || status.ActionCode != actionCode || status.FunctionCode != functionCode {
		return fmt.Errorf("SCT command verify failed: ext=0x%04x action=%d function=%d", status.ExtStatusCode, status.ActionCode, status.FunctionCode)
	}
	return nil
}

func ReadDeviceStatistics(devicePath string) ([]Attribute, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadDeviceStatistics()
}

func (d *Device) ReadDeviceStatistics() ([]Attribute, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	dirSM := C.smart_read_gpl_log(h, C.uint8_t(0x04), C.uint16_t(0x00), C.size_t(512))
	if dirSM == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read device statistics directory from %s", devicePath), devicePath)
	}
	defer C.smart_free(dirSM)

	cAttrDir := C.get_attr_at(dirSM, C.int(0))
	dirRaw := C.GoBytes(cAttrDir.raw, C.int(cAttrDir.bytes))
	if len(dirRaw) < 9 {
		return nil, fmt.Errorf("device statistics directory too short")
	}
	if dirRaw[2] != 0 || dirRaw[8] == 0 {
		return nil, fmt.Errorf("invalid device statistics directory")
	}

	nEntries := int(dirRaw[8])
	pages := make([]uint8, 0, nEntries)
	var maxPage uint8
	for i := 0; i < nEntries && 9+i < len(dirRaw); i++ {
		pageNum := dirRaw[9+i]
		if pageNum == 0 {
			continue
		}
		pages = append(pages, pageNum)
		if pageNum > maxPage {
			maxPage = pageNum
		}
	}

	var attrs []Attribute
	appendPage := func(pageNum uint8, pageRaw []byte) {
		if len(pageRaw) < 8 || pageRaw[2] != pageNum {
			return
		}
		for j := 8; j+8 <= len(pageRaw); j += 8 {
			entry := pageRaw[j : j+8]
			flags := entry[7]
			if flags&0xc0 != 0xc0 {
				continue
			}
			index := j / 8
			def, ok := deviceStatInfo(pageNum, index)
			if !ok {
				continue
			}
			rawVal, rawString, value := decodeDeviceStatValue(entry, def.size)
			attrs = append(attrs, Attribute{
				Page:      0x04,
				ID:        uint32(pageNum)<<8 | uint32(index),
				Name:      def.name,
				Value:     value,
				RawValue:  rawVal,
				RawString: rawString,
				RawBytes:  append([]byte(nil), entry...),
			})
		}
	}

	if maxPage != 0 {
		pagesSM := C.smart_read_gpl_log(h, C.uint8_t(0x04), C.uint16_t(1), C.size_t(maxPage)*512)
		if pagesSM != nil {
			var pagesSize C.size_t
			pagesBuf := C.get_sm_buffer(pagesSM, &pagesSize)
			if pagesBuf != nil && int(pagesSize) >= int(maxPage)*512 {
				pagesRaw := C.GoBytes(pagesBuf, C.int(pagesSize))
				C.smart_free(pagesSM)
				pagesSM = nil
				validBatch := true
				for _, pageNum := range pages {
					offset := (int(pageNum) - 1) * 512
					if pagesRaw[offset+2] != pageNum {
						validBatch = false
						break
					}
				}
				if validBatch {
					for _, pageNum := range pages {
						offset := (int(pageNum) - 1) * 512
						appendPage(pageNum, pagesRaw[offset:offset+512])
					}
					return attrs, nil
				}
			}
			if pagesSM != nil {
				C.smart_free(pagesSM)
			}
		}
	}

	for _, pageNum := range pages {
		pageSM := C.smart_read_gpl_log(h, C.uint8_t(0x04), C.uint16_t(pageNum), C.size_t(512))
		if pageSM == nil {
			continue
		}
		cAttr := C.get_attr_at(pageSM, C.int(0))
		pageRaw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))
		C.smart_free(pageSM)
		appendPage(pageNum, pageRaw)
	}

	return attrs, nil
}

func SetSCTFeatureControl(devicePath string, featureCode uint16, state uint16, persistent bool) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.SetSCTFeatureControl(featureCode, state, persistent)
}

func (d *Device) SetSCTFeatureControl(featureCode uint16, state uint16, persistent bool) error {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	if err := ensureSCTCommandReadyLocked(h, devicePath); err != nil {
		return err
	}

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

	rc := C.smart_write_smart_log(h, C.uint8_t(0xE0), unsafe.Pointer(&cmd[0]), C.size_t(512))
	if rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("SCT feature control set failed with code %d", int(rc)), devicePath)
	}

	return verifySCTCommandLocked(h, devicePath, 4, 1)
}

func ReadSelectiveSelfTestLog(devicePath string) (*SelfTestLog, error) {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadSelectiveSelfTestLog()
}

func (d *Device) ReadSelectiveSelfTestLog() (*SelfTestLog, error) {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	sm := C.smart_read_log(h, C.uint8_t(0x09), C.size_t(512))
	if sm == nil {
		return nil, wrapDeviceError(h, fmt.Errorf("failed to read selective self-test log from %s", devicePath), devicePath)
	}
	defer C.smart_free(sm)

	cAttr := C.get_attr_at(sm, C.int(0))
	raw := C.GoBytes(cAttr.raw, C.int(cAttr.bytes))

	log := parseSelectiveSelfTestLog(raw)
	return log, nil
}

func parseSelectiveSelfTestLog(raw []byte) *SelfTestLog {
	log := &SelfTestLog{}
	if len(raw) < 512 {
		return log
	}

	var sum byte
	for _, b := range raw[:512] {
		sum += b
	}
	log.ChecksumValid = sum == 0

	dataRev := binary.LittleEndian.Uint16(raw[0:2])
	log.Revision = dataRev
	if dataRev != 0x0001 {
		return log
	}

	for i := 0; i < 5; i++ {
		off := 2 + i*16
		if off+16 > len(raw) {
			break
		}
		log.SelectiveSpans[i] = SelectiveSpan{
			Start: uint64(raw[off]) | uint64(raw[off+1])<<8 |
				uint64(raw[off+2])<<16 | uint64(raw[off+3])<<24 |
				uint64(raw[off+4])<<32 | uint64(raw[off+5])<<40 |
				uint64(raw[off+6])<<48 | uint64(raw[off+7])<<56,
			End: uint64(raw[off+8]) | uint64(raw[off+9])<<8 |
				uint64(raw[off+10])<<16 | uint64(raw[off+11])<<24 |
				uint64(raw[off+12])<<32 | uint64(raw[off+13])<<40 |
				uint64(raw[off+14])<<48 | uint64(raw[off+15])<<56,
		}
	}

	log.SelectiveCurrentSpan = binary.LittleEndian.Uint16(raw[500:502])

	if len(raw) >= 500 {
		log.SelectiveCurrentLBA = uint64(raw[492]) | uint64(raw[493])<<8 |
			uint64(raw[494])<<16 | uint64(raw[495])<<24 |
			uint64(raw[496])<<32 | uint64(raw[497])<<40 |
			uint64(raw[498])<<48 | uint64(raw[499])<<56
	}

	if len(raw) >= 504 {
		flags := binary.LittleEndian.Uint16(raw[502:504])
		log.SelectiveFlags = flags
		log.SelectiveScanEnabled = flags&0x0002 != 0
		log.SelectiveScanActive = flags&0x0002 != 0 && flags&0x0010 != 0
		log.SelectiveScanPending = flags&0x0002 != 0 && flags&0x0008 != 0
	}

	if len(raw) >= 510 {
		log.SelectivePendingTime = uint16(raw[508]) | uint16(raw[509])<<8
	}

	return log
}

func SetSCTErrorRecoveryControl(devicePath string, read bool, timeLimit uint16) error {
	d, err := OpenDevice(devicePath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.SetSCTErrorRecoveryControl(read, timeLimit)
}

func (d *Device) SetSCTErrorRecoveryControl(read bool, timeLimit uint16) error {
	h, devicePath, err := d.lockHandle()
	if err != nil {
		return err
	}
	defer d.mu.Unlock()
	if err := ensureSCTCommandReadyLocked(h, devicePath); err != nil {
		return err
	}

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

	rc := C.smart_write_smart_log(h, C.uint8_t(0xE0), unsafe.Pointer(&cmd[0]), C.size_t(512))
	if rc != 0 {
		return wrapDeviceError(h, fmt.Errorf("SCT error recovery control set failed with code %d", int(rc)), devicePath)
	}

	return verifySCTCommandLocked(h, devicePath, 3, 1)
}
