// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

// @Summary Get QEMU Guest Agent info of a Virtual Machine
// @Description Retrieve QEMU Guest Agent OS and network info of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /qga/:rid [get]
func GetQemuGuestAgentInfo(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		info, err := libvirtService.GetQemuGuestAgentInfo(rid)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "qga_info_retrieved",
			Data:    info,
			Error:   "",
		})
	}
}
