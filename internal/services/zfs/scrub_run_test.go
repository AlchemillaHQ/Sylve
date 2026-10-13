// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	gzfsTestutil "github.com/alchemillahq/gzfs/testutil"
	"github.com/alchemillahq/sylve/internal/db"
)

// OpenZFS omits scan_stats when the pool has no recorded scan.
const unscannedPoolStatusJSON = `{
  "output_version": {"command": "zpool status", "vers_major": 0, "vers_minor": 1},
  "pools": {
    "tank": {
      "name": "tank", "state": "ONLINE", "pool_guid": "101", "txg": "1",
      "spa_version": "5000", "zpl_version": "5",
      "vdevs": {
        "tank": {
          "name": "tank", "vdev_type": "root", "guid": "101", "state": "ONLINE",
          "vdevs": {
            "disk0": {"name": "disk0", "vdev_type": "disk", "guid": "202", "state": "ONLINE"}
          }
        }
      }
    }
  }
}`

func poolScrubJSONRunner(statusJSON string) *gzfsTestutil.MockRunner {
	runner := gzfsTestutil.NewMockRunner()
	const poolJSON = `{"pools":{"tank":{"name":"tank","pool_guid":"101"}}}`
	runner.AddCommand("zpool list -o guid -p -P -j", poolJSON, "", nil)
	runner.AddCommand("zpool list -o all -v -p tank -P -j", poolJSON, "", nil)
	runner.AddCommand("zpool status -p tank -P -v -j", statusJSON, "", nil)
	runner.AddCommand("zpool scrub tank", "", "", nil)
	return runner
}

func unscannedPoolStatus(t *testing.T) *gzfs.ZPoolStatusPool {
	t.Helper()
	var status gzfs.ZPoolStatus
	if err := json.Unmarshal([]byte(unscannedPoolStatusJSON), &status); err != nil {
		t.Fatal(err)
	}
	return status.Pools["tank"]
}

func TestPoolScrubStartBaselineWithoutRecordedScan(t *testing.T) {
	status := unscannedPoolStatus(t)
	baseline, err := poolScrubStartBaseline(status, "101")
	if err != nil || baseline == nil || baseline.Function != "NONE" || baseline.State != "NONE" || baseline.StartTime != "0" {
		t.Fatalf("no-scan baseline: %+v, %v", baseline, err)
	}
	if status.ScanStats != nil {
		t.Fatalf("baseline normalization changed the observed status: %+v", status.ScanStats)
	}
	if err := validatePoolScrubStatus(status, "101"); !errors.Is(err, ErrScrubStatusUnavailable) {
		t.Fatalf("missing scan data became a usable run observation: %v", err)
	}
}

