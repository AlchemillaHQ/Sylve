// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

const SSEExpiresAtContextKey = "SSEExpiresAt"

func isVMConsoleWebSocketPath(path string) bool {
	const prefix = "/api/vm/"
	const suffix = "/console"

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}

	rid := strings.TrimPrefix(strings.TrimSuffix(path, suffix), prefix)
	return rid != "" && !strings.Contains(rid, "/")
}

func isJailConsoleWebSocketPath(path string) bool {
	const prefix = "/api/jail/"
	const suffix = "/console"

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}

	ctID := strings.TrimPrefix(strings.TrimSuffix(path, suffix), prefix)
	return ctID != "" && !strings.Contains(ctID, "/")
}

func isVNCWebSocketPath(path string) bool {
	const prefix = "/api/vnc/"
	port := strings.TrimPrefix(path, prefix)
	return strings.HasPrefix(path, prefix) && port != "" && !strings.Contains(port, "/")
}

func isConsoleWebSocketPath(path string) bool {
	return path == "/api/info/terminal" || isVNCWebSocketPath(path) ||
		isVMConsoleWebSocketPath(path) || isJailConsoleWebSocketPath(path)
}

func abortAuthentication(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

func singleHeaderValue(header http.Header, name string) (string, bool, bool) {
	values := header.Values(name)
	if len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 {
		return "", true, false
	}
	return values[0], true, true
}

func clusterBearer(header http.Header) (string, bool, bool) {
	value, present, valid := singleHeaderValue(header, authSvc.ClusterTokenHeader)
	if !present || !valid {
		return "", present, valid
	}
	if !strings.HasPrefix(value, "Bearer ") {
		return "", true, false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	if token == "" || token != strings.TrimSpace(token) || strings.ContainsAny(token, " \t\r\n") {
		return "", true, false
	}
	return token, true, true
}

var serviceReadPaths = map[string]struct{}{
	"/api/health/http":    {},
	"/api/info/node":      {},
	"/api/vm/simple":      {},
	"/api/vm/templates":   {},
	"/api/jail/simple":    {},
	"/api/jail/templates": {},
}

func clusterRequestAllowed(claims serviceInterfaces.CustomClaims, method, path string) bool {
	switch claims.TokenUse {
	case authSvc.ClusterTokenUseUserProxy:
		if claims.UserID == 0 {
			_, allowed := serviceReadPaths[path]
			return allowed && method == http.MethodGet && !claims.Admin
		}
		if !claims.Admin || strings.HasPrefix(path, "/api/intra-cluster/") || path == "/api/intra-cluster" {
			return false
		}
		return claims.AuthType == "sylve" || claims.AuthType == "pam" ||
			claims.AuthType == authSvc.AuthTypeSylvePasskey
	case authSvc.ClusterTokenUseInternalControl:
		return claims.UserID == 0 && !claims.Admin && claims.AuthType == authSvc.ClusterInternalAuthType &&
			strings.HasPrefix(path, "/api/intra-cluster/") && path != "/api/intra-cluster/"
	default:
		return false
	}
}

func authenticateClusterHeader(c *gin.Context, authService *authSvc.Service) (bool, bool) {
	clusterJWT, present, valid := clusterBearer(c.Request.Header)
	if !present {
		return false, false
	}
	if !valid {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_token")
		return true, false
	}

	claims, err := authService.VerifyClusterJWT(clusterJWT)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_token")
		return true, false
	}
	if !clusterRequestAllowed(claims, c.Request.Method, c.Request.URL.Path) {
		abortAuthentication(c, http.StatusForbidden, "cluster_token_forbidden")
		return true, false
	}

	c.Set("Token", clusterJWT)
	c.Set("AuthScope", "cluster")
	c.Set("ClusterTokenUse", claims.TokenUse)
	c.Set("UserID", claims.UserID)
	c.Set("Username", claims.Username)
	c.Set("AuthType", claims.AuthType)
	c.Set("ProxyAdmin", claims.Admin)
	return true, true
}

func setLocalAuthentication(c *gin.Context, authService *authSvc.Service, token string) bool {
	claims, err := authService.ValidateToken(token)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, err.Error())
		return false
	}

	c.Set("Token", token)
	c.Set("AuthScope", "local")
	c.Set("UserID", claims.UserID)
	c.Set("Username", claims.Username)
	c.Set("AuthType", claims.AuthType)
	_ = authService.UpdateLastUsageTime(claims.UserID)
	return true
}

