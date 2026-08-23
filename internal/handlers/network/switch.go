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
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type ListSwitchResponse struct {
	Standard []networkModels.StandardSwitch `json:"standard"`
	Manual   []networkModels.ManualSwitch   `json:"manual"`
}

// @Summary List network switches
// @Description List all configured standard and manual network switches
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[ListSwitchResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch [get]
func ListSwitches(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		standardSwitches, err := networkService.GetStandardSwitches()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_standard_switches")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_switches",
				Error:   "network_switch_list_failed",
				Data:    nil,
			})
			return
		}

		manualSwitches, err := networkService.GetManualSwitches()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_manual_switches")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_switches",
				Error:   "network_switch_list_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[ListSwitchResponse]{
			Status:  "success",
			Message: "switches_list",
			Error:   "",
			Data: ListSwitchResponse{
				Standard: standardSwitches,
				Manual:   manualSwitches,
			},
		})
	}
}
