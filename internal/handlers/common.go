// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var hostname string // Test override; production resolves the cached system hostname at registration.

var secureTransport = &http.Transport{
	MaxIdleConns:          64,
	MaxIdleConnsPerHost:   32,
	IdleConnTimeout:       60 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
}

var insecureTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	MaxIdleConns:          64,
	MaxIdleConnsPerHost:   32,
	IdleConnTimeout:       60 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
}

func newReverseProxy(target *url.URL, tr http.RoundTripper, preserveHost bool) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = tr
	p.FlushInterval = 50 * time.Millisecond

	orig := p.Director
	p.Director = func(r *http.Request) {
		orig(r)

		// Prevent upstream gzip on proxied requests so we don't double-compress
		// when Gin gzip middleware is active on the current node.
		r.Header.Del("Accept-Encoding")

		if r.Header.Get("X-Forwarded-Proto") == "" {
			if target.Scheme != "" {
				r.Header.Set("X-Forwarded-Proto", target.Scheme)
			} else {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
		}
		if r.Header.Get("X-Forwarded-Host") == "" {
			r.Header.Set("X-Forwarded-Host", r.Host)
		}
		if preserveHost {
			if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
				r.Host = xfh
			}
		}

		if up := r.Header.Get("Upgrade"); up != "" {
			r.Header.Set("Upgrade", up)
		}
		if conn := r.Header.Get("Connection"); conn != "" {
			r.Header.Set("Connection", conn)
		}
	}

	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	}

	return p
}

func ReverseProxy(c *gin.Context, backend string) {
	remote, err := url.Parse(backend)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse proxy URL"})
		c.Abort()
		return
	}
	p := newReverseProxy(remote, secureTransport, false)
	p.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

func ReverseProxyInsecure(c *gin.Context, backend string) {
	remote, err := url.Parse(backend)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse proxy URL"})
		c.Abort()
		return
	}
	p := newReverseProxy(remote, insecureTransport, false)
	p.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

func resolveForwardClusterToken(c *gin.Context, authService *authService.Service) string {
	token := strings.TrimSpace(c.GetString("Token"))
	scope := strings.TrimSpace(c.GetString("AuthScope"))
	if token != "" && (scope == "wss-cluster" || scope == "cluster") {
		return token
	}

	if authService == nil {
		return ""
	}

	userIDRaw, okUserID := c.Get("UserID")
	usernameRaw, okUsername := c.Get("Username")
	authTypeRaw, okAuthType := c.Get("AuthType")
	if !okUserID || !okUsername || !okAuthType {
		return ""
	}

	var userID uint
	switch v := userIDRaw.(type) {
	case uint:
		userID = v
	case float64:
		userID = uint(v)
	default:
		return ""
	}

	username, ok := usernameRaw.(string)
	if !ok || strings.TrimSpace(username) == "" {
		return ""
	}

	authType, ok := authTypeRaw.(string)
	if !ok {
		authType = ""
	}

	clusterToken, err := authService.CreateClusterJWT(userID, username, authType, "")
	if err != nil {
		return ""
	}

	return clusterToken
}

func injectForwardClusterAuth(c *gin.Context, authService *authService.Service) {
	clusterToken := resolveForwardClusterToken(c, authService)
	if clusterToken == "" {
		return
	}

	c.Request.Header.Set("X-Cluster-Token", fmt.Sprintf("Bearer %s", clusterToken))

	authHex := c.Query("auth")
	if authHex == "" {
		return
	}

	var wsAuth struct {
		Hash     string `json:"hash"`
		Hostname string `json:"hostname"`
		Token    string `json:"token"`
	}

	data, err := hex.DecodeString(authHex)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &wsAuth); err != nil {
		return
	}

	wsAuth.Token = clusterToken

	encoded, err := json.Marshal(wsAuth)
	if err != nil {
		return
	}

	query := c.Request.URL.Query()
	query.Set("auth", hex.EncodeToString(encoded))
	c.Request.URL.RawQuery = query.Encode()
}

func isBearerWebSocketProtocol(value string) bool {
	parts := strings.Split(value, ",")
	return len(parts) == 2 &&
		strings.TrimSpace(parts[0]) == "Bearer" &&
		strings.TrimSpace(parts[1]) != ""
}

func requestedHostname(request *http.Request) (string, bool, error) {
	if request == nil {
		return "", false, nil
	}

	_, hasHostnameHeader := request.Header[http.CanonicalHeaderKey("X-Current-Hostname")]
	hasAuthQuery := request.URL != nil && request.URL.Query().Has("auth")
	webSocketProtocol := request.Header.Get("Sec-WebSocket-Protocol")
	hasRoutingProtocol := strings.TrimSpace(webSocketProtocol) != "" &&
		!isBearerWebSocketProtocol(webSocketProtocol)
	if !hasHostnameHeader && !hasAuthQuery && !hasRoutingProtocol {
		return "", false, nil
	}

	requestCopy := request.Clone(request.Context())
	headers := request.Header
	if isBearerWebSocketProtocol(webSocketProtocol) {
		headers = request.Header.Clone()
		headers.Del("Sec-WebSocket-Protocol")
	}

	requested, err := utils.GetCurrentHostnameFromHeader(headers, requestCopy)
	if err != nil {
		return "", true, err
	}

	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", true, errors.New("selected node hostname is empty")
	}

	return requested, true, nil
}

