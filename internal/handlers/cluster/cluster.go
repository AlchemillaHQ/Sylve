// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
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

type AcceptJoinRequest struct {
	NodeID      string                               `json:"nodeId" binding:"required"`
	NodeIP      string                               `json:"nodeIp" binding:"required,ip"`
	ClusterKey  string                               `json:"clusterKey" binding:"required"`
	NodeVersion string                               `json:"nodeVersion" binding:"required"`
	Preflight   bool                                 `json:"preflight"`
	Inventory   cluster.GuestIdentityInventoryReport `json:"inventory"`
}

type RemovePeerRequest struct {
	NodeID string `json:"nodeId" binding:"required"`
}

func joinLeaderAPIHost(leaderIP string) string {
	return cluster.ClusterAPIHost(leaderIP)
}

type basicHealthData struct {
	SylveVersion string `json:"sylveVersion"`
}

func fetchNodeVersionFromHealth(healthURL string, payload any, headers map[string]string) (string, error) {
	body, _, err := utils.HTTPPostJSONRead(healthURL, payload, headers)
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
	url string,
	payload AcceptJoinRequest,
	headers map[string]string,
) (internal.APIResponse[cluster.GuestIdentityInventoryReport], int, error) {
	var response internal.APIResponse[cluster.GuestIdentityInventoryReport]
	body, statusCode, err := utils.HTTPPostJSONRead(url, payload, headers)
	if len(body) > 0 {
		if decodeErr := json.Unmarshal(body, &response); decodeErr != nil && err == nil {
			return response, statusCode, fmt.Errorf("decode_join_admission_response_failed: %w", decodeErr)
		}
	}
	if err != nil {
		return response, statusCode, err
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
	case strings.Contains(errText, "add_voter_failed"):
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

// @Summary Get Cluster
// @Description Get cluster details with information about RAFT nodes too
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[clusterServiceInterfaces.ClusterDetails] "Success"
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

// @Summary Create Cluster
// @Description Create a cluster given a bootstrapping node IP
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster [post]
func CreateCluster(as *auth.Service, cS *cluster.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
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

		details, err := cS.GetClusterDetails()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_fetching_cluster_details",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		userId := c.GetUint("UserID")
		username := c.GetString("Username")
		authType := c.GetString("AuthType")

		clusterToken, err := as.CreateClusterJWT(userId, username, authType, details.Cluster.Key)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_creating_cluster_token",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[string]{
			Status:  "success",
			Message: "cluster_created",
			Error:   "",
			Data:    clusterToken,
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
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/join [post]
func JoinCluster(aS *auth.Service, cS *cluster.Service, zS *zelta.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JoinClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
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

		leaderAPIHost := joinLeaderAPIHost(req.LeaderIP)

		userId := c.GetUint("UserID")
		username := c.GetString("Username")
		authType := c.GetString("AuthType")

		clusterToken, err := aS.CreateClusterJWT(userId, username, authType, req.ClusterKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_join_token_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		headers := utils.FlatHeaders(c)
		headers["X-Cluster-Token"] = clusterToken

		healthURL := fmt.Sprintf(
			"https://%s/api/health/basic",
			leaderAPIHost,
		)

		leaderVersion, err := fetchNodeVersionFromHealth(healthURL, req, headers)
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
			ClusterKey:  req.ClusterKey,
			NodeVersion: localVersion,
			Preflight:   true,
			Inventory:   inventory,
		}
		leaderResponse, statusCode, err := postJoinAdmission(acceptURL, admission, headers)
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

		err = cS.StartAsJoiner(fsm, req.NodeIP, req.ClusterKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_starting_joiner",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		admission.Preflight = false
		leaderResponse, statusCode, err = postJoinAdmission(acceptURL, admission, headers)
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
				Message: "error_accepting_bad_leader_response",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := zS.ReconcileBackupTargetSSHKeys(); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_reconciling_backup_target_ssh_keys",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[string]{
			Status:  "success",
			Message: "cluster_joined",
			Error:   "",
			Data:    clusterToken,
		})
	}
}

// @Summary Accept Join
// @Description Accept a join request from a cluster node
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AcceptJoinRequest true "Accept Join Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/accept-join [post]
func AcceptJoin(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AcceptJoinRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   err.Error(),
				Data:    nil,
			})
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
				map[string]any{"clusterKey": req.ClusterKey},
				map[string]string{},
			)
			if err != nil || joinerVersion == "" {
				reason := "joiner_version_unavailable"
				if err != nil {
					reason = fmt.Sprintf("joiner_version_unavailable: %v", err)
				}

				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "cluster_version_mismatch",
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
				req.ClusterKey,
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

		if err := cS.AcceptJoinInventory(
			c.Request.Context(),
			req.NodeID,
			req.NodeIP,
			req.ClusterKey,
			req.Inventory,
		); err != nil {
			writeJoinAdmissionError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
			Status:  "success",
			Message: "node_added_to_cluster",
			Error:   "",
			Data:    req.Inventory,
		})
	}
}

// @Summary Reset Raft Node
// @Description Reset a Raft node by shutting it down and cleaning up its state
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/reset-node [delete]
func ResetRaftNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cS.ResetRaftNode(); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_resetting_raft_node",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "raft_node_reset",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Resync Cluster State
// @Description Replays current cluster-backed state through Raft and forces a snapshot from the leader
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/resync-state [post]
func ResyncClusterState(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cS.ResyncClusterState(); err != nil {
			if strings.HasPrefix(err.Error(), "not_leader;") {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "not_leader",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_resyncing_cluster_state",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := zS.ReconcileBackupTargetSSHKeys(); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_reconciling_backup_target_ssh_keys",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_state_resynced",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Remove Peer
// @Description Remove a peer from the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RemovePeerRequest true "Remove Peer Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/remove-peer [post]
func RemovePeer(cS *cluster.Service) gin.HandlerFunc {
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

		raftId := raft.ServerID(req.NodeID)

		if err := cS.RemovePeer(raftId); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_removing_peer",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ClearClusterNode(req.NodeID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_clearing_cluster_node",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "peer_removed",
			Error:   "",
			Data:    nil,
		})
	}
}
