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
	"os"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type jailReadHandlerStub struct {
	exists         bool
	existsErr      error
	state          jailServiceInterfaces.State
	stateErr       error
	activeTask     *taskModels.GuestLifecycleTask
	activeTaskErr  error
	logs           string
	logsErr        error
	existsCTID     uint
	stateCTID      uint
	activeTaskCTID uint
	logsCTID       uint
}

func (s *jailReadHandlerStub) JailExistsByCTID(ctID uint) (bool, error) {
	s.existsCTID = ctID
	return s.exists, s.existsErr
}

func (s *jailReadHandlerStub) GetStateByCtId(ctID uint) (jailServiceInterfaces.State, error) {
	s.stateCTID = ctID
	return s.state, s.stateErr
}

func (s *jailReadHandlerStub) GetActiveTaskForGuest(_ string, guestID uint) (*taskModels.GuestLifecycleTask, error) {
	s.activeTaskCTID = guestID
	return s.activeTask, s.activeTaskErr
}

func (s *jailReadHandlerStub) GetJailLogs(ctID uint) (string, error) {
	s.logsCTID = ctID
	return s.logs, s.logsErr
}

func newJailReadHandlerRouter(service *jailReadHandlerStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/jail/:ctid/state", GetJailState(service, service))
	router.GET("/jail/:ctid/logs", GetJailLogs(service))
	return router
}

func TestGetJailStateReturnsLifecycleEnrichment(t *testing.T) {
	service := &jailReadHandlerStub{
		exists: true,
		state: jailServiceInterfaces.State{
			CTID:  104,
			State: "ACTIVE",
		},
		activeTask: &taskModels.GuestLifecycleTask{
			Action:            "restart",
			OverrideRequested: true,
		},
	}
	router := newJailReadHandlerRouter(service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/104/state", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	response := testutil.DecodeJSONResponse[internal.APIResponse[jailServiceInterfaces.State]](t, recorder)
	if response.Data.CTID != 104 || response.Data.PendingAction != "restart" || !response.Data.OverrideRequested {
		t.Fatalf("state response = %+v", response.Data)
	}
	if service.existsCTID != 104 || service.stateCTID != 104 || service.activeTaskCTID != 104 {
		t.Fatalf("service CTIDs = exists:%d state:%d task:%d", service.existsCTID, service.stateCTID, service.activeTaskCTID)
	}
}

func TestGetJailStateClassifiesReadFailures(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		configure  func(*jailReadHandlerStub)
		wantStatus int
		wantCode   string
	}{
		{name: "invalid CTID", path: "/jail/-1/state", wantStatus: http.StatusBadRequest, wantCode: "invalid_ctid"},
		{name: "missing jail", path: "/jail/999/state", wantStatus: http.StatusNotFound, wantCode: "jail_not_found"},
		{
			name: "existence lookup failure", path: "/jail/104/state",
			configure:  func(service *jailReadHandlerStub) { service.existsErr = errors.New("database unavailable") },
			wantStatus: http.StatusInternalServerError, wantCode: "failed_to_get_jail",
		},
		{
			name: "runtime state unavailable", path: "/jail/104/state",
			configure:  func(service *jailReadHandlerStub) { service.stateErr = errors.New("jls unavailable") },
			wantStatus: http.StatusServiceUnavailable, wantCode: "jail_state_unavailable",
		},
		{
			name: "lifecycle state unavailable", path: "/jail/104/state",
			configure:  func(service *jailReadHandlerStub) { service.activeTaskErr = errors.New("task database unavailable") },
			wantStatus: http.StatusInternalServerError, wantCode: "failed_to_get_jail_lifecycle_state",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &jailReadHandlerStub{
				exists: true,
				state:  jailServiceInterfaces.State{CTID: 104, State: "ACTIVE"},
			}
			if test.name == "missing jail" {
				service.exists = false
			}
			if test.configure != nil {
				test.configure(service)
			}

			recorder := httptest.NewRecorder()
			newJailReadHandlerRouter(service).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, recorder)
			if response.Message != test.wantCode || response.Data != nil {
				t.Fatalf("response = %+v, want code %q and null data", response, test.wantCode)
			}
		})
	}
}

func TestGetJailLogsReturnsObjectAndClassifiesMissingJail(t *testing.T) {
	service := &jailReadHandlerStub{logs: "line one\nline two\n"}
	recorder := httptest.NewRecorder()
	newJailReadHandlerRouter(service).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/jail/104/logs", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	response := testutil.DecodeJSONResponse[internal.APIResponse[JailLogsResponse]](t, recorder)
	if response.Data.Logs != service.logs || service.logsCTID != 104 {
		t.Fatalf("response = %+v, logs CTID = %d", response.Data, service.logsCTID)
	}

	service.logsErr = gorm.ErrRecordNotFound
	recorder = httptest.NewRecorder()
	newJailReadHandlerRouter(service).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/jail/104/logs", nil),
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	missing := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, recorder)
	if missing.Message != "jail_not_found" || missing.Data != nil {
		t.Fatalf("missing response = %+v", missing)
	}
}

func TestJailReadRoutesAndSwaggerCommentsUseNestedCTID(t *testing.T) {
	statsSource, err := os.ReadFile("stats.go")
	if err != nil {
		t.Fatal(err)
	}
	consoleSource, err := os.ReadFile("console.go")
	if err != nil {
		t.Fatal(err)
	}
	routesSource, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"@Router /jail/{ctid}/state [get]",
		"@Router /jail/{ctid}/logs [get]",
		"@Router /jail/{ctid}/stats [get]",
		"@Router /jail/{ctid}/stats/{step} [get]",
	} {
		if !strings.Contains(string(statsSource), expected) {
			t.Fatalf("missing source Swagger route %q", expected)
		}
	}
	if !strings.Contains(string(consoleSource), "@Router /jail/{ctid}/console [get]") {
		t.Fatal("missing nested console source Swagger route")
	}

	routes := string(routesSource)
	for _, expected := range []string{
		`jail.GET("/:ctid/state"`,
		`jail.GET("/:ctid/logs"`,
		`jail.GET("/:ctid/stats"`,
		`jail.GET("/:ctid/stats/:step"`,
		`jail.GET("/:ctid/console"`,
	} {
		if !strings.Contains(routes, expected) {
			t.Fatalf("missing route registration %q", expected)
		}
	}
	for _, retired := range []string{
		`jail.GET("/state"`,
		`jail.GET("/state/:id"`,
		`jail.GET("/stats/:ctId"`,
		`jail.GET("/console"`,
	} {
		if strings.Contains(routes, retired) {
			t.Fatalf("retired route remains registered: %q", retired)
		}
	}

	consoleRoute := strings.Index(routes, `jail.GET("/:ctid/console"`)
	if consoleRoute < 0 {
		t.Fatal("nested console route is not registered")
	}
	consoleRegistration := routes[consoleRoute:]
	adminMiddleware := strings.Index(consoleRegistration, "middleware.RequireLocalAdmin(authService)")
	consoleHandler := strings.Index(consoleRegistration, "jailHandlers.HandleJailTerminalWebsocket(jailService)")
	if adminMiddleware < 0 || consoleHandler < 0 || adminMiddleware > consoleHandler {
		t.Fatal("jail console route does not apply administrator authorization before the handler")
	}
}
