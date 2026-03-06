// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"fmt"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/jail"

	"github.com/gin-gonic/gin"
)

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
func JailAction(jailService *jail.Service) gin.HandlerFunc {
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
		if action != "start" && action != "stop" {
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

		err = jailService.JailAction(ctId, action)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: fmt.Sprintf("failed_to_%s_jail", action),
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: fmt.Sprintf("jail_%s_success", action),
			Data:    nil,
			Error:   "",
		})
	}
}
