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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
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
	// Address is the node the key joins through.
	Address string `json:"address"`
}

type RemovePeerRequest struct {
	NodeID       string `json:"nodeId" binding:"required"`
	RetainGuests bool   `json:"retainGuests"`
}

type ResetNodeRequest struct {
	RetainGuests bool `json:"retainGuests"`
}

type ForceResetNodeRequest struct {
	NodeID                       string `json:"nodeId"`
	RemoteMembershipAcknowledged bool   `json:"remoteMembershipAcknowledged"`
	WorkloadsExternallyFenced    bool   `json:"workloadsExternallyFenced"`
}

type ForceRemovePeerRequest struct {
	NodeID                 string `json:"nodeId" binding:"required"`
	TargetExternallyFenced bool   `json:"targetExternallyFenced"`
}

type MembershipStatusRequest struct {
	NodeID string `json:"nodeId" binding:"required"`
}

func joinLeaderAPIHost(leaderIP string) string {
	return cluster.ClusterAPIHost(leaderIP)
}

type basicHealthData struct {
	SylveVersion string `json:"sylveVersion"`
	ServerTime   string `json:"serverTime"`
}

type nodeHealthResult struct {
	health       basicHealthData
	requestStart time.Time
	responseAt   time.Time
}

func fetchNodeHealth(ctx context.Context, healthURL string, headers map[string]string) (nodeHealthResult, error) {
	result := nodeHealthResult{requestStart: time.Now()}
	body, _, err := utils.HTTPGetJSONReadContext(ctx, healthURL, headers)
	result.responseAt = time.Now()
	if err != nil {
		return result, err
	}

	var healthResp internal.APIResponse[basicHealthData]
	if err := json.Unmarshal(body, &healthResp); err != nil {
		return result, fmt.Errorf("decode_health_response_failed: %w", err)
	}

	result.health = healthResp.Data
	return result, nil
}

// The joining node gives up on a 4xx and asks again on anything else.
func writeJoinAdmissionError(c *gin.Context, err error) {
	var conflict *cluster.GuestIdentityInventoryConflictError
	if errors.As(err, &conflict) {
		c.JSON(http.StatusConflict, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
			Status: "error", Message: "guest_identity_inventory_conflict", Error: err.Error(), Data: conflict.Report,
		})
		return
	}
	var versionErr *cluster.ClusterVersionError
	errText := err.Error()
	status, message := http.StatusServiceUnavailable, "cluster_join_admission_unavailable"
	switch {
	case errors.As(err, &versionErr):
		status, message = http.StatusConflict, versionErr.Code
	case strings.Contains(errText, "invalid_cluster_key"):
		status, message = http.StatusForbidden, "invalid_cluster_key"
	case strings.HasPrefix(errText, "add_nonvoter_failed"), strings.HasPrefix(errText, "joining_guest_identity_claim_failed"):
		message = "cluster_join_outcome_uncertain"
	case strings.HasPrefix(errText, "joining_node_hostname_taken"):
		status, message = http.StatusConflict, "joining_node_hostname_taken"
	case strings.HasPrefix(errText, "invalid_joining_node_ip"), strings.HasPrefix(errText, "joining_"):
		status, message = http.StatusConflict, "cluster_join_refused"
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: errText})
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

