// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"reflect"
	"testing"
	"time"

	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestPoolScrubScheduleIndexMigrationPreservesRecords(t *testing.T) {
	for _, schema := range []string{"legacy", "current"} {
		t.Run(schema, func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(t, &zfsModels.PoolScrubSchedule{}, &zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubEvent{})
			if schema == "legacy" {
				for _, statement := range []string{
					"DROP INDEX idx_pool_scrub_schedules_pool_guid",
					"CREATE UNIQUE INDEX idx_pool_scrub_schedules_pool_guid ON pool_scrub_schedules(pool_guid)",
				} {
					if err := database.Exec(statement).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			now := time.Now().UTC().Truncate(time.Second)
			schedule := zfsModels.PoolScrubSchedule{
				PoolGUID: "101", PoolName: "tank", CronExpr: "0 3 1 * *", Enabled: true,
				LastStatus: "queued", QueuedAt: &now, ScheduledFor: &now, NextRunAt: &now, ProgressPct: -1,
			}
			if err := database.Create(&schedule).Error; err != nil {
				t.Fatal(err)
			}
			run := zfsModels.PoolScrubRun{
				RunKey: "legacy-run", PoolGUID: "101", PoolName: "tank", Source: "scheduled",
				ScheduleID: &schedule.ID, RequestedAt: now, CompletedAt: &now, Status: "passed",
			}
			if err := database.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			event := zfsModels.PoolScrubEvent{
				EventKey: "legacy-run|scrub_passed", RunKey: run.RunKey, PoolGUID: "101", PoolName: "tank",
				ScheduleID: schedule.ID, Source: "scheduled", Condition: "scrub_passed",
			}
			if err := database.Create(&event).Error; err != nil {
				t.Fatal(err)
			}
			var beforeSchedule zfsModels.PoolScrubSchedule
			var beforeRun zfsModels.PoolScrubRun
			var beforeEvent zfsModels.PoolScrubEvent
			for _, record := range []any{&beforeSchedule, &beforeRun, &beforeEvent} {
				if err := database.First(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := database.AutoMigrate(&zfsModels.PoolScrubSchedule{}, &zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubEvent{}); err != nil {
					t.Fatal(err)
				}
				if err := migratePoolScrubScheduleIndexes(database); err != nil {
					t.Fatal(err)
				}
			}
			var afterSchedule zfsModels.PoolScrubSchedule
			var afterRun zfsModels.PoolScrubRun
			var afterEvent zfsModels.PoolScrubEvent
			for _, record := range []any{&afterSchedule, &afterRun, &afterEvent} {
				if err := database.First(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(beforeSchedule, afterSchedule) || !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeEvent, afterEvent) {
				t.Fatal("migration changed the schedule, queued occurrence, history, or pending notification")
			}
			sibling := zfsModels.PoolScrubSchedule{
				PoolGUID: "101", PoolName: "tank", CronExpr: schedule.CronExpr, Enabled: false,
			}
			if err := database.Create(&sibling).Error; err != nil {
				t.Fatalf("migration kept the one-schedule-per-pool constraint: %v", err)
			}
			if sibling.ID == schedule.ID || !database.Migrator().HasIndex(&zfsModels.PoolScrubSchedule{}, "PoolGUID") {
				t.Fatal("migration lost independent schedule IDs or the pool GUID lookup index")
			}
		})
	}
}

func TestPoolScrubCooldownBackfillUsesOnlyActualCompletion(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubCooldown{})
	now := time.Now().UTC().Truncate(time.Second)
	started, older, latest := now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(-time.Hour)
	invalidEnd := started.Add(-time.Hour)
	runs := []zfsModels.PoolScrubRun{
		{RunKey: "older", PoolGUID: "101", StartedAt: &started, CompletedAt: &older, Status: "passed"},
		{RunKey: "latest", PoolGUID: "101", StartedAt: &started, CompletedAt: &latest, Status: "failed"},
		{RunKey: "unknown", PoolGUID: "101", StartedAt: &started, CompletedAt: &now, Status: "unknown"},
		{RunKey: "failed-start", PoolGUID: "101", CompletedAt: &now, Status: "failed"},
		{RunKey: "skipped", PoolGUID: "101", CompletedAt: &now, Status: "skipped"},
		{RunKey: "invalid-end", PoolGUID: "101", StartedAt: &started, CompletedAt: &invalidEnd, Status: "passed"},
	}
	if err := database.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillPoolScrubCooldowns(database); err != nil {
		t.Fatal(err)
	}
	var cooldown zfsModels.PoolScrubCooldown
	if err := database.Where("pool_guid = ?", "101").First(&cooldown).Error; err != nil || !cooldown.LastCompletedAt.Equal(latest) {
		t.Fatalf("backfill inferred completion from bookkeeping: %+v %v", cooldown, err)
	}
	if err := RecordPoolScrubCompletion(database, "101", now); err != nil {
		t.Fatal(err)
	}
	if err := backfillPoolScrubCooldowns(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Where("pool_guid = ?", "101").First(&cooldown).Error; err != nil || !cooldown.LastCompletedAt.Equal(now) {
		t.Fatalf("repeated startup moved the cooldown backwards: %+v %v", cooldown, err)
	}
}
