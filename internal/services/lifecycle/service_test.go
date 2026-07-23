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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func boolPtr(v bool) *bool {
	return &v
}

type retryPendingTestError struct{}

func (retryPendingTestError) Error() string               { return "retry pending" }
func (retryPendingTestError) LifecycleRetryPending() bool { return true }

type persistedResultTestError struct{}

func (persistedResultTestError) Error() string                         { return "already persisted" }
func (persistedResultTestError) LifecycleResultAlreadyPersisted() bool { return true }

func newLifecycleTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(
		t,
		&taskModels.GuestLifecycleTask{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)

	s := NewService(dbConn, nil, nil, nil)
	s.vmActionFn = func(_ uint, _ string) error { return nil }
	s.vmStateFn = func(_ uint) (int, error) { return 5, nil }
	s.jailActionFn = func(_ int, _ string) error { return nil }
	s.jailActiveFn = func(_ uint) (bool, error) { return false, nil }
	return s, dbConn
}

func TestCreateTaskConflictAndStopOverride(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	task, outcome, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "shutdown", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if err != nil {
		t.Fatalf("unexpected error creating shutdown task: %v", err)
	}
	if outcome != RequestOutcomeQueued {
		t.Fatalf("expected queued outcome, got %q", outcome)
	}
	if task == nil || task.ID == 0 {
		t.Fatalf("expected created task")
	}

	_, _, err = s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if !errors.Is(err, ErrTaskInProgress) {
		t.Fatalf("expected ErrTaskInProgress, got %v", err)
	}

	overrideTask, overrideOutcome, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 101, "stop", taskModels.LifecycleTaskSourceUser, "tester", "", false)
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

	failTask, _, err := s.createTask(context.Background(), taskModels.GuestTypeVM, 220, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
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

	okTask, _, err := s.createTask(context.Background(), taskModels.GuestTypeJail, 330, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
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

func TestRecoverInterruptedTasksFailsNormalTasksOnly(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	startedAt := time.Now().Add(-time.Minute).UTC()
	tasks := []taskModels.GuestLifecycleTask{
		{
			GuestType: taskModels.GuestTypeJail,
			GuestID:   101,
			Action:    "start",
			Status:    taskModels.LifecycleTaskStatusRunning,
			StartedAt: &startedAt,
		},
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   102,
			Action:    "migrate",
			Status:    taskModels.LifecycleTaskStatusRunning,
			StartedAt: &startedAt,
		},
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   103,
			Action:    "start",
			Status:    taskModels.LifecycleTaskStatusQueued,
		},
	}
	for i := range tasks {
		if err := dbConn.Create(&tasks[i]).Error; err != nil {
			t.Fatalf("seed task %d: %v", i, err)
		}
	}

	if err := s.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatalf("RecoverInterruptedTasks: %v", err)
	}

	var recovered taskModels.GuestLifecycleTask
	if err := dbConn.First(&recovered, tasks[0].ID).Error; err != nil {
		t.Fatalf("reload recovered task: %v", err)
	}
	if recovered.Status != taskModels.LifecycleTaskStatusFailed || recovered.FinishedAt == nil {
		t.Fatalf("normal task was not finalized: status=%q finishedAt=%v", recovered.Status, recovered.FinishedAt)
	}
	if recovered.Message != lifecycleTaskInterruptedByRestartMessage || recovered.Error != lifecycleTaskInterruptedByRestartError {
		t.Fatalf("unexpected recovery result: message=%q error=%q", recovered.Message, recovered.Error)
	}

	for _, task := range tasks[1:] {
		var got taskModels.GuestLifecycleTask
		if err := dbConn.First(&got, task.ID).Error; err != nil {
			t.Fatalf("reload task %d: %v", task.ID, err)
		}
		if got.Status != task.Status || got.FinishedAt != nil {
			t.Fatalf("task %d changed unexpectedly: status=%q finishedAt=%v", task.ID, got.Status, got.FinishedAt)
		}
	}
}

func TestExecuteTaskKeepsRecoverableMigrationRunning(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	s.SetMigrationExecutor(func(context.Context, uint) error { return retryPendingTestError{} })
	task, _, err := s.createTask(
		t.Context(), taskModels.GuestTypeVM, 440, "migrate",
		taskModels.LifecycleTaskSourceUser, "tester", `{"targetNodeUuid":"node-b"}`, false,
	)
	if err != nil {
		t.Fatalf("create migration task: %v", err)
	}
	if err := s.ExecuteTask(t.Context(), task.ID); err == nil {
		t.Fatal("expected recovery-pending execution error")
	}

	var got taskModels.GuestLifecycleTask
	if err := dbConn.First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != taskModels.LifecycleTaskStatusRunning || got.FinishedAt != nil {
		t.Fatalf("recoverable migration became terminal: status=%q finishedAt=%v", got.Status, got.FinishedAt)
	}
	if got.Message != "migration_recovery_pending" || !strings.Contains(got.Error, "retry pending") {
		t.Fatalf("unexpected recovery state: message=%q error=%q", got.Message, got.Error)
	}
}

