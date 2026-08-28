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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type CreateClusterRequest struct {
	IP string `json:"ip" binding:"required,ip"`
}

type JoinClusterRequest struct {
	NodeID     string `json:"nodeId" binding:"required"`
	NodeIP     string `json:"nodeIp" binding:"required,ip"`
	LeaderIP   string `json:"leaderIp" binding:"required,ip"`
	ClusterKey string `json:"clusterKey" binding:"required"`
}

type AcceptJoinRequest = cluster.JoinAdmissionRequest

type JoinKeyResponse struct {
	Key string `json:"key"`
}

type RemovePeerRequest struct {
	NodeID string `json:"nodeId" binding:"required"`
}

type MembershipStatusRequest struct {
	NodeID string `json:"nodeId" binding:"required"`
}

func joinLeaderAPIHost(leaderIP string) string {
	return cluster.ClusterAPIHost(leaderIP)
}

type basicHealthData struct {
	SylveVersion string `json:"sylveVersion"`
}

func fetchNodeVersionFromHealth(healthURL string, headers map[string]string) (string, error) {
	body, _, err := utils.HTTPGetJSONRead(healthURL, headers)
	if err != nil {
		return "", err
	}

	var healthResp internal.APIResponse[basicHealthData]
	if err := json.Unmarshal(body, &healthResp); err != nil {
		return "", fmt.Errorf("decode_health_response_failed: %w", err)
	}

	return strings.TrimSpace(healthResp.Data.SylveVersion), nil
}

func postJoinAdmission(
	ctx context.Context,
	url string,
	payload AcceptJoinRequest,
	headers map[string]string,
) (internal.APIResponse[cluster.GuestIdentityInventoryReport], int, error) {
	var response internal.APIResponse[cluster.GuestIdentityInventoryReport]
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return response, 0, err
	}
	httpResponse, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		url,
		requestBody,
		headers,
		30*time.Second,
		4<<20,
	)
	statusCode := httpResponse.StatusCode
	body := httpResponse.Body
	if len(body) > 0 {
		if decodeErr := json.Unmarshal(body, &response); decodeErr != nil && err == nil {
			return response, statusCode, fmt.Errorf("decode_join_admission_response_failed: %w", decodeErr)
		}
	}
	if err != nil {
		return response, statusCode, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return response, statusCode, fmt.Errorf("join_admission_http_status_%d", statusCode)
	}
	if response.Status != "success" {
		return response, statusCode, fmt.Errorf("join_admission_rejected: %s", response.Error)
	}
	return response, statusCode, nil
}

func writeJoinAdmissionError(c *gin.Context, err error) {
	var conflict *cluster.GuestIdentityInventoryConflictError
	if errors.As(err, &conflict) {
		c.JSON(http.StatusConflict, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
			Status:  "error",
			Message: "guest_identity_inventory_conflict",
			Error:   err.Error(),
			Data:    conflict.Report,
		})
		return
	}

	message := "cluster_join_failed"
	status := http.StatusBadRequest
	errText := err.Error()
	switch {
	case strings.HasPrefix(errText, "not_leader;"):
		message = "not_leader"
		status = http.StatusConflict
	case isUncertainJoinOutcome(errText):
		message = "cluster_join_outcome_uncertain"
		status = http.StatusServiceUnavailable
	case strings.Contains(errText, "inventory_unavailable") ||
		strings.Contains(errText, "inventory_auth_service_unavailable") ||
		strings.Contains(errText, "inventory_remote_") ||
		strings.Contains(errText, "inventory_cluster_token_failed") ||
		strings.Contains(errText, "inventory_collection_canceled"):
		message = "guest_identity_inventory_unavailable"
		status = http.StatusServiceUnavailable
	case strings.Contains(errText, "inventory") || strings.Contains(errText, "joining_node"):
		message = "guest_identity_join_preflight_failed"
		status = http.StatusConflict
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errText,
		Data:    nil,
	})
}

