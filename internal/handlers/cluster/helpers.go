// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

const (
	clusterForwardMaxResponseBytes = int64(4 << 20)
	clusterForwardTimeout          = 90 * time.Second
)

type clusterForwardResponse = utils.HTTPReadResponse

func clusterRequestBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func writeClusterJSONBindError(c *gin.Context, err error, invalidMessage string) {
	status := http.StatusBadRequest
	detail := err.Error()
	if clusterRequestBodyTooLarge(err) {
		status = http.StatusRequestEntityTooLarge
		invalidMessage = "request_body_too_large"
		detail = "request_body_too_large"
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: invalidMessage,
		Error:   detail,
		Data:    nil,
	})
}

func writeClusterIPv6Unsupported(c *gin.Context, field string) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: "cluster_ipv6_unsupported",
		Error:   field + "_must_be_ipv4",
		Data:    nil,
	})
}

var clusterForwardHTTP = func(
	ctx context.Context,
	method string,
	targetURL string,
	body []byte,
	headers map[string]string,
	timeout time.Duration,
) (clusterForwardResponse, error) {
	return utils.HTTPRequestReadContext(
		ctx,
		method,
		targetURL,
		body,
		headers,
		timeout,
		clusterForwardMaxResponseBytes,
	)
}

func mapRaftAddrToAPI(raftAddr string) (string, error) {
	host, _, err := net.SplitHostPort(raftAddr)
	if err != nil {
		return "", err
	}

	scheme := "https"
	apiPort := cluster.ClusterEmbeddedHTTPSPort

	return (&url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(apiPort)),
	}).String(), nil
}

// resolveLeaderAPI derives the internal HTTPS endpoint exclusively from the
// authoritative Raft leader address and the fixed embedded port. Asynchronous
// health rows and the configurable public API port must not steer forwarding.
func resolveLeaderAPI(_ *cluster.Service, _, leaderRaftAddr string) string {
	if base, err := mapRaftAddrToAPI(leaderRaftAddr); err == nil {
		return base
	}
	return ""
}

var resolveLeaderAPIForForward = resolveLeaderAPI
