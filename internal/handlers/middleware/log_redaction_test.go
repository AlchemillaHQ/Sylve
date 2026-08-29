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
		{path: "/api/auth/passkeys/login/begin", want: true},
		{path: "/api/auth/passkeys/register/begin", want: false},
		{path: "/api/auth/passkeys/register/finish", want: true},
		{path: "/api/cluster", want: true},
		{path: "/api/cluster/join", want: true},
		{path: "/api/cluster/remove-node", want: false},
		{path: "/api/cluster/remove-node/force", want: false},
		{path: "/api/cluster/reset-node", want: false},
		{path: "/api/cluster/reset-node/force", want: false},
		{path: "/api/cluster/remove-node/force/extra", want: true},
		{path: "/api/cluster/backups/jobs", want: false},
		{path: "/api/dynamic-dns/entries", want: true},
		{path: "/api/certificates", want: true},
		{path: "/api/certificates/2/activate", want: true},
		{path: "/api/certificates/2/archive", want: true},
		{path: "/api/utilities/downloads/signed-url", want: false},
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

func TestClusterLifecycleAuditSanitizesOnlySecrets(t *testing.T) {
	payload := sanitizeAuditPayloadForPath(
		"/api/cluster/remove-node/force",
		map[string]interface{}{
			"nodeId":                 "node-2",
			"leaveId":                "leave-1",
			"phase":                  "removing",
			"targetExternallyFenced": true,
			"clusterKey":             "secret-key",
			"nested": map[string]interface{}{
				"kind":  "backup_job",
				"id":    "7",
				"role":  "runner",
				"state": "running",
				"token": "secret-token",
			},
		},
	)
	result, ok := payload.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected payload type: %T", payload)
	}
	if result["nodeId"] != "node-2" || result["leaveId"] != "leave-1" || result["phase"] != "removing" {
		t.Fatalf("safe lifecycle fields were lost: %#v", result)
	}
	if result["clusterKey"] != "[REDACTED]" {
		t.Fatalf("cluster key was not redacted: %#v", result)
	}
	nested, ok := result["nested"].(map[string]interface{})
	if !ok || nested["kind"] != "backup_job" || nested["token"] != "[REDACTED]" {
		t.Fatalf("nested lifecycle fields were not sanitized: %#v", result)
	}
}

func TestPasskeyRegistrationAuditKeepsOnlySafeManagementIdentity(t *testing.T) {
	if !shouldRedactAuditResponse("/api/auth/passkeys/register/begin") {
		t.Fatal("registration challenge response must be redacted")
	}
	if shouldRedactAuditResponse("/api/auth/passkeys/register/finish") {
		t.Fatal("safe registration result should remain available to audit presentation")
	}

	beginBody := sanitizeAuditPayloadForPath(
		"/api/auth/passkeys/register/begin",
		map[string]interface{}{"userId": float64(7)},
	)
	beginMap, ok := beginBody.(map[string]interface{})
	if !ok || beginMap["userId"] != float64(7) {
		t.Fatalf("registration begin lost safe user identity: %+v", beginBody)
	}

	finishResponse := sanitizeAuditResponseForPath(
		"/api/auth/passkeys/register/finish",
		map[string]interface{}{
			"status":  "success",
			"message": "passkey_registered_successfully",
			"error":   "",
			"data": map[string]interface{}{
				"userId":       float64(7),
				"credentialId": "Y3JlZGVudGlhbA",
				"label":        "Laptop",
				"credential":   "must-not-be-stored",
			},
		},
	)
	responseMap, ok := finishResponse.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected passkey response type: %T", finishResponse)
	}
	data, ok := responseMap["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected passkey response data: %+v", responseMap)
	}
	if data["userId"] != float64(7) || data["credentialId"] != "Y3JlZGVudGlhbA" || data["label"] != "Laptop" {
		t.Fatalf("safe passkey identity was not retained: %+v", data)
	}
	if data["credential"] != "[REDACTED]" {
		t.Fatalf("raw credential was retained: %+v", data)
	}
}

