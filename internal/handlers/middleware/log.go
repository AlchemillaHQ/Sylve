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
	"strings"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	authService "github.com/alchemillahq/sylve/internal/services/auth"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var hostname string
var importantGetPaths = []string{"/api/vnc"}

type claim struct {
	UserID   *uint
	Username string
	AuthType string
}

type action struct {
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Body     interface{} `json:"body,omitempty"`
	Response interface{} `json:"response,omitempty"`
}

func parseClaimUserID(raw interface{}) (*uint, bool) {
	switch v := raw.(type) {
	case uint:
		uid := v
		return &uid, true
	case *uint:
		return v, v != nil
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
	body *bytes.Buffer
}

func (w bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func RequestLoggerMiddleware(db *gorm.DB, authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if hostname == "" {
			stored, err := utils.GetSystemHostname()
			if err != nil {
				hostname = "unknown"
			} else {
				hostname = stored
			}
		}

		if strings.Contains(c.Request.URL.Path, "file-explorer/upload") {
			c.Next()
			return
		}

		if !utils.Contains(importantGetPaths, c.Request.URL.Path) && !strings.Contains(c.Request.URL.Path, "vnc") {
			if c.Request.Method == "OPTIONS" || c.Request.Method == "HEAD" || c.Request.Method == "GET" {
				c.Next()
				return
			}
		}

		bw := &bodyWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = bw

		var claims claim
		claims, err := getClaims(c, authService)
		if err != nil && (c.Request.URL.Path == "/api/auth/login" ||
			c.Request.URL.Path == "/api/auth/passkeys/login/begin" ||
			c.Request.URL.Path == "/api/auth/passkeys/login/finish" ||
			c.Request.URL.Path == "/api/utilities/downloads/signed-url" ||
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

		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			buf := new(bytes.Buffer)
			tee := io.TeeReader(c.Request.Body, buf)

			var body interface{}
			if err := json.NewDecoder(tee).Decode(&body); err != nil {
				logger.L.Warn().Msgf("Request body exists but could not be parsed as JSON: %v", err)
			} else {
				act.Body = body
			}

			c.Request.Body = io.NopCloser(buf)
		}

		actJSON, err := json.Marshal(act)
		if err != nil {
			logger.L.Error().Msgf("Failed to marshal action: %v", err)
		}

		log := &infoModels.AuditRecord{
			UserID:   claims.UserID,
			User:     claims.Username,
			AuthType: claims.AuthType,
			Node:     hostname,
			Started:  time.Now(),
			Action:   string(actJSON),
			Status:   "started",
			Version:  2,
		}

		if err := db.Create(log).Error; err != nil {
			logger.L.Error().Msgf("Failed to create audit log: %v", err)
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

		act.Response = response
		actJSON, err = json.Marshal(act)
		if err != nil {
			logger.L.Error().Msgf("Failed to marshal final action: %v", err)
		} else {
			log.Action = string(actJSON)
		}

		cStatus := c.Writer.Status()
		switch {
		case cStatus >= 200 && cStatus < 300:
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

		if err := db.Save(log).Error; err != nil {
			logger.L.Error().Msgf("Failed to update audit log: %v", err)
		}
	}
}
