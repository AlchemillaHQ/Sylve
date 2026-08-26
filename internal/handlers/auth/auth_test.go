// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
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

func TestLoginConfigHandlerReturnsPAMDisabledByDefault(t *testing.T) {
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

	if pamEnabled {
		t.Fatalf("expected_pam_disabled")
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
			wantError:   "internal_server_error",
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

func TestWriteLoginServiceErrorClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantRetry  string
	}{
		{name: "invalid credentials", err: errors.New("invalid_credentials"), wantStatus: http.StatusUnauthorized, wantCode: "invalid_credentials"},
		{name: "non admin", err: errors.New("only_admin_allowed"), wantStatus: http.StatusForbidden, wantCode: "only_admin_allowed"},
		{name: "locked", err: errors.New("account_locked"), wantStatus: http.StatusForbidden, wantCode: "account_locked"},
		{name: "PAM unavailable", err: errors.New("pam_auth_error"), wantStatus: http.StatusServiceUnavailable, wantCode: "authentication_service_unavailable"},
		{name: "internal", err: errors.New("token_save_failed: private database detail"), wantStatus: http.StatusInternalServerError, wantCode: "internal_server_error"},
		{
			name:       "rate limited",
			err:        &authService.LoginRateLimitError{RetryAfter: 1500 * time.Millisecond},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "too_many_attempts",
			wantRetry:  "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)

			writeLoginServiceError(ctx, tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			var response internal.APIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != tt.wantCode || response.Error != tt.wantCode {
				t.Fatalf("response=%+v want code %q", response, tt.wantCode)
			}
			if got := recorder.Header().Get("Retry-After"); got != tt.wantRetry {
				t.Fatalf("Retry-After=%q want=%q", got, tt.wantRetry)
			}
		})
	}
}

func TestLogoutHandlerRevokesLocalTokenIdempotently(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Token{})
	service := &authService.Service{DB: db}
	user := models.User{Username: "admin", Admin: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&models.Token{
		UserID:   user.ID,
		Token:    "local-token",
		AuthType: "sylve",
		Expiry:   time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
		ctx.Set("AuthScope", "local")
		ctx.Set("Token", "local-token")

		LogoutHandler(service)(ctx)

		if recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d: status=%d body=%s", attempt+1, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("attempt %d: Cache-Control=%q", attempt+1, got)
		}
	}

	var count int64
	if err := db.Model(&models.Token{}).Where("token = ?", "local-token").Count(&count).Error; err != nil {
		t.Fatalf("count token: %v", err)
	}
	if count != 0 {
		t.Fatalf("token count=%d want=0", count)
	}
}

func TestLogoutHandlerRejectsClusterScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Token{})
	service := &authService.Service{DB: db}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	ctx.Set("AuthScope", "cluster")
	ctx.Set("Token", "cluster-token")

	LogoutHandler(service)(ctx)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestLoginHandlerRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(authService.MaxRequestBodyBytes))
	router.POST("/api/auth/login", LoginHandler(nil))

	payload := append([]byte(`{"username":"`), bytes.Repeat([]byte("a"), int(authService.MaxRequestBodyBytes))...)
	payload = append(payload, []byte(`","password":"secret","authType":"sylve"}`)...)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q want=no-store", response.Header().Get("Cache-Control"))
	}
}
