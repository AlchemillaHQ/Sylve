// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func boolPtr(v bool) *bool {
	return &v
}

func newLifecycleTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(
		t,
		&taskModels.GuestLifecycleTask{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)

	s := NewService(dbConn, nil, nil)
	s.vmActionFn = func(_ uint, _ string) error { return nil }
	s.vmStateFn = func(_ uint) (int, error) { return 5, nil }
	s.jailActionFn = func(_ int, _ string) error { return nil }
	s.jailActiveFn = func(_ uint) (bool, error) { return false, nil }
	return s, dbConn
}

func TestCreateTaskConflictAndStopOverride(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	task, outcome, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "shutdown", taskModels.LifecycleTaskSourceUser, "tester", false)
	if err != nil {
		t.Fatalf("unexpected error creating shutdown task: %v", err)
	}
	if outcome != RequestOutcomeQueued {
		t.Fatalf("expected queued outcome, got %q", outcome)
	}
	if task == nil || task.ID == 0 {
		t.Fatalf("expected created task")
	}

	_, _, err = s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "start", taskModels.LifecycleTaskSourceUser, "tester", false)
	if !errors.Is(err, ErrTaskInProgress) {
		t.Fatalf("expected ErrTaskInProgress, got %v", err)
	}

	overrideTask, overrideOutcome, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "stop", taskModels.LifecycleTaskSourceUser, "tester", false)
	if err != nil {
		t.Fatalf("unexpected override error: %v", err)
	}
	if overrideOutcome != RequestOutcomeForceStopOverride {
		t.Fatalf("expected force stop outcome, got %q", overrideOutcome)
	}
	if overrideTask == nil || overrideTask.ID != task.ID {
		t.Fatalf("expected override to target active shutdown task")
	}

	refetched := taskModels.GuestLifecycleTask{}
	if err := dbConn.First(&refetched, task.ID).Error; err != nil {
		t.Fatalf("failed to refetch task: %v", err)
	}
	if !refetched.OverrideRequested {
		t.Fatalf("expected override_requested to be true")
	}
}

func TestExecuteTaskUpdatesStatus(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	failTask, _, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 220, "start", taskModels.LifecycleTaskSourceUser, "tester", false)
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	s.vmActionFn = func(_ uint, _ string) error { return fmt.Errorf("boom") }
	if err := s.ExecuteTask(context.Background(), failTask.ID); err == nil {
		t.Fatalf("expected execution error")
	}

	failed := taskModels.GuestLifecycleTask{}
	if err := dbConn.First(&failed, failTask.ID).Error; err != nil {
		t.Fatalf("failed to fetch failed task: %v", err)
	}
	if failed.Status != taskModels.LifecycleTaskStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Status)
	}
	if failed.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}
	if failed.Error == "" {
		t.Fatalf("expected task error to be persisted")
	}

	okTask, _, err := s.createTask(context.Background(), taskModels.GuestTypeJail, 330, "start", taskModels.LifecycleTaskSourceUser, "tester", false)
	if err != nil {
		t.Fatalf("failed to create success task: %v", err)
	}
	s.jailActionFn = func(_ int, _ string) error { return nil }
	s.jailActiveFn = func(_ uint) (bool, error) { return false, nil }

	if err := s.ExecuteTask(context.Background(), okTask.ID); err != nil {
		t.Fatalf("unexpected success task error: %v", err)
	}

	succeeded := taskModels.GuestLifecycleTask{}
	if err := dbConn.First(&succeeded, okTask.ID).Error; err != nil {
		t.Fatalf("failed to fetch success task: %v", err)
	}
	if succeeded.Status != taskModels.LifecycleTaskStatusSuccess {
		t.Fatalf("expected success status, got %s", succeeded.Status)
	}
}

func TestStartupAutostartOrder(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	jailTrue := boolPtr(true)
	jailFalse := boolPtr(false)

	jails := []jailModels.Jail{
		{CTID: 200, Name: "j2", Type: jailModels.JailTypeFreeBSD, StartAtBoot: jailTrue, StartOrder: 2},
		{CTID: 100, Name: "j1", Type: jailModels.JailTypeFreeBSD, StartAtBoot: jailTrue, StartOrder: 1},
		{CTID: 300, Name: "j3", Type: jailModels.JailTypeFreeBSD, StartAtBoot: jailFalse, StartOrder: 0},
	}
	for _, j := range jails {
		if err := dbConn.Create(&j).Error; err != nil {
			t.Fatalf("failed to create jail: %v", err)
		}
	}

	vms := []vmModels.VM{
		{RID: 300, Name: "vm3", StartAtBoot: true, StartOrder: 1},
		{RID: 200, Name: "vm2", StartAtBoot: true, StartOrder: 1},
		{RID: 100, Name: "vm1", StartAtBoot: false, StartOrder: 0},
	}
	for _, vm := range vms {
		if err := dbConn.Create(&vm).Error; err != nil {
			t.Fatalf("failed to create vm: %v", err)
		}
	}

	var order []string
	s.jailActionFn = func(ctid int, action string) error {
		order = append(order, fmt.Sprintf("jail:%d:%s", ctid, action))
		return nil
	}
	s.vmActionFn = func(rid uint, action string) error {
		order = append(order, fmt.Sprintf("vm:%d:%s", rid, action))
		return nil
	}
	s.jailActiveFn = func(_ uint) (bool, error) { return false, nil }
	s.vmStateFn = func(_ uint) (int, error) { return 5, nil }

	if err := s.runStartupAutostart(context.Background()); err != nil {
		t.Fatalf("startup autostart failed: %v", err)
	}

	expected := []string{
		"jail:100:start",
		"jail:200:start",
		"vm:200:start",
		"vm:300:start",
	}
	if !slices.Equal(order, expected) {
		t.Fatalf("unexpected startup order: got %v want %v", order, expected)
	}

	var startupTaskCount int64
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).
		Where("source = ? AND action = ?", taskModels.LifecycleTaskSourceStartup, "start").
		Count(&startupTaskCount).Error; err != nil {
		t.Fatalf("failed to count startup tasks: %v", err)
	}
	if startupTaskCount != int64(len(expected)) {
		t.Fatalf("unexpected startup task count: got %d want %d", startupTaskCount, len(expected))
	}
}
