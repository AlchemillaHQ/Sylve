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
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

func boolPtr(v bool) *bool {
	return &v
}

func setupLifecycleAuditTest(t *testing.T) (*Service, *gorm.DB, *gorm.DB) {
	t.Helper()

	service, primaryDB := newLifecycleTestService(t)
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	service.TelemetryDB = telemetryDB

	config := &internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}
	if err := db.SetupQueue(config, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("setup lifecycle test queue: %v", err)
	}

	return service, primaryDB, telemetryDB
}

func createStartedLifecycleAudit(t *testing.T, telemetryDB *gorm.DB) infoModels.AuditRecord {
	t.Helper()
	record := infoModels.AuditRecord{
		User:    "tester",
		Action:  `{"method":"POST","path":"/api/vm/101/actions/start"}`,
		Status:  "started",
		Started: time.Now().UTC(),
		Version: 2,
	}
	if err := telemetryDB.Create(&record).Error; err != nil {
		t.Fatalf("create lifecycle audit record: %v", err)
	}
	return record
}

func TestRequestActionBindsAuditBeforeQueuePublicationAndIsolatesJobType(t *testing.T) {
	service, _, telemetryDB := setupLifecycleAuditTest(t)
	audit := createStartedLifecycleAudit(t, telemetryDB)
	requestContext := db.ContextWithAuditRecordID(t.Context(), audit.ID)

	task, outcome, err := service.RequestAction(
		requestContext,
		taskModels.GuestTypeVM,
		101,
		"start",
		taskModels.LifecycleTaskSourceUser,
		"tester",
	)
	if err != nil {
		t.Fatalf("request VM action: %v", err)
	}
	if task == nil || task.ID == 0 || outcome != RequestOutcomeQueued {
		t.Fatalf("unexpected queued task: task=%+v outcome=%q", task, outcome)
	}

	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload prepared audit: %v", err)
	}
	if audit.Status != "pending" || audit.AsyncJobID == nil || *audit.AsyncJobID != task.ID ||
		audit.AsyncJobType != "vm_start" || audit.AsyncOperationID == "" {
		t.Fatalf("audit was not bound before publication: %+v", audit)
	}

	unrelatedJobID := task.ID
	unrelated := infoModels.AuditRecord{
		User:             "backup-user",
		Action:           `{}`,
		Status:           "pending",
		Started:          time.Now().UTC(),
		Version:          2,
		AsyncJobID:       &unrelatedJobID,
		AsyncJobType:     "backup_job_run",
		AsyncOperationID: fmt.Sprintf("backup:%d", task.ID),
	}
	if err := telemetryDB.Create(&unrelated).Error; err != nil {
		t.Fatalf("create unrelated pending audit: %v", err)
	}

	if err := service.ExecuteTask(t.Context(), task.ID); err != nil {
		t.Fatalf("execute lifecycle task: %v", err)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload completed lifecycle audit: %v", err)
	}
	if audit.Status != "success" {
		t.Fatalf("lifecycle audit status = %q, want success", audit.Status)
	}
	if err := telemetryDB.First(&unrelated, unrelated.ID).Error; err != nil {
		t.Fatalf("reload unrelated audit: %v", err)
	}
	if unrelated.Status != "pending" {
		t.Fatalf("unrelated audit status = %q, want pending", unrelated.Status)
	}
}

func TestForceStopOverrideAuditCompletesWithShutdownTask(t *testing.T) {
	service, _, telemetryDB := setupLifecycleAuditTest(t)
	shutdownTask, _, err := service.createTask(
		t.Context(),
		taskModels.GuestTypeVM,
		101,
		"shutdown",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("seed shutdown task: %v", err)
	}

	audit := createStartedLifecycleAudit(t, telemetryDB)
	requestContext := db.ContextWithAuditRecordID(t.Context(), audit.ID)
	overrideTask, outcome, err := service.RequestAction(
		requestContext,
		taskModels.GuestTypeVM,
		101,
		"stop",
		taskModels.LifecycleTaskSourceUser,
		"tester",
	)
	if err != nil {
		t.Fatalf("request force stop override: %v", err)
	}
	if overrideTask == nil || overrideTask.ID != shutdownTask.ID ||
		outcome != RequestOutcomeForceStopOverride {
		t.Fatalf("unexpected override task: task=%+v outcome=%q", overrideTask, outcome)
	}

	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload prepared override audit: %v", err)
	}
	if audit.Status != "pending" || audit.AsyncJobType != "vm_stop" ||
		audit.AsyncJobID == nil || *audit.AsyncJobID != shutdownTask.ID {
		t.Fatalf("override audit was not bound to shutdown task: %+v", audit)
	}

	if err := service.ExecuteTask(t.Context(), shutdownTask.ID); err != nil {
		t.Fatalf("execute shutdown task: %v", err)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload completed override audit: %v", err)
	}
	if audit.Status != "success" {
		t.Fatalf("override audit status = %q, want success", audit.Status)
	}
}

