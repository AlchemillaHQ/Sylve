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
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
)

func guestIdentityJoinToken(nodeID, inventoryDigest string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	inventoryDigest = strings.TrimSpace(inventoryDigest)
	if nodeID == "" || inventoryDigest == "" {
		return "", fmt.Errorf("joining_inventory_identity_required")
	}
	sum := sha256.Sum256([]byte("sylve-guest-identity-join-v1\x00" + nodeID + "\x00" + inventoryDigest))
	return hex.EncodeToString(sum[:]), nil
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
	excludeJoiningClaims bool,
) GuestIdentityInventoryReport {
	entries := make([]GuestIdentityInventoryEntry, 0, len(claims)+len(joining.Entries))
	for _, claim := range claims {
		if excludeJoiningClaims && strings.TrimSpace(claim.OwnerNodeID) == joiningNodeID {
			continue
		}
		entries = append(entries, guestIdentityClaimInventoryEntry(claim))
	}
	entries = append(entries, joining.Entries...)
	return BuildGuestIdentityInventoryReport(entries)
}

func validateJoinInventoryAgainstClaims(
	joiningNodeID string,
	joining GuestIdentityInventoryReport,
	claims []clusterModels.GuestIdentityClaim,
	existingServer *raft.Server,
) (GuestIdentityInventoryReport, error) {
	joiningNodeID = strings.TrimSpace(joiningNodeID)
	joinToken, err := guestIdentityJoinToken(joiningNodeID, joining.Digest)
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	byID := make(map[uint]clusterModels.GuestIdentityClaim, len(claims))
	ownedCount := 0
	for _, claim := range claims {
		byID[claim.GuestID] = claim
		if strings.TrimSpace(claim.OwnerNodeID) == joiningNodeID {
			ownedCount++
		}
	}

	allowExisting := existingServer != nil
	alreadyVoter := allowExisting && existingServer.Suffrage == raft.Voter
	existingCandidateClaims := 0
	for _, entry := range joining.Entries {
		claim, exists := byID[entry.GuestID]
		if !exists {
			if alreadyVoter {
				return GuestIdentityInventoryReport{}, fmt.Errorf("joining_inventory_changed_for_existing_voter")
			}
			continue
		}
		existingCandidateClaims++
		valid := allowExisting && claim.OwnerNodeID == joiningNodeID &&
			claim.GuestKind == entry.GuestType
		if !alreadyVoter {
			valid = valid && claim.Token == joinToken
		}
		if !valid {
			conflict := joinInventoryReportFromClaims(claims, joiningNodeID, joining, false)
			return GuestIdentityInventoryReport{}, &GuestIdentityInventoryConflictError{Report: conflict}
		}
	}
	if ownedCount != len(joining.Entries) && (alreadyVoter || existingCandidateClaims != 0 || ownedCount != 0) {
		return GuestIdentityInventoryReport{}, fmt.Errorf("joining_inventory_changed_for_existing_member")
	}
	if existingServer != nil && !alreadyVoter &&
		existingCandidateClaims != 0 && existingCandidateClaims != len(joining.Entries) {
		return GuestIdentityInventoryReport{}, fmt.Errorf("joining_inventory_claim_set_incomplete")
	}
	return joinInventoryReportFromClaims(claims, joiningNodeID, joining, true), nil
}

func guestIdentityJoinReservation(
	nodeID string,
	report GuestIdentityInventoryReport,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	token, err := guestIdentityJoinToken(nodeID, report.Digest)
	if err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	entries := make([]clusterServiceInterfaces.GuestIdentityReference, len(report.Entries))
	for i, entry := range report.Entries {
		entries[i] = clusterServiceInterfaces.GuestIdentityReference{
			GuestKind: entry.GuestType,
			GuestID:   entry.GuestID,
		}
	}
	return clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: strings.TrimSpace(nodeID),
		Token:       token,
		Entries:     entries,
		Clustered:   true,
	}, nil
}

func (s *Service) admitStagedJoinGuestIdentities(
	ctx context.Context,
	nodeID string,
	report GuestIdentityInventoryReport,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(report.Entries) == 0 {
		return nil
	}
	reservation, err := guestIdentityJoinReservation(nodeID, report)
	if err != nil {
		return err
	}
	claimSet := guestIdentityClaimSet(reservation)
	if err := s.applyGuestIdentityRaftAction("reserve_ids", claimSet); err != nil {
		return fmt.Errorf("joining_guest_identity_reservation_failed: %w", err)
	}
	return nil
}
