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
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type localGuestIdentityReservation struct {
	token string
	kind  string
}

func normalizeGuestIdentityReferences(
	guestKind string,
	guestIDs []uint,
) ([]clusterServiceInterfaces.GuestIdentityReference, error) {
	guestKind = strings.ToLower(strings.TrimSpace(guestKind))
	if !clusterModels.ValidGuestIdentityKind(guestKind) {
		return nil, fmt.Errorf("invalid_guest_type")
	}
	if len(guestIDs) == 0 {
		return nil, fmt.Errorf("guest_identity_entries_required")
	}

	seen := make(map[uint]struct{}, len(guestIDs))
	references := make([]clusterServiceInterfaces.GuestIdentityReference, 0, len(guestIDs))
	for _, guestID := range guestIDs {
		if guestID == 0 || guestID > clusterModels.GuestIdentityMaxID {
			return nil, fmt.Errorf("invalid_guest_id")
		}
		if _, exists := seen[guestID]; exists {
			return nil, fmt.Errorf("duplicate_guest_id")
		}
		seen[guestID] = struct{}{}
		references = append(references, clusterServiceInterfaces.GuestIdentityReference{
			GuestKind: guestKind,
			GuestID:   guestID,
		})
	}
	sort.Slice(references, func(i, j int) bool { return references[i].GuestID < references[j].GuestID })
	return references, nil
}

func normalizeGuestIdentityReservation(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	reservation.OwnerNodeID = strings.TrimSpace(reservation.OwnerNodeID)
	reservation.Token = strings.TrimSpace(reservation.Token)
	reservation.LocalOperationToken = strings.TrimSpace(reservation.LocalOperationToken)
	if reservation.OwnerNodeID == "" || len(reservation.Entries) == 0 {
		return clusterServiceInterfaces.GuestIdentityReservation{}, fmt.Errorf("guest_identity_reservation_invalid")
	}

	seen := make(map[uint]struct{}, len(reservation.Entries))
	for i, entry := range reservation.Entries {
		kind := strings.ToLower(strings.TrimSpace(entry.GuestKind))
		if !clusterModels.ValidGuestIdentityKind(kind) || entry.GuestID == 0 || entry.GuestID > clusterModels.GuestIdentityMaxID {
			return clusterServiceInterfaces.GuestIdentityReservation{}, fmt.Errorf("guest_identity_reservation_invalid")
		}
		if _, exists := seen[entry.GuestID]; exists {
			return clusterServiceInterfaces.GuestIdentityReservation{}, fmt.Errorf("guest_identity_reservation_invalid")
		}
		seen[entry.GuestID] = struct{}{}
		entry.GuestKind = kind
		reservation.Entries[i] = entry
	}
	sort.Slice(reservation.Entries, func(i, j int) bool {
		return reservation.Entries[i].GuestID < reservation.Entries[j].GuestID
	})
	return reservation, nil
}

func guestIdentityLocalOperationToken(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) string {
	if token := strings.TrimSpace(reservation.LocalOperationToken); token != "" {
		return token
	}
	return strings.TrimSpace(reservation.Token)
}

func guestIdentityClaimSet(
	ownerNodeID string,
	references []clusterServiceInterfaces.GuestIdentityReference,
) clusterModels.GuestIdentityClaimSet {
	entries := make([]clusterModels.GuestIdentityEntry, len(references))
	for i, reference := range references {
		entries[i] = clusterModels.GuestIdentityEntry{GuestKind: reference.GuestKind, GuestID: reference.GuestID}
	}
	return clusterModels.GuestIdentityClaimSet{OwnerNodeID: ownerNodeID, Entries: entries}
}

func (s *Service) localGuestIdentityOwner() string {
	if owner := s.LocalNodeID(); owner != "" {
		return owner
	}
	return "local"
}

