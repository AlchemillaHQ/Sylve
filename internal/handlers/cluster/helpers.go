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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

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

// resolveLeaderAPI resolves the leader's HTTPS API base URL.
// Prefers the node's registered API address from the nodes table;
// falls back to deriving it from the Raft address.
func resolveLeaderAPI(cS *cluster.Service, leaderNodeID, leaderRaftAddr string) string {
	leaderNodeID = strings.TrimSpace(leaderNodeID)

	// Try the nodes table first (API may differ from Raft IP).
	if leaderNodeID != "" {
		nodes, err := cS.Nodes()
		if err == nil {
			for _, node := range nodes {
				if strings.TrimSpace(node.NodeUUID) == leaderNodeID {
					if api := strings.TrimSpace(node.API); api != "" {
						return "https://" + api
					}
					break
				}
			}
		}
	}

	// Fall back to port substitution from the Raft address.
	if base, err := mapRaftAddrToAPI(leaderRaftAddr); err == nil {
		return base
	}

	return ""
}

func forwardToLeader(c *gin.Context, cS *cluster.Service) {
	leaderAddr, leaderID := cS.Raft.LeaderWithID()
	if leaderAddr == "" {
		_ = cS.Raft.VerifyLeader().Error()
		c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
			Status: "error", Message: "leader_unknown",
		})
		return
	}

	leaderNodeID := strings.TrimSpace(string(leaderID))
	base := resolveLeaderAPI(cS, leaderNodeID, string(leaderAddr))
	if base == "" {
		c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
			Status: "error", Message: "map_leader_api_failed",
			Error:   "could not resolve leader API address",
		})
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status: "error", Message: "read_request_body_failed", Error: err.Error(),
		})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	userID := c.GetUint("UserID")
	username := strings.TrimSpace(c.GetString("Username"))
	authType := strings.TrimSpace(c.GetString("AuthType"))
	if username == "" {
		hostname, _ := utils.GetSystemHostname()
		if hostname != "" {
			username = hostname
		} else {
			username = "cluster"
		}
	}
	if authType == "" {
		authType = "local"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(userID, username, authType, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status: "error", Message: "create_forward_token_failed", Error: err.Error(),
		})
		return
	}

	targetURL := fmt.Sprintf("%s%s", strings.TrimRight(base, "/"), c.Request.URL.RequestURI())
	respBody, statusCode, err := utils.HTTPRequestJSON(c.Request.Method, targetURL, bodyBytes, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}, 30*time.Second)

	if err != nil {
		c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
			Status: "error", Message: "leader_forward_failed", Error: err.Error(),
		})
		return
	}

	c.Data(statusCode, "application/json", respBody)
}
