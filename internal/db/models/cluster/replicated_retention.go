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
	"time"

	"gorm.io/gorm"
)

// ReplicatedRetentionDecision is leader-decided so every FSM applies the same
// cutoff and caps without consulting its local clock.
type ReplicatedRetentionDecision struct {
	ScheduledRunReceiptCutoff    time.Time `json:"scheduledRunReceiptCutoff"`
	ScheduledRunReceiptMaxRows   int       `json:"scheduledRunReceiptMaxRows"`
	GuestOperationReceiptCutoff  time.Time `json:"guestOperationReceiptCutoff"`
	GuestOperationReceiptMaxRows int       `json:"guestOperationReceiptMaxRows"`
	ReplicationTransitionCutoff  time.Time `json:"replicationTransitionCutoff"`
	ReplicationTransitionMaxRows int       `json:"replicationTransitionMaxRows"`
}

func ApplyReplicatedRetentionTxn(db *gorm.DB, decision *ReplicatedRetentionDecision) error {
	if db == nil || decision == nil {
		return fmt.Errorf("replicated_retention_input_invalid")
	}
	if decision.ScheduledRunReceiptCutoff.IsZero() ||
		decision.GuestOperationReceiptCutoff.IsZero() ||
		decision.ReplicationTransitionCutoff.IsZero() ||
		decision.ScheduledRunReceiptMaxRows <= 0 ||
		decision.GuestOperationReceiptMaxRows <= 0 ||
		decision.ReplicationTransitionMaxRows <= 0 {
		return fmt.Errorf("replicated_retention_decision_invalid")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&ScheduledRunReceipt{}) {
			if err := pruneScheduledRunReceipts(
				tx,
				decision.ScheduledRunReceiptCutoff.UTC(),
				decision.ScheduledRunReceiptMaxRows,
			); err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&ReplicationGuestOperationReceipt{}) {
			if err := pruneReplicationGuestOperationReceipts(
				tx,
				decision.GuestOperationReceiptCutoff.UTC(),
				decision.GuestOperationReceiptMaxRows,
			); err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&ReplicationTransitionEvent{}) {
			if err := pruneReplicationTransitionEvents(
				tx,
				decision.ReplicationTransitionCutoff.UTC(),
				decision.ReplicationTransitionMaxRows,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func pruneReplicationGuestOperationReceipts(tx *gorm.DB, cutoff time.Time, maxRows int) error {
	if err := tx.
		Where("completed_at < ?", cutoff).
		Delete(&ReplicationGuestOperationReceipt{}).Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_replication_guest_operation_receipts: %w", err)
	}

	var count int64
	if err := tx.Model(&ReplicationGuestOperationReceipt{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_count_replication_guest_operation_receipts: %w", err)
	}
	excess := count - int64(maxRows)
	if excess <= 0 {
		return nil
	}

	var tokens []string
	if err := tx.Model(&ReplicationGuestOperationReceipt{}).
		Order("completed_at ASC, token ASC").
		Limit(int(excess)).
		Pluck("token", &tokens).Error; err != nil {
		return fmt.Errorf("failed_to_select_replication_guest_operation_receipts_for_prune: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}
	if err := tx.Where("token IN ?", tokens).Delete(&ReplicationGuestOperationReceipt{}).Error; err != nil {
		return fmt.Errorf("failed_to_enforce_replication_guest_operation_receipt_cap: %w", err)
	}
	return nil
}

func pruneScheduledRunReceipts(tx *gorm.DB, cutoff time.Time, maxRows int) error {
	if err := tx.
		Where("completed_at < ?", cutoff).
		Delete(&ScheduledRunReceipt{}).Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_scheduled_run_receipts: %w", err)
	}

	var count int64
	if err := tx.Model(&ScheduledRunReceipt{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_count_scheduled_run_receipts: %w", err)
	}
	excess := count - int64(maxRows)
	if excess <= 0 {
		return nil
	}

	var tokens []string
	if err := tx.Model(&ScheduledRunReceipt{}).
		Order("completed_at ASC, token ASC").
		Limit(int(excess)).
		Pluck("token", &tokens).Error; err != nil {
		return fmt.Errorf("failed_to_select_scheduled_run_receipts_for_prune: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}
	if err := tx.Where("token IN ?", tokens).Delete(&ScheduledRunReceipt{}).Error; err != nil {
		return fmt.Errorf("failed_to_enforce_scheduled_run_receipt_cap: %w", err)
	}
	return nil
}

func pruneReplicationTransitionEvents(tx *gorm.DB, cutoff time.Time, maxRows int) error {
	if err := tx.
		Where("completed_at IS NOT NULL AND completed_at < ?", cutoff).
		Delete(&ReplicationTransitionEvent{}).Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_replication_transition_events: %w", err)
	}

	var activeCount int64
	if err := tx.Model(&ReplicationTransitionEvent{}).
		Where("completed_at IS NULL").
		Count(&activeCount).Error; err != nil {
		return fmt.Errorf("failed_to_count_active_replication_transition_events: %w", err)
	}

	var terminalCount int64
	if err := tx.Model(&ReplicationTransitionEvent{}).
		Where("completed_at IS NOT NULL").
		Count(&terminalCount).Error; err != nil {
		return fmt.Errorf("failed_to_count_terminal_replication_transition_events: %w", err)
	}

	terminalBudget := int64(maxRows) - activeCount
	if terminalBudget < 0 {
		terminalBudget = 0
	}
	excess := terminalCount - terminalBudget
	if excess <= 0 {
		return nil
	}

	var ids []uint
	if err := tx.Model(&ReplicationTransitionEvent{}).
		Where("completed_at IS NOT NULL").
		Order("completed_at ASC, id ASC").
		Limit(int(excess)).
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("failed_to_select_replication_transition_events_for_prune: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Where("id IN ?", ids).Delete(&ReplicationTransitionEvent{}).Error; err != nil {
		return fmt.Errorf("failed_to_enforce_replication_transition_event_cap: %w", err)
	}
	return nil
}