func TestSignedDownloadAuditKeepsIdentityAndRedactsCapability(t *testing.T) {
	body := sanitizeAuditPayloadForPath(
		"/api/utilities/downloads/signed-url",
		map[string]interface{}{
			"name":       "installer.iso",
			"parentUUID": "parent-uuid",
			"sig":        "must-not-be-stored",
			"url":        "must-not-be-stored",
		},
	)
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected body type: %T", body)
	}
	if bodyMap["name"] != "installer.iso" || bodyMap["parentUUID"] != "parent-uuid" {
		t.Fatalf("safe identity omitted: %+v", bodyMap)
	}
	if _, exists := bodyMap["sig"]; exists {
		t.Fatalf("signature retained: %+v", bodyMap)
	}
	if _, exists := bodyMap["url"]; exists {
		t.Fatalf("capability URL retained: %+v", bodyMap)
	}

	response := sanitizeAuditResponseForPath(
		"/api/utilities/downloads/signed-url",
		map[string]interface{}{
			"status":  "success",
			"message": "signed_url_generated",
			"error":   "",
			"data": map[string]interface{}{
				"url":       "/api/utilities/downloads/id?sig=must-not-be-stored",
				"expiresAt": "2026-08-10T10:00:00Z",
			},
		},
	)
	responseMap, ok := response.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected response type: %T", response)
	}
	data, ok := responseMap["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected response data: %+v", responseMap)
	}
	if data["url"] != "[REDACTED]" || data["expiresAt"] != "2026-08-10T10:00:00Z" {
		t.Fatalf("unexpected sanitized response: %+v", responseMap)
	}
}

