// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) checkPoolUsage(poolName string) error {
	var count int64

	if err := s.DB.Model(&vmModels.Storage{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return newSettingsError(SettingsErrorInternal, "pool_usage_check_failed", poolName,
			fmt.Errorf("failed to query vm_storages: %w", err))
	}
	if count > 0 {
		return newSettingsError(SettingsErrorConflict, "pool_in_use_by_vm_storage", poolName, nil)
	}

	if err := s.DB.Model(&vmModels.VMStorageDataset{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return newSettingsError(SettingsErrorInternal, "pool_usage_check_failed", poolName,
			fmt.Errorf("failed to query vm_storage_datasets: %w", err))
	}
	if count > 0 {
		return newSettingsError(SettingsErrorConflict, "pool_in_use_by_vm_dataset", poolName, nil)
	}

	if err := s.DB.Model(&jailModels.Storage{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return newSettingsError(SettingsErrorInternal, "pool_usage_check_failed", poolName,
			fmt.Errorf("failed to query jail_storages: %w", err))
	}
	if count > 0 {
		return newSettingsError(SettingsErrorConflict, "pool_in_use_by_jail_storage", poolName, nil)
	}

	if err := s.DB.Model(&zfsModels.PeriodicSnapshot{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return newSettingsError(SettingsErrorInternal, "pool_usage_check_failed", poolName,
			fmt.Errorf("failed to query periodic_snapshots: %w", err))
	}
	if count > 0 {
		return newSettingsError(SettingsErrorConflict, "pool_in_use_by_periodic_snapshot", poolName, nil)
	}

	return nil
}

func normalizeUsablePools(pools []string) []string {
	normalized := make([]string, 0, len(pools))
	seen := make(map[string]struct{}, len(pools))

	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}
		if _, exists := seen[pool]; exists {
			continue
		}

		seen[pool] = struct{}{}
		normalized = append(normalized, pool)
	}

	return normalized
}

func cleanupCreatedDatasets(ctx context.Context, datasets []*gzfs.Dataset) error {
	var cleanupErr error
	for i := len(datasets) - 1; i >= 0; i-- {
		if datasets[i] == nil {
			continue
		}
		if err := datasets[i].Destroy(ctx, true, false); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (s *Service) AddUsablePools(ctx context.Context, pools []string) error {
	s.serviceSettingsMutex.Lock()
	defer s.serviceSettingsMutex.Unlock()
	pools = normalizeUsablePools(pools)

	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBasicSettingsNotFound
		}
		return newSettingsError(SettingsErrorInternal, "basic_settings_retrieval_failed", "", err)
	}

	zpools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return newSettingsError(SettingsErrorInternal, "zfs_pools_list_failed", "", err)
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
			return newSettingsError(SettingsErrorNotFound, "zfs_pool_not_found", p, nil)
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
			if err := s.checkPoolUsage(p); err != nil {
				return err
			}
		}
	}

	var newSets []*gzfs.Dataset

	for _, poolName := range pools {
		created, err := s.ensureSylveDatasetsOnPool(ctx, poolName)
		newSets = append(newSets, created...)
		if err != nil {
			cleanupErr := cleanupCreatedDatasets(ctx, newSets)
			return newSettingsError(SettingsErrorInternal, "pool_bootstrap_failed", poolName,
				errors.Join(err, cleanupErr))
		}
	}

	basicSettings.Pools = pools
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&basicSettings).Error; err != nil {
			return err
		}
		return db.InvalidateZFSCaches(tx)
	}); err != nil {
		cleanupErr := cleanupCreatedDatasets(ctx, newSets)
		return newSettingsError(SettingsErrorInternal, "usable_pools_update_failed", "",
			errors.Join(err, cleanupErr))
	}

	if s.OnUsablePoolsChanged != nil {
		if err := s.OnUsablePoolsChanged(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("failed to reconcile ZFS telemetry after usable pools changed")
		}
	}
	return nil
}

