// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// ValidateBackupJobSafetyInternal evaluates only this node's durable guest
// inventory. Routing places it behind the internal-cluster JWT middleware.
func ValidateBackupTargetInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_validation_service_unavailable",
				Error: "backup_target_validation_service_unavailable",
			})
			return
		}
		var request cluster.BackupTargetValidationRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: err.Error(),
			})
			return
		}
		result, err := cS.ValidateBackupTargetConnectivityLocal(c.Request.Context(), request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_validation_failed", Error: err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[clusterModels.BackupTargetNodeReadinessUpdate]{
			Status: "success", Message: "backup_target_validation_completed", Data: result,
		})
	}
}

func ValidateBackupJobSafetyInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_runner_validation_service_unavailable",
				Error: "backup_runner_validation_service_unavailable",
			})
			return
		}

		var request cluster.BackupJobSafetyValidationRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: err.Error(),
			})
			return
		}

		result, err := cS.ValidateBackupJobSafetyLocal(c.Request.Context(), request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_runner_validation_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.BackupJobSafetyValidationResult]{
			Status:  "success",
			Message: "backup_runner_validation_completed",
			Data:    result,
		})
	}
}
