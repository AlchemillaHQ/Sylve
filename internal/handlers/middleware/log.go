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
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	internalDB "github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	authService "github.com/alchemillahq/sylve/internal/services/auth"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var importantGetPaths = []string{"/api/vnc", "/api/info/terminal", "/api/vm/console"}

type claim struct {
	UserID   *uint
	Username string
	AuthType string
}

type action struct {
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Query    string      `json:"query,omitempty"`
	Body     interface{} `json:"body,omitempty"`
	Response interface{} `json:"response,omitempty"`
}

func isMetadataOnlyUploadAuditPath(path string) bool {
	path = strings.TrimSpace(path)

	return path == "/api/system/file-explorer/upload" ||
		path == "/api/utilities/downloader-uploads"
}

func shouldRedactAuditPayload(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	return isMetadataOnlyUploadAuditPath(path) ||
		auditPathMatches(path, "/api/auth/login") ||
		auditPathMatches(path, "/api/auth/passkeys/login") ||
		path == "/api/auth/passkeys/register/finish" ||
		auditPathMatches(path, "/api/dynamic-dns") ||
		auditPathMatches(path, "/api/certificates") ||
		(auditPathMatches(path, "/api/cluster") && !auditPathMatches(path, "/api/cluster/backups"))
}

func shouldRedactAuditResponse(path string) bool {
	path = strings.TrimSpace(path)
	if path == "/api/auth/passkeys/register/begin" {
		return true
	}
	if path == "/api/auth/passkeys/register/finish" {
		return false
	}
	return shouldRedactAuditPayload(path) && path != "/api/utilities/downloader-uploads"
}

func isImportantAuditGetPath(path string) bool {
	if utils.Contains(importantGetPaths, path) ||
		isVMConsoleWebSocketPath(path) ||
		isJailConsoleWebSocketPath(path) ||
		strings.Contains(path, "vnc") {
		return true
	}

	const certificateArchivePrefix = "/api/certificates/"
	if !strings.HasPrefix(path, certificateArchivePrefix) {
		return false
	}
	id, ok := strings.CutSuffix(strings.TrimPrefix(path, certificateArchivePrefix), "/archive")
	if !ok {
		return false
	}
	return id != "" && !strings.Contains(id, "/")
}

func isRoutineUnauditedRequest(method, path string) bool {
	return method == http.MethodPost && strings.TrimSpace(path) == "/api/auth/sse-tokens"
}

func auditPathMatches(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func isSensitiveAuditKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")

	switch key {
	case "auth",
		"password",
		"token",
		"accesstoken",
		"refreshtoken",
		"clustertoken",
		"hash",
		"authorization",
		"clusterauthorization",
		"clusterkey",
		"encryptionkey",
		"encryptionpassphrase",
		"secret",
		"signature",
		"sig",
		"totp",
		"otp",
		"privatekey",
		"presharedkey",
		"psk",
		"sshkey",
		"credential",
		"sessiondata",
		"assertion",
		"challenge":
		return true
	}

	return strings.Contains(key, "password") ||
		strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "privatekey") ||
		strings.Contains(key, "signature")
}

