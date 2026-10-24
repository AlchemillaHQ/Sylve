// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestMonitoringCronPersistsAndPreservesLegacyPresets(t *testing.T) {
	s := testService(t)
	if _, err := s.SetMonitoring(t.Context(), Monitoring{Enabled: true, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	state, err := s.SetMonitoring(t.Context(), Monitoring{Enabled: true, CronExpr: " 15  3 * * MON-FRI "})
	if err != nil || state.CronExpr != "15 3 * * MON-FRI" || state.IntervalHours != 6 || state.NextCheckAt == nil || !state.NextCheckAt.After(before) {
		t.Fatalf("custom schedule was not persisted: %+v %v", state, err)
	}
	localNext := state.NextCheckAt.In(time.Local)
	if localNext.Hour() != 3 || localNext.Minute() != 15 || localNext.Weekday() == time.Saturday || localNext.Weekday() == time.Sunday {
		t.Fatalf("custom schedule was not evaluated in node-local time: %v", localNext)
	}
	restarted := &Service{DB: s.DB}
	stored, err := restarted.state()
	if err != nil || stored.CronExpr != state.CronExpr || !stored.NextCheckAt.Equal(*state.NextCheckAt) {
		t.Fatalf("custom schedule did not survive a new service instance: %+v %v", stored, err)
	}
	state, err = s.SetMonitoring(t.Context(), Monitoring{Enabled: false, CronExpr: state.CronExpr})
	if err != nil || state.Monitoring || state.NextCheckAt != nil || state.CronExpr != "15 3 * * MON-FRI" {
		t.Fatalf("Never did not pause the custom schedule: %+v %v", state, err)
	}
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.DB.Model(&models.HostUpdateOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("paused schedule started work: %d %v", count, err)
	}
	before = time.Now().UTC()
	state, err = s.SetMonitoring(t.Context(), Monitoring{Enabled: true, IntervalHours: 12})
	if err != nil || state.CronExpr != "" || state.IntervalHours != 12 || state.NextCheckAt == nil || state.NextCheckAt.Before(before) || state.NextCheckAt.After(time.Now().UTC()) {
		t.Fatalf("legacy preset behavior changed: %+v %v", state, err)
	}
	for _, expression := range []string{"* * * * 8", "0 3 31 2 *"} {
		if _, err := s.SetMonitoring(t.Context(), Monitoring{Enabled: true, CronExpr: expression}); err == nil || code(err) != "invalid_update_cron" {
			t.Fatalf("invalid expression changed settings: %v", err)
		}
	}
	stored, err = s.state()
	if err != nil || stored.CronExpr != "" || stored.IntervalHours != 12 {
		t.Fatalf("invalid expression overwrote settings: %+v %v", stored, err)
	}
}

func TestMonitoringCronSkipsOverlapsWithoutPostponingItsNextOccurrence(t *testing.T) {
	s := testService(t)
	past := time.Now().UTC().Add(-time.Minute)
	if err := s.DB.Model(&models.HostUpdateState{ID: 1}).Updates(map[string]any{"cron_expr": "* * * * *", "next_check_at": past}).Error; err != nil {
		t.Fatal(err)
	}
	one := uint(1)
	busy := models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "audit"}
	if err := s.DB.Create(&busy).Error; err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	state, err := s.state()
	if err != nil || state.NextCheckAt == nil || !state.NextCheckAt.After(before) || state.NextCheckAt.After(time.Now().Add(time.Minute)) || state.NextCheckAt.Second() != 0 || state.FailureCount != 0 {
		t.Fatalf("busy cron occurrence was postponed or treated as failure: %+v %v", state, err)
	}
	var count int64
	if err := s.DB.Model(&models.HostUpdateOperation{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("overlapping monitoring operation started: %d %v", count, err)
	}
	if err := s.DB.Model(&busy).Update("active", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&state).Update("next_check_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	var scheduled models.HostUpdateOperation
	if err := s.DB.Where("action = ?", "check").First(&scheduled).Error; err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, scheduled.ID)
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&models.HostUpdateOperation{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("missed cron occurrences were replayed: %d %v", count, err)
	}
}

func TestMonitoringRetryPreservesSettingsChangedAfterTheScheduleClaim(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
	}{
		{"changed to cron", true},
		{"disabled", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testService(t)
			one := uint(1)
			if err := s.DB.Create(&models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "audit"}).Error; err != nil {
				t.Fatal(err)
			}
			var savedNext *time.Time
			if test.enabled {
				next := time.Now().UTC().Add(48 * time.Hour)
				savedNext = &next
			}
			changed := false
			const callback = "test:change_monitoring_after_claim"
			err := s.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
				if changed || tx.Statement.Table != "host_update_states" {
					return
				}
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || values["last_attempt_at"] == nil {
					return
				}
				changed = true
				tx.AddError(s.DB.Model(&models.HostUpdateState{ID: 1}).Updates(map[string]any{
					"monitoring": test.enabled, "cron_expr": "0 3 * * 0", "next_check_at": savedNext,
				}).Error)
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.DB.Callback().Update().Remove(callback) })
			if err := s.schedule(); err != nil {
				t.Fatal(err)
			}
			state, err := s.state()
			if err != nil || !changed || state.Monitoring != test.enabled || state.CronExpr != "0 3 * * 0" || (state.NextCheckAt == nil) != (savedNext == nil) {
				t.Fatalf("retry overwrote new monitoring settings: %+v %v", state, err)
			}
			if savedNext != nil && !state.NextCheckAt.Equal(*savedNext) {
				t.Fatalf("retry moved the new schedule: got %v, want %v", state.NextCheckAt, savedNext)
			}
		})
	}
}

func TestMonitoringFreshnessFollowsCronInsteadOfLegacyInterval(t *testing.T) {
	checked := time.Date(2026, time.October, 18, 3, 15, 0, 0, time.Local)
	state := models.HostUpdateState{IntervalHours: 24, CronExpr: "0 3 * * 0"}
	deadline := time.Date(2026, time.October, 25, 4, 0, 0, 0, time.Local)
	if !monitoringResultFresh(state, &checked, checked.Add(48*time.Hour)) || monitoringResultFresh(state, &checked, deadline) || monitoringResultFresh(state, nil, checked) || monitoringResultFresh(state, &checked, checked.Add(-time.Minute)) {
		t.Fatal("cron freshness did not follow the next occurrence plus the audit grace period")
	}
	state.CronExpr = "* * * * 8"
	if monitoringResultFresh(state, &checked, checked.Add(time.Minute)) {
		t.Fatal("invalid stored cron made results look current")
	}
	state.CronExpr = ""
	if !monitoringResultFresh(state, &checked, checked.Add(24*time.Hour)) || monitoringResultFresh(state, &checked, checked.Add(25*time.Hour)) {
		t.Fatal("legacy freshness window changed")
	}
}
