// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"errors"
	"regexp"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
)

type ataWearoutEncoding uint8

const (
	wearoutNormalizedRemaining ataWearoutEncoding = iota + 1
	wearoutNormalizedUsed
	wearoutRawRemaining
	wearoutRawUsed
)

type ataWearoutRule struct {
	model       *regexp.Regexp
	firmware    string
	attributeID int
	encoding    ataWearoutEncoding
	rawMask     int64
	maxPercent  int64
}

// ATA endurance list of shame. Attribute IDs and names are not a contract:
// "Media_Wearout_Indicator" can mean life remaining, life used, or packed counters.
// Keep this separate from the generated drivedb. Every decoding entry needs a
// cited source and regression samples; an unverified interpretation stays Unknown.
// Match the full ATA IDENTIFY model, not the truncated CAM inventory model.
// First matching model/firmware wins, so firmware-specific rules go first.
var ataWearoutShame = []ataWearoutRule{
	{
		// WD Blue 3D NAND, 411030WD: reported firmware quirk where normalized
		// 230 is percentage USED, not remaining. RAW contains packed counters.
		// Scope this report-based interpretation to this model/firmware only.
		// https://forums.sandisk.com/t/media-wearout-indicator-wd-blue-ssd-incorrect-value-firmware-error/277714
		// https://www.smartmontools.org/ticket/1620
		model:       regexp.MustCompile(`^WDC  ?WDS400T2B0A-00SM50$`),
		firmware:    "411030WD",
		attributeID: 230,
		encoding:    wearoutNormalizedUsed,
		maxPercent:  100,
	},
	{
		// Samsung 860: normalized 177 is life remaining, raw is erase cycles.
		// https://github.com/smartmontools/smartmontools/blob/main/src/ataprint.cpp
		// See set_json_globals_from_smart_attrib's Wear_Leveling fallback.
		model:       regexp.MustCompile(`^Samsung SSD 860 (EVO( (mSATA|M\.2))?|PRO|QVO) ((250|256|500|512)G|[124]T)B$`),
		attributeID: 177,
		encoding:    wearoutNormalizedRemaining,
		maxPercent:  100,
	},
	{
		// Kingston DC500: the LOW RAW BYTE of 231 is percentage remaining;
		// 232 is read failures and 233 is NAND GiB written, not endurance.
		// https://media.kingston.com/support/pdf/MKP_521-7_SMART-DC500_attribute_hr.pdf
		model:       regexp.MustCompile(`^KINGSTON SEDC500[RM](480|960|1920|3840)G$`),
		attributeID: 231,
		encoding:    wearoutRawRemaining,
		rawMask:     0xff,
		maxPercent:  100,
	},
	{
		// Intel 335: E9 (233) is NORMALIZED life remaining, not raw cycles.
		// The firmware floors the normalized indicator at 1 after exhaustion.
		// https://download.intel.com/newsroom/kits/ssd/pdfs/Intel_SSD_335_Series_Product_Specification.pdf
		model:       regexp.MustCompile(`^INTEL SSDSC2CT(180|240)A4$`),
		attributeID: 233,
		encoding:    wearoutNormalizedRemaining,
		maxPercent:  100,
	},
	{
		// Intel DC S3500 uses the same normalized E9 remaining-life indicator.
		// https://www.thomas-krenn.com/en/wiki/SMART_attributes_of_Intel_SSDs
		// https://www.thomas-krenn.com/en/wiki/Intel_DC_S3500_Series_SSDs_information
		model:       regexp.MustCompile(`^INTEL SSDSC(1NB|2BB)(080|120|160|240|300|400|480|600|800)G4$`),
		attributeID: 233,
		encoding:    wearoutNormalizedRemaining,
		maxPercent:  100,
	},
	{
		// Crucial MX500 / Micron 1300: normalized 202 is remaining life, but
		// RAW 202 is life USED (drivedb: norm = max(100-raw, 0)). Keep usage
		// above 100 rather than pretending a saturated normalized 0 is exact.
		// https://github.com/smartmontools/smartmontools/blob/main/drivedb/drivedb.h
		// https://www.crucial.com/support/articles-faq-ssd/ssds-and-smart-data
		model:       regexp.MustCompile(`^(CT(250|500|[124]000)MX500SSD[14][A-Z]?|(Micron_1300_)?(EE|MT)FDDA[KV](256|512|1T0|2T0)TDL(-.*)?)$`),
		attributeID: 202,
		encoding:    wearoutRawUsed,
		maxPercent:  255,
	},
}

func getATAWearout(data diskServiceInterfaces.SmartData) (float64, error) {
	model := strings.TrimSpace(data.Model)
	firmware := strings.TrimSpace(data.Firmware)
	for _, rule := range ataWearoutShame {
		if !rule.model.MatchString(model) || rule.firmware != "" && rule.firmware != firmware {
			continue
		}
		for _, attr := range data.Attributes {
			if attr.ID == rule.attributeID {
				return rule.decode(attr)
			}
		}
		return 0, errors.New("supported SSD wearout attribute is missing")
	}
	return 0, errors.New("no verified SSD wearout interpretation for this model and firmware")
}

func (rule ataWearoutRule) decode(attr diskServiceInterfaces.ATASmartAttribute) (float64, error) {
	var value int64
	switch rule.encoding {
	case wearoutNormalizedRemaining, wearoutNormalizedUsed:
		if attr.State == smart.AttrStateNoNormVal {
			return 0, errors.New("SSD wearout normalized value is unavailable")
		}
		value = int64(attr.Value)
	case wearoutRawRemaining, wearoutRawUsed:
		value = attr.RawValue
		if value >= 0 && rule.rawMask != 0 {
			value &= rule.rawMask
		}
	default:
		return 0, errors.New("SSD wearout attribute interpretation is unverified")
	}
	if value < 0 || value > rule.maxPercent {
		return 0, errors.New("SSD wearout percentage is outside its documented range")
	}
	if rule.encoding == wearoutNormalizedRemaining || rule.encoding == wearoutRawRemaining {
		return 100 - float64(value), nil
	}
	return float64(value), nil
}
