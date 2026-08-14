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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

const (
	defaultRecentLifecycleTaskLimit = 50
	maxRecentLifecycleTaskLimit     = 200
)

func parseLifecycleGuestType(raw string) (string, bool) {
	guestType := strings.ToLower(strings.TrimSpace(raw))
	switch guestType {
	case taskModels.GuestTypeVM,
		taskModels.GuestTypeJail,
		taskModels.GuestTypeJailTemplate,
		taskModels.GuestTypeVMTemplate:
		return guestType, true
	default:
		return "", false
	}
}

func lifecycleGuestTypeQuery(c *gin.Context) (string, bool) {
	raw, present := c.GetQuery("guestType")
	if !present {
		return "", true
	}

	guestType, valid := parseLifecycleGuestType(raw)
	if !valid {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_guest_type",
			Error:   "guestType must be one of vm, jail, jail-template, or vm-template",
			Data:    nil,
		})
		return "", false
	}
	return guestType, true
}

func lifecycleGuestIDQuery(c *gin.Context) (uint, bool) {
	raw, present := c.GetQuery("guestId")
	if !present {
		return 0, true
	}

	guestID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, strconv.IntSize)
	if err != nil || guestID == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_guest_id",
			Error:   "guestId must be a positive integer",
			Data:    nil,
		})
		return 0, false
	}
	return uint(guestID), true
}

func writeLifecycleServiceError(c *gin.Context, message string, err error) {
	logger.L.Error().Err(err).Str("operation", message).Msg("lifecycle_task_request_failed")
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   message,
		Data:    nil,
	})
}

// @Summary List active lifecycle tasks
// @Description List queued and running guest lifecycle tasks, optionally filtered by guest type and positive guest ID
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param guestType query string false "Guest type" Enums(vm,jail,jail-template,vm-template)
// @Param guestId query int false "Positive guest ID" minimum(1)
// @Success 200 {object} internal.APIResponse[[]taskModels.GuestLifecycleTask] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/active [get]
func ActiveLifecycleTasks(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType, ok := lifecycleGuestTypeQuery(c)
		if !ok {
			return
		}
		guestID, ok := lifecycleGuestIDQuery(c)
		if !ok {
			return
		}

		tasks, err := lifecycleService.ListActiveTasks(guestType, guestID)
		if err != nil {
			writeLifecycleServiceError(c, "failed_to_list_active_lifecycle_tasks", err)
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

// @Summary List recent lifecycle tasks
// @Description List recent guest lifecycle tasks with an explicit bounded limit and optional guest filters
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param guestType query string false "Guest type" Enums(vm,jail,jail-template,vm-template)
// @Param guestId query int false "Positive guest ID" minimum(1)
// @Param limit query int false "Maximum tasks to return" minimum(1) maximum(200) default(50)
// @Success 200 {object} internal.APIResponse[[]taskModels.GuestLifecycleTask] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/recent [get]
func RecentLifecycleTasks(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType, ok := lifecycleGuestTypeQuery(c)
		if !ok {
			return
		}
		guestID, ok := lifecycleGuestIDQuery(c)
		if !ok {
			return
		}

		limit := defaultRecentLifecycleTaskLimit
		if limitRaw, present := c.GetQuery("limit"); present {
			parsed, err := strconv.Atoi(strings.TrimSpace(limitRaw))
			if err != nil || parsed < 1 || parsed > maxRecentLifecycleTaskLimit {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_limit",
					Error:   "limit must be an integer from 1 to 200",
					Data:    nil,
				})
				return
			}
			limit = parsed
		}

		tasks, err := lifecycleService.ListRecentTasks(guestType, guestID, limit)
		if err != nil {
			writeLifecycleServiceError(c, "failed_to_list_recent_lifecycle_tasks", err)
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

// @Summary Get the active lifecycle task for a guest
// @Description Return the newest queued or running lifecycle task for one guest; successful data is null when no task is active
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param guestType path string true "Guest type" Enums(vm,jail,jail-template,vm-template)
// @Param guestId path int true "Positive guest ID" minimum(1)
// @Success 200 {object} internal.APIResponse[taskModels.GuestLifecycleTask] "Success; data may be null"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/active/{guestType}/{guestId} [get]
func ActiveLifecycleTaskForGuest(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType, valid := parseLifecycleGuestType(c.Param("guestType"))
		if !valid {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_guest_type",
				Error:   "guestType must be one of vm, jail, jail-template, or vm-template",
				Data:    nil,
			})
			return
		}
		guestIDRaw := strings.TrimSpace(c.Param("guestId"))

		guestID, err := strconv.ParseUint(guestIDRaw, 10, strconv.IntSize)
		if err != nil || guestID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_guest_id",
				Error:   "guestId must be a positive integer",
				Data:    nil,
			})
			return
		}

		task, err := lifecycleService.GetActiveTaskForGuest(guestType, uint(guestID))
		if err != nil {
			writeLifecycleServiceError(c, "failed_to_get_active_lifecycle_task", err)
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
