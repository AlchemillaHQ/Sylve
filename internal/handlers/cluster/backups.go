// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

const (
	backupJobForwardHopHeader      = "X-Sylve-Backup-Forward-Hop"
	backupJobForwardedByHeader     = "X-Sylve-Backup-Forwarded-By"
	backupJobForwardedTargetHeader = "X-Sylve-Backup-Forward-Target"
	backupJobForwardMaxHops        = 1
)

var backupJobForwardHTTP = utils.HTTPPostJSONReadContext

type backupJobRunService interface {
	EnqueueBackupJob(context.Context, uint) error
}

type backupJobRestoreService interface {
	RegisterRestoreEncryptionKey(string, string) error
	EnqueueRestoreJob(context.Context, uint, string) error
}

type restoreBackupJobRequest struct {
	Snapshot            string `json:"snapshot"`
	EncryptionKey       string `json:"encryptionKey"`
	EncryptionKeyFormat string `json:"encryptionKeyFormat"`
}

type backupJobRunnerRoute struct {
	LocalNodeID  string
	RunnerNodeID string
	TargetAPI    string
	Hop          int
	Forward      bool
}

func BackupJobs(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID := uint(0)
		if q := c.Query("targetId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				targetID = uint(parsed)
			}
		}

		guestType := strings.TrimSpace(c.Query("guestType"))
		guestIDQuery := strings.TrimSpace(c.Query("guestId"))
		if (guestType == "") != (guestIDQuery == "") {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "guest_filter_incomplete",
				Error:   "guestType and guestId must be provided together",
				Data:    nil,
			})
			return
		}

		var jobs []clusterModels.BackupJob
		var err error
		if guestType != "" {
			guestID64, parseErr := strconv.ParseUint(guestIDQuery, 10, 64)
			if parseErr != nil || guestID64 == 0 || strings.ToLower(guestType) != clusterModels.ReplicationGuestTypeVM {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_guest_filter",
					Error:   "guestType must be vm and guestId must be a positive integer",
					Data:    nil,
				})
				return
			}
			jobs, err = cS.ListBackupJobsForGuest(targetID, guestType, uint(guestID64))
		} else {
			jobs, err = cS.ListBackupJobs(targetID)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_jobs_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupJob]{
			Status:  "success",
			Message: "backup_jobs_listed",
			Data:    jobs,
		})
	}
}

func BackupTargetRunningJobIDs(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_target_id",
				Error:   "invalid_target_id",
				Data:    nil,
			})
			return
		}

		ids, err := cS.RunningJobIDsForTarget(uint(id64))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "running_job_ids_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if ids == nil {
			ids = []uint{}
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]uint]{
			Status:  "success",
			Message: "running_job_ids_listed",
			Data:    ids,
		})
	}
}

func CreateBackupJob(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.BackupJobReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := cS.ProposeBackupJobCreateContext(c.Request.Context(), req, cS.Raft == nil)

		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_create_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_created",
			Data:    nil,
		})
	}
}

func UpdateBackupJob(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_job_id",
				Error:   "invalid_job_id",
				Data:    nil,
			})
			return
		}

		var req clusterServiceInterfaces.BackupJobReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = cS.ProposeBackupJobUpdateContext(
			c.Request.Context(),
			uint(id64),
			req,
			cS.Raft == nil,
			cluster.BackupJobPlacementAuthorization{},
		)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_update_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_updated",
			Data:    nil,
		})
	}
}

func DeleteBackupJob(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_job_id",
				Error:   "invalid_job_id",
				Data:    nil,
			})
			return
		}

		err = cS.ProposeBackupJobDelete(uint(id64), cS.Raft == nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_deleted",
			Data:    nil,
		})
	}
}

func RunBackupJobNow(cS *cluster.Service, zS backupJobRunService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_job_id",
				Error:   "invalid_job_id",
				Data:    nil,
			})
			return
		}

		job, err := cS.GetBackupJobByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		runnerNodeID := strings.TrimSpace(job.RunnerNodeID)
		route, err := resolveBackupJobRunnerRoute(c, cS, runnerNodeID)
		if err != nil {
			writeBackupJobRouteError(c, "backup_job_remote_run_failed", err)
			return
		}
		if route.Forward {
			body, statusCode, err := forwardBackupJobRequestToRunner(
				c,
				cS,
				route,
				fmt.Sprintf("/api/cluster/backups/jobs/run/%d", job.ID),
				map[string]any{},
			)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_job_remote_run_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			c.Data(statusCode, "application/json", body)
			return
		}

		if runnerNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if err := zS.EnqueueBackupJob(c.Request.Context(), job.ID); err != nil {
			status := http.StatusBadRequest
			msg := "backup_job_enqueue_failed"
			if strings.Contains(err.Error(), "already_running") {
				status = http.StatusConflict
				msg = "backup_job_already_running"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.Set("AuditAsyncJobID", job.ID)
		c.Set("AuditAsyncJobType", "backup_job_run")

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_run_started",
			Data:    nil,
		})
	}
}

