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
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"gorm.io/gorm"
)

const (
	GuestIdentityInventoryConflictInvalidGuestID   = "invalid_guest_id"
	GuestIdentityInventoryConflictInvalidGuestType = "invalid_guest_type"
	GuestIdentityInventoryConflictInvalidNodeID    = "invalid_node_id"
	GuestIdentityInventoryConflictSharedGuestID    = "shared_guest_id"

	guestIdentityInventoryMaxID          = clusterModels.GuestIdentityMaxID
	guestIdentityInventoryMaxNodeIDBytes = 128
)

// GuestIdentityInventoryEntry is one durable guest registration observed on a
// node. Only VM, jail and managed container rows belong in this inventory; templates,
// storage metadata, runtime state, and ZFS artifacts are deliberately excluded.
type GuestIdentityInventoryEntry struct {
	NodeID    string `json:"nodeId"`
	GuestType string `json:"guestType"`
	GuestID   uint   `json:"guestId"`
	RecordID  uint   `json:"recordId"`
	Name      string `json:"name"`
}

// GuestIdentityInventoryConflict describes one canonical inventory invariant
// violation. Entries are sorted using the same ordering as the report.
type GuestIdentityInventoryConflict struct {
	GuestID uint                          `json:"guestId"`
	Reason  string                        `json:"reason"`
	Entries []GuestIdentityInventoryEntry `json:"entries"`
}

// GuestIdentityInventoryReport is a canonical, deterministic view of durable
// guest registrations. Digest covers only node, guest type, and numeric ID;
// record IDs and names are informational and may change without changing guest
// identity.
type GuestIdentityInventoryReport struct {
	Entries   []GuestIdentityInventoryEntry    `json:"entries"`
	Conflicts []GuestIdentityInventoryConflict `json:"conflicts"`
	Digest    string                           `json:"digest"`
}

func normalizeGuestIdentityInventoryEntry(entry GuestIdentityInventoryEntry) GuestIdentityInventoryEntry {
	entry.NodeID = strings.TrimSpace(entry.NodeID)
	entry.GuestType = strings.ToLower(strings.TrimSpace(entry.GuestType))
	entry.Name = strings.TrimSpace(entry.Name)
	return entry
}

func compareGuestIdentityInventoryEntries(left, right GuestIdentityInventoryEntry) int {
	if left.GuestID < right.GuestID {
		return -1
	}
	if left.GuestID > right.GuestID {
		return 1
	}
	if cmp := strings.Compare(left.NodeID, right.NodeID); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(left.GuestType, right.GuestType); cmp != 0 {
		return cmp
	}
	if left.RecordID < right.RecordID {
		return -1
	}
	if left.RecordID > right.RecordID {
		return 1
	}
	return strings.Compare(left.Name, right.Name)
}

func sortGuestIdentityInventoryEntries(entries []GuestIdentityInventoryEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return compareGuestIdentityInventoryEntries(entries[i], entries[j]) < 0
	})
}

func guestIdentityInventoryEntryConflicts(entry GuestIdentityInventoryEntry) []string {
	reasons := make([]string, 0, 3)
	if entry.GuestID == 0 || entry.GuestID > guestIdentityInventoryMaxID {
		reasons = append(reasons, GuestIdentityInventoryConflictInvalidGuestID)
	}
	if !clusterModels.ValidGuestIdentityKind(entry.GuestType) {
		reasons = append(reasons, GuestIdentityInventoryConflictInvalidGuestType)
	}
	if entry.NodeID == "" || len([]byte(entry.NodeID)) > guestIdentityInventoryMaxNodeIDBytes {
		reasons = append(reasons, GuestIdentityInventoryConflictInvalidNodeID)
	}
	return reasons
}

