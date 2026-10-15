// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/network/reconcile"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type fakeSwitchSyncService struct {
	status reconcile.Status
	err    error
	calls  int
}

func (s *fakeSwitchSyncService) SyncSwitchNetwork(context.Context) error { s.calls++; return s.err }
func (s *fakeSwitchSyncService) SwitchSyncStatus() reconcile.Status      { return s.status.Clone() }
func (s *fakeSwitchSyncService) SwitchStatus() reconcile.Status          { return s.status.Clone() }

func TestGetSwitchSyncStatusReportsBothFailuresWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runtime := &fakeSwitchSyncService{status: reconcile.Status{State: "degraded", Attempts: 4, Error: "bridge create failed"}}
	bootstrap := &fakeSwitchSyncService{status: reconcile.Status{State: "degraded", Attempts: 4, Failures: []reconcile.Failure{{Name: "first-boot", Error: "port missing"}}}}
	router := gin.New()
	router.GET("/network/switch/sync", GetSwitchSyncStatus(runtime, bootstrap))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/network/switch/sync", nil))
	var response internal.APIResponse[reconcile.SwitchStatus]
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || response.Data.Runtime.State != "degraded" || response.Data.Bootstrap.State != "degraded" ||
		response.Data.Bootstrap.Failures[0].Name != "first-boot" || runtime.calls != 0 || bootstrap.calls != 0 {
		t.Fatalf("status=%d response=%+v calls=%d/%d", rr.Code, response, runtime.calls, bootstrap.calls)
	}
}

func TestSyncSwitchesReturnsStatusOnSuccessFailureAndBusy(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		err   error
		state string
		code  int
	}{
		{"success", nil, "converged", http.StatusOK},
		{"exhausted", errors.New("permanent failure"), "degraded", http.StatusInternalServerError},
		{"busy", network.ErrSwitchSyncInProgress, "retrying", http.StatusConflict},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			service := &fakeSwitchSyncService{err: testCase.err, status: reconcile.Status{State: testCase.state, Attempts: 4}}
			router := gin.New()
			router.POST("/network/switch/sync", SyncSwitches(service, nil))
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/network/switch/sync", nil))
			var response internal.APIResponse[reconcile.SwitchStatus]
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rr.Code != testCase.code || response.Data.Runtime.State != testCase.state || service.calls != 1 {
				t.Fatalf("status=%d response=%+v calls=%d", rr.Code, response, service.calls)
			}
			if testCase.err != nil && response.Error == "" {
				t.Fatalf("failed sync returned no error code: %+v", response)
			}
		})
	}
}
