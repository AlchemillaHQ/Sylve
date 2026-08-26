// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	iscsiService "github.com/alchemillahq/sylve/internal/services/iscsi"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

type SetServiceStateRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type ServiceStateResponse struct {
	Service models.AvailableService `json:"service"`
	Enabled bool                    `json:"enabled"`
	Changed bool                    `json:"changed"`
}

func bindBasicSettingsJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "basic_settings_request_too_large",
				Error:   "basic_settings_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_basic_settings_request",
			Error:   "invalid_basic_settings_request",
			Data:    nil,
		})
		return false
	}

	return true
}

func basicSettingsErrorStatus(err error) int {
	switch system.ClassifySettingsError(err) {
	case system.SettingsErrorBadRequest:
		return http.StatusBadRequest
	case system.SettingsErrorNotFound:
		return http.StatusNotFound
	case system.SettingsErrorConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeBasicSettingsError(c *gin.Context, message string, err error) {
	status := basicSettingsErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("basic_settings_request_failed")
	}

	var data any
	if resource := system.SettingsErrorResource(err); resource != "" {
		data = map[string]string{"resource": resource}
	}

	errorCode := system.SettingsErrorCode(err)
	if errorCode == "basic_settings_operation_failed" {
		errorCode = message
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errorCode,
		Data:    data,
	})
}

// @Summary Get Basic System Settings
// @Description Get basic system settings information
// @Tags System
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[models.BasicSettings] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings [get]
func BasicSettings(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		basicSettings, err := systemService.GetBasicSettings()
		if err != nil {
			writeBasicSettingsError(c, "basic_settings_retrieval_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[models.BasicSettings]{
			Status:  "success",
			Message: "basic_settings_retrieved",
			Error:   "",
			Data:    basicSettings,
		})
	}
}

// @Summary Replace Usable ZFS Pools
// @Description Replace the complete set of ZFS pools usable by Sylve
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pools body []string true "Complete list of usable ZFS pool names"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings/pools [put]
func AddUsablePools(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req []string
		if !bindBasicSettingsJSON(c, &req) {
			return
		}

		if err := systemService.AddUsablePools(c.Request.Context(), req); err != nil {
			writeBasicSettingsError(c, "usable_pools_update_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "pools_updated_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

func applyServiceRuntimeState(
	ctx context.Context,
	networkSvc *networkService.Service,
	iscsiSvc *iscsiService.Service,
	service models.AvailableService,
	enabled bool,
) error {
	switch service {
	case models.Firewall:
		if networkSvc == nil {
			return errors.New("network_service_runtime_unavailable")
		}
		var err error
		if enabled {
			err = networkSvc.ApplyFirewallConfig()
		} else {
			err = networkSvc.DisableFirewall()
		}
		if err != nil {
			return err
		}
		networkSvc.SetFirewallServiceEnabledForTelemetry(enabled)
	case models.WireGuard:
		if networkSvc == nil {
			return errors.New("network_service_runtime_unavailable")
		}
		if enabled {
			return networkSvc.EnableWireGuardService(ctx)
		}
		return networkSvc.DisableWireGuardService(ctx)
	case models.ISCSI:
		if iscsiSvc == nil {
			return errors.New("iscsi_service_runtime_unavailable")
		}
		return iscsiSvc.SetEnabled(enabled)
	}

	return nil
}

// @Summary Set Service State
// @Description Set the desired enabled state of a service in the system settings
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param service path string true "Service name"
// @Param request body SetServiceStateRequest true "Desired service state"
// @Success 200 {object} internal.APIResponse[ServiceStateResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings/services/{service} [patch]
func SetServiceState(systemService *system.Service, networkSvc *networkService.Service, iscsiSvc *iscsiService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		serviceParam := c.Param("service")
		if serviceParam == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_service",
				Error:   "service_required",
				Data:    nil,
			})
			return
		}

		service := models.AvailableService(serviceParam)
		if !models.IsAvailableService(service) {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_service",
				Error:   "unsupported_service",
				Data:    map[string]string{"resource": serviceParam},
			})
			return
		}

		var req SetServiceStateRequest
		if !bindBasicSettingsJSON(c, &req) {
			return
		}

		changed, err := systemService.SetServiceEnabled(
			c.Request.Context(),
			service,
			*req.Enabled,
			func(ctx context.Context, service models.AvailableService, enabled bool) error {
				return applyServiceRuntimeState(ctx, networkSvc, iscsiSvc, service, enabled)
			},
		)
		if err != nil {
			writeBasicSettingsError(c, "service_state_update_failed", err)
			return
		}

		message := "service_state_updated"
		if !changed {
			message = "service_state_unchanged"
		}
		c.JSON(http.StatusOK, internal.APIResponse[ServiceStateResponse]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data: ServiceStateResponse{
				Service: service,
				Enabled: *req.Enabled,
				Changed: changed,
			},
		})
	}
}
