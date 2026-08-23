// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFSMDispatcherReplicationLeaseCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "policy-1", GuestType: ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 1, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	t.Run("upsert new lease", func(t *testing.T) {
		raw, _ := json.Marshal(ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1,
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_lease", Action: "upsert", Data: raw,
		}); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}

		var lease ReplicationLease
		if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
			t.Fatalf("fetch lease: %v", err)
		}
		if lease.OwnerNodeID != "node-1" {
			t.Fatalf("owner mismatch: %q", lease.OwnerNodeID)
		}
		if lease.GuestID != 100 {
			t.Fatalf("guest id mismatch: %d", lease.GuestID)
		}
	})

	t.Run("upsert same policy_id updates existing", func(t *testing.T) {
		if err := db.Model(&ReplicationPolicy{}).Where("id = ?", 1).Updates(map[string]any{
			"active_node_id": "node-2",
			"owner_epoch":    2,
		}).Error; err != nil {
			t.Fatalf("advance policy owner: %v", err)
		}
		raw, _ := json.Marshal(ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-2", OwnerEpoch: 2,
			ExpiresAt: time.Now().Add(2 * time.Hour),
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_lease", Action: "upsert", Data: raw,
		}); err != nil {
			t.Fatalf("upsert update failed: %v", err)
		}

		var count int64
		db.Model(&ReplicationLease{}).Where("policy_id = ?", 1).Count(&count)
		if count != 1 {
			t.Fatalf("expected 1 lease, got %d", count)
		}
		var lease ReplicationLease
		db.Where("policy_id = ?", 1).First(&lease)
		if lease.OwnerNodeID != "node-2" {
			t.Fatalf("owner not updated: %q", lease.OwnerNodeID)
		}
		if lease.OwnerEpoch != 2 {
			t.Fatalf("epoch not updated: %d", lease.OwnerEpoch)
		}
	})

	t.Run("upsert with zero node_id and epoch stored as-is", func(t *testing.T) {
		db2 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
		fsm2 := NewFSMDispatcher(db2)
		RegisterDefaultHandlers(fsm2)

		// upsertLease requires owner_node_id != "" and owner_epoch != 0
		// so zero values will be rejected by validation. Test that.
		raw, _ := json.Marshal(ReplicationLease{
			PolicyID: 2, GuestType: ReplicationGuestTypeVM, GuestID: 200,
			OwnerNodeID: "", OwnerEpoch: 0,
			ExpiresAt: time.Now().Add(time.Hour),
		})
		err := applyFSMCommand(t, fsm2, Command{
			Type: "replication_lease", Action: "upsert", Data: raw,
		})
		if err == nil {
			t.Fatal("expected validation error for zero owner_node_id, got nil")
		}
		if !strings.Contains(err.Error(), "owner") {
			t.Fatalf("expected owner-related error, got: %v", err)
		}
	})

	t.Run("upsert_batch two leases", func(t *testing.T) {
		db3 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
		fsm3 := NewFSMDispatcher(db3)
		RegisterDefaultHandlers(fsm3)
		if err := db3.Create(&[]ReplicationPolicy{
			{ID: 1, Name: "policy-1", GuestType: ReplicationGuestTypeVM, GuestID: 100, ActiveNodeID: "node-a", OwnerEpoch: 1, Enabled: true},
			{ID: 2, Name: "policy-2", GuestType: ReplicationGuestTypeJail, GuestID: 200, ActiveNodeID: "node-b", OwnerEpoch: 1, Enabled: true},
		}).Error; err != nil {
			t.Fatalf("seed policies: %v", err)
		}

		raw, _ := json.Marshal([]ReplicationLease{
			{PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
				OwnerNodeID: "node-a", OwnerEpoch: 1,
				ExpiresAt: time.Now().Add(time.Hour)},
			{PolicyID: 2, GuestType: ReplicationGuestTypeJail, GuestID: 200,
				OwnerNodeID: "node-b", OwnerEpoch: 1,
				ExpiresAt: time.Now().Add(2 * time.Hour)},
		})
		if err := applyFSMCommand(t, fsm3, Command{
			Type: "replication_lease", Action: "upsert_batch", Data: raw,
		}); err != nil {
			t.Fatalf("upsert_batch failed: %v", err)
		}

		var count int64
		db3.Model(&ReplicationLease{}).Count(&count)
		if count != 2 {
			t.Fatalf("expected 2 leases, got %d", count)
		}
	})

	t.Run("upsert_batch one fails rolls back", func(t *testing.T) {
		db4 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
		fsm4 := NewFSMDispatcher(db4)
		RegisterDefaultHandlers(fsm4)
		if err := db4.Create(&[]ReplicationPolicy{
			{ID: 1, Name: "policy-1", GuestType: ReplicationGuestTypeVM, GuestID: 100, ActiveNodeID: "node-a", OwnerEpoch: 1, Enabled: true},
			{ID: 2, Name: "policy-2", GuestType: ReplicationGuestTypeVM, GuestID: 200, ActiveNodeID: "node-b", OwnerEpoch: 1, Enabled: true},
		}).Error; err != nil {
			t.Fatalf("seed policies: %v", err)
		}

		// first lease valid, second has empty OwnerNodeID which fails validation
		raw, _ := json.Marshal([]ReplicationLease{
			{PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
				OwnerNodeID: "node-a", OwnerEpoch: 1,
				ExpiresAt: time.Now().Add(time.Hour)},
			{PolicyID: 2, GuestType: ReplicationGuestTypeVM, GuestID: 200,
				OwnerNodeID: "", OwnerEpoch: 1,
				ExpiresAt: time.Now().Add(time.Hour)},
		})
		err := applyFSMCommand(t, fsm4, Command{
			Type: "replication_lease", Action: "upsert_batch", Data: raw,
		})
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}

		var count int64
		db4.Model(&ReplicationLease{}).Count(&count)
		if count != 0 {
			t.Fatalf("expected transaction rollback (0 leases), got %d", count)
		}
	})

	t.Run("delete existing lease", func(t *testing.T) {
		db5 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
		fsm5 := NewFSMDispatcher(db5)
		RegisterDefaultHandlers(fsm5)

		if err := db5.Create(&ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-x", OwnerEpoch: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease: %v", err)
		}

		deleteRaw, _ := json.Marshal(map[string]any{"policyId": 1})
		if err := applyFSMCommand(t, fsm5, Command{
			Type: "replication_lease", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete failed: %v", err)
		}

		var count int64
		db5.Model(&ReplicationLease{}).Count(&count)
		if count != 0 {
			t.Fatalf("expected 0 leases, got %d", count)
		}
	})

	t.Run("delete non-existent policy is no-op", func(t *testing.T) {
		deleteRaw, _ := json.Marshal(map[string]any{"policyId": 999})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_lease", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete non-existent should be no-op: %v", err)
		}
	})

	t.Run("delete policyId=0 is no-op", func(t *testing.T) {
		deleteRaw, _ := json.Marshal(map[string]any{"policyId": 0})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_lease", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete id=0 should be no-op: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_lease", Action: "upsert",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
