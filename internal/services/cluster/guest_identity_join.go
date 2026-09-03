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
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

// GuestIdentityInventoryConflictError preserves the complete deterministic
// report so handlers can return useful conflict details without parsing text.
type GuestIdentityInventoryConflictError struct {
	Report GuestIdentityInventoryReport
}

func (e *GuestIdentityInventoryConflictError) Error() string {
	if e == nil {
		return "guest_identity_inventory_conflict"
	}
	raw, _ := json.Marshal(e.Report.Conflicts)
	return fmt.Sprintf("guest_identity_inventory_conflict: %s", string(raw))
}

func requireCleanGuestIdentityInventory(report GuestIdentityInventoryReport) error {
	if len(report.Conflicts) != 0 {
		return &GuestIdentityInventoryConflictError{Report: report}
	}
	return nil
}

func canonicalSubmittedGuestIdentityInventory(
	nodeID string,
	submitted GuestIdentityInventoryReport,
) (GuestIdentityInventoryReport, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return GuestIdentityInventoryReport{}, fmt.Errorf("joining_node_id_required")
	}
	for _, entry := range submitted.Entries {
		if strings.TrimSpace(entry.NodeID) != nodeID {
			return GuestIdentityInventoryReport{}, fmt.Errorf(
				"joining_inventory_node_mismatch: expected=%s actual=%s",
				nodeID,
				strings.TrimSpace(entry.NodeID),
			)
		}
	}

	canonical := BuildGuestIdentityInventoryReport(submitted.Entries)
	if strings.TrimSpace(submitted.Digest) == "" || submitted.Digest != canonical.Digest {
		return GuestIdentityInventoryReport{}, fmt.Errorf("joining_inventory_digest_mismatch")
	}
	if err := requireCleanGuestIdentityInventory(canonical); err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	return canonical, nil
}

func validateJoinMembership(
	configuration raft.Configuration,
	localNodeID, joiningNodeID string,
	joiningAddress raft.ServerAddress,
) (bool, error) {
	server, err := resolveJoinMembership(configuration, localNodeID, joiningNodeID, joiningAddress)
	if err != nil {
		return false, err
	}
	return server != nil && server.Suffrage == raft.Voter, nil
}

func resolveJoinMembership(
	configuration raft.Configuration,
	localNodeID, joiningNodeID string,
	joiningAddress raft.ServerAddress,
) (*raft.Server, error) {
	localNodeID = strings.TrimSpace(localNodeID)
	joiningNodeID = strings.TrimSpace(joiningNodeID)
	if joiningNodeID == "" {
		return nil, fmt.Errorf("joining_node_id_required")
	}
	if localNodeID != "" && joiningNodeID == localNodeID {
		return nil, fmt.Errorf("joining_node_id_conflicts_with_leader")
	}

	joiningServerID := raft.ServerID(joiningNodeID)
	for _, server := range configuration.Servers {
		if server.ID == joiningServerID {
			if server.Address == joiningAddress {
				copy := server
				return &copy, nil
			}
			return nil, fmt.Errorf("joining_node_id_already_in_use")
		}
		if server.Address == joiningAddress {
			return nil, fmt.Errorf("joining_node_address_already_in_use")
		}
	}

	return nil, nil
}

func (s *Service) checkJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) (GuestIdentityInventoryReport, bool, error) {
	if s == nil || s.DB == nil {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("cluster_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	nodeIP, err := normalizeClusterIPv4(nodeIP, "invalid_joining_node_ip")
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}

	details, err := s.GetClusterDetails()
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	if details.Cluster == nil {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("cluster_not_found")
	}
	if !details.Cluster.Enabled || strings.TrimSpace(details.Cluster.Key) == "" ||
		providedKey == "" || details.Cluster.Key != providedKey {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("invalid_cluster_key")
	}
	if s.Raft == nil {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		address, id := s.Raft.LeaderWithID()
		return GuestIdentityInventoryReport{}, false, fmt.Errorf(
			"not_leader; leader_addr=%s; leader_id=%s",
			string(address),
			string(id),
		)
	}

	canonicalJoiner, err := canonicalSubmittedGuestIdentityInventory(nodeID, submitted)
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("get_config_failed: %w", err)
	}
	localNodeID := strings.TrimSpace(s.NodeID)
	if localNodeID == "" {
		localNodeID = s.LocalNodeID()
	}
	existingServer, err := resolveJoinMembership(
		configurationFuture.Configuration(),
		localNodeID,
		nodeID,
		raft.ServerAddress(RaftServerAddress(nodeIP)),
	)
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}

	claims, err := s.authoritativeGuestIdentityClaims()
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	combined, err := validateJoinInventoryAgainstClaims(strings.TrimSpace(nodeID), canonicalJoiner, claims, existingServer)
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	return combined, existingServer != nil && existingServer.Suffrage == raft.Voter, nil
}

