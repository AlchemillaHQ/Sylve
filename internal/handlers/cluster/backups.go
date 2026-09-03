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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
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

type RestoreBackupJobRequest struct {
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

func writeBackupJobError(c *gin.Context, operation string, err error) {
	status := http.StatusInternalServerError
	message := operation
	detail := operation
	errorText := ""
	if err != nil {
		errorText = strings.ToLower(err.Error())
	}

	var validationRejected *cluster.BackupTargetValidationRejectedError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), strings.Contains(errorText, "backup_job_not_found"):
		status = http.StatusNotFound
		message = "backup_job_not_found"
		detail = message
	case strings.Contains(errorText, "backup_target_not_found"):
		status = http.StatusNotFound
		message = "backup_target_not_found"
		detail = message
	case strings.Contains(errorText, "backup_job_already_running"),
		strings.Contains(errorText, "backup_job_running"):
		status = http.StatusConflict
		message = "backup_job_already_running"
		detail = message
	case strings.Contains(errorText, "backup_job_id_conflict"):
		status = http.StatusConflict
		message = "backup_job_id_conflict"
		detail = message
	case strings.Contains(errorText, "guest_identity_inventory_unavailable"),
		strings.Contains(errorText, "guest_identity_registry_initializing"),
		strings.Contains(errorText, "guest_identity_cluster_formation_in_progress"),
		strings.Contains(errorText, "cluster_consensus_unavailable"),
		strings.Contains(errorText, "backup_job_runner_placement_invalid") &&
			(errors.Is(err, raft.ErrNotLeader) || errors.Is(err, raft.ErrLeadershipLost)):
		status = http.StatusServiceUnavailable
		message = "backup_job_runner_inventory_unavailable"
		detail = message
	case strings.Contains(errorText, "backup_target_disabled"),
		strings.Contains(errorText, "_conflict"),
		strings.Contains(errorText, "_immutable"),
		strings.Contains(errorText, "dest_suffix_already_in_use"),
		strings.Contains(errorText, "inventory_conflict"),
		strings.Contains(errorText, "guest_operation"),
		strings.Contains(errorText, "guest_transition"),
		strings.Contains(errorText, "runner_rebind_pending"),
		strings.Contains(errorText, "not_guest_owner"),
		strings.Contains(errorText, "owner_epoch"),
		strings.Contains(errorText, "repair_required"),
		strings.Contains(errorText, "placement_changed"),
		strings.Contains(errorText, "runner_mismatch"),
		strings.Contains(errorText, "placement_state_mismatch"),
		errors.Is(err, raft.ErrNotLeader),
		errors.Is(err, raft.ErrLeadershipLost):
		status = http.StatusConflict
		if errors.Is(err, raft.ErrNotLeader) || errors.Is(err, raft.ErrLeadershipLost) {
			message = "cluster_leadership_changed"
			detail = message
		} else if err != nil {
			detail = err.Error()
		}
	case errors.As(err, &validationRejected),
		strings.Contains(errorText, "invalid_"),
		strings.Contains(errorText, "_invalid"),
		strings.Contains(errorText, "_required"),
		strings.Contains(errorText, "not_supported"),
		strings.Contains(errorText, "backup_runner_not_raft_member"),
		strings.Contains(errorText, "backup_runner_not_raft_voter"):
		status = http.StatusBadRequest
		if err != nil {
			detail = err.Error()
		}
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, raft.ErrRaftShutdown),
		errors.Is(err, raft.ErrEnqueueTimeout),
		strings.Contains(errorText, "raft_not_initialized"),
		strings.Contains(errorText, "raft_apply_failed"),
		strings.Contains(errorText, "leader_not_available"),
		strings.Contains(errorText, "barrier_failed"),
		strings.Contains(errorText, "backup_runner_raft"),
		strings.Contains(errorText, "backup_runner_auth_service_unavailable"),
		strings.Contains(errorText, "backup_runner_cluster_token_failed"),
		strings.Contains(errorText, "backup_runner_api_resolve_failed"),
		strings.Contains(errorText, "backup_runner_validation_request_failed"),
		strings.Contains(errorText, "backup_runner_validation_non_success"),
		strings.Contains(errorText, "backup_runner_validation_failed"),
		strings.Contains(errorText, "backup_runner_local_node_id_unavailable"):
		status = http.StatusServiceUnavailable
		message = "backup_job_service_unavailable"
		detail = message
	default:
		logger.L.Error().Err(err).Str("operation", operation).Msg("backup_job_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   detail,
		Data:    nil,
	})
}

func writeBackupNodeForwardError(
	c *gin.Context,
	message string,
	notFoundMessage string,
	err error,
) {
	if err != nil {
		errorText := strings.ToLower(err.Error())
		if strings.Contains(errorText, "runner_node_not_found") ||
			strings.Contains(errorText, "restore_runner_node_not_found") {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status: "error", Message: notFoundMessage, Error: notFoundMessage, Data: nil,
			})
			return
		}
	}
	writeClusterForwardError(c, message, err)
}

