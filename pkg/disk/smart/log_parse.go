// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import "encoding/binary"

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func ataChecksumsOK(raw []byte) bool {
	if len(raw) < 512 || len(raw)%512 != 0 {
		return false
	}
	for offset := 0; offset < len(raw); offset += 512 {
		var sum byte
		for _, b := range raw[offset : offset+512] {
			sum += b
		}
		if sum != 0 {
			return false
		}
	}
	return true
}

func parseATALogDirectory(raw []byte) []uint8 {
	counts := parseATALogDirectoryCounts(raw)
	if counts == nil {
		return nil
	}
	entries := make([]uint8, 0, len(counts))
	for address := 1; address <= 255; address++ {
		if counts[uint8(address)] != 0 {
			entries = append(entries, uint8(address))
		}
	}
	return entries
}

func parseATALogDirectoryCounts(raw []byte) map[uint8]uint16 {
	if len(raw) < 512 {
		return nil
	}
	counts := make(map[uint8]uint16, 32)
	for i := 0; i < 255; i++ {
		offset := 2 + i*2
		sectors := binary.LittleEndian.Uint16(raw[offset : offset+2])
		if sectors != 0 {
			counts[uint8(i+1)] = sectors
		}
	}
	return counts
}

func parseATAErrorLog(raw []byte) ATAErrorLog {
	return parseATAErrorLogWithBugs(raw, 0)
}

func parseATAErrorLogWithBugs(raw []byte, bugs FirmwareBug) ATAErrorLog {
	log := ATAErrorLog{ChecksumValid: ataChecksumsOK(raw)}
	if len(raw) < 512 {
		return log
	}

	log.Revision = uint16(raw[0])
	if bugs&(FirmwareBugSamsung|FirmwareBugSamsung2) != 0 {
		log.ErrorCount = uint32(binary.BigEndian.Uint16(raw[452:454]))
	} else {
		log.ErrorCount = uint32(binary.LittleEndian.Uint16(raw[452:454]))
	}
	pointer := int(raw[1])
	if log.ErrorCount == 0 || pointer < 1 || pointer > 5 {
		return log
	}

	count := int(log.ErrorCount)
	if count > 5 {
		count = 5
	}
	log.Entries = make([]ATAErrorEntry, 0, count)
	for i := 0; i < count; i++ {
		index := (pointer - 1 - i + 5) % 5
		offset := 2 + index*90
		entryRaw := raw[offset : offset+90]
		if allZero(entryRaw) {
			continue
		}
		err := entryRaw[60:90]
		lifetime := binary.LittleEndian.Uint16(err[28:30])
		if bugs&FirmwareBugSamsung != 0 {
			lifetime = binary.BigEndian.Uint16(err[28:30])
		}
		entry := ATAErrorEntry{
			Error:         err[1],
			Status:        err[7],
			SectorCount:   err[2],
			SectorCount16: uint16(err[2]),
			Device:        err[6],
			ErrorData:     uint16(err[1]) | uint16(err[7])<<8,
			ExtendedData:  uint16(err[6]) | uint16(err[2])<<8,
			LifetimeHours: uint32(lifetime),
			LBA: uint64(err[3]) |
				uint64(err[4])<<8 |
				uint64(err[5])<<16,
		}
		log.Entries = append(log.Entries, entry)
	}
	return log
}

func parseATAExtendedErrorLog(raw []byte) ATAErrorLog {
	return parseATAExtendedErrorLogWithBugs(raw, 0)
}

