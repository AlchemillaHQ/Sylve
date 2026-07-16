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
	"time"

	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReplicationGuestTypeVM   = "vm"
	ReplicationGuestTypeJail = "jail"

	ReplicationSourceModeFollowActive = "follow_active"
	ReplicationSourceModePinned       = "pinned_primary"

	ReplicationFailbackManual = "manual"
	ReplicationFailbackAuto   = "auto"

	ReplicationFailoverManual    = "manual"
	ReplicationFailoverAutoSafe  = "auto_safe"
	ReplicationFailoverAutoForce = "auto_force"

	ReplicationTransitionStateNone        = "none"
	ReplicationTransitionStateDemoting    = "demoting"
	ReplicationTransitionStateCatchup     = "catchup"
	ReplicationTransitionStatePromoting   = "promoting"
	ReplicationTransitionStateRollingBack = "rolling_back"
	ReplicationTransitionStateCompleted   = "completed"
	ReplicationTransitionStateFailed      = "failed"

	// ProtectionState is deliberately separate from Enabled. Enabled=false
	// means the guest is not protected at all (there is no ownership lease).
	// Enabled policies remain protected while they are initializing, degraded,
	// suspended for a planned transition, or being deleted.
	ReplicationProtectionStateUnprotected  = "unprotected"
	ReplicationProtectionStateInitializing = "initializing"
	ReplicationProtectionStateArmed        = "armed"
	ReplicationProtectionStateDegraded     = "degraded"
	ReplicationProtectionStateSuspended    = "suspended"
	ReplicationProtectionStateDeleting     = "deleting"

	ReplicationGuestOperationMigration = "migration"
	// ReplicationGuestOperationRestore blocks management-plane mutations while
	// a jail's on-disk state is being replaced from a backup.
	ReplicationGuestOperationRestore = "restore"
	// ReplicationGuestOperationEmergencyRestore is a short-lived, exact-token
	// exclusion lock used while a node reverses its own cold-start readonly
	// fence.  It deliberately shares the guest-wide lock table with migration,
	// but it can only be released from pre-cutover and is never eligible for the
	// migration seal/completion paths.
	ReplicationGuestOperationEmergencyRestore = "emergency_restore"
	ReplicationGuestOperationPreCutover       = "pre_cutover"
	ReplicationGuestOperationCutover          = "cutover"
)