func TestRetryPendingDuplicateCannotOverwriteConcurrentMigrationSuccess(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(
		t.Context(), taskModels.GuestTypeVM, 442, "migrate",
		taskModels.LifecycleTaskSourceUser, "tester", `{"targetNodeUuid":"node-b"}`, false,
	)
	if err != nil {
		t.Fatalf("create migration task: %v", err)
	}
	s.SetMigrationExecutor(func(context.Context, uint) error {
		finishedAt := time.Now().UTC()
		if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).Updates(map[string]any{
			"status":      taskModels.LifecycleTaskStatusSuccess,
			"message":     "migration_completed",
			"finished_at": finishedAt,
		}).Error; err != nil {
			return err
		}
		return retryPendingTestError{}
	})
	if err := s.ExecuteTask(t.Context(), task.ID); err == nil {
		t.Fatal("expected duplicate retry-pending result")
	}
	if err := dbConn.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusSuccess || task.Message != "migration_completed" {
		t.Fatalf("duplicate overwrote completed migration: status=%q message=%q", task.Status, task.Message)
	}
}

func TestExecutionClaimCannotResurrectTerminalTask(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(
		t.Context(), taskModels.GuestTypeVM, 443, "migrate",
		taskModels.LifecycleTaskSourceUser, "tester", `{"targetNodeUuid":"node-b"}`, false,
	)
	if err != nil {
		t.Fatalf("create migration task: %v", err)
	}
	finishedAt := time.Now().UTC()
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status":      taskModels.LifecycleTaskStatusSuccess,
		"message":     "migration_completed",
		"finished_at": finishedAt,
	}).Error; err != nil {
		t.Fatalf("commit concurrent terminal result: %v", err)
	}

	claimed, err := s.claimTaskForExecution(t.Context(), task.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim stale delivery: %v", err)
	}
	if claimed {
		t.Fatal("stale delivery claimed a terminal task")
	}
	if err := dbConn.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusSuccess || task.Message != "migration_completed" {
		t.Fatalf("terminal task was resurrected: status=%q message=%q", task.Status, task.Message)
	}
}

func TestExecuteTaskPreservesMigrationCancellationResult(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	var taskID uint
	task, _, err := s.createTask(
		t.Context(), taskModels.GuestTypeJail, 441, "migrate",
		taskModels.LifecycleTaskSourceUser, "tester", `{"targetNodeUuid":"node-b"}`, false,
	)
	if err != nil {
		t.Fatalf("create migration task: %v", err)
	}
	taskID = task.ID
	s.SetMigrationExecutor(func(context.Context, uint) error {
		if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", taskID).Updates(map[string]any{
			"status":      taskModels.LifecycleTaskStatusFailed,
			"message":     "migration_cancelled",
			"error":       "cancelled_by_user",
			"finished_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		return persistedResultTestError{}
	})
	if err := s.ExecuteTask(t.Context(), task.ID); err == nil {
		t.Fatal("expected persisted cancellation error")
	}

	var got taskModels.GuestLifecycleTask
	if err := dbConn.First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != taskModels.LifecycleTaskStatusFailed || got.Message != "migration_cancelled" || got.Error != "cancelled_by_user" {
		t.Fatalf("cancellation result was overwritten: status=%q message=%q error=%q", got.Status, got.Message, got.Error)
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

func TestExecuteTaskVMTemplateConvertAndCreate(t *testing.T) {
	s, _ := newLifecycleTestService(t)

	convertCalled := false
	expectedConvertReq := libvirtServiceInterfaces.ConvertToTemplateRequest{
		Name: "vm-template-777",
	}
	s.vmTemplateConvertFn = func(_ context.Context, rid uint, req libvirtServiceInterfaces.ConvertToTemplateRequest) error {
		convertCalled = true
		if rid != 777 {
			t.Fatalf("unexpected convert rid: %d", rid)
		}
		if req.Name != expectedConvertReq.Name {
			t.Fatalf("unexpected convert request: %#v", req)
		}
		return nil
	}

	convertPayload, err := json.Marshal(expectedConvertReq)
	if err != nil {
		t.Fatalf("failed to marshal convert payload: %v", err)
	}

	convertTask, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		777,
		"convert",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		string(convertPayload),
		false,
	)
	if err != nil {
		t.Fatalf("failed to create vm-template convert task: %v", err)
	}
	if err := s.ExecuteTask(context.Background(), convertTask.ID); err != nil {
		t.Fatalf("vm-template convert execute failed: %v", err)
	}
	if !convertCalled {
		t.Fatalf("expected vmTemplateConvertFn to be called")
	}

	expectedReq := libvirtServiceInterfaces.CreateFromTemplateRequest{
		Mode:       "single",
		RID:        881,
		Name:       "vm-881",
		NamePrefix: "vm",
	}
	payload, err := json.Marshal(expectedReq)
	if err != nil {
		t.Fatalf("failed to marshal create payload: %v", err)
	}

	createCalled := false
	s.vmTemplateCreateFn = func(_ context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error {
		createCalled = true
		if templateID != 55 {
			t.Fatalf("unexpected template id: %d", templateID)
		}
		if req.Mode != expectedReq.Mode || req.RID != expectedReq.RID || req.Name != expectedReq.Name || req.NamePrefix != expectedReq.NamePrefix {
			t.Fatalf("unexpected create request: %#v", req)
		}
		return nil
	}

	createTask, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		55,
		"create",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		string(payload),
		false,
	)
	if err != nil {
		t.Fatalf("failed to create vm-template create task: %v", err)
	}
	if err := s.ExecuteTask(context.Background(), createTask.ID); err != nil {
		t.Fatalf("vm-template create execute failed: %v", err)
	}
	if !createCalled {
		t.Fatalf("expected vmTemplateCreateFn to be called")
	}
}

func TestExecuteTaskVMTemplateCreateInvalidPayload(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	createCalled := false
	s.vmTemplateCreateFn = func(_ context.Context, _ uint, _ libvirtServiceInterfaces.CreateFromTemplateRequest) error {
		createCalled = true
		return nil
	}

	task, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		12,
		"create",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"{invalid-json",
		false,
	)
	if err != nil {
		t.Fatalf("failed to create vm-template task: %v", err)
	}

	execErr := s.ExecuteTask(context.Background(), task.ID)
	if execErr == nil || !strings.Contains(execErr.Error(), "invalid_vm_template_create_payload") {
		t.Fatalf("expected invalid payload error, got %v", execErr)
	}
	if createCalled {
		t.Fatalf("expected vmTemplateCreateFn to not be called on invalid payload")
	}

	var failed taskModels.GuestLifecycleTask
	if err := dbConn.First(&failed, task.ID).Error; err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}
	if failed.Status != taskModels.LifecycleTaskStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Status)
	}
}