func parseATAExtendedErrorLogWithBugs(raw []byte, bugs FirmwareBug) ATAErrorLog {
	log := ATAErrorLog{ChecksumValid: ataChecksumsOK(raw)}
	if len(raw) < 512 {
		return log
	}

	log.Revision = uint16(raw[0])
	log.ErrorCount = uint32(binary.LittleEndian.Uint16(raw[500:502]))
	nSectors := len(raw) / 512
	nEntries := nSectors * 4
	index := int(binary.LittleEndian.Uint16(raw[2:4]))
	if index == 0 && raw[1] >= 1 && int(raw[1]) <= nEntries {
		index = int(raw[1])
	}
	if log.ErrorCount == 0 || index < 1 || index > nEntries {
		return log
	}
	index--

	count := int(log.ErrorCount)
	if count > nEntries {
		count = nEntries
	}
	log.Entries = make([]ATAErrorEntry, 0, count)
	for i := 0; i < count; i++ {
		page := index / 4
		pageEntry := index % 4
		offset := page*512 + 4 + pageEntry*124
		entryRaw := raw[offset : offset+124]
		if !allZero(entryRaw) {
			err := entryRaw[90:124]
			sectorCount := uint16(err[2]) | uint16(err[3])<<8
			lba := uint64(err[4]) |
				uint64(err[6])<<8 |
				uint64(err[8])<<16 |
				uint64(err[5])<<24 |
				uint64(err[7])<<32 |
				uint64(err[9])<<40
			if bugs&FirmwareBugXErrorLBA != 0 {
				lba = uint64(err[4]) | uint64(err[5])<<8 |
					uint64(err[6])<<16 | uint64(err[7])<<24 |
					uint64(err[8])<<32 | uint64(err[9])<<40
			}
			entry := ATAErrorEntry{
				Error:         err[1],
				Status:        err[11],
				SectorCount:   err[2],
				SectorCount16: sectorCount,
				Device:        err[10],
				ErrorData:     uint16(err[1]) | uint16(err[11])<<8,
				ExtendedData:  uint16(err[10]) | uint16(err[2])<<8,
				LifetimeHours: uint32(binary.LittleEndian.Uint16(err[32:34])),
				LBA:           lba,
			}
			log.Entries = append(log.Entries, entry)
		}
		if index == 0 {
			index = nEntries - 1
		} else {
			index--
		}
	}
	return log
}

func parseATAStandardSelfTestLog(raw []byte) SelfTestLog {
	return parseATAStandardSelfTestLogWithBugs(raw, 0)
}

func parseATAStandardSelfTestLogWithBugs(raw []byte, bugs FirmwareBug) SelfTestLog {
	log := SelfTestLog{ChecksumValid: ataChecksumsOK(raw)}
	if len(raw) < 512 {
		return log
	}
	log.Revision = binary.LittleEndian.Uint16(raw[0:2])
	pointer := int(raw[508])
	if bugs&FirmwareBugSamsung != 0 {
		pointer = int(raw[509])
	}
	if pointer < 1 || pointer > 21 {
		return log
	}
	log.Entries = make([]SelfTestEntry, 0, 21)
	for i := 0; i < 21; i++ {
		index := (pointer - 1 - i + 21) % 21
		offset := 2 + index*24
		entryRaw := raw[offset : offset+24]
		if allZero(entryRaw) {
			continue
		}
		if bugs&FirmwareBugSamsung != 0 {
			fixed := make([]byte, len(entryRaw))
			copy(fixed, entryRaw)
			fixed[0], fixed[1] = fixed[1], fixed[0]
			entryRaw = fixed
		}
		log.Entries = append(log.Entries, parseATASelfTestEntry(entryRaw))
	}
	return log
}

func parseATAExtendedSelfTestLog(raw []byte) SelfTestLog {
	log := SelfTestLog{ChecksumValid: ataChecksumsOK(raw)}
	if len(raw) < 512 {
		return log
	}
	log.Revision = uint16(raw[0])
	nSectors := len(raw) / 512
	nEntries := nSectors * 19
	index := int(binary.LittleEndian.Uint16(raw[2:4]))
	if index < 1 || index > nEntries {
		return log
	}
	index--
	log.Entries = make([]SelfTestEntry, 0, nEntries)
	for i := 0; i < nEntries; i++ {
		page := index / 19
		pageEntry := index % 19
		offset := page*512 + 4 + pageEntry*26
		entryRaw := raw[offset : offset+26]
		if !allZero(entryRaw) {
			log.Entries = append(log.Entries, parseATASelfTestEntry(entryRaw))
		}
		if index == 0 {
			index = nEntries - 1
		} else {
			index--
		}
	}
	return log
}