func abortSelectedNodeRequest(c *gin.Context, status int, code, requested string) {
	var data any
	if requested != "" {
		data = map[string]string{"hostname": requested}
	}

	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    data,
	})
}

func EnsureCorrectHost(db *gorm.DB, authService *authService.Service) gin.HandlerFunc {
	localHostname := strings.TrimSpace(hostname)
	var localHostnameErr error
	if localHostname == "" {
		localHostname, localHostnameErr = utils.GetSystemHostname()
		localHostname = strings.TrimSpace(localHostname)
	}

	return func(c *gin.Context) {
		reqHost, selected, err := requestedHostname(c.Request)
		if !selected {
			c.Next()
			return
		}
		if err != nil {
			abortSelectedNodeRequest(c, http.StatusBadRequest, "invalid_selected_node", "")
			return
		}

		if localHostnameErr != nil || localHostname == "" {
			abortSelectedNodeRequest(c, http.StatusInternalServerError, "local_hostname_unavailable", reqHost)
			return
		}

		if reqHost == localHostname {
			c.Next()
			return
		}

		if db == nil {
			abortSelectedNodeRequest(c, http.StatusInternalServerError, "selected_node_lookup_failed", reqHost)
			return
		}

		var node clusterModels.ClusterNode
		if err := db.Where("hostname = ?", reqHost).First(&node).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				abortSelectedNodeRequest(c, http.StatusNotFound, "selected_node_not_found", reqHost)
			} else {
				abortSelectedNodeRequest(c, http.StatusInternalServerError, "selected_node_lookup_failed", reqHost)
			}
			return
		}

		if strings.TrimSpace(node.Status) != "online" {
			abortSelectedNodeRequest(c, http.StatusServiceUnavailable, "selected_node_offline", reqHost)
			return
		}

		apiAddress := strings.TrimSpace(node.API)
		if apiAddress == "" {
			abortSelectedNodeRequest(c, http.StatusServiceUnavailable, "selected_node_unavailable", reqHost)
			return
		}

		injectForwardClusterAuth(c, authService)
		ReverseProxyInsecure(c, fmt.Sprintf("https://%s", apiAddress))
	}
}

func abortPublicDownloadRouting(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

func reverseProxyPublicDownload(c *gin.Context, backend string) {
	remote, err := url.Parse(backend)
	if err != nil {
		abortPublicDownloadRouting(c, http.StatusInternalServerError, "selected_node_routing_failed")
		return
	}

	// The capability is the only credential needed by the public destination.
	// Do not forward browser credentials that may happen to accompany the link.
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
		"X-Cluster-Token",
		"X-Current-Hostname",
	} {
		c.Request.Header.Del(header)
	}

	proxy := newReverseProxy(remote, insecureTransport, false)
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, proxyErr error) {
		if proxyErr != nil && !strings.Contains(proxyErr.Error(), "context canceled") {
			abortPublicDownloadRouting(c, http.StatusBadGateway, "selected_node_forwarding_failed")
		}
	}
	proxy.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

// EnsurePublicDownloadHost routes a signed browser capability to its serving
// node without trusting an arbitrary destination. The destination node remains
// responsible for validating the node-bound signature before opening a file.
func EnsurePublicDownloadHost(db *gorm.DB) gin.HandlerFunc {
	localHostname := strings.TrimSpace(hostname)
	var localHostnameErr error
	if localHostname == "" {
		localHostname, localHostnameErr = utils.GetSystemHostname()
		localHostname = strings.TrimSpace(localHostname)
	}

	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")

		requestedNode := strings.TrimSpace(c.Query("node"))
		if requestedNode == "" {
			c.Next()
			return
		}
		if !utils.IsValidHostname(requestedNode) {
			abortPublicDownloadRouting(c, http.StatusBadRequest, "invalid_selected_node")
			return
		}
		if localHostnameErr != nil || localHostname == "" {
			abortPublicDownloadRouting(c, http.StatusInternalServerError, "local_hostname_unavailable")
			return
		}
		if requestedNode == localHostname {
			c.Next()
			return
		}
		if db == nil {
			abortPublicDownloadRouting(c, http.StatusInternalServerError, "selected_node_lookup_failed")
			return
		}

		var node clusterModels.ClusterNode
		if err := db.Where("hostname = ?", requestedNode).First(&node).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				abortPublicDownloadRouting(c, http.StatusNotFound, "selected_node_not_found")
			} else {
				abortPublicDownloadRouting(c, http.StatusInternalServerError, "selected_node_lookup_failed")
			}
			return
		}
		if strings.TrimSpace(node.Status) != "online" {
			abortPublicDownloadRouting(c, http.StatusServiceUnavailable, "selected_node_offline")
			return
		}
		apiAddress := strings.TrimSpace(node.API)
		if apiAddress == "" {
			abortPublicDownloadRouting(c, http.StatusServiceUnavailable, "selected_node_unavailable")
			return
		}

		reverseProxyPublicDownload(c, fmt.Sprintf("https://%s", apiAddress))
	}
}
