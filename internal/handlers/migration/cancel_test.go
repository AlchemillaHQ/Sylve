// SPDX-License-Identifier: BSD-2-Clause

package migrationHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	migrationService "github.com/alchemillahq/sylve/internal/services/migration"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type cancelMigrationServiceStub struct {
	cancelledTaskID uint
	cancelErr       error
}

func (*cancelMigrationServiceStub) ValidateMigration(
	context.Context,
	migrationIface.MigrateRequest,
) (*migrationIface.ValidateResult, error) {
	return nil, errors.New("unexpected validation call")
}

func (*cancelMigrationServiceStub) ExecuteMigration(context.Context, uint) error {
	return errors.New("unexpected execution call")
}

func (s *cancelMigrationServiceStub) CancelMigration(_ context.Context, taskID uint) error {
	s.cancelledTaskID = taskID
	return s.cancelErr
}

func performCancelMigrationRequest(
	t *testing.T,
	service migrationIface.MigrationServiceInterface,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/tasks/migration/cancel/:taskId", CancelMigration(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/tasks/migration/cancel/41", nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestCancelMigrationHandlerAcknowledgesRequestWithoutClaimingCompletion(t *testing.T) {
	service := &cancelMigrationServiceStub{}
	recorder := performCancelMigrationRequest(t, service)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if service.cancelledTaskID != 41 {
		t.Fatalf("cancelled task ID = %d, want 41", service.cancelledTaskID)
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Message != "migration_cancellation_requested" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCancelMigrationHandlerRejectsPostCutoverRequest(t *testing.T) {
	service := &cancelMigrationServiceStub{cancelErr: migrationService.ErrCancelNotAllowed}
	recorder := performCancelMigrationRequest(t, service)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "error" || response.Message != "cancel_not_allowed_in_current_phase" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCancelMigrationHandlerReturnsNotFoundForMissingTask(t *testing.T) {
	service := &cancelMigrationServiceStub{cancelErr: gorm.ErrRecordNotFound}
	recorder := performCancelMigrationRequest(t, service)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "migration_task_not_found" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCancelMigrationHandlerRejectsNonMigrationTask(t *testing.T) {
	service := &cancelMigrationServiceStub{cancelErr: migrationService.ErrNotMigrationTask}
	recorder := performCancelMigrationRequest(t, service)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "not_a_migration_task" {
		t.Fatalf("response = %+v", response)
	}
}
