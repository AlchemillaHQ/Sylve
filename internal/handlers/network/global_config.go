// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type globalConfigService interface {
	GetGlobalConfig(context.Context) (*networkServiceInterfaces.GlobalConfigState, error)
	UpdateGlobalConfig(context.Context, networkServiceInterfaces.UpdateGlobalConfigRequest) error
}

// GetNetworkGlobalConfig godoc
// @Summary Get this node's global network configuration
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.GlobalConfigState]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /network/global-config [get]
func GetNetworkGlobalConfig(service globalConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := service.GetGlobalConfig(c.Request.Context())
		if err != nil {
			logger.L.Error().Err(err).Msg("network_global_config_retrieval_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Error: "network_global_config_retrieval_failed", Message: "network_global_config_retrieval_failed"})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*networkServiceInterfaces.GlobalConfigState]{Status: "success", Data: config})
	}
}

// UpdateNetworkGlobalConfig godoc
// @Summary Update this node's DNS, search domains, and host aliases
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param config body networkServiceInterfaces.UpdateGlobalConfigRequest true "Global network configuration"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400,409,413,500 {object} internal.APIResponse[any]
// @Router /network/global-config [put]
func UpdateNetworkGlobalConfig(service globalConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request networkServiceInterfaces.UpdateGlobalConfigRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			status, code := http.StatusBadRequest, "invalid_network_global_config_request"
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status, code = http.StatusRequestEntityTooLarge, "network_global_config_request_too_large"
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Error: code, Message: code})
			return
		}
		if err := service.UpdateGlobalConfig(c.Request.Context(), request); err != nil {
			status, code := http.StatusInternalServerError, "network_global_config_update_failed"
			var configError *network.GlobalConfigError
			if errors.As(err, &configError) {
				if errors.Is(configError, network.ErrInvalidGlobalConfig) {
					status, code = http.StatusBadRequest, configError.Code
				} else if errors.Is(configError, network.ErrGlobalConfigConflict) {
					status, code = http.StatusConflict, configError.Code
				}
			}
			logger.L.Error().Err(err).Msg("network_global_config_update_failed")
			c.JSON(status, internal.APIResponse[any]{Status: "error", Error: code, Message: code})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "network_global_config_updated"})
	}
}
