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

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/gin-gonic/gin"
)

func TestUtilitiesJSONHandlersRejectOversizedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &utilities.Service{}
	router := gin.New()
	router.Use(middleware.LimitRequestBody(64))
	router.POST("/downloads", DownloadFile(service))
	router.PATCH("/downloads/:id", UpdateDownload(service))
	router.POST("/downloads/bulk-delete", BulkDeleteDownload(service))
	router.POST("/downloads/signed-url", GetSignedDownloadURL(service))
	router.POST("/downloader-uploads/:id/complete", CompleteDownloaderUpload(service))

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/downloads"},
		{method: http.MethodPatch, path: "/downloads/1"},
		{method: http.MethodPost, path: "/downloads/bulk-delete"},
		{method: http.MethodPost, path: "/downloads/signed-url"},
		{method: http.MethodPost, path: "/downloader-uploads/test/complete"},
	}
	body := `{"padding":"` + strings.Repeat("x", 256) + `"}`
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var payload internal.APIResponse[any]
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Message != "request_too_large" || payload.Error != "request_too_large" {
				t.Fatalf("unexpected response: %+v", payload)
			}
		})
	}
}

func TestUtilitiesJSONBindingErrorsAreStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/downloads", DownloadFile(&utilities.Service{}))
	request := httptest.NewRequest(http.MethodPost, "/downloads", strings.NewReader(`{"url":`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != "invalid_request" || payload.Error != "invalid_request" {
		t.Fatalf("unexpected response: %+v", payload)
	}
}
