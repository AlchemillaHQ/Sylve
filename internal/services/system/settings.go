package system

import (
	"context"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
)

func (s *Service) AddUsablePools(ctx context.Context, pools []string) error {
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return err
	}

	zpools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return err
	}

	zfsSet := make(map[string]struct{}, len(zpools))
	for _, p := range zpools {
		zfsSet[p] = struct{}{}
	}

	existingSet := make(map[string]struct{}, len(basicSettings.Pools))
	for _, p := range basicSettings.Pools {
		existingSet[p] = struct{}{}
	}

	for _, p := range pools {
		if _, ok := zfsSet[p]; !ok {
			return fmt.Errorf("zfs_pool_not_found: %s", p)
		}
	}

	for p := range existingSet {
		found := false
		for _, incoming := range pools {
			if incoming == p {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("removing_existing_pool_not_allowed: %s", p)
		}
	}

	basicSettings.Pools = pools
	return s.DB.Save(&basicSettings).Error
}
