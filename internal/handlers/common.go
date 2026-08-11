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

func resolveForwardClusterToken(c *gin.Context, service *authService.Service) (string, bool, error) {
	token := strings.TrimSpace(c.GetString("Token"))
	scope := c.GetString("AuthScope")
	if scope == "cluster" {
		if token == "" || c.GetString("ClusterTokenUse") != authService.ClusterTokenUseUserProxy ||
			c.GetUint("UserID") == 0 || !c.GetBool("ProxyAdmin") {
			return "", false, fmt.Errorf("validated_user_proxy_required")
		}
		return token, true, nil
	}

	if scope != "local" || service == nil {
		return "", false, fmt.Errorf("local_forward_auth_required")
	}

	clusterToken, err := service.CreateUserProxyJWT(
		c.GetUint("UserID"),
		c.GetString("Username"),
		c.GetString("AuthType"),
	)
	if err != nil {
		return "", false, err
	}
	return clusterToken, false, nil
}

func isForwardedConsolePath(path string) bool {
	if path == "/api/info/terminal" {
		return true
	}
	for _, pattern := range []struct {
		prefix string
		suffix string
	}{
		{prefix: "/api/vnc/"},
		{prefix: "/api/vm/", suffix: "/console"},
		{prefix: "/api/jail/", suffix: "/console"},
	} {
		value := strings.TrimPrefix(strings.TrimSuffix(path, pattern.suffix), pattern.prefix)
		if strings.HasPrefix(path, pattern.prefix) && strings.HasSuffix(path, pattern.suffix) &&
			value != "" && !strings.Contains(value, "/") {
			return true
		}
	}
	return false
}

func prepareForwardClusterAuth(c *gin.Context, service *authService.Service) error {
	clusterToken, reused, err := resolveForwardClusterToken(c, service)
	if err != nil {
		return err
	}

	forwardHop := ""
	if reused {
		rawHop := strings.TrimSpace(c.GetHeader("X-Sylve-Cluster-Forward-Hop"))
		if rawHop != "" && rawHop != "0" {
			return fmt.Errorf("cluster_forward_loop_detected")
		}
		forwardHop = "1"
	}

	for _, header := range []string{
		"Authorization",
		"Proxy-Authorization",
		"Cookie",
		"ClusterToken",
		"X-Cluster-Authorization",
		authService.ClusterTokenHeader,
		authService.ClusterKeyHeader,
		"X-Current-Hostname",
		"X-Sylve-Cluster-Forward-Hop",
		"X-Sylve-Backup-Forwarded-By",
		"X-Sylve-Backup-Forward-Target",
	} {
		c.Request.Header.Del(header)
	}
	if isForwardedConsolePath(c.Request.URL.Path) {
		c.Request.Header.Del("Sec-WebSocket-Protocol")
	}

	query := c.Request.URL.Query()
	query.Del("hash")
	query.Del("auth")
	c.Request.URL.RawQuery = query.Encode()
	c.Request.Header.Set(authService.ClusterTokenHeader, fmt.Sprintf("Bearer %s", clusterToken))
	if forwardHop != "" {
		c.Request.Header.Set("X-Sylve-Cluster-Forward-Hop", forwardHop)
	}
	return nil
}

func requestedHostname(request *http.Request) (string, bool, error) {
	if request == nil {
		return "", false, nil
	}

	_, hasHostnameHeader := request.Header[http.CanonicalHeaderKey("X-Current-Hostname")]
	hasAuthQuery := request.URL != nil && request.Method == http.MethodGet &&
		(request.URL.Path == "/api/system/file-explorer/download" || isForwardedConsolePath(request.URL.Path)) &&
		request.URL.Query().Has("auth")
	if !hasHostnameHeader && !hasAuthQuery {
		return "", false, nil
	}

	requestCopy := request.Clone(request.Context())
	requested, err := utils.GetCurrentHostnameFromHeader(request.Header, requestCopy)
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

		if err := prepareForwardClusterAuth(c, authService); err != nil {
			abortSelectedNodeRequest(c, http.StatusBadGateway, "selected_node_auth_failed", reqHost)
			return
		}
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
		"ClusterToken",
		"X-Cluster-Authorization",
		"X-Cluster-Token",
		"X-Cluster-Key",
		"X-Current-Hostname",
		"X-Sylve-Cluster-Forward-Hop",
		"X-Sylve-Backup-Forwarded-By",
		"X-Sylve-Backup-Forward-Target",
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
