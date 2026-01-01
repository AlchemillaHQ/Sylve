// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

// @Summary Get Basic System Settings
// @Description Get basic system settings information
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[models.BasicSettings] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings [get]
func BasicSettings(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		basicSettings, err := systemService.GetBasicSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Add Usable ZFS Pools
// @Description Add usable ZFS pools to the system settings
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pools body []string true "List of ZFS Pools to add"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings/pools [put]
func AddUsablePools(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req []string
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := systemService.AddUsablePools(c.Request.Context(), req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_pools",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Toggle Service
// @Description Enable or disable a specific service in the system settings
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param service body internal.ToggleServiceRequest true "Service Toggle Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/basic-settings/toggle-service/:service [put]
func ToggleService(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		serviceParam := c.Param("service")
		if serviceParam == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_service",
				Error:   "service_param_required",
				Data:    nil,
			})
			return
		}

		service := models.AvailableService(serviceParam)

		switch service {
		case models.DHCPServer,
			models.Jails,
			models.SambaServer,
			models.Virtualization,
			models.WoLServer:
		default:
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_service",
				Error:   "unsupported_service",
				Data:    nil,
			})
			return
		}

		if err := systemService.ServiceToggle(service); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "toggle_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "service_toggled",
			Error:   "",
			Data:    nil,
		})
	}
}
