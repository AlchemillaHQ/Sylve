// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"

	"github.com/gin-gonic/gin"
)

type jailActionPreflightService interface {
	GetJailByCTID(ctID uint) (*jailModels.Jail, error)
	CanPerformJailAction(ctID uint, action string) (bool, error)
}

type jailLifecycleRequestService interface {
	RequestAction(
		ctx context.Context,
		guestType string,
		guestID uint,
		action string,
		source string,
		requestedBy string,
	) (*taskModels.GuestLifecycleTask, string, error)
}

type JailActionResponse struct {
	TaskID  uint   `json:"taskId"`
	CTID    uint   `json:"ctId"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
}

// @Summary Queue a Jail lifecycle action
// @Description Queue a start, stop, or restart action for a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param action path string true "Lifecycle action" Enums(start,stop,restart)
// @Success 202 {object} internal.APIResponse[JailActionResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/actions/{action} [post]
func JailAction(
	jailService jailActionPreflightService,
	lifecycleService jailLifecycleRequestService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		action := strings.ToLower(strings.TrimSpace(c.Param("action")))
		if action != "start" && action != "stop" && action != "restart" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_action",
				Error:   "Action must be one of start, stop, or restart",
				Data:    nil,
			})
			return
		}

		jail, err := jailService.GetJailByCTID(ctID)
		if err != nil {
			if isJailNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status: "error", Message: "jail_not_found", Error: err.Error(), Data: nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "failed_to_find_jail", Error: err.Error(), Data: nil,
			})
			return
		}
		if jail == nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status: "error", Message: "jail_not_found", Error: "Jail not found", Data: nil,
			})
			return
		}

		allowed, leaseErr := jailService.CanPerformJailAction(ctID, action)
		if leaseErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_check_failed",
				Error:   leaseErr.Error(),
				Data:    nil,
			})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_not_owned",
				Error:   "This node does not own the right to perform this Jail action",
				Data:    nil,
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))

		task, outcome, err := lifecycleService.RequestAction(
			c.Request.Context(),
			taskModels.GuestTypeJail,
			ctID,
			action,
			taskModels.LifecycleTaskSourceUser,
			username,
		)

		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) || errors.Is(err, lifecycle.ErrMigrationActive) {
				message := "lifecycle_task_in_progress"
				if errors.Is(err, lifecycle.ErrMigrationActive) {
					message = "migration_in_progress"
				}
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: message,
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			if errors.Is(err, lifecycle.ErrInvalidAction) || errors.Is(err, lifecycle.ErrInvalidGuest) {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_action",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if task == nil || task.ID == 0 {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Error:   "Lifecycle service returned no task",
				Data:    nil,
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", "jail_"+action)

		c.JSON(http.StatusAccepted, internal.APIResponse[JailActionResponse]{
			Status:  "success",
			Message: fmt.Sprintf("jail_%s_queued", action),
			Data: JailActionResponse{
				TaskID:  task.ID,
				CTID:    ctID,
				Action:  action,
				Outcome: outcome,
			},
			Error: "",
		})
	}
}
