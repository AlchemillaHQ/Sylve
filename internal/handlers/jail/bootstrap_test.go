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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
)

type bootstrapHandlerStub struct {
	listResult   []jailServiceInterfaces.BootstrapEntry
	listErr      error
	createResult jailServiceInterfaces.BootstrapCreateResult
	createErr    error
	deleteResult jailServiceInterfaces.BootstrapDeleteResult
	deleteErr    error
	deletedPool  string
	deletedName  string
}

func (s *bootstrapHandlerStub) ListBootstraps(
	_ context.Context,
	_ string,
) ([]jailServiceInterfaces.BootstrapEntry, error) {
	return s.listResult, s.listErr
}

func (s *bootstrapHandlerStub) CreateBootstrap(
	_ context.Context,
	_ jailServiceInterfaces.BootstrapRequest,
) (jailServiceInterfaces.BootstrapCreateResult, error) {
	return s.createResult, s.createErr
}

func (s *bootstrapHandlerStub) DeleteBootstrap(
	_ context.Context,
	pool string,
	name string,
) (jailServiceInterfaces.BootstrapDeleteResult, error) {
	s.deletedPool = pool
	s.deletedName = name
	return s.deleteResult, s.deleteErr
}

func bootstrapTestRouter(service jailBootstrapService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/jail/bootstraps", ListBootstraps(service))
	router.POST("/jail/bootstraps", CreateBootstrap(service))
	router.DELETE("/jail/bootstraps/:name", DeleteBootstrap(service))
	return router
}

func decodeBootstrapResponse(t *testing.T, recorder *httptest.ResponseRecorder) internal.APIResponse[json.RawMessage] {
	t.Helper()
	var response internal.APIResponse[json.RawMessage]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return response
}

func TestListBootstrapsRequiresPool(t *testing.T) {
	recorder := httptest.NewRecorder()
	bootstrapTestRouter(&bootstrapHandlerStub{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/jail/bootstraps", nil),
	)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if response := decodeBootstrapResponse(t, recorder); response.Message != "pool_required" {
		t.Fatalf("message = %q", response.Message)
	}
}

func TestCreateBootstrapReturnsDistinctQueuedAndCompletedOutcomes(t *testing.T) {
	for _, test := range []struct {
		name       string
		outcome    string
		status     string
		wantStatus int
		wantCode   string
	}{
		{name: "queued", outcome: "queued", status: "pending", wantStatus: http.StatusAccepted, wantCode: "bootstrap_queued"},
		{name: "completed", outcome: "already_completed", status: "completed", wantStatus: http.StatusOK, wantCode: "bootstrap_already_completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &bootstrapHandlerStub{createResult: jailServiceInterfaces.BootstrapCreateResult{
				Pool: "tank", Name: "15-0-Base", Status: test.status, Outcome: test.outcome,
			}}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/jail/bootstraps",
				strings.NewReader(`{"pool":"tank","major":15,"minor":0,"type":"base"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			bootstrapTestRouter(stub).ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if response := decodeBootstrapResponse(t, recorder); response.Message != test.wantCode {
				t.Fatalf("message = %q", response.Message)
			}
			if got := recorder.Header().Get("Location"); got != "/api/jail/bootstraps/15-0-Base?pool=tank" {
				t.Fatalf("Location = %q", got)
			}
		})
	}
}

func TestBootstrapErrorsUseStableCodesAndStatuses(t *testing.T) {
	for _, test := range []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{err: "unsupported_bootstrap_version: 99.0", wantStatus: http.StatusBadRequest, wantCode: "unsupported_bootstrap_version"},
		{err: "pool_not_found", wantStatus: http.StatusNotFound, wantCode: "pool_not_found"},
		{err: "bootstrap_already_in_progress", wantStatus: http.StatusConflict, wantCode: "bootstrap_already_in_progress"},
		{err: "pkg_not_found", wantStatus: http.StatusServiceUnavailable, wantCode: "pkg_not_found"},
		{err: "failed_to_get_bootstrap_record: sqlite busy", wantStatus: http.StatusInternalServerError, wantCode: "failed_to_get_bootstrap_record"},
	} {
		t.Run(test.wantCode, func(t *testing.T) {
			stub := &bootstrapHandlerStub{createErr: errors.New(test.err)}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/jail/bootstraps",
				strings.NewReader(`{"pool":"tank","major":15,"minor":0,"type":"base"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			bootstrapTestRouter(stub).ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if response := decodeBootstrapResponse(t, recorder); response.Message != test.wantCode {
				t.Fatalf("message = %q", response.Message)
			}
		})
	}
}

func TestDeleteBootstrapUsesPathNameAndReportsIdempotentOutcome(t *testing.T) {
	stub := &bootstrapHandlerStub{deleteResult: jailServiceInterfaces.BootstrapDeleteResult{
		Pool: "tank", Name: "15-0-Base", Outcome: "already_absent",
	}}
	recorder := httptest.NewRecorder()
	bootstrapTestRouter(stub).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodDelete, "/jail/bootstraps/15-0-Base?pool=tank", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.deletedPool != "tank" || stub.deletedName != "15-0-Base" {
		t.Fatalf("delete identity = %q/%q", stub.deletedPool, stub.deletedName)
	}
	if response := decodeBootstrapResponse(t, recorder); response.Message != "bootstrap_already_absent" {
		t.Fatalf("message = %q", response.Message)
	}
}
