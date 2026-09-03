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
	"net/http"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	guestIdentityControlReserve    = "reserve"
	guestIdentityControlRelease    = "release"
	guestIdentityControlReclaim    = "reclaim"
	guestIdentityControlReadClaim  = "read_claim"
	guestIdentityControlListClaims = "list_claims"

	guestIdentityControlMaxResponseBytes = int64(1 << 20)
)

type GuestIdentityControlRequest struct {
	Operation           string                                            `json:"operation"`
	Reservation         clusterServiceInterfaces.GuestIdentityReservation `json:"reservation"`
	GuestKind           string                                            `json:"guestKind,omitempty"`
	GuestID             uint                                              `json:"guestId,omitempty"`
	ExpectedOwnerNodeID string                                            `json:"expectedOwnerNodeId,omitempty"`
	Force               bool                                              `json:"force,omitempty"`
	Confirmation        string                                            `json:"confirmation,omitempty"`
}

type GuestIdentityControlResponse struct {
	Reservation *clusterServiceInterfaces.GuestIdentityReservation `json:"reservation,omitempty"`
	Claims      []clusterModels.GuestIdentityClaim                 `json:"claims,omitempty"`
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
	if reservation.Clustered && reservation.Token == "" {
		return clusterServiceInterfaces.GuestIdentityReservation{}, fmt.Errorf("guest_identity_reservation_token_required")
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

func guestIdentityModelEntries(
	references []clusterServiceInterfaces.GuestIdentityReference,
) []clusterModels.GuestIdentityEntry {
	entries := make([]clusterModels.GuestIdentityEntry, len(references))
	for i, reference := range references {
		entries[i] = clusterModels.GuestIdentityEntry{
			GuestKind: reference.GuestKind,
			GuestID:   reference.GuestID,
		}
	}
	return entries
}

func guestIdentityClaimSet(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) clusterModels.GuestIdentityClaimSet {
	return clusterModels.GuestIdentityClaimSet{
		OwnerNodeID: reservation.OwnerNodeID,
		Token:       reservation.Token,
		Entries:     guestIdentityModelEntries(reservation.Entries),
	}
}

func (s *Service) guestIdentityClustered(ctx context.Context) (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("guest_identity_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var state clusterModels.Cluster
	err := s.DB.WithContext(ctx).Select("enabled").First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("guest_identity_cluster_state_failed: %w", err)
	}
	return state.Enabled, nil
}

func (s *Service) localGuestIdentityOwner() string {
	owner := strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	if owner == "" {
		return "local"
	}
	return owner
}

func (s *Service) applyGuestIdentityRaftAction(action string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("guest_identity_command_marshal_failed: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type:   "guest_identity_registry",
		Action: action,
		Data:   raw,
	})
}

func (s *Service) authoritativeGuestIdentityClaims() ([]clusterModels.GuestIdentityClaim, error) {
	if s == nil || s.Raft == nil {
		return nil, fmt.Errorf("cluster_consensus_unavailable: raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return nil, raft.ErrNotLeader
	}
	if err := s.Raft.VerifyLeader().Error(); err != nil {
		return nil, fmt.Errorf("cluster_consensus_unavailable: verify_leader: %w", err)
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return nil, fmt.Errorf("cluster_consensus_unavailable: barrier_failed: %w", err)
	}
	s.replicatedStateMu.RLock()
	defer s.replicatedStateMu.RUnlock()
	var registry clusterModels.GuestIdentityRegistry
	err := s.DB.Where("id = ?", clusterModels.GuestIdentityRegistryID).First(&registry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, clusterModels.ErrGuestIdentityRegistryInitializing
	}
	if err != nil {
		return nil, fmt.Errorf("guest_identity_registry_read_failed: %w", err)
	}
	if registry.Version != clusterModels.GuestIdentityRegistryVersion {
		return nil, fmt.Errorf("guest_identity_registry_version_unsupported: %d", registry.Version)
	}
	if registry.Phase != clusterModels.GuestIdentityRegistryPhaseActive {
		return nil, clusterModels.ErrGuestIdentityRegistryInitializing
	}
	var claims []clusterModels.GuestIdentityClaim
	if err := s.DB.Order("guest_id ASC").Find(&claims).Error; err != nil {
		return nil, fmt.Errorf("guest_identity_claim_list_failed: %w", err)
	}
	return claims, nil
}

func guestIdentityReservationFromClaim(
	claim clusterModels.GuestIdentityClaim,
) clusterServiceInterfaces.GuestIdentityReservation {
	return clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: claim.OwnerNodeID,
		Token:       claim.Token,
		Clustered:   true,
		Entries: []clusterServiceInterfaces.GuestIdentityReference{{
			GuestKind: claim.GuestKind,
			GuestID:   claim.GuestID,
		}},
	}
}

func (s *Service) HandleGuestIdentityControl(
	ctx context.Context,
	issuerNodeID string,
	request GuestIdentityControlRequest,
) (GuestIdentityControlResponse, error) {
	var response GuestIdentityControlResponse
	if s == nil || s.Raft == nil {
		return response, fmt.Errorf("cluster_consensus_unavailable: raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return response, raft.ErrNotLeader
	}
	issuerNodeID = strings.TrimSpace(issuerNodeID)
	if issuerNodeID == "" {
		return response, fmt.Errorf("guest_identity_issuer_required")
	}
	if err := s.RequireCurrentRaftVoter(issuerNodeID); err != nil {
		return response, fmt.Errorf("guest_identity_issuer_not_voter: %w", err)
	}

	operation := strings.ToLower(strings.TrimSpace(request.Operation))
	switch operation {
	case guestIdentityControlReserve, guestIdentityControlRelease:
		reservation, err := normalizeGuestIdentityReservation(request.Reservation)
		if err != nil {
			return response, err
		}
		if !reservation.Clustered || reservation.OwnerNodeID != issuerNodeID {
			return response, fmt.Errorf("guest_identity_owner_not_issuer")
		}
		action := "reserve_ids"
		if operation == guestIdentityControlRelease {
			action = "release_ids"
		}
		if err := s.applyGuestIdentityRaftAction(action, guestIdentityClaimSet(reservation)); err != nil {
			return response, err
		}
		return response, nil

	case guestIdentityControlReadClaim:
		kind := strings.ToLower(strings.TrimSpace(request.GuestKind))
		owner := strings.TrimSpace(request.ExpectedOwnerNodeID)
		if !clusterModels.ValidGuestIdentityKind(kind) {
			return response, fmt.Errorf("invalid_guest_type")
		}
		if request.GuestID == 0 || request.GuestID > clusterModels.GuestIdentityMaxID {
			return response, fmt.Errorf("invalid_guest_id")
		}
		claims, err := s.authoritativeGuestIdentityClaims()
		if err != nil {
			return response, err
		}
		for _, claim := range claims {
			if claim.GuestID != request.GuestID {
				continue
			}
			if claim.GuestKind != kind || (owner != "" && claim.OwnerNodeID != owner) {
				return response, fmt.Errorf(
					"%w: guest_id=%d expected_owner=%s expected_kind=%s actual_owner=%s actual_kind=%s actual_state=%s",
					clusterModels.ErrGuestIdentityClaimConflict,
					request.GuestID,
					owner,
					kind,
					claim.OwnerNodeID,
					claim.GuestKind,
					"claimed",
				)
			}
			reservation := guestIdentityReservationFromClaim(claim)
			response.Reservation = &reservation
			return response, nil
		}
		return response, fmt.Errorf("%w: guest_id=%d missing", clusterModels.ErrGuestIdentityClaimConflict, request.GuestID)

	case guestIdentityControlListClaims:
		claims, err := s.authoritativeGuestIdentityClaims()
		if err != nil {
			return response, err
		}
		response.Claims = claims
		return response, nil
	case guestIdentityControlReclaim:
		if _, err := s.reclaimGuestIdentityOnLeader(ctx, request); err != nil {
			return response, err
		}
		return response, nil
	default:
		return response, fmt.Errorf("guest_identity_control_operation_invalid")
	}
}

func guestIdentityForwardedError(message, detail string) error {
	text := strings.TrimSpace(detail)
	if text == "" {
		text = strings.TrimSpace(message)
	}
	switch {
	case strings.Contains(text, clusterModels.ErrGuestIdentityAlreadyInUse.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityAlreadyInUse, text)
	case strings.Contains(text, clusterModels.ErrGuestIdentityClaimConflict.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityClaimConflict, text)
	case strings.Contains(text, clusterModels.ErrGuestIdentityRegistryInitializing.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityRegistryInitializing, text)
	case strings.Contains(text, clusterModels.ErrGuestIdentityInventoryConflict.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityInventoryConflict, text)
	case strings.Contains(text, clusterModels.ErrGuestIdentityStillRegistered.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityStillRegistered, text)
	case strings.Contains(text, clusterModels.ErrGuestIdentityReclaimUnsafe.Error()):
		return fmt.Errorf("%w: %s", clusterModels.ErrGuestIdentityReclaimUnsafe, text)
	case message == "cluster_leadership_changed":
		return raft.ErrLeadershipLost
	case message == "cluster_consensus_unavailable":
		return fmt.Errorf("cluster_consensus_unavailable: %s", text)
	case text != "":
		return errors.New(text)
	default:
		return fmt.Errorf("guest_identity_control_rejected")
	}
}

func (s *Service) forwardGuestIdentityControl(
	ctx context.Context,
	leaderNodeID string,
	request GuestIdentityControlRequest,
) (GuestIdentityControlResponse, error) {
	var result GuestIdentityControlResponse
	if s.guestIdentityControlForNode != nil {
		return s.guestIdentityControlForNode(ctx, leaderNodeID, request)
	}
	if s.AuthService == nil {
		return result, fmt.Errorf("guest_identity_control_auth_unavailable")
	}
	endpoint, err := s.ResolveIntraClusterVoterAPI(leaderNodeID)
	if err != nil {
		return result, fmt.Errorf("guest_identity_control_leader_api_failed: %w", err)
	}
	token, err := s.AuthService.CreateInternalClusterJWT(s.localGuestIdentityOwner())
	if err != nil {
		return result, fmt.Errorf("guest_identity_control_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return result, fmt.Errorf("guest_identity_control_request_marshal_failed: %w", err)
	}
	httpResponse, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/guest-identity-control", endpoint),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": "Bearer " + token,
		},
		raftApplyTimeout,
		guestIdentityControlMaxResponseBytes,
	)
	if err != nil {
		return result, fmt.Errorf("guest_identity_control_forward_failed: %w", err)
	}
	var response internal.APIResponse[GuestIdentityControlResponse]
	if err := json.Unmarshal(httpResponse.Body, &response); err != nil {
		return result, fmt.Errorf("guest_identity_control_response_invalid: %w", err)
	}
	if httpResponse.StatusCode >= 200 && httpResponse.StatusCode < 300 &&
		strings.EqualFold(strings.TrimSpace(response.Status), "success") {
		return response.Data, nil
	}
	return result, guestIdentityForwardedError(response.Message, response.Error)
}

func normalizeGuestIdentityConsensusError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, raft.ErrNotLeader),
		errors.Is(err, raft.ErrLeadershipLost),
		errors.Is(err, raft.ErrRaftShutdown),
		errors.Is(err, raft.ErrEnqueueTimeout),
		errors.Is(err, raft.ErrLeadershipTransferInProgress):
		return fmt.Errorf("cluster_consensus_unavailable: %w", err)
	default:
		return err
	}
}

