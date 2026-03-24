// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/gin-gonic/gin"
)

// @Summary Get VM Logs
// @Description Retrieve console log for a specific VM by RID
// @Tags VM
// @Accept json
// @Produce json
// @Param rid path int true "VM RID"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "VM Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/logs/:rid [get]
func GetVMLogs(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid",
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format: " + err.Error(),
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		logs, err := libvirtService.GetVMLogs(uint(ridInt))
		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(404, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm_logs: " + err.Error(),
				Data:    nil,
				Error:   "Internal Server Error",
			})
			return
		}

		type LogsResponse struct {
			Logs string `json:"logs"`
		}

		c.JSON(200, internal.APIResponse[LogsResponse]{
			Status:  "success",
			Message: "vm_logs_retrieved",
			Data:    LogsResponse{Logs: logs},
			Error:   "",
		})
	}
}
