package datacenterHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/datacenter"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type JoinClusterRequest struct {
	NodeID      string `json:"nodeID" binding:"required"`
	NodeAddress string `json:"nodeAddr" binding:"required"`
	LeaderAPI   string `json:"leaderAPI" binding:"required"`
	ClusterKey  string `json:"clusterKey" binding:"required"`
}

func JoinCluster(dcService *datacenter.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JoinClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request_payload", Error: err.Error(),
			})
			return
		}

		if err := dcService.StartAsJoiner(fsm); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "failed_to_start_as_joiner", Error: err.Error(),
			})
			return
		}

		joinPayload := map[string]string{
			"nodeID":     req.NodeID,
			"nodeAddr":   req.NodeAddress,
			"clusterKey": req.ClusterKey,
		}

		if err := utils.HTTPPostJSON(req.LeaderAPI+"/api/datacenter/cluster/join/accept", joinPayload); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "failed_to_contact_leader", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "node_joined_cluster",
		})
	}
}

func AcceptJoin(dcService *datacenter.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			NodeID     string `json:"nodeID" binding:"required"`
			NodeAddr   string `json:"nodeAddr" binding:"required"`
			ClusterKey string `json:"clusterKey" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request_payload", Error: err.Error(),
			})
			return
		}

		if err := dcService.AcceptJoin(req.NodeID, req.NodeAddr, req.ClusterKey); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "cluster_join_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "node_added_to_cluster",
		})
	}
}

func GetCluster(dcService *datacenter.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cluster, err := dcService.GetCluster()
		if err != nil {
			if strings.Contains(err.Error(), "cluster_not_found") {
				c.JSON(http.StatusOK, internal.APIResponse[any]{
					Status:  "success",
					Message: "cluster_not_found",
					Error:   "",
					Data:    nil,
				})
			} else {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status:  "error",
					Message: "error_finding_cluster",
					Error:   err.Error(),
					Data:    nil,
				})
			}
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_fetched",
			Error:   "",
			Data:    cluster,
		})
	}
}

func CreateCluster(dcService *datacenter.Service, fsm raft.FSM) gin.HandlerFunc {
	return func(c *gin.Context) {
		cluster, err := dcService.CreateCluster(fsm)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "cluster_creation_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_created",
			Error:   "",
			Data:    cluster,
		})
	}
}