// @Summary Generate Cluster Join Key
// @Description Make a key that lets one node join through this node, at the returned address. It replaces any earlier key, and it's shown only now: the cluster keeps only its hash. Admitting a node or removing one makes it stop working.
// @Tags Cluster
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[JoinKeyResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Cluster join key unavailable"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/join-key [post]
func GenerateJoinKey(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		c.Header("Referrer-Policy", "no-referrer")

		key, address, err := cS.GenerateJoinKey(c.Request.Context())
		if err != nil {
			status, message := http.StatusInternalServerError, "cluster_join_key_generation_failed"
			if strings.Contains(err.Error(), "cluster_join_key_unavailable") {
				status, message = http.StatusConflict, "cluster_join_key_unavailable"
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[JoinKeyResponse]{
			Status: "success", Message: "cluster_join_key_generated",
			Data: JoinKeyResponse{Key: key.String(), Address: address},
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
func CreateCluster(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if !utils.IsValidIPv4(req.IP) {
			writeClusterIPv6Unsupported(c, "cluster_ip")
			return
		}

		if err := cS.CreateCluster(req.IP); err != nil {
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
// @Description Join an existing cluster. A returning node uses the same endpoint, and all of its current VM, jail and container IDs must be free in the cluster.
// @Description The leader admits the node first, and a refusal changes nothing on it. Then it starts Raft and catches up; the leader makes it a voter once it has.
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JoinClusterRequest true "Join Cluster Request"
// @Success 202 {object} internal.APIResponse[cluster.ClusterJoinStatus] "Join started"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.GuestIdentityInventoryReport] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/join [post]
func JoinCluster(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JoinClusterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if !utils.IsValidIPv4(req.LeaderIP) {
			writeClusterIPv6Unsupported(c, "leader_ip")
			return
		}
		if !utils.IsValidIPv4(req.NodeIP) {
			writeClusterIPv6Unsupported(c, "node_ip")
			return
		}
		key, err := cluster.ParseJoinKey(req.ClusterKey)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "cluster_join_key_invalid", Error: err.Error()})
			return
		}
		if localNodeID := cS.LocalNodeID(); localNodeID == "" || localNodeID != strings.TrimSpace(req.NodeID) {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "joining_node_id_mismatch",
				Error: fmt.Sprintf("expected=%s actual=%s", localNodeID, strings.TrimSpace(req.NodeID)),
			})
			return
		}

		health, err := fetchNodeHealth(
			utils.ForJoin(c.Request.Context(), key.VerifyPin),
			fmt.Sprintf("https://%s/api/health/basic", joinLeaderAPIHost(req.LeaderIP)),
			map[string]string{"Accept": "application/json", auth.ClusterKeyHeader: key.Secret},
		)
		if err != nil {
			status, message := http.StatusInternalServerError, "error_pinging_cluster_bad_leader_response"
			if strings.Contains(err.Error(), "cluster_join_key_node_mismatch") {
				status, message = http.StatusConflict, "cluster_join_key_node_mismatch"
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
			return
		}
		leaderVersion, localVersion := strings.TrimSpace(health.health.SylveVersion), strings.TrimSpace(cmd.Version)
		if leaderVersion == "" || leaderVersion != localVersion {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "cluster_version_mismatch",
				Error: fmt.Sprintf("leader=%s,node=%s", leaderVersion, localVersion),
			})
			return
		}
		if offset, known := measureClockOffset(health.health.ServerTime, health.requestStart, health.responseAt); known && joinClockDriftBlocked(offset) {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "cluster_join_clock_drift", Error: "leader_offset=" + formatClockOffset(offset),
			})
			return
		}

		status, err := cS.Join(c.Request.Context(), req.LeaderIP, req.NodeIP, key.String())
		var refused *cluster.JoinAdmissionError
		switch {
		case errors.As(err, &refused):
			c.JSON(refused.StatusCode, internal.APIResponse[json.RawMessage]{
				Status: "error", Message: refused.Message, Error: refused.Detail, Data: refused.Data,
			})
		case err != nil:
			statusCode := http.StatusInternalServerError
			switch {
			case errors.Is(err, cluster.ErrNodeLeaveFenced):
				statusCode = http.StatusLocked
			case strings.HasPrefix(err.Error(), "invalid_"):
				statusCode = http.StatusBadRequest
			case err.Error() == "clustered_already", err.Error() == "guest_identity_mutation_in_progress":
				statusCode = http.StatusConflict
			}
			c.JSON(statusCode, internal.APIResponse[any]{Status: "error", Message: "cluster_join_failed", Error: err.Error()})
		default:
			c.JSON(http.StatusAccepted, internal.APIResponse[cluster.ClusterJoinStatus]{
				Status: "success", Message: "cluster_join_started", Data: status,
			})
		}
	}
}

