// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
)

func replicationGuestOperationPolicy(id, guestID uint, enabled bool) ReplicationPolicy {
	state := ReplicationProtectionStateUnprotected
	if enabled {
		state = ReplicationProtectionStateInitializing
	}
	return ReplicationPolicy{
		ID: id, Name: "policy", GuestType: ReplicationGuestTypeVM, GuestID: guestID,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, CronExpr: "0 * * * *", Enabled: enabled,
		ProtectionState: state, TransitionState: ReplicationTransitionStateNone,
	}
}

func TestReplicationGuestOperationSerializesMigrationAndPolicyEnable(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{}, &ReplicationEvent{},
	)
	policy := replicationGuestOperationPolicy(1, 101, false)
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}

	staleEnable := policy
	staleEnable.Enabled = true
	staleEnable.ProtectionState = ReplicationProtectionStateInitializing
	now := time.Now().UTC()
	acquire := ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 101, Operation: ReplicationGuestOperationMigration,
		Token: "migration:node-a:77", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 77,
		AcquiredAt: now,
	}
	if err := AcquireReplicationGuestOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire migration guard: %v", err)
	}
	if err := AcquireReplicationGuestOperationTxn(db, &acquire); err != nil {
		t.Fatalf("same-token acquire replay failed: %v", err)
	}
	competing := acquire
	competing.Token = "migration:node-a:78"
	competing.TaskID = 78
	if err := AcquireReplicationGuestOperationTxn(db, &competing); err == nil ||
		!strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("competing migration guard was not rejected: %v", err)
	}

	if err := UpdateReplicationPolicyTxn(db, &ReplicationPolicyPayload{
		Policy: staleEnable, ExpectedOwnerEpoch: 1,
	}); err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("stale enable payload committed after migration acquire: %v", err)
	}
	var persisted ReplicationPolicy
	if err := db.First(&persisted, policy.ID).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if persisted.Enabled {
		t.Fatal("failed enable changed the policy")
	}

	newPolicy := replicationGuestOperationPolicy(2, 101, false)
	if err := UpsertReplicationPolicyTxn(db, &newPolicy, nil); err == nil ||
		!strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("policy create committed during migration: %v", err)
	}

	seal := ReplicationGuestOperationTransition{
		GuestType: ReplicationGuestTypeVM, GuestID: 101, Operation: ReplicationGuestOperationMigration,
		Token: acquire.Token, OccurredAt: now.Add(time.Second),
	}
	if err := SealReplicationGuestOperationTxn(db, &seal); err != nil {
		t.Fatalf("seal migration guard: %v", err)
	}
	if err := SealReplicationGuestOperationTxn(db, &seal); err != nil {
		t.Fatalf("seal replay failed: %v", err)
	}
	if err := AbortReplicationGuestOperationTxn(db, &seal); err == nil ||
		!strings.Contains(err.Error(), "already_cutover") {
		t.Fatalf("cutover guard was abortable: %v", err)
	}

	complete := seal
	complete.TargetNodeID = "node-b"
	complete.OccurredAt = now.Add(3 * time.Second)
	if err := CompleteReplicationGuestOperationTxn(db, &complete); err == nil ||
		!strings.Contains(err.Error(), "reassignment_incomplete") {
		t.Fatalf("guard completed before policy reassignment: %v", err)
	}
	if err := ReassignDisabledReplicationPolicyOwnerTxn(db, &ReplicationDisabledOwnerReassignment{
		PolicyID: policy.ID, ExpectedActiveNodeID: "node-a", ExpectedOwnerEpoch: 1,
		ActiveNodeID: "node-b", SourceNodeID: "node-b", OwnerEpoch: 2,
		Targets: []ReplicationPolicyTarget{{NodeID: "node-a", Weight: 100}},
		RunID:   "migration-owner-77", OperationToken: acquire.Token, OccurredAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("reassign disabled policy: %v", err)
	}
	missingTimestamp := complete
	missingTimestamp.OccurredAt = time.Time{}
	if err := CompleteReplicationGuestOperationTxn(db, &missingTimestamp); err == nil ||
		!strings.Contains(err.Error(), "timestamp_required") {
		t.Fatalf("completion accepted without a deterministic timestamp: %v", err)
	}
	if err := CompleteReplicationGuestOperationTxn(db, &complete); err != nil {
		t.Fatalf("complete migration guard: %v", err)
	}
	replay := complete
	replay.OccurredAt = complete.OccurredAt.Add(time.Second)
	if err := CompleteReplicationGuestOperationTxn(db, &replay); err != nil {
		t.Fatalf("complete migration guard replay: %v", err)
	}
	wrongTarget := replay
	wrongTarget.TargetNodeID = "node-c"
	if err := CompleteReplicationGuestOperationTxn(db, &wrongTarget); err == nil ||
		!strings.Contains(err.Error(), "completion_receipt_mismatch") {
		t.Fatalf("completion replay accepted a non-matching receipt: %v", err)
	}
	var operationCount int64
	if err := db.Model(&ReplicationGuestOperation{}).Count(&operationCount).Error; err != nil {
		t.Fatalf("count guards: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("completed guard was not removed: %d", operationCount)
	}

	var receipts []ReplicationGuestOperationReceipt
	if err := db.Order("token ASC").Find(&receipts).Error; err != nil {
		t.Fatalf("list migration completion receipts: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one migration completion receipt, got %d", len(receipts))
	}
	receipt := receipts[0]
	if receipt.Token != acquire.Token || receipt.Operation != acquire.Operation ||
		receipt.TaskID != acquire.TaskID || receipt.OwnerNodeID != acquire.OwnerNodeID ||
		receipt.TargetNodeID != acquire.TargetNodeID || receipt.GuestType != acquire.GuestType ||
		receipt.GuestID != acquire.GuestID || !receipt.AcquiredAt.Equal(acquire.AcquiredAt) ||
		!receipt.CompletedAt.Equal(complete.OccurredAt) {
		t.Fatalf("unexpected migration completion receipt: %+v", receipt)
	}

	duplicateReceipt := receipt
	duplicateReceipt.TargetNodeID = "node-c"
	if err := db.Create(&duplicateReceipt).Error; err == nil {
		t.Fatal("duplicate migration completion receipt token was accepted")
	}
	if err := CompleteReplicationGuestOperationTxn(db, &replay); err != nil {
		t.Fatalf("duplicate receipt attempt changed the valid replay result: %v", err)
	}
	var receiptCount int64
	if err := db.Model(&ReplicationGuestOperationReceipt{}).Count(&receiptCount).Error; err != nil {
		t.Fatalf("count migration completion receipts: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("expected one deterministic receipt, got %d", receiptCount)
	}
}

func TestReplicationGuestOperationEnableWinsRaftOrder(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{}, &ReplicationEvent{},
	)
	policy := replicationGuestOperationPolicy(3, 202, true)
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create enabled policy: %v", err)
	}
	err := AcquireReplicationGuestOperationTxn(db, &ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 202, Operation: ReplicationGuestOperationMigration,
		Token: "migration:node-a:88", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 88,
		AcquiredAt: time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("migration acquired after enable committed: %v", err)
	}
	var operationCount int64
	if countErr := db.Model(&ReplicationGuestOperation{}).Count(&operationCount).Error; countErr != nil {
		t.Fatalf("count guards: %v", countErr)
	}
	if operationCount != 0 {
		t.Fatal("failed acquire left a guest operation")
	}
}