func parseNVMESelfTestLog(raw []byte) SelfTestLog {
	log := SelfTestLog{ChecksumValid: true}
	if len(raw) < 4 {
		return log
	}
	operation := raw[0] & 0x0f
	log.InProgress = operation != 0
	switch operation {
	case 0x1:
		log.CurrentType = string(SelfTestKindShort)
	case 0x2:
		log.CurrentType = string(SelfTestKindExtended)
	case 0xe:
		log.CurrentType = "vendor"
	}
	if log.InProgress {
		log.ProgressPct = int(raw[1] & 0x7f)
		log.ProgressKnown = log.ProgressPct <= 100
	}
	entryCount := (len(raw) - 4) / 28
	if entryCount > 20 {
		entryCount = 20
	}
	validEntries := 0
	for i := 0; i < entryCount; i++ {
		entryRaw := raw[4+i*28 : 4+(i+1)*28]
		if entryRaw[0]>>4 != 0 && entryRaw[0]&0x0f != 0x0f {
			validEntries++
		}
	}
	if validEntries != 0 {
		log.Entries = make([]SelfTestEntry, 0, validEntries)
	}
	for i := 0; i < entryCount; i++ {
		entryRaw := raw[4+i*28 : 4+(i+1)*28]
		if entryRaw[0]>>4 == 0 || entryRaw[0]&0x0f == 0x0f {
			continue
		}
		log.Entries = append(log.Entries, parseNVMESelfTestEntry(entryRaw))
	}
	return log
}

func parseSCSISelfTestLog(raw []byte) SelfTestLog {
	log := SelfTestLog{ChecksumValid: true}
	if len(raw) < 4 || raw[0]&0x3f != 0x10 {
		return log
	}
	end := 4 + int(binary.BigEndian.Uint16(raw[2:4]))
	if end > len(raw) {
		return log
	}
	entryCount := (end - 4) / 20
	if entryCount > 20 {
		entryCount = 20
	}
	validEntries := 0
	for i := 0; i < entryCount; i++ {
		entryRaw := raw[4+i*20 : 4+(i+1)*20]
		if entryRaw[4] == 0 && binary.BigEndian.Uint16(entryRaw[6:8]) == 0 {
			break
		}
		validEntries++
	}
	if validEntries != 0 {
		log.Entries = make([]SelfTestEntry, 0, validEntries)
	}
	for i := 0; i < entryCount; i++ {
		entryRaw := raw[4+i*20 : 4+(i+1)*20]
		if entryRaw[4] == 0 && binary.BigEndian.Uint16(entryRaw[6:8]) == 0 {
			break
		}
		scsi := parseSCSISelfTestEntry(entryRaw)
		entry := SelfTestEntry{
			Protocol:      "SCSI",
			Type:          scsi.Type,
			Mode:          scsi.Mode,
			Status:        scsi.Status,
			Outcome:       selfTestOutcome(scsi.Status),
			RemainingPct:  -1,
			LifetimeHours: scsi.LifetimeHours,
			LBA:           scsi.LBA,
			LBAValid:      scsi.LBAValid,
			SegmentNum:    scsi.SegmentNumber,
			SenseKey:      scsi.SenseKey,
			ASC:           scsi.ASC,
			ASCQ:          scsi.ASCQ,
		}
		if i == 0 && entry.Outcome == SelfTestOutcomeInProgress {
			log.InProgress = true
			log.CurrentType = entry.Type
		}
		log.Entries = append(log.Entries, entry)
	}
	return log
}

