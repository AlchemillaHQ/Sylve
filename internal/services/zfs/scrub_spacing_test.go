// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
)

func TestPoolScrubCooldownSurvivesPruningDeletionAndRestart(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	started, completed := b.now.Add(-2*time.Hour), b.now.Add(-time.Hour)
	finished := &zfsModels.PoolScrubRun{
		RunKey: "completed", PoolGUID: "101", PoolName: "tank", Source: "manual",
		RequestedAt: started, StartedAt: &started, CompletedAt: &completed, Status: "passed",
	}
	if err := s.savePoolScrubRun(t.Context(), finished); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < poolScrubHistoryLimit+1; i++ {
		run := &zfsModels.PoolScrubRun{
			RunKey: fmt.Sprintf("skipped-%d", i), PoolGUID: "101", PoolName: "tank", Source: "scheduled",
			RequestedAt: b.now.Add(time.Duration(i) * time.Second), CompletedAt: &b.now, Status: "skipped",
		}
		if err := s.savePoolScrubRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := s.DB.Model(&zfsModels.PoolScrubRun{}).Where("run_key = ?", "completed").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("completed-run fixture was not pruned: %d %v", count, err)
	}
	// A later native scan no longer exposes the completed scrub.
	b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "FINISHED"}
	id := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	restarted := &Service{DB: s.DB, scrubDriver: b, listHostPools: s.listHostPools}
	if err := restarted.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 0 {
		t.Fatalf("restart lost the cooldown: %v", err)
	}
	var schedule zfsModels.PoolScrubSchedule
	if err := s.DB.First(&schedule, id).Error; err != nil || schedule.LastStatus != "skipped" || !schedule.Enabled || schedule.QueuedAt != nil {
		t.Fatalf("cooldown occurrence was deferred: %+v %v", schedule, err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var cooldown zfsModels.PoolScrubCooldown
	if err := s.DB.Where("pool_guid = ?", "101").First(&cooldown).Error; err != nil || !cooldown.LastCompletedAt.Equal(completed) {
		t.Fatalf("skips or deletion changed the cooldown: %+v %v", cooldown, err)
	}
	b.now = completed.Add(poolScrubMinimumGap)
	createDuePoolScrub(t, s, "101", b.now)
	if err := restarted.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 1 {
		t.Fatalf("next eligible occurrence did not run: %v", err)
	}
}

func TestPoolScrubScheduledGapUsesOnlyActualCompletion(t *testing.T) {
	for _, test := range []struct {
		name, status string
		started      bool
		skipped      bool
	}{
		{"passed", "passed", true, true},
		{"errors", "failed", true, true},
		{"cancelled", "cancelled", true, true},
		{"failed-start", "failed", false, false},
		{"unknown", "unknown", true, false},
		{"skipped", "skipped", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, b := newPoolScrubTestService(t)
			started, completed := b.now.Add(-2*time.Hour), b.now.Add(-time.Hour)
			run := &zfsModels.PoolScrubRun{
				RunKey: "previous", PoolGUID: "101", PoolName: "tank", Source: "manual",
				RequestedAt: started, CompletedAt: &completed, Status: test.status,
			}
			if test.started {
				run.StartedAt = &started
			}
			if err := s.savePoolScrubRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			id := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
			if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
				t.Fatal(err)
			}
			var schedule zfsModels.PoolScrubSchedule
			if err := s.DB.First(&schedule, id).Error; err != nil {
				t.Fatal(err)
			}
			if test.skipped {
				if schedule.LastStatus != "skipped" || len(b.starts) != 0 {
					t.Fatalf("actual completion did not suppress the occurrence: %+v", schedule)
				}
				if err := s.ScrubPool(t.Context(), "101"); err != nil || len(b.starts) != 1 {
					t.Fatalf("manual override was blocked by cooldown: %v", err)
				}
			} else if schedule.LastStatus != "running" || len(b.starts) != 1 {
				t.Fatalf("bookkeeping completion falsely extended cooldown: %+v", schedule)
			}
		})
	}
}

