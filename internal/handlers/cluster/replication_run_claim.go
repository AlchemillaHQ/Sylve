// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ReplicationRunClaimInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil || cS.DB == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "cluster_service_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}

		var decision clusterModels.ReplicationPolicyScheduleDecision
		if err := c.ShouldBindJSON(&decision); err != nil ||
			decision.PolicyID == 0 || decision.ExpectedOwnerEpoch == 0 || decision.DecidedAt.IsZero() ||
			strings.TrimSpace(decision.ClaimToken) == "" ||
			strings.TrimSpace(decision.HolderNodeID) == "" || decision.OccurrenceAt == nil ||
			decision.SetRuntime || decision.LastRunAt != nil ||
			strings.TrimSpace(decision.LastStatus) != "" || strings.TrimSpace(decision.LastError) != "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "exact replication run claim is required",
			})
			return
		}

		bypassRaft, err := cS.RuntimeStateBypassRaft()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "replication_run_claim_failed", Error: err.Error(),
			})
			return
		}
		if err := cS.ApplyReplicationPolicyScheduleDecision(decision, bypassRaft); err != nil {
			c.JSON(replicationRunClaimErrorStatus(err), internal.APIResponse[any]{
				Status: "error", Message: "replication_run_claim_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "replication_run_claim_applied",
		})
	}
}

func replicationRunClaimErrorStatus(err error) int {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "record not found"):
		return http.StatusNotFound
	case strings.Contains(text, "conflict"),
		strings.Contains(text, "already_running"),
		strings.Contains(text, "mismatch"),
		strings.Contains(text, "disabled"),
		strings.Contains(text, "not_runnable"),
		strings.Contains(text, "transition_in_progress"),
		strings.Contains(text, "not_current_voter"):
		return http.StatusConflict
	case strings.Contains(text, "not_leader"),
		strings.Contains(text, "not the leader"),
		strings.Contains(text, "raft"),
		strings.Contains(text, "leadership"),
		strings.Contains(text, "quorum"),
		strings.Contains(text, "timeout"),
		strings.Contains(text, "unavailable"):
		return http.StatusServiceUnavailable
	case strings.Contains(text, "invalid"), strings.Contains(text, "required"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
