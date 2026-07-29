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

func BackupTargetRestoreOperationInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Action             string    `json:"action"`
			Token              string    `json:"token"`
			TargetID           uint      `json:"targetId"`
			HolderNodeID       string    `json:"holderNodeId"`
			DestinationDataset string    `json:"destinationDataset"`
			RequestPayload     string    `json:"requestPayload"`
			OccurredAt         time.Time `json:"occurredAt"`
		}
		if cS == nil || cS.DB == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "cluster_service_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.TargetID == 0 ||
			strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.HolderNodeID) == "" ||
			strings.TrimSpace(req.DestinationDataset) == "" || strings.TrimSpace(req.RequestPayload) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request",
				Error: "targetId, token, holderNodeId, destinationDataset, and requestPayload are required",
			})
			return
		}

		action := strings.ToLower(strings.TrimSpace(req.Action))
		var err error
		if action == "acquire" {
			err = cS.AcquireBackupTargetRestoreOperation(clusterModels.BackupTargetRestoreOperationAcquire{
				Token: req.Token, TargetID: req.TargetID, HolderNodeID: req.HolderNodeID,
				DestinationDataset: req.DestinationDataset, RequestPayload: req.RequestPayload,
				AcquiredAt: req.OccurredAt,
			}, false)
		} else {
			err = cS.TransitionBackupTargetRestoreOperation(action, clusterModels.BackupTargetRestoreOperationTransition{
				Token: req.Token, TargetID: req.TargetID, HolderNodeID: req.HolderNodeID,
				DestinationDataset: req.DestinationDataset, RequestPayload: req.RequestPayload,
				OccurredAt: req.OccurredAt,
			}, false)
		}
		if err != nil {
			status := http.StatusBadRequest
			lower := strings.ToLower(err.Error())
			switch {
			case strings.Contains(lower, "reserved"), strings.Contains(lower, "already_started"),
				strings.Contains(lower, "already_completed"), strings.Contains(lower, "mismatch"),
				strings.Contains(lower, "finishing"),
				strings.Contains(lower, "not_abortable"), strings.Contains(lower, "not_finishable"),
				strings.Contains(lower, "not_releasable"), strings.Contains(lower, "conflict"):
				status = http.StatusConflict
			case strings.Contains(lower, "not_found"):
				status = http.StatusNotFound
			case strings.Contains(lower, "not_leader"), strings.Contains(lower, "raft"),
				strings.Contains(lower, "leadership"), strings.Contains(lower, "timeout"):
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_restore_operation_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "backup_target_restore_operation_applied",
		})
	}
}
