// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

type migrationRequestServiceStub struct {
	result  *migrationIface.ValidateResult
	err     error
	request migrationIface.MigrateRequest
}

func (s *migrationRequestServiceStub) ValidateMigration(
	_ context.Context,
	request migrationIface.MigrateRequest,
) (*migrationIface.ValidateResult, error) {
	s.request = request
	return s.result, s.err
}

func (*migrationRequestServiceStub) ExecuteMigration(context.Context, uint) error {
	return errors.New("unexpected migration execution")
}

func (*migrationRequestServiceStub) CancelMigration(context.Context, uint) error {
	return errors.New("unexpected migration cancellation")
}

type migrationLifecycleRequestStub struct {
	task    *taskModels.GuestLifecycleTask
	outcome string
	err     error
	payload string
	calls   int
}

func (s *migrationLifecycleRequestStub) RequestActionWithPayload(
	_ context.Context,
	_ string,
	_ uint,
	_ string,
	_ string,
	_ string,
	payload string,
) (*taskModels.GuestLifecycleTask, string, error) {
	s.calls++
	s.payload = payload
	return s.task, s.outcome, s.err
}

func performMigrateVMRequest(
	t *testing.T,
	path string,
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService migrationLifecycleRequestService,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/vm/:rid/migrations", func(c *gin.Context) {
		c.Set("Username", "tester")
		MigrateVM(migrationService, lifecycleService)(c)
	})

	body := bytes.NewBufferString(`{"targetNodeUuid":"node-b"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func performMigrateJailRequest(
	t *testing.T,
	path string,
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService migrationLifecycleRequestService,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/jail/:ctid/migrations", func(c *gin.Context) {
		c.Set("Username", "tester")
		MigrateJail(migrationService, lifecycleService)(c)
	})

	body := bytes.NewBufferString(`{"targetNodeUuid":"node-b"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestMigrateVMQueuesTaskWithStableResponse(t *testing.T) {
	migrationStub := &migrationRequestServiceStub{
		result: &migrationIface.ValidateResult{Allowed: true},
	}
	lifecycleStub := &migrationLifecycleRequestStub{
		task:    &taskModels.GuestLifecycleTask{ID: 44, GuestType: taskModels.GuestTypeVM, GuestID: 101},
		outcome: lifecycle.RequestOutcomeQueued,
	}

	recorder := performMigrateVMRequest(
		t, "/vm/101/migrations", migrationStub, lifecycleStub,
	)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[MigrationTaskResponse]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Message != "vm_migration_queued" ||
		response.Data.TaskID != 44 || response.Data.GuestID != 101 ||
		response.Data.Outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("unexpected response: %+v", response)
	}
	if migrationStub.request.GuestType != taskModels.GuestTypeVM ||
		migrationStub.request.GuestID != 101 || migrationStub.request.TargetNodeUUID != "node-b" {
		t.Fatalf("unexpected validation request: %+v", migrationStub.request)
	}

	var payload MigrateGuestRequest
	if err := json.Unmarshal([]byte(lifecycleStub.payload), &payload); err != nil {
		t.Fatalf("decode queued payload: %v", err)
	}
	if payload.TargetNodeUUID != "node-b" {
		t.Fatalf("queued target = %q, want node-b", payload.TargetNodeUUID)
	}
}

func TestMigrateJailQueuesTaskWithStableResponse(t *testing.T) {
	migrationStub := &migrationRequestServiceStub{
		result: &migrationIface.ValidateResult{Allowed: true},
	}
	lifecycleStub := &migrationLifecycleRequestStub{
		task:    &taskModels.GuestLifecycleTask{ID: 45, GuestType: taskModels.GuestTypeJail, GuestID: 102},
		outcome: lifecycle.RequestOutcomeQueued,
	}

	recorder := performMigrateJailRequest(
		t, "/jail/102/migrations", migrationStub, lifecycleStub,
	)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[MigrationTaskResponse]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Message != "jail_migration_queued" ||
		response.Data.TaskID != 45 || response.Data.GuestID != 102 ||
		response.Data.Outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("unexpected response: %+v", response)
	}
	if migrationStub.request.GuestType != taskModels.GuestTypeJail ||
		migrationStub.request.GuestID != 102 || migrationStub.request.TargetNodeUUID != "node-b" {
		t.Fatalf("unexpected validation request: %+v", migrationStub.request)
	}
}

func TestMigrateVMValidationStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		wantStatus int
		wantMsg    string
	}{
		{name: "invalid target", reason: "target_is_source_node", wantStatus: http.StatusBadRequest, wantMsg: "migration_not_allowed"},
		{name: "missing VM", reason: "vm_not_found: record not found", wantStatus: http.StatusNotFound, wantMsg: "vm_not_found"},
		{name: "ownership", reason: "replication_lease_not_owned", wantStatus: http.StatusForbidden, wantMsg: "replication_lease_not_owned"},
		{name: "active task", reason: "guest_has_active_lifecycle_task: shutdown", wantStatus: http.StatusConflict, wantMsg: "migration_conflict"},
		{name: "target offline", reason: "target_node_offline", wantStatus: http.StatusServiceUnavailable, wantMsg: "migration_target_unavailable"},
		{name: "database failure", reason: "active_task_lookup_failed: database is locked", wantStatus: http.StatusInternalServerError, wantMsg: "migration_validation_failed"},
		{name: "jail network database failure", reason: "jail_network_lookup_failed: database is locked", wantStatus: http.StatusInternalServerError, wantMsg: "migration_validation_failed"},
		{name: "target bridge check failure", reason: "network_1_bridge_check_failed_bridge0: SSH unavailable", wantStatus: http.StatusServiceUnavailable, wantMsg: "migration_target_unavailable"},
		{name: "target bridge missing", reason: "target_missing_bridge: bridge0", wantStatus: http.StatusConflict, wantMsg: "migration_conflict"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			migrationStub := &migrationRequestServiceStub{
				result: &migrationIface.ValidateResult{Allowed: false, Reasons: []string{test.reason}},
			}
			lifecycleStub := &migrationLifecycleRequestStub{}
			recorder := performMigrateVMRequest(
				t, "/vm/101/migrations", migrationStub, lifecycleStub,
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var response internal.APIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", response.Message, test.wantMsg)
			}
			if lifecycleStub.calls != 0 {
				t.Fatal("lifecycle service called after rejected validation")
			}
		})
	}
}

func TestMigrateVMRequestErrorStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{name: "active task", err: lifecycle.ErrTaskInProgress, wantStatus: http.StatusConflict, wantMsg: "lifecycle_task_in_progress"},
		{name: "active migration", err: lifecycle.ErrMigrationActive, wantStatus: http.StatusConflict, wantMsg: "migration_in_progress"},
		{name: "queue failure", err: errors.New("queue unavailable"), wantStatus: http.StatusInternalServerError, wantMsg: "migration_request_failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			migrationStub := &migrationRequestServiceStub{
				result: &migrationIface.ValidateResult{Allowed: true},
			}
			lifecycleStub := &migrationLifecycleRequestStub{err: test.err}
			recorder := performMigrateVMRequest(
				t, "/vm/101/migrations", migrationStub, lifecycleStub,
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var response internal.APIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", response.Message, test.wantMsg)
			}
		})
	}
}

func TestMigrateVMRejectsZeroRIDBeforeValidation(t *testing.T) {
	migrationStub := &migrationRequestServiceStub{}
	lifecycleStub := &migrationLifecycleRequestStub{}
	recorder := performMigrateVMRequest(t, "/vm/0/migrations", migrationStub, lifecycleStub)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if migrationStub.request.GuestID != 0 || lifecycleStub.calls != 0 {
		t.Fatal("invalid RID reached a service")
	}
}

func TestMigrateJailRejectsInvalidCTIDBeforeValidation(t *testing.T) {
	for _, path := range []string{
		"/jail/0/migrations",
		"/jail/-1/migrations",
		"/jail/not-a-ctid/migrations",
	} {
		t.Run(path, func(t *testing.T) {
			migrationStub := &migrationRequestServiceStub{}
			lifecycleStub := &migrationLifecycleRequestStub{}
			recorder := performMigrateJailRequest(t, path, migrationStub, lifecycleStub)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if migrationStub.request.GuestID != 0 || lifecycleStub.calls != 0 {
				t.Fatal("invalid CTID reached a service")
			}
		})
	}
}

func TestMigrateJailRejectsMissingLifecycleTask(t *testing.T) {
	migrationStub := &migrationRequestServiceStub{
		result: &migrationIface.ValidateResult{Allowed: true},
	}
	lifecycleStub := &migrationLifecycleRequestStub{outcome: lifecycle.RequestOutcomeQueued}

	recorder := performMigrateJailRequest(
		t, "/jail/102/migrations", migrationStub, lifecycleStub,
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "migration_request_failed" {
		t.Fatalf("message = %q, want migration_request_failed", response.Message)
	}
}
