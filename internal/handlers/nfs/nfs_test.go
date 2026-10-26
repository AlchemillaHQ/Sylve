// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfsHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/logger"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/nfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func setupHandler(t *testing.T) (*nfs.Service, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &nfsModels.NFSSettings{}, &nfsModels.NFSShare{}, &nfsModels.NFSRuntime{})
	if err := db.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatal(err)
	}
	service := nfs.NewService(db)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(nfs.MaxRequestBodyBytes))
	router.GET("/config", GetConfig(service))
	router.PUT("/config", SetConfig(service))
	router.GET("/shares", GetShares(service))
	router.POST("/shares", SaveShare(service, true))
	router.PUT("/shares/:id", SaveShare(service, false))
	router.PUT("/shares/:id/enabled", SetShareEnabled(service))
	router.DELETE("/shares/:id", DeleteShare(service))
	router.GET("/status", GetStatus(service))
	router.GET("/preview", PreviewExports(service))
	router.POST("/apply", Apply(service))
	return service, router
}

func configRequest(t *testing.T, protocol string, numericIDs bool) []byte {
	t.Helper()
	body, err := json.Marshal(nfsModels.NFSConfig{Protocol: protocol, NumericIDs: numericIDs, BindAddresses: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestConfigResponsesDistinguishSavedFromApplied(t *testing.T) {
	service, router := setupHandler(t)
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/config", configRequest(t, "v4", true))
	if response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body)
	}
	result := testutil.DecodeJSONResponse[internal.APIResponse[MutationResult]](t, response)
	if result.Message != "nfs_configuration_saved" || !result.Data.Saved || result.Data.Applied || result.Data.ApplyStatus != "stopped" {
		t.Fatalf("save response claims apply: %#v", result)
	}
	response = testutil.PerformRequest(t, router, http.MethodGet, "/config", nil, nil)
	config := testutil.DecodeJSONResponse[internal.APIResponse[nfsModels.NFSConfig]](t, response)
	if config.Data.Protocol != "v4" || !config.Data.NumericIDs || response.Code != http.StatusOK || strings.Contains(response.Body.String(), "v4RootDataset") {
		t.Fatalf("saved config %#v", config)
	}
	if err := service.DB.Model(&models.BasicSettings{}).Where("1 = 1").Update("services", `["nfs"]`).Error; err != nil {
		t.Fatal(err)
	}
	response = testutil.PerformRequest(t, router, http.MethodGet, "/status", nil, nil)
	status := testutil.DecodeJSONResponse[internal.APIResponse[nfs.Status]](t, response)
	if !status.Data.Enabled || status.Data.ApplyStatus != "pending" || status.Data.Running != nil || status.Data.Managed {
		t.Fatalf("misleading runtime status: %s", response.Body)
	}
}

func TestWriteErrorIgnoresOnlyClientCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		canceled bool
		err      error
		ignored  bool
	}{
		{"client canceled", true, context.Canceled, true},
		{"wrapped client cancellation", true, fmt.Errorf("discover filesystems: %w", context.Canceled), true},
		{"internal cancellation", false, context.Canceled, false},
		{"internal deadline", false, context.DeadlineExceeded, false},
		{"unrelated failure after disconnect", true, errors.New("database unavailable"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := logger.L
			logger.L = zerolog.New(&output)
			t.Cleanup(func() { logger.L = previous })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/datasets", nil).WithContext(ctx)
			writeError(c, test.err)
			if test.ignored {
				if !c.IsAborted() || c.Writer.Written() || response.Body.Len() != 0 || output.Len() != 0 {
					t.Fatalf("client cancellation was logged or answered as a failure: response=%s, log=%s", response.Body, &output)
				}
			} else if response.Code != http.StatusInternalServerError || !strings.Contains(output.String(), "nfs_configuration_request_failed") {
				t.Fatalf("real failure was hidden: status=%d, log=%s", response.Code, &output)
			}
		})
	}
}