func TestReplicationGuestOperationAcquireRejectsStaleLease(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{}, &ReplicationEvent{},
	)
	policy := replicationGuestOperationPolicy(4, 303, false)
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}
	if err := db.Create(&ReplicationLease{
		PolicyID: policy.ID, GuestType: ReplicationGuestTypeVM, GuestID: 303,
		OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: time.Now().Add(time.Minute), Version: 1,
	}).Error; err != nil {
		t.Fatalf("create stale lease: %v", err)
	}
	err := AcquireReplicationGuestOperationTxn(db, &ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 303, Operation: ReplicationGuestOperationMigration,
		Token: "migration:node-a:99", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 99,
		AcquiredAt: time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "lease_still_present") {
		t.Fatalf("migration acquired with a stale lease: %v", err)
	}
}

func TestReplicationGuestOperationAcquireRejectsDeletingPolicy(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{}, &ReplicationEvent{},
	)
	policy := replicationGuestOperationPolicy(5, 404, false)
	policy.ProtectionState = ReplicationProtectionStateDeleting
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create deleting policy: %v", err)
	}

	err := AcquireReplicationGuestOperationTxn(db, &ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 404, Operation: ReplicationGuestOperationMigration,
		Token: "migration:node-a:100", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 100,
		AcquiredAt: time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "replication_policy_deleting") {
		t.Fatalf("migration acquired while policy deletion was incomplete: %v", err)
	}
	var operationCount int64
	if countErr := db.Model(&ReplicationGuestOperation{}).Count(&operationCount).Error; countErr != nil {
		t.Fatalf("count guards: %v", countErr)
	}
	if operationCount != 0 {
		t.Fatal("failed acquire left a guest operation")
	}
}