func (s *Service) dispatchGuestIdentityControl(
	ctx context.Context,
	request GuestIdentityControlRequest,
) (GuestIdentityControlResponse, error) {
	var result GuestIdentityControlResponse
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return result, fmt.Errorf("cluster_consensus_unavailable: raft_not_initialized")
	}
	localNodeID := strings.TrimSpace(s.localGuestIdentityOwner())
	if s.Raft.State() == raft.Leader {
		response, err := s.HandleGuestIdentityControl(ctx, localNodeID, request)
		return response, normalizeGuestIdentityConsensusError(err)
	}
	_, leaderID := s.Raft.LeaderWithID()
	if strings.TrimSpace(string(leaderID)) == "" {
		return result, fmt.Errorf("cluster_consensus_unavailable: leader_not_available")
	}
	response, err := s.forwardGuestIdentityControl(ctx, strings.TrimSpace(string(leaderID)), request)
	return response, normalizeGuestIdentityConsensusError(err)
}

func guestIdentityReportBlocksReclaim(
	report GuestIdentityInventoryReport,
	guestKind string,
	guestID uint,
) error {
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return fmt.Errorf("%w: %v", clusterModels.ErrGuestIdentityReclaimUnsafe, err)
	}
	for _, activeID := range report.InFlightGuestIDs {
		if activeID == guestID {
			return fmt.Errorf("%w: guest_id=%d operation_in_progress", clusterModels.ErrGuestIdentityReclaimUnsafe, guestID)
		}
	}
	for _, entry := range report.Entries {
		if entry.GuestID == guestID {
			return fmt.Errorf(
				"%w: guest_id=%d registered_node_id=%s registered_guest_kind=%s requested_guest_kind=%s",
				clusterModels.ErrGuestIdentityStillRegistered,
				guestID,
				entry.NodeID,
				entry.GuestType,
				guestKind,
			)
		}
	}
	return nil
}

