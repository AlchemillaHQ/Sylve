// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

func requireJailDeletionDetachedDB(db *gorm.DB, ctID uint) error {
	if db == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	if ctID == 0 {
		return fmt.Errorf("invalid_ct_id")
	}
	// Older/isolated standalone databases may not have initialized the
	// replication schema. In that case there cannot be a policy to detach.
	if !replicationguard.PolicySchemaReady(db) {
		return nil
	}

	var count int64
	if err := db.Model(&clusterModels.ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ?", clusterModels.ReplicationGuestTypeJail, ctID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_jail_replication_policy_before_delete: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("guest_delete_requires_replication_policy_removed")
	}

	return nil
}

// RequireJailDeletionDetached prevents a retired policy from being inherited
// by a future jail that reuses the same CTID. Disabled policies still own identity.
func (s *Service) RequireJailDeletionDetached(ctID uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	return requireJailDeletionDetachedDB(s.DB, ctID)
}
