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
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ReplicationPolicies(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		policies, err := cS.ListReplicationPolicies()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_replication_policies_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ReplicationPolicy]{
			Status:  "success",
			Message: "replication_policies_listed",
			Data:    policies,
		})
	}
}

func CreateReplicationPolicy(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.ReplicationPolicyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeReplicationPolicyCreate(req, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "create_replication_policy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_created",
			Data:    nil,
		})
	}
}

func UpdateReplicationPolicy(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_policy_id",
				Error:   "invalid_policy_id",
				Data:    nil,
			})
			return
		}

		if zS != nil && zS.IsPolicyTransitionRunning(uint(id64)) {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "policy_transition_in_progress",
				Error:   "cannot_update_policy_during_failover",
				Data:    nil,
			})
			return
		}

		var req clusterServiceInterfaces.ReplicationPolicyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeReplicationPolicyUpdate(uint(id64), req, cS.Raft == nil); err != nil {
			status := http.StatusBadRequest
			message := "update_replication_policy_failed"
			if strings.Contains(strings.ToLower(err.Error()), "transition_in_progress") {
				status = http.StatusConflict
				message = "policy_transition_in_progress"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_updated",
			Data:    nil,
		})
	}
}

func DeleteReplicationPolicy(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_policy_id",
				Error:   "invalid_policy_id",
				Data:    nil,
			})
			return
		}

		// Policy metadata must remain present until every node has acknowledged
		// cleanup. Without the replication service there is no safe way to make
		// that guarantee, so fail before moving the policy into deleting.
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_delete_cleanup_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		policy, policyErr := cS.GetReplicationPolicyByID(uint(id64))
		if policyErr != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status: "error", Message: "replication_policy_not_found", Error: policyErr.Error(), Data: nil,
			})
			return
		}
		switch strings.ToLower(strings.TrimSpace(policy.TransitionState)) {
		case clusterModels.ReplicationTransitionStateDemoting,
			clusterModels.ReplicationTransitionStateCatchup,
			clusterModels.ReplicationTransitionStatePromoting,
			clusterModels.ReplicationTransitionStateRollingBack:
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "policy_transition_in_progress", Error: "cannot_delete_policy_during_failover", Data: nil,
			})
			return
		}
		if policy.ProtectionState != clusterModels.ReplicationProtectionStateDeleting {
			if err := cS.UpdateReplicationPolicyProtectionState(
				policy.ID,
				policy.OwnerEpoch,
				clusterModels.ReplicationProtectionStateDeleting,
				cS.Raft == nil,
			); err != nil {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status: "error", Message: "mark_replication_policy_deleting_failed", Error: err.Error(), Data: nil,
				})
				return
			}
		}

		deletingOwnerEpoch := policy.OwnerEpoch
		if cleanupErr := zS.CleanupReplicationPolicyDeleteBestEffort(c.Request.Context(), uint(id64)); cleanupErr != nil {
			logger.L.Warn().
				Uint("policy_id", uint(id64)).
				Uint64("owner_epoch", deletingOwnerEpoch).
				Err(cleanupErr).
				Msg("replication_policy_delete_cleanup_incomplete")
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_delete_cleanup_incomplete",
				Error:   cleanupErr.Error(),
				Data:    nil,
			})
			return
		}

		// Revalidate immediately before deleting the durable policy. This keeps
		// a stale cleanup acknowledgement from authorizing deletion after an
		// ownership epoch or lifecycle change.
		policy, policyErr = cS.GetReplicationPolicyByID(uint(id64))
		if policyErr != nil || policy.OwnerEpoch != deletingOwnerEpoch ||
			policy.ProtectionState != clusterModels.ReplicationProtectionStateDeleting {
			revalidationErr := "replication_policy_delete_revalidation_failed"
			if policyErr != nil {
				revalidationErr = policyErr.Error()
			}
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_delete_revalidation_failed",
				Error:   revalidationErr,
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeReplicationPolicyDelete(uint(id64), cS.Raft == nil); err != nil {
			status := http.StatusInternalServerError
			lowerErr := strings.ToLower(err.Error())
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(lowerErr, "record not found"):
				status = http.StatusNotFound
			case strings.Contains(lowerErr, "transition"),
				strings.Contains(lowerErr, "not_deleting"),
				strings.Contains(lowerErr, "cas_conflict"):
				status = http.StatusConflict
			case strings.Contains(lowerErr, "not_leader"),
				strings.Contains(lowerErr, "not the leader"),
				strings.Contains(lowerErr, "leadership"),
				strings.Contains(lowerErr, "quorum"),
				strings.Contains(lowerErr, "timeout"):
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "delete_replication_policy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_deleted",
			Data:    nil,
		})
	}
}

