// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func bindDHCPConfigJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "dhcp_config_request_too_large",
				Error:   "dhcp_config_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_dhcp_config_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func writeDHCPConfigError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, network.ErrInvalidDHCPConfig) {
		status = http.StatusBadRequest
	} else if errors.Is(err, network.ErrDHCPConfigConflict) {
		status = http.StatusConflict
	} else {
		logger.L.Error().Err(err).Msg("dhcp_config_update_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: "failed_to_save_dhcp_config",
		Error:   network.DHCPConfigErrorCode(err),
		Data:    nil,
	})
}

// @Summary Get DHCP configuration
// @Description Retrieve the current singleton DHCP configuration
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[networkModels.DHCPConfig] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/config [get]
func GetDHCPConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := svc.GetConfig()
		if err != nil {
			logger.L.Error().Err(err).Msg("dhcp_config_retrieval_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_config",
				Error:   "dhcp_config_retrieval_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkModels.DHCPConfig]{
			Status:  "success",
			Message: "dhcp_config_retrieved",
			Error:   "",
			Data:    config,
		})
	}
}

// @Summary Update DHCP configuration
// @Description Replace and apply the current singleton DHCP configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.ModifyDHCPConfigRequest true "Modify DHCP Config Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/config [put]
func ModifyDHCPConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.ModifyDHCPConfigRequest
		if !bindDHCPConfigJSON(c, &req) {
			return
		}

		if err := svc.SaveConfig(&req); err != nil {
			writeDHCPConfigError(c, err)
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
