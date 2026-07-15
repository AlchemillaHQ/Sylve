// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
	"time"
)

func TestReplicationPolicyMutationAndDeleteRejectPersistedTransition(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationEvent{})
	seedControlPlanePolicy(t, db)

	var policy ReplicationPolicy
	if err := db.Preload("Targets").First(&policy, 1).Error; err != nil {
		t.Fatal(err)
	}
	policy.Name = "must-not-change"
	if err := UpsertReplicationPolicyTxn(db, &policy, policy.Targets); err == nil || !strings.Contains(err.Error(), "transition_in_progress") {
		t.Fatalf("policy update error = %v, want transition guard", err)
	}
	if err := DeleteReplicationPolicyTxn(db, 1); err == nil || !strings.Contains(err.Error(), "transition_in_progress") {
		t.Fatalf("policy delete error = %v, want transition guard", err)
	}

	var count int64
	if err := db.Model(&ReplicationPolicy{}).Where("id = ?", 1).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("guarded delete removed policy: count=%d err=%v", count, err)
	}
	if err := db.Model(&ReplicationLease{}).Where("policy_id = ?", 1).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("guarded delete removed lease: count=%d err=%v", count, err)
	}
}

func TestDeletingLifecycleWinsRaceAgainstTransitionBegin(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	now := time.Now().UTC()
	policy := ReplicationPolicy{
		ID: 7, Name: "deleting", GuestType: ReplicationGuestTypeJail, GuestID: 77,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 2,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, CronExpr: "*/5 * * * *", Enabled: true,
		ProtectionState: ReplicationProtectionStateArmed, TransitionState: ReplicationTransitionStateCompleted,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateReplicationPolicyProtectionStateTxn(db, &ReplicationPolicyProtectionStateUpdate{
		PolicyID: 7, ExpectedOwnerEpoch: 2, State: ReplicationProtectionStateDeleting,
	}); err != nil {
		t.Fatal(err)
	}
	err := BeginReplicationPolicyTransitionTxn(db, &ReplicationPolicyTransitionBegin{
		PolicyID: 7, ExpectedOwnerEpoch: 2, ProtectionState: ReplicationProtectionStateSuspended,
		Transition: ReplicationPolicyTransition{
			State: ReplicationTransitionStateDemoting, RunID: "run-delete-race",
			SourceNodeID: "node-a", TargetNodeID: "node-b", OwnerEpoch: 2, RequestedAt: &now,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "deleting") {
		t.Fatalf("begin transition error = %v, want deleting guard", err)
	}
	if err := UpdateReplicationPolicyProtectionStateTxn(db, &ReplicationPolicyProtectionStateUpdate{
		PolicyID: 7, ExpectedOwnerEpoch: 2, State: ReplicationProtectionStateArmed,
	}); err == nil || !strings.Contains(err.Error(), "deleting") {
		t.Fatalf("deleting lifecycle was resurrected: %v", err)
	}
}

func TestReplicationPolicyDeleteRequiresDeletingLifecycle(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationEvent{})
	if err := db.Create(&ReplicationPolicy{
		ID: 8, Name: "armed", GuestType: ReplicationGuestTypeVM, GuestID: 88,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		Enabled: true, ProtectionState: ReplicationProtectionStateArmed,
		TransitionState: ReplicationTransitionStateCompleted,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DeleteReplicationPolicyTxn(db, 8); err == nil || !strings.Contains(err.Error(), "not_deleting") {
		t.Fatalf("direct delete should require deleting lifecycle, got %v", err)
	}
	if err := UpdateReplicationPolicyProtectionStateTxn(db, &ReplicationPolicyProtectionStateUpdate{
		PolicyID: 8, ExpectedOwnerEpoch: 1, State: ReplicationProtectionStateDeleting,
	}); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := DeleteReplicationPolicyTxn(db, 8); err != nil {
		t.Fatalf("delete after lifecycle acknowledgement: %v", err)
	}
}

func TestTransitionTargetReadinessRequiresExactRunAndKeepsPolicySuspended(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	now := time.Now().UTC()
	policy := ReplicationPolicy{
		ID: 9, Name: "transition-readiness", GuestType: ReplicationGuestTypeVM, GuestID: 99,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 4,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, CronExpr: "*/5 * * * *", Enabled: true,
		ProtectionState: ReplicationProtectionStateArmed, TransitionState: ReplicationTransitionStateCompleted,
		Targets: []ReplicationPolicyTarget{{NodeID: "node-b", Weight: 100, OwnerEpoch: 4}},
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	transition := ReplicationPolicyTransition{
		State: ReplicationTransitionStateCatchup, RunID: "run-ready",
		SourceNodeID: "node-a", TargetNodeID: "node-b", OwnerEpoch: 4, RequestedAt: &now,
	}
	if err := BeginReplicationPolicyTransitionTxn(db, &ReplicationPolicyTransitionBegin{
		PolicyID: 9, ExpectedOwnerEpoch: 4, Transition: transition,
	}); err != nil {
		t.Fatal(err)
	}
	readyUntil := now.Add(time.Hour)
	update := ReplicationTargetReadinessUpdate{
		PolicyID: 9, NodeID: "node-b", ExpectedOwnerEpoch: 4, EvaluatedAt: now.Add(time.Second),
		Ready: true, GenerationID: "run-ready", ManifestHash: "manifest", RequiredDatasetCount: 2,
		CompletedDatasetCount: 2, LastVerifiedAt: &now, ReadyUntil: &readyUntil,
	}
	if err := UpdateReplicationTargetReadinessTxn(db, &update); err == nil || !strings.Contains(err.Error(), "transition_in_progress") {
		t.Fatalf("readiness without run bypassed transition: %v", err)
	}
	update.TransitionRunID = "wrong-run"
	if err := UpdateReplicationTargetReadinessTxn(db, &update); err == nil || !strings.Contains(err.Error(), "transition_in_progress") {
		t.Fatalf("readiness with wrong run bypassed transition: %v", err)
	}
	update.TransitionRunID = "run-ready"
	if err := UpdateReplicationTargetReadinessTxn(db, &update); err != nil {
		t.Fatalf("exact transition readiness failed: %v", err)
	}
	var stored ReplicationPolicy
	if err := db.First(&stored, 9).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ProtectionState != ReplicationProtectionStateSuspended {
		t.Fatalf("transition readiness changed protection state to %q", stored.ProtectionState)
	}
}