type ReplicationPolicy struct {
	ID                             uint                      `gorm:"primaryKey" json:"id"`
	Name                           string                    `gorm:"not null" json:"name"`
	Description                    string                    `gorm:"type:text" json:"description"`
	GuestType                      string                    `gorm:"uniqueIndex:idx_replication_policy_guest_unique,priority:1;not null" json:"guestType"`
	GuestID                        uint                      `gorm:"uniqueIndex:idx_replication_policy_guest_unique,priority:2;not null" json:"guestId"`
	SourceNodeID                   string                    `gorm:"index" json:"sourceNodeId"`
	ActiveNodeID                   string                    `gorm:"index" json:"activeNodeId"`
	OwnerEpoch                     uint64                    `gorm:"not null;default:1" json:"ownerEpoch"`
	SourceMode                     string                    `gorm:"not null;default:follow_active" json:"sourceMode"`
	FailbackMode                   string                    `gorm:"not null;default:manual" json:"failbackMode"`
	FailoverMode                   string                    `gorm:"not null;default:manual" json:"failoverMode"`
	CronExpr                       string                    `gorm:"not null" json:"cronExpr"`
	CrashRecovery                  bool                      `gorm:"not null;default:true" json:"crashRecovery"`
	CrashRestartMax                int                       `gorm:"not null;default:3" json:"crashRestartMax"`
	PoolHealthCheck                bool                      `gorm:"not null;default:true" json:"poolHealthCheck"`
	PoolCapacityPct                int                       `gorm:"not null;default:90" json:"poolCapacityPct"`
	Enabled                        bool                      `gorm:"index" json:"enabled"`
	ProtectionState                string                    `gorm:"not null;default:'';index" json:"protectionState"`
	LastRunAt                      *time.Time                `json:"lastRunAt"`
	NextRunAt                      *time.Time                `gorm:"index" json:"nextRunAt"`
	LastStatus                     string                    `gorm:"index" json:"lastStatus"`
	LastError                      string                    `gorm:"type:text" json:"lastError"`
	TransitionState                string                    `gorm:"not null;default:none;index" json:"transitionState"`
	TransitionRunID                string                    `gorm:"index" json:"transitionRunId"`
	TransitionReason               string                    `json:"transitionReason"`
	TransitionSourceNodeID         string                    `gorm:"index" json:"transitionSourceNodeId"`
	TransitionTargetNodeID         string                    `gorm:"index" json:"transitionTargetNodeId"`
	TransitionOwnerEpoch           uint64                    `json:"transitionOwnerEpoch"`
	TransitionRequestedAt          *time.Time                `gorm:"index" json:"transitionRequestedAt"`
	TransitionDemotedAt            *time.Time                `json:"transitionDemotedAt"`
	TransitionCatchupAt            *time.Time                `json:"transitionCatchupAt"`
	TransitionPromotedAt           *time.Time                `json:"transitionPromotedAt"`
	TransitionCompletedAt          *time.Time                `gorm:"index" json:"transitionCompletedAt"`
	TransitionError                string                    `gorm:"type:text" json:"transitionError"`
	TransitionAllowUnsafe          bool                      `gorm:"not null;default:false" json:"transitionAllowUnsafe"`
	TransitionMovePinnedSource     bool                      `gorm:"not null;default:false" json:"transitionMovePinnedSource"`
	TransitionTriggerValidationRun bool                      `gorm:"not null;default:false" json:"transitionTriggerValidationRun"`
	TransitionOriginalRunning      *bool                     `json:"transitionOriginalRunning"`
	TransitionOriginalSourceNodeID string                    `gorm:"index" json:"transitionOriginalSourceNodeId"`
	TransitionGenerationID         string                    `gorm:"index" json:"transitionGenerationId"`
	TransitionGenerationOwnerEpoch uint64                    `json:"transitionGenerationOwnerEpoch"`
	TransitionGenerationManifest   string                    `json:"transitionGenerationManifest"`
	TransitionGenerationRootCount  int                       `gorm:"not null;default:0" json:"transitionGenerationRootCount"`
	HAEligible                     bool                      `gorm:"-" json:"haEligible"`
	HADegraded                     bool                      `gorm:"-" json:"haDegraded"`
	HAReasons                      []string                  `gorm:"-" json:"haReasons"`
	Targets                        []ReplicationPolicyTarget `json:"targets,omitempty" gorm:"foreignKey:PolicyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAt                      time.Time                 `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt                      time.Time                 `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ReplicationPolicyTransition struct {
	State                string     `json:"state"`
	RunID                string     `json:"runId"`
	Reason               string     `json:"reason"`
	SourceNodeID         string     `json:"sourceNodeId"`
	TargetNodeID         string     `json:"targetNodeId"`
	OwnerEpoch           uint64     `json:"ownerEpoch"`
	RequestedAt          *time.Time `json:"requestedAt"`
	DemotedAt            *time.Time `json:"demotedAt"`
	CatchupAt            *time.Time `json:"catchupAt"`
	PromotedAt           *time.Time `json:"promotedAt"`
	CompletedAt          *time.Time `json:"completedAt"`
	Error                string     `json:"error"`
	AllowUnsafe          bool       `json:"allowUnsafe"`
	MovePinnedSource     bool       `json:"movePinnedSource"`
	TriggerValidationRun bool       `json:"triggerValidationRun"`
	OriginalRunning      *bool      `json:"originalRunning"`
	OriginalSourceNodeID string     `json:"originalSourceNodeId"`
	GenerationID         string     `json:"generationId"`
	GenerationOwnerEpoch uint64     `json:"generationOwnerEpoch"`
	GenerationManifest   string     `json:"generationManifest"`
	GenerationRootCount  int        `json:"generationRootCount"`
}

type ReplicationPolicyTarget struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	PolicyID              uint       `gorm:"index;not null" json:"policyId"`
	NodeID                string     `gorm:"index;not null" json:"nodeId"`
	Weight                int        `gorm:"not null;default:100" json:"weight"`
	Ready                 bool       `gorm:"not null;default:false;index" json:"ready"`
	GenerationID          string     `gorm:"index" json:"generationId"`
	OwnerEpoch            uint64     `gorm:"not null;default:0;index" json:"ownerEpoch"`
	ManifestHash          string     `json:"manifestHash"`
	RequiredDatasetCount  int        `gorm:"not null;default:0" json:"requiredDatasetCount"`
	CompletedDatasetCount int        `gorm:"not null;default:0" json:"completedDatasetCount"`
	LastVerifiedAt        *time.Time `gorm:"index" json:"lastVerifiedAt"`
	ReadyUntil            *time.Time `gorm:"index" json:"readyUntil"`
	LastError             string     `gorm:"type:text" json:"lastError"`
	CreatedAt             time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt             time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ReplicationLease struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	PolicyID    uint      `gorm:"uniqueIndex;not null" json:"policyId"`
	GuestType   string    `gorm:"index;not null" json:"guestType"`
	GuestID     uint      `gorm:"index;not null" json:"guestId"`
	OwnerNodeID string    `gorm:"index;not null" json:"ownerNodeId"`
	OwnerEpoch  uint64    `gorm:"not null;default:1;index" json:"ownerEpoch"`
	ExpiresAt   time.Time `gorm:"index;not null" json:"expiresAt"`
	Version     uint64    `gorm:"not null;default:1" json:"version"`
	LastReason  string    `json:"lastReason"`
	LastActor   string    `json:"lastActor"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

// ReplicationGuestOperation is a durable, Raft-replicated exclusion lock for
// guest-wide operations that cannot safely overlap replication policy
// mutations. A migration lock deliberately has no automatic expiry: losing a
// worker must fail closed until the same operation is resumed or explicitly
// reconciled, rather than silently permitting protection to be enabled while
// cutover state is unknown.
type ReplicationGuestOperation struct {
	GuestType    string     `gorm:"primaryKey;size:16" json:"guestType"`
	GuestID      uint       `gorm:"primaryKey;autoIncrement:false" json:"guestId"`
	Operation    string     `gorm:"index;not null" json:"operation"`
	State        string     `gorm:"index;not null" json:"state"`
	Token        string     `gorm:"uniqueIndex;not null" json:"token"`
	OwnerNodeID  string     `gorm:"index;not null" json:"ownerNodeId"`
	TargetNodeID string     `gorm:"index;not null" json:"targetNodeId"`
	TaskID       uint       `gorm:"index;not null" json:"taskId"`
	AcquiredAt   time.Time  `gorm:"index;not null" json:"acquiredAt"`
	SealedAt     *time.Time `gorm:"index" json:"sealedAt"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

// ReplicationGuestOperationReceipt is the durable completion record for a
// guest operation. Token is supplied by the operation initiator and is the
// primary key so applying the same Raft command never depends on a local
// auto-increment sequence or clock.
type ReplicationGuestOperationReceipt struct {
	Token        string    `gorm:"primaryKey" json:"token"`
	GuestType    string    `gorm:"index;not null" json:"guestType"`
	GuestID      uint      `gorm:"index;not null;autoIncrement:false" json:"guestId"`
	Operation    string    `gorm:"index;not null" json:"operation"`
	OwnerNodeID  string    `gorm:"index;not null" json:"ownerNodeId"`
	TargetNodeID string    `gorm:"index;not null" json:"targetNodeId"`
	TaskID       uint      `gorm:"index;not null;autoIncrement:false" json:"taskId"`
	AcquiredAt   time.Time `gorm:"not null" json:"acquiredAt"`
	CompletedAt  time.Time `gorm:"index;not null" json:"completedAt"`
}

type ReplicationGuestOperationAcquire struct {
	GuestType    string    `json:"guestType"`
	GuestID      uint      `json:"guestId"`
	Operation    string    `json:"operation"`
	Token        string    `json:"token"`
	OwnerNodeID  string    `json:"ownerNodeId"`
	TargetNodeID string    `json:"targetNodeId"`
	TaskID       uint      `json:"taskId"`
	AcquiredAt   time.Time `json:"acquiredAt"`
}

type ReplicationGuestOperationTransition struct {
	GuestType    string    `json:"guestType"`
	GuestID      uint      `json:"guestId"`
	Operation    string    `json:"operation"`
	Token        string    `json:"token"`
	TargetNodeID string    `json:"targetNodeId,omitempty"`
	OccurredAt   time.Time `json:"occurredAt,omitempty"`
}

type ReplicationEvent struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	PolicyID        *uint      `gorm:"index" json:"policyId"`
	TransitionRunID string     `gorm:"not null;default:'';index" json:"transitionRunId"`
	EventType       string     `gorm:"index;not null" json:"eventType"`
	Status          string     `gorm:"index;not null" json:"status"`
	Message         string     `json:"message"`
	Error           string     `gorm:"type:text" json:"error"`
	Output          string     `gorm:"type:text" json:"output"`
	SourceNodeID    string     `gorm:"index" json:"sourceNodeId"`
	TargetNodeID    string     `gorm:"index" json:"targetNodeId"`
	GuestType       string     `gorm:"index" json:"guestType"`
	GuestID         uint       `gorm:"index" json:"guestId"`
	StartedAt       time.Time  `gorm:"index" json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	CreatedAt       time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt       time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ClusterSSHIdentity struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeUUID  string    `gorm:"uniqueIndex;not null" json:"nodeUUID"`
	SSHUser   string    `gorm:"not null;default:root" json:"sshUser"`
	SSHHost   string    `gorm:"not null" json:"sshHost"`
	SSHPort   int       `gorm:"not null;default:8183" json:"sshPort"`
	PublicKey string    `gorm:"type:text;not null" json:"publicKey"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ReplicationPolicyPayload struct {
	Policy             ReplicationPolicy         `json:"policy"`
	Targets            []ReplicationPolicyTarget `json:"targets"`
	ExpectedOwnerEpoch uint64                    `json:"expectedOwnerEpoch,omitempty"`
}

// ReplicationOwnershipTransitionPayload is the single Raft/FSM mutation used
// at ownership cutover. ExpectedActiveNodeID and ExpectedOwnerEpoch form the
// compare-and-swap guard. When ReplaceTargets is true, Targets is the complete
// post-cutover topology. SourceNodeID is optional; nil preserves the current
// pinned/preferred source.
type ReplicationOwnershipTransitionPayload struct {
	PolicyID                uint   `json:"policyId"`
	ExpectedActiveNodeID    string `json:"expectedActiveNodeId"`
	ExpectedOwnerEpoch      uint64 `json:"expectedOwnerEpoch"`
	ExpectedTransitionRunID string `json:"expectedTransitionRunId"`
	// PreviousLeaseExpiresAtOrBefore is an optional force-cutover fence. When
	// present, the ownership transaction re-reads the previous owner's lease
	// and rejects the cutover if that matching lease is still valid after this
	// instant. Keeping this predicate in the same transaction as the owner CAS
	// closes the gap between a leader-side expiry check and the Raft commit.
	PreviousLeaseExpiresAtOrBefore *time.Time                  `json:"previousLeaseExpiresAtOrBefore,omitempty"`
	ActiveNodeID                   string                      `json:"activeNodeId"`
	SourceNodeID                   *string                     `json:"sourceNodeId,omitempty"`
	OwnerEpoch                     uint64                      `json:"ownerEpoch"`
	ReplaceTargets                 bool                        `json:"replaceTargets"`
	Targets                        []ReplicationPolicyTarget   `json:"targets"`
	Lease                          ReplicationLease            `json:"lease"`
	Transition                     ReplicationPolicyTransition `json:"transition"`
	ProtectionState                string                      `json:"protectionState,omitempty"`
}

// ReplicationDisabledOwnerReassignment moves the dormant ownership metadata
// after a guest migration. The policy must remain disabled/unprotected; no
// writable lease is created. Re-enabling later starts from the new owner and a
// fully invalidated target generation.
type ReplicationDisabledOwnerReassignment struct {
	PolicyID             uint                      `json:"policyId"`
	ExpectedActiveNodeID string                    `json:"expectedActiveNodeId"`
	ExpectedOwnerEpoch   uint64                    `json:"expectedOwnerEpoch"`
	ActiveNodeID         string                    `json:"activeNodeId"`
	SourceNodeID         string                    `json:"sourceNodeId"`
	OwnerEpoch           uint64                    `json:"ownerEpoch"`
	Targets              []ReplicationPolicyTarget `json:"targets"`
	RunID                string                    `json:"runId"`
	OperationToken       string                    `json:"operationToken"`
	OccurredAt           time.Time                 `json:"occurredAt"`
}

// ReplicationTargetReadinessUpdate updates one target without rewriting the
// policy. ExpectedOwnerEpoch prevents a late replication result from an old
// owner generation from making a target ready after ownership has moved.
type ReplicationTargetReadinessUpdate struct {
	PolicyID              uint       `json:"policyId"`
	NodeID                string     `json:"nodeId"`
	ExpectedOwnerEpoch    uint64     `json:"expectedOwnerEpoch"`
	EvaluatedAt           time.Time  `json:"evaluatedAt"`
	Ready                 bool       `json:"ready"`
	GenerationID          string     `json:"generationId"`
	ManifestHash          string     `json:"manifestHash"`
	RequiredDatasetCount  int        `json:"requiredDatasetCount"`
	CompletedDatasetCount int        `json:"completedDatasetCount"`
	LastVerifiedAt        *time.Time `json:"lastVerifiedAt"`
	ReadyUntil            *time.Time `json:"readyUntil"`
	LastError             string     `json:"lastError"`
	TransitionRunID       string     `json:"transitionRunId,omitempty"`
}

type ReplicationPolicyProtectionStateUpdate struct {
	PolicyID           uint   `json:"policyId"`
	ExpectedOwnerEpoch uint64 `json:"expectedOwnerEpoch"`
	State              string `json:"state"`
}

// ReplicationPolicyTransitionBegin acquires the durable per-policy transition
// lock. Replaying the same RunID is idempotent; a different RunID is rejected
// while an existing transition is in progress.
type ReplicationPolicyTransitionBegin struct {
	PolicyID           uint                        `json:"policyId"`
	ExpectedOwnerEpoch uint64                      `json:"expectedOwnerEpoch"`
	Transition         ReplicationPolicyTransition `json:"transition"`
	ProtectionState    string                      `json:"protectionState,omitempty"`
}

func validReplicationGuestType(v string) bool {
	return v == ReplicationGuestTypeVM || v == ReplicationGuestTypeJail
}

func normalizeReplicationGuestOperationIdentity(guestType string, guestID uint) (string, error) {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if !validReplicationGuestType(guestType) {
		return "", fmt.Errorf("invalid_replication_guest_type")
	}
	if guestID == 0 {
		return "", fmt.Errorf("replication_guest_id_required")
	}
	return guestType, nil
}

func requireNoReplicationGuestOperation(tx *gorm.DB, guestType string, guestID uint) error {
	if tx == nil || !replicationguard.GuestOperationSchemaReady(tx) {
		return nil
	}
	guestType, err := normalizeReplicationGuestOperationIdentity(guestType, guestID)
	if err != nil {
		return err
	}
	var operation ReplicationGuestOperation
	err = tx.Where("guest_type = ? AND guest_id = ?", guestType, guestID).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("guest_operation_in_progress: %s", strings.TrimSpace(operation.Operation))
}

func acquireReplicationGuestOperation(db *gorm.DB, payload *ReplicationGuestOperationAcquire) error {
	if payload == nil {
		return fmt.Errorf("replication_guest_operation_required")
	}
	guestType, err := normalizeReplicationGuestOperationIdentity(payload.GuestType, payload.GuestID)
	if err != nil {
		return err
	}
	payload.GuestType = guestType
	payload.Operation = strings.ToLower(strings.TrimSpace(payload.Operation))
	payload.Token = strings.TrimSpace(payload.Token)
	payload.OwnerNodeID = strings.TrimSpace(payload.OwnerNodeID)
	payload.TargetNodeID = strings.TrimSpace(payload.TargetNodeID)
	if payload.Operation != ReplicationGuestOperationMigration &&
		payload.Operation != ReplicationGuestOperationEmergencyRestore &&
		payload.Operation != ReplicationGuestOperationRestore {
		return fmt.Errorf("invalid_replication_guest_operation")
	}
	if payload.Token == "" || payload.OwnerNodeID == "" || payload.AcquiredAt.IsZero() {
		return fmt.Errorf("replication_guest_operation_identity_required")
	}
	switch payload.Operation {
	case ReplicationGuestOperationMigration:
		if payload.TargetNodeID == "" || payload.TaskID == 0 {
			return fmt.Errorf("replication_guest_operation_identity_required")
		}
		if payload.OwnerNodeID == payload.TargetNodeID {
			return fmt.Errorf("replication_guest_operation_target_must_differ")
		}
	case ReplicationGuestOperationEmergencyRestore:
		if payload.TargetNodeID != "" || payload.TaskID != 0 {
			return fmt.Errorf("replication_emergency_restore_scope_invalid")
		}
	case ReplicationGuestOperationRestore:
		if payload.TargetNodeID != "" || payload.TaskID == 0 {
			return fmt.Errorf("replication_restore_scope_invalid")
		}
	}
	payload.AcquiredAt = payload.AcquiredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationGuestOperation
		err := tx.Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			First(&existing).Error
		if err == nil {
			if strings.TrimSpace(existing.Operation) == payload.Operation &&
				strings.TrimSpace(existing.Token) == payload.Token &&
				strings.TrimSpace(existing.OwnerNodeID) == payload.OwnerNodeID &&
				strings.TrimSpace(existing.TargetNodeID) == payload.TargetNodeID &&
				existing.TaskID == payload.TaskID {
				return nil
			}
			return fmt.Errorf("guest_operation_in_progress: %s", strings.TrimSpace(existing.Operation))
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if payload.Operation == ReplicationGuestOperationMigration {
			if err := requireReplicationGuestQuiescentForMigration(tx, payload.GuestType, payload.GuestID); err != nil {
				return err
			}
		}

		return tx.Create(&ReplicationGuestOperation{
			GuestType:    payload.GuestType,
			GuestID:      payload.GuestID,
			Operation:    payload.Operation,
			State:        ReplicationGuestOperationPreCutover,
			Token:        payload.Token,
			OwnerNodeID:  payload.OwnerNodeID,
			TargetNodeID: payload.TargetNodeID,
			TaskID:       payload.TaskID,
			AcquiredAt:   payload.AcquiredAt,
		}).Error
	})
}

func normalizeReplicationGuestOperationTransition(payload *ReplicationGuestOperationTransition) error {
	if payload == nil {
		return fmt.Errorf("replication_guest_operation_required")
	}
	guestType, err := normalizeReplicationGuestOperationIdentity(payload.GuestType, payload.GuestID)
	if err != nil {
		return err
	}
	payload.GuestType = guestType
	payload.Operation = strings.ToLower(strings.TrimSpace(payload.Operation))
	payload.Token = strings.TrimSpace(payload.Token)
	if (payload.Operation != ReplicationGuestOperationMigration &&
		payload.Operation != ReplicationGuestOperationEmergencyRestore &&
		payload.Operation != ReplicationGuestOperationRestore) || payload.Token == "" {
		return fmt.Errorf("replication_guest_operation_identity_required")
	}
	payload.TargetNodeID = strings.TrimSpace(payload.TargetNodeID)
	if !payload.OccurredAt.IsZero() {
		payload.OccurredAt = payload.OccurredAt.UTC()
	}
	return nil
}

func requireReplicationGuestQuiescentForMigration(tx *gorm.DB, guestType string, guestID uint) error {
	var enabledPolicyCount int64
	if err := tx.Model(&ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).
		Count(&enabledPolicyCount).Error; err != nil {
		return err
	}
	if enabledPolicyCount > 0 {
		return fmt.Errorf("replication_policy_must_be_disabled_before_migration")
	}
	var deletingPolicyCount int64
	if err := tx.Model(&ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ? AND protection_state = ?",
			guestType, guestID, ReplicationProtectionStateDeleting).
		Count(&deletingPolicyCount).Error; err != nil {
		return err
	}
	if deletingPolicyCount > 0 {
		return fmt.Errorf("replication_policy_deleting")
	}

	var transitionCount int64
	if err := tx.Model(&ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ? AND transition_state IN ?", guestType, guestID, []string{
			ReplicationTransitionStateDemoting,
			ReplicationTransitionStateCatchup,
			ReplicationTransitionStatePromoting,
			ReplicationTransitionStateRollingBack,
		}).Count(&transitionCount).Error; err != nil {
		return err
	}
	if transitionCount > 0 {
		return fmt.Errorf("replication_policy_transition_in_progress")
	}

	var leaseCount int64
	if err := tx.Model(&ReplicationLease{}).
		Where("guest_type = ? AND guest_id = ?", guestType, guestID).
		Count(&leaseCount).Error; err != nil {
		return err
	}
	if leaseCount > 0 {
		return fmt.Errorf("replication_guest_lease_still_present")
	}
	return nil
}

func sealReplicationGuestOperation(db *gorm.DB, payload *ReplicationGuestOperationTransition) error {
	if err := normalizeReplicationGuestOperationTransition(payload); err != nil {
		return err
	}
	if payload.Operation != ReplicationGuestOperationMigration {
		return fmt.Errorf("replication_guest_operation_seal_requires_migration")
	}
	if payload.OccurredAt.IsZero() {
		return fmt.Errorf("replication_guest_operation_timestamp_required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationGuestOperation
		if err := tx.Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			First(&existing).Error; err != nil {
			return err
		}
		if strings.TrimSpace(existing.Operation) != payload.Operation ||
			strings.TrimSpace(existing.Token) != payload.Token {
			return fmt.Errorf("replication_guest_operation_token_mismatch")
		}
		if existing.State == ReplicationGuestOperationCutover {
			return nil
		}
		if existing.State != ReplicationGuestOperationPreCutover {
			return fmt.Errorf("invalid_replication_guest_operation_state")
		}
		if err := requireReplicationGuestQuiescentForMigration(tx, payload.GuestType, payload.GuestID); err != nil {
			return err
		}
		result := tx.Model(&ReplicationGuestOperation{}).
			Where("guest_type = ? AND guest_id = ? AND operation = ? AND token = ? AND state = ?",
				payload.GuestType, payload.GuestID, payload.Operation, payload.Token,
				ReplicationGuestOperationPreCutover).
			Updates(map[string]any{
				"state":      ReplicationGuestOperationCutover,
				"sealed_at":  payload.OccurredAt,
				"updated_at": payload.OccurredAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_guest_operation_cas_conflict")
		}
		return nil
	})
}

func abortReplicationGuestOperation(db *gorm.DB, payload *ReplicationGuestOperationTransition) error {
	if err := normalizeReplicationGuestOperationTransition(payload); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationGuestOperation
		err := tx.Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(existing.Operation) != payload.Operation ||
			strings.TrimSpace(existing.Token) != payload.Token {
			return fmt.Errorf("replication_guest_operation_token_mismatch")
		}
		if existing.State != ReplicationGuestOperationPreCutover {
			return fmt.Errorf("replication_guest_operation_already_cutover")
		}
		result := tx.Where(
			"guest_type = ? AND guest_id = ? AND operation = ? AND token = ? AND state = ?",
			payload.GuestType, payload.GuestID, payload.Operation, payload.Token,
			ReplicationGuestOperationPreCutover,
		).Delete(&ReplicationGuestOperation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_guest_operation_token_mismatch")
		}
		return nil
	})
}

func completeReplicationGuestOperation(db *gorm.DB, payload *ReplicationGuestOperationTransition) error {
	if err := normalizeReplicationGuestOperationTransition(payload); err != nil {
		return err
	}
	if payload.Operation != ReplicationGuestOperationMigration {
		return fmt.Errorf("replication_guest_operation_complete_requires_migration")
	}
	if payload.TargetNodeID == "" {
		return fmt.Errorf("replication_guest_operation_target_required")
	}
	if payload.OccurredAt.IsZero() {
		return fmt.Errorf("replication_guest_operation_timestamp_required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationGuestOperation
		err := tx.Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var receipt ReplicationGuestOperationReceipt
			receiptErr := tx.Where("token = ?", payload.Token).First(&receipt).Error
			if errors.Is(receiptErr, gorm.ErrRecordNotFound) {
				return fmt.Errorf("replication_guest_operation_completion_receipt_not_found")
			}
			if receiptErr != nil {
				return receiptErr
			}
			if receipt.Token != payload.Token ||
				strings.TrimSpace(receipt.Operation) != payload.Operation ||
				strings.TrimSpace(receipt.GuestType) != payload.GuestType ||
				receipt.GuestID != payload.GuestID ||
				strings.TrimSpace(receipt.TargetNodeID) != payload.TargetNodeID ||
				strings.TrimSpace(receipt.OwnerNodeID) == "" ||
				receipt.TaskID == 0 ||
				receipt.AcquiredAt.IsZero() ||
				receipt.CompletedAt.IsZero() {
				return fmt.Errorf("replication_guest_operation_completion_receipt_mismatch")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(existing.Operation) != payload.Operation ||
			strings.TrimSpace(existing.Token) != payload.Token {
			return fmt.Errorf("replication_guest_operation_token_mismatch")
		}
		if existing.State != ReplicationGuestOperationCutover ||
			strings.TrimSpace(existing.TargetNodeID) != payload.TargetNodeID {
			return fmt.Errorf("replication_guest_operation_cutover_mismatch")
		}

		var policies []ReplicationPolicy
		if err := tx.Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			Find(&policies).Error; err != nil {
			return err
		}
		for i := range policies {
			policy := &policies[i]
			if policy.Enabled || policy.ProtectionState != ReplicationProtectionStateUnprotected ||
				replicationTransitionInProgress(policy.TransitionState) ||
				strings.TrimSpace(policy.ActiveNodeID) != payload.TargetNodeID ||
				strings.TrimSpace(policy.SourceNodeID) != payload.TargetNodeID {
				return fmt.Errorf("replication_policy_migration_reassignment_incomplete")
			}
		}
		var leaseCount int64
		if err := tx.Model(&ReplicationLease{}).
			Where("guest_type = ? AND guest_id = ?", payload.GuestType, payload.GuestID).
			Count(&leaseCount).Error; err != nil {
			return err
		}
		if leaseCount != 0 {
			return fmt.Errorf("replication_guest_lease_still_present")
		}

		if err := tx.Create(&ReplicationGuestOperationReceipt{
			Token:        existing.Token,
			GuestType:    existing.GuestType,
			GuestID:      existing.GuestID,
			Operation:    existing.Operation,
			OwnerNodeID:  existing.OwnerNodeID,
			TargetNodeID: existing.TargetNodeID,
			TaskID:       existing.TaskID,
			AcquiredAt:   existing.AcquiredAt,
			CompletedAt:  payload.OccurredAt,
		}).Error; err != nil {
			return err
		}

		result := tx.Where(
			"guest_type = ? AND guest_id = ? AND operation = ? AND token = ? AND state = ?",
			payload.GuestType, payload.GuestID, payload.Operation, payload.Token,
			ReplicationGuestOperationCutover,
		).Delete(&ReplicationGuestOperation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_guest_operation_cas_conflict")
		}
		return nil
	})
}

func validReplicationSourceMode(v string) bool {
	return v == ReplicationSourceModeFollowActive || v == ReplicationSourceModePinned
}

func validReplicationFailbackMode(v string) bool {
	return v == ReplicationFailbackManual || v == ReplicationFailbackAuto
}

func validReplicationFailoverMode(v string) bool {
	return v == ReplicationFailoverManual || v == ReplicationFailoverAutoSafe || v == ReplicationFailoverAutoForce
}

func validReplicationTransitionState(v string) bool {
	switch v {
	case ReplicationTransitionStateNone,
		ReplicationTransitionStateDemoting,
		ReplicationTransitionStateCatchup,
		ReplicationTransitionStatePromoting,
		ReplicationTransitionStateRollingBack,
		ReplicationTransitionStateCompleted,
		ReplicationTransitionStateFailed:
		return true
	default:
		return false
	}
}

func replicationTransitionInProgress(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case ReplicationTransitionStateDemoting,
		ReplicationTransitionStateCatchup,
		ReplicationTransitionStatePromoting,
		ReplicationTransitionStateRollingBack:
		return true
	default:
		return false
	}
}

func validReplicationProtectionState(v string) bool {
	switch v {
	case ReplicationProtectionStateUnprotected,
		ReplicationProtectionStateInitializing,
		ReplicationProtectionStateArmed,
		ReplicationProtectionStateDegraded,
		ReplicationProtectionStateSuspended,
		ReplicationProtectionStateDeleting:
		return true
	default:
		return false
	}
}

// normalizeReplicationProtectionState keeps rows written by older versions
// safe during a rolling upgrade. An enabled legacy row was already treated as
// protected, so an empty state normalizes to armed. A disabled row is
// unprotected except while its durable deletion lifecycle is in progress.
func normalizeReplicationProtectionState(p *ReplicationPolicy) {
	if p == nil {
		return
	}
	p.ProtectionState = strings.TrimSpace(strings.ToLower(p.ProtectionState))
	if !p.Enabled {
		if p.ProtectionState != ReplicationProtectionStateDeleting {
			p.ProtectionState = ReplicationProtectionStateUnprotected
		}
		return
	}
	if p.ProtectionState == "" || p.ProtectionState == ReplicationProtectionStateUnprotected {
		p.ProtectionState = ReplicationProtectionStateArmed
	}
}

// AfterFind provides backwards-safe in-memory normalization for rows created
// before ProtectionState existed.
func (p *ReplicationPolicy) AfterFind(_ *gorm.DB) error {
	normalizeReplicationProtectionState(p)
	return nil
}

func normalizeReplicationPolicy(p *ReplicationPolicy) {
	if p == nil {
		return
	}

	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.GuestType = strings.TrimSpace(strings.ToLower(p.GuestType))
	p.SourceNodeID = strings.TrimSpace(p.SourceNodeID)
	p.ActiveNodeID = strings.TrimSpace(p.ActiveNodeID)
	p.SourceMode = strings.TrimSpace(strings.ToLower(p.SourceMode))
	p.FailbackMode = strings.TrimSpace(strings.ToLower(p.FailbackMode))
	p.FailoverMode = strings.TrimSpace(strings.ToLower(p.FailoverMode))
	p.CronExpr = strings.TrimSpace(p.CronExpr)
	p.LastStatus = strings.TrimSpace(p.LastStatus)
	p.LastError = strings.TrimSpace(p.LastError)
	p.ProtectionState = strings.TrimSpace(strings.ToLower(p.ProtectionState))
	p.TransitionState = strings.TrimSpace(strings.ToLower(p.TransitionState))
	p.TransitionRunID = strings.TrimSpace(p.TransitionRunID)
	p.TransitionReason = strings.TrimSpace(p.TransitionReason)
	p.TransitionSourceNodeID = strings.TrimSpace(p.TransitionSourceNodeID)
	p.TransitionTargetNodeID = strings.TrimSpace(p.TransitionTargetNodeID)
	p.TransitionError = strings.TrimSpace(p.TransitionError)
	normalizeReplicationProtectionState(p)
	if p.SourceMode == "" {
		p.SourceMode = ReplicationSourceModeFollowActive
	}
	if p.FailbackMode == "" {
		p.FailbackMode = ReplicationFailbackManual
	}
	if p.FailoverMode == "" {
		p.FailoverMode = ReplicationFailoverManual
	}
	if p.TransitionState == "" {
		p.TransitionState = ReplicationTransitionStateNone
	}
}

func validateReplicationPolicy(policy *ReplicationPolicy) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}

	normalizeReplicationPolicy(policy)
	if policy.Name == "" {
		return fmt.Errorf("replication_policy_name_required")
	}
	if len(policy.Description) > 1024 {
		return fmt.Errorf("replication_policy_description_too_long")
	}
	if !validReplicationGuestType(policy.GuestType) {
		return fmt.Errorf("invalid_replication_guest_type")
	}
	if !validReplicationSourceMode(policy.SourceMode) {
		return fmt.Errorf("invalid_replication_source_mode")
	}
	if !validReplicationFailbackMode(policy.FailbackMode) {
		return fmt.Errorf("invalid_replication_failback_mode")
	}
	if !validReplicationFailoverMode(policy.FailoverMode) {
		return fmt.Errorf("invalid_replication_failover_mode")
	}
	if !validReplicationTransitionState(policy.TransitionState) {
		return fmt.Errorf("invalid_replication_transition_state")
	}
	if !validReplicationProtectionState(policy.ProtectionState) {
		return fmt.Errorf("invalid_replication_protection_state")
	}
	if policy.OwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_required")
	}
	return nil
}

func upsertReplicationPolicy(db *gorm.DB, policy *ReplicationPolicy, targets []ReplicationPolicyTarget) error {
	if err := validateReplicationPolicy(policy); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := requireNoReplicationGuestOperation(tx, policy.GuestType, policy.GuestID); err != nil {
			return err
		}
		var existing ReplicationPolicy
		existingErr := tx.Preload("Targets").First(&existing, policy.ID).Error
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		if existingErr == nil && replicationTransitionInProgress(existing.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}

		preserveTargetReadiness := existingErr == nil &&
			existing.Enabled == policy.Enabled &&
			existing.CronExpr == policy.CronExpr &&
			existing.GuestType == policy.GuestType &&
			existing.GuestID == policy.GuestID &&
			strings.TrimSpace(existing.ActiveNodeID) == policy.ActiveNodeID &&
			existing.OwnerEpoch == policy.OwnerEpoch
		preserveAllTargetReadiness := preserveTargetReadiness &&
			replicationPolicyTargetTopologyEqual(existing.Targets, targets)
		if existingErr == nil {
			switch {
			case existing.Enabled && !policy.Enabled:
				policy.ProtectionState = ReplicationProtectionStateUnprotected
			case !existing.Enabled && policy.Enabled:
				policy.ProtectionState = ReplicationProtectionStateInitializing
			case policy.Enabled && !preserveAllTargetReadiness:
				policy.ProtectionState = ReplicationProtectionStateInitializing
			default:
				policy.ProtectionState = existing.ProtectionState
			}
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name",
				"description",
				"guest_type",
				"guest_id",
				"source_node_id",
				"active_node_id",
				"owner_epoch",
				"source_mode",
				"failback_mode",
				"failover_mode",
				"cron_expr",
				"crash_recovery",
				"crash_restart_max",
				"pool_health_check",
				"pool_capacity_pct",
				"enabled",
				"protection_state",
				"last_run_at",
				"next_run_at",
				"last_status",
				"last_error",
				"updated_at",
			}),
		}).Create(policy).Error; err != nil {
			return err
		}

		return replaceReplicationPolicyTargets(tx, policy.ID, targets, existing.Targets, preserveTargetReadiness)
	})
}

// updateReplicationPolicy applies an ordinary user-configurable policy edit.
// Ownership, protection lifecycle, and transition checkpoints are controlled
// by their dedicated Raft commands and are deliberately never copied from this
// payload. ExpectedOwnerEpoch makes a policy form assembled before a cutover
// fail closed instead of rolling the new owner's topology back.
func updateReplicationPolicy(db *gorm.DB, payload *ReplicationPolicyPayload) error {
	if payload == nil {
		return fmt.Errorf("replication_policy_payload_required")
	}
	if payload.ExpectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_expected_owner_epoch_required")
	}

	policy := payload.Policy
	if err := validateReplicationPolicy(&policy); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationPolicy
		if err := tx.Preload("Targets").First(&existing, policy.ID).Error; err != nil {
			return err
		}
		if err := requireNoReplicationGuestOperation(tx, existing.GuestType, existing.GuestID); err != nil {
			return err
		}
		if existing.OwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_policy_update_cas_conflict")
		}
		if existing.GuestType != policy.GuestType || existing.GuestID != policy.GuestID {
			return fmt.Errorf("replication_policy_guest_identity_immutable")
		}
		if existing.ProtectionState == ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_deleting")
		}
		if replicationTransitionInProgress(existing.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}

		// A disable/enable request is a lifecycle decision derived against the
		// current row. Never accept a protection state supplied by a stale form.
		protectionState := existing.ProtectionState
		if existing.Enabled && !policy.Enabled {
			protectionState = ReplicationProtectionStateUnprotected
		} else if !existing.Enabled && policy.Enabled {
			protectionState = ReplicationProtectionStateInitializing
		}

		preserveTargetReadiness := existing.Enabled == policy.Enabled &&
			existing.CronExpr == policy.CronExpr
		preserveAllTargetReadiness := preserveTargetReadiness &&
			replicationPolicyTargetTopologyEqual(existing.Targets, payload.Targets)
		if policy.Enabled && !preserveAllTargetReadiness {
			protectionState = ReplicationProtectionStateInitializing
		}

		result := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ?", policy.ID, payload.ExpectedOwnerEpoch).
			Updates(map[string]any{
				"name":              policy.Name,
				"description":       policy.Description,
				"source_node_id":    policy.SourceNodeID,
				"source_mode":       policy.SourceMode,
				"failback_mode":     policy.FailbackMode,
				"failover_mode":     policy.FailoverMode,
				"cron_expr":         policy.CronExpr,
				"crash_recovery":    policy.CrashRecovery,
				"crash_restart_max": policy.CrashRestartMax,
				"pool_health_check": policy.PoolHealthCheck,
				"pool_capacity_pct": policy.PoolCapacityPct,
				"enabled":           policy.Enabled,
				"protection_state":  protectionState,
				"next_run_at":       policy.NextRunAt,
				"updated_at":        time.Now().UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_policy_update_cas_conflict")
		}

		return replaceReplicationPolicyTargets(
			tx,
			policy.ID,
			payload.Targets,
			existing.Targets,
			preserveTargetReadiness,
		)
	})
}

func clearReplicationTargetReadiness(target *ReplicationPolicyTarget) {
	if target == nil {
		return
	}
	target.Ready = false
	target.GenerationID = ""
	target.OwnerEpoch = 0
	target.ManifestHash = ""
	target.RequiredDatasetCount = 0
	target.CompletedDatasetCount = 0
	target.LastVerifiedAt = nil
	target.ReadyUntil = nil
	target.LastError = ""
}

func replicationPolicyTargetTopologyEqual(
	existing []ReplicationPolicyTarget,
	desired []ReplicationPolicyTarget,
) bool {
	if len(existing) != len(desired) {
		return false
	}
	existingWeights := make(map[string]int, len(existing))
	for _, target := range existing {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" {
			return false
		}
		weight := target.Weight
		if weight == 0 {
			weight = 100
		}
		if _, duplicate := existingWeights[nodeID]; duplicate {
			return false
		}
		existingWeights[nodeID] = weight
	}
	seen := make(map[string]struct{}, len(desired))
	for _, target := range desired {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" {
			return false
		}
		if _, duplicate := seen[nodeID]; duplicate {
			return false
		}
		seen[nodeID] = struct{}{}
		weight := target.Weight
		if weight == 0 {
			weight = 100
		}
		if existingWeight, ok := existingWeights[nodeID]; !ok || existingWeight != weight {
			return false
		}
	}
	return true
}

func copyReplicationTargetReadiness(dst *ReplicationPolicyTarget, src ReplicationPolicyTarget) {
	if dst == nil {
		return
	}
	dst.Ready = src.Ready
	dst.GenerationID = src.GenerationID
	dst.OwnerEpoch = src.OwnerEpoch
	dst.ManifestHash = src.ManifestHash
	dst.RequiredDatasetCount = src.RequiredDatasetCount
	dst.CompletedDatasetCount = src.CompletedDatasetCount
	dst.LastVerifiedAt = src.LastVerifiedAt
	dst.ReadyUntil = src.ReadyUntil
	dst.LastError = src.LastError
}

func normalizeReplicationPolicyTarget(target *ReplicationPolicyTarget, policyID uint) error {
	if target == nil {
		return fmt.Errorf("replication_policy_target_required")
	}
	target.ID = 0
	target.PolicyID = policyID
	target.NodeID = strings.TrimSpace(target.NodeID)
	target.GenerationID = strings.TrimSpace(target.GenerationID)
	target.ManifestHash = strings.TrimSpace(target.ManifestHash)
	target.LastError = strings.TrimSpace(target.LastError)
	if target.NodeID == "" {
		return fmt.Errorf("replication_target_node_required")
	}
	if target.Weight == 0 {
		target.Weight = 100
	}
	if target.RequiredDatasetCount < 0 || target.CompletedDatasetCount < 0 ||
		target.CompletedDatasetCount > target.RequiredDatasetCount {
		return fmt.Errorf("invalid_replication_target_dataset_counts")
	}
	return nil
}

func validateReadyReplicationTarget(target *ReplicationPolicyTarget, ownerEpoch uint64) error {
	if target == nil || !target.Ready {
		return nil
	}
	if target.OwnerEpoch != ownerEpoch {
		return fmt.Errorf("replication_target_owner_epoch_mismatch")
	}
	if target.GenerationID == "" || target.ManifestHash == "" {
		return fmt.Errorf("replication_target_manifest_required")
	}
	if target.RequiredDatasetCount <= 0 || target.CompletedDatasetCount != target.RequiredDatasetCount {
		return fmt.Errorf("replication_target_incomplete")
	}
	if target.LastVerifiedAt == nil || target.LastVerifiedAt.IsZero() {
		return fmt.Errorf("replication_target_verification_required")
	}
	if target.ReadyUntil == nil || target.ReadyUntil.IsZero() {
		return fmt.Errorf("replication_target_freshness_required")
	}
	return nil
}

func replaceReplicationPolicyTargets(
	tx *gorm.DB,
	policyID uint,
	targets []ReplicationPolicyTarget,
	existing []ReplicationPolicyTarget,
	preserveUnchanged bool,
) error {
	existingByNode := make(map[string]ReplicationPolicyTarget, len(existing))
	for _, target := range existing {
		existingByNode[strings.TrimSpace(target.NodeID)] = target
	}

	seen := make(map[string]struct{}, len(targets))
	for i := range targets {
		if err := normalizeReplicationPolicyTarget(&targets[i], policyID); err != nil {
			return err
		}
		if _, ok := seen[targets[i].NodeID]; ok {
			return fmt.Errorf("duplicate_replication_target_node")
		}
		seen[targets[i].NodeID] = struct{}{}

		old, unchanged := existingByNode[targets[i].NodeID]
		if preserveUnchanged && unchanged && old.Weight == targets[i].Weight {
			copyReplicationTargetReadiness(&targets[i], old)
		} else if preserveUnchanged {
			// Ordinary policy edits cannot manufacture readiness. New or
			// reconfigured targets always begin unready.
			clearReplicationTargetReadiness(&targets[i])
		}
	}

	if err := tx.Where("policy_id = ?", policyID).Delete(&ReplicationPolicyTarget{}).Error; err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	return tx.Create(&targets).Error
}

func upsertReplicationLease(db *gorm.DB, lease *ReplicationLease) error {
	if lease == nil || lease.PolicyID == 0 {
		return fmt.Errorf("replication_lease_policy_id_required")
	}

	lease.GuestType = strings.TrimSpace(strings.ToLower(lease.GuestType))
	lease.OwnerNodeID = strings.TrimSpace(lease.OwnerNodeID)
	lease.LastReason = strings.TrimSpace(lease.LastReason)
	lease.LastActor = strings.TrimSpace(lease.LastActor)

	if !validReplicationGuestType(lease.GuestType) {
		return fmt.Errorf("invalid_replication_lease_guest_type")
	}
	if lease.OwnerNodeID == "" {
		return fmt.Errorf("replication_lease_owner_required")
	}
	if lease.OwnerEpoch == 0 {
		return fmt.Errorf("replication_lease_owner_epoch_required")
	}
	if lease.ExpiresAt.IsZero() {
		return fmt.Errorf("replication_lease_expiry_required")
	}
	if lease.Version == 0 {
		lease.Version = 1
	}

	var policy ReplicationPolicy
	if err := db.First(&policy, lease.PolicyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// A policy can be deleted after the leader assembled a renewal
			// batch. The safe deterministic result is to discard that entry.
			return nil
		}
		return err
	}
	if !policy.Enabled {
		// Disabled policies are deliberately unprotected and never regain a
		// lease through a delayed renewal batch.
		return nil
	}
	if policy.GuestType != lease.GuestType || policy.GuestID != lease.GuestID {
		return fmt.Errorf("replication_lease_guest_mismatch")
	}
	if err := requireNoReplicationGuestOperation(db, policy.GuestType, policy.GuestID); err != nil {
		return err
	}
	if lease.OwnerEpoch < policy.OwnerEpoch {
		// A delayed batch from the previous owner is harmless and must not
		// roll back otherwise-valid renewals in the same batch.
		return nil
	}
	if lease.OwnerEpoch > policy.OwnerEpoch {
		return fmt.Errorf("replication_lease_future_owner_epoch")
	}
	expectedOwner := strings.TrimSpace(policy.ActiveNodeID)
	if expectedOwner == "" {
		expectedOwner = strings.TrimSpace(policy.SourceNodeID)
	}
	if expectedOwner == "" {
		return fmt.Errorf("replication_policy_owner_required")
	}
	if lease.OwnerNodeID != expectedOwner {
		return fmt.Errorf("replication_lease_owner_mismatch")
	}

	var existing ReplicationLease
	err := db.Where("policy_id = ?", lease.PolicyID).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if existing.OwnerEpoch > lease.OwnerEpoch {
			return nil
		}
		if existing.OwnerEpoch == lease.OwnerEpoch {
			if strings.TrimSpace(existing.OwnerNodeID) != lease.OwnerNodeID {
				return fmt.Errorf("replication_lease_same_epoch_owner_conflict")
			}
			if existing.Version >= lease.Version {
				return nil
			}
		}

		return db.Model(&ReplicationLease{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"guest_type":    lease.GuestType,
			"guest_id":      lease.GuestID,
			"owner_node_id": lease.OwnerNodeID,
			"owner_epoch":   lease.OwnerEpoch,
			"expires_at":    lease.ExpiresAt,
			"version":       lease.Version,
			"last_reason":   lease.LastReason,
			"last_actor":    lease.LastActor,
			"updated_at":    time.Now().UTC(),
		}).Error
	}

	return db.Create(lease).Error
}

func normalizeAndValidateReplicationPolicyTransition(transition *ReplicationPolicyTransition) error {
	if transition == nil {
		return fmt.Errorf("replication_policy_transition_required")
	}

	transition.State = strings.TrimSpace(strings.ToLower(transition.State))
	transition.RunID = strings.TrimSpace(transition.RunID)
	transition.Reason = strings.TrimSpace(transition.Reason)
	transition.SourceNodeID = strings.TrimSpace(transition.SourceNodeID)
	transition.TargetNodeID = strings.TrimSpace(transition.TargetNodeID)
	transition.OriginalSourceNodeID = strings.TrimSpace(transition.OriginalSourceNodeID)
	transition.GenerationID = strings.TrimSpace(transition.GenerationID)
	transition.GenerationManifest = strings.TrimSpace(transition.GenerationManifest)
	transition.Error = strings.TrimSpace(transition.Error)
	if transition.State == "" {
		transition.State = ReplicationTransitionStateNone
	}
	if !validReplicationTransitionState(transition.State) {
		return fmt.Errorf("invalid_replication_transition_state")
	}
	if transition.State != ReplicationTransitionStateNone && transition.RunID == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	if transition.GenerationID == "" {
		if transition.GenerationOwnerEpoch != 0 || transition.GenerationManifest != "" || transition.GenerationRootCount != 0 {
			return fmt.Errorf("replication_transition_generation_evidence_incomplete")
		}
	} else if transition.GenerationOwnerEpoch == 0 || transition.GenerationManifest == "" || transition.GenerationRootCount <= 0 {
		return fmt.Errorf("replication_transition_generation_evidence_incomplete")
	}
	return nil
}

func replicationTransitionOptionsMatch(policy *ReplicationPolicy, transition *ReplicationPolicyTransition) bool {
	if policy == nil || transition == nil {
		return false
	}
	return strings.TrimSpace(policy.TransitionSourceNodeID) == transition.SourceNodeID &&
		strings.TrimSpace(policy.TransitionTargetNodeID) == transition.TargetNodeID &&
		policy.TransitionOwnerEpoch == transition.OwnerEpoch &&
		policy.TransitionAllowUnsafe == transition.AllowUnsafe &&
		policy.TransitionMovePinnedSource == transition.MovePinnedSource &&
		policy.TransitionTriggerValidationRun == transition.TriggerValidationRun &&
		strings.TrimSpace(policy.TransitionOriginalSourceNodeID) == transition.OriginalSourceNodeID
}

func replicationTransitionGenerationEvidenceMatches(policy *ReplicationPolicy, transition *ReplicationPolicyTransition) bool {
	return policy != nil && transition != nil &&
		strings.TrimSpace(policy.TransitionGenerationID) == transition.GenerationID &&
		policy.TransitionGenerationOwnerEpoch == transition.GenerationOwnerEpoch &&
		strings.TrimSpace(policy.TransitionGenerationManifest) == transition.GenerationManifest &&
		policy.TransitionGenerationRootCount == transition.GenerationRootCount
}

func replicationOptionalBoolPreserved(current, next *bool) bool {
	if current == nil {
		return true
	}
	return next != nil && *current == *next
}

func replicationCheckpointPreserved(current, next *time.Time) bool {
	if current == nil {
		return true
	}
	return next != nil && current.UTC().Equal(next.UTC())
}

func replicationTransitionCheckpointsPreserved(policy *ReplicationPolicy, transition *ReplicationPolicyTransition) bool {
	if policy == nil || transition == nil {
		return false
	}
	return replicationCheckpointPreserved(policy.TransitionRequestedAt, transition.RequestedAt) &&
		replicationCheckpointPreserved(policy.TransitionDemotedAt, transition.DemotedAt) &&
		replicationCheckpointPreserved(policy.TransitionCatchupAt, transition.CatchupAt) &&
		replicationCheckpointPreserved(policy.TransitionPromotedAt, transition.PromotedAt) &&
		replicationCheckpointPreserved(policy.TransitionCompletedAt, transition.CompletedAt) &&
		replicationOptionalBoolPreserved(policy.TransitionOriginalRunning, transition.OriginalRunning)
}

func replicationTransitionStateAdvanceAllowed(currentState, nextState string) bool {
	currentState = strings.TrimSpace(strings.ToLower(currentState))
	nextState = strings.TrimSpace(strings.ToLower(nextState))
	if currentState == nextState {
		return replicationTransitionInProgress(currentState)
	}
	switch currentState {
	case ReplicationTransitionStateDemoting:
		return nextState == ReplicationTransitionStateCatchup ||
			nextState == ReplicationTransitionStateFailed
	case ReplicationTransitionStateCatchup:
		return nextState == ReplicationTransitionStateFailed
	case ReplicationTransitionStatePromoting:
		return nextState == ReplicationTransitionStateCompleted ||
			nextState == ReplicationTransitionStateFailed
	case ReplicationTransitionStateRollingBack:
		return nextState == ReplicationTransitionStateFailed
	default:
		return false
	}
}

func persistReplicationPolicyTransition(db *gorm.DB, policyID uint, transition *ReplicationPolicyTransition) error {
	updates := map[string]any{
		"transition_state":                   transition.State,
		"transition_run_id":                  transition.RunID,
		"transition_reason":                  transition.Reason,
		"transition_source_node_id":          transition.SourceNodeID,
		"transition_target_node_id":          transition.TargetNodeID,
		"transition_owner_epoch":             transition.OwnerEpoch,
		"transition_requested_at":            transition.RequestedAt,
		"transition_demoted_at":              transition.DemotedAt,
		"transition_catchup_at":              transition.CatchupAt,
		"transition_promoted_at":             transition.PromotedAt,
		"transition_completed_at":            transition.CompletedAt,
		"transition_error":                   transition.Error,
		"transition_allow_unsafe":            transition.AllowUnsafe,
		"transition_move_pinned_source":      transition.MovePinnedSource,
		"transition_trigger_validation_run":  transition.TriggerValidationRun,
		"transition_original_running":        transition.OriginalRunning,
		"transition_original_source_node_id": transition.OriginalSourceNodeID,
		"transition_generation_id":           transition.GenerationID,
		"transition_generation_owner_epoch":  transition.GenerationOwnerEpoch,
		"transition_generation_manifest":     transition.GenerationManifest,
		"transition_generation_root_count":   transition.GenerationRootCount,
		"updated_at":                         time.Now().UTC(),
	}

	result := db.Model(&ReplicationPolicy{}).
		Where("id = ?", policyID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func upsertReplicationPolicyTransition(db *gorm.DB, policyID uint, transition *ReplicationPolicyTransition) error {
	if policyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if err := normalizeAndValidateReplicationPolicyTransition(transition); err != nil {
		return err
	}

	var current ReplicationPolicy
	if err := db.First(&current, policyID).Error; err != nil {
		return err
	}
	if err := requireNoReplicationGuestOperation(db, current.GuestType, current.GuestID); err != nil {
		return err
	}
	currentRunID := strings.TrimSpace(current.TransitionRunID)
	if currentRunID == "" || currentRunID != transition.RunID {
		if replicationTransitionInProgress(current.TransitionState) {
			return fmt.Errorf("replication_policy_transition_already_running")
		}
		return fmt.Errorf("replication_transition_run_mismatch")
	}
	if !replicationTransitionOptionsMatch(&current, transition) {
		return fmt.Errorf("replication_transition_parameters_mismatch")
	}
	if !replicationTransitionStateAdvanceAllowed(current.TransitionState, transition.State) {
		if !replicationTransitionInProgress(current.TransitionState) &&
			strings.TrimSpace(strings.ToLower(current.TransitionState)) == transition.State {
			// Replaying an already-persisted terminal checkpoint is idempotent;
			// it must not rewrite that checkpoint with late payload data.
			return nil
		}
		return fmt.Errorf("replication_transition_invalid_predecessor_state")
	}
	if !replicationTransitionCheckpointsPreserved(&current, transition) {
		return fmt.Errorf("replication_transition_checkpoint_mismatch")
	}
	if strings.TrimSpace(current.TransitionGenerationID) != "" &&
		!replicationTransitionGenerationEvidenceMatches(&current, transition) {
		return fmt.Errorf("replication_transition_generation_evidence_mismatch")
	}
	if !replicationTransitionInProgress(current.TransitionState) {
		return fmt.Errorf("replication_policy_transition_already_running")
	}
	return persistReplicationPolicyTransition(db, policyID, transition)
}

func beginReplicationPolicyTransition(db *gorm.DB, begin *ReplicationPolicyTransitionBegin) error {
	if begin == nil || begin.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if begin.ExpectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_transition_expected_owner_epoch_required")
	}
	begin.ProtectionState = strings.TrimSpace(strings.ToLower(begin.ProtectionState))
	if begin.ProtectionState == "" {
		begin.ProtectionState = ReplicationProtectionStateSuspended
	}
	if !validReplicationProtectionState(begin.ProtectionState) ||
		begin.ProtectionState == ReplicationProtectionStateUnprotected {
		return fmt.Errorf("invalid_replication_protection_state")
	}

	transition := begin.Transition
	if err := normalizeAndValidateReplicationPolicyTransition(&transition); err != nil {
		return err
	}
	if !replicationTransitionInProgress(transition.State) {
		return fmt.Errorf("replication_transition_begin_state_required")
	}
	if transition.OwnerEpoch != begin.ExpectedOwnerEpoch {
		return fmt.Errorf("replication_transition_owner_epoch_mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, begin.PolicyID).Error; err != nil {
			return err
		}
		if !policy.Enabled {
			return fmt.Errorf("replication_policy_unprotected")
		}
		if err := requireNoReplicationGuestOperation(tx, policy.GuestType, policy.GuestID); err != nil {
			return err
		}
		if policy.OwnerEpoch != begin.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_transition_cas_conflict")
		}
		if policy.ProtectionState == ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_deleting")
		}
		currentRunID := strings.TrimSpace(policy.TransitionRunID)
		if currentRunID == transition.RunID {
			if !replicationTransitionInProgress(policy.TransitionState) {
				return fmt.Errorf("replication_transition_run_already_terminal")
			}
			if strings.TrimSpace(strings.ToLower(policy.TransitionState)) != transition.State ||
				!replicationTransitionOptionsMatch(&policy, &transition) ||
				!replicationTransitionGenerationEvidenceMatches(&policy, &transition) {
				return fmt.Errorf("replication_transition_begin_replay_mismatch")
			}
			return nil
		}
		if replicationTransitionInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_policy_transition_already_running")
		}

		if err := persistReplicationPolicyTransition(tx, begin.PolicyID, &transition); err != nil {
			return err
		}
		result := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ?", begin.PolicyID, begin.ExpectedOwnerEpoch).
			Update("protection_state", begin.ProtectionState)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_transition_cas_conflict")
		}
		return nil
	})
}

func replaceReplicationPolicyTargetsExact(
	tx *gorm.DB,
	policyID uint,
	ownerEpoch uint64,
	activeNodeID string,
	targets []ReplicationPolicyTarget,
) error {
	seen := make(map[string]struct{}, len(targets))
	for i := range targets {
		if err := normalizeReplicationPolicyTarget(&targets[i], policyID); err != nil {
			return err
		}
		if targets[i].NodeID == activeNodeID {
			return fmt.Errorf("replication_active_node_cannot_be_target")
		}
		if _, ok := seen[targets[i].NodeID]; ok {
			return fmt.Errorf("duplicate_replication_target_node")
		}
		seen[targets[i].NodeID] = struct{}{}
		if err := validateReadyReplicationTarget(&targets[i], ownerEpoch); err != nil {
			return err
		}
	}

	if err := tx.Where("policy_id = ?", policyID).Delete(&ReplicationPolicyTarget{}).Error; err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	return tx.Create(&targets).Error
}

func validateReplicationOwnershipTransitionPredecessor(
	policy *ReplicationPolicy,
	payload *ReplicationOwnershipTransitionPayload,
) error {
	if policy == nil || payload == nil {
		return fmt.Errorf("replication_ownership_transition_required")
	}
	if strings.TrimSpace(policy.TransitionRunID) != payload.ExpectedTransitionRunID {
		return fmt.Errorf("replication_transition_cas_conflict")
	}
	if policy.TransitionAllowUnsafe != payload.Transition.AllowUnsafe ||
		policy.TransitionMovePinnedSource != payload.Transition.MovePinnedSource ||
		policy.TransitionTriggerValidationRun != payload.Transition.TriggerValidationRun {
		return fmt.Errorf("replication_transition_parameters_mismatch")
	}
	if strings.TrimSpace(policy.TransitionOriginalSourceNodeID) != payload.Transition.OriginalSourceNodeID ||
		!replicationTransitionCheckpointsPreserved(policy, &payload.Transition) ||
		!replicationTransitionGenerationEvidenceMatches(policy, &payload.Transition) {
		return fmt.Errorf("replication_transition_checkpoint_mismatch")
	}

	currentState := strings.TrimSpace(strings.ToLower(policy.TransitionState))
	currentSource := strings.TrimSpace(policy.TransitionSourceNodeID)
	currentTarget := strings.TrimSpace(policy.TransitionTargetNodeID)
	switch payload.Transition.State {
	case ReplicationTransitionStatePromoting:
		if currentState != ReplicationTransitionStateDemoting &&
			currentState != ReplicationTransitionStateCatchup {
			return fmt.Errorf("replication_transition_invalid_predecessor_state")
		}
		if currentSource != payload.ExpectedActiveNodeID ||
			currentTarget != payload.ActiveNodeID ||
			payload.Transition.SourceNodeID != payload.ExpectedActiveNodeID ||
			payload.Transition.TargetNodeID != payload.ActiveNodeID ||
			policy.TransitionOwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_transition_predecessor_mismatch")
		}
	case ReplicationTransitionStateCompleted:
		if currentState != ReplicationTransitionStateDemoting ||
			strings.TrimSpace(policy.TransitionReason) != "manual_migration_ownership" ||
			strings.TrimSpace(payload.Transition.Reason) != "manual_migration_ownership" ||
			payload.Transition.CompletedAt == nil || payload.Transition.PromotedAt == nil {
			return fmt.Errorf("replication_transition_invalid_predecessor_state")
		}
		if currentSource != payload.ExpectedActiveNodeID ||
			currentTarget != payload.ActiveNodeID ||
			payload.Transition.SourceNodeID != payload.ExpectedActiveNodeID ||
			payload.Transition.TargetNodeID != payload.ActiveNodeID ||
			policy.TransitionOwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_transition_predecessor_mismatch")
		}
	case ReplicationTransitionStateRollingBack:
		if currentState != ReplicationTransitionStatePromoting {
			return fmt.Errorf("replication_transition_invalid_predecessor_state")
		}
		if currentTarget != payload.ExpectedActiveNodeID ||
			currentSource != payload.ActiveNodeID ||
			payload.Transition.SourceNodeID != payload.ExpectedActiveNodeID ||
			payload.Transition.TargetNodeID != payload.ActiveNodeID ||
			policy.TransitionOwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_transition_predecessor_mismatch")
		}
	default:
		return fmt.Errorf("replication_ownership_transition_state_required")
	}
	return nil
}

func applyReplicationOwnershipTransition(db *gorm.DB, payload *ReplicationOwnershipTransitionPayload) error {
	if payload == nil {
		return fmt.Errorf("replication_ownership_transition_required")
	}
	payload.ExpectedActiveNodeID = strings.TrimSpace(payload.ExpectedActiveNodeID)
	payload.ExpectedTransitionRunID = strings.TrimSpace(payload.ExpectedTransitionRunID)
	payload.ActiveNodeID = strings.TrimSpace(payload.ActiveNodeID)
	payload.ProtectionState = strings.TrimSpace(strings.ToLower(payload.ProtectionState))
	if payload.SourceNodeID != nil {
		value := strings.TrimSpace(*payload.SourceNodeID)
		payload.SourceNodeID = &value
	}

	if payload.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if payload.ExpectedActiveNodeID == "" || payload.ActiveNodeID == "" {
		return fmt.Errorf("replication_ownership_node_required")
	}
	if payload.ExpectedOwnerEpoch == 0 || payload.OwnerEpoch == 0 {
		return fmt.Errorf("replication_ownership_epoch_required")
	}
	if payload.ExpectedTransitionRunID == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	if payload.ExpectedOwnerEpoch == ^uint64(0) || payload.OwnerEpoch != payload.ExpectedOwnerEpoch+1 {
		return fmt.Errorf("replication_ownership_epoch_must_increment")
	}
	if payload.SourceNodeID != nil && *payload.SourceNodeID == "" {
		return fmt.Errorf("replication_source_node_required")
	}
	if payload.ProtectionState != "" && !validReplicationProtectionState(payload.ProtectionState) {
		return fmt.Errorf("invalid_replication_protection_state")
	}
	if payload.ProtectionState == ReplicationProtectionStateUnprotected {
		return fmt.Errorf("replication_transition_cannot_unprotect_policy")
	}
	if payload.PreviousLeaseExpiresAtOrBefore != nil {
		if payload.PreviousLeaseExpiresAtOrBefore.IsZero() {
			return fmt.Errorf("replication_previous_lease_cutoff_required")
		}
		cutoff := payload.PreviousLeaseExpiresAtOrBefore.UTC()
		payload.PreviousLeaseExpiresAtOrBefore = &cutoff
	}

	if err := normalizeAndValidateReplicationPolicyTransition(&payload.Transition); err != nil {
		return err
	}
	if payload.Transition.RunID != payload.ExpectedTransitionRunID {
		return fmt.Errorf("replication_transition_run_id_mismatch")
	}
	if payload.Transition.OwnerEpoch != payload.OwnerEpoch {
		return fmt.Errorf("replication_transition_owner_epoch_mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, payload.PolicyID).Error; err != nil {
			return err
		}
		if !policy.Enabled {
			return fmt.Errorf("replication_policy_unprotected")
		}
		if err := requireNoReplicationGuestOperation(tx, policy.GuestType, policy.GuestID); err != nil {
			return err
		}
		if strings.TrimSpace(policy.ActiveNodeID) != payload.ExpectedActiveNodeID ||
			policy.OwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_ownership_cas_conflict")
		}
		// Older/retried callers may omit already-persisted checkpoints. Inherit
		// them inside the same transaction, while predecessor validation below
		// still rejects any explicitly conflicting value.
		if payload.Transition.RequestedAt == nil {
			payload.Transition.RequestedAt = policy.TransitionRequestedAt
		}
		if payload.Transition.DemotedAt == nil {
			payload.Transition.DemotedAt = policy.TransitionDemotedAt
		}
		if payload.Transition.CatchupAt == nil {
			payload.Transition.CatchupAt = policy.TransitionCatchupAt
		}
		if payload.Transition.OriginalRunning == nil {
			payload.Transition.OriginalRunning = policy.TransitionOriginalRunning
		}
		if payload.Transition.OriginalSourceNodeID == "" {
			payload.Transition.OriginalSourceNodeID = strings.TrimSpace(policy.TransitionOriginalSourceNodeID)
		}
		if payload.Transition.GenerationID == "" && strings.TrimSpace(policy.TransitionGenerationID) != "" {
			payload.Transition.GenerationID = strings.TrimSpace(policy.TransitionGenerationID)
			payload.Transition.GenerationOwnerEpoch = policy.TransitionGenerationOwnerEpoch
			payload.Transition.GenerationManifest = strings.TrimSpace(policy.TransitionGenerationManifest)
			payload.Transition.GenerationRootCount = policy.TransitionGenerationRootCount
		}
		if err := validateReplicationOwnershipTransitionPredecessor(&policy, payload); err != nil {
			return err
		}

		if payload.PreviousLeaseExpiresAtOrBefore != nil {
			var previousLease ReplicationLease
			err := tx.Where("policy_id = ?", payload.PolicyID).First(&previousLease).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				leaseOwner := strings.TrimSpace(previousLease.OwnerNodeID)
				if previousLease.OwnerEpoch > payload.ExpectedOwnerEpoch ||
					(previousLease.OwnerEpoch == payload.ExpectedOwnerEpoch && leaseOwner != payload.ExpectedActiveNodeID) {
					return fmt.Errorf("replication_previous_owner_lease_mismatch")
				}
				if previousLease.OwnerEpoch == payload.ExpectedOwnerEpoch &&
					leaseOwner == payload.ExpectedActiveNodeID &&
					previousLease.ExpiresAt.After(*payload.PreviousLeaseExpiresAtOrBefore) {
					return fmt.Errorf("replication_previous_owner_lease_not_expired")
				}
			}
		}

		lease := payload.Lease
		if lease.PolicyID == 0 {
			lease.PolicyID = payload.PolicyID
		}
		if lease.PolicyID != payload.PolicyID ||
			strings.TrimSpace(strings.ToLower(lease.GuestType)) != policy.GuestType ||
			lease.GuestID != policy.GuestID ||
			strings.TrimSpace(lease.OwnerNodeID) != payload.ActiveNodeID ||
			lease.OwnerEpoch != payload.OwnerEpoch {
			return fmt.Errorf("replication_transition_lease_mismatch")
		}

		updates := map[string]any{
			"active_node_id": payload.ActiveNodeID,
			"owner_epoch":    payload.OwnerEpoch,
			"updated_at":     time.Now().UTC(),
		}
		if payload.SourceNodeID != nil {
			updates["source_node_id"] = *payload.SourceNodeID
		}
		if payload.ProtectionState != "" {
			updates["protection_state"] = payload.ProtectionState
		}

		result := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND active_node_id = ? AND owner_epoch = ?", payload.PolicyID, payload.ExpectedActiveNodeID, payload.ExpectedOwnerEpoch).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_ownership_cas_conflict")
		}

		if payload.ReplaceTargets {
			if err := replaceReplicationPolicyTargetsExact(
				tx,
				payload.PolicyID,
				payload.OwnerEpoch,
				payload.ActiveNodeID,
				payload.Targets,
			); err != nil {
				return err
			}
		}
		if err := upsertReplicationLease(tx, &lease); err != nil {
			return err
		}
		if err := persistReplicationPolicyTransition(tx, payload.PolicyID, &payload.Transition); err != nil {
			return err
		}
		return nil
	})
}

func reassignDisabledReplicationPolicyOwner(
	db *gorm.DB,
	payload *ReplicationDisabledOwnerReassignment,
) error {
	if payload == nil || payload.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	payload.ExpectedActiveNodeID = strings.TrimSpace(payload.ExpectedActiveNodeID)
	payload.ActiveNodeID = strings.TrimSpace(payload.ActiveNodeID)
	payload.SourceNodeID = strings.TrimSpace(payload.SourceNodeID)
	payload.RunID = strings.TrimSpace(payload.RunID)
	payload.OperationToken = strings.TrimSpace(payload.OperationToken)
	if payload.ExpectedActiveNodeID == "" || payload.ActiveNodeID == "" || payload.SourceNodeID == "" {
		return fmt.Errorf("replication_ownership_node_required")
	}
	if payload.ExpectedOwnerEpoch == 0 || payload.ExpectedOwnerEpoch == ^uint64(0) ||
		payload.OwnerEpoch != payload.ExpectedOwnerEpoch+1 {
		return fmt.Errorf("replication_ownership_epoch_must_increment")
	}
	if payload.RunID == "" || payload.OperationToken == "" || payload.OccurredAt.IsZero() {
		return fmt.Errorf("replication_disabled_owner_reassignment_audit_required")
	}
	payload.OccurredAt = payload.OccurredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, payload.PolicyID).Error; err != nil {
			return err
		}
		if replicationguard.GuestOperationSchemaReady(tx) {
			var operation ReplicationGuestOperation
			if err := tx.Where("guest_type = ? AND guest_id = ?", policy.GuestType, policy.GuestID).
				First(&operation).Error; err != nil {
				return err
			}
			if operation.Operation != ReplicationGuestOperationMigration ||
				operation.State != ReplicationGuestOperationCutover ||
				strings.TrimSpace(operation.Token) != payload.OperationToken ||
				strings.TrimSpace(operation.TargetNodeID) != payload.ActiveNodeID {
				return fmt.Errorf("replication_guest_operation_cutover_mismatch")
			}
		}
		currentOwner := strings.TrimSpace(policy.ActiveNodeID)
		if currentOwner == "" {
			currentOwner = strings.TrimSpace(policy.SourceNodeID)
		}
		if !policy.Enabled && currentOwner == payload.ActiveNodeID &&
			policy.OwnerEpoch == payload.OwnerEpoch &&
			strings.TrimSpace(policy.TransitionRunID) == payload.RunID {
			return nil
		}
		if policy.Enabled || policy.ProtectionState == ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_must_be_disabled_for_owner_reassignment")
		}
		if replicationTransitionInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}
		if currentOwner != payload.ExpectedActiveNodeID || policy.OwnerEpoch != payload.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_ownership_cas_conflict")
		}

		updates := map[string]any{
			"active_node_id":                     payload.ActiveNodeID,
			"source_node_id":                     payload.SourceNodeID,
			"owner_epoch":                        payload.OwnerEpoch,
			"protection_state":                   ReplicationProtectionStateUnprotected,
			"next_run_at":                        nil,
			"transition_state":                   ReplicationTransitionStateCompleted,
			"transition_run_id":                  payload.RunID,
			"transition_reason":                  "manual_migration_ownership",
			"transition_source_node_id":          payload.ExpectedActiveNodeID,
			"transition_target_node_id":          payload.ActiveNodeID,
			"transition_owner_epoch":             payload.OwnerEpoch,
			"transition_requested_at":            payload.OccurredAt,
			"transition_demoted_at":              payload.OccurredAt,
			"transition_catchup_at":              nil,
			"transition_promoted_at":             payload.OccurredAt,
			"transition_completed_at":            payload.OccurredAt,
			"transition_error":                   "",
			"transition_allow_unsafe":            false,
			"transition_move_pinned_source":      true,
			"transition_trigger_validation_run":  false,
			"transition_original_running":        nil,
			"transition_original_source_node_id": strings.TrimSpace(policy.SourceNodeID),
			"transition_generation_id":           "",
			"transition_generation_owner_epoch":  uint64(0),
			"transition_generation_manifest":     "",
			"transition_generation_root_count":   0,
			"updated_at":                         payload.OccurredAt,
		}
		result := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ? AND enabled = ?", payload.PolicyID, payload.ExpectedOwnerEpoch, false).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_ownership_cas_conflict")
		}
		if err := replaceReplicationPolicyTargetsExact(
			tx,
			payload.PolicyID,
			payload.OwnerEpoch,
			payload.ActiveNodeID,
			payload.Targets,
		); err != nil {
			return err
		}
		return tx.Where("policy_id = ?", payload.PolicyID).Delete(&ReplicationLease{}).Error
	})
}

func replicationTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func replicationReadinessAssessmentEqual(
	target *ReplicationPolicyTarget,
	update *ReplicationTargetReadinessUpdate,
) bool {
	if target == nil || update == nil {
		return false
	}
	return target.Ready == update.Ready &&
		strings.TrimSpace(target.GenerationID) == update.GenerationID &&
		target.OwnerEpoch == update.ExpectedOwnerEpoch &&
		strings.TrimSpace(target.ManifestHash) == update.ManifestHash &&
		target.RequiredDatasetCount == update.RequiredDatasetCount &&
		target.CompletedDatasetCount == update.CompletedDatasetCount &&
		replicationTimesEqual(target.LastVerifiedAt, update.LastVerifiedAt) &&
		replicationTimesEqual(target.ReadyUntil, update.ReadyUntil) &&
		strings.TrimSpace(target.LastError) == update.LastError
}

func updateReplicationTargetReadiness(db *gorm.DB, update *ReplicationTargetReadinessUpdate) error {
	if update == nil {
		return fmt.Errorf("replication_target_readiness_required")
	}
	update.NodeID = strings.TrimSpace(update.NodeID)
	update.GenerationID = strings.TrimSpace(update.GenerationID)
	update.ManifestHash = strings.TrimSpace(update.ManifestHash)
	update.LastError = strings.TrimSpace(update.LastError)
	update.TransitionRunID = strings.TrimSpace(update.TransitionRunID)
	if update.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if update.NodeID == "" {
		return fmt.Errorf("replication_target_node_required")
	}
	if update.ExpectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_target_owner_epoch_required")
	}
	if update.EvaluatedAt.IsZero() {
		return fmt.Errorf("replication_target_readiness_evaluated_at_required")
	}
	if update.RequiredDatasetCount < 0 || update.CompletedDatasetCount < 0 ||
		update.CompletedDatasetCount > update.RequiredDatasetCount {
		return fmt.Errorf("invalid_replication_target_dataset_counts")
	}
	if update.Ready {
		target := ReplicationPolicyTarget{
			Ready:                 true,
			GenerationID:          update.GenerationID,
			OwnerEpoch:            update.ExpectedOwnerEpoch,
			ManifestHash:          update.ManifestHash,
			RequiredDatasetCount:  update.RequiredDatasetCount,
			CompletedDatasetCount: update.CompletedDatasetCount,
			LastVerifiedAt:        update.LastVerifiedAt,
			ReadyUntil:            update.ReadyUntil,
		}
		if err := validateReadyReplicationTarget(&target, update.ExpectedOwnerEpoch); err != nil {
			return err
		}
		if !update.ReadyUntil.After(*update.LastVerifiedAt) {
			return fmt.Errorf("replication_target_invalid_freshness_window")
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, update.PolicyID).Error; err != nil {
			return err
		}
		if !policy.Enabled {
			return fmt.Errorf("replication_policy_unprotected")
		}
		if policy.OwnerEpoch != update.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_target_readiness_cas_conflict")
		}
		if policy.ProtectionState == ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_deleting")
		}
		inTransition := replicationTransitionInProgress(policy.TransitionState)
		if inTransition {
			if update.TransitionRunID == "" ||
				strings.TrimSpace(policy.TransitionRunID) != update.TransitionRunID ||
				strings.TrimSpace(policy.TransitionTargetNodeID) != update.NodeID {
				return fmt.Errorf("replication_policy_transition_in_progress")
			}
		} else if update.TransitionRunID != "" {
			return fmt.Errorf("replication_transition_run_mismatch")
		}
		var existingTarget ReplicationPolicyTarget
		if err := tx.Where("policy_id = ? AND node_id = ?", update.PolicyID, update.NodeID).
			First(&existingTarget).Error; err != nil {
			return err
		}
		if existingTarget.OwnerEpoch > update.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_target_readiness_stale")
		}
		if existingTarget.OwnerEpoch == update.ExpectedOwnerEpoch {
			existingEvaluation := existingTarget.UpdatedAt.UTC()
			incomingEvaluation := update.EvaluatedAt.UTC()
			if incomingEvaluation.Before(existingEvaluation) ||
				(incomingEvaluation.Equal(existingEvaluation) &&
					!replicationReadinessAssessmentEqual(&existingTarget, update)) {
				return fmt.Errorf("replication_target_readiness_stale")
			}
		}

		result := tx.Model(&ReplicationPolicyTarget{}).
			Where("policy_id = ? AND node_id = ?", update.PolicyID, update.NodeID).
			Updates(map[string]any{
				"ready":                   update.Ready,
				"generation_id":           update.GenerationID,
				"owner_epoch":             update.ExpectedOwnerEpoch,
				"manifest_hash":           update.ManifestHash,
				"required_dataset_count":  update.RequiredDatasetCount,
				"completed_dataset_count": update.CompletedDatasetCount,
				"last_verified_at":        update.LastVerifiedAt,
				"ready_until":             update.ReadyUntil,
				"last_error":              update.LastError,
				"updated_at":              update.EvaluatedAt.UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if inTransition {
			// The transition lock owns ProtectionState=suspended. Exact-run
			// readiness is only a generation checkpoint and must not re-arm the
			// policy before ownership is resolved.
			return nil
		}

		var targets []ReplicationPolicyTarget
		if err := tx.Where("policy_id = ?", update.PolicyID).Find(&targets).Error; err != nil {
			return err
		}
		protectionState := replicationProtectionStateFromTargets(
			targets,
			update.ExpectedOwnerEpoch,
			update.EvaluatedAt,
		)
		result = tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ?", update.PolicyID, update.ExpectedOwnerEpoch).
			Updates(map[string]any{
				"protection_state": protectionState,
				"updated_at":       update.EvaluatedAt.UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_target_readiness_cas_conflict")
		}
		return nil
	})
}

func replicationProtectionStateFromTargets(
	targets []ReplicationPolicyTarget,
	ownerEpoch uint64,
	evaluatedAt time.Time,
) string {
	allReady := len(targets) > 0
	anyVerified := false
	for _, target := range targets {
		if target.LastVerifiedAt != nil && !target.LastVerifiedAt.IsZero() {
			anyVerified = true
		}
		completeAndFresh := target.Ready &&
			target.OwnerEpoch == ownerEpoch &&
			strings.TrimSpace(target.GenerationID) != "" &&
			strings.TrimSpace(target.ManifestHash) != "" &&
			target.RequiredDatasetCount > 0 &&
			target.CompletedDatasetCount == target.RequiredDatasetCount &&
			target.LastVerifiedAt != nil && !target.LastVerifiedAt.IsZero() &&
			target.ReadyUntil != nil && target.ReadyUntil.After(evaluatedAt)
		if !completeAndFresh {
			allReady = false
		}
	}
	if allReady {
		return ReplicationProtectionStateArmed
	}
	if anyVerified {
		return ReplicationProtectionStateDegraded
	}
	return ReplicationProtectionStateInitializing
}

func updateReplicationPolicyProtectionState(
	db *gorm.DB,
	update *ReplicationPolicyProtectionStateUpdate,
) error {
	if update == nil || update.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if update.ExpectedOwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_required")
	}
	update.State = strings.TrimSpace(strings.ToLower(update.State))
	if !validReplicationProtectionState(update.State) {
		return fmt.Errorf("invalid_replication_protection_state")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, update.PolicyID).Error; err != nil {
			return err
		}
		if policy.OwnerEpoch != update.ExpectedOwnerEpoch {
			return fmt.Errorf("replication_policy_protection_state_cas_conflict")
		}
		if policy.ProtectionState == ReplicationProtectionStateDeleting &&
			update.State != ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_deleting")
		}
		if policy.Enabled && update.State == ReplicationProtectionStateUnprotected {
			return fmt.Errorf("enabled_replication_policy_cannot_be_unprotected")
		}
		if !policy.Enabled && update.State != ReplicationProtectionStateUnprotected &&
			update.State != ReplicationProtectionStateDeleting {
			return fmt.Errorf("disabled_replication_policy_must_be_unprotected")
		}
		if update.State == ReplicationProtectionStateDeleting &&
			replicationTransitionInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}
		if update.State == ReplicationProtectionStateDeleting {
			if err := requireNoReplicationGuestOperation(tx, policy.GuestType, policy.GuestID); err != nil {
				return err
			}
		}

		result := tx.Model(&ReplicationPolicy{}).
			Where("id = ? AND owner_epoch = ?", update.PolicyID, update.ExpectedOwnerEpoch).
			Update("protection_state", update.State)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("replication_policy_protection_state_cas_conflict")
		}
		return nil
	})
}

func upsertClusterSSHIdentity(db *gorm.DB, identity *ClusterSSHIdentity) error {
	if identity == nil {
		return fmt.Errorf("cluster_ssh_identity_required")
	}

	identity.NodeUUID = strings.TrimSpace(identity.NodeUUID)
	identity.SSHUser = strings.TrimSpace(identity.SSHUser)
	identity.SSHHost = strings.TrimSpace(identity.SSHHost)
	identity.PublicKey = strings.TrimSpace(identity.PublicKey)
	if identity.SSHUser == "" {
		identity.SSHUser = "root"
	}
	if identity.SSHPort == 0 {
		identity.SSHPort = 8183
	}

	if identity.NodeUUID == "" {
		return fmt.Errorf("cluster_ssh_identity_node_required")
	}
	if identity.SSHHost == "" {
		return fmt.Errorf("cluster_ssh_identity_host_required")
	}
	if identity.PublicKey == "" {
		return fmt.Errorf("cluster_ssh_identity_pubkey_required")
	}

	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"ssh_user",
			"ssh_host",
			"ssh_port",
			"public_key",
			"updated_at",
		}),
	}).Create(identity).Error
}

func UpsertReplicationPolicyTxn(db *gorm.DB, policy *ReplicationPolicy, targets []ReplicationPolicyTarget) error {
	if policy == nil || policy.ID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	var count int64
	if err := db.Model(&ReplicationPolicy{}).Where("id = ?", policy.ID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return upsertReplicationPolicy(db, policy, targets)
	}
	return updateReplicationPolicy(db, &ReplicationPolicyPayload{
		Policy:             *policy,
		Targets:            targets,
		ExpectedOwnerEpoch: policy.OwnerEpoch,
	})
}

func UpdateReplicationPolicyTxn(db *gorm.DB, payload *ReplicationPolicyPayload) error {
	return updateReplicationPolicy(db, payload)
}

func DeleteReplicationPolicyTxn(db *gorm.DB, policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var policy ReplicationPolicy
		if err := tx.First(&policy, policyID).Error; err != nil {
			return err
		}
		if replicationTransitionInProgress(policy.TransitionState) {
			return fmt.Errorf("replication_policy_transition_in_progress")
		}
		if err := requireNoReplicationGuestOperation(tx, policy.GuestType, policy.GuestID); err != nil {
			return err
		}
		if policy.ProtectionState != ReplicationProtectionStateDeleting {
			return fmt.Errorf("replication_policy_not_deleting")
		}
		if err := tx.Where("policy_id = ?", policyID).Delete(&ReplicationPolicyTarget{}).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", policyID).Delete(&ReplicationLease{}).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", policyID).Delete(&ReplicationEvent{}).Error; err != nil {
			return err
		}
		return tx.Delete(&ReplicationPolicy{}, policyID).Error
	})
}

func UpsertReplicationLeaseTxn(db *gorm.DB, lease *ReplicationLease) error {
	return upsertReplicationLease(db, lease)
}

func UpsertReplicationPolicyTransitionTxn(
	db *gorm.DB,
	policyID uint,
	transition *ReplicationPolicyTransition,
) error {
	return upsertReplicationPolicyTransition(db, policyID, transition)
}

func BeginReplicationPolicyTransitionTxn(
	db *gorm.DB,
	begin *ReplicationPolicyTransitionBegin,
) error {
	return beginReplicationPolicyTransition(db, begin)
}

func ApplyReplicationOwnershipTransitionTxn(
	db *gorm.DB,
	payload *ReplicationOwnershipTransitionPayload,
) error {
	return applyReplicationOwnershipTransition(db, payload)
}

func ReassignDisabledReplicationPolicyOwnerTxn(
	db *gorm.DB,
	payload *ReplicationDisabledOwnerReassignment,
) error {
	return reassignDisabledReplicationPolicyOwner(db, payload)
}

func AcquireReplicationGuestOperationTxn(
	db *gorm.DB,
	payload *ReplicationGuestOperationAcquire,
) error {
	return acquireReplicationGuestOperation(db, payload)
}

func SealReplicationGuestOperationTxn(
	db *gorm.DB,
	payload *ReplicationGuestOperationTransition,
) error {
	return sealReplicationGuestOperation(db, payload)
}

func AbortReplicationGuestOperationTxn(
	db *gorm.DB,
	payload *ReplicationGuestOperationTransition,
) error {
	return abortReplicationGuestOperation(db, payload)
}

func CompleteReplicationGuestOperationTxn(
	db *gorm.DB,
	payload *ReplicationGuestOperationTransition,
) error {
	return completeReplicationGuestOperation(db, payload)
}

func UpdateReplicationTargetReadinessTxn(
	db *gorm.DB,
	update *ReplicationTargetReadinessUpdate,
) error {
	return updateReplicationTargetReadiness(db, update)
}

func UpdateReplicationPolicyProtectionStateTxn(
	db *gorm.DB,
	update *ReplicationPolicyProtectionStateUpdate,
) error {
	return updateReplicationPolicyProtectionState(db, update)
}

func UpsertClusterSSHIdentityTxn(db *gorm.DB, identity *ClusterSSHIdentity) error {
	return upsertClusterSSHIdentity(db, identity)
}
