// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

// @Summary Get Samba audit logs
// @Description Retrieve paginated Samba audit logs
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param hash query string false "SHA-256 token hash alternative to Bearer authentication"
// @Param page query int false "Page number" default(1) minimum(1)
// @Param size query int false "Page size" default(100) minimum(1)
// @Param sort[0][field] query string false "Sort field" Enums(id,action,share,path,createdAt,created_at)
// @Param sort[0][dir] query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} internal.APIResponse[sambaServiceInterfaces.AuditLogsResponse] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/audit-logs [get]
func GetAuditLogs(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Parse page & size with sensible defaults
		pageStr := c.DefaultQuery("page", "1")
		sizeStr := c.DefaultQuery("size", "100")
		page, _ := strconv.Atoi(pageStr)
		size, _ := strconv.Atoi(sizeStr)

		sortField := c.Query("sort[0][field]")
		sortDir := c.Query("sort[0][dir]")
		logs, err := smbService.GetAuditLogs(page, size, sortField, sortDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_samba_audit_logs",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*sambaServiceInterfaces.AuditLogsResponse]{
			Status:  "success",
			Message: "samba_audit_logs_retrieved",
			Error:   "",
			Data:    logs,
		})
	}
}
