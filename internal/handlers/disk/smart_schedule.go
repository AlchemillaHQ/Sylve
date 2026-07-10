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
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type diskSelfTestScheduleService interface {
	ListSelfTestSchedules(context.Context) ([]disk.SelfTestScheduleView, error)
	CreateSelfTestSchedule(context.Context, disk.SelfTestScheduleInput) (*disk.SelfTestScheduleView, error)
	UpdateSelfTestSchedule(context.Context, uint, disk.SelfTestScheduleInput) (*disk.SelfTestScheduleView, error)
	DeleteSelfTestSchedule(context.Context, uint) error
}

func selfTestScheduleErrorStatus(err error) int {
	switch {
	case errors.Is(err, disk.ErrInvalidSelfTestSchedule), errors.Is(err, disk.ErrInvalidPhysicalDisk), errors.Is(err, disk.ErrSelfTestTypeNotAllowed):
		return http.StatusBadRequest
	case errors.Is(err, disk.ErrSelfTestScheduleNotFound), errors.Is(err, disk.ErrPhysicalDiskNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, disk.ErrSelfTestScheduleRunning), errors.Is(err, disk.ErrSelfTestSchedulerBusy), errors.Is(err, smart.ErrSelfTestInProgress), errors.Is(err, gorm.ErrDuplicatedKey):
		return http.StatusConflict
	case errors.Is(err, smart.ErrUnsupportedFeature):
		return http.StatusUnprocessableEntity
	case smart.IsControllerError(err):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func ListSelfTestSchedules(service diskSelfTestScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		schedules, err := service.ListSelfTestSchedules(c.Request.Context())
		if err != nil {
			c.JSON(selfTestScheduleErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_listing_smart_self_test_schedules",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]disk.SelfTestScheduleView]{
			Status:  "success",
			Message: "smart_self_test_schedules_listed",
			Error:   "",
			Data:    schedules,
		})
	}
}

func CreateSelfTestSchedule(service diskSelfTestScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input disk.SelfTestScheduleInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		schedule, err := service.CreateSelfTestSchedule(c.Request.Context(), input)
		if err != nil {
			c.JSON(selfTestScheduleErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_creating_smart_self_test_schedule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[*disk.SelfTestScheduleView]{
			Status:  "success",
			Message: "smart_self_test_schedule_created",
			Error:   "",
			Data:    schedule,
		})
	}
}

func UpdateSelfTestSchedule(service diskSelfTestScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_smart_self_test_schedule_id",
				Error:   "invalid_schedule_id",
				Data:    nil,
			})
			return
		}
		var input disk.SelfTestScheduleInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		schedule, err := service.UpdateSelfTestSchedule(c.Request.Context(), uint(id), input)
		if err != nil {
			c.JSON(selfTestScheduleErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_updating_smart_self_test_schedule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*disk.SelfTestScheduleView]{
			Status:  "success",
			Message: "smart_self_test_schedule_updated",
			Error:   "",
			Data:    schedule,
		})
	}
}

func DeleteSelfTestSchedule(service diskSelfTestScheduleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_smart_self_test_schedule_id",
				Error:   "invalid_schedule_id",
				Data:    nil,
			})
			return
		}
		if err := service.DeleteSelfTestSchedule(c.Request.Context(), uint(id)); err != nil {
			c.JSON(selfTestScheduleErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_deleting_smart_self_test_schedule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "smart_self_test_schedule_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