func sanitizeAuditQuery(path, rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	if shouldRedactAuditPayload(path) {
		return "[REDACTED]"
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "[REDACTED]"
	}
	for key := range values {
		if isSensitiveAuditKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	return values.Encode()
}

func sanitizeAuditPayload(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for k, value := range typed {
			if isSensitiveAuditKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = sanitizeAuditPayload(value)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, value := range typed {
			out[i] = sanitizeAuditPayload(value)
		}
		return out
	case string:
		if len(typed) > 4096 {
			return typed[:4096] + "...[truncated]"
		}
		return typed
	default:
		return typed
	}
}

func sanitizeDownloaderUploadAuditResponse(value interface{}) interface{} {
	payload, ok := value.(map[string]interface{})
	if !ok {
		return "[REDACTED]"
	}

	result := make(map[string]interface{})
	for _, key := range []string{"status", "message"} {
		if field, exists := payload[key]; exists {
			result[key] = sanitizeAuditPayload(field)
		}
	}

	if errorValue, exists := payload["error"]; exists {
		errorText, errorIsString := errorValue.(string)
		messageText, messageIsString := payload["message"].(string)
		if errorIsString && (errorText == "" || (messageIsString && errorText == messageText)) {
			result["error"] = errorText
		} else {
			result["error"] = "[REDACTED]"
		}
	}

	if data, ok := payload["data"].(map[string]interface{}); ok {
		safeData := make(map[string]interface{})
		for _, key := range []string{
			"uploadId",
			"name",
			"bytes",
			"status",
			"code",
			"retryable",
			"limitBytes",
		} {
			if field, exists := data[key]; exists {
				safeData[key] = sanitizeAuditPayload(field)
			}
		}
		result["data"] = safeData
	}

	return result
}

func sanitizeSignedDownloadAuditResponse(value interface{}) interface{} {
	payload, ok := value.(map[string]interface{})
	if !ok {
		return "[REDACTED]"
	}

	result := make(map[string]interface{})
	for _, key := range []string{"status", "message"} {
		if field, exists := payload[key]; exists {
			result[key] = sanitizeAuditPayload(field)
		}
	}
	if errorValue, exists := payload["error"]; exists {
		result["error"] = sanitizeAuditPayload(errorValue)
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		safeData := make(map[string]interface{})
		if expiresAt, exists := data["expiresAt"]; exists {
			safeData["expiresAt"] = sanitizeAuditPayload(expiresAt)
		}
		if _, exists := data["url"]; exists {
			safeData["url"] = "[REDACTED]"
		}
		result["data"] = safeData
	}
	return result
}

func isCloudInitTemplateAuditPath(path string) bool {
	return auditPathMatches(
		strings.TrimSpace(path),
		"/api/utilities/cloud-init/templates",
	)
}

func sanitizeCloudInitTemplateAuditResponse(value interface{}) interface{} {
	payload, ok := value.(map[string]interface{})
	if !ok {
		return "[REDACTED]"
	}

	result := make(map[string]interface{})
	for _, key := range []string{"status", "message", "error"} {
		if field, exists := payload[key]; exists {
			result[key] = sanitizeAuditPayload(field)
		}
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		safeData := make(map[string]interface{})
		for _, key := range []string{"id", "name"} {
			if field, exists := data[key]; exists {
				safeData[key] = sanitizeAuditPayload(field)
			}
		}
		for _, key := range []string{"user", "meta", "networkConfig"} {
			if _, exists := data[key]; exists {
				safeData[key] = "[REDACTED]"
			}
		}
		result["data"] = safeData
	}
	return result
}

func sanitizeAuditResponseForPath(path string, value interface{}) interface{} {
	if strings.TrimSpace(path) == "/api/utilities/downloader-uploads" {
		return sanitizeDownloaderUploadAuditResponse(value)
	}
	if strings.TrimSpace(path) == "/api/utilities/downloads/signed-url" {
		return sanitizeSignedDownloadAuditResponse(value)
	}
	if isCloudInitTemplateAuditPath(path) {
		return sanitizeCloudInitTemplateAuditResponse(value)
	}
	sanitized := sanitizeAuditPayload(value)
	if auditPathMatches(strings.TrimSpace(path), "/api/utilities/downloads") {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return sanitized
		}
		if data, ok := payload["data"].(map[string]interface{}); ok {
			if _, exists := data["url"]; exists {
				data["url"] = "[REDACTED]"
			}
		}
	}
	return sanitized
}

func isVMCloudInitAuditPath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "api" || segments[1] != "vm" {
		return false
	}

	return (segments[2] != "" &&
		segments[3] == "options" && segments[4] == "cloud-init") ||
		(segments[2] == "options" && segments[3] == "cloud-init" &&
			segments[4] != "")
}

func jailOptionAuditSegment(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "api" || segments[1] != "jail" {
		return "", false
	}

	if segments[2] != "" && segments[3] == "options" && segments[4] != "" {
		return segments[4], true
	}
	return "", false
}

