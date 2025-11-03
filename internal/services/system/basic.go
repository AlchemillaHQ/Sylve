package system

import (
	"errors"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/pkg/zfs"
	"gorm.io/gorm"
)

func (s *Service) GetUsablePools() ([]*zfs.Zpool, error) {
	var basicSettings models.BasicSettings
	var pools []*zfs.Zpool

	if err := s.DB.First(&basicSettings).Error; err != nil {
		return pools, err
	}

	for _, name := range basicSettings.Pools {
		pool, err := zfs.GetZpool(name)
		if err != nil {
			return pools, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) GetValidPool(identifier string) (*zfs.Zpool, error) {
	usable, err := s.GetUsablePools()
	if err != nil {
		return nil, fmt.Errorf("error_fetching_usable_pools: %w", err)
	}

	for _, pool := range usable {
		if pool.Name == identifier || pool.GUID == identifier {
			return pool, nil
		}
	}

	return nil, nil
}

func (s *Service) Initialize(req systemServiceInterfaces.InitializeRequest) []error {
	var basicSettings models.BasicSettings
	err := s.DB.First(&basicSettings).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return []error{err}
		}
		basicSettings = models.BasicSettings{}
	}

	if basicSettings.Initialized {
		return []error{fmt.Errorf("system_already_initialized")}
	}

	if basicSettings.Initialized {
		return []error{fmt.Errorf("system_already_initialized")}
	}

	if len(req.Pools) == 0 {
		return []error{fmt.Errorf("no_pools_provided")}
	}

	for _, poolName := range req.Pools {
		pool, err := zfs.GetZpool(poolName)
		if err != nil {
			return []error{fmt.Errorf("invalid_pool_%s: %w", poolName, err)}
		}

		if pool == nil {
			return []error{fmt.Errorf("pool_not_found_%s", poolName)}
		}
	}

	var errs []error

	if !s.IsSupportedArch() {
		errs = append(errs, fmt.Errorf("unsupported_architecture"))
	}

	for _, service := range req.Services {
		if service == models.Virtualization {
			if err := s.CheckVirtualization(); err != nil {
				errs = append(errs, fmt.Errorf("virtualization_check_failed: %w", err))
			}
		}

		if service == models.Jails {
			if err := s.CheckJails(); err != nil {
				errs = append(errs, fmt.Errorf("jails_check_failed: %w", err))
			}
		}

		if service == models.DHCPServer {
			if err := s.CheckDHCPServer(); err != nil {
				errs = append(errs, fmt.Errorf("dhcp_server_check_failed: %w", err))
			}
		}

		if service == models.SambaServer {
			if err := s.CheckSambaServer(); err != nil {
				errs = append(errs, fmt.Errorf("samba_server_check_failed: %w", err))
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}

	basicSettings.Pools = req.Pools
	basicSettings.Services = req.Services
	basicSettings.Initialized = true

	if err := s.DB.Create(&basicSettings).Error; err != nil {
		return []error{fmt.Errorf("failed_to_create_basic_settings: %w", err)}
	}

	return nil
}

func (s *Service) GetBasicSettings() (models.BasicSettings, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return settings, fmt.Errorf("basic_settings_not_found")
		}

		return settings, fmt.Errorf("failed_to_fetch_basic_settings: %v", err)
	}

	return settings, nil
}
