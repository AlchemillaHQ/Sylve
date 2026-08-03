// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newVMStatsHandlerRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStats{})
	if err := database.Create(&vmModels.VM{RID: 107, Name: "handler-vm"}).Error; err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	service := &libvirt.Service{DB: database}

	router := gin.New()
	router.GET("/vm/stats/:rid", GetVMStatsBootstrap(service))
	router.GET("/vm/stats/:rid/:step", GetVMStats(service))
	return router
}

func TestVMStatsRoutesReturnBootstrapAndCompatibleEmptyRange(t *testing.T) {
	router := newVMStatsHandlerRouter(t)

	bootstrapRecorder := httptest.NewRecorder()
	router.ServeHTTP(bootstrapRecorder, httptest.NewRequest(http.MethodGet, "/vm/stats/107", nil))
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, body = %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}
	var bootstrap internal.APIResponse[db.StatsBootstrap[vmModels.VMStats]]
	if err := json.Unmarshal(bootstrapRecorder.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if bootstrap.Data.HistoryState != db.StatsHistoryNeverRecorded || bootstrap.Data.Points == nil {
		t.Fatalf("bootstrap data = %+v, want never-recorded with [] points", bootstrap.Data)
	}

	explicitRecorder := httptest.NewRecorder()
	router.ServeHTTP(explicitRecorder, httptest.NewRequest(http.MethodGet, "/vm/stats/107/hourly", nil))
	if explicitRecorder.Code != http.StatusOK {
		t.Fatalf("explicit status = %d, body = %s", explicitRecorder.Code, explicitRecorder.Body.String())
	}
	var explicit internal.APIResponse[[]vmModels.VMStats]
	if err := json.Unmarshal(explicitRecorder.Body.Bytes(), &explicit); err != nil {
		t.Fatalf("decode explicit response: %v", err)
	}
	if explicit.Data == nil || len(explicit.Data) != 0 {
		t.Fatalf("explicit data = %#v, want a non-nil empty array", explicit.Data)
	}
}

func TestVMStatsRoutesValidateRIDStepAndNotFound(t *testing.T) {
	router := newVMStatsHandlerRouter(t)

	tests := []struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		{path: "/vm/stats/not-a-rid", wantStatus: http.StatusBadRequest, wantCode: "invalid_rid_format"},
		{path: "/vm/stats/107/not-a-step", wantStatus: http.StatusBadRequest, wantCode: "invalid_stats_step"},
		{path: "/vm/stats/999", wantStatus: http.StatusNotFound, wantCode: "vm_not_found"},
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
