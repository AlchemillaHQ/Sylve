// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"sylve/internal"
	diskServiceInterfaces "sylve/internal/interfaces/services/disk"
	"sylve/internal/services/disk"

	"github.com/gin-gonic/gin"
)

type DiskDevicesResponse struct {
	Status string                       `json:"status"`
	Data   []diskServiceInterfaces.Disk `json:"data"`
}

// @Summary List disk devices
// @Description Get all disk devices on the system
// @Tags Disk
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} DiskDevicesResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /disk/list [get]
func List(diskService *disk.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		disks, err := diskService.GetDiskDevices()

		if err != nil {
			c.JSON(500, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_get_disk_devices",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, DiskDevicesResponse{Status: "success", Data: disks})
	}
}

// @Summary Wipe disk
// @Description Wipe a disk given its device name
// @Tags Disk
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.SuccessResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /disk/wipe [post]
func WipeDisk(diskService *disk.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Device string `json:"device"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
			})
			return
		}

		err := diskService.DestroyPartitionTable(req.Device)

		if err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "error_wiping_disk",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.SuccessResponse{Status: "success", Message: "disk_wiped"})
	}
}

// @Summary Initialize GPT
// @Description Initialize a disk with a GPT partition table
// @Tags Disk
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.SuccessResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /disk/initialize-gpt [post]
func InitializeGPT(diskService *disk.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Device string `json:"device"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
			})
			return
		}

		err := diskService.InitializeGPT(req.Device)

		if err != nil {
			c.JSON(400, internal.ErrorResponse{
				Status:  "error",
				Message: "error_initializing_gpt",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.SuccessResponse{Status: "success", Message: "gpt_initialized"})
	}
}
