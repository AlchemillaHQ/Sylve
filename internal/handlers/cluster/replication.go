// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
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
	"github.com/hashicorp/raft"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type replicationPolicyDeleteCleanupService interface {
	CleanupReplicationPolicyDeleteBestEffort(context.Context, uint, uint64) error
}

type replicationPolicyRunService interface {
	EnqueueReplicationPolicyRun(context.Context, uint) error
}

const replicationEventMaxLimit = 500

type ReplicationFailoverRequest struct {
	TargetNodeID     string `json:"targetNodeId"`
	Mode             string `json:"mode"`
	ConfirmDataLoss  *bool  `json:"confirmDataLoss"`
	MovePinnedSource *bool  `json:"movePinnedSource"`
}

func replicationErrorContains(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func replicationPolicyErrorDetails(operation string, err error) (int, string, string) {
	if err == nil {
		return http.StatusInternalServerError, operation, operation
	}

	errorText := strings.ToLower(err.Error())
	if errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(errorText, "replication_policy_not_found") {
		return http.StatusNotFound, "replication_policy_not_found", "replication_policy_not_found"
	}

	haReasons := cluster.ParseReplicationHAIneligibleReasons(err)
	if len(haReasons) > 0 {
		if cluster.ReplicationHAReasonSetIncludes(haReasons, cluster.ReplicationHAReasonQuorumLost) {
			return http.StatusServiceUnavailable, operation, err.Error()
		}
		return http.StatusConflict, operation, err.Error()
	}

	if replicationErrorContains(errorText,
		"invalid_",
		"_required",
		"_too_long",
		"duplicate_",
		"not_supported",
	) {
		return http.StatusBadRequest, operation, err.Error()
	}

	if replicationErrorContains(errorText,
		"conflict",
		"mismatch",
		"transition",
		"already_",
		"not_runnable",
		"stale",
		"ambiguous",
		"immutable",
		"_deleting",
		"guest_operation",
		"runner_rebind",
		"same_as_owner",
		"target_node_offline",
		"requires_online_owner",
		"no_healthy_target",
		"not_configured_for_policy",
		"no_complete_verified_generation",
		"local_ownership_invalid",
		"_disabled",
		"owner_missing",
		"epoch_exhausted",
	) {
		message := operation
		if strings.Contains(errorText, "transition_already_running") {
			message = "replication_policy_transition_already_running"
		} else if strings.Contains(errorText, "already_running") {
			message = "replication_policy_already_running"
		}
		return http.StatusConflict, message, err.Error()
	}

	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, raft.ErrNotLeader) ||
		errors.Is(err, raft.ErrLeadershipLost) ||
		errors.Is(err, raft.ErrRaftShutdown) ||
		errors.Is(err, raft.ErrEnqueueTimeout) ||
		replicationErrorContains(errorText,
			"unavailable",
			"failed_to_verify_guest",
			"raft_",
			"leader_",
			"not_leader",
			"not the leader",
			"leadership",
			"quorum",
			"timeout",
			"deadline",
			"context canceled",
		) {
		return http.StatusServiceUnavailable, operation, "replication_service_unavailable"
	}
	if strings.Contains(errorText, "_not_found") {
		return http.StatusNotFound, operation, err.Error()
	}

	return http.StatusInternalServerError, operation, operation
}

func writeReplicationPolicyError(c *gin.Context, operation string, err error) {
	status, message, detail := replicationPolicyErrorDetails(operation, err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", operation).Msg("replication_policy_request_failed")
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   detail,
		Data:    nil,
	})
}

func writeReplicationInternalError(c *gin.Context, operation string, err error) {
	logger.L.Error().Err(err).Str("operation", operation).Msg("replication_request_failed")
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status:  "error",
		Message: operation,
		Error:   operation,
		Data:    nil,
	})
}

func parseReplicationEventListQuery(c *gin.Context) (int, uint, string, error) {
	limit := 200
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > replicationEventMaxLimit {
			return 0, 0, "", fmt.Errorf("limit must be between 1 and %d", replicationEventMaxLimit)
		}
		limit = parsed
	}

	policyID := uint(0)
	if raw := strings.TrimSpace(c.Query("policyId")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || parsed == 0 {
			return 0, 0, "", fmt.Errorf("policyId must be a positive integer")
		}
		policyID = uint(parsed)
	}

	return limit, policyID, strings.TrimSpace(c.Query("nodeId")), nil
}

func writeReplicationEventQueryError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: "invalid_replication_event_query",
		Error:   err.Error(),
		Data:    nil,
	})
}

