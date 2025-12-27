package system

import (
	"context"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/utils"
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

func (s *Service) ToggleDHCPServer(enable bool) error {
	if enable {
		if !pkg.IsPackageInstalled("dnsmasq") {
			return fmt.Errorf("dnsmasq package is not installed")
		}

		_, err := utils.RunCommand("service", "dnsmasq", "start")
		return err
	} else {
		_, err := utils.RunCommand("service", "dnsmasq", "stop")
		return err
	}
}

func (s *Service) ServiceToggle(service models.AvailableService) error {
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return err
	}

	serviceSet := make(map[models.AvailableService]bool)
	for _, srv := range basicSettings.Services {
		serviceSet[srv] = true
	}

	enabled := !serviceSet[service]
	serviceSet[service] = enabled
	if !enabled {
		delete(serviceSet, service)
	}

	switch service {
	case models.DHCPServer:
		if err := s.ToggleDHCPServer(enabled); err != nil {
			return err
		}
	}

	basicSettings.Services = basicSettings.Services[:0]
	for srv := range serviceSet {
		basicSettings.Services = append(basicSettings.Services, srv)
	}

	return s.DB.Save(&basicSettings).Error
}
