// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type fakePoolScrubBackend struct {
	pools    map[string]*gzfs.ZPoolStatusPool
	starts   []string
	now      time.Time
	startErr error
	readErr  error
	onStart  func()
}

func (b *fakePoolScrubBackend) Read(_ context.Context, guid string) (*gzfs.ZPoolStatusPool, error) {
	if b.readErr != nil {
		return nil, b.readErr
	}
	return b.pools[guid], nil
}

func (b *fakePoolScrubBackend) Start(_ context.Context, guid string, _ *gzfs.ZPoolStatusScanStats) error {
	b.starts = append(b.starts, guid)
	if b.startErr == nil {
		b.pools[guid].ScanStats = &gzfs.ZPoolStatusScanStats{
			Function: "SCRUB", State: "SCANNING", StartTime: fmt.Sprint(b.now.Unix()), EndTime: "0",
			Errors: "0", Processed: "0", Examined: "20", Issued: "10", ToExamine: "100", ScrubPause: "0",
		}
	}
	if b.onStart != nil {
		b.onStart()
	}
	return b.startErr
}

func newPoolScrubTestService(t *testing.T) (*Service, *fakePoolScrubBackend) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{},
		&zfsModels.PoolScrubSchedule{}, &zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubEvent{}, &zfsModels.PoolScrubSchedulerLease{}, &zfsModels.PoolScrubCooldown{})
	if err := database.Create(&models.BasicSettings{Pools: []string{"tank", "vault"}}).Error; err != nil {
		t.Fatal(err)
	}
	backend := &fakePoolScrubBackend{now: time.Now().UTC().Truncate(time.Second), pools: map[string]*gzfs.ZPoolStatusPool{
		"101": {PoolGUID: "101", Name: "tank", ScanStats: &gzfs.ZPoolStatusScanStats{Function: "NONE", State: "NONE", StartTime: "0"}},
		"202": {PoolGUID: "202", Name: "vault", ScanStats: &gzfs.ZPoolStatusScanStats{Function: "NONE", State: "NONE", StartTime: "0"}},
	}}
	service := &Service{DB: database, scrubDriver: backend, listHostPools: func(context.Context) ([]*gzfs.ZPool, error) {
		pools := make([]*gzfs.ZPool, 0, len(backend.pools))
		for _, pool := range backend.pools {
			pools = append(pools, &gzfs.ZPool{PoolGUID: pool.PoolGUID, Name: pool.Name})
		}
		return pools, nil
	}}
	return service, backend
}

func createDuePoolScrub(t *testing.T, s *Service, guid string, due time.Time) uint {
	t.Helper()
	view, err := s.CreatePoolScrubSchedule(t.Context(), PoolScrubScheduleInput{PoolGUID: guid, CronExpr: "0 3 1 * *", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&zfsModels.PoolScrubSchedule{}).Where("id = ?", view.ID).Update("next_run_at", due).Error; err != nil {
		t.Fatal(err)
	}
	return view.ID
}

func TestPoolScrubScheduleCRUDAndScope(t *testing.T) {
	s, backend := newPoolScrubTestService(t)
	for _, expr := range []string{"", "* * *", "TZ=UTC 0 3 * * 0", "CRON_TZ=UTC 0 3 * * 0", "@every 1s", "0 0 31 2 *", strings.Repeat("x", 257)} {
		if _, err := parsePoolScrubSchedule(expr); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("accepted invalid cron %q: %v", expr, err)
		}
	}
	for _, guid := range []string{"", "0", "00101", "-1", "18446744073709551616", "pool-name"} {
		if err := validateScrubGUID(guid); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("accepted GUID %q", guid)
		}
	}
	view, err := s.CreatePoolScrubSchedule(t.Context(), PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0", Enabled: false})
	if err != nil || view.Enabled || view.NextRunAt == nil || view.NextRunAt.Location() != time.UTC {
		t.Fatalf("disabled schedule: %+v %v", view, err)
	}
	sibling, err := s.CreatePoolScrubSchedule(t.Context(), PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0", Enabled: true})
	if err != nil || sibling.ID == view.ID || !sibling.Enabled {
		t.Fatalf("second schedule with the same cron: %+v %v", sibling, err)
	}
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), view.ID, PoolScrubScheduleInput{PoolGUID: "202", CronExpr: "0 3 * * 0"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("retarget: %v", err)
	}
	delete(backend.pools, "101")
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), view.ID, PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 4 * * 0", Enabled: false}); err != nil {
		t.Fatalf("disable unavailable: %v", err)
	}
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), view.ID, PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 4 * * 0", Enabled: true}); !errors.Is(err, ErrPoolNotFound) {
		t.Fatalf("enable unavailable: %v", err)
	}
	backend.pools["303"] = &gzfs.ZPoolStatusPool{Name: "tank", PoolGUID: "303"}
	if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, ErrPoolNotFound) || len(backend.starts) != 0 {
		t.Fatalf("same-name recreation used old identity: %v", err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), view.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), view.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	views, err := s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 1 || views[0].ID != sibling.ID || !views[0].Enabled || views[0].CronExpr != "0 3 * * 0" {
		t.Fatalf("mutating one schedule changed its sibling: %+v %v", views, err)
	}
}