func isUncertainJoinOutcome(errText string) bool {
	for _, marker := range []string{
		"add_nonvoter_failed",
		"replicated_state_catchup_failed",
		"replicated_state_verification_failed",
		"replicated_state_digest_mismatch",
		"replicated_state_promote_nonvoter_failed",
		"replicated_state_promote_unfence_failed",
	} {
		if strings.Contains(errText, marker) {
			return true
		}
	}
	return false
}

// @Summary Get Cluster
// @Description Get cluster details with information about RAFT nodes too
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[clusterServiceInterfaces.ClusterDetails] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster [get]
func GetCluster(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		details, err := cS.GetClusterDetails()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_finding_cluster",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterServiceInterfaces.ClusterDetails]{
			Status:  "success",
			Message: "cluster_fetched",
			Error:   "",
			Data:    details,
		})
	}
}

// @Summary Reveal Cluster Join Key
// @Description Reveal the enabled cluster key to a local administrator for node enrollment
// @Tags Cluster
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[JoinKeyResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Cluster join key unavailable"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/join-key [get]
func GetJoinKey(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		c.Header("Referrer-Policy", "no-referrer")

		key, err := authService.GetClusterKey()
		if err != nil {
			if strings.Contains(err.Error(), "cluster_key_not_found") ||
				strings.Contains(err.Error(), "cluster_key_not_configured") {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status: "error", Message: "cluster_join_key_unavailable",
					Error: "cluster_join_key_unavailable", Data: nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "cluster_join_key_lookup_failed",
				Error: "cluster_join_key_lookup_failed", Data: nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[JoinKeyResponse]{
			Status: "success", Message: "cluster_join_key_fetched",
			Error: "", Data: JoinKeyResponse{Key: key},
		})
	}
}

// @Summary Create Cluster
// @Description Create a cluster given a bootstrapping node IP
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateClusterRequest true "Create Cluster Request"
// @Success 201 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.GuestIdentityInventoryReport] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster [post]
func CreateCluster(cS *cluster.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}

		if err := cS.CreateCluster(req.IP, fsm); err != nil {
			var conflict *cluster.GuestIdentityInventoryConflictError
			if errors.As(err, &conflict) {
				writeJoinAdmissionError(c, err)
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_creating_cluster",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Join Cluster
// @Description Join an existing cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JoinClusterRequest true "Join Cluster Request"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.GuestIdentityInventoryReport] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/join [post]
func JoinCluster(cS *cluster.Service, zS *zelta.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JoinClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}

		if !utils.IsValidIP(req.LeaderIP) {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_leader_ip",
				Error:   "leader_ip_must_be_valid",
				Data:    nil,
			})
			return
		}

		clusterKey := strings.TrimSpace(req.ClusterKey)
		if clusterKey == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_cluster_key",
				Error: "cluster_key_required", Data: nil,
			})
			return
		}

		leaderAPIHost := joinLeaderAPIHost(req.LeaderIP)
		healthHeaders := map[string]string{
			"Accept":              "application/json",
			auth.ClusterKeyHeader: clusterKey,
		}
		admissionHeaders := map[string]string{
			"Accept":              "application/json",
			"Content-Type":        "application/json",
			auth.ClusterKeyHeader: clusterKey,
		}

		healthURL := fmt.Sprintf(
			"https://%s/api/health/basic",
			leaderAPIHost,
		)

		leaderVersion, err := fetchNodeVersionFromHealth(healthURL, healthHeaders)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_pinging_cluster_bad_leader_response",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		localVersion := strings.TrimSpace(cmd.Version)
		if leaderVersion == "" {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_version_mismatch",
				Error:   "leader_version_unavailable",
				Data:    nil,
			})
			return
		}

		if localVersion == "" || leaderVersion != localVersion {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_version_mismatch",
				Error:   fmt.Sprintf("leader=%s,node=%s", leaderVersion, localVersion),
				Data:    nil,
			})
			return
		}

		localNodeID := strings.TrimSpace(cS.LocalNodeID())
		if localNodeID == "" || localNodeID != strings.TrimSpace(req.NodeID) {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "joining_node_id_mismatch",
				Error:   fmt.Sprintf("expected=%s actual=%s", localNodeID, strings.TrimSpace(req.NodeID)),
				Data:    nil,
			})
			return
		}

		inventory, err := cluster.ScanLocalGuestIdentityInventory(cS.DB, localNodeID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "guest_identity_inventory_scan_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		if len(inventory.Conflicts) != 0 {
			writeJoinAdmissionError(c, &cluster.GuestIdentityInventoryConflictError{Report: inventory})
			return
		}
		acceptURL := fmt.Sprintf("https://%s/api/cluster/accept-join", leaderAPIHost)
		admission := AcceptJoinRequest{
			NodeID:      localNodeID,
			NodeIP:      req.NodeIP,
			NodeVersion: localVersion,
			Preflight:   true,
			Inventory:   inventory,
		}
		leaderResponse, statusCode, err := postJoinAdmission(
			c.Request.Context(), acceptURL, admission, admissionHeaders,
		)
		if err != nil {
			if leaderResponse.Message != "" {
				if statusCode < 400 {
					statusCode = http.StatusConflict
				}
				c.JSON(statusCode, leaderResponse)
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_join_preflight_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		latestInventory, err := cluster.ScanLocalGuestIdentityInventory(cS.DB, localNodeID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "guest_identity_inventory_scan_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		if latestInventory.Digest != inventory.Digest {
			c.JSON(http.StatusConflict, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
				Status:  "error",
				Message: "joining_inventory_changed_before_start",
				Error:   "joining_inventory_changed_before_start",
				Data:    latestInventory,
			})
			return
		}

		admission.Preflight = false
		if err := cS.SaveJoinIntent(req.LeaderIP, clusterKey, admission); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_join_intent_save_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		_ = cS.MarkJoinIntentPhase(cluster.JoinPhaseStarting, nil)
		err = cS.StartAsJoiner(fsm, req.NodeIP, clusterKey)
		if err != nil {
			_ = cS.MarkJoinIntentPhase(cluster.JoinPhaseFailed, err)
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_starting_joiner",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		submission := cS.SubmitJoinIntent(c.Request.Context())
		if submission.Err != nil && !submission.Retryable {
			if submission.Response.Message != "" {
				statusCode = submission.StatusCode
				if statusCode < 400 {
					statusCode = http.StatusConflict
				}
				c.JSON(statusCode, submission.Response)
				return
			}
			c.JSON(http.StatusConflict, internal.APIResponse[cluster.ClusterJoinStatus]{
				Status:  "error",
				Message: "cluster_join_rejected",
				Error:   submission.Err.Error(),
				Data:    submission.Status,
			})
			return
		}

		status := submission.Status
		if strings.TrimSpace(status.NodeID) == "" {
			status, _ = cS.JoinStatus()
		}
		if status.Phase == cluster.JoinPhaseComplete && zS != nil {
			if err := zS.ReconcileBackupTargetSSHKeys(); err != nil {
				logger.L.Warn().Err(err).Msg("backup_target_ssh_reconciliation_deferred_after_join")
			}
			if err := zS.ReconcileEncryptionKeys(); err != nil {
				logger.L.Warn().Err(err).Msg("encryption_key_reconciliation_deferred_after_join")
			}
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[cluster.ClusterJoinStatus]{
			Status:  "success",
			Message: "cluster_join_started",
			Error:   "",
			Data:    status,
		})
	}
}

