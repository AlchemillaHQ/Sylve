// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func requireJailDeletionDetachedDB(db *gorm.DB, ctID uint) error {
	if db == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	if ctID == 0 {
		return fmt.Errorf("invalid_ct_id")
	}
	return clusterModels.RequireGuestDeletionDetachedTxn(
		db,
		clusterModels.ReplicationGuestTypeJail,
		ctID,
	)
}

func (s *Service) RequireJailDeletionDetached(ctID uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	return requireJailDeletionDetachedDB(s.DB, ctID)
}
