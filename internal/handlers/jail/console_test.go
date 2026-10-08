// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type jailConsoleHandlerStub struct {
	exists      bool
	existsErr   error
	restoring   bool
	restoreErr  error
	allowed     bool
	guardErr    error
	running     bool
	stateErr    error
	activeTask  *taskModels.GuestLifecycleTask
	taskErr     error
	existsCTID  uint
	restoreCTID uint
	guardCTID   uint
	runtimeCTID uint
	taskCTID    uint
}

func (s *jailConsoleHandlerStub) JailExistsByCTID(ctID uint) (bool, error) {
	s.existsCTID = ctID
	return s.exists, s.existsErr
}

func (s *jailConsoleHandlerStub) JailRestoreInProgress(ctID uint) (bool, error) {
	s.restoreCTID = ctID
	return s.restoring, s.restoreErr
}

func (s *jailConsoleHandlerStub) CanMutateProtectedJail(ctID uint) (bool, error) {
	s.guardCTID = ctID
	return s.allowed, s.guardErr
}

func (s *jailConsoleHandlerStub) IsJailRunning(ctID uint) (bool, error) {
	s.runtimeCTID = ctID
	return s.running, s.stateErr
}

func (s *jailConsoleHandlerStub) GetActiveTaskForGuest(guestType string, ctID uint) (*taskModels.GuestLifecycleTask, error) {
	if guestType != "jail" {
		return nil, errors.New("unexpected guest type")
	}
	s.taskCTID = ctID
	return s.activeTask, s.taskErr
}

func performJailConsoleRequest(
	t *testing.T,
	path string,
	service *jailConsoleHandlerStub,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/jail/:ctid/console", HandleJailTerminalWebsocket(service, service))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestJailConsoleReturnsStandardPreUpgradeErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		configure  func(*jailConsoleHandlerStub)
		wantStatus int
		wantCode   string
	}{
		{name: "invalid CTID", path: "/jail/0/console", wantStatus: http.StatusBadRequest, wantCode: "invalid_ctid"},
		{name: "missing jail", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.exists = false }, wantStatus: http.StatusNotFound, wantCode: "jail_not_found"},
		{name: "jail lookup failure", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.existsErr = errors.New("database unavailable") }, wantStatus: http.StatusInternalServerError, wantCode: "failed_to_get_jail"},
		{name: "restore guard failure", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.restoreErr = errors.New("replication database unavailable") }, wantStatus: http.StatusServiceUnavailable, wantCode: "jail_console_guard_unavailable"},
		{name: "restore in progress", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.restoring = true }, wantStatus: http.StatusConflict, wantCode: "restore_in_progress"},
		{name: "ownership guard failure", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.guardErr = errors.New("replication database unavailable") }, wantStatus: http.StatusServiceUnavailable, wantCode: "jail_console_guard_unavailable"},
		{name: "replication lease not owned", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.allowed = false }, wantStatus: http.StatusForbidden, wantCode: "replication_lease_not_owned"},
		{name: "runtime state unavailable", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.stateErr = errors.New("jls unavailable") }, wantStatus: http.StatusServiceUnavailable, wantCode: "jail_state_unavailable"},
		{name: "jail inactive", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.running = false }, wantStatus: http.StatusConflict, wantCode: "jail_console_requires_active_jail"},
		{name: "lifecycle guard failure", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.taskErr = errors.New("task database unavailable") }, wantStatus: http.StatusServiceUnavailable, wantCode: "jail_console_guard_unavailable"},
		{name: "jail starting", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.activeTask = &taskModels.GuestLifecycleTask{Action: "start"} }, wantStatus: http.StatusConflict, wantCode: "lifecycle_task_in_progress"},
		{name: "jail stopping", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.activeTask = &taskModels.GuestLifecycleTask{Action: "stop"} }, wantStatus: http.StatusConflict, wantCode: "lifecycle_task_in_progress"},
		{name: "jail restarting", path: "/jail/104/console", configure: func(s *jailConsoleHandlerStub) { s.activeTask = &taskModels.GuestLifecycleTask{Action: "restart"} }, wantStatus: http.StatusConflict, wantCode: "lifecycle_task_in_progress"},
		{name: "upgrade required", path: "/jail/104/console", wantStatus: http.StatusBadRequest, wantCode: "websocket_upgrade_required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &jailConsoleHandlerStub{
				exists:  true,
				allowed: true,
				running: true,
			}
			if test.configure != nil {
				test.configure(service)
			}

			recorder := performJailConsoleRequest(t, test.path, service)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, recorder)
			if response.Message != test.wantCode || response.Data != nil {
				t.Fatalf("response = %+v, want code %q and null data", response, test.wantCode)
			}
			if (service.activeTask != nil || service.taskErr != nil) && service.runtimeCTID != 0 {
				t.Fatal("console checked the runtime while its lifecycle guard blocked attachment")
			}
		})
	}
}

func TestJailConsolePreflightUsesCanonicalCTID(t *testing.T) {
	service := &jailConsoleHandlerStub{
		exists:  true,
		allowed: true,
		running: true,
	}
	recorder := performJailConsoleRequest(t, "/jail/000104/console", service)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if service.existsCTID != 104 || service.restoreCTID != 104 || service.guardCTID != 104 || service.runtimeCTID != 104 || service.taskCTID != 104 {
		t.Fatalf(
			"service CTIDs = exists:%d restore:%d guard:%d runtime:%d lifecycle:%d",
			service.existsCTID,
			service.restoreCTID,
			service.guardCTID,
			service.runtimeCTID,
			service.taskCTID,
		)
	}
}

func TestJailTerminalSessionLastObserverCleanupIsRaceSafe(t *testing.T) {
	manager := &SessionManager{sessions: make(map[string]*TerminalSession)}
	observer := &Observer{}
	session := &TerminalSession{
		ID:        "jail-104",
		Observers: map[*Observer]struct{}{observer: {}},
	}
	manager.sessions[session.ID] = session

	session.RemoveObserver(observer, manager)

	if !session.IsClosed() {
		t.Fatal("session remained open after its last observer disconnected")
	}
	if _, exists := manager.sessions[session.ID]; exists {
		t.Fatal("closed session remained registered")
	}
	if err := session.AddObserverAndReplay(&Observer{}); err == nil {
		t.Fatal("an observer joined after last-observer cleanup began")
	}
}

func TestJailTerminalSessionHistoryIsBounded(t *testing.T) {
	session := &TerminalSession{
		ID:           "jail-104",
		Observers:    make(map[*Observer]struct{}),
		HistoryLimit: 4,
	}
	session.BroadcastBinary([]byte("abcdef"), &SessionManager{
		sessions: make(map[string]*TerminalSession),
	})

	if string(session.History) != "cdef" {
		t.Fatalf("history = %q, want cdef", session.History)
	}
}
