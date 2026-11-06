// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

const (
	GuestIdentityKindContainer      = "container"
	GuestIdentityMaxID         uint = 9999

	guestIdentityMaxNodeIDBytes = 128
)

var (
	ErrGuestIdentityAlreadyInUse  = errors.New("guest_id_already_in_use")
	ErrGuestIdentityClaimConflict = errors.New("guest_identity_claim_conflict")
)

type GuestIdentityClaim struct {
	GuestID     uint   `gorm:"primaryKey;autoIncrement:false" json:"guestId"`
	GuestKind   string `gorm:"not null;size:32" json:"guestKind"`
	OwnerNodeID string `gorm:"not null;size:128;index" json:"ownerNodeId"`
}

func (GuestIdentityClaim) TableName() string { return "guest_identity_claims" }

type GuestIdentityEntry struct {
	GuestKind string `json:"guestKind"`
	GuestID   uint   `json:"guestId"`
}

type GuestIdentityClaimSet struct {
	OwnerNodeID string               `json:"ownerNodeId"`
	Entries     []GuestIdentityEntry `json:"entries"`
}

type GuestIdentityMoveOwner struct {
	GuestKind      string `json:"guestKind"`
	GuestID        uint   `json:"guestId"`
	OldOwnerNodeID string `json:"oldOwnerNodeId"`
	NewOwnerNodeID string `json:"newOwnerNodeId"`
}

func normalizeGuestIdentityKind(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ValidGuestIdentityKind(value string) bool {
	value = normalizeGuestIdentityKind(value)
	return value == ReplicationGuestTypeVM || value == ReplicationGuestTypeJail || value == GuestIdentityKindContainer
}

func normalizeGuestIdentityNodeID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]byte(value)) > guestIdentityMaxNodeIDBytes {
		return "", fmt.Errorf("invalid_guest_identity_node_id")
	}
	return value, nil
}

func normalizeGuestIdentityClaimSet(payload *GuestIdentityClaimSet) (string, []GuestIdentityEntry, error) {
	owner, err := normalizeGuestIdentityNodeID(payload.OwnerNodeID)
	if err != nil {
		return "", nil, err
	}
	if len(payload.Entries) == 0 {
		return "", nil, fmt.Errorf("guest_identity_entries_required")
	}
	entries := make([]GuestIdentityEntry, len(payload.Entries))
	for i, entry := range payload.Entries {
		entry.GuestKind = normalizeGuestIdentityKind(entry.GuestKind)
		if !ValidGuestIdentityKind(entry.GuestKind) {
			return "", nil, fmt.Errorf("invalid_guest_type")
		}
		if entry.GuestID == 0 || entry.GuestID > GuestIdentityMaxID {
			return "", nil, fmt.Errorf("invalid_guest_id")
		}
		entries[i] = entry
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].GuestID < entries[j].GuestID })
	for i := 1; i < len(entries); i++ {
		if entries[i-1].GuestID == entries[i].GuestID {
			return "", nil, fmt.Errorf("duplicate_guest_id")
		}
	}
	return owner, entries, nil
}

func guestIdentityClaimsByID(tx *gorm.DB, entries []GuestIdentityEntry) (map[uint]GuestIdentityClaim, error) {
	ids := make([]uint, len(entries))
	for i, entry := range entries {
		ids[i] = entry.GuestID
	}
	var claims []GuestIdentityClaim
	if err := tx.Where("guest_id IN ?", ids).Find(&claims).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]GuestIdentityClaim, len(claims))
	for _, claim := range claims {
		byID[claim.GuestID] = claim
	}
	return byID, nil
}

// Claiming what the owner already holds is a no-op, so retries are safe.
func ClaimGuestIdentitiesTxn(tx *gorm.DB, payload *GuestIdentityClaimSet) error {
	owner, entries, err := normalizeGuestIdentityClaimSet(payload)
	if err != nil {
		return err
	}
	existing, err := guestIdentityClaimsByID(tx, entries)
	if err != nil {
		return err
	}
	missing := make([]GuestIdentityClaim, 0, len(entries))
	for _, entry := range entries {
		claim, held := existing[entry.GuestID]
		if !held {
			missing = append(missing, GuestIdentityClaim{GuestID: entry.GuestID, GuestKind: entry.GuestKind, OwnerNodeID: owner})
			continue
		}
		if claim.OwnerNodeID != owner || claim.GuestKind != entry.GuestKind {
			return fmt.Errorf("%w: guest_id=%d owner_node_id=%s guest_kind=%s",
				ErrGuestIdentityAlreadyInUse, claim.GuestID, claim.OwnerNodeID, claim.GuestKind)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return tx.Create(&missing).Error
}

func ReleaseGuestIdentitiesTxn(tx *gorm.DB, payload *GuestIdentityClaimSet) error {
	owner, entries, err := normalizeGuestIdentityClaimSet(payload)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := tx.Where("guest_id = ? AND guest_kind = ? AND owner_node_id = ?", entry.GuestID, entry.GuestKind, owner).
			Delete(&GuestIdentityClaim{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func MoveGuestIdentityClaimTxn(tx *gorm.DB, payload *GuestIdentityMoveOwner) error {
	guestKind := normalizeGuestIdentityKind(payload.GuestKind)
	if !ValidReplicationGuestType(guestKind) {
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
	var claim GuestIdentityClaim
	result := tx.Where("guest_id = ?", payload.GuestID).Limit(1).Find(&claim)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return tx.Create(&GuestIdentityClaim{GuestID: payload.GuestID, GuestKind: guestKind, OwnerNodeID: newOwner}).Error
	}
	if claim.GuestKind != guestKind || (claim.OwnerNodeID != oldOwner && claim.OwnerNodeID != newOwner) {
		return fmt.Errorf("%w: guest_id=%d owner_node_id=%s guest_kind=%s",
			ErrGuestIdentityClaimConflict, claim.GuestID, claim.OwnerNodeID, claim.GuestKind)
	}
	return tx.Model(&GuestIdentityClaim{}).Where("guest_id = ?", payload.GuestID).Update("owner_node_id", newOwner).Error
}

func RegisterGuestIdentityHandlers(f *FSMDispatcher) {
	On(f, "guest_identity", "claim", ClaimGuestIdentitiesTxn)
	On(f, "guest_identity", "release", ReleaseGuestIdentitiesTxn)
	On(f, "guest_identity", "move", MoveGuestIdentityClaimTxn)
}