// @Summary List Replication Policies
// @Description List cluster replication policies and their current HA state
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ReplicationPolicy] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/replication/policies [get]
func ReplicationPolicies(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		policies, err := cS.ListReplicationPolicies()
		if err != nil {
			writeReplicationInternalError(c, "list_replication_policies_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ReplicationPolicy]{
			Status:  "success",
			Message: "replication_policies_listed",
			Data:    policies,
		})
	}
}

// @Summary Create a Replication Policy
// @Description Create a replication policy for a VM or jail
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body clusterServiceInterfaces.ReplicationPolicyReq true "Replication Policy Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Guest or Cluster Node Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Leader Forwarding Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Leader Forwarding Timeout"
// @Router /cluster/replication/policies [post]
func CreateReplicationPolicy(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.ReplicationPolicyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		if err := cS.ProposeReplicationPolicyCreate(req, cS.Raft == nil); err != nil {
			writeReplicationPolicyError(c, "create_replication_policy_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_created",
			Data:    nil,
		})
	}
}

// @Summary Update a Replication Policy
// @Description Update one replication policy while preserving its ownership and transition state
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Policy ID"
// @Param request body clusterServiceInterfaces.ReplicationPolicyReq true "Replication Policy Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Policy, Guest, or Cluster Node Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Leader Forwarding Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Leader Forwarding Timeout"
// @Router /cluster/replication/policies/{id} [put]
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
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		if err := cS.ProposeReplicationPolicyUpdate(uint(id64), req, cS.Raft == nil); err != nil {
			writeReplicationPolicyError(c, "update_replication_policy_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_updated",
			Data:    nil,
		})
	}
}

// @Summary Delete a Replication Policy
// @Description Delete a replication policy after fencing it and confirming cluster-wide cleanup
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Policy ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Replication Policy Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Leader Forwarding Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Leader Forwarding Timeout"
// @Router /cluster/replication/policies/{id} [delete]
func DeleteReplicationPolicy(cS *cluster.Service, zS replicationPolicyDeleteCleanupService) gin.HandlerFunc {
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
			writeReplicationPolicyError(c, "delete_replication_policy_failed", policyErr)
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
				writeReplicationPolicyError(c, "mark_replication_policy_deleting_failed", err)
				return
			}
		}

		minimumRaftAppliedIndex := uint64(0)
		if cS.Raft != nil {
			minimumRaftAppliedIndex = cS.Raft.AppliedIndex()
			if minimumRaftAppliedIndex == 0 {
				c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_policy_delete_applied_index_unavailable",
					Error:   "replication_policy_delete_applied_index_unavailable",
					Data:    nil,
				})
				return
			}
		}

		deletingOwnerEpoch := policy.OwnerEpoch
		if cleanupErr := zS.CleanupReplicationPolicyDeleteBestEffort(
			c.Request.Context(),
			uint(id64),
			minimumRaftAppliedIndex,
		); cleanupErr != nil {
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
		if policyErr != nil {
			writeReplicationPolicyError(c, "replication_policy_delete_revalidation_failed", policyErr)
			return
		}
		if policy.ID != uint(id64) || policy.OwnerEpoch != deletingOwnerEpoch ||
			policy.ProtectionState != clusterModels.ReplicationProtectionStateDeleting ||
			replicationPolicyDeleteTransitionInProgress(policy.TransitionState) {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_policy_delete_revalidation_failed",
				Error:   "replication_policy_delete_revalidation_failed",
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeReplicationPolicyDelete(uint(id64), cS.Raft == nil); err != nil {
			writeReplicationPolicyError(c, "delete_replication_policy_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_deleted",
			Data:    nil,
		})
	}
}

func replicationPolicyDeleteTransitionInProgress(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup,
		clusterModels.ReplicationTransitionStatePromoting,
		clusterModels.ReplicationTransitionStateRollingBack:
		return true
	default:
		return false
	}
}

