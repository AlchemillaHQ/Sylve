// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
)

type poolScrubScheduleService interface {
	ListPoolScrubSchedules(context.Context) ([]zfs.PoolScrubScheduleView, error)
	CreatePoolScrubSchedule(context.Context, zfs.PoolScrubScheduleInput) (*zfs.PoolScrubScheduleView, error)
	UpdatePoolScrubSchedule(context.Context, uint, zfs.PoolScrubScheduleInput) (*zfs.PoolScrubScheduleView, error)
	DeletePoolScrubSchedule(context.Context, uint) error
	ListPoolScrubRuns(context.Context, string) ([]zfs.PoolScrubRunView, error)
}

func bindPoolScrubSchedule(c *gin.Context) (*zfs.PoolScrubScheduleInput, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var input zfs.PoolScrubScheduleInput
	err := decoder.Decode(&input)
	if err == nil {
		var extra any
		err = decoder.Decode(&extra)
		if errors.Is(err, io.EOF) {
			err = nil
		} else if err == nil {
			err = errors.New("only one JSON object is allowed")
		}
	}
	if err == nil && (input.PoolGUID == "" || strings.TrimSpace(input.CronExpr) == "") {
		err = errors.New("poolGuid and cronExpr are required")
	}
	if err != nil {
		status := http.StatusBadRequest
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			status = http.StatusRequestEntityTooLarge
		}
		c.JSON(status, internal.APIResponse[any]{Status: "error", Message: "invalid_scrub_schedule_payload", Error: err.Error()})
		return nil, false
	}
	return &input, true
}

func poolScrubScheduleID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_scrub_schedule_id", Error: "invalid_schedule_id"})
		return 0, false
	}
	return uint(id), true
}

// @Summary List ZFS scrub schedules
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]zfs.PoolScrubScheduleView]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /zfs/pools/scrub/schedules [get]
func ListPoolScrubSchedules(service poolScrubScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		views, err := service.ListPoolScrubSchedules(c.Request.Context())
		if err != nil {
			writeZFSServiceError(c, err, "scrub_schedules_list_failed")
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]zfs.PoolScrubScheduleView]{Status: "success", Message: "scrub_schedules_listed", Data: views})
	}
}

// @Summary Create a ZFS scrub schedule
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfs.PoolScrubScheduleInput true "Schedule"
// @Success 201 {object} internal.APIResponse[zfs.PoolScrubScheduleView]
// @Failure 400,404,409,413,500,503 {object} internal.APIResponse[any]
// @Router /zfs/pools/scrub/schedules [post]
func CreatePoolScrubSchedule(service poolScrubScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		input, ok := bindPoolScrubSchedule(c)
		if !ok {
			return
		}
		view, err := service.CreatePoolScrubSchedule(c.Request.Context(), *input)
		if err != nil {
			writeZFSServiceError(c, err, "scrub_schedule_create_failed")
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[*zfs.PoolScrubScheduleView]{Status: "success", Message: "scrub_schedule_created", Data: view})
	}
}

// @Summary Update a ZFS scrub schedule
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Schedule ID"
// @Param request body zfs.PoolScrubScheduleInput true "Schedule"
// @Success 200 {object} internal.APIResponse[zfs.PoolScrubScheduleView]
// @Failure 400,404,409,413,500,503 {object} internal.APIResponse[any]
// @Router /zfs/pools/scrub/schedules/{id} [put]
func UpdatePoolScrubSchedule(service poolScrubScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := poolScrubScheduleID(c)
		if !ok {
			return
		}
		input, ok := bindPoolScrubSchedule(c)
		if !ok {
			return
		}
		view, err := service.UpdatePoolScrubSchedule(c.Request.Context(), id, *input)
		if err != nil {
			writeZFSServiceError(c, err, "scrub_schedule_update_failed")
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*zfs.PoolScrubScheduleView]{Status: "success", Message: "scrub_schedule_updated", Data: view})
	}
}

// @Summary Delete a ZFS scrub schedule
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param id path int true "Schedule ID"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400,404,409,500 {object} internal.APIResponse[any]
// @Router /zfs/pools/scrub/schedules/{id} [delete]
func DeletePoolScrubSchedule(service poolScrubScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := poolScrubScheduleID(c)
		if !ok {
			return
		}
		if err := service.DeletePoolScrubSchedule(c.Request.Context(), id); err != nil {
			writeZFSServiceError(c, err, "scrub_schedule_delete_failed")
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "scrub_schedule_deleted"})
	}
}

// @Summary List tracked ZFS scrub runs
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Pool GUID"
// @Success 200 {object} internal.APIResponse[[]zfs.PoolScrubRunView]
// @Failure 400,500 {object} internal.APIResponse[any]
// @Router /zfs/pools/{guid}/scrub/runs [get]
func ListPoolScrubRuns(service poolScrubScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		views, err := service.ListPoolScrubRuns(c.Request.Context(), c.Param("guid"))
		if err != nil {
			writeZFSServiceError(c, err, "scrub_runs_list_failed")
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]zfs.PoolScrubRunView]{Status: "success", Message: "scrub_runs_listed", Data: views})
	}
}
