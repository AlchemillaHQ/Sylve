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
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/hostupdates"
	"github.com/gin-gonic/gin"
)

type updateStub struct {
	request  hostupdates.Request
	action   string
	actor    string
	channel  string
	logCalls int
}

func (*updateStub) Get(context.Context) (hostupdates.View, error) { return hostupdates.View{}, nil }
func (*updateStub) History(context.Context, int, int, string, string) (hostupdates.HistoryPage, error) {
	return hostupdates.HistoryPage{}, nil
}
func (*updateStub) GetRelease(context.Context) (hostupdates.ReleaseView, error) {
	return hostupdates.ReleaseView{ReleaseChannel: "rolling"}, nil
}
func (s *updateStub) Start(_ context.Context, action string, request hostupdates.Request, actor string) (hostupdates.Job, error) {
	s.request, s.action, s.actor = request, action, actor
	return hostupdates.Job{HostUpdateOperation: models.HostUpdateOperation{ID: 17, Status: "queued"}}, nil
}
func (*updateStub) Job(context.Context, uint) (hostupdates.Job, error) { return hostupdates.Job{}, nil }
func (*updateStub) Cancel(context.Context, uint) (hostupdates.Job, error) {
	return hostupdates.Job{}, nil
}
func (s *updateStub) Logs(context.Context, uint, int64) (string, int64, error) {
	s.logCalls++
	return "output", 6, nil
}
func (*updateStub) SetMonitoring(context.Context, hostupdates.Monitoring) (models.HostUpdateState, error) {
	return models.HostUpdateState{}, nil
}

func (s *updateStub) SetReleaseChannel(_ context.Context, channel string) (models.HostUpdateState, error) {
	s.channel = channel
	return models.HostUpdateState{ReleaseChannel: channel}, nil
}

func TestSylveReleaseReadIsNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/sylve/release", GetSylveRelease(&updateStub{}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sylve/release", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Body.String(), `"releaseChannel":"rolling"`) {
		t.Fatalf("release read: %d %v %s", response.Code, response.Header(), response.Body)
	}
}

func TestHostUpdateInputValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &updateStub{}
	router := gin.New()
	router.POST("/actions/:action", StartHostUpdate(stub))
	router.PUT("/sylve/channel", SetSylveReleaseChannel(stub))
	router.GET("/jobs/:id/logs", GetHostUpdateJob(stub, true))
	for _, test := range []struct {
		method, path, body string
	}{
		{"POST", "/actions/check", "{"},
		{"PUT", "/sylve/channel", `{"channel":true}`},
		{"GET", "/jobs/1/logs?offset=-1", ""},
		{"GET", "/jobs/1/logs?offset=NaN", ""},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", test.path, response.Code, response.Body)
		}
	}
	if stub.action != "" || stub.channel != "" || stub.logCalls != 0 {
		t.Fatalf("invalid input reached the service: %+v", stub)
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