// @Summary Accept Join
// @Description Accept a join request from a cluster node
// @Tags Cluster
// @Accept json
// @Produce json
// @Security ClusterKeyAuth
// @Param request body AcceptJoinRequest true "Accept Join Request"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.GuestIdentityInventoryReport] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/accept-join [post]
func AcceptJoin(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clusterKey := c.GetString("ClusterKey")
		var req AcceptJoinRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}

		localVersion := strings.TrimSpace(cmd.Version)
		nodeVersion := strings.TrimSpace(req.NodeVersion)
		if localVersion == "" || nodeVersion == "" || nodeVersion != localVersion {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_version_mismatch",
				Error:   fmt.Sprintf("leader=%s,node=%s", localVersion, nodeVersion),
				Data:    nil,
			})
			return
		}

		if !req.Preflight {
			joinerHealthURL := fmt.Sprintf("https://%s/api/health/basic", cluster.ClusterAPIHost(req.NodeIP))
			joinerVersion, err := fetchNodeVersionFromHealth(
				joinerHealthURL,
				map[string]string{auth.ClusterKeyHeader: clusterKey},
			)
			if err != nil || joinerVersion == "" {
				reason := "joiner_version_unavailable"
				if err != nil {
					reason = fmt.Sprintf("joiner_version_unavailable: %v", err)
				}

				c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
					Status:  "error",
					Message: "cluster_version_check_unavailable",
					Error:   reason,
					Data:    nil,
				})
				return
			}

			if joinerVersion != localVersion || joinerVersion != nodeVersion {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "cluster_version_mismatch",
					Error:   fmt.Sprintf("leader=%s,node=%s", localVersion, joinerVersion),
					Data:    nil,
				})
				return
			}
		}

		if req.Preflight {
			report, err := cS.PreflightJoinInventory(
				c.Request.Context(),
				req.NodeID,
				req.NodeIP,
				clusterKey,
				req.Inventory,
			)
			if err != nil {
				writeJoinAdmissionError(c, err)
				return
			}
			c.JSON(http.StatusOK, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
				Status:  "success",
				Message: "cluster_join_preflight_passed",
				Error:   "",
				Data:    report,
			})
			return
		}

		status, err := cS.StageJoinInventory(
			c.Request.Context(),
			req.NodeID,
			req.NodeIP,
			clusterKey,
			req.Inventory,
		)
		if err != nil {
			writeJoinAdmissionError(c, err)
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[cluster.ClusterJoinStatus]{
			Status:  "success",
			Message: "cluster_join_started",
			Error:   "",
			Data:    status,
		})
	}
}

