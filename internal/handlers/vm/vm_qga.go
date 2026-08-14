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
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/gin-gonic/gin"
)

type vmQGAService interface {
	GetQemuGuestAgentInfo(rid uint) (libvirtServiceInterfaces.QemuGuestAgentInfo, error)
}

// @Summary Get virtual machine guest-agent information
// @Description Retrieve operating-system and network information reported by the QEMU Guest Agent of a running virtual machine
// @Tags VM
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.QemuGuestAgentInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/guest-agent [get]
func GetQemuGuestAgentInfo(service vmQGAService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}

		info, err := service.GetQemuGuestAgentInfo(rid)
		if err != nil {
			writeVMOptionError(c, err)
			return
		}

		c.JSON(200, internal.APIResponse[libvirtServiceInterfaces.QemuGuestAgentInfo]{
			Status:  "success",
			Message: "qga_info_retrieved",
			Data:    info,
			Error:   "",
		})
	}
}