func TestRejectInvalidBodiesWithoutChangingSavedState(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{"null config", "/config", `null`},
		{"empty config", "/config", `{}`},
		{"null protocol", "/config", `{"protocol":null}`},
		{"invalid protocol", "/config", `{"protocol":"v2"}`},
		{"manual root", "/config", `{"protocol":"v4","v4RootDataset":"1"}`},
		{"raw flags", "/config", `{"protocol":"v3","flags":"-mapall=root"}`},
		{"trailing JSON", "/config", `{"protocol":"v3"} {}`},
		{"trailing null", "/config", `{"protocol":"v3"} null`},
		{"trailing scalar", "/config", `{"protocol":"v3"} true`},
		{"trailing malformed JSON", "/config", `{"protocol":"v3"} {`},
		{"null share", "/shares", `null`},
		{"missing readonly", "/shares", `{"dataset":"1","enabled":true,"everyone":false,"clients":["192.0.2.1"]}`},
		{"null readonly", "/shares", `{"dataset":"1","enabled":true,"readOnly":null,"everyone":false,"clients":["192.0.2.1"]}`},
		{"empty restrictions", "/shares", `{"dataset":"1","enabled":true,"readOnly":true,"everyone":false,"clients":[]}`},
		{"raw export", "/shares", `{"dataset":"1","enabled":true,"readOnly":true,"everyone":true,"clients":[],"options":"-mapall=root"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, router := setupHandler(t)
			method := http.MethodPut
			if test.path == "/shares" {
				method = http.MethodPost
			}
			response := testutil.PerformJSONRequest(t, router, method, test.path, []byte(test.body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var settingsCount, sharesCount int64
			if err := service.DB.Model(&nfsModels.NFSSettings{}).Count(&settingsCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.DB.Model(&nfsModels.NFSShare{}).Count(&sharesCount).Error; err != nil {
				t.Fatal(err)
			}
			if settingsCount != 0 || sharesCount != 0 {
				t.Fatalf("rejected body changed desired state: %d settings, %d shares", settingsCount, sharesCount)
			}
		})
	}
}

func TestBodyLimitAndValidationField(t *testing.T) {
	_, router := setupHandler(t)
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/config", []byte(`{"protocol":"`+strings.Repeat("a", int(nfs.MaxRequestBodyBytes))+`"}`))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	response = testutil.PerformJSONRequest(t, router, http.MethodPut, "/config", []byte(`{"protocol":"v3"}`+strings.Repeat(" ", int(nfs.MaxRequestBodyBytes))))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized trailing data: status=%d body=%s", response.Code, response.Body)
	}
	response = testutil.PerformJSONRequest(t, router, http.MethodPost, "/shares", []byte(`{"dataset":"1","enabled":false,"readOnly":true,"everyone":false,"clients":["hostname.example.com"]}`))
	result := testutil.DecodeJSONResponse[internal.APIResponse[nfs.ValidationError]](t, response)
	if response.Code != http.StatusBadRequest || result.Data.Field != "clients" {
		t.Fatalf("no field-specific validation: %s", response.Body)
	}
}

func TestDisableAndDeleteDoNotNeedAvailableZFS(t *testing.T) {
	service, router := setupHandler(t)
	share := nfsModels.NFSShare{NFSShareConfig: nfsModels.NFSShareConfig{Dataset: "1", Enabled: true, ReadOnly: true, Clients: []string{"192.0.2.1"}}}
	if err := service.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/shares/1/enabled", []byte(`{"enabled":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body)
	}
	response = testutil.PerformRequest(t, router, http.MethodDelete, "/shares/1", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body)
	}
	for _, path := range []string{"/shares/0", "/shares/-1", "/shares/bad", "/shares/9223372036854775808", "/shares/18446744073709551616"} {
		response = testutil.PerformRequest(t, router, http.MethodDelete, path, nil, nil)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid ID path=%s status=%d", path, response.Code)
		}
	}
	response = testutil.PerformRequest(t, router, http.MethodDelete, "/shares/1", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing share status=%d", response.Code)
	}
}

