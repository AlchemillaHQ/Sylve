// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func seedControlPlanePolicy(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	running := true
	policy := ReplicationPolicy{
		ID: 1, Name: "control-plane", GuestType: ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 1,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, Enabled: true,
		ProtectionState: ReplicationProtectionStateSuspended,
		TransitionState: ReplicationTransitionStateDemoting, TransitionRunID: "run-1",
		TransitionSourceNodeID: "node-1", TransitionTargetNodeID: "node-2",
		TransitionOwnerEpoch: 1, TransitionRequestedAt: &now,
		TransitionAllowUnsafe: false, TransitionMovePinnedSource: true,
		TransitionTriggerValidationRun: true, TransitionOriginalRunning: &running,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&ReplicationPolicyTarget{
		PolicyID: 1, NodeID: "node-2", Weight: 100, Ready: true,
		GenerationID: "generation-1", OwnerEpoch: 1, ManifestHash: "manifest-1",
		RequiredDatasetCount: 2, CompletedDatasetCount: 2,
		LastVerifiedAt: &now, ReadyUntil: ptrTime(now.Add(time.Hour)),
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&ReplicationLease{
		PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 1, Version: 10,
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func ownershipCommitPayload() ReplicationOwnershipTransitionPayload {
	source := "node-2"
	now := time.Now().UTC()
	running := true
	return ReplicationOwnershipTransitionPayload{
		PolicyID: 1, ExpectedActiveNodeID: "node-1", ExpectedOwnerEpoch: 1,
		ExpectedTransitionRunID: "run-1", ActiveNodeID: "node-2",
		SourceNodeID: &source, OwnerEpoch: 2, ReplaceTargets: true,
		Targets: []ReplicationPolicyTarget{{NodeID: "node-1", Weight: 100}},
		Lease: ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-2", OwnerEpoch: 2, Version: 20,
			ExpiresAt: now.Add(time.Hour), LastReason: "planned_move", LastActor: "leader",
		},
		Transition: ReplicationPolicyTransition{
			State: ReplicationTransitionStatePromoting, RunID: "run-1",
			Reason: "planned_move", SourceNodeID: "node-1", TargetNodeID: "node-2",
			OwnerEpoch: 2, AllowUnsafe: false, MovePinnedSource: true,
			TriggerValidationRun: true, OriginalRunning: &running,
		},
		ProtectionState: ReplicationProtectionStateSuspended,
	}
}

func TestFSMReplicationOwnershipTransitionAtomicCommit(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	seedControlPlanePolicy(t, db)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	payload := ownershipCommitPayload()
	raw, _ := json.Marshal(payload)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_ownership_transition", Action: "commit", Data: raw,
	}); err != nil {
		t.Fatalf("commit ownership: %v", err)
	}

	var policy ReplicationPolicy
	if err := db.Preload("Targets").First(&policy, 1).Error; err != nil {
		t.Fatalf("read policy: %v", err)
	}
	if policy.ActiveNodeID != "node-2" || policy.SourceNodeID != "node-2" || policy.OwnerEpoch != 2 {
		t.Fatalf("unexpected owner state: active=%q source=%q epoch=%d", policy.ActiveNodeID, policy.SourceNodeID, policy.OwnerEpoch)
	}
	if policy.TransitionState != ReplicationTransitionStatePromoting || policy.TransitionOwnerEpoch != 2 {
		t.Fatalf("transition checkpoint not committed: state=%q epoch=%d", policy.TransitionState, policy.TransitionOwnerEpoch)
	}
	if !policy.TransitionMovePinnedSource || !policy.TransitionTriggerValidationRun || policy.TransitionOriginalRunning == nil || !*policy.TransitionOriginalRunning {
		t.Fatalf("transition recovery options were not persisted: %+v", policy)
	}
	if len(policy.Targets) != 1 || policy.Targets[0].NodeID != "node-1" || policy.Targets[0].Ready {
		t.Fatalf("rotated targets mismatch: %+v", policy.Targets)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if lease.OwnerNodeID != "node-2" || lease.OwnerEpoch != 2 || lease.Version != 20 {
		t.Fatalf("lease mismatch: %+v", lease)
	}

	// The original CAS can never overwrite the completed cutover.
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_ownership_transition", Action: "commit", Data: raw,
	}); err == nil || !strings.Contains(err.Error(), "replication_ownership_cas_conflict") {
		t.Fatalf("expected stale CAS rejection, got %v", err)
	}
}

