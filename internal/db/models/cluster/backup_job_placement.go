// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// BackupJobPlacementFence captures only replicated placement state. The leader
// obtains it after runner-local inventory validation and the FSM compares it in
// the same transaction as the backup-job mutation. No FSM path reads local
// inventory or performs network I/O.
type BackupJobPlacementFence struct {
	GuestType    string `json:"guestType"`
	GuestID      uint   `json:"guestId"`
	RunnerNodeID string `json:"runnerNodeId"`

	PolicyPresent                bool   `json:"policyPresent"`
	PolicyID                     uint   `json:"policyId,omitempty"`
	PolicyEnabled                bool   `json:"policyEnabled,omitempty"`
	PolicySourceNodeID           string `json:"policySourceNodeId,omitempty"`
	PolicyActiveNodeID           string `json:"policyActiveNodeId,omitempty"`
	PolicyOwnerEpoch             uint64 `json:"policyOwnerEpoch,omitempty"`
	PolicyTransitionState        string `json:"policyTransitionState,omitempty"`
	PolicyTransitionRunID        string `json:"policyTransitionRunId,omitempty"`
	PolicyTransitionSourceNodeID string `json:"policyTransitionSourceNodeId,omitempty"`
	PolicyTransitionTargetNodeID string `json:"policyTransitionTargetNodeId,omitempty"`
	PolicyTransitionOwnerEpoch   uint64 `json:"policyTransitionOwnerEpoch,omitempty"`

	OperationPresent      bool   `json:"operationPresent"`
	Operation             string `json:"operation,omitempty"`
	OperationState        string `json:"operationState,omitempty"`
	OperationToken        string `json:"operationToken,omitempty"`
	OperationOwnerNodeID  string `json:"operationOwnerNodeId,omitempty"`
	OperationTargetNodeID string `json:"operationTargetNodeId,omitempty"`
	OperationTaskID       uint   `json:"operationTaskId,omitempty"`
}

// BackupJobCommandPayload is the additive command envelope used by new
// create/update proposals. The FSM also accepts the legacy raw BackupJob JSON
// so pre-upgrade Raft log entries remain replayable.
type BackupJobCommandPayload struct {
	Job                    BackupJob                        `json:"job"`
	PlacementFence         *BackupJobPlacementFence         `json:"placementFence,omitempty"`
	PreviousPlacementFence *BackupJobPlacementFence         `json:"previousPlacementFence,omitempty"`
	TargetReadiness        *BackupTargetNodeReadinessUpdate `json:"targetReadiness,omitempty"`
}

func normalizeBackupJobPlacementFence(fence BackupJobPlacementFence) BackupJobPlacementFence {
	fence.GuestType = strings.ToLower(strings.TrimSpace(fence.GuestType))
	fence.RunnerNodeID = strings.TrimSpace(fence.RunnerNodeID)
	fence.PolicySourceNodeID = strings.TrimSpace(fence.PolicySourceNodeID)
	fence.PolicyActiveNodeID = strings.TrimSpace(fence.PolicyActiveNodeID)
	fence.PolicyTransitionState = strings.ToLower(strings.TrimSpace(fence.PolicyTransitionState))
	fence.PolicyTransitionRunID = strings.TrimSpace(fence.PolicyTransitionRunID)
	fence.PolicyTransitionSourceNodeID = strings.TrimSpace(fence.PolicyTransitionSourceNodeID)
	fence.PolicyTransitionTargetNodeID = strings.TrimSpace(fence.PolicyTransitionTargetNodeID)
	fence.Operation = strings.ToLower(strings.TrimSpace(fence.Operation))
	fence.OperationState = strings.ToLower(strings.TrimSpace(fence.OperationState))
	fence.OperationToken = strings.TrimSpace(fence.OperationToken)
	fence.OperationOwnerNodeID = strings.TrimSpace(fence.OperationOwnerNodeID)
	fence.OperationTargetNodeID = strings.TrimSpace(fence.OperationTargetNodeID)

	if !fence.PolicyPresent {
		fence.PolicyID = 0
		fence.PolicyEnabled = false
		fence.PolicySourceNodeID = ""
		fence.PolicyActiveNodeID = ""
		fence.PolicyOwnerEpoch = 0
		fence.PolicyTransitionState = ""
		fence.PolicyTransitionRunID = ""
		fence.PolicyTransitionSourceNodeID = ""
		fence.PolicyTransitionTargetNodeID = ""
		fence.PolicyTransitionOwnerEpoch = 0
	}
	if !fence.OperationPresent {
		fence.Operation = ""
		fence.OperationState = ""
		fence.OperationToken = ""
		fence.OperationOwnerNodeID = ""
		fence.OperationTargetNodeID = ""
		fence.OperationTaskID = 0
	}
	return fence
}

