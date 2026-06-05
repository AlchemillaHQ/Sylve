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
)

func TestFSMDispatcherReplicationPolicyTransitionCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	// seed a policy first
	seedPolicy := ReplicationPolicy{
		ID: 1, Name: "transition-test", GuestType: ReplicationGuestTypeVM,
		GuestID: 100,
		SourceMode: ReplicationSourceModeFollowActive,
		FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual,
		CronExpr: "* * * * *", OwnerEpoch: 1, Enabled: true,
	}
	if err := db.Create(&seedPolicy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	t.Run("update set transition columns on existing policy", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"policyId": 1,
			"transition": ReplicationPolicyTransition{
				State:      ReplicationTransitionStatePromoting,
				RunID:      "run-123",
				Reason:     "manual failover",
				OwnerEpoch: 2,
			},
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy_transition", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update transition failed: %v", err)
		}

		var policy ReplicationPolicy
		if err := db.First(&policy, 1).Error; err != nil {
			t.Fatalf("fetch policy: %v", err)
		}
		if policy.TransitionState != ReplicationTransitionStatePromoting {
			t.Fatalf("state not updated: %q", policy.TransitionState)
		}
		if policy.TransitionRunID != "run-123" {
			t.Fatalf("run_id not updated: %q", policy.TransitionRunID)
		}
		if policy.TransitionReason != "manual failover" {
			t.Fatalf("reason not updated: %q", policy.TransitionReason)
		}
		if policy.TransitionOwnerEpoch != 2 {
			t.Fatalf("epoch not updated: %d", policy.TransitionOwnerEpoch)
		}
	})

	t.Run("update non-existent policy returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"policyId": 999,
			"transition": map[string]any{
				"state": ReplicationTransitionStateCompleted,
			},
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy_transition", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for non-existent policy, got nil")
		}
		if !strings.Contains(err.Error(), "record not found") {
			t.Fatalf("expected 'record not found' error, got: %v", err)
		}
	})

	t.Run("update empty state defaults to none", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"policyId": 1,
			"transition": map[string]any{
				"state": "",
			},
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy_transition", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update with empty state failed: %v", err)
		}

		var policy ReplicationPolicy
		db.First(&policy, 1)
		if policy.TransitionState != ReplicationTransitionStateNone {
			t.Fatalf("expected 'none' default, got: %q", policy.TransitionState)
		}
	})

	t.Run("update invalid transition state returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"policyId": 1,
			"transition": map[string]any{
				"state": "invalid_state",
			},
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy_transition", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for invalid state, got nil")
		}
		if !strings.Contains(err.Error(), "invalid_replication_transition_state") {
			t.Fatalf("expected invalid state error, got: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_policy_transition", Action: "update",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
