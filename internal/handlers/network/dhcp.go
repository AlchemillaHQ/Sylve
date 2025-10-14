// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

// @Summary Get DHCP Config
// @Description Retrieve the current DHCP configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/config [get]
func GetDHCPConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := svc.GetConfig()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_config_retrieved",
			Error:   "",
			Data:    config,
		})
	}
}

// @Summary Modify DHCP Config
// @Description Modify the DHCP configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.ModifyDHCPConfigRequest true "Modify DHCP Config Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/config [put]
func ModifyDHCPConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.ModifyDHCPConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.SaveConfig(&req); err != nil {
			if err.Error() == "no_changes_detected" {
				c.JSON(http.StatusOK, internal.APIResponse[any]{
					Status:  "success",
					Message: "no_changes_detected",
					Error:   "",
					Data:    nil,
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_save_dhcp_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_config_saved",
			Error:   "",
			Data:    nil,
		})
	}
}