func TestRecoverInterruptedTasksFinalizesBoundAudit(t *testing.T) {
	service, primaryDB, telemetryDB := setupLifecycleAuditTest(t)
	task, _, err := service.createTask(
		t.Context(),
		taskModels.GuestTypeVM,
		101,
		"start",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("seed lifecycle task: %v", err)
	}
	if err := primaryDB.Model(&taskModels.GuestLifecycleTask{}).
		Where("id = ?", task.ID).
		Update("status", taskModels.LifecycleTaskStatusRunning).Error; err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	audit := createStartedLifecycleAudit(t, telemetryDB)
	if _, err := db.PrepareAsyncAuditRecord(
		telemetryDB,
		db.ContextWithAuditRecordID(t.Context(), audit.ID),
		"vm_start",
		task.ID,
		fmt.Sprintf("guest-lifecycle:vm_start:%d", task.ID),
	); err != nil {
		t.Fatalf("bind lifecycle audit: %v", err)
	}

	if err := service.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatalf("recover lifecycle tasks: %v", err)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload recovered audit: %v", err)
	}
	if audit.Status != "failed" || audit.Error != lifecycleTaskInterruptedByRestartError {
		t.Fatalf("recovered audit = status %q error %q", audit.Status, audit.Error)
	}
}

func TestRecoverQueuedTaskFinalizesBoundAudit(t *testing.T) {
	service, primaryDB, telemetryDB := setupLifecycleAuditTest(t)
	starts := 0
	service.vmActionFn = func(_ uint, _ string) error {
		starts++
		return nil
	}
	task, _, err := service.createTask(
		t.Context(), taskModels.GuestTypeVM, 102, "start",
		taskModels.LifecycleTaskSourceUser, "tester", "", false,
	)
	if err != nil {
		t.Fatalf("seed queued task: %v", err)
	}
	audit := createStartedLifecycleAudit(t, telemetryDB)
	if _, err := db.PrepareAsyncAuditRecord(
		telemetryDB, db.ContextWithAuditRecordID(t.Context(), audit.ID),
		"vm_start", task.ID, fmt.Sprintf("guest-lifecycle:vm_start:%d", task.ID),
	); err != nil {
		t.Fatalf("bind queued task audit: %v", err)
	}

	if err := service.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatalf("recover queued task: %v", err)
	}
	var recovered taskModels.GuestLifecycleTask
	if err := primaryDB.First(&recovered, task.ID).Error; err != nil {
		t.Fatalf("reload recovered task: %v", err)
	}
	if recovered.Status != taskModels.LifecycleTaskStatusFailed || recovered.FinishedAt == nil {
		t.Fatalf("queued task was not failed: %+v", recovered)
	}
	if err := service.executeQueuedTask(t.Context(), task.ID); err != nil {
		t.Fatalf("late delivery for recovered task: %v", err)
	}
	if starts != 0 {
		t.Fatalf("late delivery invoked guest action %d times", starts)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload recovered audit: %v", err)
	}
	if audit.Status != "failed" || audit.Error != lifecycleTaskInterruptedByRestartError {
		t.Fatalf("recovered audit = status %q error %q", audit.Status, audit.Error)
	}
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
		&clusterModels.ReplicationGuestOperation{},
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

