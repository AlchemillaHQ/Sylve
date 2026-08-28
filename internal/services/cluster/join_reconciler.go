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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	joinReconcileInterval        = 3 * time.Second
	joinReconcileAttemptTimeout  = 30 * time.Second
	joinFinalVerificationTimeout = 5 * time.Second
)

type joinHealthData struct {
	SylveVersion string `json:"sylveVersion"`
}

func (s *Service) fetchJoinerVersion(
	ctx context.Context,
	server raft.Server,
	clusterKey string,
) (string, error) {
	if s.joinVersionForNode != nil {
		return s.joinVersionForNode(ctx, server, clusterKey)
	}
	host := strings.TrimSpace(raftAddressHost(string(server.Address)))
	if host == "" {
		return "", fmt.Errorf("joining_node_address_invalid")
	}
	body, statusCode, err := utils.HTTPGetJSONReadContext(
		ctx,
		fmt.Sprintf("https://%s/api/health/basic", ClusterAPIHost(host)),
		map[string]string{
			"Accept":              "application/json",
			auth.ClusterKeyHeader: clusterKey,
		},
	)
	if err != nil {
		return "", fmt.Errorf(
			"joining_node_health_failed: node_id=%s status=%d: %w",
			strings.TrimSpace(string(server.ID)),
			statusCode,
			err,
		)
	}
	var response internal.APIResponse[joinHealthData]
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("joining_node_health_decode_failed: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != "success" {
		return "", fmt.Errorf(
			"joining_node_health_non_success: message=%s error=%s",
			response.Message,
			response.Error,
		)
	}
	version := strings.TrimSpace(response.Data.SylveVersion)
	if version == "" {
		return "", fmt.Errorf("joiner_version_unavailable")
	}
	return version, nil
}

func (s *Service) fetchStagedJoinInventory(
	ctx context.Context,
	server raft.Server,
) (GuestIdentityInventoryReport, error) {
	nodeID := strings.TrimSpace(string(server.ID))
	endpoint, err := s.guestIdentityInventoryRemoteAPI(nodeID, server.Address)
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	if s.AuthService == nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_auth_service_unavailable")
	}
	token, err := s.AuthService.CreateInternalClusterJWT(s.guestIdentityInventoryLocalNodeID())
	if err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_cluster_token_failed: %w",
			err,
		)
	}
	return s.fetchRemoteGuestIdentityInventory(ctx, nodeID, endpoint, token)
}

func (s *Service) reconcileLeaderPendingJoins(ctx context.Context) {
	if s == nil || s.DB == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return
	}
	var clusterRecord clusterModels.Cluster
	if err := s.DB.First(&clusterRecord).Error; err != nil {
		logger.L.Debug().Err(err).Msg("cluster_join_reconcile_cluster_load_failed")
		return
	}
	if !clusterRecord.Enabled || strings.TrimSpace(clusterRecord.Key) == "" {
		return
	}
	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		logger.L.Debug().Err(err).Msg("cluster_join_reconcile_configuration_failed")
		return
	}
	servers := append([]raft.Server(nil), configurationFuture.Configuration().Servers...)
	sort.Slice(servers, func(i, j int) bool {
		return string(servers[i].ID) < string(servers[j].ID)
	})

	for _, server := range servers {
		if server.Suffrage != raft.Nonvoter && server.Suffrage != raft.Staging {
			continue
		}
		if err := ctx.Err(); err != nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, joinReconcileAttemptTimeout)
		version, err := s.fetchJoinerVersion(attemptCtx, server, clusterRecord.Key)
		if err == nil && version != strings.TrimSpace(cmd.Version) {
			err = fmt.Errorf(
				"cluster_version_mismatch: leader=%s,node=%s",
				strings.TrimSpace(cmd.Version),
				version,
			)
		}
		var inventory GuestIdentityInventoryReport
		if err == nil {
			inventory, err = s.fetchStagedJoinInventory(attemptCtx, server)
		}
		if err == nil {
			nodeIP := strings.TrimSpace(raftAddressHost(string(server.Address)))
			err = s.finalizeStagedJoin(
				attemptCtx,
				strings.TrimSpace(string(server.ID)),
				nodeIP,
				clusterRecord.Key,
				inventory,
			)
		}
		cancel()
		if err != nil {
			logger.L.Debug().
				Err(err).
				Str("node_id", strings.TrimSpace(string(server.ID))).
				Str("address", string(server.Address)).
				Msg("cluster_join_reconcile_retry_deferred")
		}
	}
}

func (s *Service) reconcileLocalJoinIntent(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return
	}
	phase := strings.TrimSpace(record.JoinPhase)
	if record.Enabled && s.Raft != nil && s.Raft.State() != raft.Shutdown {
		status, err := s.JoinStatus()
		isBootstrap := record.RaftBootstrap != nil && *record.RaftBootstrap
		if err == nil && status.Phase == JoinPhaseComplete && !isBootstrap {
			s.notifyJoinComplete()
		}
	}
	if strings.TrimSpace(record.JoinNodeID) == "" || phase == "" ||
		phase == JoinPhaseComplete || phase == JoinPhaseFailed {
		return
	}
	if !record.Enabled {
		if s.raftFSM == nil {
			_ = s.updateJoinIntent(
				JoinPhaseStalled,
				"cluster_join_resume_fsm_unavailable",
				false,
				"",
			)
			return
		}
		_ = s.updateJoinIntent(JoinPhaseStarting, "", false, "")
		if err := s.StartAsJoiner(s.raftFSM, record.JoinNodeIP, record.Key); err != nil {
			_ = s.updateJoinIntent(JoinPhaseStalled, err.Error(), false, "")
			logger.L.Debug().Err(err).Msg("cluster_join_local_start_retry_deferred")
			return
		}
	}
	status, err := s.JoinStatus()
	if err == nil {
		if status.Phase == JoinPhaseComplete {
			return
		}
		if status.Suffrage == raftSuffrageName(raft.Nonvoter) ||
			status.Suffrage == raftSuffrageName(raft.Staging) {
			return
		}
	}
	result := s.SubmitJoinIntent(ctx)
	if result.Err != nil && result.Retryable {
		logger.L.Debug().Err(result.Err).Msg("cluster_join_intent_retry_deferred")
	}
}

func (s *Service) runJoinReconciliation(ctx context.Context) {
	s.reconcileLocalJoinIntent(ctx)
	s.reconcileLeaderPendingJoins(ctx)
}

func (s *Service) startJoinReconciler(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		for {
			s.runJoinReconciliation(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(joinReconcileInterval):
			}
		}
	}()
}
