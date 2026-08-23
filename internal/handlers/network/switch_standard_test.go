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

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
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
	if resp.Error != "invalid_standard_switch_vlan" {
		t.Fatalf("expected invalid_standard_switch_vlan error, got %q", resp.Error)
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
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); e != "standard_switch_network4_source_conflict" {
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
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
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
	if strings.Contains(e, "network4_manual") {
		t.Fatalf("DHCP should have cleared the IPv4 manual address, but it was validated: %q", e)
	}
	if e != "invalid_standard_switch_network6_manual" {
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
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
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
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
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
	switchModel := networkModels.StandardSwitch{Name: "existing", BridgeName: "vm-existing", MTU: 1500}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("failed to seed standard switch: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/network/switch/standard/:id", UpdateStandardSwitch(svc))
	r.DELETE("/network/switch/standard/:id", DeleteStandardSwitch(svc))
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
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"network4": %d,
		"network4Manual": "10.0.0.1/24"
	}`, obj.ID)

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard/1", []byte(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); e != "standard_switch_network4_source_conflict" {
		t.Fatalf("expected mutual-exclusivity error, got %q", e)
	}
}

func TestUpdateStandardSwitchForwardsManualValidation(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"network4Manual": "not-a-cidr"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard/1", []byte(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
		t.Fatalf("expected invalid_network4_manual error, got %q", e)
	}
}

func TestUpdateStandardSwitchDHCPClearsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"dhcp": true,
		"network4Manual": "garbage4",
		"network6Manual": "garbage6"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard/1", []byte(body))
	e := standardSwitchResponseError(t, rr)
	if strings.Contains(e, "network4_manual") {
		t.Fatalf("DHCP should have cleared the IPv4 manual address on PUT, but it was validated: %q", e)
	}
	if e != "invalid_standard_switch_network6_manual" {
		t.Fatalf("expected the IPv6 manual address to remain and fail validation on PUT, got %q", e)
	}
}

func TestUpdateStandardSwitchDisableIPv6KeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"disableIPv6": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard/1", []byte(body))
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
		t.Fatalf("expected disableIPv6 to leave IPv4 manual intact and fail validation, got %q", e)
	}
}

func TestUpdateStandardSwitchSLAACKeepsIPv4Manual(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	body := `{
		"mtu": 1500,
		"private": false,
		"ports": ["em0"],
		"slaac": true,
		"network4Manual": "garbage4"
	}`

	rr := performNetworkJSONRequest(t, r, http.MethodPut, "/network/switch/standard/1", []byte(body))
	if e := standardSwitchResponseError(t, rr); e != "invalid_standard_switch_network4_manual" {
		t.Fatalf("expected SLAAC to leave IPv4 manual intact and fail validation, got %q", e)
	}
}

func TestStandardSwitchHandlersRejectInvalidPathIDs(t *testing.T) {
	for _, id := range []string{"0", "-1", "not-a-number"} {
		t.Run(id, func(t *testing.T) {
			r, _ := setupStandardSwitchUpdateRouter(t)
			for _, method := range []string{http.MethodPut, http.MethodDelete} {
				rr := performNetworkJSONRequest(
					t,
					r,
					method,
					"/network/switch/standard/"+id,
					[]byte(`{"private":false}`),
				)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("method=%s id=%q expected 400, got %d body=%s", method, id, rr.Code, rr.Body.String())
				}
				if code := standardSwitchResponseError(t, rr); code != "invalid_standard_switch_id" {
					t.Fatalf("method=%s id=%q expected stable invalid-ID code, got %q", method, id, code)
				}
			}
		})
	}
}

func TestStandardSwitchHandlersReturnNotFoundForMissingSwitch(t *testing.T) {
	r, _ := setupStandardSwitchUpdateRouter(t)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		rr := performNetworkJSONRequest(
			t,
			r,
			method,
			"/network/switch/standard/999999",
			[]byte(`{"private":false}`),
		)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("method=%s expected 404, got %d body=%s", method, rr.Code, rr.Body.String())
		}
		if code := standardSwitchResponseError(t, rr); code != "standard_switch_not_found" {
			t.Fatalf("method=%s expected stable not-found code, got %q", method, code)
		}
		if strings.Contains(rr.Body.String(), "record not found") {
			t.Fatalf("method=%s leaked database details: %s", method, rr.Body.String())
		}
	}
}
