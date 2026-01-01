// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package basicHandlers

import (
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

// @Summary Initialize Sylve
// @Description Initialize Sylve with the provided configuration
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body systemServiceInterfaces.InitializeRequest true "Initialization Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/initialize [post]
func Initialize(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req systemServiceInterfaces.InitializeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		errs := sS.Initialize(ctx, req)

		if len(errs) > 0 {
			var errMessages []string
			for _, err := range errs {
				errMessages = append(errMessages, err.Error())
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "initialization_failed",
				Error:   "",
				Data:    errMessages,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "system_initialized",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get Basic Settings
// @Description Retrieve the basic settings of Sylve
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[models.BasicSettings] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/settings [get]
func GetBasicSettings(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := sS.GetBasicSettings()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_retrieve_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[models.BasicSettings]{
			Status:  "success",
			Message: "settings_retrieved",
			Error:   "",
			Data:    settings,
		})
	}
}

// @Summary Initiate System Reboot
// @Description Initiate a system reboot
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/reboot [post]
func RebootSystem(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		err := sS.RebootSystem()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_reboot_system",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "system_reboot_initiated",
			Error:   "",
			Data:    nil,
		})
	}
}
