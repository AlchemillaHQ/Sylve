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
	"slices"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"

	"gorm.io/gorm"
)

var ErrBasicSettingsNotFound = errors.New("basic_settings_not_found")

type InitializationErrorKind uint8

const (
	InitializationErrorInternal InitializationErrorKind = iota
	InitializationErrorBadRequest
	InitializationErrorConflict
	InitializationErrorUnprocessable
)

type initializationError struct {
	kind InitializationErrorKind
	err  error
}

func (e *initializationError) Error() string {
	return e.err.Error()
}

func (e *initializationError) Unwrap() error {
	return e.err
}

func (e *initializationError) InitializationKind() InitializationErrorKind {
	return e.kind
}

func newInitializationError(kind InitializationErrorKind, err error) error {
	return &initializationError{kind: kind, err: err}
}

func ClassifyInitializationError(err error) InitializationErrorKind {
	var initErr interface {
		InitializationKind() InitializationErrorKind
	}
	if errors.As(err, &initErr) {
		return initErr.InitializationKind()
	}

	return InitializationErrorInternal
}

func normalizeInitializeRequest(req systemServiceInterfaces.InitializeRequest) (systemServiceInterfaces.InitializeRequest, []error) {
	normalized := systemServiceInterfaces.InitializeRequest{
		Pools:    make([]string, 0, len(req.Pools)),
		Services: make([]models.AvailableService, 0, len(req.Services)),
	}

	seenPools := make(map[string]struct{}, len(req.Pools))
	for _, pool := range req.Pools {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}
		if _, exists := seenPools[pool]; exists {
			continue
		}

		seenPools[pool] = struct{}{}
		normalized.Pools = append(normalized.Pools, pool)
	}

	seenServices := make(map[models.AvailableService]struct{}, len(req.Services))
	var validationErrors []error
	for _, service := range req.Services {
		if !models.IsAvailableService(service) {
			validationErrors = append(validationErrors, newInitializationError(
				InitializationErrorBadRequest,
				fmt.Errorf("unsupported_service_%s", service),
			))
			continue
		}
		if _, exists := seenServices[service]; exists {
			validationErrors = append(validationErrors, newInitializationError(
				InitializationErrorBadRequest,
				fmt.Errorf("duplicate_service_%s", service),
			))
			continue
		}

		seenServices[service] = struct{}{}
		normalized.Services = append(normalized.Services, service)
	}

	return normalized, validationErrors
}

