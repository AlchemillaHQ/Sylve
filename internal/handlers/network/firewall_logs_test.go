// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type firewallLiveLogsHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupFirewallLiveLogsHandlerRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := &networkService.Service{}
	router.GET("/network/firewall/logs/live", ListFirewallLiveHits(svc))
	return router
}

func decodeFirewallLiveLogsHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) firewallLiveLogsHandlerResponse {
	t.Helper()
	var response firewallLiveLogsHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func TestListFirewallLiveHitsAcceptsCanonicalQueryParameters(t *testing.T) {
	router := setupFirewallLiveLogsHandlerRouter(t)
	rr := performNetworkJSONRequest(
		t,
		router,
		http.MethodGet,
		"/network/firewall/logs/live?cursor=0&limit=1&ruleType=TRAFFIC&ruleId=1&action=PASS&direction=IN&interface=bridge0&query=https",
		nil,
	)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	response := decodeFirewallLiveLogsHandlerResponse(t, rr)
	if response.Status != "success" || response.Message != "firewall_live_hits_listed" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestListFirewallLiveHitsRejectsInvalidQueryParameters(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		errorCode string
	}{
		{name: "negative cursor", query: "cursor=-1", errorCode: "invalid_firewall_live_hits_cursor"},
		{name: "invalid cursor", query: "cursor=nope", errorCode: "invalid_firewall_live_hits_cursor"},
		{name: "zero limit", query: "limit=0", errorCode: "invalid_firewall_live_hits_limit"},
		{name: "negative limit", query: "limit=-1", errorCode: "invalid_firewall_live_hits_limit"},
		{name: "invalid limit", query: "limit=nope", errorCode: "invalid_firewall_live_hits_limit"},
		{name: "invalid rule type", query: "ruleType=filter", errorCode: "invalid_firewall_live_hits_rule_type"},
		{name: "zero rule id", query: "ruleId=0", errorCode: "invalid_firewall_live_hits_rule_id"},
		{name: "negative rule id", query: "ruleId=-1", errorCode: "invalid_firewall_live_hits_rule_id"},
		{name: "invalid rule id", query: "ruleId=nope", errorCode: "invalid_firewall_live_hits_rule_id"},
		{name: "invalid action", query: "action=allow", errorCode: "invalid_firewall_live_hits_action"},
		{name: "invalid direction", query: "direction=sideways", errorCode: "invalid_firewall_live_hits_direction"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := setupFirewallLiveLogsHandlerRouter(t)
			rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/firewall/logs/live?"+tt.query, nil)
			response := decodeFirewallLiveLogsHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != tt.errorCode {
				t.Fatalf("status=%d response=%+v", rr.Code, response)
			}
			if strings.Contains(response.Error, "strconv") {
				t.Fatalf("response leaked parser detail: %+v", response)
			}
		})
	}
}

func TestFirewallLiveLogsRouteRequiresLocalAdmin(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`network\.GET\("/firewall/logs/live",\s*middleware\.RequireLocalAdmin\(authService\),\s*networkHandlers\.ListFirewallLiveHits\(networkService\)\)`).Match(routesSource) {
		t.Fatal("firewall live logs route is not protected by explicit local-admin middleware")
	}
}
