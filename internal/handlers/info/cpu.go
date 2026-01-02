// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/info"

	"github.com/gin-gonic/gin"

	_ "github.com/alchemillahq/sylve/internal/db/models/info"
	_ "github.com/alchemillahq/sylve/internal/interfaces/services/info"
)

// @Summary Get Current CPU information
// @Description Retrieves real-time CPU info
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.CPUInfo]
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/cpu [get]
func RealTimeCPUInfoHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetCPUInfo(false)
		if err != nil {
			logger.L.Debug().Err(err).Msg("failed to get cpu info")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cpu_info",
			Error:   "",
			Data:    info,
		})
	}
}

// @Summary Get Historical CPU information
// @Description Retrieves historical CPU info
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[[]infoModels.CPU]
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/cpu/historical [get]
func HistoricalCPUInfoHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetCPUUsageHistorical()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cpu_info",
			Error:   "",
			Data:    info,
		})
	}
}