func TestPoolScrubSamePoolSchedulesSkipOverlapsAndKeepIndependentState(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	first := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	second := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	views, err := s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 2 || views[0].LastStatus != "running" || views[1].LastStatus != "skipped" || !views[0].Active || !views[1].Active || views[1].QueuedAt != nil || !views[1].Enabled {
		t.Fatalf("same-pool schedule state: %+v %v", views, err)
	}
	if len(b.starts) != 1 {
		t.Fatalf("overlapping schedules issued concurrent starts: %v", b.starts)
	}
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), second, PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0"}); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("edited a sibling while the pool was active: %v", err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), second); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("deleted a sibling while the pool was active: %v", err)
	}
	third, err := s.CreatePoolScrubSchedule(t.Context(), PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0", Enabled: false})
	if err != nil || !third.Active || third.LastStatus != "idle" {
		t.Fatalf("create during an active scrub: %+v %v", third, err)
	}
	if err := s.DB.Model(&zfsModels.PoolScrubSchedule{}).Where("id IN ?", []uint{first, second}).Update("next_run_at", b.now.Add(48*time.Hour-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	b.pools["101"].ScanStats.ScrubPause = fmt.Sprint(b.now.Unix())
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	views, err = s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 3 || views[0].LastStatus != "paused" || views[1].LastStatus != "skipped" || views[2].LastStatus != "idle" || len(b.starts) != 1 {
		t.Fatalf("skipped occurrences overwrote active progress or restarted a scrub: %+v %v", views, err)
	}
	b.now = b.now.Add(48 * time.Hour)
	b.pools["101"].ScanStats.State = "FINISHED"
	b.pools["101"].ScanStats.EndTime = fmt.Sprint(b.now.Unix())
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListPoolScrubRuns(t.Context(), "101")
	if err != nil || len(runs) != 4 {
		t.Fatalf("history lost overlapping occurrences: %+v %v", runs, err)
	}
	skipped := 0
	for _, run := range runs {
		if run.ScheduleID == nil {
			t.Fatalf("history lost schedule attribution: %+v", run)
		}
		if run.Status == "skipped" {
			skipped++
			if run.StartedAt != nil || run.Errors != nil || run.RepairedBytes != nil || run.Error == "" {
				t.Fatalf("skipped occurrence invented a scan result: %+v", run)
			}
		} else if run.Status != "passed" || *run.ScheduleID != first {
			t.Fatalf("overlap altered the original scrub: %+v", run)
		}
	}
	if skipped != 3 {
		t.Fatalf("skipped occurrences: %d, want 3", skipped)
	}
	var eventCount int64
	if err := s.DB.Model(&zfsModels.PoolScrubEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 {
		t.Fatalf("skips generated failure notifications: %d events", eventCount)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	views, err = s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 2 || views[0].ID != second || views[0].LastStatus != "skipped" || views[1].ID != third.ID || views[1].Enabled {
		t.Fatalf("deleting a schedule changed its siblings: %+v %v", views, err)
	}
	completed := b.now
	for _, gap := range []time.Duration{poolScrubMinimumGap - time.Second, poolScrubMinimumGap} {
		b.now = completed.Add(gap)
		if err := s.DB.Model(&zfsModels.PoolScrubSchedule{}).Where("id = ?", second).Update("next_run_at", b.now).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
			t.Fatal(err)
		}
		want := 1
		if gap == poolScrubMinimumGap {
			want = 2
		}
		if len(b.starts) != want {
			t.Fatalf("completion-based cooldown after %s: %v", gap, b.starts)
		}
	}
}

func TestPoolScrubQueuedScheduleMutationLeavesSiblingReserved(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	first := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	second := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "SCANNING"}
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 0 {
		t.Fatalf("schedules overrode an external scan: %v", err)
	}
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), first, PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	views, err := s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 1 || views[0].ID != second || !views[0].Enabled || views[0].LastStatus != "queued" || views[0].QueuedAt == nil {
		t.Fatalf("sibling reservation was cleared: %+v %v", views, err)
	}
	b.pools["101"].ScanStats.State = "FINISHED"
	if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, ErrConflict) || len(b.starts) != 0 {
		t.Fatalf("manual start bypassed the remaining sibling queue: %v", err)
	}
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListPoolScrubRuns(t.Context(), "101")
	if err != nil || len(runs) != 1 || runs[0].ScheduleID == nil || *runs[0].ScheduleID != second || len(b.starts) != 1 {
		t.Fatalf("remaining sibling did not start: %+v %v", runs, err)
	}
}