func TestPoolScrubQueuedOverlapIsSkippedEvenAfterCooldownExpires(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	completed := b.now.Add(-48 * time.Hour)
	if err := db.RecordPoolScrubCompletion(s.DB, "101", completed); err != nil {
		t.Fatal(err)
	}
	due := b.now.Add(-72 * time.Hour)
	first := createDuePoolScrub(t, s, "101", b.now.Add(time.Hour))
	second := createDuePoolScrub(t, s, "202", b.now.Add(time.Hour))
	if err := s.DB.Model(&zfsModels.PoolScrubSchedule{}).Where("id IN ?", []uint{first, second}).Updates(map[string]any{
		"last_status": "queued", "scheduled_for": due, "queued_at": due,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	views, err := s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 2 || views[0].LastStatus != "skipped" || views[1].LastStatus != "running" || len(b.starts) != 1 || b.starts[0] != "202" {
		t.Fatalf("redundant queue was delayed or another pool's old queue expired: %+v %v", views, err)
	}
}

func TestPoolScrubScheduledGapObservesExternalScans(t *testing.T) {
	for _, test := range []struct {
		name, function, state string
		endAge                time.Duration
		badEnd                bool
		want                  string
	}{
		{"active", "SCRUB", "SCANNING", 0, false, "skipped"},
		{"paused", "SCRUB", "PAUSED", 0, false, "skipped"},
		{"error-scrub", "ERRORSCRUB", "ERRORSCRUBBING", 0, false, "skipped"},
		{"recent-completion", "SCRUB", "FINISHED", time.Hour, false, "skipped"},
		{"recent-cancellation", "SCRUB", "CANCELED", time.Hour, false, "skipped"},
		{"old-completion", "SCRUB", "FINISHED", 25 * time.Hour, false, "running"},
		{"unknown-completion-time", "SCRUB", "FINISHED", time.Hour, true, "queued"},
		{"future-completion", "SCRUB", "FINISHED", -time.Hour, false, "queued"},
		{"resilver", "RESILVER", "SCANNING", 0, false, "queued"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, b := newPoolScrubTestService(t)
			b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{
				Function: test.function, State: test.state,
				StartTime: fmt.Sprint(b.now.Add(-48 * time.Hour).Unix()),
				EndTime:   fmt.Sprint(b.now.Add(-test.endAge).Unix()),
			}
			if test.badEnd {
				b.pools["101"].ScanStats.EndTime = "?"
			}
			id := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
			if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
				t.Fatal(err)
			}
			var schedule zfsModels.PoolScrubSchedule
			if err := s.DB.First(&schedule, id).Error; err != nil || schedule.LastStatus != test.want {
				t.Fatalf("native scan policy: %+v %v, want %s", schedule, err, test.want)
			}
			if test.want != "running" && len(b.starts) != 0 {
				t.Fatalf("started during an overlap or without usable completion data: %v", b.starts)
			}
			if test.want == "skipped" {
				runs, err := s.ListPoolScrubRuns(t.Context(), "101")
				if err != nil || len(runs) != 1 || runs[0].Status != "skipped" || runs[0].StartedAt != nil || runs[0].Errors != nil {
					t.Fatalf("external scan invented tracked history: %+v %v", runs, err)
				}
			}
		})
	}
}

func TestPoolScrubExternalOverlapIsObservedWhileNodeSlotIsBusy(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	createDuePoolScrub(t, s, "202", b.now.Add(-time.Minute))
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{
		Function: "SCRUB", State: "SCANNING", StartTime: fmt.Sprint(b.now.Add(-time.Hour).Unix()),
	}
	id := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	var schedule zfsModels.PoolScrubSchedule
	if err := s.DB.First(&schedule, id).Error; err != nil || schedule.LastStatus != "skipped" || len(b.starts) != 1 || b.starts[0] != "202" {
		t.Fatalf("node serialization deferred an external overlap into another scrub: %+v %v", schedule, err)
	}
}

func TestPoolScrubSkipPersistenceIsAtomic(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if err := db.RecordPoolScrubCompletion(s.DB, "101", b.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	due := b.now.Add(-time.Minute)
	id := createDuePoolScrub(t, s, "101", due)
	if err := s.DB.Exec("CREATE TRIGGER deny_skipped_scrub BEFORE INSERT ON pool_scrub_runs BEGIN SELECT RAISE(ABORT, 'disk full'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPoolScrubTick(t.Context(), b.now); err == nil || len(b.starts) != 0 {
		t.Fatal("skip persistence failure issued a command or lost its error")
	}
	var schedule zfsModels.PoolScrubSchedule
	if err := s.DB.First(&schedule, id).Error; err != nil || schedule.LastStatus != "idle" || schedule.NextRunAt == nil || !schedule.NextRunAt.Equal(due) {
		t.Fatalf("failed skip advanced the schedule without history: %+v %v", schedule, err)
	}
	if err := s.DB.Exec("DROP TRIGGER deny_skipped_scrub").Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.ListPoolScrubRuns(t.Context(), "101")
	if err != nil || len(runs) != 1 || runs[0].Status != "skipped" || len(b.starts) != 0 {
		t.Fatalf("skip retry duplicated history or issued a command: %+v %v", runs, err)
	}
}
