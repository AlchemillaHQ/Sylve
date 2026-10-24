// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/hostupdates"
	"github.com/gin-gonic/gin"
)

type updateStub struct {
	request      hostupdates.Request
	action       string
	actor        string
	monitoring   hostupdates.Monitoring
	channel      string
	refresh      bool
	releaseCalls int
	logCalls     int
}

func (*updateStub) Get(context.Context) (hostupdates.View, error) { return hostupdates.View{}, nil }
func (s *updateStub) GetRelease(_ context.Context, refresh bool) (hostupdates.ReleaseView, error) {
	s.refresh = refresh
	s.releaseCalls++
	return hostupdates.ReleaseView{ReleaseChannel: "rolling"}, nil
}
func (s *updateStub) Start(_ context.Context, action string, request hostupdates.Request, actor string) (hostupdates.Job, error) {
	s.request, s.action, s.actor = request, action, actor
	return hostupdates.Job{HostUpdateOperation: models.HostUpdateOperation{ID: 17, Status: "queued"}}, nil
}
func (*updateStub) Job(context.Context, uint) (hostupdates.Job, error) { return hostupdates.Job{}, nil }
func (s *updateStub) Logs(context.Context, uint, int64) (string, int64, error) {
	s.logCalls++
	return "output", 6, nil
}
func (s *updateStub) SetMonitoring(_ context.Context, input hostupdates.Monitoring) (models.HostUpdateState, error) {
	s.monitoring = input
	return models.HostUpdateState{}, nil
}

func (s *updateStub) SetReleaseChannel(_ context.Context, channel string) (models.HostUpdateState, error) {
	s.channel = channel
	return models.HostUpdateState{ReleaseChannel: channel}, nil
}

func TestHostUpdateMonitoringPreservesCustomCron(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.PUT("/monitoring", SetHostUpdateMonitoring(stub))
	response := httptest.NewRecorder()
	request := httptest.NewRequest("PUT", "/monitoring", strings.NewReader(`{"enabled":true,"cronExpr":"0 3 * * 0"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !stub.monitoring.Enabled || stub.monitoring.CronExpr != "0 3 * * 0" {
		t.Fatalf("custom schedule changed at the API boundary: %+v %d %s", stub.monitoring, response.Code, response.Body)
	}
}

func TestSylveReleaseChannelHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.PUT("/sylve/channel", SetSylveReleaseChannel(stub))
	request := httptest.NewRequest(http.MethodPut, "/sylve/channel", strings.NewReader(`{"channel":"tagged"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || stub.channel != "tagged" || !strings.Contains(response.Body.String(), `"releaseChannel":"tagged"`) {
		t.Fatalf("channel was not saved: %d %s", response.Code, response.Body)
	}
	stub.channel = ""
	request = httptest.NewRequest(http.MethodPut, "/sylve/channel", strings.NewReader(`{"channel":true}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || stub.channel != "" {
		t.Fatalf("malformed channel reached service: %d %s", response.Code, response.Body)
	}
}

func TestSylveReleaseReadUsesGETWithoutAsyncAuditIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		if _, ok := c.Get("AuditAsyncJobID"); ok || c.GetString("AuditAsyncJobType") != "" {
			t.Error("release read was bound to an audited operation")
		}
	})
	router.GET("/sylve/release", GetSylveRelease(stub))
	router.GET("/unavailable", GetSylveRelease(nil))
	for _, test := range []struct {
		method, path string
		status       int
		refresh      bool
	}{
		{http.MethodGet, "/sylve/release", http.StatusOK, false},
		{http.MethodGet, "/sylve/release?refresh=true", http.StatusOK, true},
		{http.MethodGet, "/sylve/release?refresh=false", http.StatusOK, false},
		{http.MethodGet, "/sylve/release?refresh=invalid", http.StatusBadRequest, false},
		{http.MethodGet, "/unavailable", http.StatusServiceUnavailable, false},
		{http.MethodPost, "/sylve/release", http.StatusNotFound, false},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, response.Code, response.Body)
		}
		if test.status == http.StatusOK && (stub.refresh != test.refresh || !strings.Contains(response.Body.String(), `"releaseChannel":"rolling"`) || response.Header().Get("Cache-Control") != "private, no-store") {
			t.Fatalf("incorrect release read: %+v %s", stub, response.Body)
		}
	}
	if stub.releaseCalls != 3 || stub.action != "" {
		t.Fatalf("invalid read reached the service or started an operation: %+v", stub)
	}
}

func TestHostUpdateUnavailableAndInputValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.GET("/unavailable", GetHostUpdates(nil))
	router.POST("/actions/:action", StartHostUpdate(stub))
	router.GET("/jobs/:id/logs", GetHostUpdateJob(stub, true))
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/unavailable", "", 503},
		{"POST", "/actions/check", "{", 400},
		{"GET", "/jobs/1/logs?offset=-1", "", 400},
		{"GET", "/jobs/1/logs?offset=NaN", "", 400},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s: %d %s", test.path, response.Code, response.Body)
		}
	}
	if stub.logCalls != 0 {
		t.Fatal("invalid offset reached log service")
	}
}

func TestHostUpdateStartPreservesApprovalAndAsyncAuditIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("Username", "fixture-admin")
		c.Next()
		if id, _ := c.Get("AuditAsyncJobID"); id != uint(17) {
			t.Errorf("audit job ID: %v", id)
		}
		if c.GetString("AuditAsyncJobType") != "host_update" {
			t.Error("wrong async audit type")
		}
	})
	router.POST("/actions/:action", StartHostUpdate(stub))
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/actions/packages-apply", strings.NewReader(`{"planId":"approved-plan","acknowledge":true,"bootEnvironment":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/api/system/updates/jobs/17" {
		t.Fatalf("response: %d %s", response.Code, response.Body)
	}
	if stub.actor != "fixture-admin" || stub.action != "packages-apply" || !stub.request.Acknowledge || !stub.request.BootEnvironment || stub.request.PlanID != "approved-plan" {
		t.Fatalf("request changed: %+v", stub)
	}
}

func TestHostUpdateErrorClassification(t *testing.T) {
	for _, test := range []struct {
		error  string
		status int
	}{{"sylve_downgrade_forbidden", 409}, {"invalid_release_channel", 400}, {"pkg_version_unsupported", 503}, {"sqlite corruption", 500}} {
		response := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(response)
		updateResponse(context, nil, errors.New(test.error), 0)
		if response.Code != test.status {
			t.Fatalf("%s: got %d", test.error, response.Code)
		}
	}
}

func TestHostUpdateRoutesRequireAdminAndSelectedNodeRouting(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, fragment := range []string{
		"system.Use(middleware.EnsureAuthenticated(authService))",
		"system.Use(EnsureCorrectHost(db, authService))",
		`hostUpdates := systemJSON.Group("/updates")`,
		"hostUpdates.Use(middleware.RequireLocalAdmin(authService))",
		"systemHandlers.RegisterHostUpdateRoutes(hostUpdates, systemService.HostUpdates)",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("missing authorization/routing contract: %s", fragment)
		}
	}
	admin := strings.Index(source, "hostUpdates.Use(middleware.RequireLocalAdmin(authService))")
	registration := strings.Index(source, "systemHandlers.RegisterHostUpdateRoutes(hostUpdates, systemService.HostUpdates)")
	if admin >= registration {
		t.Fatal("host update routes registered before administrator guard")
	}
}
