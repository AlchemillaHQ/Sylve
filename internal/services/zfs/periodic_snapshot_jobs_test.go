// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"testing"
	"time"

	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func periodicSnapshotJobs(t *testing.T) (*Service, zfsModels.PeriodicSnapshot, zfsModels.PeriodicSnapshot) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &zfsModels.PeriodicSnapshot{})
	first := zfsModels.PeriodicSnapshot{GUID: "dataset-guid", Interval: 60, Prefix: "minute"}
	second := zfsModels.PeriodicSnapshot{GUID: "dataset-guid", Interval: 3600, Prefix: "hour"}
	if err := database.Create(&first).Error; err != nil {
		t.Fatalf("create first periodic snapshot job: %v", err)
	}
	if err := database.Create(&second).Error; err != nil {
		t.Fatalf("create second periodic snapshot job: %v", err)
	}
	return &Service{DB: database}, first, second
}

func TestDeletePeriodicSnapshotTargetsJobID(t *testing.T) {
	service, first, second := periodicSnapshotJobs(t)
	ctx := context.Background()

	if err := service.DeletePeriodicSnapshot(ctx, second.ID); err != nil {
		t.Fatalf("delete periodic snapshot job: %v", err)
	}

	var remaining zfsModels.PeriodicSnapshot
	if err := service.DB.First(&remaining, first.ID).Error; err != nil {
		t.Fatalf("the other job for the same dataset was deleted: %v", err)
	}
	if err := service.DB.First(&zfsModels.PeriodicSnapshot{}, second.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted job lookup error = %v, want record not found", err)
	}
}

func TestModifyPeriodicSnapshotTargetsJobID(t *testing.T) {
	service, first, second := periodicSnapshotJobs(t)
	keepLast := 7
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{KeepLast: &keepLast}

	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), second.ID, request); err != nil {
		t.Fatalf("modify periodic snapshot retention: %v", err)
	}

	var jobs []zfsModels.PeriodicSnapshot
	if err := service.DB.Order("id").Find(&jobs).Error; err != nil {
		t.Fatalf("load periodic snapshot jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != first.ID || jobs[0].KeepLast != 0 || jobs[1].ID != second.ID || jobs[1].KeepLast != keepLast {
		t.Fatalf("unexpected jobs after update: %#v", jobs)
	}
}

func TestValidateRetentionAllowsKeepLastWithGFS(t *testing.T) {
	mode := string(retentionGFS)
	keepLast := 20
	keepHourly := 24
	request := zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest{
		RetentionType: &mode,
		KeepLast:      &keepLast,
		KeepHourly:    &keepHourly,
	}

	rtype, values, err := validateAndNormalizeRetention(request, "create")
	if err != nil {
		t.Fatalf("validate GFS retention: %v", err)
	}
	if rtype != retentionGFS || values.KeepLast != keepLast || values.KeepHourly != keepHourly {
		t.Fatalf("retention = %q %#v, want GFS keepLast %d keepHourly %d", rtype, values, keepLast, keepHourly)
	}
}

func TestValidateRetentionRejectsEmptyExplicitPolicy(t *testing.T) {
	for _, mode := range []retentionType{retentionSimple, retentionGFS} {
		t.Run(string(mode), func(t *testing.T) {
			value := string(mode)
			request := zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest{RetentionType: &value}

			_, _, err := validateAndNormalizeRetention(request, "create")
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validation error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestModifyPeriodicSnapshotClearsRetention(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	if err := service.DB.Model(&job).Updates(map[string]interface{}{
		"KeepLast":   20,
		"KeepHourly": 24,
		"KeepDaily":  7,
	}).Error; err != nil {
		t.Fatalf("seed retention: %v", err)
	}

	mode := string(retentionNone)
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{RetentionType: &mode}
	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), job.ID, request); err != nil {
		t.Fatalf("clear retention: %v", err)
	}

	var updated zfsModels.PeriodicSnapshot
	if err := service.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatalf("load periodic snapshot job: %v", err)
	}
	if updated.KeepLast != 0 || updated.MaxAgeDays != 0 || updated.KeepHourly != 0 ||
		updated.KeepDaily != 0 || updated.KeepWeekly != 0 || updated.KeepMonthly != 0 || updated.KeepYearly != 0 {
		t.Fatalf("retention was not cleared: %#v", updated)
	}
}

func TestModifyPeriodicSnapshotStoresGFSKeepLast(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	if err := service.DB.Model(&job).Updates(map[string]interface{}{
		"KeepLast":   10,
		"MaxAgeDays": 30,
	}).Error; err != nil {
		t.Fatalf("seed simple retention: %v", err)
	}

	mode := string(retentionGFS)
	keepLast := 20
	keepHourly := 24
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{
		RetentionType: &mode,
		KeepLast:      &keepLast,
		KeepHourly:    &keepHourly,
	}
	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), job.ID, request); err != nil {
		t.Fatalf("store GFS retention: %v", err)
	}

	var updated zfsModels.PeriodicSnapshot
	if err := service.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatalf("load periodic snapshot job: %v", err)
	}
	if updated.KeepLast != keepLast || updated.KeepHourly != keepHourly || updated.MaxAgeDays != 0 {
		t.Fatalf("unexpected GFS retention: %#v", updated)
	}
}