// @Summary Accept Join
// @Description Admit a node to the cluster as a nonvoter. The leader makes it a voter once it caught up.
// @Tags Cluster
// @Accept json
// @Produce json
// @Security ClusterKeyAuth
// @Param request body AcceptJoinRequest true "Accept Join Request"
// @Success 202 {object} internal.APIResponse[cluster.ClusterJoinStatus] "Admitted"
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
		body, err := c.GetRawData()
		if err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		var req AcceptJoinRequest
		if err := binding.JSON.BindBody(body, &req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if !utils.IsValidIPv4(req.NodeIP) {
			writeClusterIPv6Unsupported(c, "node_ip")
			return
		}
		var pin [sha256.Size]byte
		// Forwarded by the member the joiner asked. A joiner asking again after it
		// was admitted is a member too, but sends its own cert, not a pin header.
		forwarded := cluster.PeerNode(c.Request) != "" && c.GetHeader(cluster.JoinerPinHeader) != ""
		switch {
		case forwarded:
			decoded, decodeErr := hex.DecodeString(c.GetHeader(cluster.JoinerPinHeader))
			if decodeErr != nil || len(decoded) != len(pin) {
				writeJoinAdmissionError(c, fmt.Errorf("joining_node_cert_required"))
				return
			}
			copy(pin[:], decoded)
		case c.Request.TLS == nil || len(c.Request.TLS.PeerCertificates) == 0:
			writeJoinAdmissionError(c, fmt.Errorf("joining_node_cert_required"))
			return
		default:
			pin = cluster.CertPin(c.Request.TLS.PeerCertificates[0])
		}
		if r := cS.Raft(); !forwarded && r != nil && r.State() != raft.Leader {
			leaderAddress, _ := r.LeaderWithID()
			leaderAPI := resolveLeaderAPIForForward(cS, "", string(leaderAddress))
			if leaderAPI == "" {
				writeJoinAdmissionError(c, fmt.Errorf("not_leader"))
				return
			}
			response, err := clusterForwardHTTP(c.Request.Context(), http.MethodPost, leaderAPI+"/api/cluster/accept-join", body,
				map[string]string{
					"Accept": "application/json", "Content-Type": "application/json",
					auth.ClusterKeyHeader: c.GetString("ClusterKey"), cluster.JoinerPinHeader: hex.EncodeToString(pin[:]),
				}, clusterForwardTimeout)
			if err != nil {
				writeJoinAdmissionError(c, fmt.Errorf("join_forward_failed: %w", err))
				return
			}
			c.Data(response.StatusCode, "application/json", response.Body)
			return
		}
		status, err := cS.AdmitJoiner(c.Request.Context(), req, c.GetString("ClusterKey"), pin)
		if err != nil {
			writeJoinAdmissionError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, internal.APIResponse[cluster.ClusterJoinStatus]{
			Status: "success", Message: "cluster_join_started", Data: status,
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

// @Summary Leave Cluster
// @Description Remove this node from the cluster and clear its local cluster state. The node keeps its VMs, jails and containers.
// @Description A node with guests is refused with them listed unless retainGuests=true confirms it keeps them. Other dependencies still block.
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ResetNodeRequest false "Optional leave options"
// @Success 200 {object} internal.APIResponse[cluster.ClusterLeaveStatus] "Success"
// @Failure 202 {object} internal.APIResponse[cluster.ClusterLeaveStatus] "Leave pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[cluster.PeerRemovalConflict] "Leave conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/reset-node [delete]
func ResetRaftNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ResetNodeRequest
		if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if err := cS.Leave(c.Request.Context(), request.RetainGuests); err != nil {
			writeClusterLeaveError(c, cS, err)
			return
		}
		writeClusterLeaveSuccess(c, cS, "cluster_leave_completed")
	}
}

// @Summary Force Local Cluster Reset
// @Description Reset only the local cluster state after external fencing and membership repair acknowledgement
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ForceResetNodeRequest true "Force Local Reset Request"
// @Success 200 {object} internal.APIResponse[cluster.ClusterLeaveStatus] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/reset-node/force [delete]
func ForceResetRaftNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ForceResetNodeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		ack := ""
		switch {
		case strings.TrimSpace(request.NodeID) == "" || strings.TrimSpace(request.NodeID) != cS.LocalNodeID():
			ack = "cluster_force_local_node_id_mismatch"
		case !request.RemoteMembershipAcknowledged:
			ack = "cluster_force_membership_ack_required"
		case !request.WorkloadsExternallyFenced:
			ack = "cluster_force_workload_fence_ack_required"
		}
		if ack != "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: ack, Error: ack})
			return
		}
		if err := cS.ForceLocalReset(c.Request.Context()); err != nil {
			writeClusterLeaveError(c, cS, err)
			return
		}
		writeClusterLeaveSuccess(c, cS, "cluster_local_reset_forced")
	}
}

// @Summary Remove Peer
// @Description Remove a node from the cluster. It keeps its VMs, jails and containers and clears its cluster state once it learns it was removed.
// @Description A node with guests is refused with them listed unless retainGuests=true confirms it keeps them. Other dependencies still block.
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RemovePeerRequest true "Remove Peer Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[cluster.PeerRemovalConflict] "Peer owns cluster resources"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/remove-node [post]
func RemoveNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RemovePeerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if err := cS.RemoveMember(c.Request.Context(), req.NodeID, req.RetainGuests); err != nil {
			writeClusterLeaveError(c, cS, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "peer_removed"})
	}
}

