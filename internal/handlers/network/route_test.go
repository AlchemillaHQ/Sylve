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

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type staticRouteHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupStaticRouteHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.StaticRoute{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.FirewallNATRule{},
		&networkModels.WireGuardClient{},
	)
	svc := &networkService.Service{DB: db, TelemetryDB: db}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	routes := router.Group("/network/route")
	if bodyLimit > 0 {
		routes.Use(middleware.LimitRequestBody(bodyLimit))
	}
	routes.GET("", ListStaticRoutes(svc))
	routes.POST("", CreateStaticRoute(svc))
	routes.PUT("/:id", EditStaticRoute(svc))
	routes.DELETE("/:id", DeleteStaticRoute(svc))
	router.GET("/network/firewall/nat/:id/route-suggestions", SuggestStaticRoutesFromNATRule(svc))
	return router, db
}

func decodeStaticRouteHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) staticRouteHandlerResponse {
	t.Helper()
	var response staticRouteHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func validDisabledStaticRouteBody(name string) []byte {
	body, _ := json.Marshal(map[string]any{
		"name":             name,
		"description":      "test route",
		"enabled":          false,
		"fib":              0,
		"destinationType":  "network",
		"destination":      "192.0.2.0/24",
		"destinationRaw":   "192.0.2.0/24",
		"destinationObjId": nil,
		"family":           "inet",
		"nextHopMode":      "interface",
		"gateway":          "",
		"gatewayRaw":       "",
		"gatewayObjId":     nil,
		"gatewayZone":      "",
		"interface":        "em0",
	})
	return body
}

func TestCreateStaticRouteReturnsCreatedID(t *testing.T) {
	router, db := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/route", validDisabledStaticRouteBody("documentation"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	response := decodeStaticRouteHandlerResponse(t, rr)
	var id uint
	if response.Status != "success" || json.Unmarshal(response.Data, &id) != nil || id == 0 {
		t.Fatalf("unexpected create response: %+v", response)
	}
	var route networkModels.StaticRoute
	if err := db.First(&route, id).Error; err != nil {
		t.Fatal(err)
	}
	if route.Enabled {
		t.Fatal("explicitly disabled route was persisted as enabled")
	}
}

func TestStaticRouteHandlersMapStableClientErrors(t *testing.T) {
	t.Run("invalid route IDs", func(t *testing.T) {
		for _, id := range []string{"0", "-1", "not-a-number"} {
			router, _ := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/route/"+id, nil)
			response := decodeStaticRouteHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_static_route_id" {
				t.Fatalf("id=%q status=%d response=%+v", id, rr.Code, response)
			}
		}
	})

	t.Run("missing route", func(t *testing.T) {
		router, _ := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/route/999999", nil)
		response := decodeStaticRouteHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "static_route_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		if strings.Contains(rr.Body.String(), "record not found") {
			t.Fatalf("response leaked database detail: %s", rr.Body.String())
		}
	})

	t.Run("invalid NAT rule ID", func(t *testing.T) {
		router, _ := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/firewall/nat/0/route-suggestions", nil)
		response := decodeStaticRouteHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_firewall_nat_rule_id" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("ineligible NAT rule", func(t *testing.T) {
		router, db := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rule := networkModels.FirewallNATRule{
			Name: "not-policy-routed", NATType: "snat", Family: "inet",
			EgressInterfaces: []string{"em0"}, SourceRaw: "192.0.2.0/24",
		}
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/firewall/nat/"+strconv.FormatUint(uint64(rule.ID), 10)+"/route-suggestions", nil)
		response := decodeStaticRouteHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "static_route_suggestion_unavailable" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}

func TestStaticRouteHandlersRejectInvalidAndOversizedJSON(t *testing.T) {
	router, _ := setupStaticRouteHandlerRouter(t, networkService.MaxRequestBodyBytes)
	invalid := performNetworkJSONRequest(t, router, http.MethodPost, "/network/route", []byte(`{"name":"missing fields"}`))
	response := decodeStaticRouteHandlerResponse(t, invalid)
	if invalid.Code != http.StatusBadRequest || response.Error != "invalid_static_route_request" {
		t.Fatalf("expected stable 400, status=%d response=%+v", invalid.Code, response)
	}

	const limit = 128
	limitedRouter, _ := setupStaticRouteHandlerRouter(t, limit)
	oversized := performNetworkJSONRequest(t, limitedRouter, http.MethodPost, "/network/route", validDisabledStaticRouteBody(strings.Repeat("a", limit)))
	response = decodeStaticRouteHandlerResponse(t, oversized)
	if oversized.Code != http.StatusRequestEntityTooLarge || response.Error != "static_route_request_too_large" {
		t.Fatalf("expected stable 413, status=%d response=%+v", oversized.Code, response)
	}
}
