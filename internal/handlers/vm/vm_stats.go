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
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"

	"github.com/gin-gonic/gin"
)

func parseVMStatsRID(c *gin.Context) (int, bool) {
	rid, err := strconv.Atoi(c.Param("rid"))
	if err != nil || rid <= 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_rid_format",
			Data:    nil,
			Error:   "rid must be a positive integer",
		})
		return 0, false
	}

	return rid, true
}

func writeVMStatsServiceError(c *gin.Context, err error) {
	if strings.Contains(strings.ToLower(err.Error()), "vm_not_found") {
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
		Message: "internal_server_error",
		Data:    nil,
		Error:   err.Error(),
	})
}

// @Summary Get VM Statistics
// @Description Retrieve statistics for a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID"
// @Param step path string true "Statistics resolution" Enums(minutely,hourly,daily,weekly,monthly,yearly)
// @Success 200 {object} internal.APIResponse[[]vmModels.VMStats] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "VM Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/stats/{step} [get]
func GetVMStats(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMStatsRID(c)
		if !ok {
			return
		}

		step := c.Param("step")
		if _, err := db.GFSStep(step).Window(); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_stats_step",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		stats, err := libvirtService.GetVMUsage(rid, db.GFSStep(step))
		if err != nil {
			writeVMStatsServiceError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]vmModels.VMStats]{
			Status:  "success",
			Message: "vm_stats_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}

// @Summary Get Initial VM Statistics
// @Description Retrieve the finest available statistics window and its availability metadata for a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID"
// @Success 200 {object} internal.APIResponse[db.StatsBootstrap[vmModels.VMStats]] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "VM Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/stats [get]
func GetVMStatsBootstrap(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMStatsRID(c)
		if !ok {
			return
		}

		stats, err := libvirtService.GetVMUsageBootstrap(rid)
		if err != nil {
			writeVMStatsServiceError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[db.StatsBootstrap[vmModels.VMStats]]{
			Status:  "success",
			Message: "vm_stats_bootstrap_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}