func authenticateConsole(c *gin.Context, authService *authSvc.Service) bool {
	authHex := c.Query("auth")
	if authHex == "" {
		abortAuthentication(c, http.StatusUnauthorized, "missing_ws_auth")
		return false
	}

	var auth struct {
		Hash     string `json:"hash"`
		Hostname string `json:"hostname"`
		Token    string `json:"token"`
	}
	data, err := hex.DecodeString(authHex)
	if err != nil || json.Unmarshal(data, &auth) != nil || strings.TrimSpace(auth.Hostname) == "" ||
		auth.Hash == "" || auth.Token != "" {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_ws_auth")
		return false
	}

	token, err := authService.GetTokenBySHA256(auth.Hash)
	if err != nil || !setLocalAuthentication(c, authService, token) {
		if err != nil {
			abortAuthentication(c, http.StatusUnauthorized, "invalid_ws_auth")
		}
		return false
	}
	return true
}

func authenticateLocalRequest(c *gin.Context, authService *authSvc.Service) bool {
	if isConsoleWebSocketPath(c.Request.URL.Path) {
		return authenticateConsole(c, authService)
	}

	if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/system/file-explorer/download" {
		if encodedAuth := c.Query("auth"); encodedAuth != "" {
			var routingAuth struct {
				Hostname string `json:"hostname"`
				Token    string `json:"token"`
			}
			data, err := hex.DecodeString(encodedAuth)
			if err != nil || json.Unmarshal(data, &routingAuth) != nil ||
				strings.TrimSpace(routingAuth.Hostname) == "" || routingAuth.Token != "" {
				abortAuthentication(c, http.StatusUnauthorized, "invalid_auth_transport")
				return false
			}
		}
		if hash := c.Query("hash"); hash != "" {
			token, err := authService.GetTokenBySHA256(hash)
			if err != nil {
				abortAuthentication(c, http.StatusUnauthorized, "invalid_hash")
				return false
			}
			return setLocalAuthentication(c, authService, token)
		}
	}

	localJWT, err := utils.GetTokenFromHeader(c.Request.Header)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, "no_token_provided")
		return false
	}
	return setLocalAuthentication(c, authService, localJWT)
}

func EnsureAuthenticated(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if present, ok := authenticateClusterHeader(c, authService); present {
			if ok {
				c.Next()
			}
			return
		}
		if authenticateLocalRequest(c, authService) {
			c.Next()
		}
	}
}

func AuthenticateSSE(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("sse_token")
		if token == "" {
			abortAuthentication(c, http.StatusUnauthorized, "missing_sse_token")
			return
		}
		claims, err := authService.ValidateScopedJWT(token, "sse")
		if err != nil {
			abortAuthentication(c, http.StatusUnauthorized, "invalid_sse_token")
			return
		}
		c.Set("Token", token)
		c.Set("AuthScope", "sse")
		c.Set("UserID", claims.UserID)
		c.Set("Username", claims.Username)
		c.Set("AuthType", claims.AuthType)
		c.Set(SSEExpiresAtContextKey, claims.ExpiresAt)
		c.Next()
	}
}

func validClusterKeyHeader(c *gin.Context, authService *authSvc.Service, required bool) (bool, bool) {
	key, present, single := singleHeaderValue(c.Request.Header, authSvc.ClusterKeyHeader)
	if !present {
		if required {
			abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_key")
		}
		return false, !required
	}
	if !single || key == "" || key != strings.TrimSpace(key) || !authService.IsValidClusterKey(key) {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_key")
		return true, false
	}
	c.Set("AuthScope", "cluster-key")
	c.Set("ClusterKey", key)
	return true, true
}

func AuthenticateBasicHealth(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if present, ok := authenticateClusterHeader(c, authService); present {
			if ok {
				c.Next()
			}
			return
		}
		if present, ok := validClusterKeyHeader(c, authService, false); present {
			if ok {
				c.Next()
			}
			return
		}
		if authenticateLocalRequest(c, authService) {
			c.Next()
		}
	}
}

func AuthenticateClusterKey(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token, present, valid := clusterBearer(c.Request.Header); present {
			if !valid {
				abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_token")
				return
			}
			if _, err := authService.VerifyClusterJWT(token); err != nil {
				abortAuthentication(c, http.StatusUnauthorized, "invalid_cluster_token")
				return
			}
			abortAuthentication(c, http.StatusForbidden, "cluster_token_forbidden")
			return
		}
		if _, ok := validClusterKeyHeader(c, authService, true); ok {
			c.Next()
		}
	}
}

func RequireClusterScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("AuthScope") != "cluster" {
			abortAuthentication(c, http.StatusForbidden, "cluster_scope_required")
			return
		}
		if c.GetString("ClusterTokenUse") != authSvc.ClusterTokenUseInternalControl {
			abortAuthentication(c, http.StatusForbidden, "internal_cluster_token_required")
			return
		}
		if c.GetString("AuthType") != authSvc.ClusterInternalAuthType {
			abortAuthentication(c, http.StatusForbidden, "internal_cluster_auth_type_required")
			return
		}
		if c.GetUint("UserID") != 0 || c.GetBool("ProxyAdmin") {
			abortAuthentication(c, http.StatusForbidden, "internal_cluster_user_required")
			return
		}

		c.Next()
	}
}
