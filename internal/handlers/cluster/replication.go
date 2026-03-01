// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
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

func UpdateReplicationPolicy(cS *cluster.Service) gin.HandlerFunc {
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
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "update_replication_policy_failed",
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

func DeleteReplicationPolicy(cS *cluster.Service) gin.HandlerFunc {
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

		if err := cS.ProposeReplicationPolicyDelete(uint(id64), cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
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
			body, statusCode, err := forwardReplicationRunToNode(cS, uint(id64), runNodeID)
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replication_policy_run_started",
			Data:    nil,
		})
	}
}

func forwardReplicationRunToNode(cS *cluster.Service, policyID uint, nodeID string) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return nil, 0, err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(0, hostname, "", "")
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

func ActivateReplicationPolicyInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		if err := zS.ActivateReplicationPolicy(c.Request.Context(), req.PolicyID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
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
