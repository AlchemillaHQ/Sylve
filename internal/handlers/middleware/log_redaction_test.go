// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestShouldRedactAuditPayload(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{path: "/api/auth/login", want: true},
		{path: "/api/cluster", want: true},
		{path: "/api/cluster/join", want: true},
		{path: "/api/cluster/backups/jobs", want: false},
		{path: "/api/dynamic-dns/entries", want: true},
		{path: "/api/certificates", want: true},
		{path: "/api/certificates/2/activate", want: true},
		{path: "/api/certificates/2/download", want: true},
		{path: "/api/utilities/downloads/signed-url", want: true},
		{path: "/api/zfs/pools", want: false},
	}

	for _, tc := range cases {
		if got := shouldRedactAuditPayload(tc.path); got != tc.want {
			t.Fatalf("path=%s expected=%v got=%v", tc.path, tc.want, got)
		}
	}
}

func TestSanitizeAuditQuery(t *testing.T) {
	query := sanitizeAuditQuery("/api/vm/console", "auth=secret&hash=hidden&rid=7")
	if strings.Contains(query, "secret") || strings.Contains(query, "hidden") {
		t.Fatalf("query leaked a credential: %s", query)
	}
	if !strings.Contains(query, "rid=7") || !strings.Contains(query, "%5BREDACTED%5D") {
		t.Fatalf("query did not preserve safe values and redact credentials: %s", query)
	}
	if got := sanitizeAuditQuery("/api/certificates/domain-check", "domain=secret.example"); got != "[REDACTED]" {
		t.Fatalf("sensitive path query=%q", got)
	}
	if got := sanitizeAuditQuery("/api/vm/console", "%zz"); got != "[REDACTED]" {
		t.Fatalf("malformed query=%q", got)
	}
}

func TestBodyWriterCanSkipSensitiveResponseCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	buffer := bytes.NewBuffer(nil)
	writer := bodyWriter{ResponseWriter: context.Writer, body: buffer, capture: false}
	payload := []byte("PRIVATE KEY MATERIAL")

	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if response.Body.String() != string(payload) {
		t.Fatalf("client response=%q", response.Body.String())
	}
	if buffer.Len() != 0 {
		t.Fatalf("sensitive response was captured: %q", buffer.String())
	}
}

func TestCertificateDownloadAuditDoesNotCaptureResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(1))
		c.Set("Username", "admin")
		c.Set("AuthType", "local")
		c.Next()
	})
	router.Use(RequestLoggerMiddleware(auditDB, nil))
	router.POST("/api/certificates/:id/download", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/zip", []byte("PRIVATE KEY MATERIAL"))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/certificates/2/download", nil))
	if response.Code != http.StatusOK || response.Body.String() != "PRIVATE KEY MATERIAL" {
		t.Fatalf("unexpected client response: status=%d body=%q", response.Code, response.Body.String())
	}

	var record infoModels.AuditRecord
	if err := auditDB.First(&record).Error; err != nil {
		t.Fatalf("load audit record: %v", err)
	}
	if strings.Contains(record.Action, "PRIVATE KEY MATERIAL") {
		t.Fatalf("audit record leaked response body: %s", record.Action)
	}
	if !strings.Contains(record.Action, "[REDACTED]") {
		t.Fatalf("audit record was not redacted: %s", record.Action)
	}
}

func TestCertificateBodyLimitPreservesRequestForHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(1))
		c.Set("Username", "admin")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(LimitRequestBody(1024))
	router.Use(RequestLoggerMiddleware(auditDB, nil))
	router.POST("/api/certificates", func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		if body.Name != "dashboard" {
			c.String(http.StatusBadRequest, "unexpected body")
			return
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/api/certificates", strings.NewReader(`{"name":"dashboard"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSanitizeAuditPayloadNested(t *testing.T) {
	input := map[string]interface{}{
		"username": "admin",
		"password": "super-secret",
		"nested": map[string]interface{}{
			"token": "abc",
			"safe":  "ok",
		},
		"array": []interface{}{
			map[string]interface{}{"clusterKey": "k1"},
			map[string]interface{}{"encryptionKey": "restore-passphrase"},
			map[string]interface{}{"value": "keep"},
		},
	}

	outAny := sanitizeAuditPayload(input)
	out, ok := outAny.(map[string]interface{})
	if !ok {
		t.Fatal("expected_map_output")
	}

	if out["password"] != "[REDACTED]" {
		t.Fatal("expected_password_to_be_redacted")
	}

	nested, ok := out["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("expected_nested_map")
	}
	if nested["token"] != "[REDACTED]" {
		t.Fatal("expected_nested_token_to_be_redacted")
	}
	if nested["safe"] != "ok" {
		t.Fatal("expected_safe_nested_field_to_be_preserved")
	}

	arr, ok := out["array"].([]interface{})
	if !ok || len(arr) != 3 {
		t.Fatal("expected_three_array_entries")
	}
	firstMap, ok := arr[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected_first_array_entry_map")
	}
	if firstMap["clusterKey"] != "[REDACTED]" {
		t.Fatal("expected_cluster_key_to_be_redacted")
	}
	secondMap, ok := arr[1].(map[string]interface{})
	if !ok || secondMap["encryptionKey"] != "[REDACTED]" {
		t.Fatal("expected_encryption_key_to_be_redacted")
	}
	thirdMap, ok := arr[2].(map[string]interface{})
	if !ok || thirdMap["value"] != "keep" {
		t.Fatal("expected_safe_array_value_to_be_preserved")
	}
}
