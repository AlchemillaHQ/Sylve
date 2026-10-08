// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package taskHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newLifecycleHandlerTestService(t *testing.T) *lifecycle.Service {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	return lifecycle.NewService(database, nil, nil, nil)
}

func performLifecycleRequest(t *testing.T, route, target string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(route, handler)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func lifecycleResponseMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response.Message
}

func TestLifecycleHandlersRejectInvalidFilters(t *testing.T) {
	tests := []struct {
		name, route, target, message string
		handler                      gin.HandlerFunc
	}{
		{"unsupported collection guest type", "/tasks/lifecycle/active", "/tasks/lifecycle/active?guestType=container", "invalid_guest_type", ActiveLifecycleTasks(nil)},
		{"empty collection guest type", "/tasks/lifecycle/active", "/tasks/lifecycle/active?guestType=", "invalid_guest_type", ActiveLifecycleTasks(nil)},
		{"zero collection guest ID", "/tasks/lifecycle/active", "/tasks/lifecycle/active?guestId=0", "invalid_guest_id", ActiveLifecycleTasks(nil)},
		{"malformed collection guest ID", "/tasks/lifecycle/active", "/tasks/lifecycle/active?guestId=abc", "invalid_guest_id", ActiveLifecycleTasks(nil)},
		{"zero recent limit", "/tasks/lifecycle/recent", "/tasks/lifecycle/recent?limit=0", "invalid_limit", RecentLifecycleTasks(nil)},
		{"negative recent limit", "/tasks/lifecycle/recent", "/tasks/lifecycle/recent?limit=-1", "invalid_limit", RecentLifecycleTasks(nil)},
		{"oversized recent limit", "/tasks/lifecycle/recent", "/tasks/lifecycle/recent?limit=201", "invalid_limit", RecentLifecycleTasks(nil)},
		{"malformed recent limit", "/tasks/lifecycle/recent", "/tasks/lifecycle/recent?limit=many", "invalid_limit", RecentLifecycleTasks(nil)},
		{"unsupported member guest type", "/tasks/lifecycle/active/:guestType/:guestId", "/tasks/lifecycle/active/container/1", "invalid_guest_type", ActiveLifecycleTaskForGuest(nil)},
		{"zero member guest ID", "/tasks/lifecycle/active/:guestType/:guestId", "/tasks/lifecycle/active/vm/0", "invalid_guest_id", ActiveLifecycleTaskForGuest(nil)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performLifecycleRequest(t, test.route, test.target, test.handler)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if message := lifecycleResponseMessage(t, recorder); message != test.message {
				t.Fatalf("message = %q, want %q", message, test.message)
			}
		})
	}
}

func TestActiveLifecycleTaskForGuestReturnsNullableSuccess(t *testing.T) {
	service := newLifecycleHandlerTestService(t)
	recorder := performLifecycleRequest(
		t,
		"/tasks/lifecycle/active/:guestType/:guestId",
		"/tasks/lifecycle/active/vm/41",
		ActiveLifecycleTaskForGuest(service),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[*taskModels.GuestLifecycleTask]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Data != nil {
		t.Fatalf("response = %+v, want successful null data", response)
	}
}

func TestLifecycleHandlerDoesNotExposeStorageErrors(t *testing.T) {
	service := newLifecycleHandlerTestService(t)
	sqlDB, err := service.DB.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close SQL database: %v", err)
	}

	recorder := performLifecycleRequest(
		t,
		"/tasks/lifecycle/active",
		"/tasks/lifecycle/active?guestType=vm&guestId=41",
		ActiveLifecycleTasks(service),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "database is closed") {
		t.Fatalf("response exposed storage error: %s", recorder.Body.String())
	}
	if message := lifecycleResponseMessage(t, recorder); message != "failed_to_list_active_lifecycle_tasks" {
		t.Fatalf("message = %q", message)
	}
}

func TestLifecycleTaskObservationKeepsExactTerminalAttempt(t *testing.T) {
	service := newLifecycleHandlerTestService(t)
	first := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: 42, Action: "create", Status: "failed", Error: "first attempt"}
	second := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: 42, Action: "create", Status: "queued"}
	for _, task := range []*taskModels.GuestLifecycleTask{&first, &second} {
		if err := service.DB.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	recorder := performLifecycleRequest(t, "/tasks/lifecycle/:taskId", fmt.Sprintf("/tasks/lifecycle/%d", first.ID), LifecycleTask(service))
	var response internal.APIResponse[*taskModels.GuestLifecycleTask]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || response.Data == nil || response.Data.ID != first.ID || response.Data.Status != "failed" {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	for _, tc := range []struct {
		target string
		status int
	}{{"0", 400}, {"invalid", 400}, {"9999", 404}} {
		recorder := performLifecycleRequest(t, "/tasks/lifecycle/:taskId", "/tasks/lifecycle/"+tc.target, LifecycleTask(service))
		if recorder.Code != tc.status {
			t.Fatalf("%s: status=%d body=%s", tc.target, recorder.Code, recorder.Body.String())
		}
	}
}