func (s *Service) reclaimGuestIdentityOnLeader(
	ctx context.Context,
	request GuestIdentityControlRequest,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	var reservation clusterServiceInterfaces.GuestIdentityReservation
	if request.GuestID == 0 || request.GuestID > clusterModels.GuestIdentityMaxID {
		return reservation, fmt.Errorf("invalid_guest_id")
	}
	if request.Force && strings.TrimSpace(request.Confirmation) != fmt.Sprint(request.GuestID) {
		return reservation, fmt.Errorf("%w: confirmation_must_equal_guest_id", clusterModels.ErrGuestIdentityReclaimUnsafe)
	}

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()

	claims, err := s.authoritativeGuestIdentityClaims()
	if err != nil {
		return reservation, err
	}
	var claim *clusterModels.GuestIdentityClaim
	for i := range claims {
		if claims[i].GuestID == request.GuestID {
			claim = &claims[i]
			break
		}
	}
	if claim == nil {
		return reservation, fmt.Errorf("%w: guest_id=%d missing", clusterModels.ErrGuestIdentityClaimConflict, request.GuestID)
	}
	kind := strings.ToLower(strings.TrimSpace(claim.GuestKind))
	if !clusterModels.ValidGuestIdentityKind(kind) {
		return reservation, fmt.Errorf("%w: guest_id=%d invalid_kind=%s", clusterModels.ErrGuestIdentityClaimConflict, request.GuestID, claim.GuestKind)
	}

	reports, inventory, inventoryErr := s.collectClusterGuestIdentityInventoriesAvailable(ctx)
	if inventoryErr != nil {
		if !request.Force {
			return reservation, fmt.Errorf("%w: inventory_unavailable: %v", clusterModels.ErrGuestIdentityReclaimUnsafe, inventoryErr)
		}
		if err := ctx.Err(); err != nil {
			return reservation, err
		}
	}
	if _, localReported := reports[s.localGuestIdentityOwner()]; !localReported {
		return reservation, fmt.Errorf("%w: local_inventory_unavailable", clusterModels.ErrGuestIdentityReclaimUnsafe)
	}
	if err := guestIdentityReportBlocksReclaim(inventory, kind, request.GuestID); err != nil {
		return reservation, err
	}

	reservation = guestIdentityReservationFromClaim(*claim)
	if err := s.applyGuestIdentityRaftAction("reclaim_id", guestIdentityClaimSet(reservation)); err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	return reservation, nil
}

