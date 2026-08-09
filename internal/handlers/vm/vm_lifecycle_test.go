// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

type vmActionTestResponse struct {
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Data    VMActionResponse `json:"data"`
	Error   string           `json:"error"`
}

type vmActionPreflightStub struct {
	getErr   error
	guardErr error
	allowed  bool
	calls    int
}

func (s *vmActionPreflightStub) GetVMByRID(rid uint) (vmModels.VM, error) {
	s.calls++
	if s.getErr != nil {
		return vmModels.VM{}, s.getErr
	}
	return vmModels.VM{RID: rid, Name: "test-vm"}, nil
}

func (s *vmActionPreflightStub) CanPerformVMAction(_ uint, _ string) (bool, error) {
	if s.guardErr != nil {
		return false, s.guardErr
	}
	return s.allowed, nil
}

type vmLifecycleRequestStub struct {
	task    *taskModels.GuestLifecycleTask
	outcome string
	err     error
	calls   int
}

func (s *vmLifecycleRequestStub) RequestAction(
	context.Context,
	string,
	uint,
	string,
	string,
	string,
) (*taskModels.GuestLifecycleTask, string, error) {
	s.calls++
	return s.task, s.outcome, s.err
}

func setupVMActionHandlerTest(t *testing.T) (*gin.Engine, *lifecycle.Service, *gorm.DB) {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})

	cfg := &internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}
	if err := db.SetupQueue(cfg, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("failed to setup test queue: %v", err)
	}

	lifecycleSvc := lifecycle.NewService(dbConn, nil, nil, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	preflight := &vmActionPreflightStub{allowed: true}
	r.POST("/vm/:rid/actions/:action", func(c *gin.Context) {
		c.Set("Username", "tester")
		VMActionHandler(preflight, lifecycleSvc)(c)
	})

	return r, lifecycleSvc, dbConn
}

func TestVMActionHandlerQueuedAccepted(t *testing.T) {
	r, _, _ := setupVMActionHandlerTest(t)

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/101/actions/start", nil, nil)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[vmActionTestResponse](t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success status, got %q", resp.Status)
	}
	if resp.Message != "vm_action_queued" {
		t.Fatalf("expected vm_action_queued message, got %q", resp.Message)
	}

	if resp.Data.TaskID == 0 || resp.Data.RID != 101 || resp.Data.Action != "start" ||
		resp.Data.Outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("unexpected lifecycle response: %+v", resp.Data)
	}
}

func TestVMActionHandlerConflictWhenTaskActive(t *testing.T) {
	r, lifecycleSvc, _ := setupVMActionHandlerTest(t)

	if _, _, err := lifecycleSvc.RequestAction(
		context.Background(),
		taskModels.GuestTypeVM,
		101,
		"shutdown",
		taskModels.LifecycleTaskSourceUser,
		"tester",
	); err != nil {
		t.Fatalf("failed to seed active lifecycle task: %v", err)
	}

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/101/actions/start", nil, nil)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[vmActionTestResponse](t, rr)
	if resp.Message != "lifecycle_task_in_progress" {
		t.Fatalf("expected lifecycle_task_in_progress message, got %q", resp.Message)
	}
}

func TestVMActionHandlerStopOverrideForShutdown(t *testing.T) {
	r, lifecycleSvc, dbConn := setupVMActionHandlerTest(t)

	seedTask, _, err := lifecycleSvc.RequestAction(
		context.Background(),
		taskModels.GuestTypeVM,
		101,
		"shutdown",
		taskModels.LifecycleTaskSourceUser,
		"tester",
	)
	if err != nil {
		t.Fatalf("failed to seed shutdown task: %v", err)
	}

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/101/actions/stop", nil, nil)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[vmActionTestResponse](t, rr)
	if resp.Message != "vm_force_stop_requested" {
		t.Fatalf("expected vm_force_stop_requested message, got %q", resp.Message)
	}

	if resp.Data.TaskID != seedTask.ID || resp.Data.RID != 101 || resp.Data.Action != "stop" ||
		resp.Data.Outcome != lifecycle.RequestOutcomeForceStopOverride {
		t.Fatalf("unexpected force stop response: %+v", resp.Data)
	}

	var task taskModels.GuestLifecycleTask
	if err := dbConn.First(&task, seedTask.ID).Error; err != nil {
		t.Fatalf("failed to fetch seeded task: %v", err)
	}
	if !task.OverrideRequested {
		t.Fatalf("expected override_requested=true on seeded shutdown task")
	}

	var count int64
	if err := dbConn.Model(&taskModels.GuestLifecycleTask{}).
		Where("guest_type = ? AND guest_id = ?", taskModels.GuestTypeVM, 101).
		Count(&count).Error; err != nil {
		t.Fatalf("failed to count lifecycle tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected single lifecycle task for guest, got %d", count)
	}
}

