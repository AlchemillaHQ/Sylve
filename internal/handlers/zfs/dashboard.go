// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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
	"time"

	"github.com/alchemillahq/sylve/internal"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
)

const (
	defaultDashboardRangeSeconds = 24 * 60 * 60
	maxDashboardRangeSeconds     = 70 * 24 * 60 * 60
	defaultDashboardPoints       = 900
	maxDashboardPoints           = 2000
)

func parseDashboardUintQuery(c *gin.Context, name string, required bool) (uint, error) {
	raw, exists := c.GetQuery(name)
	if !exists || strings.TrimSpace(raw) == "" {
		if required {
			return 0, fmt.Errorf("missing %s", name)
		}
		return 0, nil
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || uint64(uint(parsed)) != parsed {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return uint(parsed), nil
}

// @Summary Get the current ZFS dashboard snapshot
// @Description Retrieves current pool state, scan and error summaries, logical I/O, and ARC telemetry
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.DashboardSnapshot]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /zfs/dashboard/snapshot [get]
func DashboardSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := zfsService.GetDashboardSnapshot(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "zfs_dashboard_snapshot_failed", Error: err.Error(), Data: nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[zfsServiceInterfaces.DashboardSnapshot]{
			Status: "success", Message: "zfs_dashboard_snapshot", Error: "", Data: result,
		})
	}
}

// @Summary Get ZFS dashboard history
// @Description Retrieves bounded, downsampled pool and ARC telemetry for the ZFS dashboard
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param rangeSeconds query int false "History range in seconds"
// @Param maxPoints query int false "Maximum points per series"
// @Param poolGuid query string false "Optional pool GUID"
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.DashboardHistory]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /zfs/dashboard/history [get]
func DashboardHistory(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rangeSeconds := defaultDashboardRangeSeconds
		if raw := strings.TrimSpace(c.Query("rangeSeconds")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 60 || parsed > maxDashboardRangeSeconds {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "invalid_dashboard_range", Error: "rangeSeconds must be between 60 and 6048000", Data: nil,
				})
				return
			}
			rangeSeconds = parsed
		}

		maxPoints := defaultDashboardPoints
		if raw := strings.TrimSpace(c.Query("maxPoints")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 120 || parsed > maxDashboardPoints {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "invalid_dashboard_points", Error: "maxPoints must be between 120 and 2000", Data: nil,
				})
				return
			}
			maxPoints = parsed
		}

		to := time.Now().UTC()
		result, err := zfsService.GetDashboardHistory(c.Request.Context(), zfsServiceInterfaces.DashboardHistoryQuery{
			From:      to.Add(-time.Duration(rangeSeconds) * time.Second),
			To:        to,
			PoolGUID:  strings.TrimSpace(c.Query("poolGuid")),
			MaxPoints: maxPoints,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "zfs_dashboard_history_failed", Error: err.Error(), Data: nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[zfsServiceInterfaces.DashboardHistory]{
			Status: "success", Message: "zfs_dashboard_history", Error: "", Data: result,
		})
	}
}

// @Summary Get ZFS dashboard history delta
// @Description Retrieves pool and ARC telemetry newer than the supplied cursors
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param poolAfter query int true "Last received pool telemetry row ID"
// @Param arcAfter query int true "Last received ARC telemetry row ID"
// @Param poolGuid query string false "Optional pool GUID"
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.DashboardHistory]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /zfs/dashboard/history/delta [get]
func DashboardHistoryDelta(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		poolAfter, err := parseDashboardUintQuery(c, "poolAfter", true)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_zfs_dashboard_cursor", Error: err.Error(), Data: nil,
			})
			return
		}
		arcAfter, err := parseDashboardUintQuery(c, "arcAfter", true)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_zfs_dashboard_cursor", Error: err.Error(), Data: nil,
			})
			return
		}

		result, err := zfsService.GetDashboardHistoryDelta(c.Request.Context(), zfsServiceInterfaces.DashboardDeltaQuery{
			PoolAfter: poolAfter,
			ARCAfter:  arcAfter,
			PoolGUID:  strings.TrimSpace(c.Query("poolGuid")),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "zfs_dashboard_delta_failed", Error: err.Error(), Data: nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[zfsServiceInterfaces.DashboardHistory]{
			Status: "success", Message: "zfs_dashboard_history_delta", Error: "", Data: result,
		})
	}
}
