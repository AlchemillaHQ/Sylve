// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	migrationHandlers "github.com/alchemillahq/sylve/internal/handlers/migration"

	"github.com/gin-gonic/gin"
)

func TestBindJailJSONMapsMalformedAndOversizedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "malformed",
			body:        `{"name":`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "invalid_request_data",
		},
		{
			name:        "oversized",
			body:        `{"name":"body-that-exceeds-the-limit"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantMessage: "request_body_too_large",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			handlerCalled := false
			router := gin.New()
			router.Use(middleware.LimitRequestBody(16))
			router.POST("/api/jail", func(c *gin.Context) {
				var request struct {
					Name string `json:"name"`
				}
				if !bindJailJSON(c, &request, "invalid_request_data") {
					return
				}
				handlerCalled = true
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, "/api/jail", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if handlerCalled {
				t.Fatal("handler continued after JSON binding failure")
			}
			var envelope internal.APIResponse[any]
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Status != "error" || envelope.Message != test.wantMessage || envelope.Data != nil {
				t.Fatalf("unexpected response envelope: %#v", envelope)
			}
		})
	}
}

func TestMigrateJailMapsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(16))
	router.POST("/api/jail/:ctid/migrations", migrationHandlers.MigrateJail(nil, nil))

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/jail/101/migrations",
		strings.NewReader(`{"targetNodeUuid":"node-that-exceeds-the-limit"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	var envelope internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Status != "error" || envelope.Message != "request_body_too_large" || envelope.Data != nil {
		t.Fatalf("unexpected response envelope: %#v", envelope)
	}
}