func redactJailHookScripts(payload map[string]interface{}) {
	hooksValue, exists := payload["hooks"]
	if !exists {
		return
	}
	hooks, ok := hooksValue.(map[string]interface{})
	if !ok {
		payload["hooks"] = "[REDACTED]"
		return
	}
	for phase, phaseValue := range hooks {
		phasePayload, ok := phaseValue.(map[string]interface{})
		if !ok {
			hooks[phase] = "[REDACTED]"
			continue
		}
		if _, exists := phasePayload["script"]; exists {
			phasePayload["script"] = "[REDACTED]"
		}
	}
}

func sanitizeJailCreateAuditPayload(path string, sanitized interface{}) interface{} {
	if strings.TrimSpace(path) != "/api/jail" {
		return sanitized
	}

	payload, ok := sanitized.(map[string]interface{})
	if !ok {
		return "[REDACTED]"
	}
	for _, field := range []string{
		"fstab",
		"resolvConf",
		"devfsRuleset",
		"additionalOptions",
		"metadataMeta",
		"metadataEnv",
	} {
		if _, exists := payload[field]; exists {
			payload[field] = "[REDACTED]"
		}
	}
	redactJailHookScripts(payload)
	return payload
}

func sanitizeJailOptionAuditPayload(path string, sanitized interface{}) interface{} {
	option, ok := jailOptionAuditSegment(path)
	if !ok {
		return sanitized
	}

	sensitiveFields := map[string][]string{
		"fstab":              {"fstab"},
		"resolv-conf":        {"resolvConf"},
		"devfs-rules":        {"devFSRules"},
		"additional-options": {"additionalOptions"},
		"metadata":           {"metadata", "env"},
	}
	if fields, sensitive := sensitiveFields[option]; sensitive {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return "[REDACTED]"
		}
		for _, field := range fields {
			if _, exists := payload[field]; exists {
				payload[field] = "[REDACTED]"
			}
		}
		return payload
	}
	if option != "lifecycle-hooks" {
		return sanitized
	}

	payload, ok := sanitized.(map[string]interface{})
	if !ok {
		return "[REDACTED]"
	}
	redactJailHookScripts(payload)
	return payload
}

func sanitizeAuditPayloadForPath(path string, value interface{}) interface{} {
	sanitized := sanitizeAuditPayload(value)
	if isCloudInitTemplateAuditPath(path) {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return "[REDACTED]"
		}
		safePayload := make(map[string]interface{})
		if name, exists := payload["name"]; exists {
			safePayload["name"] = name
		}
		for _, key := range []string{"user", "meta", "networkConfig"} {
			if _, exists := payload[key]; exists {
				safePayload[key] = "[REDACTED]"
			}
		}
		return safePayload
	}
	if strings.TrimSpace(path) == "/api/utilities/downloads/signed-url" {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return "[REDACTED]"
		}
		safePayload := make(map[string]interface{})
		for _, key := range []string{"name", "parentUUID"} {
			if field, exists := payload[key]; exists {
				safePayload[key] = field
			}
		}
		return safePayload
	}
	if strings.TrimSpace(path) == "/api/utilities/downloads" {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return "[REDACTED]"
		}
		if _, exists := payload["url"]; exists {
			payload["url"] = "[REDACTED]"
		}
		sanitized = payload
	}
	if isVMCloudInitAuditPath(path) {
		payload, ok := sanitized.(map[string]interface{})
		if !ok {
			return "[REDACTED]"
		}
		for _, key := range []string{"data", "metadata", "networkConfig"} {
			if _, exists := payload[key]; exists {
				payload[key] = "[REDACTED]"
			}
		}
		sanitized = payload
	}
	sanitized = sanitizeJailCreateAuditPayload(path, sanitized)
	return sanitizeJailOptionAuditPayload(path, sanitized)
}