// LoadBackupJobPlacementFence reads the current replicated placement state for
// one guest. RunnerNodeID is part of the expected mutation but is not read from
// node-local health or inventory tables.
func LoadBackupJobPlacementFence(
	db *gorm.DB,
	guestType string,
	guestID uint,
	runnerNodeID string,
) (BackupJobPlacementFence, error) {
	if db == nil {
		return BackupJobPlacementFence{}, fmt.Errorf("backup_job_placement_database_required")
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	runnerNodeID = strings.TrimSpace(runnerNodeID)
	if (guestType != BackupJobModeVM && guestType != BackupJobModeJail) || guestID == 0 || runnerNodeID == "" {
		return BackupJobPlacementFence{}, fmt.Errorf("backup_job_placement_identity_invalid")
	}

	fence := BackupJobPlacementFence{
		GuestType:    guestType,
		GuestID:      guestID,
		RunnerNodeID: runnerNodeID,
	}

	if db.Migrator().HasTable(&ReplicationPolicy{}) {
		var policies []ReplicationPolicy
		if err := db.Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			Order("id ASC").Limit(2).Find(&policies).Error; err != nil {
			return BackupJobPlacementFence{}, fmt.Errorf("backup_job_placement_policy_read_failed: %w", err)
		}
		if len(policies) > 1 {
			return BackupJobPlacementFence{}, fmt.Errorf("backup_job_placement_policy_ambiguous")
		}
		if len(policies) == 1 {
			policy := policies[0]
			fence.PolicyPresent = true
			fence.PolicyID = policy.ID
			fence.PolicyEnabled = policy.Enabled
			fence.PolicySourceNodeID = policy.SourceNodeID
			fence.PolicyActiveNodeID = policy.ActiveNodeID
			fence.PolicyOwnerEpoch = policy.OwnerEpoch
			fence.PolicyTransitionState = policy.TransitionState
			fence.PolicyTransitionRunID = policy.TransitionRunID
			fence.PolicyTransitionSourceNodeID = policy.TransitionSourceNodeID
			fence.PolicyTransitionTargetNodeID = policy.TransitionTargetNodeID
			fence.PolicyTransitionOwnerEpoch = policy.TransitionOwnerEpoch
		}
	}

	if db.Migrator().HasTable(&ReplicationGuestOperation{}) {
		var operation ReplicationGuestOperation
		result := db.Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			Limit(1).Find(&operation)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return BackupJobPlacementFence{}, fmt.Errorf("backup_job_placement_operation_read_failed: %w", result.Error)
		}
		if result.RowsAffected != 0 {
			fence.OperationPresent = true
			fence.Operation = operation.Operation
			fence.OperationState = operation.State
			fence.OperationToken = operation.Token
			fence.OperationOwnerNodeID = operation.OwnerNodeID
			fence.OperationTargetNodeID = operation.TargetNodeID
			fence.OperationTaskID = operation.TaskID
		}
	}

	return normalizeBackupJobPlacementFence(fence), nil
}

func BackupJobPlacementFencesEqual(left, right BackupJobPlacementFence) bool {
	return normalizeBackupJobPlacementFence(left) == normalizeBackupJobPlacementFence(right)
}

// ValidateBackupJobPlacementFenceTxn compares the leader-supplied expected
// state with replicated state and is intended to run in the same transaction
// as the job create/update.
func ValidateBackupJobPlacementFenceTxn(
	db *gorm.DB,
	job *BackupJob,
	expected *BackupJobPlacementFence,
) error {
	if expected == nil {
		// Legacy log entries have no placement fence.
		return nil
	}
	if job == nil {
		return fmt.Errorf("backup_job_required")
	}

	normalized := normalizeBackupJobPlacementFence(*expected)
	if normalized.RunnerNodeID == "" || strings.TrimSpace(job.RunnerNodeID) != normalized.RunnerNodeID {
		return fmt.Errorf("backup_job_placement_runner_mismatch")
	}
	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	if normalized.GuestType != mode || (mode != BackupJobModeVM && mode != BackupJobModeJail) || normalized.GuestID == 0 {
		return fmt.Errorf("backup_job_placement_guest_mismatch")
	}

	current, err := LoadBackupJobPlacementFence(
		db,
		normalized.GuestType,
		normalized.GuestID,
		normalized.RunnerNodeID,
	)
	if err != nil {
		return err
	}
	if !BackupJobPlacementFencesEqual(current, normalized) {
		return fmt.Errorf("backup_job_placement_changed")
	}
	return nil
}
