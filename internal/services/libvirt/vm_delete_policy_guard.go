// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func requireVMDeletionDetachedDB(db *gorm.DB, rid uint) error {
	if db == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}
	return clusterModels.RequireGuestDeletionDetachedTxn(
		db,
		clusterModels.ReplicationGuestTypeVM,
		rid,
	)
}

func (s *Service) RequireVMDeletionDetached(rid uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	return requireVMDeletionDetachedDB(s.DB, rid)
}