func TestReplicationOwnershipTransitionRollsBackEveryTable(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	seedControlPlanePolicy(t, db)
	if err := db.Callback().Create().Before("gorm:create").Register("fail_rotated_target", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ReplicationPolicyTarget" {
			tx.AddError(errors.New("injected_target_write_failure"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	payload := ownershipCommitPayload()
	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err == nil || !strings.Contains(err.Error(), "injected_target_write_failure") {
		t.Fatalf("expected injected failure, got %v", err)
	}

	var policy ReplicationPolicy
	if err := db.Preload("Targets").First(&policy, 1).Error; err != nil {
		t.Fatalf("read policy: %v", err)
	}
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 || policy.TransitionState != ReplicationTransitionStateDemoting {
		t.Fatalf("policy partially committed: %+v", policy)
	}
	if len(policy.Targets) != 1 || policy.Targets[0].NodeID != "node-2" || !policy.Targets[0].Ready {
		t.Fatalf("targets partially committed: %+v", policy.Targets)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if lease.OwnerNodeID != "node-1" || lease.OwnerEpoch != 1 || lease.Version != 10 {
		t.Fatalf("lease partially committed: %+v", lease)
	}
}

func TestReplicationOwnershipTransitionRejectsTerminalOrWrongPredecessor(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	seedControlPlanePolicy(t, db)
	if err := db.Model(&ReplicationPolicy{}).Where("id = ?", 1).Updates(map[string]any{
		"transition_state": ReplicationTransitionStateFailed,
		"transition_error": "earlier failure",
	}).Error; err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	payload := ownershipCommitPayload()
	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err == nil ||
		!strings.Contains(err.Error(), "invalid_predecessor_state") {
		t.Fatalf("late ownership commit should be rejected, got %v", err)
	}
	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 ||
		policy.TransitionState != ReplicationTransitionStateFailed {
		t.Fatalf("late commit changed terminal policy: %+v", policy)
	}
}

func TestReplicationOwnershipTransitionLeaseExpiryPredicateIsAtomic(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{})
	seedControlPlanePolicy(t, db)
	payload := ownershipCommitPayload()
	cutoff := time.Now().UTC()
	payload.PreviousLeaseExpiresAtOrBefore = &cutoff

	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err == nil ||
		!strings.Contains(err.Error(), "previous_owner_lease_not_expired") {
		t.Fatalf("active previous-owner lease should reject cutover, got %v", err)
	}
	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 {
		t.Fatalf("rejected force cutover changed ownership: %+v", policy)
	}

	cutoff = cutoff.Add(2 * time.Hour)
	payload.PreviousLeaseExpiresAtOrBefore = &cutoff
	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err != nil {
		t.Fatalf("expired previous-owner lease should permit cutover: %v", err)
	}
}

func TestFSMReplicationPolicyTransitionBeginCASAndRecoveryOptions(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{})
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "begin", GuestType: ReplicationGuestTypeJail, GuestID: 20,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 4,
		Enabled: true, ProtectionState: ReplicationProtectionStateArmed,
		TransitionState: ReplicationTransitionStateNone,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	running := false
	begin := ReplicationPolicyTransitionBegin{
		PolicyID: 1, ExpectedOwnerEpoch: 4,
		Transition: ReplicationPolicyTransition{
			State: ReplicationTransitionStateDemoting, RunID: "run-a",
			SourceNodeID: "node-1", TargetNodeID: "node-2", OwnerEpoch: 4,
			AllowUnsafe: true, MovePinnedSource: true, TriggerValidationRun: true,
			OriginalRunning: &running,
		},
	}
	raw, _ := json.Marshal(begin)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_transition", Action: "begin", Data: raw,
	}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Same run is an idempotent acquisition.
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_transition", Action: "begin", Data: raw,
	}); err != nil {
		t.Fatalf("idempotent begin: %v", err)
	}

	mismatched := begin
	mismatched.Transition.TargetNodeID = "node-3"
	mismatchedRaw, _ := json.Marshal(mismatched)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_transition", Action: "begin", Data: mismatchedRaw,
	}); err == nil || !strings.Contains(err.Error(), "begin_replay_mismatch") {
		t.Fatalf("same-run mismatched begin should be rejected, got %v", err)
	}

	begin.Transition.RunID = "run-b"
	competingRaw, _ := json.Marshal(begin)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_transition", Action: "begin", Data: competingRaw,
	}); err == nil || !strings.Contains(err.Error(), "replication_policy_transition_already_running") {
		t.Fatalf("expected competing run rejection, got %v", err)
	}

	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if policy.ProtectionState != ReplicationProtectionStateSuspended || policy.TransitionRunID != "run-a" {
		t.Fatalf("begin state mismatch: %+v", policy)
	}
	if !policy.TransitionAllowUnsafe || !policy.TransitionMovePinnedSource || !policy.TransitionTriggerValidationRun {
		t.Fatalf("transition options missing: %+v", policy)
	}
	if policy.TransitionOriginalRunning == nil || *policy.TransitionOriginalRunning {
		t.Fatalf("original running state not persisted: %+v", policy.TransitionOriginalRunning)
	}
}

