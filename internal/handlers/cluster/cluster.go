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
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type CreateClusterRequest struct {
	IP   string `json:"ip" binding:"required,ip"`
	Port int    `json:"port" binding:"required,min=1024,max=65535"`
}

type JoinClusterRequest struct {
	NodeID     string `json:"nodeId" binding:"required"`
	NodeIP     string `json:"nodeIp" binding:"required,ip"`
	NodePort   int    `json:"nodePort" binding:"required,min=1024,max=65535"`
	LeaderAPI  string `json:"leaderApi" binding:"required"`
	ClusterKey string `json:"clusterKey" binding:"required"`
}

type AcceptJoinRequest struct {
	NodeID     string `json:"nodeId" binding:"required"`
	NodeIP     string `json:"nodeIp" binding:"required,ip"`
	NodePort   int    `json:"nodePort" binding:"required,min=1024,max=65535"`
	ClusterKey string `json:"clusterKey" binding:"required"`
}

type RemovePeerRequest struct {
	NodeID string `json:"nodeId" binding:"required"`
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
// @Description Create a cluster given a bootstrapping nodes IP and Port
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

		if err := cS.CreateCluster(req.IP, req.Port, fsm); err != nil {
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
func JoinCluster(aS *auth.Service, cS *cluster.Service, fsm raft.FSM) gin.HandlerFunc {
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

		if !utils.IsValidIPPort(req.LeaderAPI) {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_leader_api",
				Error:   "leader_api_must_be_in_host_port_format",
				Data:    nil,
			})
			return
		}

		userId := c.GetUint("UserID")
		username := c.GetString("Username")
		authType := c.GetString("AuthType")

		clusterToken, err := aS.CreateClusterJWT(userId, username, authType, req.ClusterKey)
		headers := utils.FlatHeaders(c)
		headers["X-Cluster-Token"] = clusterToken

		healthURL := fmt.Sprintf(
			"https://%s/api/health/basic",
			req.LeaderAPI,
		)

		if err := utils.HTTPPostJSON(healthURL, req, headers); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_pinging_cluster_bad_leader_response",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = cS.StartAsJoiner(fsm, req.NodeIP, req.NodePort, req.ClusterKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_starting_joiner",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		acceptURL := fmt.Sprintf("https://%s/api/cluster/accept-join", req.LeaderAPI)
		payload := map[string]any{
			"nodeId":     req.NodeID,
			"nodeIp":     req.NodeIP,
			"nodePort":   req.NodePort,
			"clusterKey": req.ClusterKey,
		}

		if err := utils.HTTPPostJSON(acceptURL, payload, headers); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "error_accepting_bad_leader_response",
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

		if err := cS.AcceptJoin(req.NodeID, req.NodeIP, req.NodePort, req.ClusterKey); err != nil {
			if strings.HasPrefix(err.Error(), "not_leader;") {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "not_leader",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_join_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "node_added_to_cluster",
			Error:   "",
			Data:    nil,
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
func ResyncClusterState(cS *cluster.Service) gin.HandlerFunc {
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