func TestEmergencyRestoreGuestOperationSerializesWithoutMigrationPrivileges(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{}, &ReplicationEvent{},
	)
	policy := replicationGuestOperationPolicy(6, 505, false)
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}

	now := time.Now().UTC()
	acquire := ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 505,
		Operation: ReplicationGuestOperationEmergencyRestore,
		Token:     "emergency_restore:token-505", OwnerNodeID: "node-a", AcquiredAt: now,
	}
	if err := AcquireReplicationGuestOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire emergency restore guard: %v", err)
	}
	if err := AcquireReplicationGuestOperationTxn(db, &acquire); err != nil {
		t.Fatalf("same-token emergency restore replay failed: %v", err)
	}

	enable := policy
	enable.Enabled = true
	enable.ProtectionState = ReplicationProtectionStateInitializing
	if err := UpdateReplicationPolicyTxn(db, &ReplicationPolicyPayload{
		Policy: enable, ExpectedOwnerEpoch: policy.OwnerEpoch,
	}); err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("policy enabled during emergency restoration: %v", err)
	}

	migration := ReplicationGuestOperationAcquire{
		GuestType: ReplicationGuestTypeVM, GuestID: 505,
		Operation: ReplicationGuestOperationMigration,
		Token:     "migration:node-a:505", OwnerNodeID: "node-a", TargetNodeID: "node-b",
		TaskID: 505, AcquiredAt: now,
	}
	if err := AcquireReplicationGuestOperationTxn(db, &migration); err == nil ||
		!strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("migration acquired during emergency restoration: %v", err)
	}

	transition := ReplicationGuestOperationTransition{
		GuestType: ReplicationGuestTypeVM, GuestID: 505,
		Operation: ReplicationGuestOperationEmergencyRestore,
		Token:     acquire.Token, OccurredAt: now.Add(time.Second), TargetNodeID: "node-b",
	}
	if err := SealReplicationGuestOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "seal_requires_migration") {
		t.Fatalf("emergency restore guard gained seal privilege: %v", err)
	}
	if err := CompleteReplicationGuestOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "complete_requires_migration") {
		t.Fatalf("emergency restore guard gained migration completion privilege: %v", err)
	}

	wrongToken := transition
	wrongToken.Token = "emergency_restore:wrong"
	if err := AbortReplicationGuestOperationTxn(db, &wrongToken); err == nil ||
		!strings.Contains(err.Error(), "token_mismatch") {
		t.Fatalf("wrong token released emergency restore guard: %v", err)
	}
	transition.TargetNodeID = ""
	if err := AbortReplicationGuestOperationTxn(db, &transition); err != nil {
		t.Fatalf("release emergency restore guard: %v", err)
	}
	var count int64
	if err := db.Model(&ReplicationGuestOperation{}).Count(&count).Error; err != nil {
		t.Fatalf("count emergency restore guards: %v", err)
	}
	if count != 0 {
		t.Fatalf("released emergency restore guard remains: %d", count)
	}
}