func TestNFSWritesRequireLocalAdmin(t *testing.T) {
	service, _ := setupHandler(t)
	if err := service.DB.AutoMigrate(&models.User{}, &models.Group{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{Username: "admin", Admin: true}
	reader := models.User{Username: "reader"}
	if err := service.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&reader).Error; err != nil {
		t.Fatal(err)
	}
	auth := &authService.Service{DB: service.DB}
	for _, test := range []struct {
		authType string
		id       uint
		status   int
	}{
		{"sylve", admin.ID, http.StatusOK},
		{"pam", admin.ID, http.StatusOK},
		{authService.AuthTypeSylvePasskey, admin.ID, http.StatusOK},
		{"sylve", reader.ID, http.StatusForbidden},
		{"oidc", admin.ID, http.StatusForbidden},
		{"cluster-key", admin.ID, http.StatusForbidden},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("AuthType", test.authType); c.Set("UserID", test.id) })
		router.Use(middleware.RequireLocalAdminForWrites(auth))
		router.PUT("/config", SetConfig(service))
		response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/config", configRequest(t, "v3", false))
		if response.Code != test.status {
			t.Errorf("%s status=%d want=%d body=%s", test.authType, response.Code, test.status, response.Body)
		}
	}
}

func TestNFSRouteGuards(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		`nfsGroup\.Use\(middleware\.EnsureAuthenticated\(authService\)\)`,
		`nfsGroup\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`,
		`nfsGroup\.Use\(EnsureCorrectHost\(db, authService\)\)`,
		`nfsGroup\.Use\(middleware\.LimitRequestBody\(nfs.MaxRequestBodyBytes\)\)`,
		`nfsGroup\.Use\(middleware\.RequestLoggerMiddleware\(telemetryDB, authService\)\)`,
	} {
		if !regexp.MustCompile(pattern).Match(source) {
			t.Errorf("missing NFS route guard: %s", pattern)
		}
	}
}

func TestApplyRequiresExplicitStrictConfirmation(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"confirm":false}`, `{"confirm":null}`, `{"confirm":true,"flags":"unsafe"}`, `{"confirm":true} {}`} {
		_, router := setupHandler(t)
		response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/apply", []byte(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unconfirmed apply %s: %d %s", body, response.Code, response.Body)
		}
	}
}

func TestShareReplacementCannotSilentlyRemoveMapping(t *testing.T) {
	service, router := setupHandler(t)
	share := nfsModels.NFSShare{NFSShareConfig: nfsModels.NFSShareConfig{Dataset: "1", Enabled: true, ReadOnly: true, Clients: []string{"192.0.2.1"}, MapallUser: "nobody"}}
	if err := service.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/shares/1", []byte(`{"dataset":"1","enabled":true,"readOnly":true,"everyone":false,"clients":["192.0.2.1"]}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("incomplete replacement accepted: %d %s", response.Code, response.Body)
	}
	var saved nfsModels.NFSShare
	if err := service.DB.First(&saved, share.ID).Error; err != nil || saved.MapallUser != "nobody" {
		t.Fatalf("mapping was removed: %#v, %v", saved, err)
	}
}

func TestApplyFailureResponsePreservesSavedMutation(t *testing.T) {
	service, _ := setupHandler(t)
	router := gin.New()
	router.POST("/pending", func(c *gin.Context) {
		saved(c, http.StatusCreated, service, &nfsModels.NFSShare{ID: 7}, fmt.Errorf("%w: kernel rejected policy", nfs.ErrApplyFailed))
	})
	response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/pending", []byte(`{}`))
	result := testutil.DecodeJSONResponse[internal.APIResponse[MutationResult]](t, response)
	if response.Code != http.StatusAccepted || !result.Data.Saved || result.Data.Applied || result.Data.ApplyStatus != "pending" || result.Data.Share == nil || result.Data.Share.ID != 7 || !strings.Contains(result.Error, "kernel rejected policy") {
		t.Fatalf("pending apply was reported as failed persistence or successful serving: %s", response.Body)
	}
}
