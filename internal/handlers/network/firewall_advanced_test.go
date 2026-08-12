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
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type firewallAdvancedHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupFirewallAdvancedHandlerRouter(t *testing.T, bodyLimit int64, seed bool) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t, &networkModels.FirewallAdvancedSettings{})
	if seed {
		if err := db.Create(&networkModels.FirewallAdvancedSettings{PreRules: "set skip on lo0"}).Error; err != nil {
			t.Fatalf("seed advanced settings: %v", err)
		}
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	advanced := router.Group("/network/firewall/advanced")
	if bodyLimit > 0 {
		advanced.Use(middleware.LimitRequestBody(bodyLimit))
	}
	advanced.GET("", GetFirewallAdvancedSettings(svc))
	advanced.PUT("", UpdateFirewallAdvancedSettings(svc))
	advanced.POST("/preview", PreviewRenderedConfig(svc))
	return router, db
}

func decodeFirewallAdvancedHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) firewallAdvancedHandlerResponse {
	t.Helper()
	var response firewallAdvancedHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func TestGetFirewallAdvancedSettingsReturnsConcreteSettings(t *testing.T) {
	router, _ := setupFirewallAdvancedHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/firewall/advanced", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	response := decodeFirewallAdvancedHandlerResponse(t, rr)
	var settings networkModels.FirewallAdvancedSettings
	if response.Status != "success" || json.Unmarshal(response.Data, &settings) != nil || settings.PreRules != "set skip on lo0" {
		t.Fatalf("unexpected settings response: %+v data=%s", response, response.Data)
	}
}

func TestFirewallAdvancedHandlersUseStableErrorsAndBodyLimits(t *testing.T) {
	t.Run("database error is not leaked", func(t *testing.T) {
		router, _ := setupFirewallAdvancedHandlerRouter(t, networkService.MaxRequestBodyBytes, false)
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/firewall/advanced", nil)
		response := decodeFirewallAdvancedHandlerResponse(t, rr)
		if rr.Code != http.StatusInternalServerError || response.Error != "firewall_advanced_settings_get_failed" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		if strings.Contains(rr.Body.String(), "record not found") {
			t.Fatalf("response leaked database detail: %s", rr.Body.String())
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		router, _ := setupFirewallAdvancedHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/advanced", []byte(`{"preRules":`))
		response := decodeFirewallAdvancedHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_advanced_request" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("section too large", func(t *testing.T) {
		router, _ := setupFirewallAdvancedHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		body, _ := json.Marshal(map[string]string{"preRules": strings.Repeat("a", networkService.MaxFirewallAdvancedSectionBytes+1)})
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/firewall/advanced/preview", body)
		response := decodeFirewallAdvancedHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_advanced_request" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("request body too large", func(t *testing.T) {
		const limit = 128
		router, _ := setupFirewallAdvancedHandlerRouter(t, limit, true)
		body, _ := json.Marshal(map[string]string{"preRules": strings.Repeat("a", limit)})
		rr := performNetworkJSONRequest(t, router, http.MethodPut, "/network/firewall/advanced", body)
		response := decodeFirewallAdvancedHandlerResponse(t, rr)
		if rr.Code != http.StatusRequestEntityTooLarge || response.Error != "firewall_advanced_request_too_large" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}
