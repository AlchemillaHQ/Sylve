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
	"github.com/alchemillahq/sylve/internal/logger"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"

	"github.com/gin-gonic/gin"
)

var listInterfaces = iface.List

// @Summary List Network Interfaces
// @Description List all network interfaces on the system
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[[]*iface.Interface] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/interface [get]
func ListInterfaces() gin.HandlerFunc {
	return func(c *gin.Context) {
		interfaces, err := listInterfaces()

		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_network_interfaces")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_interfaces",
				Error:   "interface_list_failed",
				Data:    nil,
			})

			return
		}
		if interfaces == nil {
			interfaces = make([]*iface.Interface, 0)
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]*iface.Interface]{
			Status:  "success",
			Message: "interfaces_list",
			Error:   "",
			Data:    interfaces,
		})
	}
}
