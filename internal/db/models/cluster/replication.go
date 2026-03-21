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

	ReplicationTransitionStateNone      = "none"
	ReplicationTransitionStateDemoting  = "demoting"
	ReplicationTransitionStateCatchup   = "catchup"
	ReplicationTransitionStatePromoting = "promoting"
	ReplicationTransitionStateCompleted = "completed"
	ReplicationTransitionStateFailed    = "failed"
)

type ReplicationPolicy struct {
	ID                     uint                      `gorm:"primaryKey" json:"id"`
	Name                   string                    `gorm:"not null" json:"name"`
	Description            string                    `gorm:"type:text" json:"description"`
	GuestType              string                    `gorm:"index:idx_replication_policy_guest,priority:1;not null" json:"guestType"`
	GuestID                uint                      `gorm:"index:idx_replication_policy_guest,priority:2;not null" json:"guestId"`
	SourceNodeID           string                    `gorm:"index" json:"sourceNodeId"`
	ActiveNodeID           string                    `gorm:"index" json:"activeNodeId"`
	OwnerEpoch             uint64                    `gorm:"not null;default:1" json:"ownerEpoch"`
	SourceMode             string                    `gorm:"not null;default:follow_active" json:"sourceMode"`
	FailbackMode           string                    `gorm:"not null;default:manual" json:"failbackMode"`
	FailoverMode           string                    `gorm:"not null;default:manual" json:"failoverMode"`
	CronExpr               string                    `gorm:"not null" json:"cronExpr"`
	Enabled                bool                      `gorm:"default:true;index" json:"enabled"`
	LastRunAt              *time.Time                `json:"lastRunAt"`
	NextRunAt              *time.Time                `gorm:"index" json:"nextRunAt"`
	LastStatus             string                    `gorm:"index" json:"lastStatus"`
	LastError              string                    `gorm:"type:text" json:"lastError"`
	TransitionState        string                    `gorm:"not null;default:none;index" json:"transitionState"`
	TransitionRunID        string                    `gorm:"index" json:"transitionRunId"`
	TransitionReason       string                    `json:"transitionReason"`
	TransitionSourceNodeID string                    `gorm:"index" json:"transitionSourceNodeId"`
	TransitionTargetNodeID string                    `gorm:"index" json:"transitionTargetNodeId"`
	TransitionOwnerEpoch   uint64                    `json:"transitionOwnerEpoch"`
	TransitionRequestedAt  *time.Time                `gorm:"index" json:"transitionRequestedAt"`
	TransitionDemotedAt    *time.Time                `json:"transitionDemotedAt"`
	TransitionCatchupAt    *time.Time                `json:"transitionCatchupAt"`
	TransitionPromotedAt   *time.Time                `json:"transitionPromotedAt"`
	TransitionCompletedAt  *time.Time                `gorm:"index" json:"transitionCompletedAt"`
	TransitionError        string                    `gorm:"type:text" json:"transitionError"`
	HAEligible             bool                      `gorm:"-" json:"haEligible"`
	HADegraded             bool                      `gorm:"-" json:"haDegraded"`
	HAReasons              []string                  `gorm:"-" json:"haReasons"`
	Targets                []ReplicationPolicyTarget `json:"targets,omitempty" gorm:"foreignKey:PolicyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAt              time.Time                 `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt              time.Time                 `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ReplicationPolicyTransition struct {
	State        string     `json:"state"`
	RunID        string     `json:"runId"`
	Reason       string     `json:"reason"`
	SourceNodeID string     `json:"sourceNodeId"`
	TargetNodeID string     `json:"targetNodeId"`
	OwnerEpoch   uint64     `json:"ownerEpoch"`
	RequestedAt  *time.Time `json:"requestedAt"`
	DemotedAt    *time.Time `json:"demotedAt"`
	CatchupAt    *time.Time `json:"catchupAt"`
	PromotedAt   *time.Time `json:"promotedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	Error        string     `json:"error"`
}