func (s *Service) GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error) {
	var basicSettings models.BasicSettings

	if err := s.DB.WithContext(ctx).First(&basicSettings).Error; err != nil {
		return nil, err
	}

	pools := make([]*gzfs.ZPool, 0, len(basicSettings.Pools))

	for _, name := range basicSettings.Pools {
		pool, err := s.GZFS.Zpool.Get(ctx, name)
		if err != nil {
			logger.L.Warn().Err(err).Str("pool", name).Msg("skipping missing pool")
			continue
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) Initialize(ctx context.Context, req systemServiceInterfaces.InitializeRequest) []error {
	s.initMutex.Lock()
	defer s.initMutex.Unlock()
	s.serviceSettingsMutex.Lock()
	defer s.serviceSettingsMutex.Unlock()

	normalizedReq, validationErrors := normalizeInitializeRequest(req)
	if len(validationErrors) > 0 {
		return validationErrors
	}
	req = normalizedReq

	var basicSettings models.BasicSettings
	rowExists := true
	err := s.DB.First(&basicSettings).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return []error{newInitializationError(InitializationErrorInternal, err)}
		}
		rowExists = false
		basicSettings = models.BasicSettings{ID: 1}
	}

	if basicSettings.Initialized {
		return []error{newInitializationError(
			InitializationErrorConflict,
			fmt.Errorf("system_already_initialized"),
		)}
	}

	var newSets []*gzfs.Dataset

	for _, poolName := range req.Pools {
		pool, err := s.GZFS.Zpool.Get(ctx, poolName)
		if err != nil {
			return []error{newInitializationError(
				InitializationErrorBadRequest,
				fmt.Errorf("invalid_pool_%s: %w", poolName, err),
			)}
		}

		if pool == nil {
			return []error{newInitializationError(
				InitializationErrorBadRequest,
				fmt.Errorf("pool_not_found_%s", poolName),
			)}
		}

		created, err := s.ensureSylveDatasetsOnPool(ctx, pool.Name)
		if err != nil {
			for i := len(newSets) - 1; i >= 0; i-- {
				newSets[i].Destroy(ctx, true, false)
			}

			return []error{newInitializationError(InitializationErrorInternal, err)}
		}

		newSets = append(newSets, created...)
	}

	var errs []error

	if !s.IsSupportedArch() {
		errs = append(errs, newInitializationError(
			InitializationErrorUnprocessable,
			fmt.Errorf("unsupported_architecture"),
		))
	}

	for _, service := range req.Services {
		if service == models.Virtualization {
			if err := s.CheckVirtualization(); err != nil {
				errs = append(errs, newInitializationError(
					InitializationErrorUnprocessable,
					fmt.Errorf("virtualization_check_failed: %w", err),
				))
			}
		}

		if service == models.Jails {
			if err := s.CheckJails(); err != nil {
				if err.Error() == "jails_racct_not_enabled" {
					updated, updateErr := s.ensureJailRacctEnabledAtBoot()
					if updateErr != nil {
						errs = append(errs, newInitializationError(
							InitializationErrorInternal,
							fmt.Errorf("jails_check_failed: jails_racct_autoconfig_failed: %w", updateErr),
						))
						continue
					}

					if updated {
						logger.L.Warn().Msg("jails_racct_auto_configured_in_loader_conf_reboot_required")
					} else {
						logger.L.Warn().Msg("jails_racct_not_enabled_runtime_loader_conf_already_set_reboot_required")
					}

					continue
				}

				errs = append(errs, newInitializationError(
					InitializationErrorUnprocessable,
					fmt.Errorf("jails_check_failed: %w", err),
				))
			}
		}

		if service == models.DHCPServer {
			if err := s.CheckDHCPServer(); err != nil {
				errs = append(errs, newInitializationError(
					InitializationErrorUnprocessable,
					fmt.Errorf("dhcp_server_check_failed: %w", err),
				))
			}
		}

		if service == models.SambaServer {
			if err := s.CheckSambaServer(); err != nil {
				errs = append(errs, newInitializationError(
					InitializationErrorUnprocessable,
					fmt.Errorf("samba_server_check_failed: %w", err),
				))
			}
		}

		if service == models.Firewall {
			// PF is part of base FreeBSD; no package precheck needed.
		}

		if service == models.WireGuard {
			if err := s.CheckWireGuard(); err != nil {
				errs = append(errs, newInitializationError(
					InitializationErrorUnprocessable,
					fmt.Errorf("wireguard_check_failed: %w", err),
				))
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}

	basicSettings.Pools = req.Pools
	basicSettings.Services = req.Services
	basicSettings.Initialized = true
	basicSettings.Restarted = false

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if rowExists {
			if err := tx.Save(&basicSettings).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&basicSettings).Error; err != nil {
			return err
		}

		return db.InvalidateZFSCaches(tx)
	}); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return []error{newInitializationError(
				InitializationErrorConflict,
				fmt.Errorf("system_already_initialized"),
			)}
		}

		return []error{newInitializationError(
			InitializationErrorInternal,
			fmt.Errorf("failed_to_create_basic_settings: %w", err),
		)}
	}

	return nil
}

func (s *Service) GetBasicSettings() (models.BasicSettings, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return settings, ErrBasicSettingsNotFound
		}

		return settings, fmt.Errorf("failed_to_fetch_basic_settings: %w", err)
	}

	return settings, nil
}

type bootstrapPendingItem struct {
	result    systemServiceInterfaces.BootstrapItemResult
	persisted bool
}

