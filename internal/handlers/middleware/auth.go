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
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func EnsureAuthenticated(authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		isWSAuthPath := strings.HasPrefix(path, "/api/vnc/") ||
			path == "/api/info/terminal" ||
			path == "/api/vm/console" ||
			path == "/api/jail/console"

		if strings.HasPrefix(path, "/api/utilities/downloads/") &&
			!strings.HasPrefix(path, "/api/utilities/downloads/bulk-delete") &&
			!strings.HasPrefix(path, "/api/utilities/downloads/signed-url") {

			subpath := strings.TrimPrefix(path, "/api/utilities/downloads/")
			if subpath != "" && !regexp.MustCompile(`^\d+$`).MatchString(subpath) {
				c.Next()
				return
			}
		}

		if path == "/api/auth/login" {
			c.Next()
			return
		}

		if isWSAuthPath {
			authHex := c.Query("auth")
			if authHex == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing_ws_auth"})
				return
			}

			var wssAuth struct {
				Hash     string `json:"hash"`
				Hostname string `json:"hostname"`
				Token    string `json:"token"`
			}

			data, err := hex.DecodeString(authHex)
			if err != nil || json.Unmarshal(data, &wssAuth) != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid_ws_auth"})
				return
			}

			if strings.TrimSpace(wssAuth.Hostname) == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid_ws_auth"})
				return
			}

			if wssAuth.Token != "" {
				if claims, err := authService.VerifyClusterJWT(wssAuth.Token); err == nil {
					c.Set("Token", wssAuth.Token)
					c.Set("AuthScope", "wss-cluster")
					c.Set("UserID", claims.UserID)
					c.Set("Username", claims.Username)
					c.Set("AuthType", claims.AuthType)
					c.Next()
					return
				}
			}

			if wssAuth.Hash != "" {
				if tok, err := authService.GetTokenBySHA256(wssAuth.Hash); err == nil {
					if claims, err := authService.ValidateToken(tok); err == nil {
						c.Set("Token", tok)
						c.Set("AuthScope", "wss")
						c.Set("UserID", claims.UserID)
						c.Set("Username", claims.Username)
						c.Set("AuthType", claims.AuthType)
						authService.UpdateLastUsageTime(claims.UserID)
						c.Next()
						return
					}
				}
			}

			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid_ws_auth"})
			return
		}

		if (path == "/api/cluster/accept-join" || strings.HasPrefix(path, "/api/health/basic")) &&
			c.Request.Method == http.MethodPost {
			raw, err := io.ReadAll(c.Request.Body)
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))

				var body struct {
					ClusterKey string `json:"clusterKey"`
				}

				if json.Unmarshal(raw, &body) == nil && body.ClusterKey != "" && authService.IsValidClusterKey(body.ClusterKey) {
					c.Next()
					return
				}
			} else {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(nil))
			}
		}

		if clusterJWT, err := utils.GetClusterTokenFromHeader(c.Request.Header); err == nil && clusterJWT != "" {
			clusterClaims, err := authService.VerifyClusterJWT(clusterJWT)

			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid_cluster_token"})
				return
			}

			c.Set("Token", clusterJWT)
			c.Set("AuthScope", "cluster")
			c.Set("UserID", clusterClaims.UserID)
			c.Set("Username", clusterClaims.Username)
			c.Set("AuthType", clusterClaims.AuthType)
			c.Next()
			return
		}

		var localJWT string
		if hash := c.Query("hash"); hash != "" {
			tok, err := authService.GetTokenBySHA256(hash)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid_hash"})
				return
			}
			localJWT = tok
		}

		if localJWT == "" {
			var err error
			localJWT, err = utils.GetTokenFromHeader(c.Request.Header)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no_token_provided"})
				return
			}
		}

		claims, err := authService.ValidateToken(localJWT)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "error": err.Error()})
			return
		}

		c.Set("Token", localJWT)
		c.Set("AuthScope", "local")
		c.Set("UserID", claims.UserID)
		c.Set("Username", claims.Username)
		c.Set("AuthType", claims.AuthType)
		authService.UpdateLastUsageTime(claims.UserID)
		c.Next()
	}
}
