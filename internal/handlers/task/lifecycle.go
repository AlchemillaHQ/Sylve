// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package taskHandlers

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
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
		taskModels.GuestTypeContainer,
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
			Error:   "guestType must be one of vm, jail, container, jail-template, or vm-template",
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

// Templates have no path of their own, so their tasks need host access.
func taskAccess(guestType string, guestID uint) (string, string) {
	switch guestType {
	case taskModels.GuestTypeVM, taskModels.GuestTypeJail, taskModels.GuestTypeContainer:
		return fmt.Sprintf("/%s/%d", guestType, guestID), auth.GuestAudit
	}
	return "/", auth.SysAudit
}

func visibleTasks(c *gin.Context, authService *auth.Service, tasks []taskModels.GuestLifecycleTask) ([]taskModels.GuestLifecycleTask, bool) {
	can, ok := middleware.Checker(c, authService)
	if !ok {
		return nil, false
	}
	return slices.DeleteFunc(tasks, func(task taskModels.GuestLifecycleTask) bool {
		path, privilege := taskAccess(task.GuestType, task.GuestID)
		return !can(path, privilege)
	}), true
}

// @Summary List active lifecycle tasks
// @Description List queued and running guest lifecycle tasks, optionally filtered by guest type and positive guest ID
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param guestType query string false "Guest type" Enums(vm,jail,container,jail-template,vm-template)
// @Param guestId query int false "Positive guest ID" minimum(1)
// @Success 200 {object} internal.APIResponse[[]taskModels.GuestLifecycleTask] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/active [get]
func ActiveLifecycleTasks(lifecycleService *lifecycle.Service, authService *auth.Service) gin.HandlerFunc {
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
		tasks, ok = visibleTasks(c, authService, tasks)
		if !ok {
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
// @Param guestType query string false "Guest type" Enums(vm,jail,container,jail-template,vm-template)
// @Param guestId query int false "Positive guest ID" minimum(1)
// @Param limit query int false "Maximum tasks to return" minimum(1) maximum(200) default(50)
// @Success 200 {object} internal.APIResponse[[]taskModels.GuestLifecycleTask] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/recent [get]
func RecentLifecycleTasks(lifecycleService *lifecycle.Service, authService *auth.Service) gin.HandlerFunc {
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
		tasks, ok = visibleTasks(c, authService, tasks)
		if !ok {
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
// @Param guestType path string true "Guest type" Enums(vm,jail,container,jail-template,vm-template)
// @Param guestId path int true "Positive guest ID" minimum(1)
// @Success 200 {object} internal.APIResponse[taskModels.GuestLifecycleTask] "Success; data may be null"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/active/{guestType}/{guestId} [get]
func ActiveLifecycleTaskForGuest(lifecycleService *lifecycle.Service, authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType, valid := parseLifecycleGuestType(c.Param("guestType"))
		if !valid {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_guest_type",
				Error:   "guestType must be one of vm, jail, container, jail-template, or vm-template",
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

		if path, privilege := taskAccess(guestType, uint(guestID)); !middleware.Allowed(c, authService, path, privilege) {
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

// @Summary Get a lifecycle task by ID
// @Description Observe an exact queued, running, or terminal task without executing it
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "Task ID" minimum(1)
// @Success 200 {object} internal.APIResponse[taskModels.GuestLifecycleTask] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/lifecycle/{taskId} [get]
func LifecycleTask(service *lifecycle.Service, authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("taskId"), 10, strconv.IntSize)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_task_id"})
			return
		}
		task, err := service.GetTask(uint(id))
		if err != nil {
			writeLifecycleServiceError(c, "failed_to_get_lifecycle_task", err)
			return
		}
		if task == nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{Status: "error", Message: "lifecycle_task_not_found"})
			return
		}
		if path, privilege := taskAccess(task.GuestType, task.GuestID); !middleware.Allowed(c, authService, path, privilege) {
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*taskModels.GuestLifecycleTask]{Status: "success", Data: task})
	}
}
