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

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"

	"github.com/gin-gonic/gin"
)

type JailLogsResponse struct {
	Logs string `json:"logs"`
}

type jailStateService interface {
	JailExistsByCTID(ctID uint) (bool, error)
	GetStateByCtId(ctID uint) (jailServiceInterfaces.State, error)
}

type jailLifecycleStateService interface {
	GetActiveTaskForGuest(guestType string, guestID uint) (*taskModels.GuestLifecycleTask, error)
}

type jailLogsService interface {
	GetJailLogs(ctID uint) (string, error)
}

type jailStatsService interface {
	GetJailUsage(ctID uint, step db.GFSStep) ([]jailModels.JailStats, error)
	GetJailUsageBootstrap(ctID uint) (db.StatsBootstrap[jailModels.JailStats], error)
}

func writeJailReadError(c *gin.Context, status int, code string, err error) {
	detail := code
	if err != nil {
		detail = err.Error()
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Data:    nil,
		Error:   detail,
	})
}

func writeJailNotFound(c *gin.Context) {
	writeJailReadError(c, http.StatusNotFound, "jail_not_found", nil)
}

func writeJailStatsServiceError(c *gin.Context, err error) {
	if isJailNotFoundError(err) {
		writeJailNotFound(c)
		return
	}

	writeJailReadError(c, http.StatusInternalServerError, "failed_to_get_jail_stats", err)
}

// @Summary Get jail state
// @Description Retrieve the current runtime state and pending lifecycle action for a jail by CTID
// @Tags Jail
// @Produce json
// @Param ctid path int true "Jail CTID" minimum(1)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.State] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/state [get]
func GetJailState(
	jailService jailStateService,
	lifecycleService jailLifecycleStateService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		exists, err := jailService.JailExistsByCTID(ctID)
		if err != nil {
			writeJailReadError(c, http.StatusInternalServerError, "failed_to_get_jail", err)
			return
		}
		if !exists {
			writeJailNotFound(c)
			return
		}

		state, err := jailService.GetStateByCtId(ctID)
		if err != nil {
			writeJailReadError(c, http.StatusServiceUnavailable, "jail_state_unavailable", err)
			return
		}

		activeTask, err := lifecycleService.GetActiveTaskForGuest("jail", ctID)
		if err != nil {
			writeJailReadError(c, http.StatusInternalServerError, "failed_to_get_jail_lifecycle_state", err)
			return
		}
		if activeTask != nil {
			state.PendingAction = activeTask.Action
			state.OverrideRequested = activeTask.OverrideRequested
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.State]{
			Status:  "success",
			Message: "jail_state_retrieved",
			Data:    state,
			Error:   "",
		})
	}
}

// @Summary Get jail logs
// @Description Retrieve the last 512 console-log lines for a jail by CTID
// @Tags Jail
// @Produce json
// @Param ctid path int true "Jail CTID" minimum(1)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[JailLogsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/logs [get]
func GetJailLogs(jailService jailLogsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		logs, err := jailService.GetJailLogs(ctID)
		if err != nil {
			if isJailNotFoundError(err) {
				writeJailNotFound(c)
				return
			}
			writeJailReadError(c, http.StatusInternalServerError, "failed_to_get_jail_logs", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[JailLogsResponse]{
			Status:  "success",
			Message: "jail_logs_retrieved",
			Data:    JailLogsResponse{Logs: logs},
			Error:   "",
		})
	}
}

// @Summary Get jail statistics
// @Description Retrieve statistics for a jail by CTID and GFS retention step
// @Tags Jail
// @Produce json
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param step path string true "Statistics retention step" Enums(minutely,hourly,daily,weekly,monthly,yearly)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.JailStats] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/stats/{step} [get]
func GetJailStats(jailService jailStatsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		step := db.GFSStep(c.Param("step"))
		if _, err := step.Window(); err != nil {
			writeJailReadError(c, http.StatusBadRequest, "invalid_stats_step", err)
			return
		}

		stats, err := jailService.GetJailUsage(ctID, step)
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

// @Summary Get initial jail statistics
// @Description Retrieve the finest available statistics window and its availability metadata for a jail
// @Tags Jail
// @Produce json
// @Param ctid path int true "Jail CTID" minimum(1)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[db.StatsBootstrap[jailModels.JailStats]] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/stats [get]
func GetJailStatsBootstrap(jailService jailStatsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
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