func TestPeriodicSnapshotScheduleAtReturnsNextIntervalBoundary(t *testing.T) {
	lastRunAt := time.Date(2026, time.August, 26, 12, 3, 0, 0, time.Local)
	job := zfsModels.PeriodicSnapshot{Interval: 180, LastRunAt: lastRunAt.UTC()}

	schedule, err := periodicSnapshotScheduleAt(job, lastRunAt.Add(20*time.Second))
	if err != nil {
		t.Fatalf("calculate interval schedule: %v", err)
	}
	if schedule.shouldRun {
		t.Fatal("interval job should not be due")
	}
	wantNext := lastRunAt.Add(3 * time.Minute)
	if !schedule.nextAtLocal.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", schedule.nextAtLocal, wantNext)
	}
}

func TestPeriodicSnapshotScheduleAtReturnsNextCronBoundary(t *testing.T) {
	lastRunAt := time.Date(2026, time.August, 26, 12, 3, 0, 0, time.Local)
	job := zfsModels.PeriodicSnapshot{CronExpr: "*/3 * * * *", LastRunAt: lastRunAt.UTC()}

	schedule, err := periodicSnapshotScheduleAt(job, lastRunAt.Add(20*time.Second))
	if err != nil {
		t.Fatalf("calculate cron schedule: %v", err)
	}
	if schedule.shouldRun {
		t.Fatal("cron job should not be due")
	}
	wantNext := lastRunAt.Add(3 * time.Minute)
	if !schedule.nextAtLocal.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", schedule.nextAtLocal, wantNext)
	}
}

func TestPeriodicSnapshotScheduleAtSkipsMissedCronBoundaries(t *testing.T) {
	lastRunAt := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.Local)
	job := zfsModels.PeriodicSnapshot{CronExpr: "*/3 * * * *", LastRunAt: lastRunAt.UTC()}

	schedule, err := periodicSnapshotScheduleAt(job, lastRunAt.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("calculate overdue cron schedule: %v", err)
	}
	if !schedule.shouldRun || !schedule.runAtLocal.Equal(lastRunAt.Add(6*time.Minute)) {
		t.Fatalf("due run = %v (due %t), want %v", schedule.runAtLocal, schedule.shouldRun, lastRunAt.Add(6*time.Minute))
	}
	wantNext := lastRunAt.Add(9 * time.Minute)
	if !schedule.nextAtLocal.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", schedule.nextAtLocal, wantNext)
	}
}

func TestPeriodicSnapshotScheduleAtSkipsMissedIntervalBoundaries(t *testing.T) {
	lastRunAt := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.Local)
	job := zfsModels.PeriodicSnapshot{Interval: 180, LastRunAt: lastRunAt.UTC()}

	schedule, err := periodicSnapshotScheduleAt(job, lastRunAt.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("calculate overdue interval schedule: %v", err)
	}
	if !schedule.shouldRun || !schedule.runAtLocal.Equal(lastRunAt.Add(6*time.Minute)) {
		t.Fatalf("due run = %v (due %t), want %v", schedule.runAtLocal, schedule.shouldRun, lastRunAt.Add(6*time.Minute))
	}
	wantNext := lastRunAt.Add(9 * time.Minute)
	if !schedule.nextAtLocal.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", schedule.nextAtLocal, wantNext)
	}
}

func TestGetPeriodicSnapshotsIncludesNextRunAt(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	lastRunAt := time.Now().UTC()
	if err := service.DB.Model(&job).Update("LastRunAt", lastRunAt).Error; err != nil {
		t.Fatalf("set last run: %v", err)
	}

	jobs, err := service.GetPeriodicSnapshots()
	if err != nil {
		t.Fatalf("get periodic snapshots: %v", err)
	}

	for _, current := range jobs {
		if current.ID != job.ID {
			continue
		}
		if current.NextRunAt == nil {
			t.Fatal("next run was not included")
		}
		wantNext := current.LastRunAt.Add(time.Duration(current.Interval) * time.Second)
		if !current.NextRunAt.Equal(wantNext) {
			t.Fatalf("next run = %v, want %v", current.NextRunAt, wantNext)
		}
		return
	}

	t.Fatalf("periodic snapshot job %d was not returned", job.ID)
}