func TestPoolScrubScheduleSerializationAndManualIsolation(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	first := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	second := createDuePoolScrub(t, s, "202", b.now.Add(-time.Minute))
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 || b.starts[0] != "101" {
		t.Fatalf("scheduled starts: %v", b.starts)
	}
	if err := s.DB.Model(&zfsModels.PoolScrubSchedule{}).Where("id = ?", first).Update("next_run_at", b.now.Add(72*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("manual active-pool start: %v", err)
	}
	if err := s.ScrubPool(t.Context(), "202"); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual queued-pool start: %v", err)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), first); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("delete active: %v", err)
	}
	b.pools["101"].ScanStats.ScrubPause = fmt.Sprint(b.now.Unix())
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 {
		t.Fatalf("paused scrub released scheduled slot: %v", b.starts)
	}
	var queued zfsModels.PoolScrubSchedule
	s.DB.First(&queued, second)
	if queued.LastStatus != "queued" {
		t.Fatalf("long wait expired: %+v", queued)
	}
	b.pools["101"].ScanStats.State = "FINISHED"
	b.pools["101"].ScanStats.EndTime = fmt.Sprint(b.now.Add(time.Hour).Unix())
	b.now = b.now.Add(48 * time.Hour)
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 2 || b.starts[1] != "202" {
		t.Fatalf("queue did not advance: %v", b.starts)
	}
	if err := s.DeletePoolScrubSchedule(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListPoolScrubRuns(t.Context(), "101")
	if err != nil || len(runs) != 1 || runs[0].Status != "passed" {
		t.Fatalf("deleted schedule erased history: %+v %v", runs, err)
	}
}

func TestPoolScrubManualStartsOnIndependentPools(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	if err := s.ScrubPool(t.Context(), "202"); err != nil || len(b.starts) != 2 {
		t.Fatalf("unrelated manual scrub blocked: %v", err)
	}
}

func TestPoolScrubCatchupAndCoalescing(t *testing.T) {
	for _, age := range []time.Duration{23 * time.Hour, 25 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			s, b := newPoolScrubTestService(t)
			createDuePoolScrub(t, s, "101", b.now.Add(-age))
			if err := s.runPoolScrubTick(t.Context(), b.now); err != nil {
				t.Fatal(err)
			}
			runs, _ := s.ListPoolScrubRuns(t.Context(), "101")
			if age < poolScrubCatchupWindow && len(b.starts) != 1 || age > poolScrubCatchupWindow && (len(b.starts) != 0 || runs[0].Status != "missed") {
				t.Fatalf("catchup: starts=%v runs=%+v", b.starts, runs)
			}
		})
	}
	parsed, err := parsePoolScrubSchedule("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	if due := latestDuePoolScrub(parsed, now.AddDate(-5, 0, 0), now); !due.Equal(now) {
		t.Fatalf("unbounded backlog or wrong latest occurrence: %s", due)
	}
}

