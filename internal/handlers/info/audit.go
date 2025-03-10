// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"sylve/internal"
	infoModels "sylve/internal/db/models/info"
	"sylve/internal/services/info"

	"github.com/gin-gonic/gin"
)

type AuditLogsResponse struct {
	Status string                `json:"status"`
	Data   []infoModels.AuditLog `json:"data"`
}

// @Summary Audit Logs
// @Description Get the latest audit logs
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} AuditLogsResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /info/audit-logs [get]
func AuditLogs(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		logs, err := infoService.GetAuditLogs(64)

		if err != nil {
			c.JSON(500, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_get_audit_logs",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, AuditLogsResponse{Status: "success", Data: logs})
	}
}
