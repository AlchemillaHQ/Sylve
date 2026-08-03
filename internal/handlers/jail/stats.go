// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"

	"github.com/gin-gonic/gin"
)

func parseJailStatsCTID(c *gin.Context) (uint, bool) {
	ctID, err := strconv.ParseUint(c.Param("ctId"), 10, 64)
	if err != nil || ctID == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_ctid_format",
			Data:    nil,
			Error:   "ctid must be a positive integer",
		})
		return 0, false
	}

	return uint(ctID), true
}

func writeJailStatsServiceError(c *gin.Context, err error) {
	if strings.Contains(strings.ToLower(err.Error()), "jail_not_found") {
		c.JSON(http.StatusNotFound, internal.APIResponse[any]{
			Status:  "error",
			Message: "jail_not_found",
			Data:    nil,
			Error:   "jail_not_found",
		})
		return
	}

	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status:  "error",
		Message: "internal_server_error",
		Data:    nil,
		Error:   err.Error(),
	})
}

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
func GetJailState(jailService *jail.Service, lifecycleService *lifecycle.Service) gin.HandlerFunc {
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

		activeTask, _ := lifecycleService.GetActiveTaskForGuest("jail", uint(idInt))
		if activeTask != nil {
			state.PendingAction = activeTask.Action
			state.OverrideRequested = activeTask.OverrideRequested
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
// @Description Retrieve statistics for a jail by CTID and GFS step
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.JailStats] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/stats/:ctid/:step [get]
func GetJailStats(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailStatsCTID(c)
		if !ok {
			return
		}

		step := c.Param("step")
		if _, err := db.GFSStep(step).Window(); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_stats_step",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		stats, err := jailService.GetJailUsage(ctID, db.GFSStep(step))
		if err != nil {
			writeJailStatsServiceError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailModels.JailStats]{
			Status:  "success",
			Message: "jail_stats_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}

// @Summary Get Initial Jail Statistics
// @Description Retrieve the finest available statistics window and its availability metadata for a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[db.StatsBootstrap[jailModels.JailStats]] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/stats/:ctid [get]
func GetJailStatsBootstrap(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailStatsCTID(c)
		if !ok {
			return
		}

		stats, err := jailService.GetJailUsageBootstrap(ctID)
		if err != nil {
			writeJailStatsServiceError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[db.StatsBootstrap[jailModels.JailStats]]{
			Status:  "success",
			Message: "jail_stats_bootstrap_retrieved",
			Data:    stats,
			Error:   "",
		})
	}
}