func backupJobForwardHop(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.GetHeader(backupJobForwardHopHeader))
	if raw == "" {
		return 0, nil
	}
	hop, err := strconv.Atoi(raw)
	if err != nil || hop < 0 || hop > backupJobForwardMaxHops {
		return 0, fmt.Errorf("backup_job_forward_loop_detected")
	}
	return hop, nil
}

func resolveBackupJobRunnerRoute(
	c *gin.Context,
	cS *cluster.Service,
	runnerNodeID string,
) (backupJobRunnerRoute, error) {
	route := backupJobRunnerRoute{RunnerNodeID: strings.TrimSpace(runnerNodeID)}
	if cS == nil {
		return route, fmt.Errorf("cluster_service_unavailable")
	}
	hop, err := backupJobForwardHop(c)
	if err != nil {
		return route, err
	}
	route.Hop = hop
	if route.RunnerNodeID == "" {
		return route, nil
	}

	if cS.Raft != nil {
		route.LocalNodeID = strings.TrimSpace(cS.NodeID)
		if route.LocalNodeID == "" {
			return route, fmt.Errorf("backup_runner_local_node_id_unavailable")
		}
		route.TargetAPI, err = cS.ResolveIntraClusterVoterAPI(route.RunnerNodeID)
		if err != nil {
			return route, err
		}
	} else {
		route.LocalNodeID = strings.TrimSpace(cS.LocalNodeID())
		if route.LocalNodeID == "" {
			return route, fmt.Errorf("backup_runner_local_node_id_unavailable")
		}
	}

	if route.RunnerNodeID == route.LocalNodeID {
		return route, nil
	}
	if route.Hop >= backupJobForwardMaxHops {
		return route, fmt.Errorf("backup_job_forward_loop_detected")
	}
	if cS.Raft == nil {
		return route, fmt.Errorf("backup_runner_raft_unavailable")
	}
	route.Forward = true
	return route, nil
}

func writeBackupJobRouteError(c *gin.Context, message string, err error) {
	status := http.StatusBadGateway
	errorText := "backup_job_forward_failed"
	if err != nil {
		errorText = err.Error()
		text := strings.ToLower(errorText)
		switch {
		case strings.Contains(text, "forward_loop"):
			status = http.StatusLoopDetected
			message = "backup_job_forward_loop_detected"
		case strings.Contains(text, "local_node_id_unavailable"):
			status = http.StatusServiceUnavailable
			message = "backup_runner_local_identity_unavailable"
		case strings.Contains(text, "not_raft_member"),
			strings.Contains(text, "not_raft_voter"),
			strings.Contains(text, "raft_unavailable"):
			status = http.StatusServiceUnavailable
			message = "backup_runner_unavailable"
		}
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errorText,
		Data:    nil,
	})
}

func forwardBackupJobRequestToRunner(
	c *gin.Context,
	cS *cluster.Service,
	route backupJobRunnerRoute,
	path string,
	payload any,
) ([]byte, int, error) {
	if !route.Forward || strings.TrimSpace(route.TargetAPI) == "" {
		return nil, 0, fmt.Errorf("backup_job_forward_route_invalid")
	}
	if cS == nil || cS.AuthService == nil {
		return nil, 0, fmt.Errorf("backup_job_forward_auth_service_unavailable")
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

	requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetString("RequestID"))
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Header("X-Request-ID", requestID)
	headers := map[string]string{
		"Accept":                       "application/json",
		"Content-Type":                 "application/json",
		"X-Cluster-Token":              fmt.Sprintf("Bearer %s", clusterToken),
		"X-Request-ID":                 requestID,
		backupJobForwardHopHeader:      strconv.Itoa(route.Hop + 1),
		backupJobForwardedByHeader:     route.LocalNodeID,
		backupJobForwardedTargetHeader: route.RunnerNodeID,
	}
	for _, header := range []string{"X-Correlation-ID", "Traceparent"} {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			headers[header] = value
		}
	}

	forwardURL := fmt.Sprintf("https://%s%s", route.TargetAPI, path)
	body, statusCode, err := backupJobForwardHTTP(c.Request.Context(), forwardURL, payload, headers)
	if err != nil {
		var statusErr *utils.HTTPStatusError
		if errors.As(err, &statusErr) {
			if statusCode == 0 {
				statusCode = statusErr.StatusCode
			}
			if body == nil {
				body = append([]byte(nil), statusErr.Body...)
			}
			return body, statusCode, nil
		}
		return nil, statusCode, err
	}
	if statusCode < 100 {
		return nil, statusCode, fmt.Errorf("backup_job_forward_response_status_invalid")
	}
	return body, statusCode, nil
}

