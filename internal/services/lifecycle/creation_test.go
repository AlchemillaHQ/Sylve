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
	"testing"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"gorm.io/gorm"
)

func TestJailCreationQueuedAdmissionAndDuplicateDelivery(t *testing.T) {
	s, primary, _ := setupLifecycleAuditTest(t)
	ctID := uint(890)
	request := jailServiceInterfaces.CreateJailRequest{Name: "queued", CTID: &ctID, Base: "base"}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	prepared, executed := 0, 0
	s.jailCreatePrepareFn = func(ctx context.Context, taskID uint, req jailServiceInterfaces.CreateJailRequest) error {
		prepared++
		var task taskModels.GuestLifecycleTask
		if err := primary.WithContext(ctx).First(&task, taskID).Error; err != nil {
			return err
		}
		if task.Status != "queued" || req.CTID == nil || *req.CTID != ctID {
			return fmt.Errorf("wrong_admission_identity")
		}
		return nil
	}
	s.jailCreateFn = func(context.Context, uint) error { executed++; return nil }
	ctx, cancel := context.WithCancel(t.Context())
	task, outcome, err := s.RequestActionWithPayload(ctx, "jail", ctID, "create", "user", "tester", string(payload))
	if err != nil || outcome != RequestOutcomeQueued || prepared != 1 {
		t.Fatalf("request=%+v outcome=%s prepared=%d err=%v", task, outcome, prepared, err)
	}
	cancel()
	if _, _, err := s.RequestActionWithPayload(t.Context(), "jail", ctID, "create", "user", "tester", string(payload)); !errors.Is(err, ErrTaskInProgress) {
		t.Fatalf("duplicate = %v", err)
	}
	for range 2 {
		if err := s.executeQueuedTask(t.Context(), task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if executed != 1 {
		t.Fatalf("executions = %d", executed)
	}
}

func TestJailCreationAuditPublicationFailureUnwindsAdmission(t *testing.T) {
	s, _, telemetry := setupLifecycleAuditTest(t)
	ctID := uint(891)
	payload, err := json.Marshal(jailServiceInterfaces.CreateJailRequest{CTID: &ctID})
	if err != nil {
		t.Fatal(err)
	}
	s.jailCreatePrepareFn = func(context.Context, uint, jailServiceInterfaces.CreateJailRequest) error { return nil }
	cancelled := 0
	s.jailCreateCancelFn = func(ctx context.Context, taskID uint, cause error) error {
		cancelled++
		if ctx.Err() != nil || taskID == 0 || cause == nil {
			return fmt.Errorf("invalid_cleanup_context")
		}
		return nil
	}
	audit := createStartedLifecycleAudit(t, telemetry)
	const callback = "test:fail_creation_audit_publish"
	if err := telemetry.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) { tx.AddError(fmt.Errorf("injected_audit_failure")) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { telemetry.Callback().Update().Remove(callback) })
	ctx := db.ContextWithAuditRecordID(t.Context(), audit.ID)
	if _, _, err := s.RequestActionWithPayload(ctx, "jail", ctID, "create", "user", "tester", string(payload)); err == nil {
		t.Fatal("failed publication was accepted")
	}
	if cancelled != 1 {
		t.Fatalf("cancelled = %d", cancelled)
	}
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" {
		t.Fatalf("task = %+v", task)
	}
}

func TestRecoverCreationTasksUsesDurableOutcomeNotJailRow(t *testing.T) {
	s, primary := newLifecycleTestService(t)
	if err := primary.AutoMigrate(&jailModels.JailCreation{}); err != nil {
		t.Fatal(err)
	}
	for i, phase := range []string{"committed", "failed", "ready"} {
		task := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: uint(892 + i), Action: "create", Status: "running"}
		if err := primary.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		op := jailModels.JailCreation{ID: fmt.Sprint(i), TaskID: &task.ID, CTID: task.GuestID, Phase: phase, State: "{}", Request: "{}", Error: "cleanup_blocked"}
		if err := primary.Create(&op).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecoverInterruptedTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	var tasks []taskModels.GuestLifecycleTask
	if err := primary.Order("id").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != "success" || tasks[1].Status != "failed" || tasks[1].Error != "cleanup_blocked" || tasks[2].Status != "running" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestJailCreationQueuePublicationFailureCancelsAdmission(t *testing.T) {
	s, _, _ := setupLifecycleAuditTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctID := uint(895)
	payload, err := json.Marshal(jailServiceInterfaces.CreateJailRequest{CTID: &ctID})
	if err != nil {
		t.Fatal(err)
	}
	s.jailCreatePrepareFn = func(context.Context, uint, jailServiceInterfaces.CreateJailRequest) error { cancel(); return nil }
	cancelled := 0
	s.jailCreateCancelFn = func(ctx context.Context, _ uint, _ error) error {
		cancelled++
		if ctx.Err() != nil {
			t.Fatal("cleanup inherited cancelled request")
		}
		return nil
	}
	if _, _, err := s.RequestActionWithPayload(ctx, "jail", ctID, "create", "user", "tester", string(payload)); err == nil {
		t.Fatal("failed queue publication accepted")
	}
	if cancelled != 1 {
		t.Fatalf("cancelled = %d", cancelled)
	}
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" {
		t.Fatalf("task = %+v", task)
	}
}

func TestJailCreationLifecycleRejectsSnapshotPayloadBeforeAdmission(t *testing.T) {
	s, _, _ := setupLifecycleAuditTest(t)
	prepared := false
	s.jailCreatePrepareFn = func(context.Context, uint, jailServiceInterfaces.CreateJailRequest) error {
		prepared = true
		return nil
	}
	payload := `{"ctId":896,"zfsSource":{"dataset":"pool/root","guid":"123","snapshot":"selected","snapshotGuid":"456"}}`
	task, _, err := s.RequestActionWithPayload(t.Context(), "jail", 896, "create", "user", "tester", payload)
	if err == nil || task != nil || prepared {
		t.Fatalf("snapshot task admitted: task=%+v error=%v prepared=%v", task, err, prepared)
	}
	var recorded taskModels.GuestLifecycleTask
	if err := s.DB.First(&recorded).Error; err != nil {
		t.Fatal(err)
	}
	if recorded.Status != taskModels.LifecycleTaskStatusFailed {
		t.Fatalf("invalid payload left an active task: %+v", recorded)
	}
}