func (s *Service) PreflightJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) (GuestIdentityInventoryReport, error) {
	admittedCtx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	defer release()
	ctx = admittedCtx
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()

	combined, _, err := s.checkJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	return combined, err
}

func (s *Service) StageJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) (ClusterJoinStatus, error) {
	status := ClusterJoinStatus{
		NodeID: strings.TrimSpace(nodeID),
		NodeIP: strings.TrimSpace(nodeIP),
		Phase:  JoinPhaseStaged,
	}
	admittedCtx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return status, err
	}
	defer release()
	ctx = admittedCtx
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()

	_, alreadyVoter, err := s.checkJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	if err != nil {
		return status, err
	}
	if alreadyVoter {
		status.Phase = JoinPhaseComplete
		status.Suffrage = raftSuffrageName(raft.Voter)
		if err := s.PopulateClusterNodes(); err != nil {
			logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_join_retry")
		}
		return status, nil
	}

	serverID := raft.ServerID(strings.TrimSpace(nodeID))
	serverAddress := raft.ServerAddress(RaftServerAddress(nodeIP))
	err = func() error {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()

		configurationFuture := s.Raft.GetConfiguration()
		if err := configurationFuture.Error(); err != nil {
			return fmt.Errorf("get_config_failed: %w", err)
		}
		existingServer, err := resolveJoinMembership(
			configurationFuture.Configuration(),
			s.guestIdentityInventoryLocalNodeID(),
			nodeID,
			serverAddress,
		)
		if err != nil {
			return err
		}
		if existingServer != nil {
			if existingServer.Suffrage == raft.Voter {
				status.Phase = JoinPhaseComplete
				status.Suffrage = raftSuffrageName(raft.Voter)
				return nil
			}
			if existingServer.Suffrage != raft.Nonvoter && existingServer.Suffrage != raft.Staging {
				return fmt.Errorf("joining_node_membership_not_promotable")
			}
			status.Suffrage = raftSuffrageName(existingServer.Suffrage)
			return nil
		}
		candidate := raft.Server{ID: serverID, Address: serverAddress, Suffrage: raft.Nonvoter}
		if err := s.checkUniformVersionsLocked(ctx, &candidate, ""); err != nil {
			return err
		}

		s.replicatedStateMu.Lock()
		defer s.replicatedStateMu.Unlock()
		if err := s.checkpointAndSnapshotLocked(); err != nil {
			return err
		}
		if err := s.Raft.AddNonvoter(serverID, serverAddress, 0, raftApplyTimeout).Error(); err != nil {
			return fmt.Errorf("add_nonvoter_failed: %w", err)
		}
		status.Suffrage = raftSuffrageName(raft.Nonvoter)
		status.TargetIndex = s.Raft.AppliedIndex()
		return nil
	}()
	if err != nil {
		return status, err
	}
	if status.Phase == JoinPhaseComplete {
		if err := s.PopulateClusterNodes(); err != nil {
			logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_join_retry")
		}
		return status, nil
	}

	logger.L.Info().
		Str("node_id", status.NodeID).
		Str("address", string(serverAddress)).
		Uint64("target_index", status.TargetIndex).
		Msg("cluster_join_staged_nonvoter")
	return status, nil
}

