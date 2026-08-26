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
	nat.DELETE("", BulkDeleteFirewallNATRules(svc))
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

func TestCreateFirewallNATRuleRejectsTCPUDP(t *testing.T) {
	router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
	var payload map[string]any
	if err := json.Unmarshal(validFirewallNATRuleBody("combined-protocol"), &payload); err != nil {
		t.Fatal(err)
	}
	payload["protocol"] = "tcp_udp"
	body, _ := json.Marshal(payload)

	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/nat", body)
	response := decodeFirewallNATHandlerResponse(t, rr)
	if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_nat_request" {
		t.Fatalf("status=%d response=%+v", rr.Code, response)
	}
}

func TestBulkDeleteFirewallNATRulesUsesCollectionDelete(t *testing.T) {
	router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rules := []networkModels.FirewallNATRule{
		{Name: "delete-one", Visible: true, Enabled: true, Priority: 10, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "keep", Visible: true, Enabled: true, Priority: 20, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.10"},
		{Name: "delete-two", Visible: true, Enabled: true, Priority: 30, NATType: "binat", IngressInterfaces: []string{"em2"}, Family: "inet", Protocol: "any", TranslateToRaw: "198.51.100.10", DNATTargetRaw: "10.0.0.10"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatalf("seed NAT rules: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, rules[2].ID}})

	rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
	response := decodeFirewallNATHandlerResponse(t, rr)
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "firewall_nat_rules_deleted" {
		t.Fatalf("unexpected bulk-delete response: status=%d response=%+v", rr.Code, response)
	}

	var remaining []networkModels.FirewallNATRule
	if err := db.Order("priority asc").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining rules: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != rules[1].ID || remaining[0].Priority != 20 {
		t.Fatalf("bulk delete changed the wrong rules or compacted priorities: %+v", remaining)
	}

	body, _ = json.Marshal(map[string]any{"ids": []uint{rules[1].ID}})
	rr = performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
	response = decodeFirewallNATHandlerResponse(t, rr)
	if rr.Code != http.StatusOK || response.Message != "firewall_nat_rules_deleted" {
		t.Fatalf("one-item collection delete failed: status=%d response=%+v", rr.Code, response)
	}
	var count int64
	if err := db.Model(&networkModels.FirewallNATRule{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("one-item collection delete left rows: count=%d err=%v", count, err)
	}
}

func TestBulkDeleteFirewallNATRulesRejectsInvalidIDSets(t *testing.T) {
	oversized := make([]uint, networkService.MaxFirewallNATRuleDeleteItems+1)
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
		{name: "string id", body: map[string]any{"ids": []string{"1"}}},
		{name: "too many ids", body: map[string]any{"ids": oversized}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
			body, _ := json.Marshal(tt.body)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
			response := decodeFirewallNATHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_nat_request" {
				t.Fatalf("status=%d response=%+v", rr.Code, response)
			}
		})
	}
}

func TestBulkDeleteFirewallNATRulesPreflightsEntireBatch(t *testing.T) {
	t.Run("missing rule", func(t *testing.T) {
		router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rules := []networkModels.FirewallNATRule{
			{Name: "keep-one", Visible: true, Enabled: true, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
			{Name: "keep-two", Visible: true, Enabled: true, Priority: 2, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.10"},
		}
		if err := db.Create(&rules).Error; err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, 999999}})
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
		response := decodeFirewallNATHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "firewall_nat_rule_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var count int64
		if err := db.Model(&networkModels.FirewallNATRule{}).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("missing-rule preflight changed rows: count=%d err=%v", count, err)
		}
	})

	t.Run("managed rule", func(t *testing.T) {
		router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rules := []networkModels.FirewallNATRule{
			{Name: "keep-visible", Visible: true, Enabled: true, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
			{Name: "keep-managed", Visible: true, Enabled: true, Priority: 2, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.10"},
		}
		if err := db.Create(&rules).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rules[1]).Update("visible", false).Error; err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"ids": []uint{rules[0].ID, rules[1].ID}})
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
		response := decodeFirewallNATHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "hidden_firewall_rule_managed_by_wireguard" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var count int64
		if err := db.Model(&networkModels.FirewallNATRule{}).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("managed-rule preflight changed rows: count=%d err=%v", count, err)
		}
	})
}

func TestBulkDeleteFirewallNATRulesReturnsStableInternalError(t *testing.T) {
	router, db := setupFirewallNATHandlerRouter(t, networkService.MaxRequestBodyBytes)
	if err := db.Migrator().DropTable(&networkModels.FirewallNATRule{}); err != nil {
		t.Fatalf("drop NAT rule table: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"ids": []uint{1}})
	rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/firewall/nat", body)
	response := decodeFirewallNATHandlerResponse(t, rr)
	if rr.Code != http.StatusInternalServerError || response.Error != "firewall_nat_rules_delete_failed" {
		t.Fatalf("status=%d response=%+v", rr.Code, response)
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

	bulkOversized := performNetworkJSONRequest(
		t,
		limitedRouter,
		http.MethodDelete,
		"/network/firewall/nat",
		[]byte(`{"ids":[`+strings.Repeat("123456789,", limit)+`1]}`),
	)
	response = decodeFirewallNATHandlerResponse(t, bulkOversized)
	if bulkOversized.Code != http.StatusRequestEntityTooLarge || response.Error != "firewall_nat_request_too_large" {
		t.Fatalf("expected bulk delete 413, status=%d response=%+v", bulkOversized.Code, response)
	}
}
