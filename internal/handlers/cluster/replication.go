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
	CleanupReplicationPolicyDeleteBestEffort(context.Context, uint) error
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
		var req clusterServiceInterfaces.ReplicationPolicyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		if err := cS.ProposeReplicationPolicyCreate(req); err != nil {
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

		if err := cS.ProposeReplicationPolicyUpdate(uint(id64), req); err != nil {
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
			); err != nil {
				writeReplicationPolicyError(c, "mark_replication_policy_deleting_failed", err)
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

		if err := cS.ProposeReplicationPolicyDelete(uint(id64)); err != nil {
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

		runNodeID := strings.TrimSpace(policy.ActiveNodeID)
		if runNodeID == "" {
			runNodeID = strings.TrimSpace(policy.SourceNodeID)
		}

		if forOtherNode(cS, runNodeID) {
			ForwardToNode(c, cS, runNodeID, nil)
			return
		}
		if runNodeID == "" && cS.Raft() != nil && cS.Raft().State() != raft.Leader {
			forwardToLeader(c, cS, nil)
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

		if forOtherNode(cS, requestedNodeID) {
			ForwardToNode(c, cS, requestedNodeID, nil)
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
		if forOtherNode(cS, requestedNodeID) {
			ForwardToNode(c, cS, requestedNodeID, nil)
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
		if forOtherNode(cS, requestedNodeID) {
			ForwardToNode(c, cS, requestedNodeID, nil)
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