func TestCloudInitTemplateAuditKeepsIdentityAndRedactsDocuments(t *testing.T) {
	path := "/api/utilities/cloud-init/templates/7"
	body, ok := sanitizeAuditPayloadForPath(path, map[string]interface{}{
		"name":          "Base Template",
		"user":          "#cloud-config\npassword: private",
		"meta":          "instance-id: private",
		"networkConfig": "version: 2",
	}).(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected body type: %T", body)
	}
	if body["name"] != "Base Template" {
		t.Fatalf("template identity was lost: %+v", body)
	}
	for _, key := range []string{"user", "meta", "networkConfig"} {
		if body[key] != "[REDACTED]" {
			t.Fatalf("%s was not redacted: %+v", key, body)
		}
	}

	response, ok := sanitizeAuditResponseForPath(path, map[string]interface{}{
		"status":  "success",
		"message": "template_edited",
		"error":   "",
		"data": map[string]interface{}{
			"id":            float64(7),
			"name":          "Base Template",
			"user":          "private user data",
			"meta":          "private metadata",
			"networkConfig": "private network data",
		},
	}).(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected response type: %T", response)
	}
	data, ok := response["data"].(map[string]interface{})
	if !ok || data["id"] != float64(7) || data["name"] != "Base Template" {
		t.Fatalf("response identity was lost: %+v", response)
	}
	for _, key := range []string{"user", "meta", "networkConfig"} {
		if data[key] != "[REDACTED]" {
			t.Fatalf("response %s was not redacted: %+v", key, data)
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

func TestJailConsoleIsAnImportantAuditedGet(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/jail/107/console", want: true},
		{path: "/api/jail/not-a-ctid/console", want: true},
		{path: "/api/jail/console", want: false},
		{path: "/api/jail/107/console/extra", want: false},
		{path: "/api/jail/107/logs", want: false},
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

	query = sanitizeAuditQuery("/api/jail/107/console", "auth=secret")
	if strings.Contains(query, "secret") || !strings.Contains(query, "%5BREDACTED%5D") {
		t.Fatalf("jail console query leaked a credential: %s", query)
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

func TestSanitizeDownloadCreateAuditPayloadRedactsSourceURL(t *testing.T) {
	result, ok := sanitizeAuditPayloadForPath(
		"/api/utilities/downloads",
		map[string]interface{}{
			"url":                    "https://user:password@example.test/private.img?token=secret",
			"filename":               "private.img",
			"downloadType":           "uncategorized",
			"automaticExtraction":    false,
			"automaticRawConversion": false,
		},
	).(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized map, got %T", result)
	}
	if result["url"] != "[REDACTED]" {
		t.Fatalf("download source URL was not redacted: %#v", result)
	}
	if result["filename"] != "private.img" || result["downloadType"] != "uncategorized" {
		t.Fatalf("safe download metadata was not preserved: %#v", result)
	}
}

func TestSanitizeDownloadAuditResponseRedactsSourceURL(t *testing.T) {
	result, ok := sanitizeAuditResponseForPath(
		"/api/utilities/downloads/42",
		map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"id":    float64(42),
				"name":  "private.img",
				"type":  "http",
				"uType": "uncategorized",
				"url":   "https://user:password@example.test/private.img?token=secret",
			},
		},
	).(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized response map, got %T", result)
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok || data["url"] != "[REDACTED]" {
		t.Fatalf("download response URL was not redacted: %#v", result)
	}
	if data["id"] != float64(42) || data["name"] != "private.img" || data["type"] != "http" {
		t.Fatalf("safe download response identity was not preserved: %#v", data)
	}
}

func TestSambaExtraGlobalConfigIsRedactedFromAuditPayloads(t *testing.T) {
	t.Parallel()

	request, ok := sanitizeAuditPayloadForPath(
		"/api/samba/config",
		map[string]interface{}{
			"workgroup":         "WORKGROUP",
			"extraGlobalConfig": "include = /private/smb.conf",
		},
	).(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized request map, got %T", request)
	}
	if request["extraGlobalConfig"] != "[REDACTED]" {
		t.Fatalf("extra global config was not redacted from request: %#v", request)
	}
	if request["workgroup"] != "WORKGROUP" {
		t.Fatalf("safe Samba setting was not preserved: %#v", request)
	}

	response, ok := sanitizeAuditResponseForPath(
		"/api/samba/config",
		map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"workgroup":         "WORKGROUP",
				"extraGlobalConfig": "include = /private/smb.conf",
			},
		},
	).(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized response map, got %T", response)
	}
	data, ok := response["data"].(map[string]interface{})
	if !ok || data["extraGlobalConfig"] != "[REDACTED]" {
		t.Fatalf("extra global config was not redacted from response: %#v", response)
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

func TestSanitizeAuditPayloadForJailOptions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		path  string
		field string
	}{
		{path: "/api/jail/101/options/fstab", field: "fstab"},
		{path: "/api/jail/101/options/resolv-conf", field: "resolvConf"},
		{path: "/api/jail/101/options/devfs-rules", field: "devFSRules"},
		{path: "/api/jail/101/options/additional-options", field: "additionalOptions"},
		{path: "/api/jail/101/options/metadata", field: "metadata"},
	} {
		t.Run(test.path, func(t *testing.T) {
			result, ok := sanitizeAuditPayloadForPath(test.path, map[string]interface{}{
				test.field: "private jail configuration",
				"enabled":  false,
			}).(map[string]interface{})
			if !ok {
				t.Fatalf("expected sanitized map, got %T", result)
			}
			if result[test.field] != "[REDACTED]" {
				t.Fatalf("%s was not redacted: %#v", test.field, result)
			}
			if result["enabled"] != false {
				t.Fatalf("safe field was not preserved: %#v", result)
			}
		})
	}

	metadata, ok := sanitizeAuditPayloadForPath(
		"/api/jail/101/options/metadata",
		map[string]interface{}{"metadata": "private-meta", "env": "private-env"},
	).(map[string]interface{})
	if !ok || metadata["metadata"] != "[REDACTED]" || metadata["env"] != "[REDACTED]" {
		t.Fatalf("metadata values were not redacted: %#v", metadata)
	}

	lifecycle, ok := sanitizeAuditPayloadForPath(
		"/api/jail/101/options/lifecycle-hooks",
		map[string]interface{}{
			"hooks": map[string]interface{}{
				"prestart": map[string]interface{}{"enabled": true, "script": "private command"},
				"stop":     map[string]interface{}{"enabled": false, "script": ""},
			},
		},
	).(map[string]interface{})
	if !ok {
		t.Fatalf("expected lifecycle map, got %T", lifecycle)
	}
	hooks, ok := lifecycle["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hooks map, got %#v", lifecycle["hooks"])
	}
	prestart, ok := hooks["prestart"].(map[string]interface{})
	if !ok || prestart["script"] != "[REDACTED]" || prestart["enabled"] != true {
		t.Fatalf("lifecycle script redaction lost safe fields: %#v", hooks)
	}

	safe, ok := sanitizeAuditPayloadForPath(
		"/api/jail/101/options/allowed-options",
		map[string]interface{}{"allowedOptions": []interface{}{"allow.mount"}},
	).(map[string]interface{})
	if !ok || len(safe["allowedOptions"].([]interface{})) != 1 {
		t.Fatalf("safe allowed options were unexpectedly redacted: %#v", safe)
	}
}

func TestSanitizeAuditPayloadForJailCreation(t *testing.T) {
	t.Parallel()

	result, ok := sanitizeAuditPayloadForPath("/api/jail", map[string]interface{}{
		"ctId":              float64(101),
		"name":              "web-jail",
		"pool":              "zroot",
		"allowedOptions":    []interface{}{"allow.mount"},
		"fstab":             "private fstab",
		"resolvConf":        "private resolv.conf",
		"devfsRuleset":      "private devfs rules",
		"additionalOptions": "private jail options",
		"metadataMeta":      "private metadata",
		"metadataEnv":       "private environment",
		"hooks": map[string]interface{}{
			"prestart": map[string]interface{}{"enabled": true, "script": "private command"},
			"stop":     map[string]interface{}{"enabled": false, "script": ""},
		},
	}).(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized map, got %T", result)
	}

	for _, field := range []string{
		"fstab",
		"resolvConf",
		"devfsRuleset",
		"additionalOptions",
		"metadataMeta",
		"metadataEnv",
	} {
		if result[field] != "[REDACTED]" {
			t.Fatalf("%s was not redacted: %#v", field, result[field])
		}
	}
	if result["ctId"] != float64(101) || result["name"] != "web-jail" || result["pool"] != "zroot" {
		t.Fatalf("safe jail identity fields were not preserved: %#v", result)
	}
	allowed, ok := result["allowedOptions"].([]interface{})
	if !ok || len(allowed) != 1 || allowed[0] != "allow.mount" {
		t.Fatalf("safe allowed options were not preserved: %#v", result["allowedOptions"])
	}
	hooks, ok := result["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hooks map, got %#v", result["hooks"])
	}
	prestart, ok := hooks["prestart"].(map[string]interface{})
	if !ok || prestart["script"] != "[REDACTED]" || prestart["enabled"] != true {
		t.Fatalf("creation hook redaction lost safe fields: %#v", hooks)
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
						"name":     "confidential-disk.raw",
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
			if recorded.Query != "[REDACTED]" || recorded.Body != "[REDACTED]" {
				t.Fatalf("upload audit was not metadata-only: %+v", recorded)
			}
			if path == "/api/system/file-explorer/upload" {
				if recorded.Response != "[REDACTED]" {
					t.Fatalf("file explorer response was not redacted: %+v", recorded)
				}
			} else {
				responsePayload, ok := recorded.Response.(map[string]interface{})
				if !ok {
					t.Fatalf("downloader receipt was not retained: %#v", recorded.Response)
				}
				data, ok := responsePayload["data"].(map[string]interface{})
				if !ok ||
					data["uploadId"] != "opaque-but-private-upload-id" ||
					data["name"] != "confidential-disk.raw" {
					t.Fatalf("downloader receipt identity was not retained: %#v", responsePayload)
				}
				if _, leaked := data["path"]; leaked {
					t.Fatalf("downloader receipt retained a host path: %#v", responsePayload)
				}
			}

			for _, secret := range []string{
				"reusable-query-hash",
				"/private/storage",
				"TOP SECRET FILE CONTENT",
			} {
				if strings.Contains(records[0].Action, secret) {
					t.Fatalf("audit action leaked %q: %s", secret, records[0].Action)
				}
			}
			if path == "/api/system/file-explorer/upload" {
				for _, secret := range []string{"confidential-disk.raw", "opaque-but-private-upload-id"} {
					if strings.Contains(records[0].Action, secret) {
						t.Fatalf("file explorer audit action leaked %q: %s", secret, records[0].Action)
					}
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
		"username":    "admin",
		"password":    "super-secret",
		"sshKey":      "managed-private-key",
		"targetId":    float64(11),
		"jobId":       float64(22),
		"name":        "nightly-backup",
		"sambaAction": "upsert",
		"nested": map[string]interface{}{
			"token":       "abc",
			"credentials": []interface{}{"ceremony-data"},
			"challenges":  map[string]interface{}{"value": "nonce"},
			"safe":        "ok",
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
	if out["sshKey"] != "[REDACTED]" {
		t.Fatal("expected_ssh_key_to_be_redacted")
	}
	if out["targetId"] != float64(11) || out["jobId"] != float64(22) || out["name"] != "nightly-backup" {
		t.Fatal("expected_backup_identity_fields_to_be_preserved")
	}
	if out["sambaAction"] != "upsert" {
		t.Fatal("expected_safe_samba_intent_to_be_preserved")
	}

	nested, ok := out["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("expected_nested_map")
	}
	if nested["token"] != "[REDACTED]" {
		t.Fatal("expected_nested_token_to_be_redacted")
	}
	if nested["credentials"] != "[REDACTED]" {
		t.Fatal("expected_nested_credentials_to_be_redacted")
	}
	if nested["challenges"] != "[REDACTED]" {
		t.Fatal("expected_nested_challenges_to_be_redacted")
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
