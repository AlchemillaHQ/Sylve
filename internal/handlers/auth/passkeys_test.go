// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newPasskeyHandlerTestAuthService(t *testing.T) *auth.Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &models.User{})

	return &auth.Service{DB: db}
}

func newPasskeyTestContext(remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest("POST", "http://example.test/api/auth/passkeys/login/begin", nil)
	req.RemoteAddr = remoteAddr
	c.Request = req

	return c, rec
}

func TestIsSecureRequestRejectsForwardedProtoFromUntrustedRemote(t *testing.T) {
	c, _ := newPasskeyTestContext("8.8.8.8:44321")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if isSecureRequest(c) {
		t.Fatalf("expected_request_to_be_insecure")
	}
}

func TestIsSecureRequestAllowsForwardedProtoFromTrustedRemote(t *testing.T) {
	c, _ := newPasskeyTestContext("127.0.0.1:44321")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if !isSecureRequest(c) {
		t.Fatalf("expected_request_to_be_secure")
	}
}

func TestIsSecureRequestAllowsDirectTLS(t *testing.T) {
	c, _ := newPasskeyTestContext("8.8.8.8:44321")
	c.Request.TLS = &tls.ConnectionState{}

	if !isSecureRequest(c) {
		t.Fatalf("expected_tls_request_to_be_secure")
	}
}

func TestGetPasskeyRelyingPartyIgnoresForwardedHostFromUntrustedRemote(t *testing.T) {
	c, _ := newPasskeyTestContext("8.8.8.8:44321")
	c.Request.TLS = &tls.ConnectionState{}
	c.Request.Host = "sylve.example.com:9443"
	c.Request.Header.Set("X-Forwarded-Host", "evil.example.com")

	rpID, origin, err := getPasskeyRelyingParty(c)
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}

	if rpID != "sylve.example.com" {
		t.Fatalf("expected_rpid_sylve_example_com_got: %s", rpID)
	}

	if origin != "https://sylve.example.com:9443" {
		t.Fatalf("expected_origin_https://sylve.example.com:9443_got: %s", origin)
	}
}

func TestRequirePasskeyManagementAccessRejectsPamRealm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authService := newPasskeyHandlerTestAuthService(t)
	c, rec := newPasskeyTestContext("127.0.0.1:12345")
	c.Set("AuthType", "pam")
	c.Set("UserID", uint(1))

	allowed := requirePasskeyManagementAccess(c, authService)
	if allowed {
		t.Fatalf("expected_access_denied")
	}

	if rec.Code != 403 {
		t.Fatalf("expected_403_got: %d", rec.Code)
	}
}

func TestRequirePasskeyManagementAccessRejectsNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authService := newPasskeyHandlerTestAuthService(t)
	if err := authService.DB.Create(&models.User{
		ID:       1,
		Username: "user",
		Admin:    false,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	c, rec := newPasskeyTestContext("127.0.0.1:12345")
	c.Set("AuthType", "sylve")
	c.Set("UserID", uint(1))

	allowed := requirePasskeyManagementAccess(c, authService)
	if allowed {
		t.Fatalf("expected_access_denied")
	}

	if rec.Code != 403 {
		t.Fatalf("expected_403_got: %d", rec.Code)
	}
}

func TestRequirePasskeyManagementAccessAllowsSylveAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authService := newPasskeyHandlerTestAuthService(t)
	if err := authService.DB.Create(&models.User{
		ID:       1,
		Username: "admin",
		Admin:    true,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	c, rec := newPasskeyTestContext("127.0.0.1:12345")
	c.Set("AuthType", auth.AuthTypeSylvePasskey)
	c.Set("UserID", uint(1))

	allowed := requirePasskeyManagementAccess(c, authService)
	if !allowed {
		t.Fatalf("expected_access_allowed")
	}

	if rec.Code != 200 {
		t.Fatalf("expected_200_got: %d", rec.Code)
	}
}
