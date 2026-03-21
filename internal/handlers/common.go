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
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var hostname string

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

func EnsureCorrectHost(db *gorm.DB, authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var err error

		if hostname == "" {
			hostname, err = utils.GetSystemHostname()
			if err != nil {
				c.Next()
				return
			}
		}

		requestCopy := c.Request.Clone(c.Request.Context())

		reqHost, err := utils.GetCurrentHostnameFromHeader(c.Request.Header, requestCopy)
		if err != nil {
			c.Next()
			return
		}

		if reqHost != hostname {
			var node clusterModels.ClusterNode
			if err := db.Where("hostname = ?", reqHost).First(&node).Error; err != nil {
				c.Next()
				return
			}

			if node.Status == "online" {
				injectForwardClusterAuth(c, authService)
				ReverseProxyInsecure(c, fmt.Sprintf("https://%s", node.API))
				return
			}

			c.Next()
			return
		}

		c.Next()
	}
}
