// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/rs/zerolog"
)

func captureAuthLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var output bytes.Buffer
	previous := logger.L
	logger.L = zerolog.New(&output)
	t.Cleanup(func() {
		logger.L = previous
	})

	return &output
}

func createLoginTestUser(t *testing.T, service *Service, username, password string, admin bool) models.User {
	t.Helper()

	hashed, err := utils.HashPassword(password)
	if err != nil {
		t.Fatalf("failed_to_hash_test_password: %v", err)
	}
	user := models.User{
		Username: username,
		Password: hashed,
		Admin:    admin,
		Source:   "local",
	}
	if err := service.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_create_test_user: %v", err)
	}
	return user
}

func requireLogField(t *testing.T, output, field, value string) {
	t.Helper()

	want := `"` + field + `":"` + value + `"`
	if !strings.Contains(output, want) {
		t.Fatalf("expected log field %s=%s, got: %s", field, value, output)
	}
}

func TestCreateJWTLogsLocalFailureReasons(t *testing.T) {
	tests := []struct {
		name           string
		username       string
		password       string
		authType       string
		seedPassword   string
		seedAdmin      bool
		expectedError  string
		expectedReason string
	}{
		{
			name:           "user not found",
			username:       "missing-user",
			password:       "submitted-secret",
			authType:       "sylve",
			expectedError:  "invalid_credentials",
			expectedReason: "user_not_found",
		},
		{
			name:           "password mismatch",
			username:       "admin",
			password:       "submitted-secret",
			authType:       "sylve",
			seedPassword:   "stored-secret",
			seedAdmin:      true,
			expectedError:  "invalid_credentials",
			expectedReason: "password_mismatch",
		},
		{
			name:           "admin required",
			username:       "operator",
			password:       "matching-secret",
			authType:       "sylve",
			seedPassword:   "matching-secret",
			seedAdmin:      false,
			expectedError:  "only_admin_allowed",
			expectedReason: "admin_required",
		},
		{
			name:           "invalid authentication type",
			username:       "admin",
			password:       "submitted-secret",
			authType:       "unknown",
			expectedError:  "invalid_auth_type",
			expectedReason: "invalid_auth_type",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs := captureAuthLogs(t)
			service := newAuthTestService(t)
			storedHash := ""
			if test.seedPassword != "" {
				user := createLoginTestUser(t, service, test.username, test.seedPassword, test.seedAdmin)
				storedHash = user.Password
			}

			_, _, err := service.CreateJWT(test.username, test.password, test.authType, false)
			if err == nil || err.Error() != test.expectedError {
				t.Fatalf("expected %s, got: %v", test.expectedError, err)
			}

			output := logs.String()
			requireLogField(t, output, "reason", test.expectedReason)
			requireLogField(t, output, "username", test.username)
			requireLogField(t, output, "auth_type", test.authType)
			if strings.Contains(output, test.password) {
				t.Fatalf("submitted password was written to logs: %s", output)
			}
			if storedHash != "" && strings.Contains(output, storedHash) {
				t.Fatalf("stored password hash was written to logs: %s", output)
			}
		})
	}
}

func TestCreateJWTLogsRateLimitState(t *testing.T) {
	logs := captureAuthLogs(t)
	service := newAuthTestService(t)
	createLoginTestUser(t, service, "admin", "stored-secret", true)

	for i := 0; i < maxLoginAttempts; i++ {
		_, _, _ = service.CreateJWT("admin", "wrong-secret", "sylve", false)
	}
	_, _, err := service.CreateJWT("admin", "wrong-secret", "sylve", false)
	if err == nil || !strings.HasPrefix(err.Error(), "too_many_attempts") {
		t.Fatalf("expected rate-limit error, got: %v", err)
	}

	output := logs.String()
	requireLogField(t, output, "reason", "rate_limited")
	if !strings.Contains(output, `"failed_attempts":5`) {
		t.Fatalf("expected failed attempt count in logs, got: %s", output)
	}
	if !strings.Contains(output, `"blocked_until"`) {
		t.Fatalf("expected block expiry in logs, got: %s", output)
	}
}

func TestCreateJWTLogsSuccessWithoutSecrets(t *testing.T) {
	logs := captureAuthLogs(t)
	service := newAuthTestService(t)
	const password = "successful-login-secret"
	user := createLoginTestUser(t, service, "admin", password, true)
	if err := service.DB.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "jwt-signing-secret"}).Error; err != nil {
		t.Fatalf("failed_to_create_jwt_secret: %v", err)
	}

	userID, token, err := service.CreateJWT("admin", password, "sylve", true)
	if err != nil {
		t.Fatalf("expected successful login, got: %v", err)
	}
	if userID != user.ID || token == "" {
		t.Fatalf("unexpected successful login result: user=%d token_empty=%t", userID, token == "")
	}

	output := logs.String()
	if !strings.Contains(output, `"message":"authentication_succeeded"`) {
		t.Fatalf("expected success log, got: %s", output)
	}
	if strings.Contains(output, password) || strings.Contains(output, user.Password) || strings.Contains(output, token) {
		t.Fatalf("authentication secret was written to logs: %s", output)
	}
}

func TestCreateJWTLogsTokenIssueFailure(t *testing.T) {
	logs := captureAuthLogs(t)
	service := newAuthTestService(t)
	createLoginTestUser(t, service, "admin", "matching-secret", true)

	_, _, err := service.CreateJWT("admin", "matching-secret", "sylve", false)
	if err == nil || err.Error() != "jwt_secret_not_found" {
		t.Fatalf("expected jwt_secret_not_found, got: %v", err)
	}

	output := logs.String()
	requireLogField(t, output, "reason", "jwt_issue_failed")
	if !strings.Contains(output, `"level":"error"`) {
		t.Fatalf("expected error-level token failure log, got: %s", output)
	}
}

func TestCreateJWTLogsPAMDisabled(t *testing.T) {
	logs := captureAuthLogs(t)
	service := newAuthTestService(t)

	previousConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{Auth: internal.AuthConfig{EnablePAM: false}}
	t.Cleanup(func() {
		config.ParsedConfig = previousConfig
	})

	_, _, err := service.CreateJWT("root", "pam-secret", "pam", false)
	if err == nil || err.Error() != "pam_auth_disabled" {
		t.Fatalf("expected pam_auth_disabled, got: %v", err)
	}

	requireLogField(t, logs.String(), "reason", "pam_disabled")
}

func TestRecordFailedLoginReturnsBlockedSnapshot(t *testing.T) {
	service := &Service{}
	var attempt loginAttempt
	for i := 0; i < maxLoginAttempts; i++ {
		attempt = service.recordFailedLogin("admin")
	}

	if attempt.count != maxLoginAttempts {
		t.Fatalf("expected %d attempts, got: %d", maxLoginAttempts, attempt.count)
	}
	if attempt.blockedUntil.Before(time.Now()) {
		t.Fatalf("expected future block expiry, got: %s", attempt.blockedUntil)
	}
}