func TestReplicationTransitionTerminalCheckpointCannotBeReopened(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{})
	completed := time.Now().UTC()
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "terminal", GuestType: ReplicationGuestTypeVM, GuestID: 30,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 5, Enabled: true,
		ProtectionState: ReplicationProtectionStateDegraded,
		TransitionState: ReplicationTransitionStateFailed, TransitionRunID: "run-terminal",
		TransitionSourceNodeID: "node-1", TransitionTargetNodeID: "node-2",
		TransitionOwnerEpoch: 5, TransitionCompletedAt: &completed,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	late := ReplicationPolicyTransition{
		State: ReplicationTransitionStateDemoting, RunID: "run-terminal",
		SourceNodeID: "node-1", TargetNodeID: "node-2", OwnerEpoch: 5,
	}
	if err := UpsertReplicationPolicyTransitionTxn(db, 1, &late); err == nil ||
		!strings.Contains(err.Error(), "invalid_predecessor_state") {
		t.Fatalf("late in-progress checkpoint reopened terminal run: %v", err)
	}
	late.RunID = "other-run"
	if err := UpsertReplicationPolicyTransitionTxn(db, 1, &late); err == nil ||
		!strings.Contains(err.Error(), "run_mismatch") {
		t.Fatalf("generic update started a new run without Begin: %v", err)
	}

	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.TransitionState != ReplicationTransitionStateFailed || policy.TransitionRunID != "run-terminal" {
		t.Fatalf("terminal checkpoint changed: %+v", policy)
	}
}

