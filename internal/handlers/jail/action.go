// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"

	"github.com/gin-gonic/gin"
)

type protectedJailMutationChecker interface {
	CanMutateProtectedJail(ctID uint) (bool, error)
}

// @Summary Perform Jail Action
// @Description Perform an action (start/stop) on a specific jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctId path int true "Container ID"
// @Param action path string true "Action to perform (start/stop)"
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/action/{action}/{ctId} [post]
func JailAction(jailService protectedJailMutationChecker, lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId, err := strconv.Atoi(c.Param("ctId"))
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Error:   "invalid_ctid: " + err.Error(),
				Data:    nil,
			})
			return
		}

		action := c.Param("action")
		if action != "start" && action != "stop" && action != "restart" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_action",
				Error:   fmt.Sprintf("invalid_action: %s", action),
				Data:    nil,
			})
			return
		}

		allowed, leaseErr := jailService.CanMutateProtectedJail(uint(ctId))
		if leaseErr != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_check_failed",
				Error:   leaseErr.Error(),
				Data:    nil,
			})
			return
		}
		if !allowed {
			c.JSON(403, internal.APIResponse[any]{
				Status:  "error",
				Message: "standby_mode_edit_not_allowed",
				Error:   "replication_lease_not_owned",
				Data:    nil,
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))

		_, outcome, err := lifecycleService.RequestAction(
			c.Request.Context(),
			taskModels.GuestTypeJail,
			uint(ctId),
			action,
			taskModels.LifecycleTaskSourceUser,
			username,
		)

		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "lifecycle_task_in_progress",
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

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: fmt.Sprintf("jail_%s_queued", action),
			Data: map[string]any{
				"outcome": outcome,
			},
			Error:   "",
		})
	}
}
