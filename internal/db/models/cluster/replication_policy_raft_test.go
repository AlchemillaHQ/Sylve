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
	"testing"
)

func TestFSMDispatcherReplicationPolicyCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationEvent{}, &ReplicationReceipt{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("create valid policy with targets", func(t *testing.T) {
		raw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "test-policy", GuestType: ReplicationGuestTypeVM,
				GuestID: 100, SourceNodeID: "node-1",
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
			Targets: []ReplicationPolicyTarget{
				{NodeID: "node-2", Weight: 100},
				{NodeID: "node-3", Weight: 50},
			},
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}

		var policy ReplicationPolicy
		if err := db.Preload("Targets").First(&policy, 1).Error; err != nil {
			t.Fatalf("failed to fetch policy: %v", err)
		}
		if policy.Name != "test-policy" {
			t.Fatalf("name mismatch: got %q", policy.Name)
		}
		if policy.GuestType != ReplicationGuestTypeVM || policy.GuestID != 100 {
			t.Fatalf("guest mismatch: type=%q id=%d", policy.GuestType, policy.GuestID)
		}
		if len(policy.Targets) != 2 {
			t.Fatalf("expected 2 targets, got %d", len(policy.Targets))
		}
		if policy.Targets[0].PolicyID != 1 {
			t.Fatalf("target PolicyID not set: %d", policy.Targets[0].PolicyID)
		}
	})

	t.Run("create disabled policy clears existing lease", func(t *testing.T) {
		db2 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
		fsm2 := NewFSMDispatcher(db2)
		RegisterDefaultHandlers(fsm2)

		// first create enabled policy with lease
		if err := db2.Create(&ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease: %v", err)
		}

		raw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "disabled-policy", GuestType: ReplicationGuestTypeVM,
				GuestID: 100, Enabled: false,
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
		})
		if err := applyFSMCommand(t, fsm2, Command{
			Type: "replication_policy", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create disabled policy failed: %v", err)
		}

		var count int64
		if err := db2.Model(&ReplicationLease{}).Where("policy_id = ?", 1).Count(&count).Error; err != nil {
			t.Fatalf("count leases: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected 0 leases for disabled policy, got %d", count)
		}
	})

	t.Run("update change guest and mode", func(t *testing.T) {
		db3 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
		fsm3 := NewFSMDispatcher(db3)
		RegisterDefaultHandlers(fsm3)

		// seed a policy first
		createRaw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "original", GuestType: ReplicationGuestTypeVM,
				GuestID: 100, SourceNodeID: "node-1",
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
		})
		if err := applyFSMCommand(t, fsm3, Command{
			Type: "replication_policy", Action: "create", Data: createRaw,
		}); err != nil {
			t.Fatalf("seed create: %v", err)
		}

		// update
		updateRaw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "updated", GuestType: ReplicationGuestTypeJail,
				GuestID: 200, SourceNodeID: "node-5",
				SourceMode: ReplicationSourceModePinned,
				FailbackMode: ReplicationFailbackAuto,
				FailoverMode: ReplicationFailoverAutoSafe,
				CronExpr: "0 */6 * * *", OwnerEpoch: 2,
			},
		})
		if err := applyFSMCommand(t, fsm3, Command{
			Type: "replication_policy", Action: "update", Data: updateRaw,
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}

		var policy ReplicationPolicy
		if err := db3.Preload("Targets").First(&policy, 1).Error; err != nil {
			t.Fatalf("fetch updated policy: %v", err)
		}
		if policy.Name != "updated" {
			t.Fatalf("name not updated: %q", policy.Name)
		}
		if policy.GuestType != ReplicationGuestTypeJail || policy.GuestID != 200 {
			t.Fatalf("guest not updated: type=%q id=%d", policy.GuestType, policy.GuestID)
		}
		if policy.SourceMode != ReplicationSourceModePinned {
			t.Fatalf("source mode not updated: %q", policy.SourceMode)
		}
		if policy.FailbackMode != ReplicationFailbackAuto {
			t.Fatalf("failback not updated: %q", policy.FailbackMode)
		}
		if policy.FailoverMode != ReplicationFailoverAutoSafe {
			t.Fatalf("failover not updated: %q", policy.FailoverMode)
		}
		if policy.OwnerEpoch != 2 {
			t.Fatalf("owner epoch not updated: %d", policy.OwnerEpoch)
		}
	})

	t.Run("update add and remove targets", func(t *testing.T) {
		db4 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
		fsm4 := NewFSMDispatcher(db4)
		RegisterDefaultHandlers(fsm4)

		createRaw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "target-test", GuestType: ReplicationGuestTypeVM,
				GuestID: 100,
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
			Targets: []ReplicationPolicyTarget{
				{NodeID: "node-1", Weight: 100},
				{NodeID: "node-2", Weight: 50},
			},
		})
		if err := applyFSMCommand(t, fsm4, Command{
			Type: "replication_policy", Action: "create", Data: createRaw,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}

		// replace targets
		updateRaw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "target-test", GuestType: ReplicationGuestTypeVM,
				GuestID: 100,
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
			Targets: []ReplicationPolicyTarget{
				{NodeID: "node-3", Weight: 200},
			},
		})
		if err := applyFSMCommand(t, fsm4, Command{
			Type: "replication_policy", Action: "update", Data: updateRaw,
		}); err != nil {
			t.Fatalf("update targets: %v", err)
		}

		var policy ReplicationPolicy
		if err := db4.Preload("Targets").First(&policy, 1).Error; err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(policy.Targets) != 1 {
			t.Fatalf("expected 1 target after update, got %d", len(policy.Targets))
		}
		if policy.Targets[0].NodeID != "node-3" || policy.Targets[0].Weight != 200 {
			t.Fatalf("target mismatch: node=%q weight=%d", policy.Targets[0].NodeID, policy.Targets[0].Weight)
		}
	})

	t.Run("delete existing policy with cascade cleanup", func(t *testing.T) {
		db5 := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationEvent{}, &ReplicationReceipt{})
		fsm5 := NewFSMDispatcher(db5)
		RegisterDefaultHandlers(fsm5)

		// seed policy
		createRaw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: 1, Name: "delete-test", GuestType: ReplicationGuestTypeVM,
				GuestID: 100,
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
			Targets: []ReplicationPolicyTarget{{NodeID: "node-1", Weight: 100}},
		})
		if err := applyFSMCommand(t, fsm5, Command{
			Type: "replication_policy", Action: "create", Data: createRaw,
		}); err != nil {
			t.Fatalf("seed create: %v", err)
		}

		// seed related rows
		if err := db5.Create(&ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease: %v", err)
		}
		if err := db5.Create(&ReplicationEvent{
			ID: 1, PolicyID: ptr[uint](1), EventType: "run", Status: "success",
		}).Error; err != nil {
			t.Fatalf("seed event: %v", err)
		}
		if err := db5.Create(&ReplicationReceipt{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			SourceNodeID: "node-1", TargetNodeID: "node-2", Status: "success",
		}).Error; err != nil {
			t.Fatalf("seed receipt: %v", err)
		}

		deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
		if err := applyFSMCommand(t, fsm5, Command{
			Type: "replication_policy", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete failed: %v", err)
		}

		// verify cascade
		var polCount int64
		db5.Model(&ReplicationPolicy{}).Count(&polCount)
		if polCount != 0 {
			t.Fatalf("expected 0 policies, got %d", polCount)
		}
		var tgtCount int64
		db5.Model(&ReplicationPolicyTarget{}).Count(&tgtCount)
		if tgtCount != 0 {
			t.Fatalf("expected 0 targets, got %d", tgtCount)
		}
		var leaseCount int64
		db5.Model(&ReplicationLease{}).Count(&leaseCount)
		if leaseCount != 0 {
			t.Fatalf("expected 0 leases, got %d", leaseCount)
		}
		var evtCount int64
		db5.Model(&ReplicationEvent{}).Count(&evtCount)
		if evtCount != 0 {
			t.Fatalf("expected 0 events, got %d", evtCount)
		}
		var recCount int64
		db5.Model(&ReplicationReceipt{}).Count(&recCount)
		if recCount != 0 {
			t.Fatalf("expected 0 receipts, got %d", recCount)
		}
	})

	t.Run("delete id=0 is no-op", func(t *testing.T) {
		deleteRaw, _ := json.Marshal(map[string]any{"id": 0})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete id=0 should be no-op: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy", Action: "create",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}

func TestFSMDispatcherReplicationPolicyMissingTargets(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	raw, _ := json.Marshal(ReplicationPolicyPayload{
		Policy: ReplicationPolicy{
			ID: 1, Name: "no-targets", GuestType: ReplicationGuestTypeVM,
			GuestID: 100,
			SourceMode: ReplicationSourceModeFollowActive,
			FailbackMode: ReplicationFailbackManual,
			FailoverMode: ReplicationFailoverManual,
			CronExpr: "* * * * *", OwnerEpoch: 1,
		},
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy", Action: "create", Data: raw,
	}); err != nil {
		t.Fatalf("create with zero targets failed: %v", err)
	}

	var policy ReplicationPolicy
	if err := db.Preload("Targets").First(&policy, 1).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if policy.Name != "no-targets" {
		t.Fatalf("name mismatch: %q", policy.Name)
	}
	if len(policy.Targets) != 0 {
		t.Fatalf("expected 0 targets, got %d", len(policy.Targets))
	}
}

func TestFSMDispatcherReplicationPolicyDuplicateNames(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	create := func(id uint, name string) error {
		raw, _ := json.Marshal(ReplicationPolicyPayload{
			Policy: ReplicationPolicy{
				ID: id, Name: name, GuestType: ReplicationGuestTypeVM,
				GuestID: uint(id * 100),
				SourceMode: ReplicationSourceModeFollowActive,
				FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual,
				CronExpr: "* * * * *", OwnerEpoch: 1,
			},
		})
		return applyFSMCommand(t, fsm, Command{
			Type: "replication_policy", Action: "create", Data: raw,
		})
	}

	if err := create(1, "same-name"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// second policy with same name but different guest (unique constraint is on guest, not name)
	if err := create(2, "same-name"); err != nil {
		t.Fatalf("second create with same name: %v", err)
	}

	var count int64
	if err := db.Model(&ReplicationPolicy{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 policies, got %d", count)
	}
}

func ptr[T any](v T) *T { return &v }
