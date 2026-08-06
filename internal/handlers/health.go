// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

// @Summary Basic health check
// @Description Retrieve the system's basic health information
// @Tags Health
// @Produce json
// @Security BearerAuth
// @Param X-Cluster-Key header string false "Cluster key authentication"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /health/basic [get]
func BasicHealthCheckHandler(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := utils.GetSystemHostname()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   "unable_to_get_hostname",
				Data:    nil,
			})
			return
		}

		b, err := systemService.GetBasicSettings()
		if err != nil && !errors.Is(err, system.ErrBasicSettingsNotFound) {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "health_check_unavailable",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "Basic health is OK",
			Data: gin.H{
				"hostname":     h,
				"initialized":  b.Initialized,
				"restarted":    b.Restarted,
				"sylveVersion": cmd.Version,
			},
		})
	}
}

// @Summary HTTP health check
// @Description Check whether the system's HTTP API is reachable
// @Tags Health
// @Security BearerAuth
// @Success 200 "OK"
// @Router /health/http [get]
func HTTPHealthCheckHandler(c *gin.Context) {
	c.Status(http.StatusOK)
}
