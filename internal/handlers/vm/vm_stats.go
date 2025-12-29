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
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

// @Summary Get VM Statistics
// @Description Retrieve statistics for a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]vmModels.VMStats] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/stats/:rid/:step [get]
func GetVMStats(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		step := c.Param("step")
		if rid == "" || step == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid and step are required",
			})
			return
		}

		stats, err := libvirtService.GetVMUsage(int(utils.StringToUint64(rid)), db.GFSStep(step))
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]vmModels.VMStats]{
			Status:  "success",
			Message: "vm_stats_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}