func canonicalGuestIdentityInventoryDigest(entries []GuestIdentityInventoryEntry) string {
	type identity struct {
		NodeID    string `json:"nodeId"`
		GuestType string `json:"guestType"`
		GuestID   uint   `json:"guestId"`
	}
	identities := make([]identity, len(entries))
	for i, entry := range entries {
		identities[i] = identity{
			NodeID:    entry.NodeID,
			GuestType: entry.GuestType,
			GuestID:   entry.GuestID,
		}
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		// GuestIdentityInventoryEntry contains only JSON-safe scalar fields, so
		// this path is unreachable unless the type changes incompatibly.
		panic(fmt.Sprintf("marshal canonical guest identity inventory: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// BuildGuestIdentityInventoryReport normalizes and canonically orders entries,
// records validation failures, groups every shared numeric ID, and hashes the
// ordered entry set. The input slice is never mutated.
func BuildGuestIdentityInventoryReport(entries []GuestIdentityInventoryEntry) GuestIdentityInventoryReport {
	canonical := make([]GuestIdentityInventoryEntry, len(entries))
	for i := range entries {
		canonical[i] = normalizeGuestIdentityInventoryEntry(entries[i])
	}
	sortGuestIdentityInventoryEntries(canonical)

	conflicts := make([]GuestIdentityInventoryConflict, 0)
	for _, entry := range canonical {
		for _, reason := range guestIdentityInventoryEntryConflicts(entry) {
			conflicts = append(conflicts, GuestIdentityInventoryConflict{
				GuestID: entry.GuestID,
				Reason:  reason,
				Entries: []GuestIdentityInventoryEntry{entry},
			})
		}
	}

	for start := 0; start < len(canonical); {
		end := start + 1
		for end < len(canonical) && canonical[end].GuestID == canonical[start].GuestID {
			end++
		}
		if end-start > 1 {
			group := append([]GuestIdentityInventoryEntry(nil), canonical[start:end]...)
			conflicts = append(conflicts, GuestIdentityInventoryConflict{
				GuestID: canonical[start].GuestID,
				Reason:  GuestIdentityInventoryConflictSharedGuestID,
				Entries: group,
			})
		}
		start = end
	}

	sort.SliceStable(conflicts, func(i, j int) bool {
		if conflicts[i].GuestID != conflicts[j].GuestID {
			return conflicts[i].GuestID < conflicts[j].GuestID
		}
		if conflicts[i].Reason != conflicts[j].Reason {
			return conflicts[i].Reason < conflicts[j].Reason
		}
		left, right := conflicts[i].Entries, conflicts[j].Entries
		for idx := 0; idx < len(left) && idx < len(right); idx++ {
			if cmp := compareGuestIdentityInventoryEntries(left[idx], right[idx]); cmp != 0 {
				return cmp < 0
			}
		}
		return len(left) < len(right)
	})

	return GuestIdentityInventoryReport{
		Entries:   canonical,
		Conflicts: conflicts,
		Digest:    canonicalGuestIdentityInventoryDigest(canonical),
	}
}

// ScanLocalGuestIdentityInventory reads only durable VM, jail and container registration
// rows in one database transaction and returns their canonical typed report.
func ScanLocalGuestIdentityInventory(db *gorm.DB, nodeID string) (GuestIdentityInventoryReport, error) {
	if db == nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_database_required")
	}

	entries := make([]GuestIdentityInventoryEntry, 0)
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, source := range []struct {
			model        any
			kind, column string
		}{
			{&vmModels.VM{}, clusterModels.ReplicationGuestTypeVM, "rid"},
			{&jailModels.Jail{}, clusterModels.ReplicationGuestTypeJail, "ct_id"},
			{&containerModels.Container{}, clusterModels.GuestIdentityKindContainer, "oc_id"},
		} {
			var scanned []GuestIdentityInventoryEntry
			if err := tx.Model(source.model).
				Select("id AS record_id, " + source.column + " AS guest_id, name").
				Order(source.column + " ASC, id ASC").Scan(&scanned).Error; err != nil {
				return fmt.Errorf("scan_%s_guest_identity_inventory: %w", source.kind, err)
			}
			for i := range scanned {
				scanned[i].NodeID = nodeID
				scanned[i].GuestType = source.kind
			}
			entries = append(entries, scanned...)
		}

		return nil
	})
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}

	return BuildGuestIdentityInventoryReport(entries), nil
}

func (s *Service) RequireGuestIDAvailable(ctx context.Context, guestID uint) error {
	return s.RequireGuestIDsAvailable(ctx, []uint{guestID})
}

