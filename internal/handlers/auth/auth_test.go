// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func runLoginConfigHandler(t *testing.T) map[string]any {
	t.Helper()

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/auth/login/config", nil)

	LoginConfigHandler()(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected_200_got: %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed_to_decode_response: %v", err)
	}

	return payload
}

func TestLoginConfigHandlerReturnsPAMDisabledFromConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{
		Auth: internal.AuthConfig{
			EnablePAM: false,
		},
	}
	t.Cleanup(func() {
		config.ParsedConfig = originalConfig
	})

	payload := runLoginConfigHandler(t)
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected_data_object")
	}

	pamEnabled, ok := data["pamEnabled"].(bool)
	if !ok {
		t.Fatalf("expected_pam_enabled_bool")
	}

	if pamEnabled {
		t.Fatalf("expected_pam_disabled")
	}
}

func TestLoginConfigHandlerReturnsPAMEnabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalConfig := config.ParsedConfig
	config.ParsedConfig = nil
	t.Cleanup(func() {
		config.ParsedConfig = originalConfig
	})

	payload := runLoginConfigHandler(t)
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected_data_object")
	}

	pamEnabled, ok := data["pamEnabled"].(bool)
	if !ok {
		t.Fatalf("expected_pam_enabled_bool")
	}

	if !pamEnabled {
		t.Fatalf("expected_pam_enabled")
	}
}

func TestWriteLoginHostnameError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
		wantError   string
	}{
		{
			name:        "hostname not configured",
			err:         utils.ErrSystemHostnameNotConfigured,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "system_hostname_not_configured",
			wantError:   "system_hostname_not_configured",
		},
		{
			name:        "hostname lookup failed",
			err:         errors.New("hostname lookup failed"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "internal_server_error",
			wantError:   "hostname lookup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)

			writeLoginHostnameError(ctx, tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			var response internal.APIResponse[any]
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed_to_decode_response: %v", err)
			}
			if response.Message != tt.wantMessage {
				t.Fatalf("expected message %q, got %q", tt.wantMessage, response.Message)
			}
			if response.Error != tt.wantError {
				t.Fatalf("expected error %q, got %q", tt.wantError, response.Error)
			}
		})
	}
}
