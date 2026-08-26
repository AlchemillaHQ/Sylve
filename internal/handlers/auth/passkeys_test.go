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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/gin-gonic/gin"
)

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

func TestIsSecureRequestAllowsConfiguredTrustedProxy(t *testing.T) {
	config.ParsedConfig = &internal.SylveConfig{
		TrustedProxies: []string{"10.10.30.0/24"},
	}
	defer func() { config.ParsedConfig = nil }()

	c, _ := newPasskeyTestContext("10.10.30.103:44321")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if !isSecureRequest(c) {
		t.Fatalf("expected_request_to_be_secure_via_trusted_proxy")
	}
}

func TestIsSecureRequestRejectsNonConfiguredTrustedProxy(t *testing.T) {
	config.ParsedConfig = &internal.SylveConfig{
		TrustedProxies: []string{"10.10.30.0/24"},
	}
	defer func() { config.ParsedConfig = nil }()

	c, _ := newPasskeyTestContext("192.168.1.1:44321")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if isSecureRequest(c) {
		t.Fatalf("expected_request_to_be_insecure_from_unknown_proxy")
	}
}

func TestIsSecureRequestAllowsTrustedProxySingleIP(t *testing.T) {
	config.ParsedConfig = &internal.SylveConfig{
		TrustedProxies: []string{"10.10.30.103"},
	}
	defer func() { config.ParsedConfig = nil }()

	c, _ := newPasskeyTestContext("10.10.30.103:44321")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if !isSecureRequest(c) {
		t.Fatalf("expected_request_to_be_secure_via_trusted_proxy_ip")
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

func TestClassifyPasskeyManagementError(t *testing.T) {
	tests := []struct {
		code       string
		wantStatus int
		wantCode   string
	}{
		{code: "invalid_user_id", wantStatus: http.StatusBadRequest, wantCode: "invalid_user_id"},
		{code: "passkey_registration_not_allowed", wantStatus: http.StatusForbidden, wantCode: "passkey_registration_not_allowed"},
		{code: "user_not_found", wantStatus: http.StatusNotFound, wantCode: "user_not_found"},
		{code: "credential_not_found", wantStatus: http.StatusNotFound, wantCode: "credential_not_found"},
		{code: "challenge_used", wantStatus: http.StatusConflict, wantCode: "challenge_used"},
		{code: "credential_already_registered", wantStatus: http.StatusConflict, wantCode: "credential_already_registered"},
		{code: "failed_to_load_user", wantStatus: http.StatusInternalServerError, wantCode: "internal_server_error"},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			status, code := classifyPasskeyManagementError(errors.New(tt.code))
			if status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("expected (%d, %s), got (%d, %s)", tt.wantStatus, tt.wantCode, status, code)
			}
		})
	}
}