func (s *Service) finalizeStagedJoin(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) error {
	ctx, cancel := withReplicatedStateTimeout(ctx)
	defer cancel()
	admittedCtx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = admittedCtx

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()

	_, alreadyVoter, err := s.checkJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	if err != nil {
		return err
	}
	if alreadyVoter {
		return nil
	}

	serverAddress := raft.ServerAddress(RaftServerAddress(nodeIP))
	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return fmt.Errorf("get_config_failed: %w", err)
	}
	server, err := resolveJoinMembership(
		configurationFuture.Configuration(),
		s.guestIdentityInventoryLocalNodeID(),
		nodeID,
		serverAddress,
	)
	if err != nil {
		return err
	}
	if server == nil {
		return fmt.Errorf("joining_node_not_staged")
	}
	if server.Suffrage != raft.Nonvoter && server.Suffrage != raft.Staging {
		return fmt.Errorf("joining_node_membership_not_promotable")
	}
	canonicalJoiner, err := canonicalSubmittedGuestIdentityInventory(nodeID, submitted)
	if err != nil {
		return err
	}
	if err := s.admitStagedJoinGuestIdentities(ctx, nodeID, canonicalJoiner); err != nil {
		return err
	}
	targetIndex := s.Raft.AppliedIndex()
	progress, err := s.fetchJoinProgress(ctx, strings.TrimSpace(nodeID), server.Address, targetIndex)
	if err != nil {
		return fmt.Errorf("replicated_state_catchup_failed: %w", err)
	}
	if progress.AppliedIndex < targetIndex {
		return fmt.Errorf(
			"replicated_state_catchup_pending: target=%d applied=%d",
			targetIndex,
			progress.AppliedIndex,
		)
	}

	verifiedIndex := progress.AppliedIndex
	err = func() error {
		s.clusterJoinMu.Lock()
		defer s.clusterJoinMu.Unlock()
		s.replicatedStateMu.Lock()
		defer s.replicatedStateMu.Unlock()
		if s.Raft == nil || s.Raft.State() != raft.Leader {
			return fmt.Errorf("not_leader")
		}
		configurationFuture = s.Raft.GetConfiguration()
		if err := configurationFuture.Error(); err != nil {
			return fmt.Errorf("get_config_failed: %w", err)
		}
		server, err = resolveJoinMembership(
			configurationFuture.Configuration(),
			s.guestIdentityInventoryLocalNodeID(),
			nodeID,
			serverAddress,
		)
		if err != nil {
			return err
		}
		if server == nil {
			return fmt.Errorf("joining_node_not_staged")
		}
		if server.Suffrage == raft.Voter {
			verifiedIndex = s.Raft.AppliedIndex()
			return nil
		}
		if err := s.checkUniformVersionsLocked(ctx, nil, ""); err != nil {
			return err
		}
		if err := s.checkpointReplicatedStateLocked(); err != nil {
			return err
		}
		reference, err := s.LocalReplicatedStateDigest(
			ctx,
			s.guestIdentityInventoryLocalNodeID(),
			s.Raft.AppliedIndex(),
		)
		if err != nil {
			return err
		}
		verificationCtx, verificationCancel := context.WithTimeout(ctx, joinFinalVerificationTimeout)
		defer verificationCancel()
		verified, err := s.promoteVerifiedNonvoterLocked(
			verificationCtx,
			*server,
			reference,
			progress.RepairFenced,
		)
		verifiedIndex = verified.AppliedIndex
		return err
	}()
	if err != nil {
		return err
	}

	logger.L.Info().
		Str("node_id", strings.TrimSpace(nodeID)).
		Str("address", string(serverAddress)).
		Uint64("verified_index", verifiedIndex).
		Msg("cluster_join_promoted_voter")

	// Do not make the joining node wait for the normal monitor interval before
	// it receives the initial node-health snapshot.
	if err := s.PopulateClusterNodes(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_join")
	}
	return nil
}

func (s *Service) AcceptJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) error {
	status, err := s.StageJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	if err != nil || status.Phase == JoinPhaseComplete {
		return err
	}
	return s.finalizeStagedJoin(ctx, nodeID, nodeIP, providedKey, submitted)
}
