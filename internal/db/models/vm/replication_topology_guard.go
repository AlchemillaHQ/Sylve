// SPDX-License-Identifier: BSD-2-Clause

package vmModels

import (
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

func vmStorageTopologyEqual(left, right *Storage) bool {
	if left == nil || right == nil {
		return false
	}
	leftDatasetID, rightDatasetID := uint(0), uint(0)
	if left.DatasetID != nil {
		leftDatasetID = *left.DatasetID
	}
	if right.DatasetID != nil {
		rightDatasetID = *right.DatasetID
	}
	return left.Type == right.Type && left.Name == right.Name && left.DownloadUUID == right.DownloadUUID &&
		left.Pool == right.Pool && left.Enable == right.Enable && leftDatasetID == rightDatasetID &&
		left.Size == right.Size && left.Emulation == right.Emulation &&
		left.FilesystemTarget == right.FilesystemTarget && left.ReadOnly == right.ReadOnly &&
		left.RecordSize == right.RecordSize && left.VolBlockSize == right.VolBlockSize &&
		left.BootOrder == right.BootOrder && left.VMID == right.VMID
}

func (storage *Storage) replicationTopologyMutable(tx *gorm.DB, deleting bool) error {
	if tx == nil || !replicationguard.PolicySchemaReady(tx) {
		return nil
	}
	vmID := storage.VMID
	if storage.ID != 0 {
		var current Storage
		if err := tx.First(&current, storage.ID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		vmID = current.VMID
		if !deleting && vmStorageTopologyEqual(storage, &current) {
			return nil
		}
	}
	if vmID == 0 {
		return nil
	}
	var vm VM
	if err := tx.Select("rid").First(&vm, vmID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var policy clusterModels.ReplicationPolicy
	result := tx.Select("owner_epoch", "transition_state", "transition_run_id").
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", clusterModels.ReplicationGuestTypeVM, vm.RID, true).
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
		// Only metadata reconciliation carrying the exact durable run ID may
		// replace protected storage during cutover. A user request cannot gain
		// this privilege merely because a transition happens to be active.
		authority := clusterModels.ReplicationTransitionAuthorityFromContext(tx.Statement.Context)
		if authority.RunID != "" && authority.RunID == strings.TrimSpace(policy.TransitionRunID) &&
			authority.OwnerEpoch == policy.OwnerEpoch {
			return nil
		}
	}
	if storage.Type == VMStorageTypeFilesystem && storage.Enable {
		return fmt.Errorf(ReplicationFilesystemStorageUnsupported)
	}
	return fmt.Errorf("replication_storage_topology_change_requires_policy_disabled")
}

func (storage *Storage) BeforeSave(tx *gorm.DB) error {
	if storage.ID == 0 && storage.VMID == 0 {
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
			// A bulk update does not expose the complete desired row, so treat
			// every matched storage record as a topology change.
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

func (dataset *VMStorageDataset) replicationTopologyMutable(tx *gorm.DB, deleting bool) error {
	if tx == nil || dataset.ID == 0 {
		return nil
	}
	if !deleting {
		var current VMStorageDataset
		if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
			Model(&VMStorageDataset{}).
			First(&current, dataset.ID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		if current.Pool == dataset.Pool && current.Name == dataset.Name && current.GUID == dataset.GUID {
			return nil
		}
	}
	var storages []Storage
	if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
		Model(&Storage{}).
		Where("dataset_id = ?", dataset.ID).
		Find(&storages).Error; err != nil {
		return err
	}
	for i := range storages {
		if err := storages[i].replicationTopologyMutable(tx, true); err != nil {
			return err
		}
	}
	return nil
}

func vmStorageDatasetCandidates(tx *gorm.DB) ([]VMStorageDataset, error) {
	where, ok := tx.Statement.Clauses["WHERE"]
	if !ok || where.Expression == nil {
		return nil, nil
	}
	var candidates []VMStorageDataset
	err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
		Model(&VMStorageDataset{}).
		Clauses(where.Expression).
		Find(&candidates).Error
	return candidates, err
}

func (dataset *VMStorageDataset) BeforeSave(tx *gorm.DB) error {
	if dataset.ID != 0 {
		return dataset.replicationTopologyMutable(tx, false)
	}
	candidates, err := vmStorageDatasetCandidates(tx)
	if err != nil {
		return err
	}
	for i := range candidates {
		if err := candidates[i].replicationTopologyMutable(tx, true); err != nil {
			return err
		}
	}
	return nil
}

func (dataset *VMStorageDataset) BeforeDelete(tx *gorm.DB) error {
	if dataset.ID != 0 {
		return dataset.replicationTopologyMutable(tx, true)
	}
	candidates, err := vmStorageDatasetCandidates(tx)
	if err != nil {
		return err
	}
	for i := range candidates {
		if err := candidates[i].replicationTopologyMutable(tx, true); err != nil {
			return err
		}
	}
	return nil
}
