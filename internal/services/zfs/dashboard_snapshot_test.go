// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestSummarizeDashboardPoolStatus(t *testing.T) {
	status := &gzfs.ZPoolStatusPool{
		Vdevs: map[string]*gzfs.ZPoolStatusVDEV{
			"mirror-0": {
				Vdevs: map[string]*gzfs.ZPoolStatusVDEV{
					"disk-1": {ReadErrors: "2", WriteErrors: "3", ChkErrors: "4"},
					"disk-2": {ReadErrors: "1", WriteErrors: "0", ChkErrors: "5"},
				},
			},
		},
		L2Cache: map[string]*gzfs.ZPoolStatusVDEV{
			"cache-0": {ReadErrors: "0", WriteErrors: "1", ChkErrors: "0"},
		},
		ScanStats: &gzfs.ZPoolStatusScanStats{
			Function:  "scrub",
			State:     "SCANNING",
			ToExamine: "1000",
			Issued:    "250",
			Examined:  "300",
			Errors:    "6",
		},
	}

	errors, scan, topology := summarizeDashboardPoolStatus(status)
	if errors.Read != 3 || errors.Write != 4 || errors.Checksum != 9 || errors.Scan != 6 {
		t.Fatalf("unexpected error summary: %+v", errors)
	}
	if topology.DataVDEVs != 1 || topology.Cache != 1 || topology.Disks != 3 {
		t.Fatalf("unexpected topology summary: %+v", topology)
	}
	if scan == nil || scan.ProgressPercent == nil || *scan.ProgressPercent != 25 {
		t.Fatalf("unexpected scan summary: %+v", scan)
	}
}

func TestDashboardScanSummaryUsesResilverProgress(t *testing.T) {
	scan := dashboardScanSummary(&gzfs.ZPoolStatusScanStats{
		Function:  "resilver",
		State:     "SCANNING",
		ToExamine: "200",
		Processed: "100",
	})
	if scan == nil || scan.ProgressPercent == nil || *scan.ProgressPercent != 50 {
		t.Fatalf("unexpected resilver progress: %+v", scan)
	}
}