func (s *Service) ReclaimGuestIdentity(
	ctx context.Context,
	guestID uint,
	force bool,
	confirmation string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	clustered, err := s.guestIdentityClustered(ctx)
	if err != nil {
		return err
	}
	if !clustered {
		return fmt.Errorf("guest_identity_reclaim_not_clustered")
	}
	_, err = s.dispatchGuestIdentityControl(ctx, GuestIdentityControlRequest{
		Operation:    guestIdentityControlReclaim,
		GuestID:      guestID,
		Force:        force,
		Confirmation: confirmation,
	})
	return err
}

func (s *Service) trackClusterGuestIdentityOperation(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	return s.beginGuestIdentityOperation(reservation, clusterModels.ErrGuestIdentityAlreadyInUse)
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
	if s.guestIdentityLocalReservations == nil {
		s.guestIdentityLocalReservations = make(map[uint]string)
	}
	for _, entry := range reservation.Entries {
		if _, exists := s.guestIdentityLocalReservations[entry.GuestID]; exists {
			return fmt.Errorf("%w: guest_id=%d local_mutation_in_progress", conflict, entry.GuestID)
		}
	}
	for _, entry := range reservation.Entries {
		s.guestIdentityLocalReservations[entry.GuestID] = token
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
		if s.guestIdentityLocalReservations[entry.GuestID] == token {
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
		if s.guestIdentityLocalReservations[entry.GuestID] != token {
			return fmt.Errorf(
				"%w: guest_id=%d local_mutation_not_owned",
				clusterModels.ErrGuestIdentityClaimConflict,
				entry.GuestID,
			)
		}
	}
	return nil
}

func (s *Service) requireStandaloneGuestIdentityMutationAllowedLocked(ctx context.Context) error {
	if s.guestIdentityClusterFormation {
		return fmt.Errorf("guest_identity_cluster_formation_in_progress")
	}
	var state clusterModels.Cluster
	err := s.DB.WithContext(ctx).
		Select("enabled", "join_node_id", "join_phase").
		First(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("guest_identity_cluster_state_failed: %w", err)
	}
	if err == nil && (state.Enabled || state.HasIncompleteJoin()) {
		return fmt.Errorf("guest_identity_cluster_formation_in_progress")
	}
	return nil
}

func (s *Service) reserveStandaloneGuestIdentities(
	ctx context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	token := guestIdentityLocalOperationToken(reservation)
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	if err := s.requireStandaloneGuestIdentityMutationAllowedLocked(ctx); err != nil {
		return err
	}
	if s.guestIdentityLocalReservations == nil {
		s.guestIdentityLocalReservations = make(map[uint]string)
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), reservation.OwnerNodeID)
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return err
	}
	occupied := make(map[uint]GuestIdentityInventoryEntry, len(report.Entries))
	for _, entry := range report.Entries {
		occupied[entry.GuestID] = entry
	}
	for _, entry := range reservation.Entries {
		if existing, exists := occupied[entry.GuestID]; exists {
			return fmt.Errorf(
				"%w: guest_id=%d node_id=%s guest_type=%s",
				clusterModels.ErrGuestIdentityAlreadyInUse,
				entry.GuestID,
				existing.NodeID,
				existing.GuestType,
			)
		}
		if _, exists := s.guestIdentityLocalReservations[entry.GuestID]; exists {
			return fmt.Errorf("%w: guest_id=%d", clusterModels.ErrGuestIdentityAlreadyInUse, entry.GuestID)
		}
	}
	for _, entry := range reservation.Entries {
		s.guestIdentityLocalReservations[entry.GuestID] = token
	}
	return nil
}

