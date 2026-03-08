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

type vmActionTestResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
	Error   string         `json:"error"`
}

func setupVMActionHandlerTest(t *testing.T) (*gin.Engine, *lifecycle.Service, *gorm.DB) {
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

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/vm/:action/:rid", func(c *gin.Context) {
		c.Set("Username", "tester")
		VMActionHandler(lifecycleSvc)(c)
	})

	return r, lifecycleSvc, dbConn
}

func decodeVMActionResponse(t *testing.T, rr *httptest.ResponseRecorder) vmActionTestResponse {
	t.Helper()

	var resp vmActionTestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestVMActionHandlerQueuedAccepted(t *testing.T) {
	r, _, _ := setupVMActionHandlerTest(t)

	req := httptest.NewRequest(http.MethodPost, "/vm/start/101", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := decodeVMActionResponse(t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success status, got %q", resp.Status)
	}
	if resp.Message != "vm_action_queued" {
		t.Fatalf("expected vm_action_queued message, got %q", resp.Message)
	}

	outcome, ok := resp.Data["outcome"].(string)
	if !ok || outcome != lifecycle.RequestOutcomeQueued {
		t.Fatalf("expected queued outcome, got %#v", resp.Data["outcome"])
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

	req := httptest.NewRequest(http.MethodPost, "/vm/start/101", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}

	resp := decodeVMActionResponse(t, rr)
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

	req := httptest.NewRequest(http.MethodPost, "/vm/stop/101", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rr.Code, rr.Body.String())
	}

	resp := decodeVMActionResponse(t, rr)
	if resp.Message != "vm_force_stop_requested" {
		t.Fatalf("expected vm_force_stop_requested message, got %q", resp.Message)
	}

	outcome, ok := resp.Data["outcome"].(string)
	if !ok || outcome != lifecycle.RequestOutcomeForceStopOverride {
		t.Fatalf("expected force stop outcome, got %#v", resp.Data["outcome"])
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
