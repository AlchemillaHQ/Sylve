// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"sylve/internal"
	infoModels "sylve/internal/db/models/info"
	infoServiceInterfaces "sylve/internal/interfaces/services/info"
	"sylve/internal/services/info"

	"github.com/gin-gonic/gin"
)

type CPUInfoResponse struct {
	Status string                        `json:"status"`
	Data   infoServiceInterfaces.CPUInfo `json:"data"`
}

type CPUInfoHistoricalResponse struct {
	Status string           `json:"status"`
	Data   []infoModels.CPU `json:"data"`
}

// @Summary Current CPU Info
// @Description Get current CPU information
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} CPUInfoResponse
// @Failure 400 {object} internal.ErrorResponse "Bad request"
// @Router /info/cpu [get]
func CurrentCPUInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetCPUInfo(false)
		if err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_get_cpu_info",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, CPUInfoResponse{Status: "success", Data: info})
	}
}

// @Summary Historical CPU Info
// @Description Get historical CPU information
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} CPUInfoHistoricalResponse
// @Failure 400 {object} internal.ErrorResponse "Bad request"
// @Router /info/cpu/historical [get]
func HistoricalCPUInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetCPUUsageHistorical()
		if err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_get_cpu_info",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, CPUInfoHistoricalResponse{Status: "success", Data: info})
	}
}