func forwardBackupTargetRestoreToNode(c *gin.Context, cS *cluster.Service, targetID uint, restoreNodeID string, payload map[string]any) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, restoreNodeID)
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

	restoreURL := fmt.Sprintf("https://%s/api/cluster/backups/targets/%d/restore", targetAPI, targetID)
	body, statusCode, err := utils.HTTPPostJSONRead(restoreURL, payload, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	return body, statusCode, nil
}

func resolveClusterNodeAPI(cS *cluster.Service, nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("restore_runner_node_not_found")
	}

	nodes, err := cS.Nodes()
	if err != nil {
		return "", fmt.Errorf("list_cluster_nodes_failed: %w", err)
	}

	var targetAPI string
	for _, node := range nodes {
		if strings.TrimSpace(node.NodeUUID) == nodeID {
			targetAPI = strings.TrimSpace(node.API)
			break
		}
	}

	if targetAPI == "" {
		if cS.Raft != nil {
			fut := cS.Raft.GetConfiguration()
			if err := fut.Error(); err == nil {
				for _, server := range fut.Configuration().Servers {
					if string(server.ID) != nodeID {
						continue
					}

					host, _, err := net.SplitHostPort(string(server.Address))
					if err != nil {
						host = string(server.Address)
					}

					targetAPI = net.JoinHostPort(host, strconv.Itoa(cluster.ClusterEmbeddedHTTPSPort))
					break
				}
			}
		}
	}

	if targetAPI == "" {
		return "", fmt.Errorf("backup_runner_node_not_found")
	}

	return targetAPI, nil
}

func extractGuestFromDatasetPath(dataset string) (string, uint) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return "", 0
	}

	parts := strings.Split(strings.Trim(dataset, "/"), "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		segment := strings.TrimSpace(parts[idx])
		if segment != "jails" && segment != "virtual-machines" {
			continue
		}

		raw := strings.TrimSpace(parts[idx+1])
		if raw == "" {
			continue
		}

		cutAt := len(raw)
		if split := strings.IndexAny(raw, "._"); split > 0 && split < cutAt {
			cutAt = split
		}
		raw = strings.TrimSpace(raw[:cutAt])
		if raw == "" {
			continue
		}

		guestID, err := strconv.ParseUint(raw, 10, 64)
		if err == nil && guestID > 0 {
			if segment == "jails" {
				return clusterModels.BackupJobModeJail, uint(guestID)
			}
			return clusterModels.BackupJobModeVM, uint(guestID)
		}
	}

	return "", 0
}

func containsGuestID(guestIDs []uint, guestID uint) bool {
	if guestID == 0 {
		return false
	}
	for _, existing := range guestIDs {
		if existing == guestID {
			return true
		}
	}
	return false
}

func validateGuestIDRestorePlacement(cS *cluster.Service, guestID uint, restoreNodeID string) error {
	if cS == nil || guestID == 0 {
		return nil
	}

	details, err := cS.GetClusterDetails()
	if err != nil {
		return fmt.Errorf("load_cluster_details_failed: %w", err)
	}
	if details == nil {
		return nil
	}

	restoreNodeID = strings.TrimSpace(restoreNodeID)

	matches := make([]string, 0)
	conflicts := make([]string, 0)
	for _, node := range details.Nodes {
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" || !containsGuestID(node.GuestIDs, guestID) {
			continue
		}
		matches = append(matches, nodeID)
		if restoreNodeID == "" || nodeID != restoreNodeID {
			conflicts = append(conflicts, nodeID)
		}
	}

	if len(matches) == 0 {
		return nil
	}

	if len(conflicts) > 0 {
		return fmt.Errorf("guest_id_%d_already_registered_on_other_nodes: %s", guestID, strings.Join(conflicts, ","))
	}
	if len(matches) > 1 {
		return fmt.Errorf("guest_id_%d_registered_on_multiple_nodes: %s", guestID, strings.Join(matches, ","))
	}

	return nil
}