func TestTemplateTaskConflictsAreActionAware(t *testing.T) {
	s, _ := newLifecycleTestService(t)

	convertTask, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		77,
		"convert",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("create convert task: %v", err)
	}

	createTask, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		77,
		"create",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("different template action should not conflict: %v", err)
	}
	if convertTask.ID == createTask.ID {
		t.Fatalf("expected distinct tasks, got ID %d", createTask.ID)
	}

	if _, _, err := s.createTask(
		context.Background(),
		taskModels.GuestTypeVMTemplate,
		77,
		"create",
		taskModels.LifecycleTaskSourceUser,
		"tester",
		"",
		false,
	); !errors.Is(err, ErrTaskInProgress) {
		t.Fatalf("same template action should conflict, got %v", err)
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

func TestQueuedTaskRetriesClaimFailureBeforeGuestAction(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(t.Context(), taskModels.GuestTypeVM, 221, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	s.vmActionFn = func(_ uint, _ string) error {
		starts++
		return nil
	}
	claimErr := errors.New("temporary claim failure")
	const callbackName = "test:fail_lifecycle_claim"
	if err := dbConn.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		tx.AddError(claimErr)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.executeQueuedTask(t.Context(), task.ID); !errors.Is(err, claimErr) {
		t.Fatalf("queued handler error = %v, want retryable claim error", err)
	}
	if starts != 0 {
		t.Fatalf("guest action ran after failed claim: %d", starts)
	}
	var unclaimed taskModels.GuestLifecycleTask
	if err := dbConn.First(&unclaimed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unclaimed.Status != taskModels.LifecycleTaskStatusQueued {
		t.Fatalf("failed claim changed task status to %q", unclaimed.Status)
	}
	if err := dbConn.Callback().Update().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	if err := s.executeQueuedTask(t.Context(), task.ID); err != nil {
		t.Fatalf("retry queued task: %v", err)
	}
	if starts != 1 {
		t.Fatalf("guest action invocations = %d, want 1", starts)
	}
}

func TestQueuedTaskConsumesGuestActionFailure(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(t.Context(), taskModels.GuestTypeVM, 222, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.vmActionFn = func(_ uint, _ string) error { return errors.New("guest action failed") }
	if err := s.executeQueuedTask(t.Context(), task.ID); err != nil {
		t.Fatalf("guest action failure must not retry: %v", err)
	}
	var failed taskModels.GuestLifecycleTask
	if err := dbConn.First(&failed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != taskModels.LifecycleTaskStatusFailed || failed.Error != "guest action failed" {
		t.Fatalf("guest action result was not saved: %+v", failed)
	}
}

func TestQueuedTaskDoesNotRepeatRunningGuestAction(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(t.Context(), taskModels.GuestTypeVM, 223, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).
		Update("status", taskModels.LifecycleTaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	starts := 0
	s.vmActionFn = func(_ uint, _ string) error {
		starts++
		return nil
	}
	if err := s.executeQueuedTask(t.Context(), task.ID); err == nil {
		t.Fatal("duplicate delivery should wait for the running task")
	}
	if starts != 0 {
		t.Fatalf("duplicate delivery invoked guest action %d times", starts)
	}
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).
		Update("status", taskModels.LifecycleTaskStatusSuccess).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.executeQueuedTask(t.Context(), task.ID); err != nil {
		t.Fatalf("late delivery of completed task: %v", err)
	}
}

func TestRunningMigrationCanBeReclaimed(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	task, _, err := s.createTask(t.Context(), taskModels.GuestTypeVM, 224, "migrate", taskModels.LifecycleTaskSourceUser, "tester", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).
		Update("status", taskModels.LifecycleTaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	executions := 0
	s.SetMigrationExecutor(func(context.Context, uint) error {
		executions++
		return nil
	})
	if err := s.ExecuteTask(t.Context(), task.ID); err != nil {
		t.Fatalf("reclaim running migration: %v", err)
	}
	if executions != 1 {
		t.Fatalf("migration executions = %d, want 1", executions)
	}
}

func TestRecoverInterruptedTasksFailsUnclaimedAndNonMigrationTasks(t *testing.T) {
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
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   104,
			Action:    "migrate",
			Status:    taskModels.LifecycleTaskStatusQueued,
		},
		{
			GuestType: taskModels.GuestTypeVM,
			GuestID:   105,
			Action:    "migrate",
			Status:    taskModels.LifecycleTaskStatusQueued,
		},
		{
			GuestType: taskModels.GuestTypeVMTemplate,
			GuestID:   106,
			Action:    "create",
			Status:    taskModels.LifecycleTaskStatusQueued,
		},
	}
	for i := range tasks {
		if err := dbConn.Create(&tasks[i]).Error; err != nil {
			t.Fatalf("seed task %d: %v", i, err)
		}
	}
	if err := dbConn.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:    taskModels.GuestTypeVM,
		GuestID:      tasks[4].GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationPreCutover,
		Token:        fmt.Sprintf("migration:node-a:%d", tasks[4].ID),
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       tasks[4].ID,
		AcquiredAt:   startedAt,
	}).Error; err != nil {
		t.Fatalf("seed migration operation: %v", err)
	}

	if err := s.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatalf("RecoverInterruptedTasks: %v", err)
	}

	for i, task := range tasks {
		var got taskModels.GuestLifecycleTask
		if err := dbConn.First(&got, task.ID).Error; err != nil {
			t.Fatalf("reload task %d: %v", task.ID, err)
		}
		if i == 1 || i == 4 {
			if got.Status != task.Status || got.FinishedAt != nil {
				t.Fatalf("migration task %d changed unexpectedly: status=%q finishedAt=%v", task.ID, got.Status, got.FinishedAt)
			}
			continue
		}
		if got.Status != taskModels.LifecycleTaskStatusFailed || got.FinishedAt == nil ||
			got.Message != lifecycleTaskInterruptedByRestartMessage || got.Error != lifecycleTaskInterruptedByRestartError {
			t.Fatalf("task %d was not recovered: status=%q message=%q error=%q finishedAt=%v",
				task.ID, got.Status, got.Message, got.Error, got.FinishedAt)
		}
	}
}

func TestRecoverQueuedStartupTaskAllowsAutostart(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	if err := dbConn.Create(&vmModels.VM{RID: 105, Name: "vm105", StartAtBoot: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Create(&vmModels.VM{RID: 106, Name: "vm106", StartAtBoot: true}).Error; err != nil {
		t.Fatal(err)
	}
	orphan := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   105,
		Action:    "start",
		Source:    taskModels.LifecycleTaskSourceStartup,
		Status:    taskModels.LifecycleTaskStatusQueued,
	}
	if err := dbConn.Create(&orphan).Error; err != nil {
		t.Fatal(err)
	}
	manuallyStartedOrphan := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   106,
		Action:    "start",
		Source:    taskModels.LifecycleTaskSourceStartup,
		Status:    taskModels.LifecycleTaskStatusQueued,
	}
	if err := dbConn.Create(&manuallyStartedOrphan).Error; err != nil {
		t.Fatal(err)
	}
	starts := 0
	s.vmStateFn = func(rid uint) (int, error) {
		if rid == 106 {
			return 1, nil
		}
		return 5, nil
	}
	s.vmActionFn = func(_ uint, _ string) error {
		starts++
		return nil
	}

	if err := s.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.executeQueuedTask(t.Context(), orphan.ID); err != nil {
		t.Fatalf("late delivery for recovered task: %v", err)
	}
	if err := s.runStartupAutostart(t.Context()); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("VM starts = %d, want 1", starts)
	}
	var oldTask taskModels.GuestLifecycleTask
	if err := dbConn.First(&oldTask, orphan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if oldTask.Status != taskModels.LifecycleTaskStatusFailed || oldTask.FinishedAt == nil {
		t.Fatalf("orphan task was not closed: %+v", oldTask)
	}
	var activeCount, successfulCount int64
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).
		Where("guest_type = ? AND guest_id = ? AND status IN ?", taskModels.GuestTypeVM, 105,
			[]string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).
		Count(&activeCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).
		Where("guest_type = ? AND guest_id = ? AND status = ?", taskModels.GuestTypeVM, 105, taskModels.LifecycleTaskStatusSuccess).
		Count(&successfulCount).Error; err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 || successfulCount != 1 {
		t.Fatalf("recovered VM tasks: active=%d success=%d, want 0 and 1", activeCount, successfulCount)
	}
	var alreadyRunning taskModels.GuestLifecycleTask
	if err := dbConn.Where("guest_type = ? AND guest_id = ? AND status = ?", taskModels.GuestTypeVM, 106, taskModels.LifecycleTaskStatusSuccess).
		First(&alreadyRunning).Error; err != nil {
		t.Fatal(err)
	}
	if alreadyRunning.Message != "already_running" {
		t.Fatalf("manually started VM result = %q, want already_running", alreadyRunning.Message)
	}
	active, err := s.GetActiveTaskForGuest(taskModels.GuestTypeVM, 106)
	if err != nil || active != nil {
		t.Fatalf("manually started VM still has an active task: task=%+v err=%v", active, err)
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

	claimed, err := s.claimTaskForExecution(t.Context(), task.ID, task.Action, time.Now().UTC())
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

func TestStartupAutostartStopsAfterCancellation(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	for _, rid := range []uint{102, 104, 105} {
		if err := dbConn.Create(&vmModels.VM{RID: rid, Name: fmt.Sprintf("vm%d", rid), StartAtBoot: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	starts := 0
	s.vmActionFn = func(_ uint, _ string) error {
		starts++
		cancel()
		return nil
	}
	if err := s.runStartupAutostart(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("autostart error = %v, want context cancellation", err)
	}
	if starts != 1 {
		t.Fatalf("guest action invocations = %d, want 1", starts)
	}
	var startupTasks []taskModels.GuestLifecycleTask
	if err := dbConn.Where("source = ?", taskModels.LifecycleTaskSourceStartup).Find(&startupTasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(startupTasks) != 1 || startupTasks[0].Status != taskModels.LifecycleTaskStatusSuccess {
		t.Fatalf("canceled startup left unexpected tasks: %+v", startupTasks)
	}
}

type cancelOnSecondMutationGate struct {
	calls  int
	cancel context.CancelFunc
}

func (g *cancelOnSecondMutationGate) EnterMutation(ctx context.Context) (context.Context, func(), error) {
	g.calls++
	if g.calls == 2 {
		g.cancel()
	}
	return ctx, func() {}, nil
}

func TestStartupClaimCancellationClosesQueuedTask(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	if err := dbConn.Create(&vmModels.VM{RID: 226, Name: "vm226", StartAtBoot: true}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.SetMutationAdmission(&cancelOnSecondMutationGate{cancel: cancel})
	starts := 0
	s.vmActionFn = func(_ uint, _ string) error {
		starts++
		return nil
	}
	if err := s.runStartupAutostart(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("autostart error = %v, want context cancellation", err)
	}
	if starts != 0 {
		t.Fatalf("guest action invoked %d times after canceled claim", starts)
	}
	var tasks []taskModels.GuestLifecycleTask
	if err := dbConn.Where("source = ?", taskModels.LifecycleTaskSourceStartup).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != taskModels.LifecycleTaskStatusFailed ||
		tasks[0].Message != "startup_task_not_started" || tasks[0].FinishedAt == nil {
		t.Fatalf("canceled claim left unexpected tasks: %+v", tasks)
	}
}

func TestCanceledTaskCreationLeavesNoQueuedRow(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.createTask(ctx, taskModels.GuestTypeVM, 225, "start", taskModels.LifecycleTaskSourceUser, "tester", "", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("create task error = %v, want context cancellation", err)
	}
	var count int64
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("canceled request created %d task rows", count)
	}
}

func TestStartupAutostartSkipsOnlyGuestsThatAreNotReady(t *testing.T) {
	s, dbConn := newLifecycleTestService(t)
	startAtBoot := boolPtr(true)
	if err := dbConn.Create(&jailModels.Jail{
		CTID: 101, Name: "protected", Type: jailModels.JailTypeFreeBSD, StartAtBoot: startAtBoot,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Create(&vmModels.VM{RID: 202, Name: "unprotected", StartAtBoot: true}).Error; err != nil {
		t.Fatal(err)
	}

	s.SetStartupGuestReadinessChecker(func(guestType string, guestID uint) (bool, error) {
		return guestType == taskModels.GuestTypeVM && guestID == 202, nil
	})
	var started []string
	s.jailActionFn = func(ctid int, action string) error {
		started = append(started, fmt.Sprintf("jail:%d:%s", ctid, action))
		return nil
	}
	s.vmActionFn = func(rid uint, action string) error {
		started = append(started, fmt.Sprintf("vm:%d:%s", rid, action))
		return nil
	}

	if err := s.runStartupAutostart(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"vm:202:start"}; !slices.Equal(started, want) {
		t.Fatalf("started guests = %v, want %v", started, want)
	}

	var taskCount int64
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).
		Where("source = ?", taskModels.LifecycleTaskSourceStartup).
		Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("startup task count = %d, want 1", taskCount)
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
	activeCount, err := s.CountActiveTasks(context.Background())
	if err != nil {
		t.Fatalf("count active tasks: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("active task count = %d, want 2", activeCount)
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
