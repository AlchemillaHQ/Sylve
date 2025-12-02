// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

// @Summary List all Jails States
// @Description Retrieve a list of all jails states
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.State] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/state [get]
func ListJailStates(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		states, err := jailService.GetStates()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_jail_states: " + err.Error(),
				Data:    nil,
				Error:   "Internal Server Error",
			})
			return
		}
		c.JSON(200, internal.APIResponse[[]jailServiceInterfaces.State]{
			Status:  "success",
			Message: "jail_states_listed",
			Data:    states,
			Error:   "",
		})
	}
}

// @Summary Get Jail State
// @Description Retrieve the state of a specific jail by CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Param id path int true "Jail CTID"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.State] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/state/:id [get]
func GetJailState(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id",
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		idInt, err := strconv.Atoi(id)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id_format: " + err.Error(),
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		state, err := jailService.GetStateByCtId(uint(idInt))
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail_state: " + err.Error(),
				Data:    nil,
				Error:   "Internal Server Error",
			})
			return
		}

		c.JSON(200, internal.APIResponse[jailServiceInterfaces.State]{
			Status:  "success",
			Message: "jail_state_retrieved",
			Data:    state,
			Error:   "",
		})
	}
}

// @Summary Get Jail Logs
// @Description Retrieve Console log for a specific jail
// @Tags Jail
// @Accept json
// @Produce json
// @Param id path int true "Jail ID"
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/:id/logs [get]
func GetJailLogs(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id",
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		idInt, err := strconv.Atoi(id)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id_format: " + err.Error(),
				Data:    nil,
				Error:   "Bad Request",
			})
			return
		}

		logs, err := jailService.GetJailLogs(uint(idInt))
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail_logs: " + err.Error(),
				Data:    nil,
				Error:   "Internal Server Error",
			})
			return
		}

		type LogsResponse struct {
			Logs string `json:"logs"`
		}

		c.JSON(200, internal.APIResponse[LogsResponse]{
			Status:  "success",
			Message: "jail_logs_retrieved",
			Data:    LogsResponse{Logs: logs},
			Error:   "",
		})
	}
}

// @Summary Get Jail Statistics
// @Description Retrieve statistics for a specific jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.JailStats] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/stats/:ctId/:limit [get]
func GetJailStats(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId := c.Param("ctId")
		limit := c.Param("limit")
		if ctId == "" || limit == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "ctId and limit are required",
			})
			return
		}

		stats, err := jailService.GetJailUsage(
			uint(utils.StringToUint64(ctId)),
			int(utils.StringToUint64(limit)),
		)

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailModels.JailStats]{
			Status:  "success",
			Message: "jail_stats_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}
