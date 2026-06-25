// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupStandardSwitchCreateRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	svc := &network.Service{DB: db}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/network/switch/standard", CreateStandardSwitch(svc))
	return r, db
}

func standardSwitchResponseError(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v body=%s", err, rr.Body.String())
	}
	return resp.Error
}

func TestCreateStandardSwitchDoesNotPanicWhenOptionalIPv6FieldsMissing(t *testing.T) {
	db := newNetworkHandlerTestDB(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	svc := &network.Service{DB: db}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/network/switch/standard", CreateStandardSwitch(svc))

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(`{
		"name": "switch-a",
		"vlan": 5000,
		"private": false,
		"ports": ["em0"]
	}`))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusInternalServerError, rr.Code, rr.Body.String())
	}

	var resp struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	if resp.Status != "error" {
		t.Fatalf("expected error status, got %q", resp.Status)
	}
	if resp.Message != "failed_to_create_switch" {
		t.Fatalf("expected failed_to_create_switch message, got %q", resp.Message)
	}
	if !strings.Contains(resp.Error, "invalid_vlan") {
		t.Fatalf("expected invalid_vlan error, got %q", resp.Error)
	}
}

func TestCreateStandardSwitchRejectsObjectAndManualConflict(t *testing.T) {
	r, db := setupStandardSwitchCreateRouter(t)

	obj := networkModels.Object{
		Name:    "net-obj",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "10.0.0.0/24"}},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	body := fmt.Sprintf(`{
		"name": "sw-conflict",
		"private": false,
		"ports": ["em0"],
		"network4": %d,
		"network4Manual": "10.0.0.1/24"
	}`, obj.ID)

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(body))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "network4_object_and_manual_mutually_exclusive") {
		t.Fatalf("expected mutual-exclusivity error, got %q", e)
	}
}

func TestCreateStandardSwitchForwardsManualValidation(t *testing.T) {
	r, _ := setupStandardSwitchCreateRouter(t)

	body := `{
		"name": "sw-bad-manual",
		"private": false,
		"ports": ["em0"],
		"network4Manual": "not-a-cidr"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(body))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected invalid_network4_manual error, got %q", e)
	}
}

func TestCreateStandardSwitchDHCPClearsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchCreateRouter(t)

	body := `{
		"name": "sw-dhcp",
		"private": false,
		"ports": ["em0"],
		"dhcp": true,
		"network4Manual": "garbage4",
		"network6Manual": "garbage6"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(body))
	e := standardSwitchResponseError(t, rr)
	if strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("DHCP should have cleared the IPv4 manual address, but it was validated: %q", e)
	}
	if !strings.Contains(e, "invalid_network6_manual") {
		t.Fatalf("expected the IPv6 manual address to remain and fail validation, got %q", e)
	}
}

func TestCreateStandardSwitchDisableIPv6KeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchCreateRouter(t)

	body := `{
		"name": "sw-no-ipv6",
		"private": false,
		"ports": ["em0"],
		"disableIPv6": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(body))
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected disableIPv6 to leave IPv4 manual intact and fail validation, got %q", e)
	}
}

func TestCreateStandardSwitchSLAACKeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchCreateRouter(t)

	body := `{
		"name": "sw-slaac",
		"private": false,
		"ports": ["em0"],
		"slaac": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPost, "/network/switch/standard", []byte(body))
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected SLAAC to leave IPv4 manual intact and fail validation, got %q", e)
	}
}

func setupStandardSwitchUpdateRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	svc := &network.Service{DB: db}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/network/switch/standard", UpdateStandardSwitch(svc))
	return r, db
}

func TestUpdateStandardSwitchRejectsObjectAndManualConflict(t *testing.T) {
	r, db := setupStandardSwitchUpdateRouter(t)

	obj := networkModels.Object{
		Name:    "net-obj-upd",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "10.0.0.0/24"}},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	body := fmt.Sprintf(`{
		"id": 1,
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"network4": %d,
		"network4Manual": "10.0.0.1/24"
	}`, obj.ID)

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard", []byte(body))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "network4_object_and_manual_mutually_exclusive") {
		t.Fatalf("expected mutual-exclusivity error, got %q", e)
	}
}

func TestUpdateStandardSwitchForwardsManualValidation(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"id": 1,
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"network4Manual": "not-a-cidr"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard", []byte(body))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected invalid_network4_manual error, got %q", e)
	}
}

func TestUpdateStandardSwitchDHCPClearsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"id": 1,
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"dhcp": true,
		"network4Manual": "garbage4",
		"network6Manual": "garbage6"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard", []byte(body))
	e := standardSwitchResponseError(t, rr)
	if strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("DHCP should have cleared the IPv4 manual address on PUT, but it was validated: %q", e)
	}
	if !strings.Contains(e, "invalid_network6_manual") {
		t.Fatalf("expected the IPv6 manual address to remain and fail validation on PUT, got %q", e)
	}
}

func TestUpdateStandardSwitchDisableIPv6KeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"id": 1,
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"disableIPv6": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard", []byte(body))
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected disableIPv6 to leave IPv4 manual intact and fail validation, got %q", e)
	}
}

func TestUpdateStandardSwitchSLAACKeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"id": 1,
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"slaac": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard", []byte(body))
	if e := standardSwitchResponseError(t, rr); !strings.Contains(e, "invalid_network4_manual") {
		t.Fatalf("expected SLAAC to leave IPv4 manual intact and fail validation, got %q", e)
	}
}
