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
	"github.com/alchemillahq/sylve/internal/services/info"

	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"

	"github.com/gin-gonic/gin"
)

// @Summary Get Basic Info
// @Description Get the basic information about the system
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.BasicInfo] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/basic [get]
func BasicInfo(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := infoService.GetBasicInfo()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[infoServiceInterfaces.BasicInfo]{
			Status:  "success",
			Message: "basic_info",
			Error:   "",
			Data:    info,
		})
	}
}
