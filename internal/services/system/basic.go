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
	var pools []*gzfs.ZPool

	if err := s.DB.WithContext(ctx).First(&basicSettings).Error; err != nil {
		return pools, err
	}

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

	normalizedReq, validationErrors := normalizeInitializeRequest(req)
	if len(validationErrors) > 0 {
		return validationErrors
	}
	req = normalizedReq

	var basicSettings models.BasicSettings
	err := s.DB.First(&basicSettings).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return []error{newInitializationError(InitializationErrorInternal, err)}
		}
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
		if err := tx.Create(&basicSettings).Error; err != nil {
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
