// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newVMStatsTestService(t *testing.T) (*Service, vmModels.VM) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStats{})
	vm := vmModels.VM{RID: 107, Name: "stats-vm"}
	if err := database.Create(&vm).Error; err != nil {
		t.Fatalf("seed vm: %v", err)
	}

	return &Service{DB: database}, vm
}

func TestGetVMUsageBootstrapReturnsConsistentDailySnapshot(t *testing.T) {
	service, vm := newVMStatsTestService(t)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	stats := []vmModels.VMStats{
		{VMID: vm.ID, CPUUsage: 1, CreatedAt: now.Add(-40 * 24 * time.Hour)},
		{VMID: vm.ID, CPUUsage: 2, CreatedAt: now.Add(-20 * 24 * time.Hour)},
		{VMID: vm.ID, CPUUsage: 3, CreatedAt: now.Add(-3 * 24 * time.Hour)},
	}
	if err := service.DB.Create(&stats).Error; err != nil {
		t.Fatalf("seed vm stats: %v", err)
	}

	got, err := service.getVMUsageBootstrapAt(int(vm.RID), now)
	if err != nil {
		t.Fatalf("get VM usage bootstrap: %v", err)
	}
	if got.ResolvedStep == nil || *got.ResolvedStep != db.GFSStepDaily {
		t.Fatalf("resolved step = %v, want daily", got.ResolvedStep)
	}
	if got.HistoryState != db.StatsHistoryAvailable {
		t.Fatalf("history state = %q, want available", got.HistoryState)
	}
	if len(got.Points) != 2 || got.Points[0].CPUUsage != 2 || got.Points[1].CPUUsage != 3 {
		t.Fatalf("points = %+v, want the two points inside the daily window", got.Points)
	}
	if got.LastSampleAt == nil || !got.LastSampleAt.Equal(stats[2].CreatedAt) {
		t.Fatalf("last sample = %v, want %v", got.LastSampleAt, stats[2].CreatedAt)
	}
}

func TestGetVMUsageBootstrapDistinguishesNoHistoryAndExpiredHistory(t *testing.T) {
	service, vm := newVMStatsTestService(t)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

	empty, err := service.getVMUsageBootstrapAt(int(vm.RID), now)
	if err != nil {
		t.Fatalf("get empty VM usage bootstrap: %v", err)
	}
	if empty.HistoryState != db.StatsHistoryNeverRecorded || empty.Points == nil {
		t.Fatalf("empty bootstrap = %+v, want never-recorded with a non-nil points array", empty)
	}

	oldTimestamp := now.Add(-71 * 24 * time.Hour)
	if err := service.DB.Create(&vmModels.VMStats{
		VMID: vm.ID, CPUUsage: 9, CreatedAt: oldTimestamp,
	}).Error; err != nil {
		t.Fatalf("seed expired VM stat: %v", err)
	}

	expired, err := service.getVMUsageBootstrapAt(int(vm.RID), now)
	if err != nil {
		t.Fatalf("get expired VM usage bootstrap: %v", err)
	}
	if expired.HistoryState != db.StatsHistoryOutsideSupportedRange || expired.ResolvedStep != nil {
		t.Fatalf("expired bootstrap = %+v, want outside-supported-range", expired)
	}
	if expired.LastSampleAt == nil || !expired.LastSampleAt.Equal(oldTimestamp) {
		t.Fatalf("last sample = %v, want %v", expired.LastSampleAt, oldTimestamp)
	}
}

func TestGetVMUsageBootstrapNotFound(t *testing.T) {
	service, _ := newVMStatsTestService(t)

	_, err := service.getVMUsageBootstrapAt(999, time.Now())
	if err == nil || !strings.Contains(err.Error(), "vm_not_found") {
		t.Fatalf("error = %v, want vm_not_found", err)
	}
}

func TestGetVMUsageBootstrapDatabaseFailureIsNotAnEmptySuccess(t *testing.T) {
	service, vm := newVMStatsTestService(t)
	sqlDB, err := service.DB.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close SQL database: %v", err)
	}

	_, err = service.getVMUsageBootstrapAt(int(vm.RID), time.Now())
	if err == nil {
		t.Fatal("database failure returned a successful empty bootstrap")
	}
}

func TestGetVMUsageEmptyExplicitRangeSerializesAsArray(t *testing.T) {
	service, vm := newVMStatsTestService(t)

	got, err := service.GetVMUsage(int(vm.RID), db.GFSStepHourly)
	if err != nil {
		t.Fatalf("get explicit VM usage: %v", err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal explicit VM usage: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("explicit empty VM usage JSON = %s, want []", encoded)
	}
}