func TestPoolScrubWaitsForExternalScanAndQueuedDisable(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	id := createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "SCANNING"}
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 0 {
		t.Fatalf("external resilver overridden: %v", err)
	}
	if _, err := s.UpdatePoolScrubSchedule(t.Context(), id, PoolScrubScheduleInput{PoolGUID: "101", CronExpr: "0 3 * * 0", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	b.pools["101"].ScanStats.State = "FINISHED"
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 0 {
		t.Fatalf("disabled queued scrub executed: %v", err)
	}
}

func TestPoolScrubPersistsIntentAndRecoversWithoutRestarting(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if err := s.DB.Exec("CREATE TRIGGER deny_scrub_insert BEFORE INSERT ON pool_scrub_runs BEGIN SELECT RAISE(ABORT, 'disk full'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ScrubPool(t.Context(), "101"); err == nil || len(b.starts) != 0 {
		t.Fatal("issued command without durable intent")
	}
	s.DB.Exec("DROP TRIGGER deny_scrub_insert")
	b.onStart = func() {
		if err := s.DB.Exec("CREATE TRIGGER deny_scrub_update BEFORE UPDATE ON pool_scrub_runs BEGIN SELECT RAISE(ABORT, 'disk full'); END").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ScrubPool(t.Context(), "101"); err == nil || len(b.starts) != 1 {
		t.Fatalf("expected acknowledgement write failure: %v", err)
	}
	s.DB.Exec("DROP TRIGGER deny_scrub_update")
	copyService := &Service{DB: s.DB, scrubDriver: b, listHostPools: s.listHostPools}
	if err := copyService.runPoolScrubTick(t.Context(), b.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.ListPoolScrubRuns(t.Context(), "101")
	if len(b.starts) != 1 || len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("recovery restarted or lost run: %v %+v", b.starts, runs)
	}
}

func TestPoolScrubResultCorrelation(t *testing.T) {
	for _, scenario := range []string{"clean", "errors", "cancelled", "malformed-errors", "resilver", "old-result", "paused", "unknown-progress", "identity-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := newPoolScrubTestService(t)
			if scenario == "old-result" {
				b.startErr = context.DeadlineExceeded
				b.pools["101"].ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "FINISHED", StartTime: "1", EndTime: "2", Errors: "0"}
			}
			err := s.ScrubPool(t.Context(), "101")
			if err != nil && scenario != "old-result" {
				t.Fatal(err)
			}
			scan := b.pools["101"].ScanStats
			want := "passed"
			scan.State, scan.EndTime = "FINISHED", fmt.Sprint(b.now.Add(time.Minute).Unix())
			switch scenario {
			case "errors":
				scan.Errors, want = "4", "failed"
			case "cancelled":
				scan.State, want = "CANCELED", "cancelled"
			case "malformed-errors":
				scan.Errors, want = "invalid", "unknown"
			case "resilver":
				scan.Function, want = "RESILVER", "unknown"
			case "old-result":
				want = "unknown"
			case "paused":
				scan.State, scan.ScrubPause, want = "SCANNING", fmt.Sprint(b.now.Unix()), "paused"
			case "unknown-progress":
				scan.State, scan.ToExamine, want = "SCANNING", "?", "running"
			case "identity-mismatch":
				b.pools["101"].PoolGUID, want = "303", "running"
			}
			if err := s.runPoolScrubTick(t.Context(), b.now.Add(10*time.Minute)); err != nil {
				t.Fatal(err)
			}
			runs, _ := s.ListPoolScrubRuns(t.Context(), "101")
			if len(runs) != 1 || runs[0].Status != want || scenario == "unknown-progress" && runs[0].ProgressKnown {
				t.Fatalf("result: %+v, want %s", runs, want)
			}
			if len(b.starts) != 1 {
				t.Fatalf("restarted ambiguous run: %v", b.starts)
			}
		})
	}
}