// @Summary Run a Replication Policy Now
// @Description Queue an immediate run for a replication policy on its active node
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Policy ID"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Replication Policy Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/replication/policies/{id}/run [post]
func RunReplicationPolicyNow(cS *cluster.Service, zS replicationPolicyRunService) gin.HandlerFunc {
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
			writeReplicationPolicyError(c, "replication_policy_lookup_failed", err)
			return
		}

		localNodeID := cS.LocalNodeID()
		runNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if runNodeID == "" {
			runNodeID = strings.TrimSpace(policy.SourceNodeID)
		}

		if runNodeID != "" && localNodeID != "" && runNodeID != localNodeID {
			response, err := forwardReplicationRunToNode(c, cS, uint(id64), runNodeID)
			if err != nil {
				writeClusterForwardError(c, "replication_run_remote_forward_failed", err)
				return
			}
			writeClusterForwardResponse(c, response)
			return
		}

		if runNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		if err := zS.EnqueueReplicationPolicyRun(c.Request.Context(), policy.ID); err != nil {
			writeReplicationPolicyError(c, "replication_policy_enqueue_failed", err)
			return
		}

		c.Set("AuditAsyncJobID", policy.ID)
		c.Set("AuditAsyncJobType", "replication_policy_run")

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_run_started",
			Data:    nil,
		})
	}
}

func replicationPolicyEnqueueErrorResponse(err error) (int, string) {
	status, message, _ := replicationPolicyErrorDetails("replication_policy_enqueue_failed", err)
	return status, message
}

// @Summary Fail Over a Replication Policy
// @Description Queue a safe or forced failover for a replication policy
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Policy ID"
// @Param request body clusterHandlers.ReplicationFailoverRequest false "Failover Options"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Replication Policy or Cluster Node Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Leader Forwarding Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Leader Forwarding Timeout"
// @Router /cluster/replication/policies/{id}/failover [post]
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
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_service_unavailable",
				Error:   "replication_service_unavailable",
				Data:    nil,
			})
			return
		}

		var req ReplicationFailoverRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			writeClusterJSONBindError(c, err, "invalid_request")
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
			writeReplicationPolicyError(c, "failover_replication_policy_failed", err)
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

func forwardReplicationRunToNode(
	c *gin.Context,
	cS *cluster.Service,
	policyID uint,
	nodeID string,
) (clusterForwardResponse, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return clusterForwardResponse{}, err
	}

	runURL := fmt.Sprintf("https://%s/api/cluster/replication/policies/%d/run", targetAPI, policyID)
	return performClusterForward(
		c,
		cS,
		http.MethodPost,
		runURL,
		[]byte(`{}`),
		clusterForwardDurable,
	)
}

// @Summary List Replication Events
// @Description List replication events from the local or selected cluster node
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Maximum events to return (1-500)" default(200)
// @Param policyId query int false "Replication Policy ID"
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[[]clusterModels.ReplicationEvent] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/replication/events [get]
func ReplicationEvents(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, policyID, requestedNodeID, err := parseReplicationEventListQuery(c)
		if err != nil {
			writeReplicationEventQueryError(c, err)
			return
		}

		if shouldForwardReplicationEventsRequest(cS, requestedNodeID) {
			response, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, "/api/cluster/replication/events")
			if err != nil {
				writeBackupNodeForwardError(
					c, "replication_events_remote_forward_failed", "replication_events_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		events, err := cS.ListReplicationEvents(limit, policyID)
		if err != nil {
			writeReplicationInternalError(c, "list_replication_events_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ReplicationEvent]{
			Status:  "success",
			Message: "replication_events_listed",
			Data:    events,
		})
	}
}

// @Summary Get a Replication Event
// @Description Get one local or transition replication event from the local or selected cluster node
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Event ID"
// @Param scope query string false "Event scope" Enums(local,transition)
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[clusterModels.ReplicationEvent] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Replication Event or Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/replication/events/{id} [get]
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

		scope := strings.TrimSpace(c.Query("scope"))
		if scope != "" && scope != clusterModels.ReplicationEventScopeLocal &&
			scope != clusterModels.ReplicationEventScopeTransition {
			writeReplicationEventQueryError(c, fmt.Errorf("scope must be local or transition"))
			return
		}

		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardReplicationEventsRequest(cS, requestedNodeID) {
			path := fmt.Sprintf("/api/cluster/replication/events/%d", id64)
			response, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				writeBackupNodeForwardError(
					c, "replication_event_remote_forward_failed", "replication_event_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		var event *clusterModels.ReplicationEvent
		if scope == "" {
			event, err = cS.GetReplicationEventByID(uint(id64))
		} else {
			event, err = cS.GetReplicationEventByScopedID(uint(id64), scope)
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_not_found",
					Error:   "replication_event_not_found",
					Data:    nil,
				})
				return
			}

			writeReplicationInternalError(c, "get_replication_event_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterModels.ReplicationEvent]{
			Status:  "success",
			Message: "replication_event_fetched",
			Data:    event,
		})
	}
}