func GetJoinStatus(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := cS.JoinStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "cluster_join_status_failed", Error: err.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterJoinStatus]{
			Status: "success", Message: "cluster_join_status", Error: "", Data: status,
		})
	}
}

func JoinProgressInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		progress, err := cS.LocalJoinProgress(c.Query("expectedNodeId"))
		if err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "cluster_join_progress_failed", Error: err.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterJoinProgress]{
			Status: "success", Message: "cluster_join_progress", Error: "", Data: progress,
		})
	}
}

// @Summary Leave Cluster
// @Description Safely remove this node from Raft and clear its local cluster state
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/reset-node [delete]
func ResetRaftNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := cS.LeaveCluster(c.Request.Context())
		if err != nil {
			writeClusterLeaveError(c, result, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status:  "success",
			Message: "cluster_leave_completed",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Force Local Cluster Reset
// @Description Reset only the local cluster state after external fencing and membership repair acknowledgement
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body cluster.ForceLocalResetRequest true "Force Local Reset Request"
// @Success 200 {object} internal.APIResponse[cluster.ClusterLeaveResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/reset-node/force [delete]
func ForceResetRaftNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.ForceLocalResetRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		result, err := cS.ForceLocalReset(c.Request.Context(), request)
		if err != nil {
			writeClusterLeaveError(c, result, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status: "success", Message: "cluster_local_reset_forced", Error: "", Data: result,
		})
	}
}

func ReplicatedStateInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		minimumIndex, err := strconv.ParseUint(
			strings.TrimSpace(c.Query("minimumRaftAppliedIndex")),
			10,
			64,
		)
		if err != nil && strings.TrimSpace(c.Query("minimumRaftAppliedIndex")) != "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_minimum_raft_applied_index",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		digest, err := cS.LocalReplicatedStateDigest(
			c.Request.Context(),
			c.Query("expectedNodeId"),
			minimumIndex,
		)
		if err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[cluster.ReplicatedStateDigest]{
				Status:  "error",
				Message: "replicated_state_unavailable",
				Error:   err.Error(),
				Data:    digest,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ReplicatedStateDigest]{
			Status:  "success",
			Message: "replicated_state_captured",
			Error:   "",
			Data:    digest,
		})
	}
}