func TestFSMReplicationTargetReadinessCAS(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "readiness", GuestType: ReplicationGuestTypeVM, GuestID: 10,
		ActiveNodeID: "node-1", OwnerEpoch: 7, Enabled: true,
		ProtectionState: ReplicationProtectionStateInitializing,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&ReplicationPolicyTarget{PolicyID: 1, NodeID: "node-2", Weight: 100}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	now := time.Now().UTC()
	update := ReplicationTargetReadinessUpdate{
		PolicyID: 1, NodeID: "node-2", ExpectedOwnerEpoch: 7, EvaluatedAt: now, Ready: true,
		GenerationID: "gen-7", ManifestHash: "hash-7",
		RequiredDatasetCount: 3, CompletedDatasetCount: 3,
		LastVerifiedAt: &now, ReadyUntil: ptrTime(now.Add(time.Hour)),
	}
	raw, _ := json.Marshal(update)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_target_readiness", Action: "update", Data: raw,
	}); err != nil {
		t.Fatalf("update readiness: %v", err)
	}

	var target ReplicationPolicyTarget
	if err := db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target).Error; err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !target.Ready || target.OwnerEpoch != 7 || target.GenerationID != "gen-7" || target.ReadyUntil == nil {
		t.Fatalf("readiness mismatch: %+v", target)
	}
	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("protection state mismatch: %q", policy.ProtectionState)
	}

	conflictingSameInstant := update
	conflictingSameInstant.ManifestHash = "different-hash"
	conflictingRaw, _ := json.Marshal(conflictingSameInstant)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_target_readiness", Action: "update", Data: conflictingRaw,
	}); err == nil || !strings.Contains(err.Error(), "readiness_stale") {
		t.Fatalf("same-instant conflicting readiness should be rejected, got %v", err)
	}

	older := now.Add(-time.Minute)
	staleSameEpoch := update
	staleSameEpoch.EvaluatedAt = older
	staleSameEpoch.GenerationID = "older-generation"
	staleSameEpoch.ManifestHash = "older-hash"
	staleSameEpoch.LastVerifiedAt = &older
	staleSameEpoch.ReadyUntil = ptrTime(older.Add(time.Hour))
	staleSameEpochRaw, _ := json.Marshal(staleSameEpoch)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_target_readiness", Action: "update", Data: staleSameEpochRaw,
	}); err == nil || !strings.Contains(err.Error(), "readiness_stale") {
		t.Fatalf("expected same-epoch older generation rejection, got %v", err)
	}

	update.ExpectedOwnerEpoch = 6
	update.GenerationID = "stale"
	staleRaw, _ := json.Marshal(update)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_target_readiness", Action: "update", Data: staleRaw,
	}); err == nil || !strings.Contains(err.Error(), "cas_conflict") {
		t.Fatalf("expected stale readiness rejection, got %v", err)
	}
	db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target)
	if target.GenerationID != "gen-7" {
		t.Fatalf("stale update overwrote readiness: %+v", target)
	}
}

func TestReplicationReadinessRequiresEveryTargetBeforeArming(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "multi-target", GuestType: ReplicationGuestTypeVM, GuestID: 10,
		ActiveNodeID: "node-1", OwnerEpoch: 9, Enabled: true,
		ProtectionState: ReplicationProtectionStateInitializing,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&[]ReplicationPolicyTarget{
		{PolicyID: 1, NodeID: "node-2", Weight: 100},
		{PolicyID: 1, NodeID: "node-3", Weight: 90},
	}).Error; err != nil {
		t.Fatalf("seed targets: %v", err)
	}

	now := time.Now().UTC()
	readyUntil := now.Add(time.Hour)
	first := ReplicationTargetReadinessUpdate{
		PolicyID: 1, NodeID: "node-2", ExpectedOwnerEpoch: 9, EvaluatedAt: now,
		Ready: true, GenerationID: "gen-a", ManifestHash: "hash-a",
		RequiredDatasetCount: 2, CompletedDatasetCount: 2,
		LastVerifiedAt: &now, ReadyUntil: &readyUntil,
	}
	if err := UpdateReplicationTargetReadinessTxn(db, &first); err != nil {
		t.Fatalf("first readiness: %v", err)
	}
	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.ProtectionState != ReplicationProtectionStateDegraded {
		t.Fatalf("one ready target incorrectly armed policy: %q", policy.ProtectionState)
	}

	second := first
	second.NodeID = "node-3"
	second.GenerationID = "gen-b"
	second.ManifestHash = "hash-b"
	if err := UpdateReplicationTargetReadinessTxn(db, &second); err != nil {
		t.Fatalf("second readiness: %v", err)
	}
	db.First(&policy, 1)
	if policy.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("all complete fresh targets should arm policy: %q", policy.ProtectionState)
	}
}

