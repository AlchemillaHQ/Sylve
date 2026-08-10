// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newJailStatsHandlerRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.JailStats{})
	if err := database.Create(&jailModels.Jail{
		CTID: 104, Name: "handler-jail", Type: jailModels.JailTypeFreeBSD,
	}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	service := &jail.Service{DB: database}

	router := gin.New()
	router.GET("/jail/:ctid/stats", GetJailStatsBootstrap(service))
	router.GET("/jail/:ctid/stats/:step", GetJailStats(service))
	return router
}

func TestJailStatsRoutesReturnBootstrapAndCompatibleEmptyRange(t *testing.T) {
	router := newJailStatsHandlerRouter(t)

	bootstrapRecorder := httptest.NewRecorder()
	router.ServeHTTP(bootstrapRecorder, httptest.NewRequest(http.MethodGet, "/jail/104/stats", nil))
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, body = %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}
	var bootstrap internal.APIResponse[db.StatsBootstrap[jailModels.JailStats]]
	if err := json.Unmarshal(bootstrapRecorder.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if bootstrap.Data.HistoryState != db.StatsHistoryNeverRecorded || bootstrap.Data.Points == nil {
		t.Fatalf("bootstrap data = %+v, want never-recorded with [] points", bootstrap.Data)
	}

	explicitRecorder := httptest.NewRecorder()
	router.ServeHTTP(explicitRecorder, httptest.NewRequest(http.MethodGet, "/jail/104/stats/hourly", nil))
	if explicitRecorder.Code != http.StatusOK {
		t.Fatalf("explicit status = %d, body = %s", explicitRecorder.Code, explicitRecorder.Body.String())
	}
	var explicit internal.APIResponse[[]jailModels.JailStats]
	if err := json.Unmarshal(explicitRecorder.Body.Bytes(), &explicit); err != nil {
		t.Fatalf("decode explicit response: %v", err)
	}
	if explicit.Data == nil || len(explicit.Data) != 0 {
		t.Fatalf("explicit data = %#v, want a non-nil empty array", explicit.Data)
	}
}

func TestJailStatsRoutesValidateCTIDStepAndNotFound(t *testing.T) {
	router := newJailStatsHandlerRouter(t)

	tests := []struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		{path: "/jail/not-a-ctid/stats", wantStatus: http.StatusBadRequest, wantCode: "invalid_ctid"},
		{path: "/jail/104/stats/not-a-step", wantStatus: http.StatusBadRequest, wantCode: "invalid_stats_step"},
		{path: "/jail/999/stats", wantStatus: http.StatusNotFound, wantCode: "jail_not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			var response internal.APIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != tt.wantCode {
				t.Fatalf("message = %q, want %q", response.Message, tt.wantCode)
			}
		})
	}
}
