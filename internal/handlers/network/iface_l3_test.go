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
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func TestListHostInterfaceL3ReturnsConcreteArrayResponse(t *testing.T) {
	db := newNetworkHandlerTestDB(t,
		&networkModels.HostInterfaceL3{},
		&networkModels.HostInterfaceL3Address{},
		&networkModels.PendingApply{},
		&networkModels.PendingApplyTarget{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
	)
	svc := &network.Service{DB: db}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/network/interface/l3", ListHostInterfaceL3(svc))

	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/interface/l3", nil)

	var response interfaceListHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "host_interface_l3_list" {
		t.Fatalf("unexpected response code=%d status=%q message=%q", rr.Code, response.Status, response.Message)
	}
	var data struct {
		Rows json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode data %s: %v", response.Data, err)
	}
	if string(data.Rows) != "[]" {
		t.Fatalf("expected an empty rows array, got %s", data.Rows)
	}
}

func TestGetHostInterfaceL3PendingReturnsConcreteArrayResponse(t *testing.T) {
	db := newNetworkHandlerTestDB(t,
		&networkModels.HostInterfaceL3{},
		&networkModels.PendingApply{},
		&networkModels.PendingApplyTarget{},
	)
	svc := &network.Service{DB: db}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/network/interface/l3/pending", GetHostInterfaceL3Pending(svc))

	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/interface/l3/pending", nil)

	var response interfaceListHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "host_interface_l3_pending_list" {
		t.Fatalf("unexpected response code=%d status=%q message=%q", rr.Code, response.Status, response.Message)
	}
	if string(response.Data) != "[]" {
		t.Fatalf("expected an empty array response, got %s", response.Data)
	}
}
