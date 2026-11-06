// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterHandlers "github.com/alchemillahq/sylve/internal/handlers/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
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

func pathNode(path string) (string, bool) {
	rest, named := strings.CutPrefix(path, "/api/nodes/")
	node, _, routed := strings.Cut(rest, "/")
	return node, named && routed && node != ""
}

func stripNodePath(u *url.URL, node string) {
	prefix := "/api/nodes/" + node
	u.Path = "/api" + strings.TrimPrefix(u.Path, prefix)
	if u.RawPath != "" {
		u.RawPath = "/api" + strings.TrimPrefix(u.RawPath, prefix)
	}
}

func isThisNode(node string) bool {
	if nodeID, err := utils.GetSystemUUID(); err == nil && node == nodeID {
		return true
	}
	local := hostname
	if local == "" {
		local, _ = utils.GetSystemHostname()
	}
	return local != "" && node == local
}

func NodePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if node, ok := pathNode(r.URL.Path); ok && isThisNode(node) {
			plain := *r
			plain.URL = new(url.URL)
			*plain.URL = *r.URL
			stripNodePath(plain.URL, node)
			r = &plain
		}
		next.ServeHTTP(w, r)
	})
}

// The admin is authenticated on the plain path (consoles and downloads by their
// query), so the node prefix is stripped first.
func registerNodeRoute(
	api *gin.RouterGroup,
	cS *cluster.Service,
	memberAPIHost func(string) (string, error),
	authenticate gin.HandlerFunc,
) {
	node := api.Group("/nodes/:node")
	node.Use(func(c *gin.Context) {
		stripNodePath(c.Request.URL, c.Param("node"))
		c.Next()
	})
	node.Use(authenticate)
	node.Any("/*path", func(c *gin.Context) {
		node := c.Param("node")
		host, err := memberAPIHost(node)
		if err != nil {
			clusterHandlers.AbortUnknownNode(c, node, err)
			return
		}
		clusterHandlers.ForwardTo(c, cS, host, nil)
	})
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
		abortPublicDownloadRouting(c, http.StatusInternalServerError, "node_unreachable")
		return
	}

	// The capability is the only credential needed by the public destination.
	// Do not forward browser credentials that may happen to accompany the link.
	clusterHandlers.StripForwardHeaders(c.Request.Header)

	proxy := newReverseProxy(remote, utils.IntraClusterTransport{}, false)
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, proxyErr error) {
		if proxyErr != nil && !strings.Contains(proxyErr.Error(), "context canceled") {
			abortPublicDownloadRouting(c, http.StatusBadGateway, "node_unreachable")
		}
	}
	proxy.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

// EnsurePublicDownloadHost routes a signed browser capability to its serving
// node without trusting an arbitrary destination. The destination node remains
// responsible for validating the node-bound signature before opening a file.
func EnsurePublicDownloadHost(memberAPIHost func(string) (string, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")

		requestedNode := strings.TrimSpace(c.Query("node"))
		if requestedNode == "" || isThisNode(requestedNode) {
			c.Next()
			return
		}
		host, err := memberAPIHost(requestedNode)
		if err != nil {
			clusterHandlers.AbortUnknownNode(c, requestedNode, err)
			return
		}
		reverseProxyPublicDownload(c, "https://"+host)
	}
}
