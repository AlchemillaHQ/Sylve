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
	"strings"
	infoModels "sylve/internal/db/models/info"
	authService "sylve/internal/services/auth"

	"sylve/internal/logger"
	"sylve/pkg/utils"
	"time"

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

func getClaims(c *gin.Context, authService *authService.Service) (claim, error) {
	var claims claim
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

	all := cMap["custom_claims"].(map[string]interface{})
	userID := uint(all["userId"].(float64))
	user := all["username"].(string)
	authType := all["authType"].(string)

	claims = claim{
		UserID:   &userID,
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
		if err != nil && (c.Request.URL.Path == "/api/auth/login" || c.Request.URL.Path == "/api/utilities/downloads/signed-url") {
			claims = claim{
				UserID:   nil,
				Username: "anonymous",
				AuthType: "none",
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

		if c.Request.URL.Path == "/api/auth/login" && cStatus == 200 {
			var resBody struct {
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			if err := json.Unmarshal(bw.body.Bytes(), &resBody); err == nil && resBody.Data.Token != "" {
				if newClaims, err := utils.ParseJWT(resBody.Data.Token); err == nil {
					if cMap, ok := newClaims.(map[string]interface{}); ok {
						all := cMap["custom_claims"].(map[string]interface{})
						uid := uint(all["userId"].(float64))
						user := all["username"].(string)
						authType := all["authType"].(string)
						log.UserID = &uid
						log.User = user
						log.AuthType = authType
					}
				}
			}
		}

		if err := db.Save(log).Error; err != nil {
			logger.L.Error().Msgf("Failed to update audit log: %v", err)
		}
	}
}