func TestFSMReplicationPolicyProtectionStateCAS(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{})
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "state", GuestType: ReplicationGuestTypeJail, GuestID: 12,
		ActiveNodeID: "node-1", OwnerEpoch: 3, Enabled: true,
		ProtectionState: ReplicationProtectionStateSuspended,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	update := ReplicationPolicyProtectionStateUpdate{
		PolicyID: 1, ExpectedOwnerEpoch: 3, State: ReplicationProtectionStateArmed,
	}
	raw, _ := json.Marshal(update)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_protection_state", Action: "update", Data: raw,
	}); err != nil {
		t.Fatalf("update state: %v", err)
	}
	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("state not updated: %q", policy.ProtectionState)
	}

	update.ExpectedOwnerEpoch = 2
	update.State = ReplicationProtectionStateDegraded
	staleRaw, _ := json.Marshal(update)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_policy_protection_state", Action: "update", Data: staleRaw,
	}); err == nil || !strings.Contains(err.Error(), "cas_conflict") {
		t.Fatalf("expected stale state rejection, got %v", err)
	}
	db.First(&policy, 1)
	if policy.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("stale update changed state: %q", policy.ProtectionState)
	}
}

func TestReplicationPolicyEditPreservesOnlyUnchangedTargetReadiness(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	now := time.Now().UTC()
	policy := ReplicationPolicy{
		ID: 1, Name: "before", GuestType: ReplicationGuestTypeVM, GuestID: 10,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 2,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, Enabled: true,
		ProtectionState: ReplicationProtectionStateArmed,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&ReplicationPolicyTarget{
		PolicyID: 1, NodeID: "node-2", Weight: 100, Ready: true,
		GenerationID: "gen", OwnerEpoch: 2, ManifestHash: "hash",
		RequiredDatasetCount: 1, CompletedDatasetCount: 1,
		LastVerifiedAt: &now, ReadyUntil: ptrTime(now.Add(time.Hour)),
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	policy.Name = "after"
	if err := UpsertReplicationPolicyTxn(db, &policy, []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}}); err != nil {
		t.Fatalf("ordinary edit: %v", err)
	}
	var target ReplicationPolicyTarget
	db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target)
	if !target.Ready || target.GenerationID != "gen" {
		t.Fatalf("unchanged target lost readiness: %+v", target)
	}
	var persistedPolicy ReplicationPolicy
	if err := db.First(&persistedPolicy, policy.ID).Error; err != nil {
		t.Fatalf("reload metadata-edited policy: %v", err)
	}
	if persistedPolicy.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("metadata-only edit changed protection state: %q", persistedPolicy.ProtectionState)
	}

	if err := UpsertReplicationPolicyTxn(db, &policy, []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 200}}); err != nil {
		t.Fatalf("reconfigure target: %v", err)
	}
	target = ReplicationPolicyTarget{}
	db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target)
	if target.Ready || target.GenerationID != "" || target.OwnerEpoch != 0 {
		t.Fatalf("reconfigured target retained readiness: %+v", target)
	}
	if err := db.First(&persistedPolicy, policy.ID).Error; err != nil {
		t.Fatalf("reload reconfigured policy: %v", err)
	}
	if persistedPolicy.ProtectionState != ReplicationProtectionStateInitializing {
		t.Fatalf("reconfigured policy state = %q, want initializing", persistedPolicy.ProtectionState)
	}
}

