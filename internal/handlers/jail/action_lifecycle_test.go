// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

type jailActionTestResponse struct {
	Status  string             `json:"status"`
	Message string             `json:"message"`
	Data    JailActionResponse `json:"data"`
	Error   string             `json:"error"`
}

type stubJailActionPreflightService struct {
	jail          *jailModels.Jail
	jailErr       error
	allowed       bool
	err           error
	checkedCTID   uint
	checkedAction string
}

func (s *stubJailActionPreflightService) GetJailByCTID(_ uint) (*jailModels.Jail, error) {
	return s.jail, s.jailErr
}

func (s *stubJailActionPreflightService) CanPerformJailAction(ctID uint, action string) (bool, error) {
	s.checkedCTID = ctID
	s.checkedAction = action
	return s.allowed, s.err
}

type stubJailLifecycleRequestService struct {
	task    *taskModels.GuestLifecycleTask
	outcome string
	err     error
	calls   int
	ctID    uint
	action  string
}

func (s *stubJailLifecycleRequestService) RequestAction(
	_ context.Context,
	_ string,
	ctID uint,
	action string,
	_ string,
	_ string,
) (*taskModels.GuestLifecycleTask, string, error) {
	s.calls++
	s.ctID = ctID
	s.action = action
	return s.task, s.outcome, s.err
}

func setupStubJailActionRouter(
	preflightService *stubJailActionPreflightService,
	lifecycleService *stubJailLifecycleRequestService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/jail/:ctid/actions/:action", func(c *gin.Context) {
		c.Set("Username", "tester")
		JailAction(preflightService, lifecycleService)(c)
	})
	return r
}

func setupJailActionHandlerTest(
	t *testing.T,
	allowed bool,
	mutationErr error,
) (*gin.Engine, *lifecycle.Service, *gorm.DB, *stubJailActionPreflightService) {
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
	preflightService := &stubJailActionPreflightService{
		jail:    &jailModels.Jail{CTID: 42},
		allowed: allowed,
		err:     mutationErr,
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/jail/:ctid/actions/:action", func(c *gin.Context) {
		c.Set("Username", "tester")
		JailAction(preflightService, lifecycleSvc)(c)
	})

	return r, lifecycleSvc, dbConn, preflightService
}

func TestJailActionQueuedAccepted(t *testing.T) {
	r, _, _, preflightService := setupJailActionHandlerTest(t, true, nil)

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[jailActionTestResponse](t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success status, got %q", resp.Status)
	}
	if resp.Message != "jail_start_queued" {
		t.Fatalf("expected jail_start_queued message, got %q", resp.Message)
	}

	if resp.Data.TaskID == 0 || resp.Data.CTID != 42 || resp.Data.Action != "start" ||
		resp.Data.Outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("unexpected action response: %+v", resp.Data)
	}
	if preflightService.checkedCTID != 42 || preflightService.checkedAction != "start" {
		t.Fatalf("unexpected action preflight: ctid=%d action=%q", preflightService.checkedCTID, preflightService.checkedAction)
	}
}

func TestJailActionConflictWhenTaskActive(t *testing.T) {
	r, lifecycleSvc, _, _ := setupJailActionHandlerTest(t, true, nil)

	if _, _, err := lifecycleSvc.RequestAction(
		context.Background(),
		taskModels.GuestTypeJail,
		42,
		"stop",
		taskModels.LifecycleTaskSourceUser,
		"tester",
	); err != nil {
		t.Fatalf("failed to seed active lifecycle task: %v", err)
	}

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[jailActionTestResponse](t, rr)
	if resp.Message != "lifecycle_task_in_progress" {
		t.Fatalf("expected lifecycle_task_in_progress message, got %q", resp.Message)
	}
}

func TestJailActionSupportsAllLifecycleActions(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			preflightService := &stubJailActionPreflightService{
				jail:    &jailModels.Jail{CTID: 42},
				allowed: true,
			}
			lifecycleService := &stubJailLifecycleRequestService{
				task:    &taskModels.GuestLifecycleTask{ID: 7, GuestID: 42, Action: action},
				outcome: lifecycle.RequestOutcomeQueued,
			}
			r := setupStubJailActionRouter(preflightService, lifecycleService)

			rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/"+action, nil, nil)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
			}

			resp := testutil.DecodeJSONResponse[jailActionTestResponse](t, rr)
			if resp.Data.Action != action || lifecycleService.action != action || lifecycleService.ctID != 42 {
				t.Fatalf("unexpected lifecycle action response=%+v service_action=%q service_ctid=%d", resp.Data, lifecycleService.action, lifecycleService.ctID)
			}
		})
	}
}

func TestJailActionRejectsUnownedProtectedJail(t *testing.T) {
	preflightService := &stubJailActionPreflightService{
		jail:    &jailModels.Jail{CTID: 42},
		allowed: false,
	}
	lifecycleService := &stubJailLifecycleRequestService{}
	r := setupStubJailActionRouter(preflightService, lifecycleService)

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
	if lifecycleService.calls != 0 {
		t.Fatal("unowned jail action reached the lifecycle service")
	}
}

func TestJailActionMapsActiveMigrationToConflict(t *testing.T) {
	preflightService := &stubJailActionPreflightService{
		jail:    &jailModels.Jail{CTID: 42},
		allowed: true,
	}
	lifecycleService := &stubJailLifecycleRequestService{err: lifecycle.ErrMigrationActive}
	r := setupStubJailActionRouter(preflightService, lifecycleService)

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
	resp := testutil.DecodeJSONResponse[jailActionTestResponse](t, rr)
	if resp.Message != "migration_in_progress" {
		t.Fatalf("expected migration_in_progress message, got %q", resp.Message)
	}
}

func TestJailActionRejectsInvalidCTIDBeforePreflight(t *testing.T) {
	for _, path := range []string{
		"/jail/0/actions/start",
		"/jail/-1/actions/start",
		"/jail/not-a-ctid/actions/start",
	} {
		t.Run(path, func(t *testing.T) {
			r, _, _, preflightService := setupJailActionHandlerTest(t, true, nil)
			rr := testutil.PerformRequest(t, r, http.MethodPost, path, nil, nil)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
			}
			if preflightService.checkedCTID != 0 || preflightService.checkedAction != "" {
				t.Fatal("invalid CTID reached the action preflight")
			}
		})
	}
}

func TestJailActionReturnsNotFoundBeforeQueueing(t *testing.T) {
	r, _, _, preflightService := setupJailActionHandlerTest(t, true, nil)
	preflightService.jail = nil
	preflightService.jailErr = gorm.ErrRecordNotFound

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rr.Code, rr.Body.String())
	}
}

func TestJailActionRejectsMissingLifecycleTask(t *testing.T) {
	preflightService := &stubJailActionPreflightService{
		jail:    &jailModels.Jail{CTID: 42},
		allowed: true,
	}
	lifecycleService := &stubJailLifecycleRequestService{outcome: lifecycle.RequestOutcomeQueued}
	r := setupStubJailActionRouter(preflightService, lifecycleService)

	rr := testutil.PerformRequest(t, r, http.MethodPost, "/jail/42/actions/start", nil, nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusInternalServerError, rr.Code, rr.Body.String())
	}
}
