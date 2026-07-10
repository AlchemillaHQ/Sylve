// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import "encoding/binary"

type scsiInformationalException struct {
	ASC                     uint8
	ASCQ                    uint8
	CurrentTemperature      uint8
	CurrentTemperatureKnown bool
	TripTemperature         uint8
	TripTemperatureKnown    bool
}

func parseSCSIInformationalException(raw []byte) (scsiInformationalException, bool) {
	result := scsiInformationalException{}
	if len(raw) < 10 || raw[0]&0x3f != 0x2f {
		return result, false
	}
	end := 4 + int(binary.BigEndian.Uint16(raw[2:4]))
	if end < 10 || end > len(raw) || raw[4] != 0 || raw[5] != 0 {
		return result, false
	}
	length := int(raw[7])
	if length < 2 || 8+length > end {
		return result, false
	}
	result.ASC = raw[8]
	result.ASCQ = raw[9]
	if length >= 3 && raw[10] != 0xff {
		result.CurrentTemperature = raw[10]
		result.CurrentTemperatureKnown = true
	}
	if length >= 4 && raw[11] != 0xff {
		result.TripTemperature = raw[11]
		result.TripTemperatureKnown = true
	}
	return result, true
}

func parseSCSISenseCode(raw []byte) (uint8, uint8, bool) {
	if len(raw) == 0 {
		return 0, 0, false
	}
	if len(raw) >= 18 && allZero(raw) {
		return 0, 0, true
	}
	switch raw[0] & 0x7f {
	case 0x70, 0x71:
		if len(raw) < 14 {
			return 0, 0, false
		}
		return raw[12], raw[13], true
	case 0x72, 0x73:
		if len(raw) < 4 {
			return 0, 0, false
		}
		return raw[2], raw[3], true
	default:
		return 0, 0, false
	}
}

func parseSCSIPowerMode(raw []byte) (SCSIPowerMode, bool) {
	asc, ascq, ok := parseSCSISenseCode(raw)
	if !ok {
		return SCSIPowerModeUnknown, false
	}
	if asc != 0x5e {
		return SCSIPowerModeActive, true
	}
	switch ascq {
	case 0x00:
		return SCSIPowerModeLowPower, true
	case 0x01, 0x03, 0x05, 0x06, 0x07, 0x08, 0x42:
		return SCSIPowerModeIdle, true
	case 0x02, 0x04, 0x43:
		return SCSIPowerModeStandby, true
	case 0x09, 0x0a:
		return SCSIPowerModeStandbyY, true
	case 0x41:
		return SCSIPowerModeActive, true
	case 0x45:
		return SCSIPowerModeSleep, true
	default:
		return SCSIPowerModeUnknown, true
	}
}

func scsiHealthFromCode(asc, ascq uint8) (bool, bool) {
	if asc == 0 {
		return true, true
	}
	if asc == 0x0b && ascq <= 0x14 {
		return true, false
	}
	if asc != 0x5d {
		return false, false
	}
	if ascq <= 0x03 || ascq == 0x1d || ascq == 0x73 || ascq == 0xff {
		return true, false
	}
	if ascq >= 0x10 && ascq <= 0x6c && (ascq-0x10)%0x10 <= 0x0c {
		return true, false
	}
	return false, false
}

func scsiHealthNeedsRequestSense(info scsiInformationalException, valid bool) bool {
	return !valid || info.ASC == 0
}