func parseNVMeErrorLog(raw []byte) NVMeErrorLog {
	entryCount := len(raw) / 64
	log := NVMeErrorLog{Capacity: entryCount}
	log.Entries = make([]NVMeErrorEntry, 0, entryCount)
	for i := 0; i < entryCount; i++ {
		entry := raw[i*64 : (i+1)*64]
		errorCount := binary.LittleEndian.Uint64(entry[0:8])
		if errorCount == 0 {
			continue
		}
		log.Entries = append(log.Entries, NVMeErrorEntry{
			ErrorCount:  errorCount,
			SQID:        binary.LittleEndian.Uint16(entry[8:10]),
			CommandID:   binary.LittleEndian.Uint16(entry[10:12]),
			StatusField: binary.LittleEndian.Uint16(entry[12:14]),
			ParamError:  binary.LittleEndian.Uint16(entry[14:16]),
			LBA:         binary.LittleEndian.Uint64(entry[16:24]),
			NamespaceID: binary.LittleEndian.Uint32(entry[24:28]),
		})
	}
	return log
}

func parseATASelfTestEntry(raw []byte) SelfTestEntry {
	e := SelfTestEntry{Protocol: "ATA", RemainingPct: -1}
	if len(raw) < 2 {
		return e
	}

	switch raw[0] {
	case 0x00:
		e.Type = "offline"
	case 0x01:
		e.Type = "short"
	case 0x02:
		e.Type = "extended"
	case 0x03:
		e.Type = "conveyance"
	case 0x04:
		e.Type = "selective"
	case 0x7F:
		e.Type = "abort"
	case 0x81:
		e.Type = "short_captive"
		e.Mode = "captive"
	case 0x82:
		e.Type = "extended_captive"
		e.Mode = "captive"
	case 0x83:
		e.Type = "conveyance_captive"
		e.Mode = "captive"
	case 0x84:
		e.Type = "selective_captive"
		e.Mode = "captive"
	default:
		if raw[0] >= 0x40 && raw[0] <= 0x7E {
			e.Type = "vendor_specific"
		} else {
			e.Type = "unknown"
		}
	}

	switch raw[1] >> 4 {
	case 0x0:
		e.Status = "completed"
	case 0x1:
		e.Status = "aborted_by_host"
	case 0x2:
		e.Status = "interrupted"
	case 0x3:
		e.Status = "fatal"
	case 0x4:
		e.Status = "failed_unknown"
	case 0x5:
		e.Status = "failed_electrical"
	case 0x6:
		e.Status = "failed_servo"
	case 0x7:
		e.Status = "failed_read"
	case 0x8:
		e.Status = "failed_handling"
	case 0xF:
		e.Status = "in_progress"
	default:
		e.Status = "unknown"
	}
	if remaining := int(raw[1] & 0x0f); remaining <= 9 {
		e.RemainingPct = remaining * 10
	}
	e.Outcome = selfTestOutcome(e.Status)
	if len(raw) >= 4 {
		e.LifetimeHours = uint64(binary.LittleEndian.Uint16(raw[2:4]))
	}
	if len(raw) >= 5 {
		e.Checkpoint = raw[4]
	}
	if len(raw) >= 26 {
		e.LBA = uint64(raw[5]) | uint64(raw[6])<<8 |
			uint64(raw[7])<<16 | uint64(raw[8])<<24 |
			uint64(raw[9])<<32 | uint64(raw[10])<<40
	} else if len(raw) >= 9 {
		lba := uint64(binary.LittleEndian.Uint32(raw[5:9]))
		if lba >= 0xffffffff {
			lba = 0xffffffffffff
		}
		e.LBA = lba
	}
	e.LBAValid = e.Outcome == SelfTestOutcomeFailed && e.LBA != 0xffffffffffff
	return e
}

