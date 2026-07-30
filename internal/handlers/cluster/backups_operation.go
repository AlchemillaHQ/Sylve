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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func BackupJobOperationInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Action         string    `json:"action"`
			JobID          uint      `json:"jobId"`
			Token          string    `json:"token"`
			Operation      string    `json:"operation"`
			HolderNodeID   string    `json:"holderNodeId"`
			RequestPayload string    `json:"requestPayload"`
			OccurredAt     time.Time `json:"occurredAt"`
		}
		if cS == nil || cS.DB == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "cluster_service_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.JobID == 0 ||
			strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.Operation) == "" ||
			strings.TrimSpace(req.HolderNodeID) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "jobId, token, operation, and holderNodeId are required",
			})
			return
		}

		action := strings.ToLower(strings.TrimSpace(req.Action))
		bypassRaft, authorityErr := cS.RuntimeStateBypassRaft()
		if authorityErr != nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "backup_job_operation_failed", Error: authorityErr.Error(),
			})
			return
		}
		var err error
		if action == "acquire" {
			err = cS.AcquireBackupJobOperation(clusterModels.BackupJobOperationAcquire{
				JobID: req.JobID, Token: req.Token, Operation: req.Operation,
				HolderNodeID: req.HolderNodeID, RequestPayload: req.RequestPayload,
				AcquiredAt: req.OccurredAt,
			}, bypassRaft)
		} else {
			err = cS.TransitionBackupJobOperation(action, clusterModels.BackupJobOperationTransition{
				JobID: req.JobID, Token: req.Token, Operation: req.Operation,
				HolderNodeID: req.HolderNodeID, RequestPayload: req.RequestPayload,
				OccurredAt: req.OccurredAt,
			}, bypassRaft)
		}
		if err != nil {
			status := http.StatusBadRequest
			lower := strings.ToLower(err.Error())
			switch {
			case strings.Contains(lower, "running"), strings.Contains(lower, "conflict"),
				strings.Contains(lower, "mismatch"), strings.Contains(lower, "finishing"),
				strings.Contains(lower, "not_releasable"):
				status = http.StatusConflict
			case strings.Contains(lower, "not_found"):
				status = http.StatusNotFound
			case strings.Contains(lower, "not_leader"), strings.Contains(lower, "raft"),
				strings.Contains(lower, "leadership"), strings.Contains(lower, "timeout"):
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, internal.APIResponse[any]{
				Status: "error", Message: "backup_job_operation_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "backup_job_operation_applied",
		})
	}
}