type ReplicationPolicyTarget struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	PolicyID  uint      `gorm:"index;not null" json:"policyId"`
	NodeID    string    `gorm:"index;not null" json:"nodeId"`
	Weight    int       `gorm:"not null;default:100" json:"weight"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
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

type ReplicationEvent struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	PolicyID     *uint      `gorm:"index" json:"policyId"`
	EventType    string     `gorm:"index;not null" json:"eventType"`
	Status       string     `gorm:"index;not null" json:"status"`
	Message      string     `json:"message"`
	Error        string     `gorm:"type:text" json:"error"`
	Output       string     `gorm:"type:text" json:"output"`
	SourceNodeID string     `gorm:"index" json:"sourceNodeId"`
	TargetNodeID string     `gorm:"index" json:"targetNodeId"`
	GuestType    string     `gorm:"index" json:"guestType"`
	GuestID      uint       `gorm:"index" json:"guestId"`
	StartedAt    time.Time  `gorm:"index" json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ReplicationReceipt struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	PolicyID           uint       `gorm:"not null;index:idx_replication_receipt_policy_target,unique;index" json:"policyId"`
	GuestType          string     `gorm:"index;not null" json:"guestType"`
	GuestID            uint       `gorm:"index;not null" json:"guestId"`
	SourceNodeID       string     `gorm:"index;not null" json:"sourceNodeId"`
	TargetNodeID       string     `gorm:"not null;index:idx_replication_receipt_policy_target,unique;index" json:"targetNodeId"`
	Status             string     `gorm:"index;not null" json:"status"`
	Message            string     `json:"message"`
	Error              string     `gorm:"type:text" json:"error"`
	LastAttemptAt      time.Time  `gorm:"index;not null" json:"lastAttemptAt"`
	LastSuccessAt      *time.Time `gorm:"index" json:"lastSuccessAt"`
	LastSourceDataset  string     `json:"lastSourceDataset"`
	LastTargetDataset  string     `json:"lastTargetDataset"`
	FreshnessWindowSec *int64     `gorm:"-" json:"freshnessWindowSeconds,omitempty"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
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
	Policy  ReplicationPolicy         `json:"policy"`
	Targets []ReplicationPolicyTarget `json:"targets"`
}

func validReplicationGuestType(v string) bool {
	return v == ReplicationGuestTypeVM || v == ReplicationGuestTypeJail
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
		ReplicationTransitionStateCompleted,
		ReplicationTransitionStateFailed:
		return true
	default:
		return false
	}
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
	p.TransitionState = strings.TrimSpace(strings.ToLower(p.TransitionState))
	p.TransitionRunID = strings.TrimSpace(p.TransitionRunID)
	p.TransitionReason = strings.TrimSpace(p.TransitionReason)
	p.TransitionSourceNodeID = strings.TrimSpace(p.TransitionSourceNodeID)
	p.TransitionTargetNodeID = strings.TrimSpace(p.TransitionTargetNodeID)
	p.TransitionError = strings.TrimSpace(p.TransitionError)
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

func upsertReplicationPolicy(db *gorm.DB, policy *ReplicationPolicy, targets []ReplicationPolicyTarget) error {
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
	if policy.OwnerEpoch == 0 {
		return fmt.Errorf("replication_policy_owner_epoch_required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
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
				"enabled",
				"last_run_at",
				"next_run_at",
				"last_status",
				"last_error",
				"updated_at",
			}),
		}).Create(policy).Error; err != nil {
			return err
		}

		if err := tx.Where("policy_id = ?", policy.ID).Delete(&ReplicationPolicyTarget{}).Error; err != nil {
			return err
		}

		for i := range targets {
			targets[i].ID = 0
			targets[i].PolicyID = policy.ID
			targets[i].NodeID = strings.TrimSpace(targets[i].NodeID)
		}

		if len(targets) > 0 {
			if err := tx.Create(&targets).Error; err != nil {
				return err
			}
		}

		return nil
	})
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

	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "policy_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"guest_type",
			"guest_id",
			"owner_node_id",
			"owner_epoch",
			"expires_at",
			"version",
			"last_reason",
			"last_actor",
			"updated_at",
		}),
	}).Create(lease).Error
}

func upsertReplicationPolicyTransition(db *gorm.DB, policyID uint, transition *ReplicationPolicyTransition) error {
	if policyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if transition == nil {
		return fmt.Errorf("replication_policy_transition_required")
	}

	transition.State = strings.TrimSpace(strings.ToLower(transition.State))
	transition.RunID = strings.TrimSpace(transition.RunID)
	transition.Reason = strings.TrimSpace(transition.Reason)
	transition.SourceNodeID = strings.TrimSpace(transition.SourceNodeID)
	transition.TargetNodeID = strings.TrimSpace(transition.TargetNodeID)
	transition.Error = strings.TrimSpace(transition.Error)
	if transition.State == "" {
		transition.State = ReplicationTransitionStateNone
	}
	if !validReplicationTransitionState(transition.State) {
		return fmt.Errorf("invalid_replication_transition_state")
	}

	updates := map[string]any{
		"transition_state":          transition.State,
		"transition_run_id":         transition.RunID,
		"transition_reason":         transition.Reason,
		"transition_source_node_id": transition.SourceNodeID,
		"transition_target_node_id": transition.TargetNodeID,
		"transition_owner_epoch":    transition.OwnerEpoch,
		"transition_requested_at":   transition.RequestedAt,
		"transition_demoted_at":     transition.DemotedAt,
		"transition_catchup_at":     transition.CatchupAt,
		"transition_promoted_at":    transition.PromotedAt,
		"transition_completed_at":   transition.CompletedAt,
		"transition_error":          transition.Error,
		"updated_at":                time.Now().UTC(),
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

func upsertReplicationReceipt(db *gorm.DB, receipt *ReplicationReceipt) error {
	if receipt == nil {
		return fmt.Errorf("replication_receipt_required")
	}

	receipt.GuestType = strings.TrimSpace(strings.ToLower(receipt.GuestType))
	receipt.SourceNodeID = strings.TrimSpace(receipt.SourceNodeID)
	receipt.TargetNodeID = strings.TrimSpace(receipt.TargetNodeID)
	receipt.Status = strings.TrimSpace(strings.ToLower(receipt.Status))
	receipt.Message = strings.TrimSpace(receipt.Message)
	receipt.Error = strings.TrimSpace(receipt.Error)
	receipt.LastSourceDataset = strings.TrimSpace(receipt.LastSourceDataset)
	receipt.LastTargetDataset = strings.TrimSpace(receipt.LastTargetDataset)

	if receipt.PolicyID == 0 {
		return fmt.Errorf("replication_receipt_policy_id_required")
	}
	if !validReplicationGuestType(receipt.GuestType) {
		return fmt.Errorf("invalid_replication_receipt_guest_type")
	}
	if receipt.SourceNodeID == "" {
		return fmt.Errorf("replication_receipt_source_node_required")
	}
	if receipt.TargetNodeID == "" {
		return fmt.Errorf("replication_receipt_target_node_required")
	}
	if receipt.Status != "success" && receipt.Status != "failed" {
		return fmt.Errorf("invalid_replication_receipt_status")
	}
	if receipt.LastAttemptAt.IsZero() {
		return fmt.Errorf("replication_receipt_last_attempt_required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing ReplicationReceipt
		err := tx.
			Where("policy_id = ? AND target_node_id = ?", receipt.PolicyID, receipt.TargetNodeID).
			First(&existing).
			Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && existing.LastAttemptAt.After(receipt.LastAttemptAt) {
			return nil
		}

		if err == nil {
			updates := map[string]any{
				"guest_type":          receipt.GuestType,
				"guest_id":            receipt.GuestID,
				"source_node_id":      receipt.SourceNodeID,
				"status":              receipt.Status,
				"message":             receipt.Message,
				"error":               receipt.Error,
				"last_attempt_at":     receipt.LastAttemptAt,
				"last_source_dataset": receipt.LastSourceDataset,
				"last_target_dataset": receipt.LastTargetDataset,
				"updated_at":          time.Now().UTC(),
			}
			if receipt.LastSuccessAt != nil {
				updates["last_success_at"] = receipt.LastSuccessAt
			}
			return tx.Model(&ReplicationReceipt{}).Where("id = ?", existing.ID).Updates(updates).Error
		}

		if createErr := tx.Create(receipt).Error; createErr == nil {
			return nil
		} else {
			var current ReplicationReceipt
			readErr := tx.
				Where("policy_id = ? AND target_node_id = ?", receipt.PolicyID, receipt.TargetNodeID).
				First(&current).
				Error
			if readErr != nil {
				return createErr
			}
			if current.LastAttemptAt.After(receipt.LastAttemptAt) {
				return nil
			}

			updates := map[string]any{
				"guest_type":          receipt.GuestType,
				"guest_id":            receipt.GuestID,
				"source_node_id":      receipt.SourceNodeID,
				"status":              receipt.Status,
				"message":             receipt.Message,
				"error":               receipt.Error,
				"last_attempt_at":     receipt.LastAttemptAt,
				"last_source_dataset": receipt.LastSourceDataset,
				"last_target_dataset": receipt.LastTargetDataset,
				"updated_at":          time.Now().UTC(),
			}
			if receipt.LastSuccessAt != nil {
				updates["last_success_at"] = receipt.LastSuccessAt
			}
			return tx.Model(&ReplicationReceipt{}).Where("id = ?", current.ID).Updates(updates).Error
		}
	})
}

func UpsertReplicationPolicyTxn(db *gorm.DB, policy *ReplicationPolicy, targets []ReplicationPolicyTarget) error {
	return upsertReplicationPolicy(db, policy, targets)
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

func UpsertClusterSSHIdentityTxn(db *gorm.DB, identity *ClusterSSHIdentity) error {
	return upsertClusterSSHIdentity(db, identity)
}

func UpsertReplicationReceiptTxn(db *gorm.DB, receipt *ReplicationReceipt) error {
	return upsertReplicationReceipt(db, receipt)
}
