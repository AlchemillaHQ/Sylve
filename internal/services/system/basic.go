package system

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"

	"gorm.io/gorm"
)

func (s *Service) GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error) {
	var basicSettings models.BasicSettings
	var pools []*gzfs.ZPool

	if err := s.DB.First(&basicSettings).Error; err != nil {
		return pools, err
	}

	for _, name := range basicSettings.Pools {
		pool, err := s.GZFS.Zpool.Get(ctx, name)
		if err != nil {
			return pools, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) Initialize(ctx context.Context, req systemServiceInterfaces.InitializeRequest) []error {
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
		pool, err := s.GZFS.Zpool.Get(ctx, poolName)
		if err != nil {
			return []error{fmt.Errorf("invalid_pool_%s: %w", poolName, err)}
		}

		if pool == nil {
			return []error{fmt.Errorf("pool_not_found_%s", poolName)}
		}

		toCreate := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}
		for _, dataset := range toCreate {
			fullDatasetName := fmt.Sprintf("%s/%s", pool.Name, dataset)
			// sets, err := zfs.Datasets(fullDatasetName)
			sets, err := s.GZFS.ZFS.List(ctx, false, fullDatasetName)
			if err != nil {
				if !strings.Contains(err.Error(), "dataset does not exist") {
					return []error{fmt.Errorf("error_checking_dataset_%s: %w", fullDatasetName, err)}
				}
			}

			exists := len(sets) > 0
			props := map[string]string{}

			var newSets []*gzfs.Dataset
			if !exists {
				created, err := s.GZFS.ZFS.CreateFilesystem(ctx, fullDatasetName, props)
				if err != nil {
					if len(newSets) > 0 {
						for _, ds := range newSets {
							ds.Destroy(ctx, true, false)
						}
					}
					return []error{fmt.Errorf("error_creating_dataset_%s: %w", fullDatasetName, err)}
				} else {
					newSets = append(newSets, created)
				}
			}
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