func RunReplicationPolicyNow(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_policy_id",
				Error:   "invalid_policy_id",
				Data:    nil,
			})
			return
		}

		policy, err := cS.GetReplicationPolicyByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		localNodeID := cS.LocalNodeID()
		runNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if runNodeID == "" {
			runNodeID = strings.TrimSpace(policy.SourceNodeID)
		}

		if runNodeID != "" && localNodeID != "" && runNodeID != localNodeID {
			body, statusCode, err := forwardReplicationRunToNode(c, cS, uint(id64), runNodeID)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_run_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			c.Data(statusCode, "application/json", body)
			return
		}

		if runNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if err := zS.EnqueueReplicationPolicyRun(c.Request.Context(), policy.ID); err != nil {
			status := http.StatusBadRequest
			msg := "replication_policy_enqueue_failed"
			if strings.Contains(err.Error(), "already_running") {
				status = http.StatusConflict
				msg = "replication_policy_already_running"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.Set("AuditAsyncJobID", policy.ID)
		c.Set("AuditAsyncJobType", "replication_policy_run")

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_run_started",
			Data:    nil,
		})
	}
}

func FailoverReplicationPolicy(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_policy_id",
				Error:   "invalid_policy_id",
				Data:    nil,
			})
			return
		}
		if zS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		var req struct {
			TargetNodeID     string `json:"targetNodeId"`
			Mode             string `json:"mode"`
			ConfirmDataLoss  *bool  `json:"confirmDataLoss"`
			MovePinnedSource *bool  `json:"movePinnedSource"`
		}
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		mode := strings.ToLower(strings.TrimSpace(req.Mode))
		if mode == "" {
			mode = "safe"
		}
		if mode != "safe" && mode != "force" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_failover_mode",
				Error:   "mode must be safe or force",
				Data:    nil,
			})
			return
		}

		confirmDataLoss := req.ConfirmDataLoss != nil && *req.ConfirmDataLoss
		movePinnedSource := true
		if req.MovePinnedSource != nil {
			movePinnedSource = *req.MovePinnedSource
		}

		if err := zS.EnqueueReplicationPolicyFailover(
			uint(id64),
			strings.TrimSpace(req.TargetNodeID),
			mode,
			confirmDataLoss,
			movePinnedSource,
		); err != nil {
			statusCode := http.StatusBadRequest
			message := "failover_replication_policy_failed"
			lowerErr := strings.ToLower(err.Error())
			if strings.Contains(lowerErr, "transition_already_running") {
				statusCode = http.StatusConflict
				message = "replication_policy_transition_already_running"
			} else if strings.Contains(lowerErr, "not_leader") {
				statusCode = http.StatusConflict
				message = "not_leader"
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.Set("AuditAsyncJobID", uint(id64))
		c.Set("AuditAsyncJobType", "replication_policy_failover")

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_failover_queued",
			Data:    nil,
		})
	}
}

func forwardReplicationRunToNode(c *gin.Context, cS *cluster.Service, policyID uint, nodeID string) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return nil, 0, err
	}

	userID := c.GetUint("UserID")
	username := strings.TrimSpace(c.GetString("Username"))
	authType := strings.TrimSpace(c.GetString("AuthType"))
	if username == "" {
		hostname, _ := utils.GetSystemHostname()
		if hostname != "" {
			username = hostname
		} else {
			username = "cluster"
		}
	}
	if authType == "" {
		authType = "local"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(userID, username, authType, "")
	if err != nil {
		return nil, 0, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	runURL := fmt.Sprintf("https://%s/api/cluster/replication/policies/%d/run", targetAPI, policyID)
	body, statusCode, err := utils.HTTPPostJSONRead(runURL, map[string]any{}, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	return body, statusCode, nil
}

func ReplicationEvents(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardReplicationEventsRequest(cS, requestedNodeID) {
			body, statusCode, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, "/api/cluster/replication/events")
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_events_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		limit := 200
		if q := c.Query("limit"); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil {
				limit = parsed
			}
		}

		policyID := uint(0)
		if q := c.Query("policyId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				policyID = uint(parsed)
			}
		}

		events, err := cS.ListReplicationEvents(limit, policyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_replication_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ReplicationEvent]{
			Status:  "success",
			Message: "replication_events_listed",
			Data:    events,
		})
	}
}

