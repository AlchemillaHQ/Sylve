// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/services/disk"
	smartLib "github.com/alchemillahq/sylve/pkg/disk/smart"

	"github.com/gin-gonic/gin"
)

type DiskActionRequest struct {
	Device string `json:"device" binding:"required,min=2"`
}

type DiskPartitionRequest struct {
	Sizes []uint64 `json:"sizes" binding:"required,min=1,max=128,dive,gte=1048576"`
}

type DiskSelfTestRequest struct {
	Device   string `json:"device" binding:"required,min=2"`
	TestType string `json:"testType" binding:"required"`
}

type diskListService interface {
	GetDiskDevices(context.Context) ([]diskServiceInterfaces.Disk, error)
	GetDiskDevicesWithoutSMART(context.Context) ([]diskServiceInterfaces.Disk, error)
}

type diskSelfTestService interface {
	GetSelfTestInfo(string) (*diskServiceInterfaces.DiskSelfTestInfo, error)
	StartSelfTestContext(context.Context, string, string) (*diskServiceInterfaces.DiskSelfTestInfo, error)
	StopSelfTest(string) (*diskServiceInterfaces.DiskSelfTestInfo, error)
}

type diskMutationService interface {
	DestroyPartitionTableContext(context.Context, string) error
	InitializeGPTContext(context.Context, string) error
	CreatePartitionsContext(context.Context, string, []uint64) error
	DeletePartitionContext(context.Context, string) error
}

// @Summary List disk devices
// @Description List all disk devices on the system
// @Tags Disk
// @Accept json
// @Produce json
// @Param smart query string false "S.M.A.R.T. collection mode" Enums(full, none) default(full)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]diskServiceInterfaces.Disk] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk [get]
func List(diskService diskListService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		var disks []diskServiceInterfaces.Disk
		var err error
		smartMode := strings.ToLower(strings.TrimSpace(c.Query("smart")))
		switch smartMode {
		case "", "full":
			disks, err = diskService.GetDiskDevices(ctx)
		case "none":
			disks, err = diskService.GetDiskDevicesWithoutSMART(ctx)
		default:
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_smart_mode",
				Error:   "smart must be either full or none",
				Data:    nil,
			})
			return
		}

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

func selfTestHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, disk.ErrInvalidPhysicalDisk),
		errors.Is(err, disk.ErrSelfTestTypeNotAllowed),
		errors.Is(err, smartLib.ErrInvalidSelfTestType),
		errors.Is(err, smartLib.ErrSelfTestConfigurationRequired):
		return http.StatusBadRequest, "invalid_smart_self_test_request"
	case errors.Is(err, disk.ErrPhysicalDiskNotFound):
		return http.StatusNotFound, "disk_not_found"
	case errors.Is(err, smartLib.ErrSelfTestInProgress), errors.Is(err, disk.ErrSelfTestNotRunning), errors.Is(err, disk.ErrSelfTestScheduleRunning), errors.Is(err, disk.ErrSelfTestSchedulerBusy):
		return http.StatusConflict, "smart_self_test_conflict"
	case errors.Is(err, smartLib.ErrUnsupportedFeature):
		return http.StatusUnprocessableEntity, "smart_self_test_unsupported"
	case smartLib.IsControllerError(err):
		return http.StatusServiceUnavailable, "smart_controller_unavailable"
	default:
		return http.StatusInternalServerError, "smart_self_test_failed"
	}
}

func writeSelfTestError(c *gin.Context, err error) {
	status, message := selfTestHTTPError(err)
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

// @Summary Get S.M.A.R.T. self-test information
// @Description Get S.M.A.R.T. self-test capabilities, status, and results for a physical disk
// @Tags Disk
// @Accept json
// @Produce json
// @Param device query string true "Physical disk device"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[diskServiceInterfaces.DiskSelfTestInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 422 {object} internal.APIResponse[any] "Unprocessable Entity"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /disk/smart/self-test [get]
func GetSelfTestInfo(service diskSelfTestService) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := service.GetSelfTestInfo(c.Query("device"))
		if err != nil {
			writeSelfTestError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*diskServiceInterfaces.DiskSelfTestInfo]{
			Status:  "success",
			Message: "smart_self_test_info_retrieved",
			Data:    info,
		})
	}
}

