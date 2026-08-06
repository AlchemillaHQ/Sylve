// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/info"

	_ "github.com/alchemillahq/sylve/internal/db/models/info"

	"github.com/gin-gonic/gin"
)

// @Summary Get Audit Records
// @Description Get the latest audit records
// @Tags Info
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]infoModels.AuditRecord] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/audit-records [get]
func AuditRecords(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := infoService.GetAuditRecords(64)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "audit_records",
			Error:   "",
			Data:    records,
		})
	}
}
