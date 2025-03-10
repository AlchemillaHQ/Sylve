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
	infoServiceInterfaces "sylve/internal/interfaces/services/info"
	"sylve/internal/services/info"

	"github.com/gin-gonic/gin"
)

type BasicInfoResponse struct {
	Status string                          `json:"status"`
	Data   infoServiceInterfaces.BasicInfo `json:"data"`
}

// @Summary Basic Info
// @Description Get basic information about the system
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} BasicInfoResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /info/basic [get]
func BasicInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetBasicInfo()

		if err != nil {
			c.JSON(500, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_get_basic_info",
				Error:   err.Error(),
			})
		}

		c.JSON(200, BasicInfoResponse{
			Status: "success",
			Data:   info,
		})
	}
}
