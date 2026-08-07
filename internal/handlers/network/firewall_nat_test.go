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

type firewallNATHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupFirewallNATHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&models.BasicSettings{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.FirewallNATRule{},
	)
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	nat := router.Group("/network/firewall/nat")
	if bodyLimit > 0 {
		nat.Use(middleware.LimitRequestBody(bodyLimit))
	}
	nat.GET("", ListFirewallNATRules(svc))
	nat.GET("/counters", ListFirewallNATRuleCounters(svc))
	nat.POST("", CreateFirewallNATRule(svc))
	nat.PUT("/reorder", ReorderFirewallNATRules(svc))
	nat.PUT("/:id", EditFirewallNATRule(svc))
	nat.DELETE("/:id", DeleteFirewallNATRule(svc))
	return router, db
}

func decodeFirewallNATHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) firewallNATHandlerResponse {
	t.Helper()
	var response firewallNATHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func validFirewallNATRuleBody(name string) []byte {
	body, _ := json.Marshal(map[string]any{
		"name":                 name,
		"description":          "test rule",
		"enabled":              true,
		"log":                  false,
		"priority":             1,
		"natType":              "snat",
		"policyRoutingEnabled": false,
		"policyRouteGateway":   "",
		"ingressInterfaces":    []string{},
		"egressInterfaces":     []string{"em0"},
		"family":               "inet",
		"protocol":             "any",
		"sourceRaw":            "any",
		"sourceObjId":          nil,
		"destRaw":              "any",
		"destObjId":            nil,
		"translateMode":        "interface",
		"translateToRaw":       "",
		"translateToObjId":     nil,
		"dnatTargetRaw":        "",
		"dnatTargetObjId":      nil,
		"dstPortsRaw":          "",
		"dstPortObjId":         nil,
		"redirectPortsRaw":     "",
		"redirectPortObjId":    nil,
	})
	return body
}

func TestCreateFirewallNATRuleReturnsCreatedID(t *testing.T) {
	router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/nat", validFirewallNATRuleBody("masquerade-lan"))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	response := decodeFirewallNATHandlerResponse(t, rr)
	var id uint
	if response.Status != "success" || json.Unmarshal(response.Data, &id) != nil || id == 0 {
		t.Fatalf("unexpected create response: %+v", response)
	}
}

func TestFirewallNATHandlersMapStableClientErrors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		for _, id := range []string{"0", "-1", "not-a-number"} {
			router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat/"+id, nil)
			response := decodeFirewallNATHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_nat_rule_id" {
				t.Fatalf("id=%q status=%d response=%+v", id, rr.Code, response)
			}
		}
	})

	t.Run("missing rule", func(t *testing.T) {
		router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat/999999", nil)
		response := decodeFirewallNATHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "firewall_nat_rule_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		if strings.Contains(rr.Body.String(), "record not found") {
			t.Fatalf("response leaked database detail: %s", rr.Body.String())
		}
	})

	t.Run("ambiguous selectors", func(t *testing.T) {
		router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
		var payload map[string]any
		if err := json.Unmarshal(validFirewallNATRuleBody("ambiguous"), &payload); err != nil {
			t.Fatal(err)
		}
		payload["sourceObjId"] = 1
		body, _ := json.Marshal(payload)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/nat", body)
		response := decodeFirewallNATHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_nat_rule" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("managed rule", func(t *testing.T) {
		router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rule := networkModels.FirewallNATRule{
			Name: "managed", Visible: true, Enabled: true, Priority: 1,
			NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface",
		}
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rule).Update("visible", false).Error; err != nil {
			t.Fatal(err)
		}
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat/"+strconv.FormatUint(uint64(rule.ID), 10), nil)
		response := decodeFirewallNATHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "hidden_firewall_rule_managed_by_wireguard" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}

func TestFirewallNATHandlersRejectInvalidReorderAndOversizedJSON(t *testing.T) {
	router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rules := []networkModels.FirewallNATRule{
		{Name: "one", Visible: true, Enabled: true, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "two", Visible: true, Enabled: true, Priority: 2, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	empty := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/nat/reorder", []byte(`[]`))
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected empty reorder 400, got %d body=%s", empty.Code, empty.Body.String())
	}

	incompleteBody, _ := json.Marshal([]map[string]any{{"id": rules[0].ID, "priority": 1}})
	incomplete := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/nat/reorder", incompleteBody)
	response := decodeFirewallNATHandlerResponse(t, incomplete)
	if incomplete.Code != http.StatusConflict || response.Error != "firewall_nat_rule_conflict" {
		t.Fatalf("expected incomplete reorder 409, status=%d response=%+v", incomplete.Code, response)
	}

	const limit = 128
	limitedRouter, _ := setupFirewallNATHandlerRouter(t, limit)
	oversized := performNetworkJSONRequest(t, limitedRouter, http.MethodPost, "/network/firewall/nat", validFirewallNATRuleBody(strings.Repeat("a", limit)))
	response = decodeFirewallNATHandlerResponse(t, oversized)
	if oversized.Code != http.StatusRequestEntityTooLarge || response.Error != "firewall_nat_request_too_large" {
		t.Fatalf("expected 413, status=%d response=%+v", oversized.Code, response)
	}
}

func TestRegisteredFirewallNATRoutesMatchSourceAnnotations(t *testing.T) {
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
	routeHandlerSource, err := os.ReadFile(filepath.Join(handlerDir, "route.go"))
	if err != nil {
		t.Fatal(err)
	}

	registered := map[string]struct{}{}
	routePattern := regexp.MustCompile(`(?m)^\s*nat\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString("/network/firewall/nat"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := map[string]struct{}{}
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/network/firewall/nat\S*) \[(get|post|put|patch|delete)\]$`)
	annotationSource := string(handlerSource) + "\n" + string(routeHandlerSource)
	for _, match := range annotationPattern.FindAllStringSubmatch(annotationSource, -1) {
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
	if len(registered) != 7 || len(annotated) != 7 {
		t.Fatalf("unexpected route totals: registered=%d annotated=%d", len(registered), len(annotated))
	}

	if !regexp.MustCompile(`nat\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`).Match(routesSource) {
		t.Error("firewall NAT routes are missing local-admin write authorization")
	}
}
