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
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestVNCRouteAndSwaggerContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve VNC test path")
	}
	dir := filepath.Dir(filename)

	read := func(path string) string {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(contents)
	}

	handlerSource := read(filepath.Join(dir, "vnc.go"))
	routesSource := read(filepath.Join(dir, "..", "routes.go"))
	start := strings.Index(handlerSource, "// @Summary Open a VM VNC WebSocket")
	end := strings.Index(handlerSource, "func VNCProxyHandler(")
	if start < 0 || end <= start {
		t.Fatal("VNC source Swagger block is missing")
	}
	vncBlock := handlerSource[start:end]

	for _, required := range []string{
		`// @Param port path int true`,
		`// @Param auth query string true`,
		`// @Param overtake query bool false`,
		"// @Success 101",
		"// @Failure 400",
		"// @Failure 401",
		"// @Failure 403",
		"// @Failure 404",
		"// @Failure 500",
		"// @Failure 502",
		"// @Failure 503",
		"// @Router /vnc/{port} [get]",
	} {
		if !strings.Contains(vncBlock, required) {
			t.Errorf("VNC source Swagger block is missing %q", required)
		}
	}
	if strings.Contains(vncBlock, "// @Security BearerAuth") {
		t.Fatal("VNC handshake must document its query capability instead of browser Bearer authentication")
	}
	if strings.Count(routesSource, `vnc.GET("/:port",`) != 1 {
		t.Fatal("VNC route must be registered exactly once")
	}
}
