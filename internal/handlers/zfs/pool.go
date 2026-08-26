// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"fmt"
	"net/http"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"

	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"github.com/gin-gonic/gin"
)

type PoolEditRequest struct {
	Properties map[string]string `json:"properties"`
	Spares     *[]string         `json:"spares,omitempty"`
}

// @Summary Get Pool Status
// @Description Get the status of a ZFS pool
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[gzfs.ZPoolStatusPool] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid}/status [get]
func GetPoolStatus(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		status, err := zfsService.GetPoolStatus(ctx, guid)
		if status == nil || err != nil {
			if err == nil {
				err = fmt.Errorf("unknown_error")
			}

			writeZFSServiceError(c, err, "pool_status_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*gzfs.ZPoolStatusPool]{
			Status:  "success",
			Message: "pool_status",
			Error:   "",
			Data:    status,
		})
	}
}

// @Summary Get Pools
// @Description Get all ZFS pools
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param all query bool false "Include pools that are not configured as usable" default(false)
// @Success 200 {object} internal.APIResponse[[]gzfs.ZPool] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools [get]
func GetPools(zfsService *zfs.Service, systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var pools []*gzfs.ZPool
		var err error

		all, err := parseOptionalBoolQuery(c, "all")
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}
		ctx := c.Request.Context()

		if all {
			pools, err = systemService.GZFS.Zpool.List(ctx)
		} else {
			pools, err = systemService.GetUsablePools(ctx)
		}

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]*gzfs.ZPool]{
			Status:  "success",
			Message: "pools",
			Error:   "",
			Data:    pools,
		})
	}
}

// @Summary Get Disk Usage
// @Description Get the overall disk usage percentage across all ZFS pools
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.SimpleZFSDiskUsage] "Disk usage percentage"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/disks-usage [get]
func GetDisksUsage(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		poolDisksUsageResponse, err := zfsService.GetDisksUsage(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "disk_usage",
			Data:    poolDisksUsageResponse,
		})
	}
}

// @Summary Create Pool
// @Description Create a new ZFS pool
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.CreateZPoolRequest true "Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools [post]
func CreatePool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request zfsServiceInterfaces.CreateZPoolRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.CreatePool(ctx, request)
		if err != nil {
			writeZFSServiceError(c, err, "pool_create_failed")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "pool_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Scrub Pool
// @Description Start a scrub on a ZFS pool
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid}/scrub [post]
func ScrubPool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.ScrubPool(ctx, guid)
		if err != nil {
			writeZFSServiceError(c, err, "pool_scrub_failed")
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "pool_scrub_started",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Pool
// @Description Delete a ZFS pool
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid} [delete]
func DeletePool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.DeletePool(ctx, guid)
		if err != nil {
			writeZFSServiceError(c, err, "pool_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "pool_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Replace Device
// @Description Replace a device in a ZFS pool
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Param request body zfsServiceInterfaces.ReplaceDevice true "Request"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid}/replace-device [post]
func ReplaceDevice(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request zfsServiceInterfaces.ReplaceDevice

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.ReplaceDevice(ctx, guid, request.Old, request.New)
		if err != nil {
			writeZFSServiceError(c, err, "device_replace_failed")
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_replaced",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit Pool
// @Description Edit a ZFS pool's properties
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Param request body zfsHandlers.PoolEditRequest true "Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid} [patch]
func EditPool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request PoolEditRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.EditPool(ctx, guid, request.Properties, request.Spares)
		if err != nil {
			writeZFSServiceError(c, err, "pool_edit_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "pool_edited",
			Error:   "",
			Data:    nil,
		})
	}
}

type DetachRequest struct {
	Device string `json:"device" binding:"required"`
}

// @Summary Detach Device
// @Description Detach a device from a mirrored ZFS pool
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Param request body zfsHandlers.DetachRequest true "Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid}/detach [post]
func DetachDevice(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request DetachRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		if err := zfsService.DetachDevice(ctx, guid, request.Device); err != nil {
			writeZFSServiceError(c, err, "detach_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_detached",
			Error:   "",
			Data:    nil,
		})
	}
}
