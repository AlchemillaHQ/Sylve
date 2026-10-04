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
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	iscsiService "github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
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
	router.PATCH("/system/basic-settings/services/:service", SetServiceState(&system.Service{DB: db}, nil, nil))
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

func TestISCSIStopFailureReturnsAcceptedAndRetriesSavedState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	db.Create(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI, models.Jails}})
	stops := 0
	t.Cleanup(utils.SetCommandWithContextForTest(func(command string, args ...string) *exec.Cmd {
		if command == "/usr/sbin/service" && len(args) == 2 && args[1] == "onestatus" {
			return exec.Command("/usr/bin/printf", "ctld is running as pid 23.")
		}
		if command == "/usr/sbin/service" && len(args) == 2 && args[1] == "onestop" {
			stops++
		}
		return exec.Command("/usr/bin/false")
	}))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/services/:service", SetServiceState(&system.Service{DB: db}, nil, &iscsiService.Service{DB: db}))
	for i := 0; i < 2; i++ {
		response := testutil.PerformJSONRequest(t, router, http.MethodPatch, "/services/iscsi", []byte(`{"enabled":false}`))
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		body := testutil.DecodeJSONResponse[internal.APIResponse[ServiceStateResponse]](t, response)
		if body.Message != "iscsi_configuration_saved_apply_pending" || body.Data.Enabled || body.Data.Changed != (i == 0) {
			t.Fatalf("response=%+v", body)
		}
	}
	if stops != 2 {
		t.Fatalf("stop attempts=%d want 2", stops)
	}
	var saved models.BasicSettings
	db.First(&saved)
	if len(saved.Services) != 1 || saved.Services[0] != models.Jails {
		t.Fatal("desired state rolled back or another service changed")
	}
}

func TestISCSIEnableFailureReturnsAcceptedAndRetriesSavedState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &iscsiModels.ISCSIInitiator{},
		&iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetPortal{}, &iscsiModels.ISCSITargetLUN{})
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.Jails}}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(iscsiService.SetConfigPath(t.TempDir() + "/iscsi.conf"))
	t.Cleanup(iscsiService.SetTargetConfigPath(t.TempDir() + "/ctl.conf"))
	starts, running := 0, false
	t.Cleanup(utils.SetCommandWithContextForTest(func(command string, args ...string) *exec.Cmd {
		if command == "/usr/sbin/service" {
			if args[0] == "iscsid" {
				return exec.Command("/usr/bin/true")
			}
			if args[1] == "onestatus" {
				if running {
					return exec.Command("/usr/bin/printf", "ctld is running as pid 23.")
				}
				return exec.Command("/usr/bin/false")
			}
			if args[1] == "onestart" {
				starts++
				if starts == 1 {
					return exec.Command("/usr/bin/false")
				}
				running = true
			}
		}
		if command == "/bin/ps" {
			return exec.Command("/usr/bin/printf", "fixture birth")
		}
		if command == "/usr/sbin/ctladm" {
			if args[0] == "portlist" {
				return exec.Command("/usr/bin/printf", "<ctlportlist/>")
			}
			return exec.Command("/usr/bin/printf", "<ctllunlist/>")
		}
		return exec.Command("/usr/bin/true")
	}))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/services/:service", SetServiceState(&system.Service{DB: db}, nil, &iscsiService.Service{DB: db}))
	for i, status := range []int{http.StatusAccepted, http.StatusOK} {
		response := testutil.PerformJSONRequest(t, router, http.MethodPatch, "/services/iscsi", []byte(`{"enabled":true}`))
		if response.Code != status {
			t.Fatalf("status=%d want %d body=%s", response.Code, status, response.Body.String())
		}
		body := testutil.DecodeJSONResponse[internal.APIResponse[ServiceStateResponse]](t, response)
		if !body.Data.Enabled || body.Data.Changed != (i == 0) || (i == 0 && body.Message != "iscsi_configuration_saved_apply_pending") {
			t.Fatalf("response=%+v", body)
		}
		var saved models.BasicSettings
		if err := db.First(&saved).Error; err != nil || !slices.Equal(saved.Services, []models.AvailableService{models.Jails, models.ISCSI}) {
			t.Fatal("pending enable changed another service or rolled back desired state")
		}
	}
	if starts != 2 {
		t.Fatalf("start attempts=%d want 2", starts)
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
	router.PATCH("/system/basic-settings/services/:service", SetServiceState(&system.Service{DB: db}, nil, nil))

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
