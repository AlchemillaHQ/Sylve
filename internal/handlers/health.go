// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

// @Summary Basic health check
// @Description Overall basic health check of the system
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "Basic health is OK",
			Data: gin.H{
				"hostname":    h,
				"initialized": b.Initialized,
			},
		})
	}
}

func HTTPHealthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: "HTTP health is OK",
		Data:    nil,
	})
}
