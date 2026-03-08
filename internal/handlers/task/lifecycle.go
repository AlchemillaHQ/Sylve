// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package taskHandlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

func ActiveLifecycleTasks(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType := strings.TrimSpace(c.Query("guestType"))
		guestIDRaw := strings.TrimSpace(c.Query("guestId"))
		var guestID uint64
		if guestIDRaw != "" {
			parsed, err := strconv.ParseUint(guestIDRaw, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_guest_id",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			guestID = parsed
		}

		tasks, err := lifecycleService.ListActiveTasks(guestType, uint(guestID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_active_lifecycle_tasks",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]taskModels.GuestLifecycleTask]{
			Status:  "success",
			Message: "active_lifecycle_tasks_listed",
			Error:   "",
			Data:    tasks,
		})
	}
}

func RecentLifecycleTasks(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType := strings.TrimSpace(c.Query("guestType"))
		guestIDRaw := strings.TrimSpace(c.Query("guestId"))
		limitRaw := strings.TrimSpace(c.Query("limit"))

		var guestID uint64
		if guestIDRaw != "" {
			parsed, err := strconv.ParseUint(guestIDRaw, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_guest_id",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			guestID = parsed
		}

		limit := 50
		if limitRaw != "" {
			parsed, err := strconv.Atoi(limitRaw)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_limit",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			limit = parsed
		}

		tasks, err := lifecycleService.ListRecentTasks(guestType, uint(guestID), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_recent_lifecycle_tasks",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]taskModels.GuestLifecycleTask]{
			Status:  "success",
			Message: "recent_lifecycle_tasks_listed",
			Error:   "",
			Data:    tasks,
		})
	}
}

func ActiveLifecycleTaskForGuest(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType := strings.TrimSpace(c.Param("guestType"))
		guestIDRaw := strings.TrimSpace(c.Param("guestId"))

		guestID, err := strconv.ParseUint(guestIDRaw, 10, 64)
		if err != nil || guestID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_guest_id",
				Error:   "invalid_guest_id",
				Data:    nil,
			})
			return
		}

		task, err := lifecycleService.GetActiveTaskForGuest(guestType, uint(guestID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_active_lifecycle_task",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*taskModels.GuestLifecycleTask]{
			Status:  "success",
			Message: "active_lifecycle_task_retrieved",
			Error:   "",
			Data:    task,
		})
	}
}
