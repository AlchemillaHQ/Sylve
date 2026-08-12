// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestSetServiceStateAcceptsExplicitFalse(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{
		Services: []models.AvailableService{models.Jails},
	}).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/system/basic-settings/services/:service", SetServiceState(&system.Service{DB: db}, nil))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPatch,
		"/system/basic-settings/services/jails",
		[]byte(`{"enabled":false}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[ServiceStateResponse]](t, response)
	if body.Message != "service_state_updated" || body.Data.Enabled || !body.Data.Changed {
		t.Fatalf("unexpected response: %+v", body)
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if len(current.Services) != 0 {
		t.Fatalf("services = %v; want none", current.Services)
	}
}

func TestSetServiceStateValidatesBodyServiceAndSize(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(32))
	router.PATCH("/system/basic-settings/services/:service", SetServiceState(&system.Service{DB: db}, nil))

	tests := []struct {
		name       string
		path       string
		body       []byte
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing enabled",
			path:       "/system/basic-settings/services/jails",
			body:       []byte(`{}`),
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid_basic_settings_request",
		},
		{
			name:       "unsupported service",
			path:       "/system/basic-settings/services/unknown",
			body:       []byte(`{"enabled":true}`),
			wantStatus: http.StatusBadRequest,
			wantError:  "unsupported_service",
		},
		{
			name:       "oversized body",
			path:       "/system/basic-settings/services/jails",
			body:       []byte(`{"enabled":true,"padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantError:  "basic_settings_request_too_large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testutil.PerformJSONRequest(t, router, http.MethodPatch, test.path, test.body)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantError {
				t.Fatalf("error = %v; want %q", body.Error, test.wantError)
			}
		})
	}
}

func TestBasicSettingsReturnsNotFoundWithoutSettings(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/system/basic-settings", BasicSettings(&system.Service{DB: db}))
	response := testutil.PerformJSONRequest(t, router, http.MethodGet, "/system/basic-settings", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if body.Error != system.ErrBasicSettingsNotFound.Error() {
		t.Fatalf("error = %v; want %q", body.Error, system.ErrBasicSettingsNotFound.Error())
	}
}

func TestBasicSettingsServiceRouteUsesDesiredStatePatch(t *testing.T) {
	routesSource, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Fatalf("reading routes.go: %v", err)
	}
	if !regexp.MustCompile(`systemJSON\.PATCH\("/basic-settings/services/:service",\s*systemHandlers\.SetServiceState`).Match(routesSource) {
		t.Fatal("routes.go is missing the desired-state service PATCH route")
	}
	if strings.Contains(string(routesSource), "/basic-settings/services/:service/toggle") {
		t.Fatal("routes.go still registers the non-idempotent toggle route")
	}
}