func TestExecuteTaskVMTemplateConvertInvalidPayload(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	convertCalled := false
	s.vmTemplateConvertFn = func(_ context.Context, _ uint, _ libvirtServiceInterfaces.ConvertToTemplateRequest) error {
		convertCalled = true
		return nil
	}

	task, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		88,
		"convert",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"{invalid-json",
		false,
	)
	if err != nil {
		t.Fatalf("failed to create vm-template convert task: %v", err)
	}

	execErr := s.ExecuteTask(context.Background(), task.ID)
	if execErr == nil || !strings.Contains(execErr.Error(), "invalid_vm_template_convert_payload") {
		t.Fatalf("expected invalid convert payload error, got %v", execErr)
	}
	if convertCalled {
		t.Fatalf("expected vmTemplateConvertFn not called on invalid payload")
	}

	var failed taskModels.GuestLifecycleTask
	if err := dbConn.First(&failed, task.ID).Error; err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}
	if failed.Status != taskModels.LifecycleTaskStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Status)
	}
}

func TestListAndGetTasks(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)

	tasks := []taskModels.GuestLifecycleTask{
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   101,
			Action:    "start",
			Status:    taskModels.LifecycleTaskStatusQueued,
		},
		{
			GuestType: taskModels.GuestTypeJail,
			GuestID:   101,
			Action:    "restart",
			Status:    taskModels.LifecycleTaskStatusRunning,
		},
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   102,
			Action:    "stop",
			Status:    taskModels.LifecycleTaskStatusSuccess,
		},
	}
	for index := range tasks {
		if err := dbConn.Create(&tasks[index]).Error; err != nil {
			t.Fatalf("seed task %d: %v", index, err)
		}
	}

	active, err := s.ListActiveTasks(taskModels.GuestTypeVM, 101)
	if err != nil {
		t.Fatalf("list active tasks: %v", err)
	}
	if len(active) != 1 || active[0].ID != tasks[0].ID {
		t.Fatalf("active tasks = %#v, want VM task %d", active, tasks[0].ID)
	}

	recent, err := s.ListRecentTasks(taskModels.GuestTypeVM, 0, 1)
	if err != nil {
		t.Fatalf("list recent tasks: %v", err)
	}
	if len(recent) != 1 || recent[0].ID != tasks[2].ID {
		t.Fatalf("recent tasks = %#v, want task %d", recent, tasks[2].ID)
	}

	got, err := s.GetTask(tasks[1].ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got == nil || got.ID != tasks[1].ID || got.Status != taskModels.LifecycleTaskStatusRunning {
		t.Fatalf("got task = %#v", got)
	}

	missing, err := s.GetTask(9999)
	if err != nil {
		t.Fatalf("get missing task: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected missing task to be nil, got %#v", missing)
	}

	if _, err := s.GetTask(0); err == nil || err.Error() != "invalid_task_id" {
		t.Fatalf("GetTask(0) error = %v", err)
	}
}
