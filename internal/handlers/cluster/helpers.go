// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	clusterForwardHopHeader        = "X-Sylve-Cluster-Forward-Hop"
	clusterForwardMaxHops          = 1
	clusterForwardMaxResponseBytes = int64(4 << 20)

	clusterForwardShortReadTimeout  = 15 * time.Second
	clusterForwardValidationTimeout = 70 * time.Second
	clusterForwardDurableTimeout    = 90 * time.Second
)

type clusterForwardTimeoutClass uint8

const (
	clusterForwardShortRead clusterForwardTimeoutClass = iota
	clusterForwardValidation
	clusterForwardDurable
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

func clusterForwardTimeout(class clusterForwardTimeoutClass) time.Duration {
	switch class {
	case clusterForwardShortRead:
		return clusterForwardShortReadTimeout
	case clusterForwardValidation:
		return clusterForwardValidationTimeout
	default:
		return clusterForwardDurableTimeout
	}
}

func clusterForwardClassForRequest(request *http.Request) clusterForwardTimeoutClass {
	if request == nil {
		return clusterForwardDurable
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		return clusterForwardShortRead
	}

	path := strings.ToLower(request.URL.Path)
	if strings.Contains(path, "/validate") ||
		strings.Contains(path, "validation") ||
		strings.Contains(path, "readiness") {
		return clusterForwardValidation
	}
	return clusterForwardDurable
}

func currentClusterForwardHop(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.GetHeader(clusterForwardHopHeader))
	if raw == "" {
		return 0, nil
	}
	hop, err := strconv.Atoi(raw)
	if err != nil || hop < 0 || hop > clusterForwardMaxHops {
		return 0, fmt.Errorf("cluster_forward_loop_detected")
	}
	return hop, nil
}

func nextClusterForwardHop(c *gin.Context) (int, error) {
	if c.GetString("AuthScope") != "cluster" {
		return 1, nil
	}
	hop, err := currentClusterForwardHop(c)
	if err != nil {
		return 0, err
	}
	if hop >= clusterForwardMaxHops {
		return 0, fmt.Errorf("cluster_forward_loop_detected")
	}
	return hop + 1, nil
}

func clusterForwardHeaders(c *gin.Context, cS *cluster.Service, hop int) (map[string]string, error) {
	if cS == nil || cS.AuthService == nil {
		return nil, fmt.Errorf("cluster_forward_auth_service_unavailable")
	}

	var clusterToken string
	switch c.GetString("AuthScope") {
	case "local":
		var err error
		clusterToken, err = cS.AuthService.CreateUserProxyJWT(
			c.GetUint("UserID"),
			c.GetString("Username"),
			c.GetString("AuthType"),
		)
		if err != nil {
			return nil, fmt.Errorf("cluster_forward_auth_failed: %w", err)
		}
	case "cluster":
		clusterToken = strings.TrimSpace(c.GetString("Token"))
		if clusterToken == "" || c.GetString("ClusterTokenUse") != authService.ClusterTokenUseUserProxy ||
			c.GetUint("UserID") == 0 || !c.GetBool("ProxyAdmin") {
			return nil, fmt.Errorf("cluster_forward_auth_failed: validated_user_proxy_required")
		}
	default:
		return nil, fmt.Errorf("cluster_forward_auth_failed: authenticated_scope_required")
	}

	accept := strings.TrimSpace(c.GetHeader("Accept"))
	if accept == "" {
		accept = "application/json"
	}
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}

	requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetString("RequestID"))
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Header("X-Request-ID", requestID)

	headers := map[string]string{
		"Accept":                       accept,
		"Content-Type":                 contentType,
		authService.ClusterTokenHeader: fmt.Sprintf("Bearer %s", clusterToken),
		"X-Request-ID":                 requestID,
		clusterForwardHopHeader:        strconv.Itoa(hop),
	}
	for _, header := range []string{
		"X-Correlation-ID",
		"Traceparent",
		"Idempotency-Key",
	} {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			headers[header] = value
		}
	}
	return headers, nil
}

func performClusterForward(
	c *gin.Context,
	cS *cluster.Service,
	method string,
	targetURL string,
	body []byte,
	class clusterForwardTimeoutClass,
) (clusterForwardResponse, error) {
	hop, err := nextClusterForwardHop(c)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	headers, err := clusterForwardHeaders(c, cS, hop)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	response, err := clusterForwardHTTP(
		c.Request.Context(),
		method,
		targetURL,
		body,
		headers,
		clusterForwardTimeout(class),
	)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	if response.StatusCode < 100 || response.StatusCode > 599 {
		return clusterForwardResponse{}, fmt.Errorf("cluster_forward_response_status_invalid: %d", response.StatusCode)
	}
	return response, nil
}

func writeClusterForwardResponse(c *gin.Context, response clusterForwardResponse) {
	for _, header := range []string{
		"X-Request-ID",
		"X-Correlation-ID",
		"Traceparent",
		"Idempotency-Key",
		"Retry-After",
	} {
		if value := strings.TrimSpace(response.Header.Get(header)); value != "" {
			c.Header(header, value)
		}
	}

	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(response.StatusCode, contentType, response.Body)
}

func writeClusterForwardError(c *gin.Context, message string, err error) {
	status := http.StatusBadGateway
	errorText := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case strings.Contains(errorText, "forward_loop"):
		status = http.StatusLoopDetected
		message = "cluster_forward_loop_detected"
	case strings.Contains(errorText, "auth_service_unavailable"):
		status = http.StatusServiceUnavailable
	case strings.Contains(errorText, "create_cluster_token_failed"):
		status = http.StatusInternalServerError
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

func forwardToLeader(c *gin.Context, cS *cluster.Service) {
	if cS == nil || cS.Raft == nil {
		c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
			Status: "error", Message: "leader_unknown",
		})
		return
	}
	leaderAddr, leaderID := cS.Raft.LeaderWithID()
	if leaderAddr == "" {
		_ = cS.Raft.VerifyLeader().Error()
		c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
			Status: "error", Message: "leader_unknown",
		})
		return
	}

	leaderNodeID := strings.TrimSpace(string(leaderID))
	base := resolveLeaderAPIForForward(cS, leaderNodeID, string(leaderAddr))
	if base == "" {
		c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
			Status: "error", Message: "map_leader_api_failed",
			Error: "could not resolve leader API address",
		})
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if clusterRequestBodyTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "request_body_too_large",
				Error:   "request_body_too_large",
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status: "error", Message: "read_request_body_failed", Error: err.Error(),
		})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	targetURL := fmt.Sprintf("%s%s", strings.TrimRight(base, "/"), c.Request.URL.RequestURI())
	response, err := performClusterForward(
		c,
		cS,
		c.Request.Method,
		targetURL,
		bodyBytes,
		clusterForwardClassForRequest(c.Request),
	)
	if err != nil {
		writeClusterForwardError(c, "leader_forward_failed", err)
		return
	}

	writeClusterForwardResponse(c, response)
}