func ReplicatedStateRepairInternal(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.ReplicatedStateRepairRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var err error
		switch strings.ToLower(strings.TrimSpace(request.Action)) {
		case cluster.ReplicatedStateRepairFence:
			err = cS.SetReplicatedStateRepairFence(request.ExpectedNodeID, true)
		case cluster.ReplicatedStateRepairReset:
			err = cS.ResetReplicatedStateForRepair(request.ExpectedNodeID)
		case cluster.ReplicatedStateRepairUnfence:
			if zS != nil {
				if reconcileErr := zS.ReconcileBackupTargetSSHKeys(); reconcileErr != nil {
					err = fmt.Errorf("reconcile_backup_target_ssh_keys: %w", reconcileErr)
					break
				}
				if reconcileErr := zS.ReconcileEncryptionKeys(); reconcileErr != nil {
					err = fmt.Errorf("reconcile_encryption_keys: %w", reconcileErr)
					break
				}
			}
			err = cS.SetReplicatedStateRepairFence(request.ExpectedNodeID, false)
		default:
			err = fmt.Errorf("unsupported_replicated_state_repair_action")
		}
		if err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "replicated_state_repair_action_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "replicated_state_repair_action_completed",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Resync Cluster State
// @Description Audits every Raft member and rebuilds divergent followers one at a time
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[cluster.ClusterStateResyncResult] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.ClusterStateResyncResult] "Conflict with partial audit/repair result"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/resync-state [post]
func ResyncClusterState(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := cS.ResyncClusterStateWithResult(c.Request.Context())
		if err != nil {
			if strings.HasPrefix(err.Error(), "not_leader;") {
				c.JSON(http.StatusConflict, internal.APIResponse[cluster.ClusterStateResyncResult]{
					Status:  "error",
					Message: "not_leader",
					Error:   err.Error(),
					Data:    result,
				})
				return
			}

			statusCode := http.StatusInternalServerError
			var blocked *cluster.ReplicatedStateRepairBlockedError
			if errors.As(err, &blocked) {
				statusCode = http.StatusConflict
			}
			c.JSON(statusCode, internal.APIResponse[cluster.ClusterStateResyncResult]{
				Status:  "error",
				Message: "error_resyncing_cluster_state",
				Error:   err.Error(),
				Data:    result,
			})
			return
		}

		if zS != nil {
			if err := zS.ReconcileBackupTargetSSHKeys(); err != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[cluster.ClusterStateResyncResult]{
					Status:  "error",
					Message: "error_reconciling_backup_target_ssh_keys",
					Error:   err.Error(),
					Data:    result,
				})
				return
			}
			if err := zS.ReconcileEncryptionKeys(); err != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[cluster.ClusterStateResyncResult]{
					Status:  "error",
					Message: "error_reconciling_encryption_keys",
					Error:   err.Error(),
					Data:    result,
				})
				return
			}
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterStateResyncResult]{
			Status:  "success",
			Message: "cluster_state_resynced",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Remove Peer
// @Description Ask an online peer to fence local work, leave Raft, and clear its cluster state
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RemovePeerRequest true "Remove Peer Request"
// @Success 200 {object} internal.APIResponse[cluster.ClusterLeaveResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.PeerRemovalConflict] "Peer owns cluster resources"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/remove-node [post]
func RemoveNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RemovePeerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		nodeID := strings.TrimSpace(req.NodeID)
		if nodeID == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "node_id_required",
				Data:    nil,
			})
			return
		}
		result, err := cS.OrchestratePeerRemoval(c.Request.Context(), nodeID)
		if err != nil {
			writeClusterLeaveError(c, result, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status:  "success",
			Message: "peer_removed",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Force Remove Peer
// @Description Remove an externally fenced, unavailable peer without contacting it
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body cluster.ForceRemovePeerRequest true "Force Remove Peer Request"
// @Success 200 {object} internal.APIResponse[cluster.ClusterLeaveResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[cluster.PeerRemovalConflict] "Conflict"
// @Failure 503 {object} internal.APIResponse[any] "Consensus unavailable"
// @Router /cluster/remove-node/force [post]
func ForceRemoveNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.ForceRemovePeerRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		result, err := cS.ForceRemovePeer(c.Request.Context(), request)
		if err != nil {
			writeClusterLeaveError(c, result, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status: "success", Message: "peer_force_removed", Error: "", Data: result,
		})
	}
}

func StartLeaveInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.StartLeaveRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		result, err := cS.StartCooperativeLeave(
			c.Request.Context(),
			request,
			c.GetString("IssuerNodeID"),
		)
		if err != nil {
			writeClusterLeaveError(c, result, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status: "success", Message: "cluster_leave_completed", Error: "", Data: result,
		})
	}
}

func RemoveMembershipInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.RemoveMembershipRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if err := cS.RemoveMembership(c.Request.Context(), request, c.GetString("IssuerNodeID")); err != nil {
			writeClusterLeaveError(c, cluster.ClusterLeaveResult{}, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "peer_membership_removed", Error: "", Data: nil,
		})
	}
}

func MembershipStatus(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request MembershipStatusRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		status, err := cS.AuthoritativeMembershipStatus(request.NodeID)
		if err != nil {
			code := http.StatusServiceUnavailable
			message := "cluster_membership_status_unavailable"
			if strings.Contains(err.Error(), "not_leader") {
				code = http.StatusConflict
				message = "not_leader"
			}
			c.JSON(code, internal.APIResponse[cluster.MembershipStatus]{
				Status: "error", Message: message, Error: err.Error(), Data: status,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.MembershipStatus]{
			Status: "success", Message: "cluster_membership_status", Error: "", Data: status,
		})
	}
}

func GetLeaveStatus(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := cS.LeaveStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "cluster_leave_status_failed", Error: err.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveStatus]{
			Status: "success", Message: "cluster_leave_status", Error: "", Data: status,
		})
	}
}

func writeClusterLeaveError(c *gin.Context, result cluster.ClusterLeaveResult, err error) {
	var consensusErr *cluster.ClusterConsensusError
	if errors.As(err, &consensusErr) {
		c.JSON(http.StatusServiceUnavailable, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status: "error", Message: "cluster_consensus_unavailable", Error: err.Error(), Data: result,
		})
		return
	}
	var blocked *cluster.PeerRemovalBlockedError
	if errors.As(err, &blocked) {
		c.JSON(http.StatusConflict, internal.APIResponse[cluster.PeerRemovalConflict]{
			Status: "error", Message: "peer_removal_blocked", Error: err.Error(), Data: blocked.Conflict,
		})
		return
	}
	var versionErr *cluster.ClusterVersionError
	if errors.As(err, &versionErr) {
		status := http.StatusConflict
		if versionErr.Code == "cluster_version_check_unavailable" {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, internal.APIResponse[any]{
			Status: "error", Message: versionErr.Code, Error: err.Error(), Data: nil,
		})
		return
	}
	var leaveErr *cluster.ClusterLeaveError
	if errors.As(err, &leaveErr) {
		status := http.StatusAccepted
		if leaveErr.Code == "cluster_target_unreachable" {
			status = http.StatusServiceUnavailable
		} else if leaveErr.Code == "cluster_leave_active_mutations" {
			status = http.StatusConflict
		}
		c.JSON(status, internal.APIResponse[cluster.ClusterLeaveResult]{
			Status: "error", Message: leaveErr.Code, Error: err.Error(), Data: result,
		})
		return
	}
	status := http.StatusInternalServerError
	message := "cluster_leave_failed"
	if strings.Contains(err.Error(), "not_leader") {
		status = http.StatusConflict
		message = "not_leader"
	} else if strings.Contains(err.Error(), "peer_not_found") {
		status = http.StatusNotFound
		message = "peer_not_found"
	} else if strings.Contains(err.Error(), "_ack_required") {
		status = http.StatusBadRequest
		message = err.Error()
	} else if strings.Contains(err.Error(), "mismatch") || strings.Contains(err.Error(), "invalid") ||
		strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "in_progress") {
		status = http.StatusConflict
	}
	c.JSON(status, internal.APIResponse[cluster.ClusterLeaveResult]{
		Status: "error", Message: message, Error: err.Error(), Data: result,
	})
}
