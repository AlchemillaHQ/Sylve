// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"fmt"
	"testing"
	"time"
)

func TestApplyReplicatedRetentionTxnBoundsReceiptsAndTransitionEvents(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ScheduledRunReceipt{},
		&ReplicationGuestOperationReceipt{},
		&ReplicationTransitionEvent{},
		&ReplicationEvent{},
	)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	receiptTimes := []time.Time{
		now.Add(-100 * 24 * time.Hour),
		now.Add(-4 * time.Hour),
		now.Add(-3 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-time.Hour),
	}
	for i, completedAt := range receiptTimes {
		if err := db.Create(&ScheduledRunReceipt{
			Token: fmt.Sprintf("receipt-%d", i), Kind: ScheduledRunKindBackup,
			ObjectID: 1, HolderNodeID: "node-1", Status: "success",
			CompletedAt: completedAt,
		}).Error; err != nil {
			t.Fatalf("seed receipt %d: %v", i, err)
		}
	}

	expiredAt := now.Add(-100 * 24 * time.Hour)
	activeStartedAt := now.Add(-200 * 24 * time.Hour)
	if err := db.Create(&ReplicationTransitionEvent{
		ID: 1, TransitionRunID: "expired", EventType: "failover", Status: "success",
		StartedAt: expiredAt, CompletedAt: &expiredAt,
	}).Error; err != nil {
		t.Fatalf("seed expired transition: %v", err)
	}
	if err := db.Create(&ReplicationTransitionEvent{
		ID: 2, TransitionRunID: "active", EventType: "failover", Status: "promoting",
		StartedAt: activeStartedAt,
	}).Error; err != nil {
		t.Fatalf("seed active transition: %v", err)
	}
	for i := 0; i < 3; i++ {
		completedAt := now.Add(-time.Duration(3-i) * time.Hour)
		if err := db.Create(&ReplicationTransitionEvent{
			ID: uint(3 + i), TransitionRunID: fmt.Sprintf("recent-%d", i),
			EventType: "failover", Status: "success",
			StartedAt: completedAt.Add(-time.Minute), CompletedAt: &completedAt,
		}).Error; err != nil {
			t.Fatalf("seed recent transition %d: %v", i, err)
		}
	}
	if err := db.Create(&ReplicationEvent{
		ID: 1, EventType: "replication", Status: "success", StartedAt: expiredAt, CompletedAt: &expiredAt,
	}).Error; err != nil {
		t.Fatalf("seed local event: %v", err)
	}
	for i, completedAt := range receiptTimes {
		if err := db.Create(&ReplicationGuestOperationReceipt{
			Token: fmt.Sprintf("guest-receipt-%d", i), GuestType: ReplicationGuestTypeVM,
			GuestID: uint(i + 1), Operation: ReplicationGuestOperationMigration,
			OwnerNodeID: "node-1", TargetNodeID: "node-2", TaskID: uint(i + 1),
			AcquiredAt: completedAt.Add(-time.Minute), CompletedAt: completedAt,
		}).Error; err != nil {
			t.Fatalf("seed guest receipt %d: %v", i, err)
		}
	}

	decision := ReplicatedRetentionDecision{
		ScheduledRunReceiptCutoff:    now.Add(-90 * 24 * time.Hour),
		ScheduledRunReceiptMaxRows:   2,
		GuestOperationReceiptCutoff:  now.Add(-90 * 24 * time.Hour),
		GuestOperationReceiptMaxRows: 2,
		ReplicationTransitionCutoff:  now.Add(-90 * 24 * time.Hour),
		ReplicationTransitionMaxRows: 3,
	}
	if err := ApplyReplicatedRetentionTxn(db, &decision); err != nil {
		t.Fatalf("apply retention: %v", err)
	}
	if err := ApplyReplicatedRetentionTxn(db, &decision); err != nil {
		t.Fatalf("reapply retention: %v", err)
	}

	var receipts []ScheduledRunReceipt
	if err := db.Order("completed_at ASC").Find(&receipts).Error; err != nil {
		t.Fatalf("load receipts: %v", err)
	}
	if len(receipts) != 2 || receipts[0].Token != "receipt-3" || receipts[1].Token != "receipt-4" {
		t.Fatalf("receipt retention mismatch: %+v", receipts)
	}
	var guestReceipts []ReplicationGuestOperationReceipt
	if err := db.Order("completed_at ASC").Find(&guestReceipts).Error; err != nil {
		t.Fatalf("load guest receipts: %v", err)
	}
	if len(guestReceipts) != 2 || guestReceipts[0].Token != "guest-receipt-3" ||
		guestReceipts[1].Token != "guest-receipt-4" {
		t.Fatalf("guest receipt retention mismatch: %+v", guestReceipts)
	}

	var transitions []ReplicationTransitionEvent
	if err := db.Order("id ASC").Find(&transitions).Error; err != nil {
		t.Fatalf("load transitions: %v", err)
	}
	if len(transitions) != 3 || transitions[0].TransitionRunID != "active" ||
		transitions[1].TransitionRunID != "recent-1" ||
		transitions[2].TransitionRunID != "recent-2" {
		t.Fatalf("transition retention mismatch: %+v", transitions)
	}

	var localCount int64
	if err := db.Model(&ReplicationEvent{}).Count(&localCount).Error; err != nil {
		t.Fatalf("count local events: %v", err)
	}
	if localCount != 1 {
		t.Fatalf("replicated retention touched local telemetry: count=%d", localCount)
	}
}
