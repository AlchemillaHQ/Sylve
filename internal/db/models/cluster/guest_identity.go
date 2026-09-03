// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

const (
	GuestIdentityRegistryID      uint = 1
	GuestIdentityRegistryVersion uint = 1
	GuestIdentityMaxID           uint = 9999

	GuestIdentityRegistryPhaseCollecting = "collecting"
	GuestIdentityRegistryPhaseActive     = "active"

	guestIdentityMaxNodeIDBytes = 128
	guestIdentityMaxTokenBytes  = 128
)

var (
	ErrGuestIdentityRegistryInitializing = errors.New("guest_identity_registry_initializing")
	ErrGuestIdentityAlreadyInUse         = errors.New("guest_id_already_in_use")
	ErrGuestIdentityClaimConflict        = errors.New("guest_identity_claim_conflict")
	ErrGuestIdentityInventoryConflict    = errors.New("guest_identity_inventory_conflict")
	ErrGuestIdentityStillRegistered      = errors.New("guest_identity_still_registered")
	ErrGuestIdentityReclaimUnsafe        = errors.New("guest_identity_reclaim_unsafe")
)

type GuestIdentityRegistry struct {
	ID      uint   `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Version uint   `gorm:"not null" json:"version"`
	Phase   string `gorm:"not null;index" json:"phase"`
}

func (GuestIdentityRegistry) TableName() string { return "guest_identity_registries" }

type GuestIdentityEnrollment struct {
	NodeID          string `gorm:"primaryKey;size:128" json:"nodeId"`
	InventoryDigest string `gorm:"not null;size:64" json:"inventoryDigest"`
}

func (GuestIdentityEnrollment) TableName() string { return "guest_identity_enrollments" }

type GuestIdentityClaim struct {
	GuestID     uint   `gorm:"primaryKey;autoIncrement:false" json:"guestId"`
	GuestKind   string `gorm:"not null;size:32" json:"guestKind"`
	OwnerNodeID string `gorm:"not null;size:128;index" json:"ownerNodeId"`
	Token       string `gorm:"not null;size:128;index" json:"token"`
}

func (GuestIdentityClaim) TableName() string { return "guest_identity_claims" }

type GuestIdentityEntry struct {
	GuestKind string `json:"guestKind"`
	GuestID   uint   `json:"guestId"`
}

type GuestIdentityRegisterNodeInventory struct {
	NodeID          string               `json:"nodeId"`
	InventoryDigest string               `json:"inventoryDigest"`
	Entries         []GuestIdentityEntry `json:"entries"`
}

type GuestIdentityActivateRegistry struct {
	VoterNodeIDs []string `json:"voterNodeIds"`
}

type GuestIdentityClaimSet struct {
	OwnerNodeID string               `json:"ownerNodeId"`
	Token       string               `json:"token"`
	Entries     []GuestIdentityEntry `json:"entries"`
}

type GuestIdentityMoveOwner struct {
	GuestKind      string `json:"guestKind"`
	GuestID        uint   `json:"guestId"`
	OldOwnerNodeID string `json:"oldOwnerNodeId"`
	NewOwnerNodeID string `json:"newOwnerNodeId"`
	OldToken       string `json:"oldToken"`
	NewToken       string `json:"newToken"`
}

func normalizeGuestIdentityKind(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ValidGuestIdentityKind(value string) bool {
	value = normalizeGuestIdentityKind(value)
	return value == ReplicationGuestTypeVM || value == ReplicationGuestTypeJail
}

func normalizeGuestIdentityNodeID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]byte(value)) > guestIdentityMaxNodeIDBytes {
		return "", fmt.Errorf("invalid_guest_identity_node_id")
	}
	return value, nil
}

func normalizeGuestIdentityToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]byte(value)) > guestIdentityMaxTokenBytes {
		return "", fmt.Errorf("invalid_guest_identity_token")
	}
	return value, nil
}

func normalizeGuestIdentityEntries(entries []GuestIdentityEntry, allowEmpty bool) ([]GuestIdentityEntry, error) {
	if !allowEmpty && len(entries) == 0 {
		return nil, fmt.Errorf("guest_identity_entries_required")
	}
	if len(entries) > int(GuestIdentityMaxID) {
		return nil, fmt.Errorf("guest_identity_entries_too_large")
	}

	canonical := make([]GuestIdentityEntry, len(entries))
	for i, entry := range entries {
		entry.GuestKind = normalizeGuestIdentityKind(entry.GuestKind)
		if !ValidGuestIdentityKind(entry.GuestKind) {
			return nil, fmt.Errorf("invalid_guest_type")
		}
		if entry.GuestID == 0 || entry.GuestID > GuestIdentityMaxID {
			return nil, fmt.Errorf("invalid_guest_id")
		}
		canonical[i] = entry
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].GuestID != canonical[j].GuestID {
			return canonical[i].GuestID < canonical[j].GuestID
		}
		return canonical[i].GuestKind < canonical[j].GuestKind
	})
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1].GuestID == canonical[i].GuestID {
			return nil, fmt.Errorf("%w: duplicate_guest_id=%d", ErrGuestIdentityInventoryConflict, canonical[i].GuestID)
		}
	}
	return canonical, nil
}

func GuestIdentityInventoryDigest(nodeID string, entries []GuestIdentityEntry) string {
	type identity struct {
		NodeID    string `json:"nodeId"`
		GuestType string `json:"guestType"`
		GuestID   uint   `json:"guestId"`
	}
	canonical := append([]GuestIdentityEntry(nil), entries...)
	for i := range canonical {
		canonical[i].GuestKind = normalizeGuestIdentityKind(canonical[i].GuestKind)
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].GuestID != canonical[j].GuestID {
			return canonical[i].GuestID < canonical[j].GuestID
		}
		return canonical[i].GuestKind < canonical[j].GuestKind
	})
	identities := make([]identity, len(canonical))
	for i, entry := range canonical {
		identities[i] = identity{
			NodeID:    strings.TrimSpace(nodeID),
			GuestType: entry.GuestKind,
			GuestID:   entry.GuestID,
		}
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		panic(fmt.Sprintf("marshal canonical guest identity inventory: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func guestIdentityBootstrapToken(nodeID string, entry GuestIdentityEntry) string {
	raw := fmt.Sprintf("v%d\x00%s\x00%s\x00%d", GuestIdentityRegistryVersion, nodeID, entry.GuestKind, entry.GuestID)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func loadGuestIdentityRegistry(tx *gorm.DB) (*GuestIdentityRegistry, error) {
	if tx == nil {
		return nil, fmt.Errorf("guest_identity_database_required")
	}
	var registry GuestIdentityRegistry
	err := tx.Where("id = ?", GuestIdentityRegistryID).First(&registry).Error
	if err != nil {
		return nil, err
	}
	if registry.Version != GuestIdentityRegistryVersion {
		return nil, fmt.Errorf("unsupported_guest_identity_registry_version_%d", registry.Version)
	}
	if registry.Phase != GuestIdentityRegistryPhaseCollecting && registry.Phase != GuestIdentityRegistryPhaseActive {
		return nil, fmt.Errorf("guest_identity_registry_invalid_phase")
	}
	return &registry, nil
}

func requireGuestIdentityRegistryActiveTxn(tx *gorm.DB) error {
	registry, err := loadGuestIdentityRegistry(tx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrGuestIdentityRegistryInitializing
	}
	if err != nil {
		return err
	}
	if registry.Phase != GuestIdentityRegistryPhaseActive {
		return ErrGuestIdentityRegistryInitializing
	}
	return nil
}

func claimsMatchInventory(claims []GuestIdentityClaim, nodeID string, entries []GuestIdentityEntry) bool {
	if len(claims) != len(entries) {
		return false
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].GuestID < claims[j].GuestID })
	for i, entry := range entries {
		claim := claims[i]
		if claim.GuestID != entry.GuestID || claim.GuestKind != entry.GuestKind ||
			claim.OwnerNodeID != nodeID || claim.Token != guestIdentityBootstrapToken(nodeID, entry) {
			return false
		}
	}
	return true
}

func RegisterGuestIdentityInventoryTxn(tx *gorm.DB, payload *GuestIdentityRegisterNodeInventory) error {
	if tx == nil || payload == nil {
		return fmt.Errorf("guest_identity_registration_required")
	}
	nodeID, err := normalizeGuestIdentityNodeID(payload.NodeID)
	if err != nil {
		return err
	}
	entries, err := normalizeGuestIdentityEntries(payload.Entries, true)
	if err != nil {
		return err
	}
	digest := strings.ToLower(strings.TrimSpace(payload.InventoryDigest))
	if digest == "" || digest != GuestIdentityInventoryDigest(nodeID, entries) {
		return fmt.Errorf("guest_identity_inventory_digest_mismatch")
	}

	registry, err := loadGuestIdentityRegistry(tx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		registry = &GuestIdentityRegistry{
			ID: GuestIdentityRegistryID, Version: GuestIdentityRegistryVersion,
			Phase: GuestIdentityRegistryPhaseCollecting,
		}
		if err := tx.Create(registry).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	var enrollment GuestIdentityEnrollment
	err = tx.Where("node_id = ?", nodeID).First(&enrollment).Error
	if err == nil {
		if enrollment.InventoryDigest != digest {
			return fmt.Errorf("%w: node_id=%s changed_inventory", ErrGuestIdentityInventoryConflict, nodeID)
		}
		if registry.Phase == GuestIdentityRegistryPhaseActive {
			return nil
		}
		var claims []GuestIdentityClaim
		if err := tx.Where("owner_node_id = ?", nodeID).Order("guest_id ASC").Find(&claims).Error; err != nil {
			return err
		}
		if !claimsMatchInventory(claims, nodeID, entries) {
			return fmt.Errorf("%w: node_id=%s enrollment_claim_mismatch", ErrGuestIdentityInventoryConflict, nodeID)
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if registry.Phase != GuestIdentityRegistryPhaseCollecting {
		return fmt.Errorf("guest_identity_registry_already_active")
	}

	if len(entries) > 0 {
		ids := make([]uint, len(entries))
		for i := range entries {
			ids[i] = entries[i].GuestID
		}
		var existing []GuestIdentityClaim
		if err := tx.Where("guest_id IN ?", ids).Order("guest_id ASC").Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) != 0 {
			return fmt.Errorf("%w: guest_id=%d", ErrGuestIdentityInventoryConflict, existing[0].GuestID)
		}

		claims := make([]GuestIdentityClaim, len(entries))
		for i, entry := range entries {
			claims[i] = GuestIdentityClaim{
				GuestID: entry.GuestID, GuestKind: entry.GuestKind, OwnerNodeID: nodeID,
				Token: guestIdentityBootstrapToken(nodeID, entry),
			}
		}
		if err := tx.Create(&claims).Error; err != nil {
			return err
		}
	}
	return tx.Create(&GuestIdentityEnrollment{NodeID: nodeID, InventoryDigest: digest}).Error
}

func normalizeVoterNodeIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("guest_identity_voters_required")
	}
	canonical := make([]string, len(values))
	for i, value := range values {
		nodeID, err := normalizeGuestIdentityNodeID(value)
		if err != nil {
			return nil, err
		}
		canonical[i] = nodeID
	}
	sort.Strings(canonical)
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1] == canonical[i] {
			return nil, fmt.Errorf("duplicate_guest_identity_voter")
		}
	}
	return canonical, nil
}

func ActivateGuestIdentityRegistryTxn(tx *gorm.DB, payload *GuestIdentityActivateRegistry) error {
	if tx == nil || payload == nil {
		return fmt.Errorf("guest_identity_activation_required")
	}
	voters, err := normalizeVoterNodeIDs(payload.VoterNodeIDs)
	if err != nil {
		return err
	}
	registry, err := loadGuestIdentityRegistry(tx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrGuestIdentityRegistryInitializing
	}
	if err != nil {
		return err
	}
	if registry.Phase == GuestIdentityRegistryPhaseActive {
		return nil
	}

	var enrollments []GuestIdentityEnrollment
	if err := tx.Where("node_id IN ?", voters).Find(&enrollments).Error; err != nil {
		return err
	}
	if len(enrollments) != len(voters) {
		return ErrGuestIdentityRegistryInitializing
	}

	var claims []GuestIdentityClaim
	if err := tx.Order("guest_id ASC").Find(&claims).Error; err != nil {
		return err
	}
	for _, claim := range claims {
		if claim.GuestID == 0 || claim.GuestID > GuestIdentityMaxID ||
			!ValidGuestIdentityKind(claim.GuestKind) {
			return fmt.Errorf("guest_identity_registry_invalid_claim")
		}
		if _, err := normalizeGuestIdentityNodeID(claim.OwnerNodeID); err != nil {
			return fmt.Errorf("guest_identity_registry_invalid_claim: %w", err)
		}
		if _, err := normalizeGuestIdentityToken(claim.Token); err != nil {
			return fmt.Errorf("guest_identity_registry_invalid_claim: %w", err)
		}
	}

	result := tx.Model(&GuestIdentityRegistry{}).
		Where("id = ? AND version = ? AND phase = ?", GuestIdentityRegistryID, GuestIdentityRegistryVersion, GuestIdentityRegistryPhaseCollecting).
		Update("phase", GuestIdentityRegistryPhaseActive)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrGuestIdentityClaimConflict
	}
	return nil
}

func claimsMatchSet(claims []GuestIdentityClaim, owner, token string, entries []GuestIdentityEntry) bool {
	if len(claims) != len(entries) {
		return false
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].GuestID < claims[j].GuestID })
	for i, entry := range entries {
		claim := claims[i]
		if claim.GuestID != entry.GuestID || claim.GuestKind != entry.GuestKind ||
			claim.OwnerNodeID != owner || claim.Token != token {
			return false
		}
	}
	return true
}

func normalizeClaimSet(payload *GuestIdentityClaimSet, allowEmpty bool) (string, string, []GuestIdentityEntry, error) {
	if payload == nil {
		return "", "", nil, fmt.Errorf("guest_identity_claim_set_required")
	}
	owner, err := normalizeGuestIdentityNodeID(payload.OwnerNodeID)
	if err != nil {
		return "", "", nil, err
	}
	token, err := normalizeGuestIdentityToken(payload.Token)
	if err != nil {
		return "", "", nil, err
	}
	entries, err := normalizeGuestIdentityEntries(payload.Entries, allowEmpty)
	if err != nil {
		return "", "", nil, err
	}
	return owner, token, entries, nil
}

func ReserveGuestIdentityClaimsTxn(tx *gorm.DB, payload *GuestIdentityClaimSet) error {
	if tx == nil {
		return fmt.Errorf("guest_identity_database_required")
	}
	owner, token, entries, err := normalizeClaimSet(payload, false)
	if err != nil {
		return err
	}
	if err := requireGuestIdentityRegistryActiveTxn(tx); err != nil {
		return err
	}

	var tokenClaims []GuestIdentityClaim
	if err := tx.Where("token = ?", token).Order("guest_id ASC").Find(&tokenClaims).Error; err != nil {
		return err
	}
	if len(tokenClaims) > 0 {
		if claimsMatchSet(tokenClaims, owner, token, entries) {
			return nil
		}
		return fmt.Errorf("%w: token_reused", ErrGuestIdentityClaimConflict)
	}

	ids := make([]uint, len(entries))
	for i := range entries {
		ids[i] = entries[i].GuestID
	}
	var existing []GuestIdentityClaim
	if err := tx.Where("guest_id IN ?", ids).Order("guest_id ASC").Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 {
		claim := existing[0]
		return fmt.Errorf("%w: guest_id=%d owner_node_id=%s guest_kind=%s", ErrGuestIdentityAlreadyInUse, claim.GuestID, claim.OwnerNodeID, claim.GuestKind)
	}

	claims := make([]GuestIdentityClaim, len(entries))
	for i, entry := range entries {
		claims[i] = GuestIdentityClaim{
			GuestID: entry.GuestID, GuestKind: entry.GuestKind, OwnerNodeID: owner,
			Token: token,
		}
	}
	return tx.Create(&claims).Error
}

func loadAndValidateClaimSet(tx *gorm.DB, owner, token string, entries []GuestIdentityEntry, missingAllowed bool) (map[uint]GuestIdentityClaim, error) {
	ids := make([]uint, len(entries))
	for i := range entries {
		ids[i] = entries[i].GuestID
	}
	var claims []GuestIdentityClaim
	if err := tx.Where("guest_id IN ?", ids).Find(&claims).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]GuestIdentityClaim, len(claims))
	for _, claim := range claims {
		byID[claim.GuestID] = claim
	}
	for _, entry := range entries {
		claim, exists := byID[entry.GuestID]
		if !exists {
			if missingAllowed {
				continue
			}
			return nil, fmt.Errorf("%w: guest_id=%d missing", ErrGuestIdentityClaimConflict, entry.GuestID)
		}
		if claim.GuestKind != entry.GuestKind || claim.OwnerNodeID != owner || claim.Token != token {
			return nil, fmt.Errorf("%w: guest_id=%d", ErrGuestIdentityClaimConflict, entry.GuestID)
		}
	}
	return byID, nil
}

func ReleaseGuestIdentityClaimsTxn(tx *gorm.DB, payload *GuestIdentityClaimSet) error {
	if tx == nil {
		return fmt.Errorf("guest_identity_database_required")
	}
	owner, token, entries, err := normalizeClaimSet(payload, false)
	if err != nil {
		return err
	}
	if err := requireGuestIdentityRegistryActiveTxn(tx); err != nil {
		return err
	}
	claims, err := loadAndValidateClaimSet(tx, owner, token, entries, true)
	if err != nil {
		return err
	}
	ids := make([]uint, 0, len(claims))
	for id := range claims {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	result := tx.Where("guest_id IN ? AND owner_node_id = ? AND token = ?", ids, owner, token).
		Delete(&GuestIdentityClaim{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return ErrGuestIdentityClaimConflict
	}
	return nil
}

func ReclaimGuestIdentityClaimTxn(tx *gorm.DB, payload *GuestIdentityClaimSet) error {
	if tx == nil {
		return fmt.Errorf("guest_identity_database_required")
	}
	_, _, entries, err := normalizeClaimSet(payload, false)
	if err != nil {
		return err
	}
	if len(entries) != 1 {
		return fmt.Errorf("guest_identity_reclaim_requires_single_id")
	}
	if err := requireNoReplicationGuestOperation(tx, entries[0].GuestKind, entries[0].GuestID); err != nil {
		return err
	}
	return ReleaseGuestIdentityClaimsTxn(tx, payload)
}

func MoveGuestIdentityClaimTxn(tx *gorm.DB, payload *GuestIdentityMoveOwner) error {
	if tx == nil || payload == nil {
		return fmt.Errorf("guest_identity_move_required")
	}
	if err := requireGuestIdentityRegistryActiveTxn(tx); err != nil {
		return err
	}
	guestKind := normalizeGuestIdentityKind(payload.GuestKind)
	if !ValidGuestIdentityKind(guestKind) {
		return fmt.Errorf("invalid_guest_type")
	}
	if payload.GuestID == 0 || payload.GuestID > GuestIdentityMaxID {
		return fmt.Errorf("invalid_guest_id")
	}
	oldOwner, err := normalizeGuestIdentityNodeID(payload.OldOwnerNodeID)
	if err != nil {
		return err
	}
	newOwner, err := normalizeGuestIdentityNodeID(payload.NewOwnerNodeID)
	if err != nil {
		return err
	}
	if oldOwner == newOwner {
		return fmt.Errorf("guest_identity_owner_unchanged")
	}
	oldToken, err := normalizeGuestIdentityToken(payload.OldToken)
	if err != nil {
		return err
	}
	newToken, err := normalizeGuestIdentityToken(payload.NewToken)
	if err != nil {
		return err
	}
	if oldToken == newToken {
		return fmt.Errorf("guest_identity_token_unchanged")
	}

	var claim GuestIdentityClaim
	if err := tx.Where("guest_id = ?", payload.GuestID).First(&claim).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: guest_id=%d missing", ErrGuestIdentityClaimConflict, payload.GuestID)
		}
		return err
	}
	if claim.GuestKind == guestKind && claim.OwnerNodeID == newOwner && claim.Token == newToken {
		return nil
	}
	if claim.GuestKind != guestKind || claim.OwnerNodeID != oldOwner || claim.Token != oldToken {
		return fmt.Errorf("%w: guest_id=%d", ErrGuestIdentityClaimConflict, payload.GuestID)
	}

	var reused int64
	if err := tx.Model(&GuestIdentityClaim{}).
		Where("token = ? AND guest_id <> ?", newToken, payload.GuestID).
		Count(&reused).Error; err != nil {
		return err
	}
	if reused != 0 {
		return fmt.Errorf("%w: new_token_reused", ErrGuestIdentityClaimConflict)
	}

	result := tx.Model(&GuestIdentityClaim{}).
		Where("guest_id = ? AND guest_kind = ? AND owner_node_id = ? AND token = ?", payload.GuestID, guestKind, oldOwner, oldToken).
		Updates(map[string]any{"owner_node_id": newOwner, "token": newToken})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrGuestIdentityClaimConflict
	}
	return nil
}

func ValidateGuestIdentitySnapshot(snapshot *ClusterSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("cluster_snapshot_required")
	}
	if len(snapshot.GuestIdentityRegistries) == 0 {
		if len(snapshot.GuestIdentityEnrollments) != 0 || len(snapshot.GuestIdentityClaims) != 0 {
			return fmt.Errorf("guest_identity_snapshot_missing_registry")
		}
		return nil
	}
	if len(snapshot.GuestIdentityRegistries) != 1 {
		return fmt.Errorf("guest_identity_snapshot_invalid_registry_count")
	}
	registry := snapshot.GuestIdentityRegistries[0]
	if registry.ID != GuestIdentityRegistryID || registry.Version != GuestIdentityRegistryVersion ||
		(registry.Phase != GuestIdentityRegistryPhaseCollecting && registry.Phase != GuestIdentityRegistryPhaseActive) {
		return fmt.Errorf("guest_identity_snapshot_invalid_registry")
	}

	enrolled := make(map[string]struct{}, len(snapshot.GuestIdentityEnrollments))
	for _, enrollment := range snapshot.GuestIdentityEnrollments {
		nodeID, err := normalizeGuestIdentityNodeID(enrollment.NodeID)
		if err != nil || nodeID != enrollment.NodeID {
			return fmt.Errorf("guest_identity_snapshot_invalid_enrollment")
		}
		if _, exists := enrolled[nodeID]; exists {
			return fmt.Errorf("guest_identity_snapshot_duplicate_enrollment")
		}
		digest := strings.ToLower(strings.TrimSpace(enrollment.InventoryDigest))
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || digest != enrollment.InventoryDigest {
			return fmt.Errorf("guest_identity_snapshot_invalid_enrollment_digest")
		}
		enrolled[nodeID] = struct{}{}
	}

	seenIDs := make(map[uint]struct{}, len(snapshot.GuestIdentityClaims))
	for _, claim := range snapshot.GuestIdentityClaims {
		if claim.GuestID == 0 || claim.GuestID > GuestIdentityMaxID || !ValidGuestIdentityKind(claim.GuestKind) {
			return fmt.Errorf("guest_identity_snapshot_invalid_claim")
		}
		if _, exists := seenIDs[claim.GuestID]; exists {
			return fmt.Errorf("guest_identity_snapshot_duplicate_claim")
		}
		seenIDs[claim.GuestID] = struct{}{}
		owner, err := normalizeGuestIdentityNodeID(claim.OwnerNodeID)
		if err != nil || owner != claim.OwnerNodeID {
			return fmt.Errorf("guest_identity_snapshot_invalid_claim_owner")
		}
		token, err := normalizeGuestIdentityToken(claim.Token)
		if err != nil || token != claim.Token {
			return fmt.Errorf("guest_identity_snapshot_invalid_claim_token")
		}
		if registry.Phase == GuestIdentityRegistryPhaseCollecting {
			if _, exists := enrolled[owner]; !exists {
				return fmt.Errorf("guest_identity_snapshot_claim_owner_not_enrolled")
			}
			entry := GuestIdentityEntry{GuestKind: claim.GuestKind, GuestID: claim.GuestID}
			if claim.Token != guestIdentityBootstrapToken(owner, entry) {
				return fmt.Errorf("guest_identity_snapshot_invalid_bootstrap_token")
			}
		}
	}
	return nil
}

func RegisterGuestIdentityRegistryHandler(fsm *FSMDispatcher) {
	fsm.Register("guest_identity_registry", func(db *gorm.DB, action string, raw json.RawMessage) error {
		return db.Transaction(func(tx *gorm.DB) error {
			switch action {
			case "register_node_inventory":
				var payload GuestIdentityRegisterNodeInventory
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return RegisterGuestIdentityInventoryTxn(tx, &payload)
			case "activate_registry":
				var payload GuestIdentityActivateRegistry
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return ActivateGuestIdentityRegistryTxn(tx, &payload)
			case "reserve_ids":
				var payload GuestIdentityClaimSet
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return ReserveGuestIdentityClaimsTxn(tx, &payload)
			case "release_ids":
				var payload GuestIdentityClaimSet
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return ReleaseGuestIdentityClaimsTxn(tx, &payload)
			case "reclaim_id":
				var payload GuestIdentityClaimSet
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return ReclaimGuestIdentityClaimTxn(tx, &payload)
			case "move_id_owner":
				var payload GuestIdentityMoveOwner
				if err := json.Unmarshal(raw, &payload); err != nil {
					return err
				}
				return MoveGuestIdentityClaimTxn(tx, &payload)
			default:
				return fmt.Errorf("unsupported_guest_identity_registry_action_%s", action)
			}
		})
	})
}
