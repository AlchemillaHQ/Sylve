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
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func TestRequireLocalHostOnly(t *testing.T) {
	local, err := utils.GetSystemHostname()
	if err != nil {
		t.Fatalf("GetSystemHostname: %v", err)
	}
	tests := []struct {
		name       string
		target     string
		header     string
		statusCode int
	}{
		{name: "missing selection", target: "/write", statusCode: http.StatusNoContent},
		{name: "local selection", target: "/write", header: local, statusCode: http.StatusNoContent},
		{name: "remote selection", target: "/write", header: local + "-remote", statusCode: http.StatusConflict},
		{name: "malformed auth", target: "/write?auth=not-hex", statusCode: http.StatusBadRequest},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequireLocalHostOnly())
	router.POST("/write", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, nil)
			if test.header != "" {
				request.Header.Set("X-Current-Hostname", test.header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.statusCode {
				t.Fatalf("expected status %d, got %d: %s", test.statusCode, response.Code, response.Body.String())
			}
		})
	}
}

func TestHostInterfaceL3ListEndpointsReturnConcreteArrays(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		message string
		models  []any
		handler func(*network.Service) gin.HandlerFunc
		field   string
	}{
		{
			name:    "configuration list",
			path:    "/network/interface/l3",
			message: "host_interface_l3_list",
			models: []any{
				&networkModels.HostInterfaceL3{},
				&networkModels.HostInterfaceL3Address{},
				&networkModels.PendingApply{},
				&networkModels.NetworkPort{},
				&networkModels.ManualSwitch{},
			},
			handler: ListHostInterfaceL3,
			field:   "rows",
		},
		{
			name:    "pending list",
			path:    "/network/interface/l3/pending",
			message: "host_interface_l3_pending_list",
			models:  []any{&networkModels.HostInterfaceL3{}, &networkModels.PendingApply{}},
			handler: GetHostInterfaceL3Pending,
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newNetworkHandlerTestDB(t, test.models...)
			router := gin.New()
			router.GET(test.path, test.handler(&network.Service{DB: db}))

			rr := performNetworkJSONRequest(t, router, http.MethodGet, test.path, nil)
			var response interfaceListHandlerResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
			}
			if rr.Code != http.StatusOK || response.Status != "success" || response.Message != test.message {
				t.Fatalf("unexpected response code=%d status=%q message=%q", rr.Code, response.Status, response.Message)
			}
			data := response.Data
			if test.field != "" {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(data, &object); err != nil {
					t.Fatalf("decode data %s: %v", data, err)
				}
				data = object[test.field]
			}
			if string(data) != "[]" {
				t.Fatalf("expected a concrete empty array, got %s", data)
			}
		})
	}
}
