// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

func requireVMDeletionDetachedDB(db *gorm.DB, rid uint) error {
	if db == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}
	// Older/isolated standalone databases may not have initialized the
	// replication schema. In that case there cannot be a policy to detach.
	if !replicationguard.PolicySchemaReady(db) {
		return nil
	}

	var count int64
	if err := db.Model(&clusterModels.ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ?", clusterModels.ReplicationGuestTypeVM, rid).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_vm_replication_policy_before_delete: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("guest_delete_requires_replication_policy_removed")
	}

	return nil
}

// RequireVMDeletionDetached prevents a retired policy from being inherited by
// a future VM that reuses the same RID. Disabled policies still own identity.
func (s *Service) RequireVMDeletionDetached(rid uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	return requireVMDeletionDetachedDB(s.DB, rid)
}