func ReplicationEventByID(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_event_id",
				Error:   "invalid_event_id",
				Data:    nil,
			})
			return
		}

		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardReplicationEventsRequest(cS, requestedNodeID) {
			path := fmt.Sprintf("/api/cluster/replication/events/%d", id64)
			body, statusCode, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		event, err := cS.GetReplicationEventByID(uint(id64))
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_not_found",
					Error:   "replication_event_not_found",
					Data:    nil,
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "get_replication_event_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterModels.ReplicationEvent]{
			Status:  "success",
			Message: "replication_event_fetched",
			Data:    event,
		})
	}
}

func ReplicationEventProgressByID(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_event_id",
				Error:   "invalid_event_id",
				Data:    nil,
			})
			return
		}

		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardReplicationEventsRequest(cS, requestedNodeID) {
			path := fmt.Sprintf("/api/cluster/replication/events/%d/progress", id64)
			body, statusCode, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_progress_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		progress, err := zS.GetReplicationEventProgress(c.Request.Context(), uint(id64))
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_not_found",
					Error:   "replication_event_not_found",
					Data:    nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "get_replication_event_progress_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.ReplicationEventProgress]{
			Status:  "success",
			Message: "replication_event_progress_fetched",
			Data:    progress,
		})
	}
}

func UpsertClusterSSHIdentityInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterModels.ClusterSSHIdentity
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.UpsertClusterSSHIdentity(req, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "upsert_cluster_ssh_identity_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_ssh_identity_upserted",
			Data:    nil,
		})
	}
}

func ReconcileClusterSSHNow(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cS.EnsureAndPublishLocalSSHIdentity(); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "reconcile_cluster_ssh_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[map[string]any]{
			Status:  "success",
			Message: "cluster_ssh_reconciled",
			Data: map[string]any{
				"at": time.Now().UTC(),
			},
		})
	}
}

type replicationRuntimeStateRequest struct {
	PolicyID        uint   `json:"policyId"`
	OwnerEpoch      uint64 `json:"ownerEpoch"`
	TransitionRunID string `json:"transitionRunId"`
}

type replicationActivateRequest struct {
	PolicyID        uint   `json:"policyId"`
	OwnerEpoch      uint64 `json:"ownerEpoch"`
	TransitionRunID string `json:"transitionRunId"`
	DesiredRunning  *bool  `json:"desiredRunning"`
}

type replicationDemoteRequest struct {
	PolicyID        uint   `json:"policyId"`
	OwnerEpoch      uint64 `json:"ownerEpoch"`
	TransitionRunID string `json:"transitionRunId"`
}

type replicationCatchupRequest struct {
	PolicyID        uint   `json:"policyId"`
	TargetNodeID    string `json:"targetNodeId"`
	OwnerEpoch      uint64 `json:"ownerEpoch"`
	TransitionRunID string `json:"transitionRunId"`
	GenerationID    string `json:"generationId"`
}

func replicationControlErrorStatus(err error) int {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	lowerErr := strings.ToLower(err.Error())
	if strings.Contains(lowerErr, "mismatch") ||
		strings.Contains(lowerErr, "conflict") ||
		strings.Contains(lowerErr, "stale") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func ReplicationPolicyRuntimeStateInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req replicationRuntimeStateRequest
		if err := c.ShouldBindJSON(&req); err != nil ||
			req.PolicyID == 0 || req.OwnerEpoch == 0 || strings.TrimSpace(req.TransitionRunID) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId, ownerEpoch, and transitionRunId are required",
				Data:    nil,
			})
			return
		}
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		running, err := zS.ReplicationPolicyRuntimeState(
			c.Request.Context(),
			req.PolicyID,
			req.OwnerEpoch,
			strings.TrimSpace(req.TransitionRunID),
		)
		if err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_runtime_state_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[map[string]bool]{
			Status:  "success",
			Message: "replication_policy_runtime_state_fetched",
			Data: map[string]bool{
				"running": running,
			},
		})
	}
}

func ActivateReplicationPolicyInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req replicationActivateRequest
		if err := c.ShouldBindJSON(&req); err != nil ||
			req.PolicyID == 0 || req.OwnerEpoch == 0 || strings.TrimSpace(req.TransitionRunID) == "" ||
			req.DesiredRunning == nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId, ownerEpoch, transitionRunId, and desiredRunning are required",
				Data:    nil,
			})
			return
		}
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		if err := zS.ActivateReplicationPolicyForTransition(
			c.Request.Context(),
			req.PolicyID,
			req.OwnerEpoch,
			strings.TrimSpace(req.TransitionRunID),
			req.DesiredRunning,
		); err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "activate_replication_policy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_activated",
			Data:    nil,
		})
	}
}

func RunReplicationPolicyInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if zS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		var req struct {
			PolicyID uint `json:"policyId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.PolicyID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId is required",
				Data:    nil,
			})
			return
		}

		if err := zS.EnqueueReplicationPolicyRun(c.Request.Context(), req.PolicyID); err != nil {
			status := http.StatusBadRequest
			msg := "replication_policy_enqueue_failed"
			if strings.Contains(strings.ToLower(err.Error()), "already_running") {
				status = http.StatusConflict
				msg = "replication_policy_already_running"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_run_started",
			Data:    nil,
		})
	}
}

func DemoteReplicationPolicyInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req replicationDemoteRequest
		if err := c.ShouldBindJSON(&req); err != nil ||
			req.PolicyID == 0 || req.OwnerEpoch == 0 || strings.TrimSpace(req.TransitionRunID) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId, ownerEpoch, and transitionRunId are required",
				Data:    nil,
			})
			return
		}
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		if err := zS.DemoteReplicationPolicyForTransition(
			c.Request.Context(),
			req.PolicyID,
			req.OwnerEpoch,
			strings.TrimSpace(req.TransitionRunID),
		); err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "demote_replication_policy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_demoted",
			Data:    nil,
		})
	}
}

func ReassignReplicationOwnerInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "replication_service_unavailable", Error: "replication_service_unavailable",
			})
			return
		}
		var req struct {
			GuestType      string `json:"guest_type"`
			GuestID        uint   `json:"guest_id"`
			NewOwnerNodeID string `json:"new_owner_node_id"`
			OperationToken string `json:"operation_token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.GuestID == 0 ||
			strings.TrimSpace(req.NewOwnerNodeID) == "" || strings.TrimSpace(req.OperationToken) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "guest_id and new_owner_node_id are required",
				Data:    nil,
			})
			return
		}

		if err := zS.MigrateGuestOwnership(
			c.Request.Context(), req.GuestType, req.GuestID, req.NewOwnerNodeID, req.OperationToken,
		); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "reassign_replication_owner_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_owner_reassigned",
			Data:    nil,
		})
	}
}

func ReplicationGuestOperationInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Action       string `json:"action"`
			GuestType    string `json:"guestType"`
			GuestID      uint   `json:"guestId"`
			Operation    string `json:"operation"`
			Token        string `json:"token"`
			OwnerNodeID  string `json:"ownerNodeId"`
			TargetNodeID string `json:"targetNodeId"`
			TaskID       uint   `json:"taskId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.GuestID == 0 ||
			strings.TrimSpace(req.GuestType) == "" || strings.TrimSpace(req.Token) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "guestType, guestId, and token are required",
			})
			return
		}
		if cS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "cluster_service_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}

		action := strings.ToLower(strings.TrimSpace(req.Action))
		operation := strings.ToLower(strings.TrimSpace(req.Operation))
		var err error
		switch action {
		case "acquire":
			err = cS.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
				GuestType: req.GuestType, GuestID: req.GuestID, Operation: operation,
				Token: req.Token, OwnerNodeID: req.OwnerNodeID, TargetNodeID: req.TargetNodeID, TaskID: req.TaskID,
			}, false)
		case "seal", "abort", "complete":
			payload := clusterModels.ReplicationGuestOperationTransition{
				GuestType: req.GuestType, GuestID: req.GuestID, Operation: operation,
				Token: req.Token, TargetNodeID: req.TargetNodeID,
			}
			switch action {
			case "seal":
				err = cS.SealReplicationGuestOperation(payload, false)
			case "abort":
				err = cS.AbortReplicationGuestOperation(payload, false)
			case "complete":
				err = cS.CompleteReplicationGuestOperation(payload, false)
			}
		default:
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "invalid replication guest operation action",
			})
			return
		}
		if err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status: "error", Message: "replication_guest_operation_failed", Error: err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "replication_guest_operation_applied",
		})
	}
}

func ReplicationGuestOperationStatusInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GuestType    string `json:"guestType"`
			GuestID      uint   `json:"guestId"`
			Operation    string `json:"operation"`
			State        string `json:"state"`
			Token        string `json:"token"`
			TargetNodeID string `json:"targetNodeId"`
		}
		if cS == nil || cS.DB == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "cluster_service_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.GuestID == 0 ||
			strings.TrimSpace(req.GuestType) == "" || strings.TrimSpace(req.Token) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "guestType, guestId, and token are required",
			})
			return
		}
		var operation clusterModels.ReplicationGuestOperation
		err := cS.DB.Where("guest_type = ? AND guest_id = ?", strings.TrimSpace(req.GuestType), req.GuestID).
			First(&operation).Error
		if err != nil || strings.TrimSpace(operation.Operation) != strings.TrimSpace(req.Operation) ||
			strings.TrimSpace(operation.State) != strings.TrimSpace(req.State) ||
			strings.TrimSpace(operation.Token) != strings.TrimSpace(req.Token) ||
			strings.TrimSpace(operation.TargetNodeID) != strings.TrimSpace(req.TargetNodeID) {
			detail := "replication_guest_operation_not_applied"
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				detail = err.Error()
			}
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "replication_guest_operation_not_applied", Error: detail,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "replication_guest_operation_applied",
		})
	}
}

func CatchupReplicationPolicyInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req replicationCatchupRequest
		if err := c.ShouldBindJSON(&req); err != nil ||
			req.PolicyID == 0 || req.OwnerEpoch == 0 ||
			strings.TrimSpace(req.TargetNodeID) == "" ||
			strings.TrimSpace(req.TransitionRunID) == "" ||
			strings.TrimSpace(req.GenerationID) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId, targetNodeId, ownerEpoch, transitionRunId, and generationId are required",
				Data:    nil,
			})
			return
		}
		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		if err := zS.CatchupReplicationPolicyToNodeForTransition(
			c.Request.Context(),
			req.PolicyID,
			strings.TrimSpace(req.TargetNodeID),
			req.OwnerEpoch,
			strings.TrimSpace(req.TransitionRunID),
			strings.TrimSpace(req.GenerationID),
		); err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "catchup_replication_policy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_catchup_completed",
			Data:    nil,
		})
	}
}

func UpdateReplicationTargetReadinessInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_service_unavailable",
				Error:   "cluster_service_unavailable",
				Data:    nil,
			})
			return
		}
		// Forward before binding so the leader receives the untouched request body.
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterModels.ReplicationTargetReadinessUpdate
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.UpdateReplicationTargetReadiness(req, cS.Raft == nil); err != nil {
			status := http.StatusBadRequest
			message := "update_replication_target_readiness_failed"
			lowerErr := strings.ToLower(err.Error())
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(lowerErr, "record not found"):
				status = http.StatusNotFound
				message = "replication_target_readiness_not_found"
			case strings.Contains(lowerErr, "cas_conflict") || strings.Contains(lowerErr, "stale"):
				status = http.StatusConflict
				message = "replication_target_readiness_conflict"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_target_readiness_updated",
			Data:    nil,
		})
	}
}

func CleanupReplicationPolicyDeleteInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PolicyID           uint   `json:"policyId"`
			ExpectedOwnerEpoch uint64 `json:"expectedOwnerEpoch"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.PolicyID == 0 || req.ExpectedOwnerEpoch == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policyId and expectedOwnerEpoch are required",
				Data:    nil,
			})
			return
		}

		if zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "cleanup_replication_policy_delete_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		if err := zS.CleanupReplicationPolicyDeleteLocalBestEffort(
			c.Request.Context(),
			req.PolicyID,
			req.ExpectedOwnerEpoch,
		); err != nil {
			status := http.StatusInternalServerError
			lowerErr := strings.ToLower(err.Error())
			switch {
			case strings.Contains(lowerErr, "delete_cleanup_quiescing"):
				status = http.StatusServiceUnavailable
			case strings.Contains(lowerErr, "delete_authority"),
				strings.Contains(lowerErr, "policy_not_deleting"):
				status = http.StatusConflict
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "cleanup_replication_policy_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_delete_cleanup_completed",
			Data:    nil,
		})
	}
}

