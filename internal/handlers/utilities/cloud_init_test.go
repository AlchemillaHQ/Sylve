// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newCloudInitTemplateHandlerRouter(t *testing.T, limit int64) *gin.Engine {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.CloudInitTemplate{})
	service := &utilities.Service{DB: database}
	router := gin.New()
	router.Use(middleware.LimitRequestBody(limit))
	router.POST("/utilities/cloud-init/templates", AddCloudInitTemplate(service))
	router.PUT("/utilities/cloud-init/templates/:templateId", EditCloudInitTemplate(service))
	router.DELETE("/utilities/cloud-init/templates/:templateId", DeleteCloudInitTemplate(service))
	return router
}

func performCloudInitTemplateRequest(
	router *gin.Engine,
	method, path, body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestCloudInitTemplateHandlerReturnsTypedCreationAndStableErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newCloudInitTemplateHandlerRouter(t, utilities.MaxRequestBodyBytes)

	created := performCloudInitTemplateRequest(
		router,
		http.MethodPost,
		"/utilities/cloud-init/templates",
		`{"name":"Template","user":"#cloud-config","meta":"instance-id: one","networkConfig":""}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	if location := created.Header().Get("Location"); location != "/api/utilities/cloud-init/templates/1" {
		t.Fatalf("Location=%q", location)
	}
	var payload map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["name"] != "Template" || data["networkConfig"] != "" {
		t.Fatalf("create payload=%+v", payload)
	}

	omittedNetwork := performCloudInitTemplateRequest(
		router,
		http.MethodPut,
		"/utilities/cloud-init/templates/1",
		`{"name":"Template","user":"#cloud-config","meta":"instance-id: one"}`,
	)
	if omittedNetwork.Code != http.StatusBadRequest ||
		!strings.Contains(omittedNetwork.Body.String(), "invalid_request") {
		t.Fatalf("omitted network status=%d body=%s", omittedNetwork.Code, omittedNetwork.Body.String())
	}

	missing := performCloudInitTemplateRequest(
		router,
		http.MethodDelete,
		"/utilities/cloud-init/templates/999",
		"",
	)
	if missing.Code != http.StatusNotFound ||
		!strings.Contains(missing.Body.String(), "cloud_init_template_not_found") {
		t.Fatalf("missing delete status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestCloudInitTemplateHandlerRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newCloudInitTemplateHandlerRouter(t, 128)
	response := performCloudInitTemplateRequest(
		router,
		http.MethodPost,
		"/utilities/cloud-init/templates",
		`{"name":"Template","user":"`+strings.Repeat("x", 256)+`","meta":"metadata","networkConfig":""}`,
	)
	if response.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