func TestPoolScrubScopeRemovalIsAtomicAndRetainsHistory(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	id := createDuePoolScrub(t, s, "101", b.now.Add(time.Hour))
	sibling := createDuePoolScrub(t, s, "101", b.now.Add(time.Hour))
	if err := s.WithUsablePoolsChange(t.Context(), []string{"vault"}, func(*gorm.DB) error { return errors.New("settings failure") }); err == nil {
		t.Fatal("expected settings failure")
	}
	var record zfsModels.PoolScrubSchedule
	for _, scheduleID := range []uint{id, sibling} {
		record = zfsModels.PoolScrubSchedule{}
		if err := s.DB.First(&record, scheduleID).Error; err != nil {
			t.Fatal(err)
		}
		if !record.Enabled {
			t.Fatal("failed settings write disabled a schedule")
		}
	}
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	if err := s.WithUsablePoolsChange(t.Context(), []string{"vault"}, func(*gorm.DB) error { return nil }); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("removed actively scrubbing pool: %v", err)
	}
	if err := s.DeletePool(t.Context(), "101"); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("destroy bypassed active tracking: %v", err)
	}
	b.pools["101"].ScanStats.State, b.pools["101"].ScanStats.EndTime = "FINISHED", fmt.Sprint(b.now.Add(time.Minute).Unix())
	s.runPoolScrubTick(t.Context(), b.now.Add(2*time.Minute))
	if err := s.WithUsablePoolsChange(t.Context(), []string{"vault"}, func(tx *gorm.DB) error {
		return tx.Model(&models.BasicSettings{}).Where("id = 1").Update("pools", []string{"vault"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, scheduleID := range []uint{id, sibling} {
		record = zfsModels.PoolScrubSchedule{}
		if err := s.DB.First(&record, scheduleID).Error; err != nil {
			t.Fatal(err)
		}
		if record.Enabled || record.QueuedAt != nil || record.ScheduledFor != nil || record.NextRunAt != nil {
			t.Fatalf("removed pool schedule retained execution: %+v", record)
		}
	}
	runs, _ := s.ListPoolScrubRuns(t.Context(), "101")
	if len(runs) != 1 || runs[0].Status != "passed" {
		t.Fatalf("history lost: %+v", runs)
	}
}

func TestPoolScrubDispatchAndLeaseRecovery(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	s.SetScrubSchedulerReady(true)
	var jobs []poolScrubSchedulerJob
	s.scrubJobEnqueue = func(_ context.Context, job poolScrubSchedulerJob) error { jobs = append(jobs, job); return nil }
	createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	if err := s.enqueuePoolScrubTick(t.Context(), b.now); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueuePoolScrubTick(t.Context(), b.now); err != nil || len(jobs) != 1 {
		t.Fatalf("duplicate dispatch: %v %v", jobs, err)
	}
	if err := s.handlePoolScrubSchedulerJob(t.Context(), jobs[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.runPoolScrubTick(t.Context(), b.now); err != nil || len(b.starts) != 1 {
		t.Fatalf("duplicate tick start: %v", err)
	}
	token, err := db.AcquirePoolScrubLease(t.Context(), s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquirePoolScrubLease(t.Context(), s.DB); !errors.Is(err, db.ErrPoolScrubBusy) {
		t.Fatalf("second service acquired ownership: %v", err)
	}
	s.DB.Model(&zfsModels.PoolScrubSchedulerLease{}).Where("id = 1").Update("expires_at", b.now.Add(-time.Minute))
	newToken, err := db.AcquirePoolScrubLease(t.Context(), s.DB)
	if err != nil || newToken == token {
		t.Fatalf("stale lease recovery: %v", err)
	}
	db.ReleasePoolScrubLease(t.Context(), s.DB, token)
	var lease zfsModels.PoolScrubSchedulerLease
	s.DB.First(&lease, 1)
	if lease.Token != newToken {
		t.Fatal("stale owner released new ownership")
	}
}

type scrubTargetEmitter struct{ calls map[string]int }

func (e *scrubTargetEmitter) Emit(context.Context, notifier.EventInput) (notifier.EmitResult, error) {
	return notifier.EmitResult{}, nil
}
func (e *scrubTargetEmitter) DeliveryTargets(context.Context, notifier.EventInput) ([]string, error) {
	return []string{"ui", "email:1"}, nil
}
func (e *scrubTargetEmitter) EmitTarget(_ context.Context, _ notifier.EventInput, target string) (notifier.EmitResult, error) {
	e.calls[target]++
	if target == "email:1" && e.calls[target] == 1 {
		return notifier.EmitResult{}, errors.New("temporary transport failure")
	}
	return notifier.EmitResult{}, nil
}

func TestPoolScrubNotificationRetriesAndStartedEventDedup(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	emitter := &scrubTargetEmitter{calls: map[string]int{}}
	notifier.SetEmitter(emitter)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	var event zfsModels.PoolScrubEvent
	if err := s.DB.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token := fmt.Sprint(attempt)
		s.DB.Model(&event).Updates(map[string]any{"claim_token": token, "claimed_at": b.now})
		s.DB.First(&event, event.ID)
		err := s.deliverPoolScrubEvent(t.Context(), &event, token, b.now.Add(time.Duration(attempt)*time.Minute))
		if attempt == 0 && err == nil || attempt == 1 && err != nil {
			t.Fatalf("delivery attempt %d: %v", attempt, err)
		}
	}
	if emitter.calls["ui"] != 1 || emitter.calls["email:1"] != 2 {
		t.Fatalf("retried successful target: %v", emitter.calls)
	}
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.DB.Model(&zfsModels.PoolScrubEvent{}).Count(&count)
	if count != 0 {
		t.Fatalf("recreated delivered started notification: %d", count)
	}
}

func TestPoolScrubHistoryRetentionAndCounterPrecision(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if value := scrubCounter("18446744073709551615"); value == nil || *value != "18446744073709551615" {
		t.Fatal("lost unsigned counter precision")
	}
	for i := 0; i < 70; i++ {
		run := &zfsModels.PoolScrubRun{RunKey: fmt.Sprint(i), PoolGUID: "101", PoolName: "tank", Source: "manual", RequestedAt: b.now.Add(time.Duration(i) * time.Minute), Status: "passed"}
		if err := s.savePoolScrubRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	active := zfsModels.PoolScrubRun{RunKey: "active", PoolGUID: "101", PoolName: "tank", Source: "manual", RequestedAt: b.now.Add(-time.Hour), Status: "paused"}
	s.DB.Create(&active)
	var count int64
	s.DB.Model(&zfsModels.PoolScrubRun{}).Count(&count)
	if count != 65 {
		t.Fatalf("history retention: %d", count)
	}
}

func TestPoolScrubCronUsesNodeTimezoneAcrossDST(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = previous })
	for _, test := range []struct {
		expr       string
		from, want time.Time
	}{
		{"30 2 * * *", time.Date(2026, 3, 8, 1, 55, 0, 0, zone), time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)},
		{"30 1 * * *", time.Date(2026, 11, 1, 0, 55, 0, 0, zone), time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)},
		{"30 1 * * *", time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)},
	} {
		parsed, err := parsePoolScrubSchedule(test.expr)
		if err != nil {
			t.Fatal(err)
		}
		if next := nextPoolScrubRun(parsed, test.from); !next.Equal(test.want) || next.Location() != time.UTC {
			t.Fatalf("%s after %s: %s, want %s", test.expr, test.from, next, test.want)
		}
	}
}

func TestPoolScrubScopeChangeUsesGUIDAfterRename(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	id := createDuePoolScrub(t, s, "101", b.now.Add(time.Hour))
	b.pools["101"].Name = "renamed"
	if err := s.WithUsablePoolsChange(t.Context(), []string{"renamed", "vault"}, func(*gorm.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var schedule zfsModels.PoolScrubSchedule
	if err := s.DB.First(&schedule, id).Error; err != nil {
		t.Fatal(err)
	}
	if !schedule.Enabled {
		t.Fatal("rename disabled the same GUID's schedule")
	}
	b.pools["101"].Name = "tank"
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	views, err := s.ListPoolScrubSchedules(t.Context())
	if err != nil || len(views) != 1 || !views[0].Active || views[0].LastStatus != "idle" {
		t.Fatalf("manual run must lock schedules without overwriting scheduled history: %+v %v", views, err)
	}
}

func TestPoolScrubConcurrentStartsAndMutationsShareOwnership(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	defer close(unblock)
	b.onStart = func() { close(entered); <-unblock }
	finished := make(chan error, 1)
	go func() { finished <- s.ScrubPool(t.Context(), "101") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("start never reached command")
	}
	secondService := &Service{DB: s.DB, scrubDriver: b, listHostPools: s.listHostPools}
	for _, attempt := range []func() error{
		func() error { return secondService.ScrubPool(t.Context(), "101") },
		func() error { return secondService.DeletePool(t.Context(), "101") },
		func() error { return secondService.ReplaceDevice(t.Context(), "101", "old", "new") },
		func() error {
			return secondService.WithUsablePoolsChange(t.Context(), nil, func(*gorm.DB) error { return nil })
		},
	} {
		if err := attempt(); !errors.Is(err, db.ErrPoolScrubBusy) {
			t.Fatalf("concurrent mutation bypassed start ownership: %v", err)
		}
	}
	unblock <- struct{}{}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 {
		t.Fatalf("concurrent starts: %v", b.starts)
	}
}

func TestPoolScrubRecoverIntentBeforeCommandAndImmediateCompletion(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprint(immediate), func(t *testing.T) {
			s, b := newPoolScrubTestService(t)
			if immediate {
				b.onStart = func() {
					b.pools["101"].ScanStats.State = "FINISHED"
					b.pools["101"].ScanStats.EndTime = fmt.Sprint(b.now.Unix())
				}
				if err := s.ScrubPool(t.Context(), "101"); err != nil {
					t.Fatal(err)
				}
			} else {
				run := &zfsModels.PoolScrubRun{RunKey: "crash-before-command", PoolGUID: "101", PoolName: "tank", Source: "manual", RequestedAt: b.now, Status: "starting", ProgressPct: -1}
				if err := s.savePoolScrubRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
				if err := s.runPoolScrubTick(t.Context(), b.now.Add(6*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := s.ListPoolScrubRuns(t.Context(), "101")
			want := "unknown"
			if immediate {
				want = "passed"
			}
			if err != nil || len(runs) != 1 || runs[0].Status != want {
				t.Fatalf("recovery: %+v %v, want %s", runs, err, want)
			}
			if !immediate && len(b.starts) != 0 {
				t.Fatal("recovery blindly retried an unconfirmed command")
			}
		})
	}
}

func TestPoolScrubUnavailableObservationIsBoundedWithoutAborting(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	b.readErr = errors.New("temporary status failure")
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.ListPoolScrubRuns(t.Context(), "101")
	if runs[0].Status != "running" || runs[0].Error == "" {
		t.Fatalf("lost active tracking on transient failure: %+v", runs)
	}
	b.readErr = nil
	b.pools["101"].ScanStats.State = "UNRECOGNIZED"
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	runs, _ = s.ListPoolScrubRuns(t.Context(), "101")
	if runs[0].Status != "unknown" || len(b.starts) != 1 {
		t.Fatalf("unreadable scan never recovered or was restarted: %+v", runs)
	}
	if err := s.ScrubPool(t.Context(), "101"); !errors.Is(err, ErrScrubStatusUnavailable) || len(b.starts) != 1 {
		t.Fatalf("unknown result bypassed fresh scan guard: %v", err)
	}
}

func TestPoolScrubDispatchReadinessFailedEnqueueAndStaleJobs(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	createDuePoolScrub(t, s, "101", b.now.Add(-time.Minute))
	calls := 0
	s.scrubJobEnqueue = func(context.Context, poolScrubSchedulerJob) error { calls++; return errors.New("queue unavailable") }
	if err := s.enqueuePoolScrubTick(t.Context(), b.now); err != nil || calls != 0 {
		t.Fatalf("bootstrap enqueued work: %v", err)
	}
	s.SetScrubSchedulerReady(true)
	if err := s.enqueuePoolScrubTick(t.Context(), b.now); err == nil || calls != 1 {
		t.Fatal("expected enqueue failure")
	}
	var lease zfsModels.PoolScrubSchedulerLease
	if err := s.DB.First(&lease, 1).Error; err != nil {
		t.Fatal(err)
	}
	if lease.DispatchToken != "" || lease.DispatchedAt != nil {
		t.Fatal("failed enqueue kept dispatch reserved")
	}
	var job poolScrubSchedulerJob
	s.scrubJobEnqueue = func(_ context.Context, payload poolScrubSchedulerJob) error { job = payload; return nil }
	if err := s.enqueuePoolScrubTick(t.Context(), b.now.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.handlePoolScrubSchedulerJob(t.Context(), job); err != nil || len(b.starts) != 0 {
		t.Fatalf("stale queued job started scrub: %v", err)
	}
}

func TestPoolScrubNotificationOrderingAndPermanentFailure(t *testing.T) {
	s, b := newPoolScrubTestService(t)
	if err := s.ScrubPool(t.Context(), "101"); err != nil {
		t.Fatal(err)
	}
	b.pools["101"].ScanStats.State, b.pools["101"].ScanStats.EndTime = "FINISHED", fmt.Sprint(b.now.Unix())
	if err := s.runPoolScrubTick(t.Context(), b.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var events []zfsModels.PoolScrubEvent
	if err := s.DB.Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("missing started/completion events: %+v", events)
	}
	emitter := &scrubTargetEmitter{calls: map[string]int{}}
	notifier.SetEmitter(emitter)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	claim := func(event *zfsModels.PoolScrubEvent) {
		if err := s.DB.Model(event).Updates(map[string]any{"claim_token": "test", "claimed_at": b.now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	claim(&events[1])
	if err := s.deliverPoolScrubEvent(t.Context(), &events[1], "test", b.now); err != nil {
		t.Fatal(err)
	}
	if len(emitter.calls) != 0 {
		t.Fatal("completion overtook started event")
	}
	claim(&events[0])
	events[0].DeliveryPlan = `["ui"]`
	events[0].FailedTargets = `["ui"]`
	if err := s.DB.Model(&events[0]).Updates(map[string]any{"delivery_plan": events[0].DeliveryPlan, "failed_targets": events[0].FailedTargets}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.deliverPoolScrubEvent(t.Context(), &events[0], "test", b.now); err != nil {
		t.Fatal(err)
	}
	var failed zfsModels.PoolScrubEvent
	if err := s.DB.First(&failed, events[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.DeadLetteredAt == nil {
		t.Fatal("permanently failed event did not leave ordering queue")
	}
	claim(&events[1])
	events[1].DeliveryPlan = `["ui"]`
	if err := s.DB.Model(&events[1]).Update("delivery_plan", events[1].DeliveryPlan).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.deliverPoolScrubEvent(t.Context(), &events[1], "test", b.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if emitter.calls["ui"] != 1 {
		t.Fatalf("completion was blocked by dead letter: %v", emitter.calls)
	}
}
