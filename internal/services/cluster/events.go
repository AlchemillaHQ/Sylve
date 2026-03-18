// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const leftPanelRefreshFanoutTimeout = 2 * time.Second

type leftPanelRefreshSyncRequest struct {
	Reason string `json:"reason"`
}

func (s *Service) EmitLeftPanelRefreshLocal(reason string) {
	reason = strings.TrimSpace(reason)

	publishLeftPanelRefresh()

	if reason != "" {
		logger.L.Debug().
			Str("reason", reason).
			Msg("left_panel_refresh_published_local")
	}
}

func (s *Service) EmitLeftPanelRefreshClusterWide(reason string) {
	reason = strings.TrimSpace(reason)
	s.EmitLeftPanelRefreshLocal(reason)

	if s == nil || s.Raft == nil || s.AuthService == nil {
		return
	}

	detail := s.Detail()
	if detail == nil || strings.TrimSpace(detail.Hostname) == "" {
		logger.L.Warn().
			Str("reason", reason).
			Msg("left_panel_refresh_fanout_skipped_missing_local_hostname")
		return
	}

	clusterToken, err := s.AuthService.CreateInternalClusterJWT(strings.TrimSpace(detail.Hostname), "")
	if err != nil {
		logger.L.Warn().
			Err(err).
			Str("reason", reason).
			Msg("left_panel_refresh_fanout_token_failed")
		return
	}

	cfgFuture := s.Raft.GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		logger.L.Warn().
			Err(err).
			Str("reason", reason).
			Msg("left_panel_refresh_fanout_raft_config_failed")
		return
	}
	cfg := cfgFuture.Configuration()
	if len(cfg.Servers) <= 1 {
		return
	}

	payload, err := json.Marshal(leftPanelRefreshSyncRequest{Reason: reason})
	if err != nil {
		logger.L.Warn().
			Err(err).
			Str("reason", reason).
			Msg("left_panel_refresh_fanout_payload_failed")
		return
	}

	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	selfNodeID := strings.TrimSpace(s.NodeID)

	for _, server := range cfg.Servers {
		targetNodeID := strings.TrimSpace(string(server.ID))
		if selfNodeID != "" && targetNodeID == selfNodeID {
			continue
		}

		host := strings.TrimSpace(raftAddressHost(string(server.Address)))
		if host == "" {
			continue
		}

		url := fmt.Sprintf(
			"https://%s:%d/api/intra-cluster/events/left-panel-refresh",
			host,
			ClusterEmbeddedHTTPSPort,
		)

		go func(nodeID, endpoint string) {
			_, statusCode, err := utils.HTTPPostJSONWithTimeout(
				endpoint,
				payload,
				headers,
				leftPanelRefreshFanoutTimeout,
			)
			if err != nil {
				logger.L.Warn().
					Err(err).
					Str("target_node_id", nodeID).
					Str("url", endpoint).
					Str("reason", reason).
					Int("status_code", statusCode).
					Msg("left_panel_refresh_fanout_failed")
			}
		}(targetNodeID, url)
	}
}
