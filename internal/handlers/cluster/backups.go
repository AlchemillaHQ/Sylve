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
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

const (
	backupJobForwardHopHeader      = clusterForwardHopHeader
	backupJobForwardedByHeader     = "X-Sylve-Backup-Forwarded-By"
	backupJobForwardedTargetHeader = "X-Sylve-Backup-Forward-Target"
	backupJobForwardMaxHops        = clusterForwardMaxHops
	backupJobMaxSafeQueryID        = 1<<53 - 1
)

var backupJobForwardHTTP = func(
	ctx context.Context,
	targetURL string,
	payload any,
	headers map[string]string,
) (clusterForwardResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	return clusterForwardHTTP(
		ctx,
		http.MethodPost,
		targetURL,
		body,
		headers,
		clusterForwardTimeout(clusterForwardDurable),
	)
}

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

func canonicalRestoreSnapshot(raw string) (string, error) {
	if strings.LastIndex(strings.TrimSpace(raw), "@") > 0 {
		snapshot, err := remoteexec.ParseZFSSnapshot(raw)
		if err != nil {
			return "", err
		}
		return snapshot.String(), nil
	}
	snapshot, err := remoteexec.ParseZFSSnapshotName(raw)
	if err != nil {
		return "", err
	}
	return snapshot.WithAt(), nil
}

type backupJobRunnerRoute struct {
	LocalNodeID  string
	RunnerNodeID string
	TargetAPI    string
	Hop          int
	Forward      bool
}

func backupJobTargetIDQuery(c *gin.Context) (uint, error) {
	values, present := c.Request.URL.Query()["targetId"]
	if !present {
		return 0, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, fmt.Errorf("targetId must be provided once as a positive JavaScript-safe integer")
	}

	parsed, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || parsed == 0 || parsed > backupJobMaxSafeQueryID {
		return 0, fmt.Errorf("targetId must be provided once as a positive JavaScript-safe integer")
	}
	return uint(parsed), nil
}

func BackupJobs(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID, err := backupJobTargetIDQuery(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_target_filter",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		guestType := strings.ToLower(strings.TrimSpace(c.Query("guestType")))
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
		if guestType != "" {
			guestID64, parseErr := strconv.ParseUint(guestIDQuery, 10, 64)
			if parseErr != nil || guestID64 == 0 ||
				(guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail) {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_guest_filter",
					Error:   "guestType must be vm or jail and guestId must be a positive integer",
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
			status := http.StatusBadRequest
			message := "backup_job_create_failed"
			if strings.Contains(err.Error(), "backup_job_id_conflict") {
				status = http.StatusConflict
				message = "backup_job_id_conflict"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
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
			status := http.StatusBadRequest
			message := "backup_job_update_failed"
			switch {
			case strings.Contains(err.Error(), "guest_identity_inventory_unavailable"):
				status = http.StatusServiceUnavailable
				message = "backup_job_runner_inventory_unavailable"
			case strings.Contains(err.Error(), "backup_job_not_found"):
				status = http.StatusNotFound
				message = "backup_job_not_found"
			case strings.Contains(err.Error(), "guest_identity_inventory_conflict"),
				strings.Contains(err.Error(), "backup_job_runner_rebind_pending"):
				status = http.StatusConflict
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
			response, err := forwardBackupJobRequestToRunner(
				c,
				cS,
				route,
				fmt.Sprintf("/api/cluster/backups/jobs/run/%d", job.ID),
				map[string]any{},
			)
			if err != nil {
				writeClusterForwardError(c, "backup_job_remote_run_failed", err)
				return
			}
			writeClusterForwardResponse(c, response)
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
	hop, err := currentClusterForwardHop(c)
	if err != nil {
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
) (clusterForwardResponse, error) {
	if !route.Forward || strings.TrimSpace(route.TargetAPI) == "" {
		return clusterForwardResponse{}, fmt.Errorf("backup_job_forward_route_invalid")
	}

	headers, err := clusterForwardHeaders(c, cS, route.Hop+1)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	headers[backupJobForwardedByHeader] = route.LocalNodeID
	headers[backupJobForwardedTargetHeader] = route.RunnerNodeID

	forwardURL := fmt.Sprintf("https://%s%s", route.TargetAPI, path)
	response, err := backupJobForwardHTTP(c.Request.Context(), forwardURL, payload, headers)
	if err != nil {
		return clusterForwardResponse{}, err
	}
	if response.StatusCode < 100 || response.StatusCode > 599 {
		return clusterForwardResponse{}, fmt.Errorf("backup_job_forward_response_status_invalid")
	}
	return response, nil
}

func forwardBackupTargetRestoreToNode(
	c *gin.Context,
	cS *cluster.Service,
	targetID uint,
	restoreNodeID string,
	payload map[string]any,
) (clusterForwardResponse, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, restoreNodeID)
	if err != nil {
		return clusterForwardResponse{}, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return clusterForwardResponse{}, err
	}

	restoreURL := fmt.Sprintf("https://%s/api/cluster/backups/targets/%d/restore", targetAPI, targetID)
	return performClusterForward(
		c,
		cS,
		http.MethodPost,
		restoreURL,
		body,
		clusterForwardDurable,
	)
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
		req.Snapshot, err = canonicalRestoreSnapshot(req.Snapshot)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "snapshot_invalid", Error: err.Error(), Data: nil,
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
			response, err := forwardBackupJobRequestToRunner(
				c,
				cS,
				route,
				fmt.Sprintf("/api/cluster/backups/jobs/%d/restore", job.ID),
				req,
			)
			if err != nil {
				writeClusterForwardError(c, "backup_job_remote_restore_failed", err)
				return
			}
			writeClusterForwardResponse(c, response)
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
			guestType, guestID := extractGuestFromDatasetPath(job.JailRootDataset)
			if guestID == 0 {
				guestType, guestID = extractGuestFromDatasetPath(job.SourceDataset)
			}

			restoreNodeID := runnerNodeID
			if restoreNodeID == "" {
				if cS.Raft != nil {
					_, leaderID := cS.Raft.LeaderWithID()
					restoreNodeID = strings.TrimSpace(string(leaderID))
				}
				if restoreNodeID == "" {
					restoreNodeID = strings.TrimSpace(cS.NodeID)
				}
				if restoreNodeID == "" {
					if detail := cS.Detail(); detail != nil {
						restoreNodeID = strings.TrimSpace(detail.NodeID)
					}
				}
			}

			if guestID > 0 {
				if err := cS.RequireGuestRestorePlacement(
					c.Request.Context(),
					guestType,
					guestID,
					restoreNodeID,
				); err != nil {
					status := http.StatusConflict
					message := "restore_guest_id_conflict"
					switch {
					case strings.Contains(err.Error(), "guest_identity_inventory_unavailable"):
						status = http.StatusServiceUnavailable
						message = "restore_guest_identity_unavailable"
					case strings.Contains(err.Error(), "guest_identity_inventory_scan_failed"):
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

		bypassRaft, authorityErr := cS.RuntimeStateBypassRaft()
		if authorityErr != nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status: "error", Message: "update_backup_job_state_failed", Error: authorityErr.Error(),
			})
			return
		}
		if err := cS.UpdateBackupJobRuntimeState(req, bypassRaft); err != nil {
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
