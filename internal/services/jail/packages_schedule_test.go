// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
)

func TestPackageMonitoringCronPersistsAndPreservesPresets(t *testing.T) {
	s, jail := newPackageTestService(t)
	if _, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	monitor, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, CronExpr: " 15  3 * * MON-FRI "})
	if err != nil || !monitor.Enabled || monitor.CronExpr != "15 3 * * MON-FRI" || monitor.IntervalHours != 6 || monitor.NextAuditAt == nil || !monitor.NextAuditAt.After(before) {
		t.Fatalf("custom schedule was not persisted: %+v %v", monitor, err)
	}
	localNext := monitor.NextAuditAt.In(time.Local)
	if localNext.Hour() != 3 || localNext.Minute() != 15 || localNext.Weekday() == time.Saturday || localNext.Weekday() == time.Sunday {
		t.Fatalf("custom schedule was not evaluated in node-local time: %v", localNext)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("custom schedule ran before its next occurrence: %d %v", count, err)
	}
	monitor, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: false, CronExpr: monitor.CronExpr})
	if err != nil || monitor.Enabled || monitor.NextAuditAt != nil || monitor.CronExpr != "15 3 * * MON-FRI" {
		t.Fatalf("Never did not pause the custom schedule: %+v %v", monitor, err)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("disabled monitoring started an audit: %d %v", count, err)
	}
	before = time.Now().UTC()
	monitor, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 12})
	if err != nil || monitor.CronExpr != "" || monitor.IntervalHours != 12 || monitor.NextAuditAt == nil || monitor.NextAuditAt.Before(before) || monitor.NextAuditAt.After(time.Now().UTC()) {
		t.Fatalf("preset did not clear cron or retain its immediate first audit: %+v %v", monitor, err)
	}
	for _, invalid := range []jailTypes.PackageMonitoring{{Enabled: true, CronExpr: "* * * * 8"}, {Enabled: true, CronExpr: "0 3 31 2 *"}, {Enabled: true, IntervalHours: 5}} {
		if _, err := s.SetPackageMonitoring(t.Context(), jail.CTID, invalid); err == nil {
			t.Fatalf("invalid schedule was accepted: %+v", invalid)
		}
	}
	stored, err := s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || stored.CronExpr != "" || stored.IntervalHours != 12 || !stored.NextAuditAt.Equal(*monitor.NextAuditAt) {
		t.Fatalf("invalid schedule overwrote settings: %+v %v", stored, err)
	}
}

func TestPackageMonitoringCronSkipsOverlapsWithoutPostponing(t *testing.T) {
	s, jail := newPackageTestService(t)
	if _, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, CronExpr: "* * * * *"}); err != nil {
		t.Fatal(err)
	}
	state, err := s.packageState(&jail, jail.Storages[0].GUID)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-5 * time.Minute)
	if err = s.DB.Model(&state).Update("next_audit_at", past).Error; err != nil {
		t.Fatal(err)
	}
	release, err := s.ReserveSystemOperation(t.Context(), jail.CTID)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err = s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	monitor, err := s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || monitor.NextAuditAt == nil || !monitor.NextAuditAt.After(before) || monitor.NextAuditAt.After(time.Now().UTC().Add(time.Minute)) || monitor.NextAuditAt.Second() != 0 || monitor.FailureCount != 0 || monitor.SkipReason != "jail_operation_in_progress" {
		t.Fatalf("overlap postponed cron or counted as a failure: %+v %v", monitor, err)
	}
	release()
	if err = s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("skipped occurrence was queued after the operation completed: %d %v", count, err)
	}
	if err = s.DB.Model(&state).Update("next_audit_at", past).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.runPackageSchedules(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var tasks []taskModels.GuestLifecycleTask
	if err = s.DB.Find(&tasks).Error; err != nil || len(tasks) != 1 || tasks[0].Action != "pkg-audit" || tasks[0].Source != "scheduled" || tasks[0].RequestedBy != "package-monitor" {
		t.Fatalf("cron replayed missed occurrences or scheduled something other than an audit: %+v %v", tasks, err)
	}
}

func TestPackageAuditFreshnessFollowsMonitoringCadence(t *testing.T) {
	audited := time.Date(2026, time.October, 18, 3, 15, 0, 0, time.Local)
	weekly := jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6, CronExpr: "0 3 * * 0"}
	disabled := jailTypes.PackageMonitoring{IntervalHours: 6, CronExpr: "* * * * 8"}
	for _, test := range []struct {
		name    string
		monitor jailTypes.PackageMonitoring
		audited *time.Time
		now     time.Time
		fresh   bool
	}{
		{"before next cron run", weekly, &audited, audited.Add(48 * time.Hour), true},
		{"next cron run plus grace", weekly, &audited, time.Date(2026, time.October, 25, 4, 0, 0, 0, time.Local), false},
		{"never audited", weekly, nil, audited, false},
		{"audited in the future", weekly, &audited, audited.Add(-time.Minute), false},
		{"invalid cron", jailTypes.PackageMonitoring{Enabled: true, CronExpr: "* * * * 8"}, &audited, audited.Add(time.Minute), false},
		{"disabled within a day", disabled, &audited, audited.Add(24 * time.Hour), true},
		{"disabled after a day", disabled, &audited, audited.Add(25 * time.Hour), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if fresh := packageAuditResultFresh(test.monitor, test.audited, test.now); fresh != test.fresh {
				t.Fatalf("fresh=%v, want %v", fresh, test.fresh)
			}
		})
	}
	s, jail, fixture := newPackageReadTestService(t)
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * time.Hour)
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, AuditAt: &old, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory)}
	if err = s.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	for _, interval := range []int{6, 24} {
		if _, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: interval}); err != nil {
			t.Fatal(err)
		}
		view, err := s.GetPackages(t.Context(), jail.CTID)
		if err != nil || view.AuditStale != (interval == 6) {
			t.Fatalf("%d-hour monitoring: stale=%v: %v", interval, view.AuditStale, err)
		}
	}
}

func TestPackageMonitoringInvalidStoredCronDoesNotBlockOtherJails(t *testing.T) {
	s, jail := newPackageTestService(t)
	if err := s.DB.Model(&jail).Updates(map[string]any{"package_monitoring_enabled": true, "package_audit_cron_expr": "* * * * 8"}).Error; err != nil {
		t.Fatal(err)
	}
	other := jailModels.Jail{CTID: jail.CTID + 1, Name: "valid-monitor", Type: jailModels.JailTypeFreeBSD, PackageMonitoringEnabled: true, PackageAuditCronExpr: "* * * * *", Storages: []jailModels.Storage{{Pool: "tank", GUID: "other-root", IsBase: true}}}
	if err := s.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	monitor, err := s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || monitor.FailureCount != 1 || monitor.SkipReason != "invalid_package_audit_cron" || monitor.NextAuditAt == nil || !monitor.NextAuditAt.After(time.Now().UTC()) {
		t.Fatalf("invalid metadata was not reported and deferred: %+v %v", monitor, err)
	}
	var tasks []taskModels.GuestLifecycleTask
	if err := s.DB.Find(&tasks).Error; err != nil || len(tasks) != 1 || tasks[0].GuestID != other.CTID || tasks[0].Action != "pkg-audit" {
		t.Fatalf("invalid cron blocked another jail or started work: %+v %v", tasks, err)
	}
}
