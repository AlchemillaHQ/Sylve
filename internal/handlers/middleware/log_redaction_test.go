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
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type auditTrackingReadCloser struct {
	reader *bytes.Reader
	reads  int
}

func (r *auditTrackingReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (r *auditTrackingReadCloser) Close() error {
	return nil
}

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
		{path: "/api/certificates/2/archive", want: true},
		{path: "/api/utilities/downloads/signed-url", want: true},
		{path: "/api/system/file-explorer/upload", want: true},
		{path: "/api/utilities/downloader-uploads", want: true},
		{path: "/api/utilities/downloader-uploads/id/complete", want: false},
		{path: "/api/zfs/pools", want: false},
	}

	for _, tc := range cases {
		if got := shouldRedactAuditPayload(tc.path); got != tc.want {
			t.Fatalf("path=%s expected=%v got=%v", tc.path, tc.want, got)
		}
	}
}

func TestCertificateArchiveIsAnImportantAuditedGet(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/certificates/2/archive", want: true},
		{path: "/api/certificates/not-a-number/archive", want: true},
		{path: "/api/certificates/archive", want: false},
		{path: "/api/certificates/2/archive/extra", want: false},
		{path: "/api/certificates/domain-check", want: false},
	}

	for _, test := range tests {
		if got := isImportantAuditGetPath(test.path); got != test.want {
			t.Fatalf("path=%q important=%v want=%v", test.path, got, test.want)
		}
	}
}

func TestVMConsoleIsAnImportantAuditedGet(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/vm/107/console", want: true},
		{path: "/api/vm/not-a-rid/console", want: true},
		{path: "/api/vm/console", want: true},
		{path: "/api/vm/107/console/extra", want: false},
		{path: "/api/vm/107/logs", want: false},
	}

	for _, test := range tests {
		if got := isImportantAuditGetPath(test.path); got != test.want {
			t.Fatalf("path=%q important=%v want=%v", test.path, got, test.want)
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

	query = sanitizeAuditQuery("/api/vm/7/console", "auth=secret&baudrate=115200")
	if strings.Contains(query, "secret") {
		t.Fatalf("normalized console query leaked a credential: %s", query)
	}
	if !strings.Contains(query, "baudrate=115200") || !strings.Contains(query, "%5BREDACTED%5D") {
		t.Fatalf("normalized console query lost safe values or redaction: %s", query)
	}
}

func TestSanitizeAuditPayloadRedactsWireGuardKeys(t *testing.T) {
	payload := sanitizeAuditPayload(map[string]interface{}{
		"enabled":      false,
		"privateKey":   "server-private-key",
		"preSharedKey": "peer-preshared-key",
		"psk":          "short-preshared-key",
		"publicKey":    "peer-public-key",
	})

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	result := string(encoded)
	if strings.Contains(result, "server-private-key") ||
		strings.Contains(result, "peer-preshared-key") ||
		strings.Contains(result, "short-preshared-key") {
		t.Fatalf("wireguard key material leaked into audit payload: %s", result)
	}
	if !strings.Contains(result, `"enabled":false`) {
		t.Fatalf("safe state field was not preserved: %s", result)
	}
	if !strings.Contains(result, "peer-public-key") {
		t.Fatalf("public key was unexpectedly redacted: %s", result)
	}
}

func TestSanitizeAuditPayloadForVMCloudInitRedactsOnlyDocuments(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/vm/101/options/cloud-init",
		"/api/vm/options/cloud-init/101",
	} {
		t.Run(path, func(t *testing.T) {
			result, ok := sanitizeAuditPayloadForPath(path, map[string]interface{}{
				"rid":           float64(101),
				"data":          "#cloud-config\npassword: secret",
				"metadata":      "instance-id: vm-101",
				"networkConfig": "version: 2",
				"enabled":       false,
			}).(map[string]interface{})
			if !ok {
				t.Fatalf("expected sanitized map, got %T", result)
			}
			for _, key := range []string{"data", "metadata", "networkConfig"} {
				if result[key] != "[REDACTED]" {
					t.Fatalf("%s was not redacted: %#v", key, result[key])
				}
			}
			if result["rid"] != float64(101) || result["enabled"] != false {
				t.Fatalf("safe VM option fields were not preserved: %#v", result)
			}
		})
	}

	unrelated, ok := sanitizeAuditPayloadForPath("/api/vm/101/options/wol", map[string]interface{}{
		"data":    "safe unrelated value",
		"enabled": false,
	}).(map[string]interface{})
	if !ok || unrelated["data"] != "safe unrelated value" || unrelated["enabled"] != false {
		t.Fatalf("unrelated VM option payload was unexpectedly redacted: %#v", unrelated)
	}
}

