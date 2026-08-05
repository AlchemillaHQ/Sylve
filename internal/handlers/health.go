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
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type BasicHealthCheckRequest struct {
	ClusterKey string `json:"clusterKey"`
}

// @Summary Basic health check
// @Description Retrieve the system's basic health information
// @Tags Health
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
				"hostname":     h,
				"initialized":  b.Initialized,
				"restarted":    b.Restarted,
				"sylveVersion": cmd.Version,
			},
		})
	}
}

// @Summary Basic health check with cluster authentication
// @Description Retrieve the system's basic health information. A cluster key may be supplied instead of bearer authentication.
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body BasicHealthCheckRequest false "Optional cluster-key authentication"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /health/basic [post]
func BasicHealthCheckWithClusterKeyHandler(systemService *system.Service) gin.HandlerFunc {
	return BasicHealthCheckHandler(systemService)
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
