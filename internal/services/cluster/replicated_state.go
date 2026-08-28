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
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const replicatedStateOperationTimeout = 45 * time.Second

const (
	ReplicatedStateRepairFence   = "fence"
	ReplicatedStateRepairReset   = "reset"
	ReplicatedStateRepairUnfence = "unfence"
)

type ReplicatedStateDigest struct {
	NodeID       string `json:"nodeId"`
	AppliedIndex uint64 `json:"appliedIndex"`
	Digest       string `json:"digest"`
	RepairFenced bool   `json:"repairFenced"`
}

type ReplicatedStateRepairRequest struct {
	Action         string `json:"action"`
	ExpectedNodeID string `json:"expectedNodeId"`
}

type ClusterStateMemberResult struct {
	NodeID       string `json:"nodeId"`
	Address      string `json:"address"`
	Suffrage     string `json:"suffrage"`
	AppliedIndex uint64 `json:"appliedIndex,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}

type ClusterStateResyncResult struct {
	LeaderNodeID          string                     `json:"leaderNodeId"`
	ReferenceAppliedIndex uint64                     `json:"referenceAppliedIndex"`
	ReferenceDigest       string                     `json:"referenceDigest"`
	Members               []ClusterStateMemberResult `json:"members"`
}

type ReplicatedStateRepairBlockedError struct {
	NodeID       string
	Dependencies []PeerRemovalDependency
}

func (e *ReplicatedStateRepairBlockedError) Error() string {
	if e == nil {
		return "replicated_state_repair_not_quiescent"
	}
	return fmt.Sprintf(
		"replicated_state_repair_not_quiescent: node_id=%s dependencies=%d",
		e.NodeID,
		len(e.Dependencies),
	)
}

func withReplicatedStateTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, replicatedStateOperationTimeout)
}

func raftSuffrageName(suffrage raft.ServerSuffrage) string {
	switch suffrage {
	case raft.Voter:
		return "voter"
	case raft.Nonvoter:
		return "nonvoter"
	case raft.Staging:
		return "staging"
	default:
		return fmt.Sprintf("unknown_%d", suffrage)
	}
}

func (s *Service) waitForReplicatedStateAppliedIndex(ctx context.Context, minimum uint64) (uint64, error) {
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return 0, fmt.Errorf("replicated_state_raft_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		applied := s.Raft.AppliedIndex()
		if applied >= minimum {
			return applied, nil
		}
		select {
		case <-ctx.Done():
			return applied, fmt.Errorf("replicated_state_catchup_failed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) WaitForReplicatedStateAppliedIndex(ctx context.Context, minimum uint64) (uint64, error) {
	if minimum == 0 {
		if s != nil && s.Raft != nil && s.Raft.State() != raft.Shutdown {
			return s.Raft.AppliedIndex(), nil
		}
		return 0, nil
	}
	return s.waitForReplicatedStateAppliedIndex(ctx, minimum)
}

func (s *Service) LocalReplicatedStateDigest(
	ctx context.Context,
	expectedNodeID string,
	minimumIndex uint64,
) (ReplicatedStateDigest, error) {
	result := ReplicatedStateDigest{}
	if s == nil || s.DB == nil {
		return result, fmt.Errorf("cluster_service_not_initialized")
	}
	result.NodeID = strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	expectedNodeID = strings.TrimSpace(expectedNodeID)
	if result.NodeID == "" {
		return result, fmt.Errorf("replicated_state_local_node_id_unavailable")
	}
	if expectedNodeID != "" && expectedNodeID != result.NodeID {
		return result, fmt.Errorf(
			"replicated_state_node_id_mismatch: expected=%s actual=%s",
			expectedNodeID,
			result.NodeID,
		)
	}
	if _, err := s.waitForReplicatedStateAppliedIndex(ctx, minimumIndex); err != nil {
		return result, err
	}
	if s.stateFSM == nil {
		return result, fmt.Errorf("replicated_state_fsm_unavailable")
	}

	digest, appliedIndex, err := s.stateFSM.StateDigest(func() uint64 {
		if s.Raft == nil {
			return 0
		}
		return s.Raft.AppliedIndex()
	})
	if err != nil {
		return result, fmt.Errorf("capture_replicated_state_digest: %w", err)
	}
	if appliedIndex < minimumIndex {
		return result, fmt.Errorf(
			"replicated_state_applied_index_too_old: minimum=%d actual=%d",
			minimumIndex,
			appliedIndex,
		)
	}
	result.AppliedIndex = appliedIndex
	result.Digest = digest
	result.RepairFenced = s.stateRepair.Load()
	return result, nil
}

func (s *Service) replicatedStateRemoteAPI(
	nodeID string,
	address raft.ServerAddress,
) (string, error) {
	host := strings.TrimSpace(raftAddressHost(string(address)))
	if host == "" {
		return "", fmt.Errorf(
			"replicated_state_remote_api_resolve_failed: node_id=%s: empty_raft_address",
			nodeID,
		)
	}
	endpoint := net.JoinHostPort(host, fmt.Sprintf("%d", ClusterEmbeddedHTTPSPort))
	normalized, err := normalizeGuestIdentityInventoryAPIEndpoint(endpoint)
	if err != nil {
		return "", fmt.Errorf(
			"replicated_state_remote_api_resolve_failed: node_id=%s: %w",
			nodeID,
			err,
		)
	}
	return normalized, nil
}

func (s *Service) replicatedStateInternalToken() (string, error) {
	if s == nil || s.AuthService == nil {
		return "", fmt.Errorf("replicated_state_auth_service_unavailable")
	}
	nodeID := strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	token, err := s.AuthService.CreateInternalClusterJWT(nodeID)
	if err != nil {
		return "", fmt.Errorf("replicated_state_cluster_token_failed: %w", err)
	}
	return token, nil
}

func (s *Service) fetchReplicatedStateDigest(
	ctx context.Context,
	nodeID string,
	address raft.ServerAddress,
	minimumIndex uint64,
) (ReplicatedStateDigest, error) {
	if s.stateDigestForNode != nil {
		return s.stateDigestForNode(ctx, nodeID, address, minimumIndex)
	}
	endpoint, err := s.replicatedStateRemoteAPI(nodeID, address)
	if err != nil {
		return ReplicatedStateDigest{}, err
	}
	token, err := s.replicatedStateInternalToken()
	if err != nil {
		return ReplicatedStateDigest{}, err
	}
	query := url.Values{}
	query.Set("expectedNodeId", nodeID)
	query.Set("minimumRaftAppliedIndex", fmt.Sprint(minimumIndex))
	requestURL := fmt.Sprintf(
		"https://%s/api/intra-cluster/replicated-state?%s",
		endpoint,
		query.Encode(),
	)
	body, statusCode, err := utils.HTTPGetJSONReadContext(ctx, requestURL, map[string]string{
		"Accept":          "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", token),
	})
	if err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_request_failed: node_id=%s status=%d: %w",
			nodeID,
			statusCode,
			err,
		)
	}
	var response internal.APIResponse[ReplicatedStateDigest]
	if err := json.Unmarshal(body, &response); err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_decode_failed: node_id=%s: %w",
			nodeID,
			err,
		)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != "success" {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_non_success: node_id=%s message=%s error=%s",
			nodeID,
			response.Message,
			response.Error,
		)
	}
	if strings.TrimSpace(response.Data.NodeID) != strings.TrimSpace(nodeID) {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_node_id_mismatch: expected=%s actual=%s",
			nodeID,
			response.Data.NodeID,
		)
	}
	if response.Data.AppliedIndex < minimumIndex {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_index_too_old: node_id=%s minimum=%d actual=%d",
			nodeID,
			minimumIndex,
			response.Data.AppliedIndex,
		)
	}
	if strings.TrimSpace(response.Data.Digest) == "" {
		return ReplicatedStateDigest{}, fmt.Errorf(
			"replicated_state_remote_digest_empty: node_id=%s",
			nodeID,
		)
	}
	return response.Data, nil
}

func (s *Service) requestReplicatedStateRepair(
	ctx context.Context,
	nodeID string,
	address raft.ServerAddress,
	request ReplicatedStateRepairRequest,
) error {
	if s.stateRepairForNode != nil {
		return s.stateRepairForNode(ctx, nodeID, address, request)
	}
	endpoint, err := s.replicatedStateRemoteAPI(nodeID, address)
	if err != nil {
		return err
	}
	token, err := s.replicatedStateInternalToken()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal_replicated_state_repair_request: %w", err)
	}
	body, statusCode, err := utils.HTTPPostJSONWithTimeoutContext(
		ctx,
		fmt.Sprintf("https://%s/api/intra-cluster/replicated-state-repair", endpoint),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", token),
		},
		replicatedStateOperationTimeout,
	)
	if err != nil {
		return fmt.Errorf(
			"replicated_state_repair_remote_request_failed: node_id=%s action=%s status=%d body=%s: %w",
			nodeID,
			request.Action,
			statusCode,
			strings.TrimSpace(string(body)),
			err,
		)
	}
	var response internal.APIResponse[any]
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf(
			"replicated_state_repair_remote_decode_failed: node_id=%s action=%s: %w",
			nodeID,
			request.Action,
			err,
		)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != "success" {
		return fmt.Errorf(
			"replicated_state_repair_remote_non_success: node_id=%s action=%s message=%s error=%s",
			nodeID,
			request.Action,
			response.Message,
			response.Error,
		)
	}
	return nil
}

func (s *Service) SetReplicatedStateRepairFence(expectedNodeID string, fenced bool) error {
	if s == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	localNodeID := strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	expectedNodeID = strings.TrimSpace(expectedNodeID)
	if localNodeID == "" || expectedNodeID == "" || localNodeID != expectedNodeID {
		return fmt.Errorf(
			"replicated_state_repair_node_id_mismatch: expected=%s actual=%s",
			expectedNodeID,
			localNodeID,
		)
	}
	s.stateRepair.Store(fenced)
	return nil
}

func (s *Service) ResetReplicatedStateForRepair(expectedNodeID string) error {
	if err := s.SetReplicatedStateRepairFence(expectedNodeID, true); err != nil {
		return err
	}
	if s.raftFSM == nil {
		return fmt.Errorf("replicated_state_repair_fsm_unavailable")
	}

	var clusterRecord clusterModels.Cluster
	if err := s.DB.First(&clusterRecord).Error; err != nil {
		return fmt.Errorf("replicated_state_repair_cluster_load_failed: %w", err)
	}
	if !clusterRecord.Enabled {
		return fmt.Errorf("replicated_state_repair_cluster_disabled")
	}
	raftIP := strings.TrimSpace(clusterRecord.RaftIP)
	if raftIP == "" {
		return fmt.Errorf("replicated_state_repair_raft_ip_unavailable")
	}

	if err := s.stopRaftRuntime(); err != nil {
		return fmt.Errorf("replicated_state_repair_stop_failed: %w", err)
	}
	if err := s.DB.Transaction(clusterModels.ClearReplicatedStateTx); err != nil {
		return fmt.Errorf("replicated_state_repair_clear_failed: %w", err)
	}
	if err := s.CleanRaftDir(); err != nil {
		return fmt.Errorf("replicated_state_repair_raft_clear_failed: %w", err)
	}
	if _, err := s.setupRaftAtIP(false, s.raftFSM, raftIP); err != nil {
		return fmt.Errorf("replicated_state_repair_restart_failed: %w", err)
	}
	return nil
}

func replicatedStateActiveDependencies(
	dependencies []PeerRemovalDependency,
) []PeerRemovalDependency {
	active := make([]PeerRemovalDependency, 0)
	for _, dependency := range dependencies {
		switch dependency.Kind {
		case PeerRemovalDependencyBackupOperation,
			PeerRemovalDependencyReplicationOperation,
			PeerRemovalDependencyRestoreOperation,
			PeerRemovalDependencyGuestOperation,
			PeerRemovalDependencyRunnerRebind:
			active = append(active, dependency)
		}
	}
	return active
}

func (s *Service) checkpointAndSnapshotLocked() error {
	if s == nil || s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if err := s.checkpointReplicatedStateLocked(); err != nil {
		return err
	}
	originalConfig := s.Raft.ReloadableConfig()
	snapshotConfig := originalConfig
	// The rebuild starts with an empty Raft store. Retaining even the tiny
	// checkpoint log would let it replay that no-op without installing the
	// authoritative database image.
	snapshotConfig.TrailingLogs = 0
	if err := s.Raft.ReloadConfig(snapshotConfig); err != nil {
		return fmt.Errorf("replicated_state_snapshot_config_failed: %w", err)
	}
	snapshotErr := s.Raft.Snapshot().Error()
	restoreConfigErr := s.Raft.ReloadConfig(originalConfig)
	if snapshotErr != nil && !errors.Is(snapshotErr, raft.ErrNothingNewToSnapshot) {
		return fmt.Errorf("replicated_state_snapshot_failed: %w", snapshotErr)
	}
	if restoreConfigErr != nil {
		return fmt.Errorf("replicated_state_snapshot_config_restore_failed: %w", restoreConfigErr)
	}
	return nil
}

func (s *Service) checkpointReplicatedStateLocked() error {
	if s == nil || s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if err := s.applyRaftCommandUnlocked(clusterModels.Command{
		Type:   "cluster_state",
		Action: "checkpoint",
		Data:   []byte("{}"),
	}); err != nil {
		return fmt.Errorf("replicated_state_checkpoint_failed: %w", err)
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("replicated_state_barrier_failed: %w", err)
	}
	return nil
}

func (s *Service) waitForMemberDigest(
	ctx context.Context,
	server raft.Server,
	reference ReplicatedStateDigest,
) (ReplicatedStateDigest, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last ReplicatedStateDigest
	var lastErr error
	for {
		last, lastErr = s.fetchReplicatedStateDigest(
			ctx,
			strings.TrimSpace(string(server.ID)),
			server.Address,
			reference.AppliedIndex,
		)
		if lastErr == nil && last.Digest == reference.Digest {
			return last, nil
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return last, fmt.Errorf("replicated_state_verification_failed: %w", lastErr)
			}
			return last, fmt.Errorf(
				"replicated_state_digest_mismatch: expected=%s actual=%s: %w",
				reference.Digest,
				last.Digest,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func (s *Service) repairReplicatedStateMemberLocked(
	ctx context.Context,
	server raft.Server,
	reference ReplicatedStateDigest,
) (ReplicatedStateDigest, error) {
	nodeID := strings.TrimSpace(string(server.ID))
	dependencies, err := s.peerRemovalDependencies(nodeID)
	if err != nil {
		return ReplicatedStateDigest{}, err
	}
	if active := replicatedStateActiveDependencies(dependencies); len(active) != 0 {
		return ReplicatedStateDigest{}, &ReplicatedStateRepairBlockedError{
			NodeID:       nodeID,
			Dependencies: active,
		}
	}

	request := ReplicatedStateRepairRequest{
		Action:         ReplicatedStateRepairFence,
		ExpectedNodeID: nodeID,
	}
	if err := s.requestReplicatedStateRepair(ctx, nodeID, server.Address, request); err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf("replicated_state_repair_fence_failed: %w", err)
	}
	fenced := true
	removed := false
	defer func() {
		if fenced && !removed {
			_ = s.requestReplicatedStateRepair(ctx, nodeID, server.Address, ReplicatedStateRepairRequest{
				Action:         ReplicatedStateRepairUnfence,
				ExpectedNodeID: nodeID,
			})
		}
	}()

	if err := s.Raft.RemoveServer(server.ID, 0, raftApplyTimeout).Error(); err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf("replicated_state_repair_remove_failed: %w", err)
	}
	removed = true
	if err := s.requestReplicatedStateRepair(ctx, nodeID, server.Address, ReplicatedStateRepairRequest{
		Action:         ReplicatedStateRepairReset,
		ExpectedNodeID: nodeID,
	}); err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf("replicated_state_repair_reset_failed: %w", err)
	}
	if err := s.Raft.AddNonvoter(server.ID, server.Address, 0, raftApplyTimeout).Error(); err != nil {
		return ReplicatedStateDigest{}, fmt.Errorf("replicated_state_repair_add_nonvoter_failed: %w", err)
	}
	verified, err := s.waitForMemberDigest(ctx, raft.Server{
		ID:       server.ID,
		Address:  server.Address,
		Suffrage: raft.Nonvoter,
	}, reference)
	if err != nil {
		return verified, err
	}
	if err := s.Raft.AddVoter(server.ID, server.Address, 0, raftApplyTimeout).Error(); err != nil {
		return verified, fmt.Errorf("replicated_state_repair_promote_failed: %w", err)
	}
	if err := s.requestReplicatedStateRepair(ctx, nodeID, server.Address, ReplicatedStateRepairRequest{
		Action:         ReplicatedStateRepairUnfence,
		ExpectedNodeID: nodeID,
	}); err != nil {
		return verified, fmt.Errorf("replicated_state_repair_unfence_failed: %w", err)
	}
	fenced = false
	return verified, nil
}

func (s *Service) promoteCaughtUpNonvoterLocked(
	ctx context.Context,
	server raft.Server,
	reference ReplicatedStateDigest,
) (ReplicatedStateDigest, error) {
	verified, err := s.waitForMemberDigest(ctx, server, reference)
	if err != nil {
		return verified, err
	}
	if err := s.Raft.AddVoter(server.ID, server.Address, 0, raftApplyTimeout).Error(); err != nil {
		return verified, fmt.Errorf("replicated_state_promote_nonvoter_failed: %w", err)
	}
	if err := s.requestReplicatedStateRepair(
		ctx,
		strings.TrimSpace(string(server.ID)),
		server.Address,
		ReplicatedStateRepairRequest{
			Action:         ReplicatedStateRepairUnfence,
			ExpectedNodeID: strings.TrimSpace(string(server.ID)),
		},
	); err != nil {
		return verified, fmt.Errorf("replicated_state_promote_unfence_failed: %w", err)
	}
	return verified, nil
}

func (s *Service) promoteVerifiedNonvoterLocked(
	ctx context.Context,
	server raft.Server,
	reference ReplicatedStateDigest,
	repairFenced bool,
) (ReplicatedStateDigest, error) {
	verified, err := s.fetchReplicatedStateDigest(
		ctx,
		strings.TrimSpace(string(server.ID)),
		server.Address,
		reference.AppliedIndex,
	)
	if err != nil {
		return verified, fmt.Errorf("replicated_state_verification_failed: %w", err)
	}
	if verified.Digest != reference.Digest {
		return verified, fmt.Errorf(
			"replicated_state_digest_mismatch: expected=%s actual=%s",
			reference.Digest,
			verified.Digest,
		)
	}
	if err := s.Raft.AddVoter(server.ID, server.Address, 0, raftApplyTimeout).Error(); err != nil {
		return verified, fmt.Errorf("replicated_state_promote_nonvoter_failed: %w", err)
	}
	if !repairFenced {
		return verified, nil
	}
	if err := s.requestReplicatedStateRepair(
		ctx,
		strings.TrimSpace(string(server.ID)),
		server.Address,
		ReplicatedStateRepairRequest{
			Action:         ReplicatedStateRepairUnfence,
			ExpectedNodeID: strings.TrimSpace(string(server.ID)),
		},
	); err != nil {
		return verified, fmt.Errorf("replicated_state_promote_unfence_failed: %w", err)
	}
	return verified, nil
}

func (s *Service) resyncClusterStateLocked(ctx context.Context) (ClusterStateResyncResult, error) {
	result := ClusterStateResyncResult{Members: []ClusterStateMemberResult{}}
	if s == nil || s.Raft == nil {
		return result, fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		address, id := s.Raft.LeaderWithID()
		return result, fmt.Errorf(
			"not_leader; leader_addr=%s; leader_id=%s",
			string(address),
			string(id),
		)
	}
	result.LeaderNodeID = strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	if result.LeaderNodeID == "" {
		return result, fmt.Errorf("replicated_state_leader_id_unavailable")
	}
	if err := s.checkpointAndSnapshotLocked(); err != nil {
		return result, err
	}
	reference, err := s.LocalReplicatedStateDigest(ctx, result.LeaderNodeID, s.Raft.AppliedIndex())
	if err != nil {
		return result, err
	}
	result.ReferenceAppliedIndex = reference.AppliedIndex
	result.ReferenceDigest = reference.Digest

	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return result, fmt.Errorf("replicated_state_get_configuration_failed: %w", err)
	}
	servers := append([]raft.Server(nil), configurationFuture.Configuration().Servers...)
	sort.Slice(servers, func(i, j int) bool {
		return string(servers[i].ID) < string(servers[j].ID)
	})

	for _, server := range servers {
		member := ClusterStateMemberResult{
			NodeID:   strings.TrimSpace(string(server.ID)),
			Address:  string(server.Address),
			Suffrage: raftSuffrageName(server.Suffrage),
			Status:   "checking",
		}
		if member.NodeID == result.LeaderNodeID {
			member.AppliedIndex = reference.AppliedIndex
			member.Digest = reference.Digest
			member.Status = "matched"
			result.Members = append(result.Members, member)
			continue
		}

		observed, fetchErr := s.fetchReplicatedStateDigest(
			ctx,
			member.NodeID,
			server.Address,
			reference.AppliedIndex,
		)
		member.AppliedIndex = observed.AppliedIndex
		member.Digest = observed.Digest
		if fetchErr != nil {
			member.Status = "unreachable"
			member.Error = fetchErr.Error()
			result.Members = append(result.Members, member)
			return result, fetchErr
		}

		if observed.Digest == reference.Digest {
			if server.Suffrage == raft.Voter {
				member.Status = "matched"
				if observed.RepairFenced {
					if err := s.requestReplicatedStateRepair(
						ctx,
						member.NodeID,
						server.Address,
						ReplicatedStateRepairRequest{
							Action:         ReplicatedStateRepairUnfence,
							ExpectedNodeID: member.NodeID,
						},
					); err != nil {
						member.Status = "unfence_failed"
						member.Error = err.Error()
						result.Members = append(result.Members, member)
						return result, err
					}
					member.Status = "unfenced"
				}
				result.Members = append(result.Members, member)
				continue
			}
			verified, promoteErr := s.promoteCaughtUpNonvoterLocked(ctx, server, reference)
			member.AppliedIndex = verified.AppliedIndex
			member.Digest = verified.Digest
			if promoteErr != nil {
				member.Status = "promotion_failed"
				member.Error = promoteErr.Error()
				result.Members = append(result.Members, member)
				return result, promoteErr
			}
			member.Suffrage = "voter"
			member.Status = "promoted"
			result.Members = append(result.Members, member)
			continue
		}
		verified, repairErr := s.repairReplicatedStateMemberLocked(ctx, server, reference)
		member.AppliedIndex = verified.AppliedIndex
		member.Digest = verified.Digest
		if repairErr != nil {
			member.Status = "repair_failed"
			member.Error = repairErr.Error()
			result.Members = append(result.Members, member)
			return result, repairErr
		}
		member.Suffrage = "voter"
		member.Status = "rebuilt"
		result.Members = append(result.Members, member)
	}

	return result, nil
}

func (s *Service) ResyncClusterStateWithResult(ctx context.Context) (ClusterStateResyncResult, error) {
	ctx, cancel := withReplicatedStateTimeout(ctx)
	defer cancel()

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	s.replicatedStateMu.Lock()
	defer s.replicatedStateMu.Unlock()
	return s.resyncClusterStateLocked(ctx)
}