func (s *Service) finalizeStandaloneGuestIdentities(
	ctx context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	token := guestIdentityLocalOperationToken(reservation)
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	for _, entry := range reservation.Entries {
		if s.guestIdentityLocalReservations[entry.GuestID] != token {
			return fmt.Errorf("%w: guest_id=%d", clusterModels.ErrGuestIdentityClaimConflict, entry.GuestID)
		}
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), reservation.OwnerNodeID)
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return err
	}
	registered := make(map[uint]string, len(report.Entries))
	for _, entry := range report.Entries {
		registered[entry.GuestID] = entry.GuestType
	}
	for _, entry := range reservation.Entries {
		if registered[entry.GuestID] != entry.GuestKind {
			return fmt.Errorf(
				"%w: guest_id=%d canonical_registration_missing",
				clusterModels.ErrGuestIdentityClaimConflict,
				entry.GuestID,
			)
		}
	}
	for _, entry := range reservation.Entries {
		delete(s.guestIdentityLocalReservations, entry.GuestID)
	}
	return nil
}

func (s *Service) releaseStandaloneGuestIdentities(
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	token := guestIdentityLocalOperationToken(reservation)
	if token == "" {
		return nil
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	for _, entry := range reservation.Entries {
		existingToken, exists := s.guestIdentityLocalReservations[entry.GuestID]
		if exists && existingToken != token {
			return fmt.Errorf("%w: guest_id=%d", clusterModels.ErrGuestIdentityClaimConflict, entry.GuestID)
		}
	}
	for _, entry := range reservation.Entries {
		if s.guestIdentityLocalReservations[entry.GuestID] == token {
			delete(s.guestIdentityLocalReservations, entry.GuestID)
		}
	}
	return nil
}

func (s *Service) ReserveGuestIdentities(
	ctx context.Context,
	guestKind string,
	guestIDs []uint,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	var reservation clusterServiceInterfaces.GuestIdentityReservation
	if ctx == nil {
		ctx = context.Background()
	}
	references, err := normalizeGuestIdentityReferences(guestKind, guestIDs)
	if err != nil {
		return reservation, err
	}
	clustered, err := s.guestIdentityClustered(ctx)
	if err != nil {
		return reservation, err
	}
	operationToken := uuid.NewString()
	reservation = clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID:         s.localGuestIdentityOwner(),
		Token:               operationToken,
		LocalOperationToken: operationToken,
		Entries:             references,
		Clustered:           clustered,
	}
	if clustered {
		if err := s.trackClusterGuestIdentityOperation(reservation); err != nil {
			return clusterServiceInterfaces.GuestIdentityReservation{}, err
		}
		_, err = s.dispatchGuestIdentityControl(ctx, GuestIdentityControlRequest{
			Operation:   guestIdentityControlReserve,
			Reservation: reservation,
		})
		if err != nil {
			s.clearGuestIdentityOperation(reservation)
			return clusterServiceInterfaces.GuestIdentityReservation{}, err
		}
		return reservation, nil
	}
	if err := s.reserveStandaloneGuestIdentities(ctx, reservation); err != nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, err
	}
	return reservation, nil
}

