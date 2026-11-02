// SPDX-License-Identifier: BSD-2-Clause

package system

import (
	"context"
	"errors"
	"slices"

	"github.com/alchemillahq/sylve/internal/db/models"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
)

type ContainerRuntime interface {
	Reconcile(context.Context) error
}

func containerServiceSet(services []models.AvailableService) error {
	if slices.Contains(services, models.Containers) && !slices.Contains(services, models.Firewall) {
		return errors.New("container_firewall_required")
	}
	return nil
}

func (s *Service) setContainersEnabled(ctx context.Context, enabled bool, externalApply ServiceRuntimeStateApplier) (bool, error) {
	if s.Containers == nil {
		return false, newSettingsError(SettingsErrorInternal, "container_service_runtime_unavailable", "", nil)
	}
	changed := false
	var conflict error
	err := s.WithServiceSettingsLock(func() error {
		var basic models.BasicSettings
		if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
			return err
		}
		changed = serviceIsEnabled(basic.Services, models.Containers) != enabled
		if !changed {
			return nil
		}
		services := servicesWithState(basic.Services, models.Containers, enabled)
		if err := containerServiceSet(services); err != nil {
			conflict = newSettingsError(SettingsErrorConflict, err.Error(), string(models.Containers), nil)
			return nil
		}
		if !enabled {
			var count int64
			if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				conflict = newSettingsError(SettingsErrorConflict, "containers_exist", string(models.Containers), nil)
				return nil
			}
		}
		return s.DB.WithContext(ctx).Model(&basic).Select("Services").Updates(&models.BasicSettings{Services: services}).Error
	})
	if err != nil {
		return false, newSettingsError(SettingsErrorInternal, "service_state_persist_failed", string(models.Containers), err)
	}
	if conflict != nil {
		return false, conflict
	}
	if enabled {
		if err := s.Containers.Reconcile(ctx); err != nil {
			return changed, newSettingsError(SettingsErrorInternal, "service_runtime_update_failed", string(models.Containers), err)
		}
	}
	if changed && externalApply != nil {
		if err := externalApply(ctx, models.Containers, enabled); err != nil {
			return changed, newSettingsError(SettingsErrorInternal, "service_runtime_update_failed", string(models.Containers), err)
		}
	}
	return changed, nil
}
