// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/info"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type DiskActionRequest struct {
	Device string `json:"device" binding:"required,min=2"`
}

type DiskPartitionRequest struct {
	Device string   `json:"device" binding:"required,min=2"`
	Sizes  []uint64 `json:"sizes" binding:"required"`
}

// @Summary List disk devices
// @Description List all disk devices on the system
// @Tags Disk
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]diskServiceInterfaces.Disk] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/list [get]
func List(diskService *disk.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		disks, err := diskService.GetDiskDevices(ctx)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_listing_devices",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]diskServiceInterfaces.Disk]{
			Status:  "success",
			Message: "devices_listed",
			Error:   "",
			Data:    disks,
		})
	}
}

// @Summary Wipe disk
// @Description Wipe the partition table of a disk device
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskActionRequest true "Wipe disk request body"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/wipe [post]
func WipeDisk(diskService *disk.Service, infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var r DiskActionRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			validationErrors := utils.MapValidationErrors(err, DiskActionRequest{})

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    validationErrors,
			})
			return
		}

		err := diskUtils.DestroyDisk(r.Device)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_wiping_disk",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "disk_wiped",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Initialize GPT
// @Description Initialize a disk with a GPT partition table
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskActionRequest true "Initialize GPT request body"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/initialize-gpt [post]
func InitializeGPT(diskService *disk.Service, infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var r DiskActionRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			validationErrors := utils.MapValidationErrors(err, DiskActionRequest{})

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    validationErrors,
			})
			return
		}

		err := diskService.InitializeGPT(r.Device)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_initializing_gpt",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "gpt_initialized",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Create partition
// @Description Create a partition on a disk device
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskPartitionRequest true "Create partition request body"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/create-partitions [post]
func CreatePartition(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var r DiskPartitionRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			validationErrors := utils.MapValidationErrors(err, DiskPartitionRequest{})

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    validationErrors,
			})
			return
		}

		err := diskUtils.CreatePartitions(r.Device, r.Sizes)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_creating_partition",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "partition_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete partition
// @Description Delete a partition on a disk device
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskActionRequest true "Delete partition request body"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/delete-partition [post]
func DeletePartition(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var r DiskActionRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			validationErrors := utils.MapValidationErrors(err, DiskActionRequest{})

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    validationErrors,
			})
			return
		}

		err := diskUtils.DeletePartition(r.Device)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_deleting_partition",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "partition_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
