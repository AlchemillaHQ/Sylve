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
	traffic.DELETE("", BulkDeleteFirewallTrafficRules(svc))
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

func TestCreateFirewallTrafficRuleAcceptsTCPUDPWithPorts(t *testing.T) {
	router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
	body := validFirewallTrafficRuleBody("allow-dns")
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload["protocol"] = "tcp_udp"
	payload["dstPortsRaw"] = "53"
	body, _ = json.Marshal(payload)

	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/traffic", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	var rule networkModels.FirewallTrafficRule
	if err := db.First(&rule).Error; err != nil {
		t.Fatalf("load created traffic rule: %v", err)
	}
	if rule.Protocol != "tcp_udp" || rule.DstPortsRaw != "53" {
		t.Fatalf("unexpected stored traffic rule: %+v", rule)
	}
}

func TestBulkDeleteFirewallTrafficRulesUsesCollectionDelete(t *testing.T) {
	router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rules := []networkModels.FirewallTrafficRule{
		{Name: "delete-one", Visible: true, Enabled: true, Priority: 10, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "keep", Visible: true, Enabled: true, Priority: 20, Action: "pass", Direction: "out", Protocol: "any", Family: "any"},
		{Name: "delete-two", Visible: true, Enabled: true, Priority: 30, Action: "block", Direction: "in", Protocol: "any", Family: "any"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatalf("seed traffic rules: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, rules[2].ID}})

	rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic", body)
	response := decodeFirewallTrafficHandlerResponse(t, rr)
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "firewall_traffic_rules_deleted" {
		t.Fatalf("unexpected bulk-delete response: status=%d response=%+v", rr.Code, response)
	}

	var remaining []networkModels.FirewallTrafficRule
	if err := db.Order("priority asc").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining rules: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != rules[1].ID || remaining[0].Priority != 20 {
		t.Fatalf("bulk delete changed the wrong rules or compacted priorities: %+v", remaining)
	}
}

func TestBulkDeleteFirewallTrafficRulesRejectsInvalidIDSets(t *testing.T) {
	oversized := make([]uint, networkService.MaxFirewallTrafficRuleDeleteItems+1)
	for i := range oversized {
		oversized[i] = uint(i + 1)
	}

	tests := []struct {
		name string
		body any
	}{
		{name: "missing ids", body: map[string]any{}},
		{name: "empty ids", body: map[string]any{"ids": []uint{}}},
		{name: "duplicate ids", body: map[string]any{"ids": []uint{1, 1}}},
		{name: "zero id", body: map[string]any{"ids": []uint{0}}},
		{name: "negative id", body: map[string]any{"ids": []int{-1}}},
		{name: "too many ids", body: map[string]any{"ids": oversized}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
			body, _ := json.Marshal(tt.body)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic", body)
			response := decodeFirewallTrafficHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_traffic_request" {
				t.Fatalf("status=%d response=%+v", rr.Code, response)
			}
		})
	}
}

func TestBulkDeleteFirewallTrafficRulesPreflightsEntireBatch(t *testing.T) {
	t.Run("missing rule", func(t *testing.T) {
		router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rules := []networkModels.FirewallTrafficRule{
			{Name: "keep-one", Visible: true, Enabled: true, Priority: 1, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
			{Name: "keep-two", Visible: true, Enabled: true, Priority: 2, Action: "pass", Direction: "out", Protocol: "any", Family: "any"},
		}
		if err := db.Create(&rules).Error; err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, 999999}})
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic", body)
		response := decodeFirewallTrafficHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "firewall_traffic_rule_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var count int64
		if err := db.Model(&networkModels.FirewallTrafficRule{}).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("missing-rule preflight changed rows: count=%d err=%v", count, err)
		}
	})

	t.Run("managed rule", func(t *testing.T) {
		router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rules := []networkModels.FirewallTrafficRule{
			{Name: "keep-visible", Visible: true, Enabled: true, Priority: 1, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
			{Name: "keep-managed", Visible: true, Enabled: true, Priority: 2, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		}
		if err := db.Create(&rules).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rules[1]).Update("visible", false).Error; err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, rules[1].ID}})
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic", body)
		response := decodeFirewallTrafficHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "hidden_firewall_rule_managed_by_wireguard" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var count int64
		if err := db.Model(&networkModels.FirewallTrafficRule{}).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("managed-rule preflight changed rows: count=%d err=%v", count, err)
		}
	})
}

func TestBulkDeleteFirewallTrafficRulesReturnsStableInternalError(t *testing.T) {
	router, db := setupFirewallTrafficHandlerRouter(t, networkService.MaxRequestBodyBytes)
	if err := db.Migrator().DropTable(&networkModels.FirewallTrafficRule{}); err != nil {
		t.Fatalf("drop traffic rule table: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"ids": []uint{1}})
	rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/traffic", body)
	response := decodeFirewallTrafficHandlerResponse(t, rr)
	if rr.Code != http.StatusInternalServerError || response.Error != "firewall_traffic_rules_delete_failed" {
		t.Fatalf("status=%d response=%+v", rr.Code, response)
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

	bulkOversized := performNetworkJSONRequest(
		t,
		limitedRouter,
		http.MethodDelete,
		"/network/firewall/traffic",
		[]byte(`{"ids":[`+strings.Repeat("123456789,", limit)+`1]}`),
	)
	response = decodeFirewallTrafficHandlerResponse(t, bulkOversized)
	if bulkOversized.Code != http.StatusRequestEntityTooLarge || response.Error != "firewall_traffic_request_too_large" {
		t.Fatalf("expected bulk delete 413, status=%d response=%+v", bulkOversized.Code, response)
	}
}