func isMultipartAuditRequest(request *http.Request) bool {
	if request == nil {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Type")))
	return strings.HasPrefix(contentType, "multipart/")
}

func parseClaimUserID(raw interface{}) (*uint, bool) {
	switch v := raw.(type) {
	case uint:
		if uint64(v) > uint64(math.MaxInt64) {
			return nil, false
		}
		uid := v
		return &uid, true
	case *uint:
		if v == nil {
			return nil, false
		}
		if uint64(*v) > uint64(math.MaxInt64) {
			return nil, false
		}
		return v, true
	case int:
		if v < 0 {
			return nil, false
		}
		uid := uint(v)
		return &uid, true
	case int64:
		if v < 0 {
			return nil, false
		}
		uid := uint(v)
		return &uid, true
	case uint64:
		if v > uint64(math.MaxInt64) {
			return nil, false
		}
		uid := uint(v)
		return &uid, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > float64(math.MaxInt64) || v != math.Trunc(v) {
			return nil, false
		}
		uid := uint(v)
		return &uid, true
	case json.Number:
		n, err := v.Int64()
		if err != nil || n < 0 {
			return nil, false
		}
		uid := uint(n)
		return &uid, true
	default:
		return nil, false
	}
}

func getClaims(c *gin.Context, authService *authService.Service) (claim, error) {
	var claims claim

	if uidAny, hasUserID := c.Get("UserID"); hasUserID {
		usernameAny, hasUsername := c.Get("Username")
		authTypeAny, hasAuthType := c.Get("AuthType")
		if hasUsername && hasAuthType {
			var uid *uint
			if parsed, ok := parseClaimUserID(uidAny); ok {
				uid = parsed
			}

			claims = claim{
				UserID:   uid,
				Username: fmt.Sprintf("%v", usernameAny),
				AuthType: fmt.Sprintf("%v", authTypeAny),
			}

			if strings.TrimSpace(claims.Username) != "" && strings.TrimSpace(claims.AuthType) != "" {
				return claims, nil
			}
		}
	}

	token := c.GetString("Token")

	if token == "" {
		if hash := c.Query("hash"); hash != "" {
			t, err := authService.GetTokenBySHA256(hash)

			if err != nil {
				return claims, fmt.Errorf("invalid_hash: %w", err)
			}

			token = t
		}
	}

	if token == "" {
		return claims, fmt.Errorf("token_not_found")
	}

	iface, err := utils.ParseJWT(token)
	if err != nil {
		return claims, fmt.Errorf("failed_to_parse_jwt: %w", err)
	}

	cMap, ok := iface.(map[string]interface{})
	if !ok {
		return claims, fmt.Errorf("invalid_claims_format")
	}

	allAny, ok := cMap["custom_claims"]
	if !ok {
		return claims, fmt.Errorf("custom_claims_missing")
	}

	all, ok := allAny.(map[string]interface{})
	if !ok {
		return claims, fmt.Errorf("invalid_custom_claims_format")
	}

	userID, _ := parseClaimUserID(all["userId"])
	user := fmt.Sprintf("%v", all["username"])
	authType := fmt.Sprintf("%v", all["authType"])
	if strings.TrimSpace(user) == "" || strings.TrimSpace(authType) == "" {
		return claims, fmt.Errorf("invalid_custom_claims")
	}

	claims = claim{
		UserID:   userID,
		Username: user,
		AuthType: authType,
	}

	return claims, nil
}

type bodyWriter struct {
	gin.ResponseWriter
	body    *bytes.Buffer
	capture bool
}

type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *replayReadCloser) Close() error {
	return r.closer.Close()
}