func parseNVMESelfTestEntry(raw []byte) SelfTestEntry {
	e := SelfTestEntry{Protocol: "NVMe", RemainingPct: -1}
	if len(raw) < 3 {
		return e
	}
	op := raw[0] >> 4
	result := raw[0] & 0x0f
	e.SegmentNum = raw[1]
	valid := raw[2]

	switch op {
	case 0x1:
		e.Type = "short"
	case 0x2:
		e.Type = "extended"
	case 0xE:
		e.Type = "vendor"
	default:
		e.Type = "unknown"
	}
	switch result {
	case 0x0:
		e.Status = "completed"
	case 0x1:
		e.Status = "aborted"
	case 0x2:
		e.Status = "aborted_reset"
	case 0x3:
		e.Status = "aborted_ns_removed"
	case 0x4:
		e.Status = "aborted_format"
	case 0x5:
		e.Status = "fatal"
	case 0x6:
		e.Status = "failed_unknown"
	case 0x7:
		e.Status = "failed_segments"
	case 0x8:
		e.Status = "aborted_unknown"
	case 0x9:
		e.Status = "aborted_sanitize"
	case 0xF:
		e.Status = "unused"
	default:
		e.Status = "unknown"
	}
	e.Outcome = selfTestOutcome(e.Status)
	if len(raw) >= 12 {
		e.LifetimeHours = binary.LittleEndian.Uint64(raw[4:12])
	}
	if valid&0x01 != 0 && len(raw) >= 16 {
		e.NSID = binary.LittleEndian.Uint32(raw[12:16])
		e.NSIDValid = true
	}
	if valid&0x02 != 0 && len(raw) >= 24 {
		e.LBA = binary.LittleEndian.Uint64(raw[16:24])
		e.LBAValid = true
	}
	if valid&0x04 != 0 && len(raw) >= 25 {
		e.StatusCodeType = raw[24]
		e.StatusCodeTypeValid = true
	}
	if valid&0x08 != 0 && len(raw) >= 26 {
		e.StatusCode = raw[25]
		e.StatusCodeValid = true
	}
	return e
}

func parseSCSISelfTestEntry(raw []byte) SCSISelfTestEntry {
	e := SCSISelfTestEntry{}
	if len(raw) < 5 {
		return e
	}
	code := raw[4] >> 5 & 0x07
	result := raw[4] & 0x0f
	switch code {
	case 0:
		e.Type = "default"
		e.Mode = "foreground"
	case 1:
		e.Type = "short"
		e.Mode = "background"
	case 2:
		e.Type = "extended"
		e.Mode = "background"
	case 3:
		e.Type = "reserved"
	case 4:
		e.Type = "abort"
		e.Mode = "background"
	case 5:
		e.Type = "short"
		e.Mode = "foreground"
	case 6:
		e.Type = "extended"
		e.Mode = "foreground"
	default:
		e.Type = "unknown"
	}
	switch result {
	case 0:
		e.Status = "completed"
	case 1:
		e.Status = "aborted_by_user"
	case 2:
		e.Status = "aborted_reset"
	case 3:
		e.Status = "unknown_error"
	case 4:
		e.Status = "completed_segment_failed"
	case 5:
		e.Status = "failed_first_segment"
	case 6:
		e.Status = "failed_second_segment"
	case 7:
		e.Status = "failed_segment"
	case 15:
		e.Status = "in_progress"
	default:
		e.Status = "unknown"
	}
	if len(raw) >= 6 {
		e.SegmentNumber = raw[5]
	}
	if len(raw) >= 8 {
		e.LifetimeHours = uint64(binary.BigEndian.Uint16(raw[6:8]))
	}
	if len(raw) >= 16 {
		e.LBA = binary.BigEndian.Uint64(raw[8:16])
		e.LBAValid = result >= 3 && result <= 7 && e.LBA != 0xffffffffffffffff
	}
	if len(raw) >= 19 {
		e.SenseKey = raw[16] & 0x0f
		e.ASC = raw[17]
		e.ASCQ = raw[18]
	}
	return e
}