func TestReplicationPolicyUpdateAtomicallyReflectsTargetReadinessInvalidation(t *testing.T) {
	tests := []struct {
		name            string
		initialTargets  []ReplicationPolicyTarget
		desiredTargets  []ReplicationPolicyTarget
		cronExpr        string
		wantState       string
		wantReadyByNode map[string]bool
	}{
		{
			name:           "metadata edit preserves all readiness",
			initialTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			desiredTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			cronExpr:       "*/5 * * * *",
			wantState:      ReplicationProtectionStateArmed,
			wantReadyByNode: map[string]bool{
				"node-2": true,
			},
		},
		{
			name:           "adding target initializes policy",
			initialTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			desiredTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}, {NodeID: "node-3", Weight: 50}},
			cronExpr:       "*/5 * * * *",
			wantState:      ReplicationProtectionStateInitializing,
			wantReadyByNode: map[string]bool{
				"node-2": true,
				"node-3": false,
			},
		},
		{
			name: "removing target initializes policy",
			initialTargets: []ReplicationPolicyTarget{
				{NodeID: "node-2", Weight: 100},
				{NodeID: "node-3", Weight: 50},
			},
			desiredTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			cronExpr:       "*/5 * * * *",
			wantState:      ReplicationProtectionStateInitializing,
			wantReadyByNode: map[string]bool{
				"node-2": true,
			},
		},
		{
			name:           "reweighting target clears readiness and initializes policy",
			initialTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			desiredTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 200}},
			cronExpr:       "*/5 * * * *",
			wantState:      ReplicationProtectionStateInitializing,
			wantReadyByNode: map[string]bool{
				"node-2": false,
			},
		},
		{
			name:           "schedule change clears readiness and initializes policy",
			initialTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			desiredTargets: []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
			cronExpr:       "*/15 * * * *",
			wantState:      ReplicationProtectionStateInitializing,
			wantReadyByNode: map[string]bool{
				"node-2": false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
			now := time.Now().UTC()
			policy := ReplicationPolicy{
				ID: 1, Name: "before", GuestType: ReplicationGuestTypeVM, GuestID: 10,
				SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 2,
				SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
				FailoverMode: ReplicationFailoverManual, CronExpr: "*/5 * * * *", Enabled: true,
				ProtectionState: ReplicationProtectionStateArmed,
			}
			if err := db.Create(&policy).Error; err != nil {
				t.Fatalf("seed policy: %v", err)
			}
			for i, target := range tt.initialTargets {
				target.PolicyID = policy.ID
				target.Ready = true
				target.GenerationID = fmt.Sprintf("generation-%d", i)
				target.OwnerEpoch = policy.OwnerEpoch
				target.ManifestHash = fmt.Sprintf("manifest-%d", i)
				target.RequiredDatasetCount = 1
				target.CompletedDatasetCount = 1
				target.LastVerifiedAt = &now
				target.ReadyUntil = ptrTime(now.Add(time.Hour))
				if err := db.Create(&target).Error; err != nil {
					t.Fatalf("seed target %s: %v", target.NodeID, err)
				}
			}

			updated := policy
			updated.Name = "after"
			updated.CronExpr = tt.cronExpr
			if err := UpdateReplicationPolicyTxn(db, &ReplicationPolicyPayload{
				Policy:             updated,
				Targets:            tt.desiredTargets,
				ExpectedOwnerEpoch: policy.OwnerEpoch,
			}); err != nil {
				t.Fatalf("update policy: %v", err)
			}

			var stored ReplicationPolicy
			if err := db.Preload("Targets").First(&stored, policy.ID).Error; err != nil {
				t.Fatalf("reload policy: %v", err)
			}
			if stored.ProtectionState != tt.wantState {
				t.Fatalf("protection state = %q, want %q", stored.ProtectionState, tt.wantState)
			}
			if len(stored.Targets) != len(tt.wantReadyByNode) {
				t.Fatalf("target count = %d, want %d", len(stored.Targets), len(tt.wantReadyByNode))
			}
			for _, target := range stored.Targets {
				wantReady, ok := tt.wantReadyByNode[target.NodeID]
				if !ok {
					t.Fatalf("unexpected target: %+v", target)
				}
				if target.Ready != wantReady {
					t.Fatalf("target %s ready = %t, want %t", target.NodeID, target.Ready, wantReady)
				}
				if !wantReady && (target.GenerationID != "" || target.OwnerEpoch != 0) {
					t.Fatalf("target %s retained readiness evidence: %+v", target.NodeID, target)
				}
			}
		})
	}
}