func (s *Service) guestIdentityClaim(ctx context.Context, guestID uint) (*clusterModels.GuestIdentityClaim, error) {
	var claim clusterModels.GuestIdentityClaim
	result := s.DB.WithContext(ctx).Where("guest_id = ?", guestID).Limit(1).Find(&claim)
	if result.Error != nil {
		return nil, fmt.Errorf("guest_identity_claim_read_failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &claim, nil
}

func (s *Service) beginGuestIdentityOperation(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
	conflict error,
) error {
	token := guestIdentityLocalOperationToken(reservation)
	if token == "" {
		return fmt.Errorf("%w: local_operation_token_required", conflict)
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	if s.mutationGate != nil && s.mutationGate.IsFenced() {
		return ErrNodeLeaveFenced
	}
	// A join sent the leader this node's guests; saveJoinIntent holds this
	// lock while it checks them, so none can change until the join is done.
	var state clusterModels.Cluster
	if err := s.DB.Select("join_leader_ip").Limit(1).Find(&state).Error; err != nil {
		return fmt.Errorf("guest_identity_cluster_state_failed: %w", err)
	}
	if state.HasIncompleteJoin() {
		return fmt.Errorf("guest_identity_cluster_formation_in_progress")
	}
	if s.guestIdentityLocalReservations == nil {
		s.guestIdentityLocalReservations = make(map[uint]localGuestIdentityReservation)
	}
	for _, entry := range reservation.Entries {
		expected := localGuestIdentityReservation{token: token, kind: entry.GuestKind}
		if existing, exists := s.guestIdentityLocalReservations[entry.GuestID]; exists && existing != expected {
			return fmt.Errorf("%w: guest_id=%d local_mutation_in_progress", conflict, entry.GuestID)
		}
	}
	for _, entry := range reservation.Entries {
		s.guestIdentityLocalReservations[entry.GuestID] = localGuestIdentityReservation{token: token, kind: entry.GuestKind}
	}
	return nil
}

func (s *Service) clearGuestIdentityOperation(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) {
	token := guestIdentityLocalOperationToken(reservation)
	if token == "" {
		return
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	for _, entry := range reservation.Entries {
		expected := localGuestIdentityReservation{token: token, kind: entry.GuestKind}
		if s.guestIdentityLocalReservations[entry.GuestID] == expected {
			delete(s.guestIdentityLocalReservations, entry.GuestID)
		}
	}
}

func (s *Service) requireGuestIdentityOperationOwned(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	token := guestIdentityLocalOperationToken(reservation)
	if token == "" {
		return fmt.Errorf("%w: local_operation_token_required", clusterModels.ErrGuestIdentityClaimConflict)
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	for _, entry := range reservation.Entries {
		expected := localGuestIdentityReservation{token: token, kind: entry.GuestKind}
		if s.guestIdentityLocalReservations[entry.GuestID] != expected {
			return fmt.Errorf(
				"%w: guest_id=%d local_mutation_not_owned",
				clusterModels.ErrGuestIdentityClaimConflict,
				entry.GuestID,
			)
		}
	}
	return nil
}

func (s *Service) ReserveGuestIdentities(
	ctx context.Context,
	guestKind string,
	guestIDs []uint,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	reservation, err := s.PrepareGuestIdentityReservation(ctx, guestKind, guestIDs, uuid.NewString())
	if err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	if err := s.AcquireGuestIdentityReservation(ctx, reservation); err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	return reservation, nil
}

func (s *Service) PrepareGuestIdentityReservation(
	_ context.Context, guestKind string, guestIDs []uint, operationToken string,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	references, err := normalizeGuestIdentityReferences(guestKind, guestIDs)
	if err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	if strings.TrimSpace(operationToken) == "" {
		return clusterServiceInterfaces.GuestIdentityReservation{}, fmt.Errorf("guest_identity_operation_token_required")
	}
	return clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID:         s.localGuestIdentityOwner(),
		Token:               operationToken,
		LocalOperationToken: operationToken,
		Entries:             references,
	}, nil
}

func (s *Service) AcquireGuestIdentityReservation(ctx context.Context, reservation clusterServiceInterfaces.GuestIdentityReservation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reservation, err := normalizeGuestIdentityReservation(reservation)
	if err != nil {
		return err
	}
	if reservation.OwnerNodeID != s.localGuestIdentityOwner() {
		return fmt.Errorf("%w: creation_owner_changed", clusterModels.ErrGuestIdentityClaimConflict)
	}
	token := guestIdentityLocalOperationToken(reservation)
	guestIDs := make(map[string][]uint)
	for _, entry := range reservation.Entries {
		guestIDs[entry.GuestKind] = append(guestIDs[entry.GuestKind], entry.GuestID)
	}
	for _, kind := range slices.Sorted(maps.Keys(guestIDs)) {
		if err := s.requireGuestCreationIdentityAvailable(ctx, guestIDs[kind], kind, token); err != nil {
			return err
		}
	}
	if err := s.beginGuestIdentityOperation(reservation, clusterModels.ErrGuestIdentityAlreadyInUse); err != nil {
		return err
	}
	if err := s.requireNoLocalGuestsWithIDs(ctx, reservation.Entries, clusterModels.ErrGuestIdentityAlreadyInUse); err != nil {
		s.clearGuestIdentityOperation(reservation)
		return err
	}
	if err := s.Propose(ctx, "guest_identity", "claim", guestIdentityClaimSet(reservation.OwnerNodeID, reservation.Entries)); err != nil {
		// A claim that committed anyway is released by the owner reconcile:
		// no guest has these IDs once this operation clears.
		s.clearGuestIdentityOperation(reservation)
		return err
	}
	return nil
}

func (s *Service) RestoreGuestIdentityReservation(_ context.Context, reservation clusterServiceInterfaces.GuestIdentityReservation) error {
	normalized, err := normalizeGuestIdentityReservation(reservation)
	if err != nil {
		return err
	}
	if normalized.OwnerNodeID != s.localGuestIdentityOwner() {
		return fmt.Errorf("%w: creation_owner_changed", clusterModels.ErrGuestIdentityClaimConflict)
	}
	if err := s.requireGuestIdentityOperationOwned(normalized); err == nil {
		return nil
	}
	return s.beginGuestIdentityOperation(normalized, clusterModels.ErrGuestIdentityClaimConflict)
}

func (s *Service) FinalizeGuestIdentities(
	_ context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	normalized, err := normalizeGuestIdentityReservation(reservation)
	if err != nil {
		return err
	}
	s.clearGuestIdentityOperation(normalized)
	return nil
}

func (s *Service) requireNoLocalGuestsWithIDs(
	ctx context.Context,
	references []clusterServiceInterfaces.GuestIdentityReference,
	conflict error,
) error {
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), s.localGuestIdentityOwner())
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	requested := make(map[uint]struct{}, len(references))
	for _, reference := range references {
		requested[reference.GuestID] = struct{}{}
	}
	for _, entry := range report.Entries {
		if _, exists := requested[entry.GuestID]; exists {
			return fmt.Errorf("%w: guest_id=%d local_guest_kind=%s", conflict, entry.GuestID, entry.GuestType)
		}
	}
	return nil
}

func (s *Service) ReleaseGuestIdentities(
	ctx context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	cleanupBase := context.Background()
	if ctx != nil {
		cleanupBase = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(cleanupBase, raftApplyTimeout)
	defer cancel()
	normalized, err := normalizeGuestIdentityReservation(reservation)
	if err != nil {
		return err
	}
	defer s.clearGuestIdentityOperation(normalized)
	if err := s.requireNoLocalGuestsWithIDs(cleanupCtx, normalized.Entries, clusterModels.ErrGuestIdentityClaimConflict); err != nil {
		return err
	}
	return s.Propose(cleanupCtx, "guest_identity", "release", guestIdentityClaimSet(normalized.OwnerNodeID, normalized.Entries))
}

func (s *Service) GuestIdentityClaim(
	ctx context.Context,
	guestKind string,
	guestID uint,
	expectedOwnerNodeID string,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	references, err := normalizeGuestIdentityReferences(guestKind, []uint{guestID})
	if err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	owner := strings.TrimSpace(expectedOwnerNodeID)
	if owner == "" {
		owner = s.localGuestIdentityOwner()
	}
	reservation := clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID:         owner,
		LocalOperationToken: uuid.NewString(),
		Entries:             references,
	}
	if err := s.beginGuestIdentityOperation(reservation, clusterModels.ErrGuestIdentityClaimConflict); err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	if err := s.requireGuestIdentityHeldBy(ctx, references[0], owner); err != nil {
		s.clearGuestIdentityOperation(reservation)
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	return reservation, nil
}

// An unclaimed ID is fine: the owner reconcile hasn't run yet.
func (s *Service) requireGuestIdentityHeldBy(
	ctx context.Context,
	reference clusterServiceInterfaces.GuestIdentityReference,
	owner string,
) error {
	claim, err := s.guestIdentityClaim(ctx, reference.GuestID)
	if err != nil || claim == nil {
		return err
	}
	if claim.OwnerNodeID != owner || claim.GuestKind != reference.GuestKind {
		return fmt.Errorf(
			"%w: guest_id=%d owner_node_id=%s guest_kind=%s",
			clusterModels.ErrGuestIdentityClaimConflict,
			claim.GuestID,
			claim.OwnerNodeID,
			claim.GuestKind,
		)
	}
	return nil
}

func (s *Service) ValidateGuestIdentityClaim(
	ctx context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeGuestIdentityReservation(reservation)
	if err != nil {
		return err
	}
	if len(normalized.Entries) != 1 {
		return fmt.Errorf("%w: expected_single_claim", clusterModels.ErrGuestIdentityClaimConflict)
	}
	if err := s.requireGuestIdentityOperationOwned(normalized); err != nil {
		return err
	}
	return s.requireGuestIdentityHeldBy(ctx, normalized.Entries[0], normalized.OwnerNodeID)
}

func (s *Service) CancelGuestIdentityClaim(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) {
	s.clearGuestIdentityOperation(reservation)
}

func (s *Service) ListGuestIdentityClaims(ctx context.Context) ([]clusterModels.GuestIdentityClaim, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var claims []clusterModels.GuestIdentityClaim
	if err := s.DB.WithContext(ctx).Order("guest_id ASC").Find(&claims).Error; err != nil {
		return nil, fmt.Errorf("guest_identity_claim_list_failed: %w", err)
	}
	return claims, nil
}

// It runs on the leader, since only the owner may release through Propose.
func (s *Service) ReclaimGuestIdentity(ctx context.Context, guestID uint) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if guestID == 0 || guestID > clusterModels.GuestIdentityMaxID {
		return fmt.Errorf("invalid_guest_id")
	}
	claim, err := s.guestIdentityClaim(ctx, guestID)
	if err != nil {
		return err
	}
	if claim == nil {
		return fmt.Errorf("%w: guest_id=%d missing", clusterModels.ErrGuestIdentityClaimConflict, guestID)
	}
	var operation clusterModels.ReplicationGuestOperation
	if err := s.DB.WithContext(ctx).Where("guest_id = ?", guestID).First(&operation).Error; err == nil {
		return fmt.Errorf("guest_operation_in_progress: %s", strings.TrimSpace(operation.Operation))
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return s.apply("guest_identity", "release", clusterModels.GuestIdentityClaimSet{
		OwnerNodeID: claim.OwnerNodeID,
		Entries:     []clusterModels.GuestIdentityEntry{{GuestKind: claim.GuestKind, GuestID: claim.GuestID}},
	})
}

var _ clusterServiceInterfaces.GuestIdentityCoordinator = (*Service)(nil)