func (s *Service) ApplyBootstrapSettings(ctx context.Context, req systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
	s.initMutex.Lock()
	s.serviceSettingsMutex.Lock()

	result, poolsChanged, err := s.applyBootstrapSettingsLocked(ctx, req)

	s.serviceSettingsMutex.Unlock()
	s.initMutex.Unlock()

	if poolsChanged && s.OnUsablePoolsChanged != nil {
		if hookErr := s.OnUsablePoolsChanged(ctx); hookErr != nil {
			logger.L.Warn().Err(hookErr).Msg("failed to reconcile ZFS telemetry after bootstrap added pools")
		}
	}

	return result, err
}

func (s *Service) applyBootstrapSettingsLocked(ctx context.Context, req systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, bool, error) {
	empty := systemServiceInterfaces.BootstrapSettingsResult{Items: []systemServiceInterfaces.BootstrapItemResult{}}
	var basicSettings models.BasicSettings
	rowExists := true
	if err := s.DB.WithContext(ctx).First(&basicSettings).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, false, fmt.Errorf("failed_to_fetch_basic_settings: %w", err)
		}
		rowExists = false
		basicSettings = models.BasicSettings{ID: 1}
	}

	finalPools := normalizeUsablePools(basicSettings.Pools)
	poolSet := make(map[string]struct{}, len(finalPools))
	for _, pool := range finalPools {
		poolSet[pool] = struct{}{}
	}
	var finalServices []models.AvailableService
	serviceSet := make(map[models.AvailableService]struct{}, len(basicSettings.Services))
	for _, service := range basicSettings.Services {
		if _, exists := serviceSet[service]; !exists {
			serviceSet[service] = struct{}{}
			finalServices = append(finalServices, service)
		}
	}
	initialServiceCount := len(finalServices)
	wasInitialized := basicSettings.Initialized

	items := make([]bootstrapPendingItem, 0, len(req.Pools)+len(req.Services)+1)
	poolOutcomes := make(map[string]bootstrapPendingItem, len(req.Pools))
	serviceOutcomes := make(map[models.AvailableService]bootstrapPendingItem, len(req.Services))
	invalidateCaches := false

	for index, rawPool := range req.Pools {
		poolName := strings.TrimSpace(rawPool)
		if poolName == "" {
			items = append(items, newBootstrapItem("pool", index, "", systemServiceInterfaces.BootstrapFailed, "empty_pool_name"))
			continue
		}
		if previous, seen := poolOutcomes[poolName]; seen {
			previous.result.Index = index
			items = append(items, previous)
			continue
		}

		item := newBootstrapItem("pool", index, poolName, systemServiceInterfaces.BootstrapApplied, "")
		datasetsCreated, err := s.ensureBootstrapPool(ctx, poolName)
		invalidateCaches = invalidateCaches || datasetsCreated || err == nil
		if err != nil {
			item.result.Status = systemServiceInterfaces.BootstrapFailed
			item.result.Message = err.Error()
		} else if _, alreadyRegistered := poolSet[poolName]; !alreadyRegistered {
			poolSet[poolName] = struct{}{}
			finalPools = append(finalPools, poolName)
			item.persisted = true
		}
		poolOutcomes[poolName] = item
		items = append(items, item)
	}

	for index, service := range req.Services {
		if !models.IsAvailableService(service) {
			items = append(items, newBootstrapItem("service", index, string(service), systemServiceInterfaces.BootstrapFailed, fmt.Sprintf("unsupported_service_%s", service)))
			continue
		}
		if previous, seen := serviceOutcomes[service]; seen {
			previous.result.Index = index
			items = append(items, previous)
			continue
		}

		_, alreadyEnabled := serviceSet[service]
		item := newBootstrapItem("service", index, string(service), systemServiceInterfaces.BootstrapApplied, "")
		if err := s.bootstrapServicePrecheck(service); err != nil {
			item.result.Status = systemServiceInterfaces.BootstrapWarning
			if alreadyEnabled {
				item.result.Message = fmt.Sprintf("precheck_failed_existing_service_preserved: %v", err)
			} else {
				item.result.Message = fmt.Sprintf("precheck_failed_service_not_enabled: %v", err)
			}
		} else if !alreadyEnabled {
			serviceSet[service] = struct{}{}
			finalServices = append(finalServices, service)
			item.persisted = true
		}
		serviceOutcomes[service] = item
		items = append(items, item)
	}

	finalInitialized := wasInitialized || req.Initialized
	if req.Initialized {
		item := newBootstrapItem("initialized", 0, "initialized", systemServiceInterfaces.BootstrapApplied, "")
		item.persisted = !wasInitialized
		items = append(items, item)
	}

	poolsChanged := !slices.Equal(finalPools, basicSettings.Pools)
	changed := poolsChanged || !slices.Equal(finalServices, basicSettings.Services) || finalInitialized != wasInitialized
	if changed {
		basicSettings.Pools = finalPools
		basicSettings.Services = finalServices
		basicSettings.Initialized = finalInitialized

		var invalidateErr error
		saveErr := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if rowExists {
				if err := tx.Save(&basicSettings).Error; err != nil {
					return err
				}
			} else if err := tx.Create(&basicSettings).Error; err != nil {
				return err
			}
			if invalidateCaches {
				invalidateErr = db.InvalidateZFSCaches(tx)
				return invalidateErr
			}
			return nil
		})
		if saveErr != nil {
			for i := range items {
				if items[i].persisted && items[i].result.Status == systemServiceInterfaces.BootstrapApplied {
					items[i].result.Status = systemServiceInterfaces.BootstrapFailed
					items[i].result.Message = "settings_persist_failed"
				}
			}
			failure := "failed_to_persist_basic_settings"
			if invalidateErr != nil {
				failure = "failed_to_invalidate_zfs_caches"
			}
			return bootstrapItemsResult(items), false, fmt.Errorf("%s: %w", failure, saveErr)
		}
	} else if invalidateCaches {
		if err := db.InvalidateZFSCaches(s.DB.WithContext(ctx)); err != nil {
			return bootstrapItemsResult(items), false, fmt.Errorf("failed_to_invalidate_zfs_caches: %w", err)
		}
	}

	result := bootstrapItemsResult(items)
	result.RestartRequired = len(finalServices) != initialServiceCount || (finalInitialized && !wasInitialized)
	return result, poolsChanged, nil
}

