// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

const appliedIndexHeader = "X-Sylve-Applied-Index"

var forwardTransport http.RoundTripper = utils.IntraClusterTransport{}

// The client's credentials mustn't reach a member, and it would take any
// X-Sylve- header as this node's word.
func StripForwardHeaders(header http.Header) {
	for name := range header {
		if strings.HasPrefix(name, "X-Sylve-") {
			delete(header, name)
		}
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", authService.ClusterKeyHeader} {
		header.Del(name)
	}
}

func abortForward(c *gin.Context, status int, code string, data any) {
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{Status: "error", Message: code, Error: code, Data: data})
}

func ForwardTo(c *gin.Context, cS *cluster.Service, host string, body []byte) {
	defer c.Abort()
	if c.GetString("AuthScope") != "local" {
		abortForward(c, http.StatusMisdirectedRequest, "node_forward_second_hop", nil)
		return
	}
	user, err := cS.AuthService.ProxiedUser(c.GetUint("UserID"), c.GetString("AuthType"))
	if err != nil {
		abortForward(c, http.StatusBadGateway, "node_forward_auth_failed", nil)
		return
	}
	if body != nil {
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: "https", Host: host})
			r.SetXForwarded()
			StripForwardHeaders(r.Out.Header)
			// When this node's gzip middleware compresses the answer, the member
			// mustn't as well. /api/nodes/ routes are excluded and pass the member's
			// compression through.
			if c.Writer.Header().Get("Content-Encoding") == "gzip" {
				r.Out.Header.Del("Accept-Encoding")
			}
			r.Out.Header.Set(authService.ProxyUserHeader, user.Header())
			query := r.Out.URL.Query()
			query.Del("hash")
			query.Del("auth")
			r.Out.URL.RawQuery = query.Encode()
		},
		Transport:     forwardTransport,
		FlushInterval: 50 * time.Millisecond,
		ModifyResponse: func(response *http.Response) error {
			// A write's answer waits until this node reflects it; a read, a
			// stream or a console doesn't wait on a lagging node.
			if c.Request.Method == http.MethodGet {
				return nil
			}
			if index, err := strconv.ParseUint(response.Header.Get(appliedIndexHeader), 10, 64); err == nil {
				cS.WaitForLocalApply(c.Request.Context(), index)
			}
			return nil
		},
		ErrorHandler: func(_ http.ResponseWriter, r *http.Request, err error) {
			switch {
			case clusterRequestBodyTooLarge(err):
				abortForward(c, http.StatusRequestEntityTooLarge, "request_body_too_large", nil)
			case r.Context().Err() == nil:
				abortForward(c, http.StatusBadGateway, "node_unreachable", nil)
			}
		},
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func ForwardToNode(c *gin.Context, cS *cluster.Service, node string, body []byte) {
	host, err := cS.MemberAPIHost(node)
	if err != nil {
		AbortUnknownNode(c, node, err)
		return
	}
	ForwardTo(c, cS, host, body)
}

func AbortUnknownNode(c *gin.Context, node string, err error) {
	data := map[string]string{"node": node}
	switch {
	case errors.Is(err, cluster.ErrNodeNotFound):
		abortForward(c, http.StatusNotFound, "node_not_found", data)
	case errors.Is(err, cluster.ErrNodeNameAmbiguous):
		abortForward(c, http.StatusConflict, "node_name_ambiguous", data)
	default:
		abortForward(c, http.StatusServiceUnavailable, "node_unavailable", data)
	}
}

func forwardToLeader(c *gin.Context, cS *cluster.Service, body []byte) {
	var leaderID raft.ServerID
	if r := cS.Raft(); r != nil {
		_, leaderID = r.LeaderWithID()
	}
	if leaderID == "" {
		abortForward(c, http.StatusServiceUnavailable, "leader_unknown", nil)
		return
	}
	ForwardToNode(c, cS, string(leaderID), body)
}

func forOtherNode(cS *cluster.Service, nodeID string) bool {
	nodeID = strings.TrimSpace(nodeID)
	return nodeID != "" && nodeID != cS.LocalNodeID()
}

func LeaderOnly(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS != nil {
			if r := cS.Raft(); r != nil && r.State() != raft.Leader {
				forwardToLeader(c, cS, nil)
				return
			}
		}
		c.Next()
	}
}

func StampAppliedIndex(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS != nil && cluster.PeerNode(c.Request) != "" {
			c.Writer = &appliedIndexWriter{ResponseWriter: c.Writer, cS: cS}
		}
		c.Next()
	}
}

// The index is read just before the response goes out, after the handler's own
// write.
type appliedIndexWriter struct {
	gin.ResponseWriter
	cS *cluster.Service
}

func (w *appliedIndexWriter) stamp() {
	if !w.Written() {
		w.Header().Set(appliedIndexHeader, strconv.FormatUint(w.cS.AppliedIndex(), 10))
	}
}

func (w *appliedIndexWriter) WriteHeaderNow() {
	w.stamp()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *appliedIndexWriter) Write(data []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(data)
}

func (w *appliedIndexWriter) WriteString(data string) (int, error) {
	w.stamp()
	return w.ResponseWriter.WriteString(data)
}
