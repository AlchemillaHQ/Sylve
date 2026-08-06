// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/gin-gonic/gin"
)

// @Summary Get node summary chart history
// @Description Retrieves the complete CPU, RAM, and network history used by the node summary page
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.SummaryHistory] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/summary/history [get]
func SummaryHistoryHandler(infoService *info.Service) gin.HandlerFunc {
	return summaryHistoryHandler(infoService, false)
}

// @Summary Get node summary chart history deltas
// @Description Retrieves CPU, RAM, and network rows newer than the supplied per-series cursors
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Param cpuAfter query int true "Last received CPU row ID" minimum(0)
// @Param ramAfter query int true "Last received RAM row ID" minimum(0)
// @Param networkAfter query int true "Last received network row ID" minimum(0)
// @Success 200 {object} internal.APIResponse[infoServiceInterfaces.SummaryHistory] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/summary/history/delta [get]
func SummaryHistoryDeltaHandler(infoService *info.Service) gin.HandlerFunc {
	return summaryHistoryHandler(infoService, true)
}

func summaryHistoryHandler(infoService *info.Service, delta bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var cursors *infoServiceInterfaces.SummaryHistoryCursors
		if delta {
			parsed, err := parseSummaryHistoryCursors(c)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_summary_history_cursor",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			cursors = &parsed
		}

		history, err := infoService.GetSummaryHistory(c.Request.Context(), cursors)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "summary_history_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		message := "summary_history"
		if delta {
			message = "summary_history_delta"
		}
		c.JSON(http.StatusOK, internal.APIResponse[infoServiceInterfaces.SummaryHistory]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data:    history,
		})
	}
}

func parseSummaryHistoryCursors(c *gin.Context) (infoServiceInterfaces.SummaryHistoryCursors, error) {
	cpu, err := parseSummaryHistoryCursor(c, "cpuAfter")
	if err != nil {
		return infoServiceInterfaces.SummaryHistoryCursors{}, err
	}
	ram, err := parseSummaryHistoryCursor(c, "ramAfter")
	if err != nil {
		return infoServiceInterfaces.SummaryHistoryCursors{}, err
	}
	network, err := parseSummaryHistoryCursor(c, "networkAfter")
	if err != nil {
		return infoServiceInterfaces.SummaryHistoryCursors{}, err
	}

	return infoServiceInterfaces.SummaryHistoryCursors{
		CPU:     cpu,
		RAM:     ram,
		Network: network,
	}, nil
}

func parseSummaryHistoryCursor(c *gin.Context, name string) (uint, error) {
	raw, ok := c.GetQuery(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("missing %s", name)
	}

	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || uint64(uint(parsed)) != parsed {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return uint(parsed), nil
}
