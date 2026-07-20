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

func mergeGuestIdentityInventoryReports(
	left, right GuestIdentityInventoryReport,
) GuestIdentityInventoryReport {
	entries := make([]GuestIdentityInventoryEntry, 0, len(left.Entries)+len(right.Entries))
	entries = append(entries, left.Entries...)
	entries = append(entries, right.Entries...)
	return BuildGuestIdentityInventoryReport(entries)
}

func validateJoinMembership(
	configuration raft.Configuration,
	localNodeID, joiningNodeID string,
	joiningAddress raft.ServerAddress,
) (bool, error) {
	localNodeID = strings.TrimSpace(localNodeID)
	joiningNodeID = strings.TrimSpace(joiningNodeID)
	if joiningNodeID == "" {
		return false, fmt.Errorf("joining_node_id_required")
	}
	if localNodeID != "" && joiningNodeID == localNodeID {
		return false, fmt.Errorf("joining_node_id_conflicts_with_leader")
	}

	joiningServerID := raft.ServerID(joiningNodeID)
	for _, server := range configuration.Servers {
		if server.ID == joiningServerID {
			if server.Address == joiningAddress && server.Suffrage == raft.Voter {
				return true, nil
			}
			return false, fmt.Errorf("joining_node_id_already_in_use")
		}
		if server.Address == joiningAddress {
			return false, fmt.Errorf("joining_node_address_already_in_use")
		}
	}

	return false, nil
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

	details, err := s.GetClusterDetails()
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	if details.Cluster == nil {
		return GuestIdentityInventoryReport{}, false, fmt.Errorf("cluster_not_found")
	}
	if details.Cluster.Key != providedKey {
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
	alreadyVoter, err := validateJoinMembership(
		configurationFuture.Configuration(),
		localNodeID,
		nodeID,
		raft.ServerAddress(RaftServerAddress(nodeIP)),
	)
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}

	reports, current, err := s.collectClusterGuestIdentityInventoriesStrict(ctx)
	if err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	if err := requireCleanGuestIdentityInventory(current); err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}

	if alreadyVoter {
		existing, exists := reports[strings.TrimSpace(nodeID)]
		if !exists || existing.Digest != canonicalJoiner.Digest {
			return GuestIdentityInventoryReport{}, false, fmt.Errorf("joining_inventory_changed_for_existing_voter")
		}
		return current, true, nil
	}

	combined := mergeGuestIdentityInventoryReports(current, canonicalJoiner)
	if err := requireCleanGuestIdentityInventory(combined); err != nil {
		return GuestIdentityInventoryReport{}, false, err
	}
	return combined, false, nil
}

// PreflightJoinInventory checks the joining node's submitted inventory against
// every current cluster member before the joining node changes local state.
func (s *Service) PreflightJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) (GuestIdentityInventoryReport, error) {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()

	combined, _, err := s.checkJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	return combined, err
}

// AcceptJoinInventory repeats the inexpensive inventory check immediately
// before membership changes, then adds the node without replacing conflicts.
func (s *Service) AcceptJoinInventory(
	ctx context.Context,
	nodeID, nodeIP, providedKey string,
	submitted GuestIdentityInventoryReport,
) error {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()

	_, alreadyVoter, err := s.checkJoinInventory(ctx, nodeID, nodeIP, providedKey, submitted)
	if err != nil {
		return err
	}
	if alreadyVoter {
		if err := s.ResyncClusterState(); err != nil {
			return err
		}
		return s.PopulateClusterNodes()
	}

	serverID := raft.ServerID(strings.TrimSpace(nodeID))
	serverAddress := raft.ServerAddress(RaftServerAddress(nodeIP))
	if err := s.Raft.AddVoter(serverID, serverAddress, 0, raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("add_voter_failed: %w", err)
	}

	if err := s.ResyncClusterState(); err != nil {
		return err
	}

	// Do not make the joining node wait for the normal monitor interval before
	// it receives the initial node-health snapshot.
	return s.PopulateClusterNodes()
}
