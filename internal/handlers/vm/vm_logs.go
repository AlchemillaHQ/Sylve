// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/gin-gonic/gin"
)

type VMLogsResponse struct {
	Logs string `json:"logs"`
}

type vmLogsService interface {
	GetVMLogs(rid uint) (string, error)
}

// @Summary Get VM Logs
// @Description Retrieve console log for a specific VM by RID
// @Tags VM
// @Accept json
// @Produce json
// @Param rid path int true "VM RID"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[VMLogsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "VM Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/logs [get]
func GetVMLogs(libvirtService vmLogsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid",
				Data:    nil,
				Error:   "rid is required",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 32)
		if err != nil || ridInt == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format",
				Data:    nil,
				Error:   "rid must be a positive integer",
			})
			return
		}

		logs, err := libvirtService.GetVMLogs(uint(ridInt))
		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm_logs",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[VMLogsResponse]{
			Status:  "success",
			Message: "vm_logs_retrieved",
			Data:    VMLogsResponse{Logs: logs},
			Error:   "",
		})
	}
}
