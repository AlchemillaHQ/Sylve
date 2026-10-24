// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
)

type packageHandlerStub struct {
	jailPackageService
	action, actor string
	ctID          uint
	request       jailTypes.PackageRequest
	monitoring    jailTypes.PackageMonitoring
}

func (s *packageHandlerStub) SetPackageMonitoring(_ context.Context, ctID uint, monitor jailTypes.PackageMonitoring) (jailTypes.PackageMonitoring, error) {
	s.ctID, s.monitoring = ctID, monitor
	return monitor, nil
}

func TestPackageMonitoringHandlerPreservesCustomCron(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &packageHandlerStub{}
	router := gin.New()
	router.PUT("/jail/:ctid/packages/monitoring", JailPackageMonitoring(stub, true))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/jail/900/packages/monitoring", strings.NewReader(`{"enabled":true,"cronExpr":"0 3 * * 0"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || stub.ctID != 900 || !stub.monitoring.Enabled || stub.monitoring.CronExpr != "0 3 * * 0" || !strings.Contains(response.Body.String(), `"cronExpr":"0 3 * * 0"`) {
		t.Fatalf("custom schedule changed at the API boundary: %+v %d %s", stub.monitoring, response.Code, response.Body)
	}
}

func (s *packageHandlerStub) StartPackageOperation(_ context.Context, ctID uint, action string, request jailTypes.PackageRequest, source, actor string) (jailTypes.PackageJob, error) {
	s.ctID, s.action, s.actor, s.request = ctID, action, actor, request
	return jailTypes.PackageJob{TaskID: 77, OperationID: "operation", Action: action, Status: "queued"}, nil
}

func TestPackageHandlerReturnsAcceptedAndBindsActivityIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		body    string
		request jailTypes.PackageRequest
	}{
		{"applications", `{"packages":["foo-bar"]}`, jailTypes.PackageRequest{Packages: []string{"foo-bar"}}},
		{"system", `{"scope":"system","all":true}`, jailTypes.PackageRequest{Scope: "system", All: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &packageHandlerStub{}
			router := gin.New()
			var jobID, jobType any
			router.Use(func(c *gin.Context) {
				c.Set("Username", "tester")
				c.Next()
				jobID, _ = c.Get("AuditAsyncJobID")
				jobType, _ = c.Get("AuditAsyncJobType")
			})
			router.POST("/jail/:ctid/packages/upgrade/preview", StartJailPackageOperation(stub, "pkg-preview"))
			request := httptest.NewRequest(http.MethodPost, "/jail/900/packages/upgrade/preview", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted || jobID != uint(77) || jobType != "jail_pkg-preview" || response.Header().Get("Location") != "/api/jail/900/packages/jobs/77" {
				t.Fatalf("response %d %s job=%v type=%v", response.Code, response.Body, jobID, jobType)
			}
			if stub.ctID != 900 || stub.actor != "tester" || stub.action != "pkg-preview" || !reflect.DeepEqual(stub.request, tc.request) {
				t.Fatalf("request %+v, want %+v", stub, tc.request)
			}
		})
	}
}

func TestPackageResponseKeepsErrorsDistinct(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"package_management_unsupported_jail_type", 422},
		{"jail_not_running", 409},
		{"package_plan_stale", 409},
		{"package_job_not_found", 404},
		{"replication_lease_not_owned", 403},
		{"invalid_package_name", 400},
		{"invalid_package_audit_cron", 400},
		{"package_service_unavailable", 503},
		{"pkg_static_not_found", 503},
		{"application_repositories_unavailable", 409},
		{"package_selection_required", 400},
		{"unexpected_failure", 500},
	} {
		t.Run(tc.code, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			packageResponse(ctx, nil, errors.New(tc.code), 0)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
		})
	}
}

func TestPackageHandlerRejectsMalformedRequestsBeforeAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &packageHandlerStub{}
	router := gin.New()
	router.POST("/jail/:ctid/packages/audit", StartJailPackageOperation(stub, "pkg-audit"))
	for _, body := range []string{`{"packages":`, `{"scope":true}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/jail/900/packages/audit", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != 400 || stub.ctID != 0 {
			t.Fatalf("admitted malformed request %q: %d", body, response.Code)
		}
	}
}
