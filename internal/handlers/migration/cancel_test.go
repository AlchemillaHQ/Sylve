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
	"github.com/gin-gonic/gin"
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
	service := &cancelMigrationServiceStub{cancelErr: errors.New("cancel_not_allowed_in_current_phase")}
	recorder := performCancelMigrationRequest(t, service)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "error" || response.Message != "cancel_not_allowed_in_current_phase" {
		t.Fatalf("response = %+v", response)
	}
}
