// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"fmt"
	"slices"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"gorm.io/gorm"
)

func durableGuestIdentityInFlightIDs(tx *gorm.DB) ([]uint, error) {
	var ids []uint
	if tx.Migrator().HasTable(&jailModels.JailCreation{}) {
		if err := tx.Model(&jailModels.JailCreation{}).Where("active_ct_id IS NOT NULL").Pluck("active_ct_id", &ids).Error; err != nil {
			return nil, err
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (s *Service) requireNoDurableGuestIdentityOperations(ctx context.Context) error {
	ids, err := durableGuestIdentityInFlightIDs(s.DB.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if len(ids) != 0 {
		return fmt.Errorf("guest_identity_mutation_in_progress")
	}
	return nil
}

func (s *Service) requireGuestCreationIdentityAvailable(ctx context.Context, guestIDs []uint, kind, operationToken string) error {
	if s.DB.Migrator().HasTable(&jailModels.JailCreation{}) {
		except := ""
		if kind == clusterModels.ReplicationGuestTypeJail {
			except = operationToken
		}
		var count int64
		if err := s.DB.WithContext(ctx).Model(&jailModels.JailCreation{}).
			Where("active_ct_id IN ? AND id <> ?", guestIDs, except).Count(&count).Error; err != nil {
			return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
		}
		if count != 0 {
			return fmt.Errorf("%w: jail_creation_in_progress", clusterModels.ErrGuestIdentityAlreadyInUse)
		}
	}
	return nil
}