func BackupJobSnapshots(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_job_id",
				Error:   "invalid_job_id",
				Data:    nil,
			})
			return
		}

		job, err := cS.GetBackupJobByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		snapshots, err := zS.ListRemoteSnapshots(ctx, job)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_snapshots_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zelta.SnapshotInfo]{
			Status:  "success",
			Message: "snapshots_listed",
			Data:    snapshots,
		})
	}
}

func RestoreBackupJob(cS *cluster.Service, zS backupJobRestoreService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_job_id",
				Error:   "invalid_job_id",
				Data:    nil,
			})
			return
		}

		var req restoreBackupJobRequest
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Snapshot) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "snapshot_required",
				Error:   "snapshot field is required",
				Data:    nil,
			})
			return
		}

		job, err := cS.GetBackupJobByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		runnerNodeID := strings.TrimSpace(job.RunnerNodeID)
		route, err := resolveBackupJobRunnerRoute(c, cS, runnerNodeID)
		if err != nil {
			writeBackupJobRouteError(c, "backup_job_remote_restore_failed", err)
			return
		}
		if route.Forward {
			body, statusCode, err := forwardBackupJobRequestToRunner(
				c,
				cS,
				route,
				fmt.Sprintf("/api/cluster/backups/jobs/%d/restore", job.ID),
				req,
			)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_job_remote_restore_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			c.Data(statusCode, "application/json", body)
			return
		}

		if runnerNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			body, marshalErr := json.Marshal(req)
			if marshalErr != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "restore_forward_encode_failed", Error: marshalErr.Error(), Data: nil,
				})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Request.ContentLength = int64(len(body))
			forwardToLeader(c, cS)
			return
		}

		if job.Mode == clusterModels.BackupJobModeJail || job.Mode == clusterModels.BackupJobModeVM {
			_, guestID := extractGuestFromDatasetPath(job.JailRootDataset)
			if guestID == 0 {
				_, guestID = extractGuestFromDatasetPath(job.SourceDataset)
			}

			restoreNodeID := runnerNodeID
			if restoreNodeID == "" {
				if cS.Raft != nil {
					_, leaderID := cS.Raft.LeaderWithID()
					restoreNodeID = strings.TrimSpace(string(leaderID))
				}
				if restoreNodeID == "" {
					if detail := cS.Detail(); detail != nil {
						restoreNodeID = strings.TrimSpace(detail.NodeID)
					}
				}
			}

			if guestID > 0 {
				if err := validateGuestIDRestorePlacement(cS, guestID, restoreNodeID); err != nil {
					status := http.StatusConflict
					message := "restore_guest_id_conflict"
					if strings.Contains(err.Error(), "load_cluster_details_failed") {
						status = http.StatusInternalServerError
						message = "restore_precheck_failed"
					}

					c.JSON(status, internal.APIResponse[any]{
						Status:  "error",
						Message: message,
						Error:   err.Error(),
						Data:    nil,
					})
					return
				}
			}
		}

		if err := zS.RegisterRestoreEncryptionKey(req.EncryptionKey, req.EncryptionKeyFormat); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "restore_encryption_key_register_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := zS.EnqueueRestoreJob(c.Request.Context(), job.ID, req.Snapshot); err != nil {
			status := http.StatusBadRequest
			msg := "restore_enqueue_failed"
			errorText := strings.ToLower(err.Error())
			switch {
			case strings.Contains(errorText, "already_running"):
				status = http.StatusConflict
				msg = "backup_job_already_running"
			case strings.Contains(errorText, "async_audit_"),
				strings.Contains(errorText, "restore_event_"):
				status = http.StatusInternalServerError
				msg = "restore_observability_unavailable"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "restore_job_started",
			Data:    nil,
		})
	}
}

func UpdateBackupJobStateInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req cluster.BackupJobRuntimeStateUpdate
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.UpdateBackupJobRuntimeState(req, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "update_backup_job_state_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_state_updated",
			Data:    nil,
		})
	}
}

func UpdateBackupJobFriendlySourceInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req cluster.BackupJobFriendlySourceUpdate
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.SyncBackupJobFriendlySourceByGuest(req, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_friendly_source_update_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_friendly_source_updated",
			Data:    nil,
		})
	}
}
