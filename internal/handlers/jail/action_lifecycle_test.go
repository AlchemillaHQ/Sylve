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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type jailActionTestResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
	Error   string         `json:"error"`
}

type stubProtectedJailMutationChecker struct {
	allowed bool
	err     error
}

func (s stubProtectedJailMutationChecker) CanMutateProtectedJail(_ uint) (bool, error) {
	return s.allowed, s.err
}

func setupJailActionHandlerTest(
	t *testing.T,
	allowed bool,
	mutationErr error,
) (*gin.Engine, *lifecycle.Service, *gorm.DB) {
	t.Helper()

	dbConn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if err := dbConn.AutoMigrate(&taskModels.GuestLifecycleTask{}); err != nil {
		t.Fatalf("failed to migrate task table: %v", err)
	}

	cfg := &internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}
	if err := db.SetupQueue(cfg, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("failed to setup test queue: %v", err)
	}

	lifecycleSvc := lifecycle.NewService(dbConn, nil, nil)
	mutationChecker := stubProtectedJailMutationChecker{
		allowed: allowed,
		err:     mutationErr,
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/jail/action/:action/:ctId", func(c *gin.Context) {
		c.Set("Username", "tester")
		JailAction(mutationChecker, lifecycleSvc)(c)
	})

	return r, lifecycleSvc, dbConn
}

func decodeJailActionResponse(t *testing.T, rr *httptest.ResponseRecorder) jailActionTestResponse {
	t.Helper()

	var resp jailActionTestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestJailActionQueuedAccepted(t *testing.T) {
	r, _, _ := setupJailActionHandlerTest(t, true, nil)

	req := httptest.NewRequest(http.MethodPost, "/jail/action/start/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := decodeJailActionResponse(t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success status, got %q", resp.Status)
	}
	if resp.Message != "jail_start_queued" {
		t.Fatalf("expected jail_start_queued message, got %q", resp.Message)
	}

	outcome, ok := resp.Data["outcome"].(string)
	if !ok || outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("expected queued outcome, got %#v", resp.Data["outcome"])
	}
}

func TestJailActionConflictWhenTaskActive(t *testing.T) {
	r, lifecycleSvc, _ := setupJailActionHandlerTest(t, true, nil)

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

	req := httptest.NewRequest(http.MethodPost, "/jail/action/start/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}

	resp := decodeJailActionResponse(t, rr)
	if resp.Message != "lifecycle_task_in_progress" {
		t.Fatalf("expected lifecycle_task_in_progress message, got %q", resp.Message)
	}
}
