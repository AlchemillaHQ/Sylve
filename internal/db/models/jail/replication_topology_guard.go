// SPDX-License-Identifier: BSD-2-Clause

package jailModels

import (
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

func jailStorageTopologyEqual(left, right *Storage) bool {
	return left != nil && right != nil && left.JailID == right.JailID && left.Pool == right.Pool &&
		left.GUID == right.GUID && left.Name == right.Name && left.IsBase == right.IsBase
}

func (storage *Storage) replicationTopologyMutable(tx *gorm.DB, deleting bool) error {
	if tx == nil || !replicationguard.PolicySchemaReady(tx) {
		return nil
	}
	jailID := storage.JailID
	if storage.ID != 0 {
		var current Storage
		if err := tx.First(&current, storage.ID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		jailID = current.JailID
		if !deleting && jailStorageTopologyEqual(storage, &current) {
			return nil
		}
	}
	if jailID == 0 {
		return nil
	}
	var jail Jail
	if err := tx.Select("ct_id").First(&jail, jailID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var policy clusterModels.ReplicationPolicy
	result := tx.Select("id", "owner_epoch", "transition_state", "transition_run_id").
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", clusterModels.ReplicationGuestTypeJail, jail.CTID, true).
		Limit(1).
		Find(&policy)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	switch strings.ToLower(strings.TrimSpace(policy.TransitionState)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup,
		clusterModels.ReplicationTransitionStatePromoting,
		clusterModels.ReplicationTransitionStateRollingBack:
		authority := clusterModels.ReplicationTransitionAuthorityFromContext(tx.Statement.Context)
		if authority.RunID != "" && authority.RunID == strings.TrimSpace(policy.TransitionRunID) &&
			authority.OwnerEpoch == policy.OwnerEpoch {
			return nil
		}
		return fmt.Errorf("replication_storage_topology_change_requires_policy_disabled")
	default:
		return fmt.Errorf("replication_storage_topology_change_requires_policy_disabled")
	}
}

func (storage *Storage) BeforeSave(tx *gorm.DB) error {
	if storage.ID == 0 && storage.JailID == 0 {
		where, ok := tx.Statement.Clauses["WHERE"]
		if !ok || where.Expression == nil {
			return nil
		}
		var candidates []Storage
		if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
			Model(&Storage{}).
			Clauses(where.Expression).
			Find(&candidates).Error; err != nil {
			return err
		}
		for i := range candidates {
			if err := candidates[i].replicationTopologyMutable(tx, true); err != nil {
				return err
			}
		}
		return nil
	}
	return storage.replicationTopologyMutable(tx, false)
}

func (storage *Storage) BeforeDelete(tx *gorm.DB) error {
	if storage.ID == 0 {
		where, ok := tx.Statement.Clauses["WHERE"]
		if !ok || where.Expression == nil {
			return fmt.Errorf("replication_storage_delete_scope_unavailable")
		}
		var candidates []Storage
		if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
			Model(&Storage{}).
			Clauses(where.Expression).
			Find(&candidates).Error; err != nil {
			return err
		}
		for i := range candidates {
			if err := candidates[i].replicationTopologyMutable(tx, true); err != nil {
				return err
			}
		}
		return nil
	}
	return storage.replicationTopologyMutable(tx, true)
}
