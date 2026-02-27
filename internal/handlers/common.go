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
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
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

func EnsureCorrectHost(db *gorm.DB) gin.HandlerFunc {
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
				ReverseProxyInsecure(c, fmt.Sprintf("https://%s", node.API))
				return
			}

			c.Next()
			return
		}

		c.Next()
	}
}