func TestPoolScrubStartBaselineRejectsIncompleteAndActiveStatus(t *testing.T) {
	if _, err := poolScrubStartBaseline(nil, "101"); !errors.Is(err, ErrScrubStatusUnavailable) {
		t.Fatalf("missing status: %v", err)
	}
	for _, test := range []struct {
		name   string
		modify func(*gzfs.ZPoolStatusPool)
		want   error
	}{
		{"missing-guid", func(s *gzfs.ZPoolStatusPool) { s.PoolGUID = "" }, ErrScrubStatusUnavailable},
		{"different-guid", func(s *gzfs.ZPoolStatusPool) { s.PoolGUID = "303" }, ErrScrubStatusUnavailable},
		{"missing-name", func(s *gzfs.ZPoolStatusPool) { s.Name = "" }, ErrScrubStatusUnavailable},
		{"missing-pool-state", func(s *gzfs.ZPoolStatusPool) { s.State = "" }, ErrScrubStatusUnavailable},
		{"missing-configuration", func(s *gzfs.ZPoolStatusPool) { s.Vdevs = nil }, ErrScrubStatusUnavailable},
		{"different-root-guid", func(s *gzfs.ZPoolStatusPool) { s.Vdevs["tank"].GUID = "303" }, ErrScrubStatusUnavailable},
		{"not-root-vdev", func(s *gzfs.ZPoolStatusPool) { s.Vdevs["tank"].VdevType = "disk" }, ErrScrubStatusUnavailable},
		{"empty-scan", func(s *gzfs.ZPoolStatusPool) { s.ScanStats = &gzfs.ZPoolStatusScanStats{} }, ErrScrubStatusUnavailable},
		{"unknown-scan", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "UNKNOWN"}
		}, ErrScrubStatusUnavailable},
		{"scrub", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "SCANNING"}
		}, ErrConflict},
		{"paused-scrub", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "SCANNING", ScrubPause: "1"}
		}, ErrConflict},
		{"paused-state", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "PAUSED"}
		}, ErrConflict},
		{"resilver", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "SCANNING"}
		}, ErrConflict},
		{"error-scrub", func(s *gzfs.ZPoolStatusPool) {
			s.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "ERRORSCRUB", State: "ERRORSCRUBBING"}
		}, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := unscannedPoolStatus(t)
			test.modify(status)
			if _, err := poolScrubStartBaseline(status, "101"); !errors.Is(err, test.want) {
				t.Fatalf("baseline error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPoolScrubLibraryStartWithoutRecordedScan(t *testing.T) {
	for _, source := range []string{"manual", "scheduled"} {
		t.Run(source, func(t *testing.T) {
			s, backend := newPoolScrubTestService(t)
			runner := poolScrubJSONRunner(unscannedPoolStatusJSON)
			s.scrubDriver = nil
			s.GZFS = gzfs.NewClient(gzfs.Options{Runner: runner})
			var err error
			if source == "scheduled" {
				createDuePoolScrub(t, s, "101", backend.now.Add(-time.Minute))
				err = s.runPoolScrubTick(t.Context(), backend.now)
			} else {
				err = s.ScrubPool(t.Context(), "101")
			}
			if err != nil {
				t.Fatalf("start with native no-scan JSON: %v", err)
			}
			runs, err := s.ListPoolScrubRuns(t.Context(), "101")
			if err != nil || len(runs) != 1 {
				t.Fatalf("start intent not persisted: %+v, %v", runs, err)
			}
			if runs[0].Source != source || runs[0].Status != "starting" || runs[0].BaselineStart != nil || runs[0].StartedAt != nil || runs[0].Errors != nil {
				t.Fatalf("missing scan data invented a result: %+v", runs[0])
			}
			if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, db.ErrPoolScrubActive) {
				t.Fatalf("pending intent did not prevent another start: %v", err)
			}
			if err := s.runPoolScrubTick(t.Context(), backend.now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			starts := 0
			for _, call := range runner.CallHistory {
				if call.Cmd == "zpool scrub tank" {
					starts++
				}
			}
			if starts != 1 {
				t.Fatalf("scrub command calls = %d, want 1", starts)
			}
		})
	}
}

func TestPoolScrubLibraryRechecksUnscannedPoolBeforeStart(t *testing.T) {
	for _, test := range []struct {
		name, guid, state string
		want              error
	}{
		{"active-scan", "101", "SCANNING", ErrConflict},
		{"changed-scan", "101", "FINISHED", ErrConflict},
		{"recreated-pool", "303", "", ErrScrubStatusUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline, err := poolScrubStartBaseline(unscannedPoolStatus(t), "101")
			if err != nil {
				t.Fatal(err)
			}
			current := unscannedPoolStatus(t)
			current.PoolGUID = test.guid
			current.Vdevs["tank"].GUID = test.guid
			if test.state != "" {
				current.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: test.state, StartTime: "1"}
			}
			statusJSON, err := json.Marshal(gzfs.ZPoolStatus{Pools: map[string]*gzfs.ZPoolStatusPool{"tank": current}})
			if err != nil {
				t.Fatal(err)
			}
			runner := poolScrubJSONRunner(string(statusJSON))
			service := &Service{GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}
			if err := (libraryPoolScrubBackend{service: service}).Start(t.Context(), "101", baseline); !errors.Is(err, test.want) {
				t.Fatalf("fresh start guard = %v, want %v", err, test.want)
			}
			for _, call := range runner.CallHistory {
				if call.Cmd == "zpool scrub tank" {
					t.Fatal("issued scrub after the pool or scan changed")
				}
			}
		})
	}
}

func TestPoolScrubMissingScanObservationKeepsReservation(t *testing.T) {
	s, backend := newPoolScrubTestService(t)
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	backend.pools["101"] = unscannedPoolStatus(t)
	if err := s.runPoolScrubTick(t.Context(), backend.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListPoolScrubRuns(t.Context(), "101")
	if err != nil || len(runs) != 1 {
		t.Fatalf("lost run: %+v, %v", runs, err)
	}
	if runs[0].Status != "running" || runs[0].ObservationLostAt == nil || runs[0].Error == "" || runs[0].CompletedAt != nil {
		t.Fatalf("missing scan data was treated as an idle or completed run: %+v", runs[0])
	}
	if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, db.ErrPoolScrubActive) || len(backend.starts) != 1 {
		t.Fatalf("missing observation allowed another start: %v, %v", err, backend.starts)
	}
}
