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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

// @Summary Get Samba audit logs
// @Description Retrieve paginated Samba audit log entries, newest first unless sorted. createdAt is when the event happened, taken from its syslog timestamp. total counts the entries that match the filters.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number" default(1) minimum(1)
// @Param size query int false "Page size, at most 500" default(100) minimum(1) maximum(500)
// @Param sort[0][field] query string false "Sort field" Enums(id,action,share,user,client,ip,path,target,occurrences,createdAt,created_at)
// @Param sort[0][dir] query string false "Sort direction" Enums(asc,desc)
// @Param share query string false "Exact share name"
// @Param user query string false "Case-insensitive part of the user name"
// @Param action query string false "Comma-separated operations: connect, disconnect, create_file, mkdirat, unlinkat, renameat, openat, close, read, write"
// @Param q query string false "Case-insensitive text in the path, rename target, user, share, client name or IP address"
// @Param from query string false "Earliest event time (RFC 3339)"
// @Param to query string false "Latest event time (RFC 3339)"
// @Success 200 {object} internal.APIResponse[sambaServiceInterfaces.AuditLogsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid filter (invalid_audit_log_filter)"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/audit-logs [get]
func GetAuditLogs(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("size", "100"))
		query := sambaServiceInterfaces.AuditLogQuery{
			Page:      page,
			Size:      size,
			SortField: c.Query("sort[0][field]"),
			SortDir:   c.Query("sort[0][dir]"),
			Share:     c.Query("share"),
			User:      c.Query("user"),
			Search:    c.Query("q"),
		}
		for _, action := range strings.Split(c.Query("action"), ",") {
			if action = strings.TrimSpace(action); action == "" {
				continue
			}
			if !samba.ValidAuditOperation(action) {
				auditLogFilterError(c, "invalid action: "+action)
				return
			}
			query.Actions = append(query.Actions, action)
		}
		for _, bound := range []struct {
			name   string
			target **time.Time
		}{{"from", &query.From}, {"to", &query.To}} {
			name, target := bound.name, bound.target
			raw := strings.TrimSpace(c.Query(name))
			if raw == "" {
				continue
			}
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				auditLogFilterError(c, "invalid "+name+": use an RFC 3339 time")
				return
			}
			*target = &parsed
		}
		logs, err := smbService.GetAuditLogs(query)
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

func auditLogFilterError(c *gin.Context, detail string) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: "invalid_audit_log_filter",
		Error:   detail,
	})
}