func TestGetPeriodicSnapshotsReportsOverdueOccurrenceAsNext(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	lastRunAt := time.Now().UTC().Add(-2*time.Hour - 10*time.Minute)
	if err := service.DB.Model(&job).Update("LastRunAt", lastRunAt).Error; err != nil {
		t.Fatalf("set overdue last run: %v", err)
	}

	jobs, err := service.GetPeriodicSnapshots()
	if err != nil {
		t.Fatalf("get periodic snapshots: %v", err)
	}

	for _, current := range jobs {
		if current.ID != job.ID {
			continue
		}
		if current.NextRunAt == nil {
			t.Fatal("overdue next run was not included")
		}
		wantNext := current.LastRunAt.Add(2 * time.Hour)
		if !current.NextRunAt.Equal(wantNext) {
			t.Fatalf("next overdue run = %v, want %v", current.NextRunAt, wantNext)
		}
		return
	}

	t.Fatalf("periodic snapshot job %d was not returned", job.ID)
}

func TestModifyPeriodicSnapshotScheduleTargetsJobID(t *testing.T) {
	service, first, second := periodicSnapshotJobs(t)
	interval := 60
	cronExpr := ""
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{
		Interval: &interval,
		CronExpr: &cronExpr,
	}

	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), second.ID, request); err != nil {
		t.Fatalf("modify periodic snapshot schedule: %v", err)
	}

	var jobs []zfsModels.PeriodicSnapshot
	if err := service.DB.Order("id").Find(&jobs).Error; err != nil {
		t.Fatalf("load periodic snapshot jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != first.ID || jobs[0].Interval != first.Interval ||
		jobs[1].ID != second.ID || jobs[1].Interval != interval || jobs[1].CronExpr != "" {
		t.Fatalf("unexpected jobs after schedule update: %#v", jobs)
	}
}

func TestModifyPeriodicSnapshotScheduleSwitchesToCron(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	interval := 0
	cronExpr := "*/5 * * * *"
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{
		Interval: &interval,
		CronExpr: &cronExpr,
	}

	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), job.ID, request); err != nil {
		t.Fatalf("modify periodic snapshot cron schedule: %v", err)
	}

	var updated zfsModels.PeriodicSnapshot
	if err := service.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatalf("load periodic snapshot job: %v", err)
	}
	if updated.Interval != 0 || updated.CronExpr != cronExpr {
		t.Fatalf("schedule = interval %d cron %q, want interval 0 cron %q", updated.Interval, updated.CronExpr, cronExpr)
	}
}

func TestModifyPeriodicSnapshotRejectsInvalidSchedule(t *testing.T) {
	service, _, job := periodicSnapshotJobs(t)
	interval := 60
	cronExpr := "0 * * * *"
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{
		Interval: &interval,
		CronExpr: &cronExpr,
	}

	err := service.ModifyPeriodicSnapshotRetention(context.Background(), job.ID, request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("modify error = %v, want ErrInvalidRequest", err)
	}

	var unchanged zfsModels.PeriodicSnapshot
	if dbErr := service.DB.First(&unchanged, job.ID).Error; dbErr != nil {
		t.Fatalf("load periodic snapshot job: %v", dbErr)
	}
	if unchanged.Interval != job.Interval || unchanged.CronExpr != job.CronExpr {
		t.Fatalf("invalid schedule changed job: %#v", unchanged)
	}
}

func TestPeriodicSnapshotMissingJobErrors(t *testing.T) {
	service, _, _ := periodicSnapshotJobs(t)
	ctx := context.Background()
	keepLast := 1
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{KeepLast: &keepLast}

	if err := service.DeletePeriodicSnapshot(ctx, 9999); !errors.Is(err, ErrSnapshotJobNotFound) {
		t.Fatalf("delete error = %v, want ErrSnapshotJobNotFound", err)
	}
	if err := service.ModifyPeriodicSnapshotRetention(ctx, 9999, request); !errors.Is(err, ErrSnapshotJobNotFound) {
		t.Fatalf("modify error = %v, want ErrSnapshotJobNotFound", err)
	}
}

func TestClassifiedErrorPreservesDetailAndCategory(t *testing.T) {
	detail := errors.New("dataset_in_use_by_vm")
	err := classifyError(ErrConflict, "%w", detail)
	if err.Error() != detail.Error() {
		t.Fatalf("error detail = %q, want %q", err.Error(), detail.Error())
	}
	if !errors.Is(err, ErrConflict) || !errors.Is(err, detail) {
		t.Fatalf("classified error did not unwrap category and detail: %v", err)
	}
}
