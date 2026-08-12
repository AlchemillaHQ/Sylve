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

type dhcpConfigHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupDHCPConfigHandlerRouter(t *testing.T, bodyLimit int64, seedConfig bool) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
	)
	if seedConfig {
		config := networkModels.DHCPConfig{
			DNSServers:  []string{},
			Domain:      "lan",
			ExpandHosts: true,
		}
		if err := db.Create(&config).Error; err != nil {
			t.Fatalf("seed DHCP config: %v", err)
		}
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	config := router.Group("/network/dhcp/config")
	if bodyLimit > 0 {
		config.Use(middleware.LimitRequestBody(bodyLimit))
	}
	config.GET("", GetDHCPConfig(svc))
	config.PUT("", ModifyDHCPConfig(svc))
	return router, db
}

func decodeDHCPConfigHandlerResponse(t *testing.T, recorder *httptest.ResponseRecorder) dhcpConfigHandlerResponse {
	t.Helper()
	var response dhcpConfigHandlerResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return response
}

func TestGetDHCPConfigReturnsConcreteConfiguration(t *testing.T) {
	router, _ := setupDHCPConfigHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
	recorder := performNetworkJSONRequest(t, router, http.MethodGet, "/network/dhcp/config", nil)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" {
		t.Fatalf("unexpected GET response: status=%d response=%+v", recorder.Code, response)
	}

	var config networkModels.DHCPConfig
	if err := json.Unmarshal(response.Data, &config); err != nil {
		t.Fatalf("decode DHCP config: %v", err)
	}
	if config.ID == 0 || config.StandardSwitches == nil || config.ManualSwitches == nil || config.DNSServers == nil {
		t.Fatalf("expected concrete DHCP config with stable collections, got %#v", config)
	}
}

func TestModifyDHCPConfigNoChangeIsSuccessful(t *testing.T) {
	router, _ := setupDHCPConfigHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
	body := []byte(`{"standardSwitches":[],"manualSwitches":[],"dnsServers":[],"domain":"lan","expandHosts":true}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPut, "/network/dhcp/config", body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" || response.Message != "dhcp_config_saved" {
		t.Fatalf("unexpected no-change response: status=%d response=%+v", recorder.Code, response)
	}
}

func TestModifyDHCPConfigReturnsStableValidationError(t *testing.T) {
	router, _ := setupDHCPConfigHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
	body := []byte(`{"standardSwitches":[],"manualSwitches":[],"dnsServers":[],"domain":"bad_domain","expandHosts":true}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPut, "/network/dhcp/config", body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusBadRequest || response.Error != "invalid_dhcp_domain" {
		t.Fatalf("expected stable validation response, status=%d response=%+v", recorder.Code, response)
	}
}

func TestModifyDHCPConfigRejectsOversizedJSON(t *testing.T) {
	const limit = 128
	router, _ := setupDHCPConfigHandlerRouter(t, limit, true)
	body := []byte(`{"domain":"` + strings.Repeat("a", limit) + `"}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPut, "/network/dhcp/config", body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusRequestEntityTooLarge || response.Error != "dhcp_config_request_too_large" {
		t.Fatalf("expected stable 413 response, status=%d response=%+v", recorder.Code, response)
	}
}

func TestModifyDHCPConfigRejectsRemovingSwitchReferencedByRange(t *testing.T) {
	router, db := setupDHCPConfigHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
	standard := networkModels.StandardSwitch{Name: "standard", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	var config networkModels.DHCPConfig
	if err := db.First(&config).Error; err != nil {
		t.Fatalf("load DHCP config: %v", err)
	}
	if err := db.Model(&config).Association("StandardSwitches").Append(&standard); err != nil {
		t.Fatalf("associate standard switch: %v", err)
	}
	rangeModel := networkModels.DHCPRange{
		Type:             "ipv4",
		StartIP:          "192.0.2.10",
		EndIP:            "192.0.2.20",
		StandardSwitchID: &standard.ID,
	}
	if err := db.Create(&rangeModel).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}

	body := []byte(`{"standardSwitches":[],"manualSwitches":[],"dnsServers":[],"domain":"lan","expandHosts":true}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPut, "/network/dhcp/config", body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusConflict || response.Error != "dhcp_switch_has_ranges" {
		t.Fatalf("expected stable 409 response: status=%d response=%+v", recorder.Code, response)
	}
}

func TestGetDHCPConfigDoesNotLeakMissingRecordError(t *testing.T) {
	router, _ := setupDHCPConfigHandlerRouter(t, networkService.MaxRequestBodyBytes, false)
	recorder := performNetworkJSONRequest(t, router, http.MethodGet, "/network/dhcp/config", nil)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusInternalServerError || response.Error != "dhcp_config_retrieval_failed" {
		t.Fatalf("unexpected missing-config response: status=%d response=%+v", recorder.Code, response)
	}
	if strings.Contains(recorder.Body.String(), "record not found") {
		t.Fatalf("response leaked database detail: %s", recorder.Body.String())
	}
}