func EnqueueFailoverInternal(zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PolicyID         uint   `json:"policy_id"`
			TargetNodeID     string `json:"target_node_id"`
			Mode             string `json:"mode"`
			ConfirmDataLoss  bool   `json:"confirm_data_loss"`
			MovePinnedSource bool   `json:"move_pinned_source"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.PolicyID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "policy_id is required",
				Data:    nil,
			})
			return
		}

		if err := zS.EnqueueReplicationPolicyFailover(req.PolicyID, req.TargetNodeID, req.Mode, req.ConfirmDataLoss, req.MovePinnedSource); err != nil {
			statusCode := http.StatusInternalServerError
			message := "enqueue_failover_failed"
			lowerErr := strings.ToLower(err.Error())
			switch {
			case strings.Contains(lowerErr, "invalid_policy_id"):
				statusCode = http.StatusBadRequest
			case strings.Contains(lowerErr, "not_found") || strings.Contains(lowerErr, "record not found"):
				statusCode = http.StatusNotFound
			case strings.Contains(lowerErr, "transition_already_running"):
				statusCode = http.StatusConflict
			case strings.Contains(lowerErr, "not_leader"):
				statusCode = http.StatusConflict
			case strings.Contains(lowerErr, "confirm_data_loss_required"):
				statusCode = http.StatusBadRequest
			case strings.Contains(lowerErr, "cluster_service_unavailable"):
				statusCode = http.StatusServiceUnavailable
			case strings.Contains(lowerErr, "ha_ineligible"):
				statusCode = http.StatusBadRequest
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "failover_enqueued",
			Data:    nil,
		})
	}
}

func UpdateReplicationPolicyStateInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req cluster.ReplicationPolicyRuntimeState
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeReplicationPolicyStateUpdate(req, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "update_replication_policy_state_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_state_updated",
			Data:    nil,
		})
	}
}

func shouldForwardReplicationEventsRequest(cS *cluster.Service, requestedNodeID string) bool {
	requestedNodeID = strings.TrimSpace(requestedNodeID)
	if requestedNodeID == "" || cS == nil {
		return false
	}

	localNodeID := ""
	if detail := cS.Detail(); detail != nil {
		localNodeID = strings.TrimSpace(detail.NodeID)
	}

	return localNodeID == "" || requestedNodeID != localNodeID
}

func forwardReplicationEventsRequestToNode(c *gin.Context, cS *cluster.Service, nodeID, path string) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return nil, 0, err
	}

	userID := c.GetUint("UserID")
	username := strings.TrimSpace(c.GetString("Username"))
	authType := strings.TrimSpace(c.GetString("AuthType"))
	if username == "" {
		hostname, _ := utils.GetSystemHostname()
		if hostname != "" {
			username = hostname
		} else {
			username = "cluster"
		}
	}
	if authType == "" {
		authType = "local"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(userID, username, authType, "")
	if err != nil {
		return nil, 0, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	query := c.Request.URL.Query()
	query.Del("nodeId")

	remoteURL := fmt.Sprintf("https://%s%s", targetAPI, path)
	if encoded := query.Encode(); encoded != "" {
		remoteURL += "?" + encoded
	}

	body, statusCode, err := utils.HTTPGetJSONRead(remoteURL, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	return body, statusCode, nil
}
