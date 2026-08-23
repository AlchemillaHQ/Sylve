// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package basicHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

func initializationHTTPStatus(errs []error) int {
	status := http.StatusBadRequest

	for _, err := range errs {
		switch system.ClassifyInitializationError(err) {
		case system.InitializationErrorInternal:
			return http.StatusInternalServerError
		case system.InitializationErrorConflict:
			status = http.StatusConflict
		case system.InitializationErrorUnprocessable:
			if status != http.StatusConflict {
				status = http.StatusUnprocessableEntity
			}
		}
	}

	return status
}

// @Summary Initialize Sylve
// @Description Initialize Sylve with the provided configuration
// @Tags Basic
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body systemServiceInterfaces.InitializeRequest true "Initialization Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 422 {object} internal.APIResponse[any] "Unprocessable Entity"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/initialize [post]
func Initialize(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req systemServiceInterfaces.InitializeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
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

			c.JSON(initializationHTTPStatus(errs), internal.APIResponse[any]{
				Status:  "error",
				Message: "initialization_failed",
				Error:   "",
				Data:    errMessages,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "system_initialized",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get Basic Settings
// @Description Retrieve the basic settings of Sylve
// @Tags Basic
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[models.BasicSettings] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/settings [get]
func GetBasicSettings(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := sS.GetBasicSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_retrieve_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[models.BasicSettings]{
			Status:  "success",
			Message: "settings_retrieved",
			Error:   "",
			Data:    settings,
		})
	}
}

// @Summary Initiate System Reboot
// @Description Initiate a system reboot
// @Tags Basic
// @Produce json
// @Security BearerAuth
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /basic/system/reboot [post]
func RebootSystem(sS *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		err := sS.RebootSystem()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_reboot_system",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "system_reboot_initiated",
			Error:   "",
			Data:    nil,
		})
	}
}