// @Summary Get Replication Event Progress
// @Description Get live progress for a replication event from the local or selected cluster node
// @Tags Cluster Replication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Replication Event ID"
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[zelta.ReplicationEventProgress] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Replication Event or Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/replication/events/{id}/progress [get]
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
			response, err := forwardReplicationEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				writeBackupNodeForwardError(
					c, "replication_event_progress_remote_forward_failed", "replication_event_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "replication_event_not_found",
					Error:   "replication_event_not_found",
					Data:    nil,
				})
				return
			}
			writeReplicationInternalError(c, "get_replication_event_progress_failed", err)
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
		if strings.TrimSpace(req.NodeUUID) == "" ||
			strings.TrimSpace(req.NodeUUID) != strings.TrimSpace(c.GetString("IssuerNodeID")) {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status: "error", Message: "cluster_ssh_identity_issuer_mismatch",
				Error: "cluster_ssh_identity_issuer_mismatch", Data: nil,
			})
			return
		}
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

func RunReplicationPolicyInternal(cS *cluster.Service, zS replicationPolicyRunService) gin.HandlerFunc {
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
			status, msg := replicationPolicyEnqueueErrorResponse(err)
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

func PrepareBackupJobRunnerRebindInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil || zS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "backup_job_runner_rebind_unavailable", Error: "cluster_service_unavailable",
			})
			return
		}
		var req struct {
			GuestType       string `json:"guestType"`
			GuestID         uint   `json:"guestId"`
			NewRunnerNodeID string `json:"newRunnerNodeId"`
			OperationToken  string `json:"operationToken"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.GuestID == 0 ||
			strings.TrimSpace(req.GuestType) == "" || strings.TrimSpace(req.NewRunnerNodeID) == "" ||
			strings.TrimSpace(req.OperationToken) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: "guestType, guestId, newRunnerNodeId, and operationToken are required",
			})
			return
		}
		if err := zS.PrepareGuestBackupJobRunnerRebind(
			c.Request.Context(), req.GuestType, req.GuestID, req.NewRunnerNodeID, req.OperationToken,
		); err != nil {
			c.JSON(replicationControlErrorStatus(err), internal.APIResponse[any]{
				Status: "error", Message: "prepare_backup_job_runner_rebind_failed", Error: err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "backup_job_runner_rebind_prepared",
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
			OwnershipOnly  bool   `json:"ownership_only"`
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

		var err error
		if req.OwnershipOnly {
			err = zS.MoveGuestIdentityOwner(
				c.Request.Context(), req.GuestType, req.GuestID, req.NewOwnerNodeID, req.OperationToken,
			)
		} else {
			err = zS.MigrateGuestOwnership(
				c.Request.Context(), req.GuestType, req.GuestID, req.NewOwnerNodeID, req.OperationToken,
			)
		}
		if err != nil {
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
		var req clusterServiceInterfaces.ReplicationPolicyDeleteCleanupRequest
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
		if cS == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "cleanup_replication_policy_delete_unavailable",
				Error:   "cluster_service_unavailable",
				Data:    nil,
			})
			return
		}
		if cS.Raft != nil && req.MinimumRaftAppliedIndex == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "minimumRaftAppliedIndex is required in clustered mode",
				Data:    nil,
			})
			return
		}
		if _, err := cS.WaitForReplicatedStateAppliedIndex(
			c.Request.Context(),
			req.MinimumRaftAppliedIndex,
		); err != nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "cleanup_replication_policy_delete_applied_index_unavailable",
				Error:   fmt.Sprintf("replication_policy_delete_applied_index_wait_failed: %v", err),
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

		bypassRaft, authorityErr := cS.RuntimeStateBypassRaft()
		if authorityErr != nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "update_replication_policy_state_failed", Error: authorityErr.Error(),
			})
			return
		}
		if err := cS.ProposeReplicationPolicyStateUpdate(req, bypassRaft); err != nil {
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

func forwardReplicationEventsRequestToNode(
	c *gin.Context,
	cS *cluster.Service,
	nodeID string,
	path string,
) (clusterForwardResponse, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return clusterForwardResponse{}, err
	}

	query := c.Request.URL.Query()
	query.Del("nodeId")

	remoteURL := fmt.Sprintf("https://%s%s", targetAPI, path)
	if encoded := query.Encode(); encoded != "" {
		remoteURL += "?" + encoded
	}

	return performClusterForward(
		c,
		cS,
		http.MethodGet,
		remoteURL,
		nil,
		clusterForwardShortRead,
	)
}