func (w bodyWriter) Write(b []byte) (int, error) {
	if w.capture {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func persistRequestAuditResult(auditDB *gorm.DB, desired *infoModels.AuditRecord) error {
	if auditDB == nil || desired == nil || desired.ID == 0 {
		return nil
	}

	return auditDB.Transaction(func(tx *gorm.DB) error {
		var current infoModels.AuditRecord
		if err := tx.First(&current, desired.ID).Error; err != nil {
			return err
		}

		updates := map[string]any{
			"user_id":   desired.UserID,
			"user":      desired.User,
			"auth_type": desired.AuthType,
			"node":      desired.Node,
			"action":    desired.Action,
			"ended":     desired.Ended,
			"duration":  desired.Duration,
			"version":   desired.Version,
		}

		expectedStatus := current.Status
		switch current.Status {
		case "started":
			updates["status"] = desired.Status
			updates["async_job_id"] = desired.AsyncJobID
			updates["async_job_type"] = desired.AsyncJobType
			updates["async_operation_id"] = desired.AsyncOperationID
			updates["error"] = desired.Error
		case "pending":
			// The handler persisted the exact async identity before queue
			// publication. Keep it pending while recording the HTTP response.
			// If a worker wins the CAS first, the update below affects no row
			// and cannot overwrite its terminal result.
		default:
			// A fast worker already made the audit terminal. Its operation
			// response and completion timestamp are authoritative.
			return nil
		}

		result := tx.Model(&infoModels.AuditRecord{}).
			Where("id = ? AND status = ?", desired.ID, expectedStatus).
			Updates(updates)
		return result.Error
	})
}

func RequestLoggerMiddleware(telemetryDB *gorm.DB, authService *authService.Service) gin.HandlerFunc {
	if telemetryDB == nil {
		panic("request logger middleware requires a non-nil telemetry database")
	}
	auditDB := telemetryDB
	nodeHostname := "unknown"
	if storedHostname, err := utils.GetSystemHostname(); err == nil {
		nodeHostname = storedHostname
	}

	return func(c *gin.Context) {
		if strings.Contains(c.Request.URL.Path, "/network/firewall/advanced/preview") ||
			isRoutineUnauditedRequest(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}

		if !isImportantAuditGetPath(c.Request.URL.Path) {
			if c.Request.Method == "OPTIONS" || c.Request.Method == "HEAD" || c.Request.Method == "GET" {
				c.Next()
				return
			}
		}

		redactPayload := shouldRedactAuditPayload(c.Request.URL.Path)
		redactResponse := shouldRedactAuditResponse(c.Request.URL.Path)
		captureResponse := !redactResponse ||
			c.Request.URL.Path == "/api/auth/login" ||
			c.Request.URL.Path == "/api/auth/passkeys/login/finish"
		bw := &bodyWriter{
			body:           bytes.NewBufferString(""),
			ResponseWriter: c.Writer,
			capture:        captureResponse,
		}
		c.Writer = bw

		var claims claim
		claims, err := getClaims(c, authService)
		if err != nil && (c.Request.URL.Path == "/api/auth/login" ||
			c.Request.URL.Path == "/api/auth/passkeys/login/begin" ||
			c.Request.URL.Path == "/api/auth/passkeys/login/finish" ||
			strings.HasPrefix(c.Request.URL.Path, "/api/cluster")) {

			if strings.HasPrefix(c.Request.URL.Path, "/api/cluster") {
				claims = claim{
					UserID:   nil,
					Username: "cluster",
					AuthType: "cluster-key",
				}
			} else {
				claims = claim{
					UserID:   nil,
					Username: "anonymous",
					AuthType: "none",
				}
			}
		} else if err != nil {
			logger.L.Error().Msgf("%s, Failed to get claims: %v", c.Request.URL.Path, err)
			c.Next()
			return
		}

		var act action
		act.Method = c.Request.Method
		act.Path = c.Request.URL.Path
		act.Query = sanitizeAuditQuery(c.Request.URL.Path, c.Request.URL.RawQuery)

		if isMultipartAuditRequest(c.Request) {
			if redactPayload {
				act.Body = "[REDACTED]"
			} else {
				act.Body = "[OMITTED: multipart]"
			}
		} else if c.Request.Body != nil && c.Request.ContentLength > 0 {
			if redactPayload {
				act.Body = "[REDACTED]"
			} else {
				originalBody := c.Request.Body
				buf := new(bytes.Buffer)
				tee := io.TeeReader(originalBody, buf)

				var body interface{}
				if err := json.NewDecoder(tee).Decode(&body); err != nil {
					logger.L.Warn().Msgf("Request body exists but could not be parsed as JSON: %v", err)
				} else {
					act.Body = sanitizeAuditPayloadForPath(c.Request.URL.Path, body)
				}

				// Replay everything consumed by the audit decoder, then continue
				// from the original reader. Keeping the original reader preserves
				// any MaxBytesReader state and its *http.MaxBytesError.
				c.Request.Body = &replayReadCloser{
					Reader: io.MultiReader(bytes.NewReader(buf.Bytes()), originalBody),
					closer: originalBody,
				}
			}
		}

		actJSON, err := json.Marshal(act)
		if err != nil {
			logger.L.Error().Msgf("Failed to marshal action: %v", err)
		}

		log := &infoModels.AuditRecord{
			UserID:   claims.UserID,
			User:     claims.Username,
			AuthType: claims.AuthType,
			Node:     nodeHostname,
			Started:  time.Now(),
			Action:   string(actJSON),
			Status:   "started",
			Version:  2,
		}

		if err := auditDB.Create(log).Error; err != nil {
			logger.L.Error().Msgf("Failed to create audit log: %v", err)
		} else if log.ID > 0 {
			// Async handlers bind this already-persisted row to their exact
			// operation before publishing a queue message.
			c.Request = c.Request.WithContext(
				internalDB.ContextWithAuditRecordID(c.Request.Context(), log.ID),
			)
		}

		c.Next()

		var response interface{}
		bodyBytes := bw.body.Bytes()

		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &response); err != nil {
				response = string(bodyBytes)
			}
		} else {
			response = nil
		}

		if redactResponse {
			act.Response = "[REDACTED]"
		} else {
			act.Response = sanitizeAuditResponseForPath(c.Request.URL.Path, response)
		}
		actJSON, err = json.Marshal(act)
		if err != nil {
			logger.L.Error().Msgf("Failed to marshal final action: %v", err)
		} else {
			log.Action = string(actJSON)
		}

		cStatus := c.Writer.Status()
		switch {
		case cStatus >= 200 && cStatus < 300:
			// If the handler flagged an async job, set status to "pending" instead of "success".
			if jobIDAny, hasAsync := c.Get("AuditAsyncJobID"); hasAsync {
				if jobID, ok := jobIDAny.(uint); ok {
					log.Status = "pending"
					log.AsyncJobID = &jobID
					if jobTypeAny, ok := c.Get("AuditAsyncJobType"); ok {
						if jobType, ok := jobTypeAny.(string); ok {
							log.AsyncJobType = jobType
						}
					}
					break
				}
			}
			log.Status = "success"
		case cStatus >= 400 && cStatus < 500:
			log.Status = "client_error"
		case cStatus >= 500:
			log.Status = "server_error"
		default:
			log.Status = "unknown"
		}

		log.Ended = time.Now()
		log.Duration = time.Since(log.Started)

		if (c.Request.URL.Path == "/api/auth/login" || c.Request.URL.Path == "/api/auth/passkeys/login/finish") && cStatus == 200 {
			var resBody struct {
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			if err := json.Unmarshal(bw.body.Bytes(), &resBody); err == nil && resBody.Data.Token != "" {
				if newClaims, err := utils.ParseJWT(resBody.Data.Token); err == nil {
					if cMap, ok := newClaims.(map[string]interface{}); ok {
						if allAny, ok := cMap["custom_claims"]; ok {
							if all, ok := allAny.(map[string]interface{}); ok {
								if uid, ok := parseClaimUserID(all["userId"]); ok {
									log.UserID = uid
								}
								log.User = fmt.Sprintf("%v", all["username"])
								log.AuthType = fmt.Sprintf("%v", all["authType"])
							}
						}
					}
				}
			}
		}

		if err := persistRequestAuditResult(auditDB, log); err != nil {
			logger.L.Error().Msgf("Failed to update audit log: %v", err)
		}
	}
}