// RequireGuestIDsAvailable refuses IDs claimed anywhere in the cluster, used by
// a guest on this node, or held by a jail creation in progress here.
func (s *Service) RequireGuestIDsAvailable(ctx context.Context, guestIDs []uint) error {
	if len(guestIDs) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	references := make([]clusterServiceInterfaces.GuestIdentityReference, len(guestIDs))
	for i, guestID := range guestIDs {
		if guestID == 0 || guestID > guestIdentityInventoryMaxID {
			return fmt.Errorf("invalid_guest_id")
		}
		references[i] = clusterServiceInterfaces.GuestIdentityReference{GuestID: guestID}
	}
	if err := s.requireGuestCreationIdentityAvailable(ctx, guestIDs, "", ""); err != nil {
		return err
	}
	var claims []clusterModels.GuestIdentityClaim
	if err := s.DB.WithContext(ctx).Where("guest_id IN ?", guestIDs).Order("guest_id ASC").Find(&claims).Error; err != nil {
		return fmt.Errorf("guest_identity_claim_read_failed: %w", err)
	}
	if len(claims) != 0 {
		return fmt.Errorf(
			"%w: guest_id=%d node_id=%s guest_type=%s",
			clusterModels.ErrGuestIdentityAlreadyInUse,
			claims[0].GuestID,
			claims[0].OwnerNodeID,
			claims[0].GuestKind,
		)
	}
	return s.requireNoLocalGuestsWithIDs(ctx, references, clusterModels.ErrGuestIdentityAlreadyInUse)
}

// RequireGuestPlacement checks that expectedNodeID holds the guest's ID as
// guestType (this node when empty). An ID nobody holds yet passes when that
// node is this one and has exactly that guest: the owner reconcile claims it.
func (s *Service) RequireGuestPlacement(
	ctx context.Context,
	guestType string,
	guestID uint,
	expectedNodeID string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if !clusterModels.ValidGuestIdentityKind(guestType) {
		return fmt.Errorf("invalid_guest_type")
	}
	if guestID == 0 || guestID > guestIdentityInventoryMaxID {
		return fmt.Errorf("invalid_guest_id")
	}
	expectedNodeID = strings.TrimSpace(expectedNodeID)
	if expectedNodeID == "" {
		expectedNodeID = s.localGuestIdentityOwner()
	}

	claim, err := s.guestIdentityClaim(ctx, guestID)
	if err != nil {
		return err
	}
	if claim != nil {
		if claim.OwnerNodeID == expectedNodeID && claim.GuestKind == guestType {
			return nil
		}
		return fmt.Errorf(
			"guest_identity_inventory_conflict: placement guest_id=%d expected_node_id=%s expected_guest_type=%s owner_node_id=%s guest_kind=%s",
			guestID, expectedNodeID, guestType, claim.OwnerNodeID, claim.GuestKind,
		)
	}
	if expectedNodeID == s.localGuestIdentityOwner() {
		report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), expectedNodeID)
		if err != nil {
			return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
		}
		matches := 0
		for _, entry := range report.Entries {
			if entry.GuestID == guestID {
				if entry.GuestType != guestType {
					matches = 2
					break
				}
				matches++
			}
		}
		if matches == 1 {
			return nil
		}
	}
	return fmt.Errorf(
		"guest_identity_inventory_conflict: placement guest_id=%d expected_node_id=%s expected_guest_type=%s unclaimed",
		guestID, expectedNodeID, guestType,
	)
}

// RequireGuestRestorePlacement keeps restore call sites explicit while sharing
// the same placement rule.
func (s *Service) RequireGuestRestorePlacement(
	ctx context.Context,
	guestType string,
	guestID uint,
	expectedNodeID string,
) error {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if !clusterModels.ValidReplicationGuestType(guestType) {
		return fmt.Errorf("invalid_guest_type")
	}
	return s.RequireGuestPlacement(ctx, guestType, guestID, expectedNodeID)
}

// GuestIdentityInventorySnapshot is a node's local guest inventory.
type GuestIdentityInventorySnapshot struct {
	NodeID string                       `json:"nodeId"`
	Report GuestIdentityInventoryReport `json:"report"`
}

// LocalGuestIdentityInventory scans this node's VM, jail and container rows.
// Migration asks its target for it, to refuse an ID the target already uses.
func (s *Service) LocalGuestIdentityInventory(ctx context.Context) (GuestIdentityInventorySnapshot, error) {
	if s == nil || s.DB == nil {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nodeID := s.LocalNodeID()
	if nodeID == "" {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_local_node_id_unavailable")
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), nodeID)
	if err != nil {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_local_scan_failed: %w", err)
	}
	return GuestIdentityInventorySnapshot{NodeID: nodeID, Report: report}, nil
}