func (s *Service) FinalizeGuestIdentities(
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
	if normalized.Clustered {
		s.clearGuestIdentityOperation(normalized)
		return nil
	}
	return s.finalizeStandaloneGuestIdentities(ctx, normalized)
}

func (s *Service) requireNoLocalCanonicalGuestIdentitiesForRelease(
	ctx context.Context,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) error {
	if strings.TrimSpace(reservation.OwnerNodeID) != strings.TrimSpace(s.localGuestIdentityOwner()) {
		return nil
	}
	report, err := ScanLocalGuestIdentityInventory(
		s.DB.WithContext(ctx),
		reservation.OwnerNodeID,
	)
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return err
	}
	requested := make(map[uint]struct{}, len(reservation.Entries))
	for _, entry := range reservation.Entries {
		requested[entry.GuestID] = struct{}{}
	}
	for _, entry := range report.Entries {
		if _, exists := requested[entry.GuestID]; exists {
			return fmt.Errorf(
				"%w: guest_id=%d canonical_registration_present guest_kind=%s",
				clusterModels.ErrGuestIdentityClaimConflict,
				entry.GuestID,
				entry.GuestType,
			)
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
	if normalized.Clustered {
		defer s.clearGuestIdentityOperation(normalized)
		if err := s.requireNoLocalCanonicalGuestIdentitiesForRelease(cleanupCtx, normalized); err != nil {
			return err
		}
		_, err := s.dispatchGuestIdentityControl(cleanupCtx, GuestIdentityControlRequest{
			Operation:   guestIdentityControlRelease,
			Reservation: normalized,
		})
		return err
	}
	return s.releaseStandaloneGuestIdentities(normalized)
}

func (s *Service) readGuestIdentityClaim(
	ctx context.Context,
	guestKind string,
	guestID uint,
	expectedOwnerNodeID string,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	var reservation clusterServiceInterfaces.GuestIdentityReservation
	response, err := s.dispatchGuestIdentityControl(ctx, GuestIdentityControlRequest{
		Operation:           guestIdentityControlReadClaim,
		GuestKind:           guestKind,
		GuestID:             guestID,
		ExpectedOwnerNodeID: expectedOwnerNodeID,
	})
	if err != nil {
		return reservation, err
	}
	if response.Reservation == nil {
		return reservation, fmt.Errorf("guest_identity_control_response_missing_claim")
	}
	return *response.Reservation, nil
}

func (s *Service) GuestIdentityClaim(
	ctx context.Context,
	guestKind string,
	guestID uint,
	expectedOwnerNodeID string,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	var reservation clusterServiceInterfaces.GuestIdentityReservation
	if ctx == nil {
		ctx = context.Background()
	}
	references, err := normalizeGuestIdentityReferences(guestKind, []uint{guestID})
	if err != nil {
		return reservation, err
	}
	clustered, err := s.guestIdentityClustered(ctx)
	if err != nil {
		return reservation, err
	}
	if clustered {
		owner := strings.TrimSpace(expectedOwnerNodeID)
		if owner == "" {
			owner = s.localGuestIdentityOwner()
		}
		localReservation := clusterServiceInterfaces.GuestIdentityReservation{
			OwnerNodeID:         owner,
			LocalOperationToken: uuid.NewString(),
			Entries:             references,
			Clustered:           true,
		}
		if err := s.beginGuestIdentityOperation(
			localReservation,
			clusterModels.ErrGuestIdentityClaimConflict,
		); err != nil {
			return reservation, err
		}
		claim, err := s.readGuestIdentityClaim(ctx, references[0].GuestKind, guestID, owner)
		if err != nil {
			s.clearGuestIdentityOperation(localReservation)
			return reservation, err
		}
		claim.LocalOperationToken = localReservation.LocalOperationToken
		return claim, nil
	}

	owner := strings.TrimSpace(expectedOwnerNodeID)
	if owner == "" {
		owner = s.localGuestIdentityOwner()
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	if err := s.requireStandaloneGuestIdentityMutationAllowedLocked(ctx); err != nil {
		return reservation, err
	}
	if s.guestIdentityLocalReservations == nil {
		s.guestIdentityLocalReservations = make(map[uint]string)
	}
	if _, exists := s.guestIdentityLocalReservations[guestID]; exists {
		return reservation, fmt.Errorf("%w: guest_id=%d local_mutation_in_progress", clusterModels.ErrGuestIdentityClaimConflict, guestID)
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), owner)
	if err != nil {
		return reservation, fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return reservation, err
	}
	for _, entry := range report.Entries {
		if entry.GuestID == guestID && entry.GuestType == references[0].GuestKind && entry.NodeID == owner {
			token := uuid.NewString()
			s.guestIdentityLocalReservations[guestID] = token
			return clusterServiceInterfaces.GuestIdentityReservation{
				OwnerNodeID:         owner,
				Token:               token,
				LocalOperationToken: token,
				Entries:             references,
				Clustered:           false,
			}, nil
		}
	}
	return reservation, fmt.Errorf("%w: guest_id=%d missing", clusterModels.ErrGuestIdentityClaimConflict, guestID)
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
	clustered, err := s.guestIdentityClustered(ctx)
	if err != nil {
		return err
	}
	if clustered != normalized.Clustered {
		return fmt.Errorf("%w: cluster_mode_changed", clusterModels.ErrGuestIdentityClaimConflict)
	}
	if !clustered {
		return s.requireGuestIdentityOperationOwned(normalized)
	}
	entry := normalized.Entries[0]
	claim, err := s.readGuestIdentityClaim(
		ctx,
		entry.GuestKind,
		entry.GuestID,
		normalized.OwnerNodeID,
	)
	if err != nil {
		return err
	}
	if len(claim.Entries) != 1 ||
		claim.Entries[0] != entry ||
		claim.OwnerNodeID != normalized.OwnerNodeID ||
		claim.Token != normalized.Token {
		return fmt.Errorf(
			"%w: guest_id=%d claim_changed",
			clusterModels.ErrGuestIdentityClaimConflict,
			entry.GuestID,
		)
	}
	return s.requireGuestIdentityOperationOwned(normalized)
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
	clustered, err := s.guestIdentityClustered(ctx)
	if err != nil {
		return nil, err
	}
	if clustered {
		response, err := s.dispatchGuestIdentityControl(ctx, GuestIdentityControlRequest{
			Operation: guestIdentityControlListClaims,
		})
		if err != nil {
			return nil, err
		}
		return response.Claims, nil
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), s.localGuestIdentityOwner())
	if err != nil {
		return nil, err
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return nil, err
	}
	claims := make([]clusterModels.GuestIdentityClaim, len(report.Entries))
	for i, entry := range report.Entries {
		claims[i] = clusterModels.GuestIdentityClaim{
			GuestID:     entry.GuestID,
			GuestKind:   entry.GuestType,
			OwnerNodeID: entry.NodeID,
		}
	}
	return claims, nil
}

var _ clusterServiceInterfaces.GuestIdentityCoordinator = (*Service)(nil)
