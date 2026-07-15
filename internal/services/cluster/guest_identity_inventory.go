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
	"errors"
	"fmt"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"gorm.io/gorm"
)

const (
	GuestIdentityInventoryConflictInvalidGuestID   = "invalid_guest_id"
	GuestIdentityInventoryConflictInvalidGuestType = "invalid_guest_type"
	GuestIdentityInventoryConflictInvalidNodeID    = "invalid_node_id"
	GuestIdentityInventoryConflictSharedGuestID    = "shared_guest_id"

	guestIdentityInventoryMaxID          = 9999
	guestIdentityInventoryMaxNodeIDBytes = 128
)

// GuestIdentityInventoryEntry is one durable guest registration observed on a
// node. Only VM and jail registration rows belong in this inventory; templates,
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

// GuestIdentityInventorySnapshot identifies the node that produced a durable
// inventory report. The node ID is repeated outside the report so callers can
// bind an authenticated peer endpoint to the Raft voter they intended to read.
type GuestIdentityInventorySnapshot struct {
	NodeID string                       `json:"nodeId"`
	Report GuestIdentityInventoryReport `json:"report"`
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
	if entry.GuestType != clusterModels.ReplicationGuestTypeVM &&
		entry.GuestType != clusterModels.ReplicationGuestTypeJail {
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

// ScanLocalGuestIdentityInventory reads only durable VM and jail registration
// rows in one database transaction and returns their canonical typed report.
func ScanLocalGuestIdentityInventory(db *gorm.DB, nodeID string) (GuestIdentityInventoryReport, error) {
	if db == nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_database_required")
	}

	entries := make([]GuestIdentityInventoryEntry, 0)
	err := db.Transaction(func(tx *gorm.DB) error {
		var vms []struct {
			ID   uint
			RID  uint `gorm:"column:rid"`
			Name string
		}
		if err := tx.Model(&vmModels.VM{}).
			Select("id", "rid", "name").
			Order("rid ASC, id ASC").
			Scan(&vms).Error; err != nil {
			return fmt.Errorf("scan_vm_guest_identity_inventory: %w", err)
		}
		for _, vm := range vms {
			entries = append(entries, GuestIdentityInventoryEntry{
				NodeID:    nodeID,
				GuestType: clusterModels.ReplicationGuestTypeVM,
				GuestID:   vm.RID,
				RecordID:  vm.ID,
				Name:      vm.Name,
			})
		}

		var jails []struct {
			ID   uint
			CTID uint `gorm:"column:ct_id"`
			Name string
		}
		if err := tx.Model(&jailModels.Jail{}).
			Select("id", "ct_id", "name").
			Order("ct_id ASC, id ASC").
			Scan(&jails).Error; err != nil {
			return fmt.Errorf("scan_jail_guest_identity_inventory: %w", err)
		}
		for _, jail := range jails {
			entries = append(entries, GuestIdentityInventoryEntry{
				NodeID:    nodeID,
				GuestType: clusterModels.ReplicationGuestTypeJail,
				GuestID:   jail.CTID,
				RecordID:  jail.ID,
				Name:      jail.Name,
			})
		}

		return nil
	})
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}

	return BuildGuestIdentityInventoryReport(entries), nil
}

// LocalGuestIdentityInventory scans only durable VM and jail registrations and
// pairs the canonical report with this node's UUID.
func (s *Service) LocalGuestIdentityInventory(ctx context.Context) (GuestIdentityInventorySnapshot, error) {
	if s == nil || s.DB == nil {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_collection_canceled: %w", err)
	}

	nodeID := s.guestIdentityInventoryLocalNodeID()
	if nodeID == "" {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_local_node_id_unavailable")
	}
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), nodeID)
	if err != nil {
		return GuestIdentityInventorySnapshot{}, fmt.Errorf("guest_identity_inventory_local_scan_failed: %w", err)
	}

	return GuestIdentityInventorySnapshot{NodeID: nodeID, Report: report}, nil
}

// RequireGuestIDAvailable checks the shared VM RID/jail CTID namespace. A
// standalone node reads only its local database; a clustered node reads every
// current voter through the authenticated inventory endpoint.
func (s *Service) RequireGuestIDAvailable(ctx context.Context, guestID uint) error {
	return s.RequireGuestIDsAvailable(ctx, []uint{guestID})
}

// RequireGuestIDsAvailable performs one inventory read for a batch of IDs.
func (s *Service) RequireGuestIDsAvailable(ctx context.Context, guestIDs []uint) error {
	if len(guestIDs) == 0 {
		return nil
	}
	requested := make(map[uint]struct{}, len(guestIDs))
	for _, guestID := range guestIDs {
		if guestID == 0 || guestID > guestIdentityInventoryMaxID {
			return fmt.Errorf("invalid_guest_id")
		}
		requested[guestID] = struct{}{}
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: cluster_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}

	var clusterState clusterModels.Cluster
	err := s.DB.WithContext(ctx).Select("enabled").First(&clusterState).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}

	var report GuestIdentityInventoryReport
	if err == nil && clusterState.Enabled {
		_, report, err = s.collectClusterGuestIdentityInventoriesStrict(ctx)
		if err != nil {
			return fmt.Errorf("guest_identity_inventory_unavailable: %w", err)
		}
	} else {
		nodeID := s.guestIdentityInventoryLocalNodeID()
		if nodeID == "" {
			nodeID = "local"
		}
		report, err = ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), nodeID)
		if err != nil {
			return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
		}
	}

	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return err
	}
	for _, entry := range report.Entries {
		if _, exists := requested[entry.GuestID]; exists {
			return fmt.Errorf(
				"guest_id_already_in_use: guest_id=%d node_id=%s guest_type=%s",
				entry.GuestID,
				entry.NodeID,
				entry.GuestType,
			)
		}
	}

	return nil
}
