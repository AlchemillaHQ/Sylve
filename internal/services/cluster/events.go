// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
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
	if s == nil || s.Raft() == nil || s.AuthService == nil {
		return
	}
	cfgFuture := s.Raft().GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		logger.L.Warn().Err(err).Str("reason", reason).Msg("left_panel_refresh_fanout_raft_config_failed")
		return
	}
	payload, err := json.Marshal(leftPanelRefreshSyncRequest{Reason: reason})
	if err != nil {
		return
	}
	index := s.AppliedIndex()
	for _, server := range cfgFuture.Configuration().Servers {
		if string(server.ID) == s.LocalNodeID() {
			continue
		}
		go func(server raft.Server) {
			ctx, cancel := context.WithTimeout(context.Background(), leftPanelRefreshFanoutTimeout)
			defer cancel()
			host := raftAddressHost(string(server.Address))
			if _, err := callNodeAt[struct{}](ctx, s, host, string(server.ID), "left-panel-refresh", payload, index); err != nil {
				logger.L.Warn().Err(err).Str("target_node_id", string(server.ID)).Str("reason", reason).
					Msg("left_panel_refresh_fanout_failed")
			}
		}(server)
	}
}
