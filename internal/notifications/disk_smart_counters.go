// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import "math/big"

const DiskSmartCounterCondition = "historical_counter"

const (
	DiskSmartCounterReallocated         = "ata_reallocated_sectors"
	DiskSmartCounterNvmeMediaErrors     = "nvme_media_errors"
	DiskSmartCounterScsiReadErrors      = "scsi_read_uncorrected_errors"
	DiskSmartCounterScsiWriteErrors     = "scsi_write_uncorrected_errors"
	DiskSmartCounterScsiVerifyErrors    = "scsi_verify_uncorrected_errors"
	DiskSmartCounterScsiNonMediumErrors = "scsi_non_medium_uncorrected_errors"
)

type DiskSmartCounter struct {
	Key        string
	Label      string
	KindPrefix string
}

// Only counters with known historical semantics belong here. ATA pending and
// offline-uncorrectable sectors are deliberately not treated as lifetime totals.
var diskSmartCounters = map[string]DiskSmartCounter{
	DiskSmartCounterReallocated:         {DiskSmartCounterReallocated, "Reallocated sectors", DiskSmartHealthKindPrefix},
	DiskSmartCounterNvmeMediaErrors:     {DiskSmartCounterNvmeMediaErrors, "NVMe media/data-integrity errors", DiskSmartNvmeKindPrefix},
	DiskSmartCounterScsiReadErrors:      {DiskSmartCounterScsiReadErrors, "SCSI read uncorrected errors", DiskSmartHealthKindPrefix},
	DiskSmartCounterScsiWriteErrors:     {DiskSmartCounterScsiWriteErrors, "SCSI write uncorrected errors", DiskSmartHealthKindPrefix},
	DiskSmartCounterScsiVerifyErrors:    {DiskSmartCounterScsiVerifyErrors, "SCSI verify uncorrected errors", DiskSmartHealthKindPrefix},
	DiskSmartCounterScsiNonMediumErrors: {DiskSmartCounterScsiNonMediumErrors, "SCSI non-medium uncorrected errors", DiskSmartHealthKindPrefix},
}

func DiskSmartCounterByKey(key string) (DiskSmartCounter, bool) {
	counter, ok := diskSmartCounters[key]
	return counter, ok
}

// NVMe historical counters are unsigned 128-bit values. Keep decimal text in
// notifications and storage rather than comparing rounded or saturated numbers.
func ParseDiskSmartCounter(value string) (*big.Int, bool) {
	if value == "" || len(value) > 39 {
		return nil, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return nil, false
		}
	}
	number, ok := new(big.Int).SetString(value, 10)
	return number, ok && number.BitLen() <= 128
}
