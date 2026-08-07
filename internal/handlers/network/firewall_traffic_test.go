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
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type firewallTrafficHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupFirewallTrafficHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&models.BasicSettings{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.FirewallTrafficRule{},
	)
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	traffic := router.Group("/network/firewall/traffic")
	if bodyLimit > 0 {
		traffic.Use(middleware.LimitRequestBody(bodyLimit))
	}
	traffic.GET("", ListFirewallTrafficRules(svc))
	traffic.GET("/counters", ListFirewallTrafficRuleCounters(svc))
	traffic.POST("", CreateFirewallTrafficRule(svc))
	traffic.PUT("/reorder", ReorderFirewallTrafficRules(svc))
	traffic.PUT("/:id", EditFirewallTrafficRule(svc))
	traffic.DELETE("/:id", DeleteFirewallTrafficRule(svc))
	return router, db
}

func decodeFirewallTrafficHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) firewallTrafficHandlerResponse {
	t.Helper()
	var response firewallTrafficHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func validFirewallTrafficRuleBody(name string) []byte {
	body, _ := json.Marshal(map[string]any{
		"name":              name,
		"description":       "test rule",
		"enabled":           true,
		"log":               false,
		"quick":             false,
		"priority":          1,
		"action":            "pass",
		"direction":         "in",
		"protocol":          "any",
		"ingressInterfaces": []string{},
		"egressInterfaces":  []string{},
		"family":            "any",
		"sourceRaw":         "any",
		"sourceObjId":       nil,
		"destRaw":           "any",
		"destObjId":         nil,
		"srcPortsRaw":       "",
		"srcPortObjId":      nil,
		"dstPortsRaw":       "",
		"dstPortObjId":      nil,
	})
	return body
}

func TestCreateFirewallTrafficRuleReturnsCreatedID(t *testing.T) {
	router, _ := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/traffic", validFirewallTrafficRuleBody("allow-web"))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	response := decodeFirewallTrafficHandlerResponse(t, rr)
	var id uint
	if response.Status != "success" || json.Unmarshal(response.Data, &id) != nil || id == 0 {
		t.Fatalf("unexpected create response: %+v", response)
	}
}

func TestFirewallTrafficHandlersMapStableClientErrors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		for _, id := range []string{"0", "-1", "not-a-number"} {
			router, _ := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic/"+id, nil)
			response := decodeFirewallTrafficHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_traffic_rule_id" {
				t.Fatalf("id=%q status=%d response=%+v", id, rr.Code, response)
			}
		}
	})

	t.Run("missing rule", func(t *testing.T) {
		router, _ := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic/999999", nil)
		response := decodeFirewallTrafficHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "firewall_traffic_rule_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		if strings.Contains(rr.Body.String(), "record not found") {
			t.Fatalf("response leaked database detail: %s", rr.Body.String())
		}
	})

	t.Run("invalid selectors", func(t *testing.T) {
		router, _ := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
		body := validFirewallTrafficRuleBody("invalid-selectors")
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		payload["sourceObjId"] = 1
		body, _ = json.Marshal(payload)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/traffic", body)
		response := decodeFirewallTrafficHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_traffic_rule" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("managed rule", func(t *testing.T) {
		router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rule := networkModels.FirewallTrafficRule{
			Name: "managed", Visible: true, Enabled: true, Priority: 1,
			Action: "pass", Direction: "in", Protocol: "any", Family: "any",
		}
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rule).Update("visible", false).Error; err != nil {
			t.Fatal(err)
		}
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic/"+strconv.FormatUint(uint64(rule.ID), 10), nil)
		response := decodeFirewallTrafficHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "hidden_firewall_rule_managed_by_wireguard" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}

func TestFirewallTrafficHandlersRejectInvalidReorderAndOversizedJSON(t *testing.T) {
	router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rules := []networkModels.FirewallTrafficRule{
		{Name: "one", Visible: true, Enabled: true, Priority: 1, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "two", Visible: true, Enabled: true, Priority: 2, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	empty := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/traffic/reorder", []byte(`[]`))
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected empty reorder 400, got %d body=%s", empty.Code, empty.Body.String())
	}

	incompleteBody, _ := json.Marshal([]map[string]any{{"id": rules[0].ID, "priority": 1}})
	incomplete := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/traffic/reorder", incompleteBody)
	response := decodeFirewallTrafficHandlerResponse(t, incomplete)
	if incomplete.Code != http.StatusConflict || response.Error != "firewall_traffic_rule_conflict" {
		t.Fatalf("expected incomplete reorder 409, status=%d response=%+v", incomplete.Code, response)
	}

	const limit = 128
	limitedRouter, _ := setupFirewallTrafficHandlerRouter(t, limit)
	oversized := performNetworkJSONRequest(t, limitedRouter, http.MethodPost, "/network/firewall/traffic", validFirewallTrafficRuleBody(strings.Repeat("a", limit)))
	response = decodeFirewallTrafficHandlerResponse(t, oversized)
	if oversized.Code != http.StatusRequestEntityTooLarge || response.Error != "firewall_traffic_request_too_large" {
		t.Fatalf("expected 413, status=%d response=%+v", oversized.Code, response)
	}
}

func TestRegisteredFirewallTrafficRoutesMatchSourceAnnotations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	handlerSource, err := os.ReadFile(filepath.Join(handlerDir, "firewall.go"))
	if err != nil {
		t.Fatal(err)
	}

	registered := map[string]struct{}{}
	routePattern := regexp.MustCompile(`(?m)^\s*traffic\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString("/network/firewall/traffic"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := map[string]struct{}{}
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/network/firewall/traffic\S*) \[(get|post|put|patch|delete)\]$`)
	for _, match := range annotationPattern.FindAllStringSubmatch(string(handlerSource), -1) {
		annotated[strings.ToUpper(match[2])+" "+match[1]] = struct{}{}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 6 || len(annotated) != 6 {
		t.Fatalf("unexpected route totals: registered=%d annotated=%d", len(registered), len(annotated))
	}

	if !regexp.MustCompile(`traffic\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`).Match(routesSource) {
		t.Error("firewall traffic routes are missing local-admin write authorization")
	}
}
