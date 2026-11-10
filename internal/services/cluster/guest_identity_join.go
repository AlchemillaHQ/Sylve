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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
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

// The joiner's identity and guest ID claims are recorded before it's added to
// Raft, so members talk to it from the start and no one takes its IDs while it
// catches up.
func (s *Service) AdmitJoiner(
	ctx context.Context,
	request JoinAdmissionRequest,
	secret string,
	pin [sha256.Size]byte,
) (ClusterJoinStatus, error) {
	nodeID := strings.TrimSpace(request.NodeID)
	status := ClusterJoinStatus{NodeID: nodeID, NodeIP: strings.TrimSpace(request.NodeIP), Phase: JoinPhaseCatchingUp}
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return status, err
	}
	defer release()
	if version := strings.TrimSpace(request.NodeVersion); version != strings.TrimSpace(cmd.Version) {
		return status, &ClusterVersionError{
			Code: "cluster_version_mismatch", NodeID: nodeID, Expected: strings.TrimSpace(cmd.Version), Actual: version,
		}
	}
	nodeIP, err := normalizeClusterIPv4(request.NodeIP, "invalid_joining_node_ip")
	if err != nil {
		return status, err
	}
	keyValid := clusterModels.JoinKeyMatches(s.DB.WithContext(ctx), secret)
	joiner, err := canonicalSubmittedGuestIdentityInventory(nodeID, request.Inventory)
	if err != nil {
		return status, err
	}
	address := raft.ServerAddress(RaftServerAddress(nodeIP))

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	r := s.Raft()
	if r == nil || r.State() != raft.Leader {
		return status, fmt.Errorf("not_leader")
	}
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return status, fmt.Errorf("get_config_failed: %w", err)
	}
	existing, err := resolveJoinMembership(future.Configuration(), s.LocalNodeID(), nodeID, address)
	if err != nil {
		return status, err
	}
	identity := clusterModels.ClusterSSHIdentity{
		NodeUUID: nodeID, SSHUser: "root", SSHHost: nodeIP, SSHPort: ClusterEmbeddedSSHPort,
		PublicKey: strings.TrimSpace(request.SSHPublicKey), CertPin: hex.EncodeToString(pin[:]),
	}
	var known clusterModels.ClusterSSHIdentity
	if err := s.DB.WithContext(ctx).Where("node_uuid = ?", nodeID).Limit(1).Find(&known).Error; err != nil {
		return status, err
	}
	// A join key mustn't let anyone take over a member's identity.
	if known.CertPin != "" && known.CertPin != identity.CertPin {
		return status, fmt.Errorf("joining_node_pin_mismatch")
	}
	// Without the key, only a node already admitted may ask again, with the cert
	// it was admitted with: one that restarted while it caught up.
	if !keyValid && (existing == nil || known.CertPin != identity.CertPin) {
		return status, fmt.Errorf("invalid_cluster_key")
	}
	// Requests name a node by its hostname, so it names one member.
	var named clusterModels.ClusterNode
	if err := s.DB.WithContext(ctx).Where("hostname = ? AND node_uuid <> ?", strings.TrimSpace(request.NodeHostname), nodeID).
		Limit(1).Find(&named).Error; err != nil {
		return status, err
	}
	if named.NodeUUID != "" {
		return status, fmt.Errorf("joining_node_hostname_taken: %s", named.NodeUUID)
	}
	claims, err := s.ListGuestIdentityClaims(ctx)
	if err != nil {
		return status, err
	}
	if err := requireCleanGuestIdentityInventory(joinInventoryReportFromClaims(claims, nodeID, joiner)); err != nil {
		return status, err
	}
	// Retained copies can only carry an ID that already exists, so admission doesn't create one.
	status.ClusterID, err = clusterModels.ReadClusterID(s.DB.WithContext(ctx))
	if err != nil {
		return status, err
	}
	if existing != nil {
		status.Suffrage = raftSuffrageName(existing.Suffrage)
		if existing.Suffrage == raft.Voter {
			status.Phase = JoinPhaseComplete
		}
		status.Pins, err = s.memberPins(ctx)
		return status, err
	}
	// Checked only for a new member: one already admitted may have shared its users since.
	var localUsers []string
	for _, name := range request.LocalUsers {
		if status.ClusterID == "" || request.UserClusters[name] != status.ClusterID {
			localUsers = append(localUsers, name)
		}
	}
	var taken []string
	if len(localUsers) != 0 {
		if err := s.DB.WithContext(ctx).Model(&clusterModels.SylveUser{}).Where("username IN ?", localUsers).
			Order("username").Pluck("username", &taken).Error; err != nil {
			return status, err
		}
	}
	if len(taken) != 0 {
		return status, fmt.Errorf("joining_node_users_taken: %s", strings.Join(taken, ", "))
	}
	if err := s.apply("cluster_ssh_identity", "upsert", identity); err != nil {
		return status, fmt.Errorf("join_identity_record_failed: %w", err)
	}
	if len(joiner.Entries) != 0 {
		entries := make([]clusterModels.GuestIdentityEntry, len(joiner.Entries))
		for i, entry := range joiner.Entries {
			entries[i] = clusterModels.GuestIdentityEntry{GuestKind: entry.GuestType, GuestID: entry.GuestID}
		}
		if err := s.apply("guest_identity", "claim", clusterModels.GuestIdentityClaimSet{OwnerNodeID: nodeID, Entries: entries}); err != nil {
			return status, fmt.Errorf("joining_guest_identity_claim_failed: %w", err)
		}
	}
	// The key admits one node.
	if err := s.apply("cluster_join_key", "set", clusterModels.ClusterJoinKey{}); err != nil {
		return status, fmt.Errorf("join_key_rotation_failed: %w", err)
	}
	if err := r.AddNonvoter(raft.ServerID(nodeID), address, 0, raftApplyTimeout).Error(); err != nil {
		return status, fmt.Errorf("add_nonvoter_failed: %w", err)
	}
	status.Suffrage = raftSuffrageName(raft.Nonvoter)
	logger.L.Info().Str("node_id", nodeID).Str("address", string(address)).Msg("cluster_join_staged_nonvoter")
	status.Pins, err = s.memberPins(ctx)
	return status, err
}

func guestIdentityClaimInventoryEntry(claim clusterModels.GuestIdentityClaim) GuestIdentityInventoryEntry {
	return GuestIdentityInventoryEntry{
		NodeID:    claim.OwnerNodeID,
		GuestType: claim.GuestKind,
		GuestID:   claim.GuestID,
	}
}

func joinInventoryReportFromClaims(
	claims []clusterModels.GuestIdentityClaim,
	joiningNodeID string,
	joining GuestIdentityInventoryReport,
) GuestIdentityInventoryReport {
	entries := make([]GuestIdentityInventoryEntry, 0, len(claims)+len(joining.Entries))
	for _, claim := range claims {
		if strings.TrimSpace(claim.OwnerNodeID) == joiningNodeID {
			continue
		}
		entries = append(entries, guestIdentityClaimInventoryEntry(claim))
	}
	entries = append(entries, joining.Entries...)
	return BuildGuestIdentityInventoryReport(entries)
}