func TestUploadAuditDoesNotReadMultipartOrRecordSensitivePayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{
		"/api/system/file-explorer/upload",
		"/api/utilities/downloader-uploads",
	} {
		t.Run(path, func(t *testing.T) {
			auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("UserID", uint(7))
				c.Set("Username", "admin")
				c.Set("AuthType", "sylve")
				c.Next()
			})
			router.Use(RequestLoggerMiddleware(auditDB, nil))

			var receivedBody []byte
			var readsBeforeHandler int
			router.POST(path, func(c *gin.Context) {
				tracked, ok := c.Request.Body.(*auditTrackingReadCloser)
				if !ok {
					t.Fatalf("request body was replaced with %T", c.Request.Body)
				}
				readsBeforeHandler = tracked.reads

				var err error
				receivedBody, err = io.ReadAll(c.Request.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}

				c.JSON(http.StatusCreated, gin.H{
					"data": gin.H{
						"path":     "/private/storage/confidential-disk.raw",
						"uploadId": "opaque-but-private-upload-id",
					},
				})
			})

			var multipartBody bytes.Buffer
			writer := multipart.NewWriter(&multipartBody)
			file, err := writer.CreateFormFile("file", "confidential-disk.raw")
			if err != nil {
				t.Fatalf("create multipart file: %v", err)
			}
			if _, err := file.Write([]byte("TOP SECRET FILE CONTENT")); err != nil {
				t.Fatalf("write multipart file: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close multipart writer: %v", err)
			}
			wantBody := append([]byte(nil), multipartBody.Bytes()...)
			trackedBody := &auditTrackingReadCloser{reader: bytes.NewReader(wantBody)}

			request := httptest.NewRequest(
				http.MethodPost,
				path+"?hash=reusable-query-hash&path=/private/storage",
				nil,
			)
			request.Body = trackedBody
			request.ContentLength = int64(len(wantBody))
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if readsBeforeHandler != 0 {
				t.Fatalf("audit middleware read multipart body %d times before handler", readsBeforeHandler)
			}
			if !bytes.Equal(receivedBody, wantBody) {
				t.Fatal("handler did not receive the original multipart body")
			}

			var records []infoModels.AuditRecord
			if err := auditDB.Find(&records).Error; err != nil {
				t.Fatalf("load audit records: %v", err)
			}
			if len(records) != 1 {
				t.Fatalf("audit record count=%d want=1", len(records))
			}

			var recorded action
			if err := json.Unmarshal([]byte(records[0].Action), &recorded); err != nil {
				t.Fatalf("decode audit action: %v", err)
			}
			if recorded.Method != http.MethodPost || recorded.Path != path {
				t.Fatalf("unexpected audit metadata: %+v", recorded)
			}
			if recorded.Query != "[REDACTED]" ||
				recorded.Body != "[REDACTED]" ||
				recorded.Response != "[REDACTED]" {
				t.Fatalf("upload audit was not metadata-only: %+v", recorded)
			}

			for _, secret := range []string{
				"reusable-query-hash",
				"/private/storage",
				"confidential-disk.raw",
				"TOP SECRET FILE CONTENT",
				"opaque-but-private-upload-id",
			} {
				if strings.Contains(records[0].Action, secret) {
					t.Fatalf("audit action leaked %q: %s", secret, records[0].Action)
				}
			}
		})
	}
}

func TestRedactedJSONAuditDoesNotReadRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(7))
		c.Set("Username", "admin")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(RequestLoggerMiddleware(auditDB, nil))

	wantBody := []byte(`{"hostname":"router.example.com","token":"private-token"}`)
	trackedBody := &auditTrackingReadCloser{reader: bytes.NewReader(wantBody)}
	readsBeforeHandler := -1
	var receivedBody []byte
	router.POST("/api/dynamic-dns/entries", func(c *gin.Context) {
		readsBeforeHandler = trackedBody.reads
		var err error
		receivedBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/api/dynamic-dns/entries", nil)
	request.Body = trackedBody
	request.ContentLength = int64(len(wantBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if readsBeforeHandler != 0 {
		t.Fatalf("audit middleware read sensitive JSON body %d times before handler", readsBeforeHandler)
	}
	if !bytes.Equal(receivedBody, wantBody) {
		t.Fatalf("handler body=%q want=%q", receivedBody, wantBody)
	}

	var record infoModels.AuditRecord
	if err := auditDB.First(&record).Error; err != nil {
		t.Fatalf("load audit record: %v", err)
	}
	if strings.Contains(record.Action, "private-token") || !strings.Contains(record.Action, "[REDACTED]") {
		t.Fatalf("sensitive JSON audit was not redacted: %s", record.Action)
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
	router.GET("/api/certificates/:id/archive", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/zip", []byte("PRIVATE KEY MATERIAL"))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/certificates/2/archive", nil))
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

func TestRequestLoggerReplaysCompleteBodyIncludingTrailingBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(1))
		c.Set("Username", "admin")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(RequestLoggerMiddleware(auditDB, nil))

	wantBody := []byte("{\"name\":\"router\"}\ntrailing-data")
	var receivedBody []byte
	router.POST("/api/network/object", func(c *gin.Context) {
		var err error
		receivedBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/api/network/object", bytes.NewReader(wantBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !bytes.Equal(receivedBody, wantBody) {
		t.Fatalf("handler body=%q want=%q", receivedBody, wantBody)
	}
}

func TestRequestLoggerPreservesBodyLimitErrorForHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(1))
		c.Set("Username", "admin")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(LimitRequestBody(16))
	router.Use(RequestLoggerMiddleware(auditDB, nil))
	router.POST("/api/network/object", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		var tooLarge *http.MaxBytesError
		if !errors.As(err, &tooLarge) {
			c.String(http.StatusBadRequest, "expected MaxBytesError, got %v", err)
			return
		}
		c.Status(http.StatusRequestEntityTooLarge)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/network/object",
		strings.NewReader(`{"name":"body-that-exceeds-the-limit"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
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
