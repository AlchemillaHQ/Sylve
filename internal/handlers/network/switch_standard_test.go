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
	"net/http"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

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