func (s *Service) ensureBootstrapPool(ctx context.Context, poolName string) (bool, error) {
	if s.ensureBootstrapPoolFn != nil {
		return s.ensureBootstrapPoolFn(ctx, poolName)
	}
	if s.GZFS == nil || s.GZFS.Zpool == nil {
		return false, fmt.Errorf("zfs_client_not_configured")
	}
	created, err := s.ensureSylveDatasetsOnPool(ctx, poolName)
	return len(created) > 0, err
}

func (s *Service) bootstrapServicePrecheck(service models.AvailableService) error {
	if s.bootstrapServicePrecheckFn != nil {
		return s.bootstrapServicePrecheckFn(service)
	}
	switch service {
	case models.Virtualization:
		return s.CheckVirtualization()
	case models.Jails:
		return s.CheckJails()
	case models.DHCPServer:
		return s.CheckDHCPServer()
	case models.SambaServer:
		return s.CheckSambaServer()
	case models.WireGuard:
		return s.CheckWireGuard()
	default:
		return nil
	}
}

func newBootstrapItem(kind string, index int, name string, status systemServiceInterfaces.BootstrapItemStatus, message string) bootstrapPendingItem {
	return bootstrapPendingItem{result: systemServiceInterfaces.BootstrapItemResult{
		Kind:    kind,
		Index:   index,
		Name:    name,
		Status:  status,
		Message: message,
	}}
}

func bootstrapItemsResult(items []bootstrapPendingItem) systemServiceInterfaces.BootstrapSettingsResult {
	results := make([]systemServiceInterfaces.BootstrapItemResult, 0, len(items))
	for _, item := range items {
		results = append(results, item.result)
	}
	return systemServiceInterfaces.BootstrapSettingsResult{Items: results}
}