func TestReplicationPolicyUpdateCASCannotOverwriteCutoverState(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{})
	completed := time.Now().UTC()
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "after-cutover", GuestType: ReplicationGuestTypeVM, GuestID: 10,
		SourceNodeID: "node-2", ActiveNodeID: "node-2", OwnerEpoch: 2,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, Enabled: true,
		ProtectionState: ReplicationProtectionStateDegraded,
		TransitionState: ReplicationTransitionStateCompleted, TransitionRunID: "run-cutover",
		TransitionSourceNodeID: "node-1", TransitionTargetNodeID: "node-2",
		TransitionOwnerEpoch: 2, TransitionCompletedAt: &completed,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	stale := ReplicationPolicyPayload{
		ExpectedOwnerEpoch: 1,
		Policy: ReplicationPolicy{
			ID: 1, Name: "stale-form", GuestType: ReplicationGuestTypeVM, GuestID: 10,
			SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 1,
			SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
			FailoverMode: ReplicationFailoverManual, Enabled: true,
			ProtectionState: ReplicationProtectionStateArmed,
		},
	}
	if err := UpdateReplicationPolicyTxn(db, &stale); err == nil || !strings.Contains(err.Error(), "cas_conflict") {
		t.Fatalf("expected stale policy CAS rejection, got %v", err)
	}

	// Even a current-epoch payload cannot write ownership or transition fields.
	stale.ExpectedOwnerEpoch = 2
	stale.Policy.OwnerEpoch = 99
	stale.Policy.ActiveNodeID = "attacker-node"
	stale.Policy.ProtectionState = ReplicationProtectionStateArmed
	stale.Policy.TransitionState = ReplicationTransitionStateFailed
	stale.Policy.TransitionRunID = "other-run"
	if err := UpdateReplicationPolicyTxn(db, &stale); err != nil {
		t.Fatalf("current ordinary config edit: %v", err)
	}

	var policy ReplicationPolicy
	db.First(&policy, 1)
	if policy.Name != "stale-form" || policy.ActiveNodeID != "node-2" || policy.OwnerEpoch != 2 ||
		policy.ProtectionState != ReplicationProtectionStateDegraded ||
		policy.TransitionState != ReplicationTransitionStateCompleted || policy.TransitionRunID != "run-cutover" {
		t.Fatalf("ordinary update overwrote control-plane state: %+v", policy)
	}
}

func TestReplicationProtectionStateLegacyNormalization(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{})
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "legacy-enabled", GuestType: ReplicationGuestTypeVM, GuestID: 1,
		Enabled: true, OwnerEpoch: 1, ProtectionState: "",
	}).Error; err != nil {
		t.Fatalf("seed enabled: %v", err)
	}
	if err := db.Create(&ReplicationPolicy{
		ID: 2, Name: "legacy-disabled", GuestType: ReplicationGuestTypeVM, GuestID: 2,
		Enabled: false, OwnerEpoch: 1, ProtectionState: "",
	}).Error; err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	var enabled, disabled ReplicationPolicy
	db.First(&enabled, 1)
	db.First(&disabled, 2)
	if enabled.ProtectionState != ReplicationProtectionStateArmed {
		t.Fatalf("legacy enabled state = %q", enabled.ProtectionState)
	}
	if disabled.ProtectionState != ReplicationProtectionStateUnprotected {
		t.Fatalf("legacy disabled state = %q", disabled.ProtectionState)
	}
}