// @Summary Start a S.M.A.R.T. self-test
// @Description Start a supported S.M.A.R.T. self-test on a physical disk
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskSelfTestRequest true "S.M.A.R.T. self-test request"
// @Security BearerAuth
// @Success 202 {object} internal.APIResponse[diskServiceInterfaces.DiskSelfTestInfo] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 422 {object} internal.APIResponse[any] "Unprocessable Entity"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /disk/smart/self-test [post]
func StartSelfTest(service diskSelfTestService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DiskSelfTestRequest
		if !bindDiskJSON(c, &request, DiskSelfTestRequest{}) {
			return
		}
		info, err := service.StartSelfTestContext(c.Request.Context(), request.Device, request.TestType)
		if err != nil {
			writeSelfTestError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, internal.APIResponse[*diskServiceInterfaces.DiskSelfTestInfo]{
			Status:  "success",
			Message: "smart_self_test_started",
			Data:    info,
		})
	}
}

// @Summary Abort a S.M.A.R.T. self-test
// @Description Request that the active S.M.A.R.T. self-test on a physical disk be aborted
// @Tags Disk
// @Accept json
// @Produce json
// @Param request body DiskActionRequest true "S.M.A.R.T. self-test abort request"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[diskServiceInterfaces.DiskSelfTestInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 422 {object} internal.APIResponse[any] "Unprocessable Entity"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /disk/smart/self-test/abort [post]
func StopSelfTest(service diskSelfTestService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DiskActionRequest
		if !bindDiskJSON(c, &request, DiskActionRequest{}) {
			return
		}
		info, err := service.StopSelfTest(request.Device)
		if err != nil {
			writeSelfTestError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*diskServiceInterfaces.DiskSelfTestInfo]{
			Status:  "success",
			Message: "smart_self_test_abort_requested",
			Data:    info,
		})
	}
}

// @Summary Clear a disk partition table
// @Description Remove the partition table and partition metadata from a physical disk. This does not securely erase all disk data.
// @Tags Disk
// @Accept json
// @Produce json
// @Param device path string true "Physical disk device"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/{device}/partition-table [delete]
func ClearPartitionTable(service diskMutationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DestroyPartitionTableContext(c.Request.Context(), c.Param("device")); err != nil {
			writeDiskMutationError(c, "error_clearing_partition_table", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "partition_table_cleared",
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
// @Param device path string true "Physical disk device"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/{device}/partition-table [post]
func InitializeGPT(service diskMutationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.InitializeGPTContext(c.Request.Context(), c.Param("device")); err != nil {
			writeDiskMutationError(c, "error_initializing_gpt", err)
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

// @Summary Create disk partitions
// @Description Create one or more GPT partitions on a physical disk
// @Tags Disk
// @Accept json
// @Produce json
// @Param device path string true "Physical disk device"
// @Param request body DiskPartitionRequest true "Partition sizes in bytes"
// @Security BearerAuth
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/{device}/partitions [post]
func CreatePartitions(service diskMutationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DiskPartitionRequest
		if !bindDiskJSON(c, &request, DiskPartitionRequest{}) {
			return
		}
		if err := service.CreatePartitionsContext(c.Request.Context(), c.Param("device"), request.Sizes); err != nil {
			writeDiskMutationError(c, "error_creating_partitions", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "partitions_created",
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
// @Param partition path string true "Partition device"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /disk/partitions/{partition} [delete]
func DeletePartition(service diskMutationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeletePartitionContext(c.Request.Context(), c.Param("partition")); err != nil {
			writeDiskMutationError(c, "error_deleting_partition", err)
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
