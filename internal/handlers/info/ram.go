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
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/services/info"

	"github.com/gin-gonic/gin"
)

// @Summary Get RAM Info
// @Description Get the RAM information about the system
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.RAMInfo] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/ram [get]
func RAMInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetRAMInfo()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[infoServiceInterfaces.RAMInfo]{
			Status:  "success",
			Message: "ram_info",
			Error:   "",
			Data:    info,
		})
	}
}

// @Summary Get Historical RAM information
// @Description Retrieves historical RAM info
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]infoModels.RAM] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/ram/historical [get]
func HistoricalRAMInfoHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetRAMUsageHistorical()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]infoModels.RAM]{
			Status:  "success",
			Message: "ram_info",
			Error:   "",
			Data:    info,
		})
	}
}

// @Summary Get Swap Info
// @Description Get the Swap information about the system
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.SwapInfo] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/swap [get]
func SwapInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetSwapInfo()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[infoServiceInterfaces.SwapInfo]{
			Status:  "success",
			Message: "swap_info",
			Error:   "",
			Data:    info,
		})
	}
}

// @Summary Get Historical Swap information
// @Description Retrieves historical Swap info
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]infoModels.Swap] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/swap/historical [get]
func HistoricalSwapInfoHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetSwapUsageHistorical()
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
			Message: "swap_info",
			Error:   "",
			Data:    info,
		})
	}
}
