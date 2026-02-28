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
)

type ReplicationPolicy struct {
	ID           uint                      `gorm:"primaryKey" json:"id"`
	Name         string                    `gorm:"not null" json:"name"`
	GuestType    string                    `gorm:"index:idx_replication_policy_guest,priority:1;not null" json:"guestType"`
	GuestID      uint                      `gorm:"index:idx_replication_policy_guest,priority:2;not null" json:"guestId"`
	SourceNodeID string                    `gorm:"index" json:"sourceNodeId"`
	ActiveNodeID string                    `gorm:"index" json:"activeNodeId"`
	SourceMode   string                    `gorm:"not null;default:follow_active" json:"sourceMode"`
	FailbackMode string                    `gorm:"not null;default:manual" json:"failbackMode"`
	CronExpr     string                    `gorm:"not null" json:"cronExpr"`
	Enabled      bool                      `gorm:"default:true;index" json:"enabled"`
	LastRunAt    *time.Time                `json:"lastRunAt"`
	NextRunAt    *time.Time                `gorm:"index" json:"nextRunAt"`
	LastStatus   string                    `gorm:"index" json:"lastStatus"`
	LastError    string                    `gorm:"type:text" json:"lastError"`
	Targets      []ReplicationPolicyTarget `json:"targets,omitempty" gorm:"foreignKey:PolicyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAt    time.Time                 `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time                 `gorm:"autoUpdateTime" json:"updatedAt"`
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

type ClusterSSHIdentity struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeUUID  string    `gorm:"uniqueIndex;not null" json:"nodeUUID"`
	SSHUser   string    `gorm:"not null;default:root" json:"sshUser"`
	SSHHost   string    `gorm:"not null" json:"sshHost"`
	SSHPort   int       `gorm:"not null;default:22" json:"sshPort"`
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

func normalizeReplicationPolicy(p *ReplicationPolicy) {
	if p == nil {
		return
	}

	p.Name = strings.TrimSpace(p.Name)
	p.GuestType = strings.TrimSpace(strings.ToLower(p.GuestType))
	p.SourceNodeID = strings.TrimSpace(p.SourceNodeID)
	p.ActiveNodeID = strings.TrimSpace(p.ActiveNodeID)
	p.SourceMode = strings.TrimSpace(strings.ToLower(p.SourceMode))
	p.FailbackMode = strings.TrimSpace(strings.ToLower(p.FailbackMode))
	p.CronExpr = strings.TrimSpace(p.CronExpr)
	p.LastStatus = strings.TrimSpace(p.LastStatus)
	p.LastError = strings.TrimSpace(p.LastError)
	if p.SourceMode == "" {
		p.SourceMode = ReplicationSourceModeFollowActive
	}
	if p.FailbackMode == "" {
		p.FailbackMode = ReplicationFailbackManual
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
	if !validReplicationGuestType(policy.GuestType) {
		return fmt.Errorf("invalid_replication_guest_type")
	}
	if !validReplicationSourceMode(policy.SourceMode) {
		return fmt.Errorf("invalid_replication_source_mode")
	}
	if !validReplicationFailbackMode(policy.FailbackMode) {
		return fmt.Errorf("invalid_replication_failback_mode")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name",
				"guest_type",
				"guest_id",
				"source_node_id",
				"active_node_id",
				"source_mode",
				"failback_mode",
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
	if lease.ExpiresAt.IsZero() {
		return fmt.Errorf("replication_lease_expiry_required")
	}

	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "policy_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"guest_type",
			"guest_id",
			"owner_node_id",
			"expires_at",
			"version",
			"last_reason",
			"last_actor",
			"updated_at",
		}),
	}).Create(lease).Error
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
		identity.SSHPort = 22
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
	return upsertReplicationPolicy(db, policy, targets)
}

func UpsertReplicationLeaseTxn(db *gorm.DB, lease *ReplicationLease) error {
	return upsertReplicationLease(db, lease)
}

func UpsertClusterSSHIdentityTxn(db *gorm.DB, identity *ClusterSSHIdentity) error {
	return upsertClusterSSHIdentity(db, identity)
}
