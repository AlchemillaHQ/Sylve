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
	restarted := &Service{DB: s.DB}
	stored, err := restarted.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || stored.CronExpr != monitor.CronExpr || !stored.NextAuditAt.Equal(*monitor.NextAuditAt) {
		t.Fatalf("custom schedule did not survive a new service instance: %+v %v", stored, err)
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
	stored, err = s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || stored.CronExpr != "" || stored.IntervalHours != 12 || !stored.NextAuditAt.Equal(*monitor.NextAuditAt) {
		t.Fatalf("invalid schedule overwrote settings: %+v %v", stored, err)
	}
}

func TestPackageMonitoringCronSurvivesMetadataRestore(t *testing.T) {
	s, jail := newPackageTestService(t)
	jail.PackageMonitoringEnabled = true
	jail.PackageAuditIntervalHours = 12
	jail.PackageAuditCronExpr = "0 3 * * 0"
	for _, expression := range []string{jail.PackageAuditCronExpr, ""} {
		jail.PackageAuditCronExpr = expression
		if err := s.restoreJailDatabaseFromSnapshot(jail.CTID, jail); err != nil {
			t.Fatal(err)
		}
		monitor, err := s.GetPackageMonitoring(t.Context(), jail.CTID)
		if err != nil || !monitor.Enabled || monitor.IntervalHours != 12 || monitor.CronExpr != expression {
			t.Fatalf("snapshot restore did not restore or clear cron: %+v %v", monitor, err)
		}
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

func TestPackageAuditFreshnessFollowsCron(t *testing.T) {
	audited := time.Date(2026, time.October, 18, 3, 15, 0, 0, time.Local)
	monitor := jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6, CronExpr: "0 3 * * 0"}
	deadline := time.Date(2026, time.October, 25, 4, 0, 0, 0, time.Local)
	if !packageAuditResultFresh(monitor, &audited, audited.Add(48*time.Hour)) || packageAuditResultFresh(monitor, &audited, deadline) || packageAuditResultFresh(monitor, nil, audited) || packageAuditResultFresh(monitor, &audited, audited.Add(-time.Minute)) {
		t.Fatal("cron freshness did not follow the next occurrence plus the grace period")
	}
	monitor.CronExpr = "* * * * 8"
	if packageAuditResultFresh(monitor, &audited, audited.Add(time.Minute)) {
		t.Fatal("invalid stored cron made results look current")
	}
	monitor.Enabled = false
	if !packageAuditResultFresh(monitor, &audited, audited.Add(24*time.Hour)) || packageAuditResultFresh(monitor, &audited, audited.Add(25*time.Hour)) {
		t.Fatal("disabled monitoring changed the default freshness window")
	}
	s, jail, fixture := newPackageReadTestService(t)
	if _, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, CronExpr: "0 0 1 * *"}); err != nil {
		t.Fatal(err)
	}
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).UTC()
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, AuditAt: &monthStart, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory)}
	if err = s.DB.Model(&state).Select("AuditAt", "AuditInventoryHash").Updates(&state).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.GetPackages(t.Context(), jail.CTID)
	if err != nil || view.AuditStale || view.Monitoring.CronExpr != "0 0 1 * *" {
		t.Fatalf("package view ignored the custom monitoring cadence: %+v %v", view, err)
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