func TestReplicationLeaseUpsertIsPolicyBoundAndMonotonic(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
	now := time.Now().UTC()
	if err := db.Create(&ReplicationPolicy{
		ID: 1, Name: "lease", GuestType: ReplicationGuestTypeVM, GuestID: 50,
		ActiveNodeID: "node-new", OwnerEpoch: 2, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&ReplicationLease{
		PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 50,
		OwnerNodeID: "node-new", OwnerEpoch: 2, Version: 200,
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	stale := ReplicationLease{
		PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 50,
		OwnerNodeID: "node-old", OwnerEpoch: 1, Version: 999,
		ExpiresAt: now.Add(2 * time.Hour),
	}
	if err := UpsertReplicationLeaseTxn(db, &stale); err != nil {
		t.Fatalf("stale epoch should be a no-op: %v", err)
	}
	olderVersion := ReplicationLease{
		PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 50,
		OwnerNodeID: "node-new", OwnerEpoch: 2, Version: 199,
		ExpiresAt: now.Add(3 * time.Hour),
	}
	if err := UpsertReplicationLeaseTxn(db, &olderVersion); err != nil {
		t.Fatalf("old version should be a no-op: %v", err)
	}

	conflict := olderVersion
	conflict.OwnerNodeID = "node-other"
	conflict.Version = 201
	if err := UpsertReplicationLeaseTxn(db, &conflict); err == nil || !strings.Contains(err.Error(), "owner_mismatch") {
		t.Fatalf("expected same-epoch owner rejection, got %v", err)
	}
	future := olderVersion
	future.OwnerEpoch = 3
	future.Version = 300
	if err := UpsertReplicationLeaseTxn(db, &future); err == nil || !strings.Contains(err.Error(), "future_owner_epoch") {
		t.Fatalf("expected future epoch rejection, got %v", err)
	}

	var lease ReplicationLease
	db.Where("policy_id = ?", 1).First(&lease)
	if lease.OwnerNodeID != "node-new" || lease.OwnerEpoch != 2 || lease.Version != 200 || !lease.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("stale lease changed persisted value: %+v", lease)
	}
}

func TestReplicationLeaseBatchStaleEntryDoesNotRollbackValidRenewal(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationLease{})
	now := time.Now().UTC()
	if err := db.Create(&[]ReplicationPolicy{
		{ID: 1, Name: "moved", GuestType: ReplicationGuestTypeVM, GuestID: 1, ActiveNodeID: "node-b", OwnerEpoch: 2, Enabled: true},
		{ID: 2, Name: "steady", GuestType: ReplicationGuestTypeJail, GuestID: 2, ActiveNodeID: "node-c", OwnerEpoch: 1, Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	if err := db.Create(&ReplicationLease{
		PolicyID: 2, GuestType: ReplicationGuestTypeJail, GuestID: 2,
		OwnerNodeID: "node-c", OwnerEpoch: 1, Version: 10, ExpiresAt: now.Add(time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	raw, _ := json.Marshal([]ReplicationLease{
		{PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 1, OwnerNodeID: "node-a", OwnerEpoch: 1, Version: 500, ExpiresAt: now.Add(time.Hour)},
		{PolicyID: 2, GuestType: ReplicationGuestTypeJail, GuestID: 2, OwnerNodeID: "node-c", OwnerEpoch: 1, Version: 11, ExpiresAt: now.Add(time.Hour)},
	})
	if err := applyFSMCommand(t, fsm, Command{Type: "replication_lease", Action: "upsert_batch", Data: raw}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	var lease ReplicationLease
	db.Where("policy_id = ?", 2).First(&lease)
	if lease.Version != 11 || !lease.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("valid renewal was not committed: %+v", lease)
	}
	var staleCount int64
	db.Model(&ReplicationLease{}).Where("policy_id = ?", 1).Count(&staleCount)
	if staleCount != 0 {
		t.Fatalf("stale old-owner lease was inserted")
	}
}