// @Summary List Backup Jobs
// @Description List backup jobs, optionally filtered by target or guest
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId query int false "Backup Target ID"
// @Param guestType query string false "Guest type (vm or jail)"
// @Param guestId query int false "Guest ID"
// @Success 200 {object} internal.APIResponse[[]clusterModels.BackupJob] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/backups/jobs [get]
func BackupJobs(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

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
			writeBackupJobError(c, "list_backup_jobs_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupJob]{
			Status:  "success",
			Message: "backup_jobs_listed",
			Data:    jobs,
		})
	}
}

// @Summary List Running Jobs for a Backup Target
// @Description List IDs of jobs currently running against a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Success 200 {object} internal.APIResponse[[]uint] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/backups/targets/{id}/running-jobs [get]
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
			writeBackupJobError(c, "running_job_ids_failed", err)
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

// @Summary Create a Backup Job
// @Description Create a scheduled backup job
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body clusterServiceInterfaces.BackupJobReq true "Backup Job Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/backups/jobs [post]
func CreateBackupJob(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.BackupJobReq
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		err := cS.ProposeBackupJobCreateContext(c.Request.Context(), req, cS.Raft == nil)

		if err != nil {
			writeBackupJobError(c, "backup_job_create_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_created",
			Data:    nil,
		})
	}
}

// @Summary Update a Backup Job
// @Description Update a scheduled backup job
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Job ID"
// @Param request body clusterServiceInterfaces.BackupJobReq true "Backup Job Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Job or Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/backups/jobs/{id} [put]
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
			writeClusterJSONBindError(c, err, "invalid_request")
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
			writeBackupJobError(c, "backup_job_update_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_updated",
			Data:    nil,
		})
	}
}

// @Summary Delete a Backup Job
// @Description Delete a backup job when it is not running
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Job ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Job Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Job Running"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/backups/jobs/{id} [delete]
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
			writeBackupJobError(c, "backup_job_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_deleted",
			Data:    nil,
		})
	}
}

// @Summary Run a Backup Job
// @Description Queue an immediate run of a backup job on its assigned runner
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Job ID"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Job or Runner Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Job Already Running"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Runner Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Runner Timeout"
// @Router /cluster/backups/jobs/{id}/run [post]
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
			writeBackupJobError(c, "backup_job_lookup_failed", err)
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
				fmt.Sprintf("/api/cluster/backups/jobs/%d/run", job.ID),
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
			writeBackupJobError(c, "backup_job_enqueue_failed", err)
			return
		}

		c.Set("AuditAsyncJobID", job.ID)
		c.Set("AuditAsyncJobType", "backup_job_run")

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
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

// @Summary List Backup Job Snapshots
// @Description List restorable snapshots produced by a backup job
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Job ID"
// @Success 200 {object} internal.APIResponse[[]zelta.SnapshotInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Job Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Target Failure"
// @Failure 503 {object} internal.APIResponse[any] "Local Key Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Target Timeout"
// @Router /cluster/backups/jobs/{id}/snapshots [get]
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
			writeBackupJobError(c, "backup_job_lookup_failed", err)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		snapshots, err := zS.ListRemoteSnapshots(ctx, job)
		if err != nil {
			writeBackupTargetRemoteReadError(c, "list_snapshots_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zelta.SnapshotInfo]{
			Status:  "success",
			Message: "snapshots_listed",
			Data:    snapshots,
		})
	}
}

// @Summary Restore a Backup Job Snapshot
// @Description Queue restoration of a snapshot produced by a backup job
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Job ID"
// @Param request body RestoreBackupJobRequest true "Restore Request"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Job or Runner Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Restore Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Runner Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Runner Timeout"
// @Router /cluster/backups/jobs/{id}/restore [post]
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

		var req RestoreBackupJobRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}
		if strings.TrimSpace(req.Snapshot) == "" {
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
			writeBackupJobError(c, "backup_job_lookup_failed", err)
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
					detail := err.Error()
					switch {
					case strings.Contains(err.Error(), "guest_identity_inventory_unavailable"),
						strings.Contains(err.Error(), "guest_identity_registry_initializing"),
						strings.Contains(err.Error(), "guest_identity_cluster_formation_in_progress"),
						strings.Contains(err.Error(), "cluster_consensus_unavailable"),
						errors.Is(err, raft.ErrNotLeader),
						errors.Is(err, raft.ErrLeadershipLost),
						errors.Is(err, raft.ErrRaftShutdown),
						errors.Is(err, raft.ErrEnqueueTimeout):
						status = http.StatusServiceUnavailable
						message = "restore_guest_identity_unavailable"
					case strings.Contains(err.Error(), "guest_identity_inventory_scan_failed"):
						status = http.StatusInternalServerError
						message = "restore_precheck_failed"
						detail = message
						logger.L.Error().Err(err).Uint("job_id", job.ID).Msg("backup_restore_precheck_failed")
					}

					c.JSON(status, internal.APIResponse[any]{
						Status:  "error",
						Message: message,
						Error:   detail,
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
			errorText := strings.ToLower(err.Error())
			if strings.Contains(errorText, "async_audit_") ||
				strings.Contains(errorText, "restore_event_") {
				logger.L.Error().Err(err).Uint("job_id", job.ID).Msg("backup_restore_observability_failed")
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "restore_observability_unavailable",
					Error: "restore_observability_unavailable", Data: nil,
				})
				return
			}
			writeBackupJobError(c, "restore_enqueue_failed", err)
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
