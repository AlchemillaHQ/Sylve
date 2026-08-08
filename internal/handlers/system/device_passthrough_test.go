// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func restorePassthroughHandlerOperations(t *testing.T) {
	t.Helper()
	oldAdd := addPPTDeviceOperation
	oldImport := importPPTDeviceOperation
	oldRemove := removePPTDeviceOperation
	t.Cleanup(func() {
		addPPTDeviceOperation = oldAdd
		importPPTDeviceOperation = oldImport
		removePPTDeviceOperation = oldRemove
	})
}

func TestPassthroughErrorStatusAndCode(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{system.ErrInvalidPassthroughDevice, http.StatusBadRequest, "invalid_passthrough_device"},
		{system.ErrUnsupportedPassthroughDomain, http.StatusBadRequest, "unsupported_passthrough_domain"},
		{system.ErrPassthroughDeviceNotFound, http.StatusNotFound, "passthrough_device_not_found"},
		{system.ErrPassthroughDeviceAlreadyAdded, http.StatusConflict, "passthrough_device_already_managed"},
		{system.ErrPassthroughDeviceNeedsImport, http.StatusConflict, "passthrough_device_requires_import"},
		{system.ErrPassthroughDeviceNotAttached, http.StatusConflict, "passthrough_device_not_attached"},
		{system.ErrPassthroughDeviceInUse, http.StatusConflict, "passthrough_device_in_use"},
		{errors.New("failure"), http.StatusInternalServerError, "passthrough_operation_failed"},
	}

	for _, test := range tests {
		if status := passthroughErrorStatus(test.err); status != test.wantStatus {
			t.Errorf("error %v status = %d; want %d", test.err, status, test.wantStatus)
		}
		if code := system.PassthroughErrorCode(test.err); code != test.wantCode {
			t.Errorf("error %v code = %q; want %q", test.err, code, test.wantCode)
		}
	}
}

func TestAddPPTDeviceReturnsCreatedMapping(t *testing.T) {
	restorePassthroughHandlerOperations(t)
	addPPTDeviceOperation = func(*system.Service, string, string) (*models.PassedThroughIDs, error) {
		return &models.PassedThroughIDs{ID: 7, Domain: 0, DeviceID: "1/2/3", OldDriver: "xhci"}, nil
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/ppt-devices", AddPPTDevice(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/system/ppt-devices",
		[]byte(`{"domain":"0","deviceId":"1/2/3"}`),
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[models.PassedThroughIDs]](t, response)
	if body.Message != "device_added" || body.Data.ID != 7 || body.Data.DeviceID != "1/2/3" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestPassthroughRequestBodyLimit(t *testing.T) {
	restorePassthroughHandlerOperations(t)
	called := false
	addPPTDeviceOperation = func(*system.Service, string, string) (*models.PassedThroughIDs, error) {
		called = true
		return nil, nil
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(32))
	router.POST("/system/ppt-devices", AddPPTDevice(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/system/ppt-devices",
		[]byte(`{"domain":"0","deviceId":"1/2/3","padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`),
	)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if called {
		t.Fatal("service operation ran for an oversized request")
	}
}

func TestRemovePPTDeviceReturnsRebootWarning(t *testing.T) {
	restorePassthroughHandlerOperations(t)
	removePPTDeviceOperation = func(*system.Service, uint) (bool, error) {
		return true, nil
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/system/ppt-devices/:id", RemovePPTDevice(&system.Service{}))
	response := testutil.PerformJSONRequest(t, router, http.MethodDelete, "/system/ppt-devices/7", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[RemovePassthroughDeviceResponse]](t, response)
	if body.Message != "device_removed_reboot_required" || !body.Data.RebootRequired {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestSystemRoutesUseWriteAuthorizationAndPreLoggerJSONLimit(t *testing.T) {
	source, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Fatalf("reading routes.go: %v", err)
	}

	checks := map[string]*regexp.Regexp{
		"write authorization":         regexp.MustCompile(`system\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`),
		"selected node before limit":  regexp.MustCompile(`system\.Use\(EnsureCorrectHost\(db, authService\)\)[\s\S]*systemJSON\.Use\(middleware\.LimitRequestBody`),
		"limit before logger":         regexp.MustCompile(`systemJSON\.Use\(middleware\.LimitRequestBody[\s\S]*systemJSON\.Use\(middleware\.RequestLoggerMiddleware`),
		"file explorer admin":         regexp.MustCompile(`fileExplorer\.Use\(middleware\.RequireLocalAdmin\(authService\)\)`),
		"file explorer core ordering": regexp.MustCompile(`fileExplorerCore\.Use\(middleware\.LimitRequestBody[\s\S]*fileExplorerCore\.Use\(middleware\.RequestLoggerMiddleware`),
		"file explorer transfer log":  regexp.MustCompile(`fileExplorerTransfer\.Use\(middleware\.RequestLoggerMiddleware`),
	}

	for name, pattern := range checks {
		if !pattern.Match(source) {
			t.Errorf("routes.go is missing %s", name)
		}
	}
}
