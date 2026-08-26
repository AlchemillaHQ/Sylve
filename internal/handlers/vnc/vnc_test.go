// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vncHandler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtSvc "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func performVNCHandshakeRequest(
	t *testing.T,
	service *libvirtSvc.Service,
	port string,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vnc/:port", VNCProxyHandler(service))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/vnc/"+port, nil)
	router.ServeHTTP(response, request)
	return response
}

func assertVNCHandshakeError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
	}

	var body internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "error" || body.Message != wantCode || body.Error != wantCode || body.Data != nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestVNCProxyRejectsInvalidPortBeforeUpgrade(t *testing.T) {
	for _, port := range []string{"0", "65536", "not-a-port"} {
		t.Run(port, func(t *testing.T) {
			response := performVNCHandshakeRequest(t, nil, port)
			assertVNCHandshakeError(t, response, http.StatusBadRequest, "invalid_vnc_port")
		})
	}
}

func TestVNCProxyRejectsUnavailableServiceBeforeUpgrade(t *testing.T) {
	for _, test := range []struct {
		name    string
		service *libvirtSvc.Service
	}{
		{name: "nil service"},
		{name: "nil database", service: &libvirtSvc.Service{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performVNCHandshakeRequest(t, test.service, "5900")
			assertVNCHandshakeError(t, response, http.StatusServiceUnavailable, "vnc_service_unavailable")
		})
	}
}

func TestVNCProxyRejectsUnknownVMPortBeforeUpgrade(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	response := performVNCHandshakeRequest(t, &libvirtSvc.Service{DB: database}, "5900")
	assertVNCHandshakeError(t, response, http.StatusNotFound, "vnc_port_not_found")
}

func TestResolveVNCBackendEndpointUsesMatchedVMBind(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	vm := vmModels.VM{
		Name:       "bound-vm",
		RID:        41,
		VNCEnabled: true,
		VNCPort:    5907,
		VNCBind:    "192.0.2.15",
	}
	if err := database.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	endpoint, err := resolveVNCBackendEndpoint(&libvirtSvc.Service{DB: database}, vm.VNCPort)
	if err != nil {
		t.Fatalf("resolve backend endpoint: %v", err)
	}
	if endpoint != "192.0.2.15:5907" {
		t.Fatalf("endpoint=%q want=%q", endpoint, "192.0.2.15:5907")
	}
}
