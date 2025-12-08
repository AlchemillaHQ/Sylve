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

	"github.com/alchemillahq/sylve/internal"

	"github.com/alchemillahq/sylve/internal/db"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"github.com/gin-gonic/gin"

	zfsUtils "github.com/alchemillahq/sylve/pkg/zfs"
)

type AvgIODelayResponse struct {
	Delay float64 `json:"delay"`
}

type PoolDisksUsageResponse struct {
	Total float64 `json:"total"`
	Usage float64 `json:"usage"`
}

type ZpoolListResponse struct {
	Status  string            `json:"status"`
	Message string            `json:"message"`
	Error   string            `json:"error"`
	Data    []*zfsUtils.Zpool `json:"data"`
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
		var pools []*zfsUtils.Zpool
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

		c.JSON(http.StatusOK, internal.APIResponse[[]*zfsUtils.Zpool]{
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
// @Success 200 {object} internal.APIResponse[PoolDisksUsageResponse] "Disk usage percentage"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/pools/disk-usage [get]
func GetDisksUsage(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		fmt.Println(1)

		poolDisksUsageResponse := PoolDisksUsageResponse{
			Usage: 0,
			Total: 0,
		}

		pools, err := zfsUtils.ListZpools()
		if err != nil {
			fmt.Println("Error listing zpools:", err)
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    &poolDisksUsageResponse,
			})
			return
		}

		fmt.Println(2)

		var totalSize uint64
		var totalUsed uint64

		for _, pool := range pools {
			hasErr := false
			fmt.Println(2.1)
			sizeProp, err := pool.GetProperty("size")
			fmt.Println("After GetProperty size")
			if err != nil {
				fmt.Println("Error getting size property:", err)
				hasErr = true
			}

			fmt.Println(2.2)

			fmt.Println("sizeProp", sizeProp, err)

			usedProp, err := pool.GetProperty("alloc")
			if err != nil {
				fmt.Println("Error getting used property:", err)
				hasErr = true
			}
			// sizeProp := pool.Properties["size"]
			// usedProp := pool.Properties["alloc"]

			if hasErr {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status:  "error",
					Message: "internal_server_error",
					Error:   "failed_to_get_pool_size_or_used_property",
					Data:    &poolDisksUsageResponse,
				})
				return
			}

			size := zfsUtils.ParseSize(sizeProp.Value)
			used := zfsUtils.ParseSize(usedProp.Value)

			totalSize += size
			totalUsed += used
		}

		fmt.Println(3)

		var usage float64
		if totalSize > 0 {
			usage = (float64(totalUsed) / float64(totalSize)) * 100
		} else {
			usage = 0
		}

		poolDisksUsageResponse.Total = float64(totalSize)
		poolDisksUsageResponse.Usage = usage

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
// @Param request body zfsServiceInterfaces.Zpool true "Request"
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

		err := zfsService.CreatePool(request)
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

		err := zfsUtils.ScrubPool(guid)
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

		err := zfsService.DeletePool(guid)
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

		err := zfsService.ReplaceDevice(guid, request.Old, request.New)
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

		err := zfsService.EditPool(request.Name, request.Properties, request.Spares)
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
