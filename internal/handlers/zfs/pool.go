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
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"

	"github.com/alchemillahq/sylve/internal/db"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"github.com/gin-gonic/gin"
)

type AvgIODelayResponse struct {
	Delay float64 `json:"delay"`
}

type ZpoolListResponse struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Error   string        `json:"error"`
	Data    []*gzfs.ZPool `json:"data"`
}

type PoolStatPointResponse struct {
	PoolStatPoint map[string][]zfsServiceInterfaces.PoolStatPoint `json:"poolStatPoint"`
	IntervalMap   []db.IntervalOption                             `json:"intervalMap"`
}

type PoolEditRequest struct {
	Name       string            `json:"name"`
	Properties map[string]string `json:"properties"`
	Spares     []string          `json:"spares,omitempty"`
}

// @Summary Get Pool Status
// @Description Get the status of a ZFS pool
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[gzfs.ZPoolStatusPool] "Success"
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

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} zfsHandlers.ZpoolListResponse "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools [get]
func GetPools(zfsService *zfs.Service, systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var pools []*gzfs.ZPool
		var err error

		all := c.Query("all")
		ctx := c.Request.Context()

		if all == "true" {
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.SimpleZFSDiskUsage] "Disk usage percentage"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/disk-usage [get]
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
// @Success 200 {object} internal.APIResponse[any] "Success"
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_create_failed",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid}/scrub [post]
func ScrubPool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.ScrubPool(ctx, guid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_scrub_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/{guid} [delete]
func DeletePool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.DeletePool(ctx, guid)
		if err != nil {
			if strings.HasPrefix(err.Error(), "error_getting_pool") {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "pool_not_found",
					Error:   err.Error(),
					Data:    nil,
				})

				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})

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
// @Param request body zfsServiceInterfaces.ReplaceDevice true "Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
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
			if strings.HasPrefix(err.Error(), "pool_not_found") {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "pool_not_found",
					Error:   err.Error(),
					Data:    nil,
				})

				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "device_replace_failed",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_replaced",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get Pool Stats
// @Description Get the historical stats of a ZFS pool
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param interval path int true "Interval in minutes"
// @Param limit path int true "Limit"
// @Success 200 {object} internal.APIResponse[PoolStatPointResponse] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pool/stats/{interval}/{limit} [get]
func PoolStats(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		interval := c.Param("interval")
		limit := c.Param("limit")

		intervalInt, err := strconv.Atoi(interval)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_interval",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		limitInt, err := strconv.Atoi(limit)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_limit",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		stats, count, err := zfsService.GetZpoolHistoricalStats(intervalInt, limitInt)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		response := PoolStatPointResponse{
			PoolStatPoint: stats,
			IntervalMap:   db.IntervalToMap(count),
		}

		c.JSON(http.StatusOK, internal.APIResponse[PoolStatPointResponse]{
			Status:  "success",
			Message: "pool_stats",
			Error:   "",
			Data:    response,
		})
	}
}

// @Summary Edit Pool
// @Description Edit a ZFS pool's properties
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body PoolEditRequest true "Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools [patch]
func EditPool(infoService *info.Service, zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		err := zfsService.EditPool(ctx, request.Name, request.Properties, request.Spares)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_edit_failed",
				Error:   err.Error(),
				Data:    nil,
			})
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
