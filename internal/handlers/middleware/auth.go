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
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

const SSEExpiresAtContextKey = "SSEExpiresAt"

func isGuestConsoleWebSocketPath(path, prefix string) bool {
	const suffix = "/console"

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}

	id := strings.TrimPrefix(strings.TrimSuffix(path, suffix), prefix)
	return id != "" && !strings.Contains(id, "/")
}

func isVMConsoleWebSocketPath(path string) bool {
	return isGuestConsoleWebSocketPath(path, "/api/vm/")
}

func isJailConsoleWebSocketPath(path string) bool {
	return isGuestConsoleWebSocketPath(path, "/api/jail/")
}

func isContainerConsoleWebSocketPath(path string) bool {
	return isGuestConsoleWebSocketPath(path, "/api/container/")
}

func isVNCWebSocketPath(path string) bool {
	const prefix = "/api/vnc/"
	port := strings.TrimPrefix(path, prefix)
	return strings.HasPrefix(path, prefix) && port != "" && !strings.Contains(port, "/")
}

func isConsoleWebSocketPath(path string) bool {
	return path == "/api/info/terminal" || isVNCWebSocketPath(path) ||
		isVMConsoleWebSocketPath(path) || isJailConsoleWebSocketPath(path) ||
		isContainerConsoleWebSocketPath(path)
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

var serviceReadPaths = map[string]struct{}{
	"/api/health/basic":     {},
	"/api/health/http":      {},
	"/api/info/node":        {},
	"/api/vm/simple":        {},
	"/api/vm/templates":     {},
	"/api/jail/simple":      {},
	"/api/jail/templates":   {},
	"/api/container/simple": {},
}

func clusterRequestAllowed(caller, method, path string) bool {
	intra := strings.HasPrefix(path, "/api/intra-cluster/") || path == "/api/intra-cluster"
	if caller == authSvc.ClusterCallerUser {
		return !intra
	}
	if intra {
		return path != "/api/intra-cluster/"
	}
	_, read := serviceReadPaths[path]
	return read && method == http.MethodGet
}

// A member is identified by its cert (cluster.PeerHandler). Any other client
// isn't a peer, and is left to the local session checks.
func authenticatePeer(c *gin.Context, authService *authSvc.Service) (bool, bool) {
	nodeID := cluster.PeerNode(c.Request)
	if nodeID == "" {
		return false, false
	}
	caller := authSvc.ClusterCallerNode
	user := authSvc.ProxiedUser{Username: nodeID, AuthType: authSvc.ClusterInternalAuthType}
	if value, present, single := singleHeaderValue(c.Request.Header, authSvc.ProxyUserHeader); present {
		proxied, err := authSvc.ParseProxiedUser(value)
		if !single || err != nil {
			abortAuthentication(c, http.StatusUnauthorized, "invalid_proxy_user")
			return true, false
		}
		caller, user = authSvc.ClusterCallerUser, proxied
	}
	if !clusterRequestAllowed(caller, c.Request.Method, c.Request.URL.Path) {
		abortAuthentication(c, http.StatusForbidden, "cluster_peer_forbidden")
		return true, false
	}
	if err := authService.AuthorizeClusterPeer(nodeID, caller, c.Request.Method, c.Request.URL.Path); err != nil {
		abortAuthentication(c, http.StatusForbidden, "cluster_peer_forbidden")
		return true, false
	}

	c.Set("AuthScope", "cluster")
	c.Set("ClusterCaller", caller)
	c.Set("Username", user.Username)
	c.Set("AuthType", user.AuthType)
	c.Set("IssuerNodeID", nodeID)
	if caller == authSvc.ClusterCallerUser {
		c.Set("Owner", authSvc.ProxiedOwner(nodeID, user))
	}
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
	c.Set("Owner", authSvc.LocalOwner(claims.UserID))
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
		Hash  string `json:"hash"`
		Token string `json:"token"`
	}
	data, err := hex.DecodeString(authHex)
	if err != nil || json.Unmarshal(data, &auth) != nil || auth.Hash == "" || auth.Token != "" {
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
		if present, ok := authenticatePeer(c, authService); present {
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
		if present, ok := authenticatePeer(c, authService); present {
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

// A member forwarding a joiner's request, or a joiner asking again once admitted
// (its key used up by then), is a peer and isn't checked against the key.
func AuthenticateJoin(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cluster.PeerNode(c.Request) != "" {
			if key, present, single := singleHeaderValue(c.Request.Header, authSvc.ClusterKeyHeader); present && single {
				c.Set("ClusterKey", key)
			}
			c.Next()
			return
		}
		if _, ok := validClusterKeyHeader(c, authService, true); ok {
			c.Next()
		}
	}
}

func RequireClusterScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("AuthScope") != "cluster" || c.GetString("ClusterCaller") != authSvc.ClusterCallerNode {
			abortAuthentication(c, http.StatusForbidden, "cluster_node_scope_required")
			return
		}
		c.Next()
	}
}