func (s *Service) ToggleDHCPServer(enable bool) error {
	if enable {
		if !pkg.IsPackageInstalled("dnsmasq") {
			return fmt.Errorf("dnsmasq package is not installed")
		}

		_, err := utils.RunCommand("/usr/sbin/service", "dnsmasq", "start")
		if err != nil {
			if !strings.Contains(err.Error(), "already running") {
				return err
			}
		}
	} else {
		_, err := utils.RunCommand("/usr/sbin/service", "dnsmasq", "stop")
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not running") {
			return err
		}
	}

	return nil
}

func (s *Service) EnsureMdnsEnabled(tx *gorm.DB) error {
	var basicSettings models.BasicSettings
	if err := tx.First(&basicSettings).Error; err != nil {
		return err
	}

	for _, service := range basicSettings.Services {
		if service == models.Mdns {
			return nil
		}
	}

	basicSettings.Services = append(basicSettings.Services, models.Mdns)
	return tx.Save(&basicSettings).Error
}

func (s *Service) WithServiceSettingsLock(update func() error) error {
	s.serviceSettingsMutex.Lock()
	defer s.serviceSettingsMutex.Unlock()
	return update()
}

type ServiceRuntimeStateApplier func(context.Context, models.AvailableService, bool) error

func serviceIsEnabled(services []models.AvailableService, service models.AvailableService) bool {
	for _, enabledService := range services {
		if enabledService == service {
			return true
		}
	}
	return false
}

func servicesWithState(
	services []models.AvailableService,
	service models.AvailableService,
	enabled bool,
) []models.AvailableService {
	updated := make([]models.AvailableService, 0, len(services)+1)
	for _, enabledService := range services {
		if enabledService != service {
			updated = append(updated, enabledService)
		}
	}
	if enabled {
		updated = append(updated, service)
	}
	return updated
}

func (s *Service) applyServiceRuntimeState(
	ctx context.Context,
	service models.AvailableService,
	enabled bool,
	externalApply ServiceRuntimeStateApplier,
) error {
	switch service {
	case models.DHCPServer:
		if s.dhcpServiceStateApply != nil {
			return s.dhcpServiceStateApply(enabled)
		}
		return s.ToggleDHCPServer(enabled)
	case models.Mdns:
		if s.MdnsRebuild != nil {
			return s.MdnsRebuild()
		}
	case models.Firewall, models.WireGuard:
		if externalApply == nil {
			return fmt.Errorf("network_service_runtime_unavailable")
		}
		return externalApply(ctx, service, enabled)
	}

	return nil
}

func (s *Service) SetServiceEnabled(
	ctx context.Context,
	service models.AvailableService,
	enabled bool,
	externalApply ServiceRuntimeStateApplier,
) (bool, error) {
	s.serviceSettingsMutex.Lock()
	defer s.serviceSettingsMutex.Unlock()
	if !models.IsAvailableService(service) {
		return false, newSettingsError(SettingsErrorBadRequest, "unsupported_service", string(service), nil)
	}

	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrBasicSettingsNotFound
		}
		return false, newSettingsError(SettingsErrorInternal, "basic_settings_retrieval_failed", "", err)
	}
	currentlyEnabled := serviceIsEnabled(basicSettings.Services, service)
	if currentlyEnabled == enabled {
		return false, nil
	}

	previousServices := append([]models.AvailableService(nil), basicSettings.Services...)
	basicSettings.Services = servicesWithState(basicSettings.Services, service, enabled)
	if err := s.DB.Save(&basicSettings).Error; err != nil {
		return false, newSettingsError(SettingsErrorInternal, "service_state_persist_failed", string(service), err)
	}

	if err := s.applyServiceRuntimeState(ctx, service, enabled, externalApply); err != nil {
		applyErr := err
		basicSettings.Services = previousServices
		persistRollbackErr := s.DB.Save(&basicSettings).Error
		var runtimeRollbackErr error
		if persistRollbackErr == nil {
			runtimeRollbackErr = s.applyServiceRuntimeState(ctx, service, currentlyEnabled, externalApply)
		}

		return false, newSettingsError(SettingsErrorInternal, "service_runtime_update_failed", string(service),
			errors.Join(applyErr, persistRollbackErr, runtimeRollbackErr))
	}

	return true, nil
}