// @Summary Force Remove Peer
// @Description Remove an unavailable peer the operator fenced externally. Its guests stay on it.
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ForceRemovePeerRequest true "Force Remove Peer Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[cluster.PeerRemovalConflict] "Conflict"
// @Failure 503 {object} internal.APIResponse[any] "Consensus unavailable"
// @Router /cluster/remove-node/force [post]
func ForceRemoveNode(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ForceRemovePeerRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if !request.TargetExternallyFenced {
			const ack = "cluster_force_target_fence_ack_required"
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: ack, Error: ack})
			return
		}
		if err := cS.RemoveMember(c.Request.Context(), request.NodeID, true); err != nil {
			writeClusterLeaveError(c, cS, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "peer_force_removed"})
	}
}

func MembershipStatus(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request MembershipStatusRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if c.Request.TLS == nil || len(c.Request.TLS.PeerCertificates) == 0 {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status: "error", Message: "cluster_node_cert_required", Error: "cluster_node_cert_required",
			})
			return
		}
		status, err := cS.AuthoritativeMembershipStatus(request.NodeID, cluster.CertPin(c.Request.TLS.PeerCertificates[0]))
		if err != nil {
			code := http.StatusServiceUnavailable
			message := "cluster_membership_status_unavailable"
			switch {
			case strings.Contains(err.Error(), "not_leader"):
				code, message = http.StatusConflict, "not_leader"
			case strings.Contains(err.Error(), "membership_status_cert_mismatch"):
				code, message = http.StatusForbidden, "membership_status_cert_mismatch"
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

// @Summary Cancel Cluster Leave
// @Description Abandon an unfinished leave and reopen this node. Refused once the leader has removed it.
// @Tags Cluster
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 409 {object} internal.APIResponse[any] "Leader already removed this node"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/leave/cancel [post]
func CancelLeave(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cS.CancelLeave(c.Request.Context()); err != nil {
			writeClusterLeaveError(c, cS, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "cluster_leave_cancelled"})
	}
}

func writeClusterLeaveSuccess(c *gin.Context, cS *cluster.Service, message string) {
	status, err := cS.LeaveStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status: "error", Message: "cluster_leave_status_failed", Error: err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, internal.APIResponse[cluster.ClusterLeaveStatus]{Status: "success", Message: message, Data: status})
}

func writeClusterLeaveError(c *gin.Context, cS *cluster.Service, err error) {
	var blocked *cluster.PeerRemovalBlockedError
	if errors.As(err, &blocked) {
		c.JSON(http.StatusConflict, internal.APIResponse[cluster.PeerRemovalConflict]{
			Status: "error", Message: "peer_removal_blocked", Error: err.Error(), Data: blocked.Conflict,
		})
		return
	}
	var leaveErr *cluster.ClusterLeaveError
	status, message := http.StatusInternalServerError, "cluster_leave_failed"
	switch {
	case errors.As(err, &leaveErr) && leaveErr.Code == "cluster_leave_membership_unconfirmed":
		leaveStatus, _ := cS.LeaveStatus()
		c.JSON(http.StatusAccepted, internal.APIResponse[cluster.ClusterLeaveStatus]{
			Status: "error", Message: leaveErr.Code, Error: err.Error(), Data: leaveStatus,
		})
		return
	case errors.As(err, &leaveErr):
		status, message = http.StatusConflict, leaveErr.Code
	case errors.Is(err, cluster.ErrPeerNotFound):
		status, message = http.StatusNotFound, "peer_not_found"
	case errors.Is(err, cluster.ErrRemoveSelf):
		status, message = http.StatusConflict, "peer_removal_target_is_local"
	case errors.Is(err, raft.ErrNotLeader):
		status, message = http.StatusConflict, "not_leader"
	case errors.Is(err, cluster.ErrNodeLeaveFenced):
		status, message = http.StatusLocked, cluster.ProposalErrorCode(err)
	case errors.Is(err, raft.ErrRaftShutdown), errors.Is(err, raft.ErrLeadershipLost), errors.Is(err, raft.ErrEnqueueTimeout):
		status, message = http.StatusServiceUnavailable, "cluster_consensus_unavailable"
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
}