func performStubbedVMActionRequest(
	t *testing.T,
	path string,
	preflight *vmActionPreflightStub,
	lifecycleService vmLifecycleRequestService,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/vm/:rid/actions/:action", VMActionHandler(preflight, lifecycleService))
	return testutil.PerformRequest(t, router, http.MethodPost, path, nil, nil)
}

func TestVMActionHandlerRejectsInvalidIdentityAndActionBeforeService(t *testing.T) {
	for _, path := range []string{
		"/vm/0/actions/start",
		"/vm/not-a-rid/actions/start",
		"/vm/101/actions/migrate",
	} {
		preflight := &vmActionPreflightStub{allowed: true}
		lifecycleStub := &vmLifecycleRequestStub{}
		recorder := performStubbedVMActionRequest(t, path, preflight, lifecycleStub)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400; body=%s", path, recorder.Code, recorder.Body.String())
		}
		if lifecycleStub.calls != 0 {
			t.Fatalf("%s called lifecycle service", path)
		}
	}
}

func TestVMActionHandlerPreflightStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		preflight  *vmActionPreflightStub
		wantStatus int
		wantMsg    string
	}{
		{
			name: "missing VM", preflight: &vmActionPreflightStub{allowed: true, getErr: gorm.ErrRecordNotFound},
			wantStatus: http.StatusNotFound, wantMsg: "vm_not_found",
		},
		{
			name: "ownership denied", preflight: &vmActionPreflightStub{allowed: false},
			wantStatus: http.StatusForbidden, wantMsg: "replication_lease_not_owned",
		},
		{
			name: "ownership lookup failed", preflight: &vmActionPreflightStub{guardErr: errors.New("db unavailable")},
			wantStatus: http.StatusInternalServerError, wantMsg: "replication_lease_check_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycleStub := &vmLifecycleRequestStub{}
			recorder := performStubbedVMActionRequest(
				t, "/vm/101/actions/start", test.preflight, lifecycleStub,
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := testutil.DecodeJSONResponse[vmActionTestResponse](t, recorder)
			if response.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", response.Message, test.wantMsg)
			}
			if lifecycleStub.calls != 0 {
				t.Fatal("lifecycle service called after failed preflight")
			}
		})
	}
}

func TestVMActionHandlerRequestErrorStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{name: "active task", err: lifecycle.ErrTaskInProgress, wantStatus: http.StatusConflict, wantMsg: "lifecycle_task_in_progress"},
		{name: "active migration", err: lifecycle.ErrMigrationActive, wantStatus: http.StatusConflict, wantMsg: "migration_in_progress"},
		{name: "queue failure", err: errors.New("queue unavailable"), wantStatus: http.StatusInternalServerError, wantMsg: "failed_to_enqueue_lifecycle_task"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preflight := &vmActionPreflightStub{allowed: true}
			lifecycleStub := &vmLifecycleRequestStub{err: test.err}
			recorder := performStubbedVMActionRequest(
				t, "/vm/101/actions/start", preflight, lifecycleStub,
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := testutil.DecodeJSONResponse[vmActionTestResponse](t, recorder)
			if response.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", response.Message, test.wantMsg)
			}
		})
	}
}
