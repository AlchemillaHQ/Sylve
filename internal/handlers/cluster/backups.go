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
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	replicationServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/replication"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/replication"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type backupTargetRequest struct {
	Name        string `json:"name" binding:"required,min=2"`
	Endpoint    string `json:"endpoint" binding:"required,min=3"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type backupJobRequest struct {
	Name               string `json:"name" binding:"required,min=2"`
	TargetID           uint   `json:"targetId" binding:"required"`
	RunnerNodeID       string `json:"runnerNodeId"`
	Mode               string `json:"mode" binding:"required"`
	SourceDataset      string `json:"sourceDataset"`
	JailRootDataset    string `json:"jailRootDataset"`
	DestinationDataset string `json:"destinationDataset" binding:"required"`
	CronExpr           string `json:"cronExpr" binding:"required"`
	Force              bool   `json:"force"`
	WithIntermediates  bool   `json:"withIntermediates"`
	Enabled            *bool  `json:"enabled"`
}

type backupPullRequest struct {
	TargetID           uint   `json:"targetId" binding:"required"`
	RunnerNodeID       string `json:"runnerNodeId"`
	SourceDataset      string `json:"sourceDataset" binding:"required"`
	DestinationDataset string `json:"destinationDataset" binding:"required"`
	Snapshot           string `json:"snapshot"`
	Force              bool   `json:"force"`
	WithIntermediates  bool   `json:"withIntermediates"`
	Rollback           bool   `json:"rollback"`
}

func BackupTargets(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targets, err := cS.ListBackupTargets()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_targets_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupTarget]{
			Status:  "success",
			Message: "backup_targets_listed",
			Data:    targets,
		})
	}
}

func CreateBackupTarget(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req backupTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := cS.ProposeBackupTargetCreate(cluster.BackupTargetInput{
			Name:        req.Name,
			Endpoint:    req.Endpoint,
			Description: req.Description,
			Enabled:     req.Enabled,
		}, cS.Raft == nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_create_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_created",
			Data:    nil,
		})
	}
}

func UpdateBackupTarget(cS *cluster.Service) gin.HandlerFunc {
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

		var req backupTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = cS.ProposeBackupTargetUpdate(uint(id64), cluster.BackupTargetInput{
			Name:        req.Name,
			Endpoint:    req.Endpoint,
			Description: req.Description,
			Enabled:     req.Enabled,
		}, cS.Raft == nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_update_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_updated",
			Data:    nil,
		})
	}
}

func DeleteBackupTarget(cS *cluster.Service) gin.HandlerFunc {
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

		err = cS.ProposeBackupTargetDelete(uint(id64), cS.Raft == nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_deleted",
			Data:    nil,
		})
	}
}

func BackupJobs(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobs, err := cS.ListBackupJobs()
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

func CreateBackupJob(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req backupJobRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := cS.ProposeBackupJobCreate(cluster.BackupJobInput{
			Name:               req.Name,
			TargetID:           req.TargetID,
			RunnerNodeID:       req.RunnerNodeID,
			Mode:               req.Mode,
			SourceDataset:      req.SourceDataset,
			JailRootDataset:    req.JailRootDataset,
			DestinationDataset: req.DestinationDataset,
			CronExpr:           req.CronExpr,
			Force:              req.Force,
			WithIntermediates:  req.WithIntermediates,
			Enabled:            req.Enabled,
		}, cS.Raft == nil)
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

		var req backupJobRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = cS.ProposeBackupJobUpdate(uint(id64), cluster.BackupJobInput{
			Name:               req.Name,
			TargetID:           req.TargetID,
			RunnerNodeID:       req.RunnerNodeID,
			Mode:               req.Mode,
			SourceDataset:      req.SourceDataset,
			JailRootDataset:    req.JailRootDataset,
			DestinationDataset: req.DestinationDataset,
			CronExpr:           req.CronExpr,
			Force:              req.Force,
			WithIntermediates:  req.WithIntermediates,
			Enabled:            req.Enabled,
		}, cS.Raft == nil)
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

func BackupTargetDatasets(cS *cluster.Service, rS *replication.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		target, err := cS.GetBackupTargetByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		prefix := c.Query("prefix")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		datasets, err := rS.ListTargetDatasets(ctx, target.Endpoint, prefix)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_target_datasets_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]replication.DatasetInfo]{
			Status:  "success",
			Message: "target_datasets_listed",
			Data:    datasets,
		})
	}
}

func BackupTargetSnapshots(cS *cluster.Service, rS *replication.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		target, err := cS.GetBackupTargetByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		dataset := c.Query("dataset")
		if dataset == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "dataset_required",
				Error:   "dataset query parameter is required",
				Data:    nil,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		snapshots, err := rS.ListTargetSnapshots(ctx, target.Endpoint, dataset)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_target_snapshots_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]replication.SnapInfo]{
			Status:  "success",
			Message: "target_snapshots_listed",
			Data:    snapshots,
		})
	}
}

func BackupTargetStatus(cS *cluster.Service, rS *replication.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		target, err := cS.GetBackupTargetByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		limit := 50
		if q := c.Query("limit"); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil {
				limit = parsed
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		events, err := rS.ListTargetStatus(ctx, target.Endpoint, limit)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_target_status_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]replication.ReplicationEventInfo]{
			Status:  "success",
			Message: "target_status_listed",
			Data:    events,
		})
	}
}

func RunBackupJobNow(cS *cluster.Service, rS *replication.Service) gin.HandlerFunc {
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

		localNodeID := ""
		if detail := cS.Detail(); detail != nil {
			localNodeID = strings.TrimSpace(detail.NodeID)
		}

		runnerNodeID := strings.TrimSpace(job.RunnerNodeID)
		if runnerNodeID != "" && localNodeID != "" && runnerNodeID != localNodeID {
			body, statusCode, err := forwardBackupJobRunToRunner(cS, uint(id64), runnerNodeID)
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

		// Backward compatibility for legacy jobs without runner pinning.
		if runnerNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
		defer cancel()

		if err := rS.RunBackupJobByID(ctx, job.ID); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_job_run_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_run_started",
			Data:    nil,
		})
	}
}

func forwardBackupJobRunToRunner(cS *cluster.Service, jobID uint, runnerNodeID string) ([]byte, int, error) {
	nodes, err := cS.Nodes()
	if err != nil {
		return nil, 0, fmt.Errorf("list_cluster_nodes_failed: %w", err)
	}

	var targetAPI string
	for _, node := range nodes {
		if strings.TrimSpace(node.NodeUUID) == runnerNodeID {
			targetAPI = strings.TrimSpace(node.API)
			break
		}
	}

	if targetAPI == "" {
		if cS.Raft != nil {
			fut := cS.Raft.GetConfiguration()
			if err := fut.Error(); err == nil {
				for _, server := range fut.Configuration().Servers {
					if string(server.ID) != runnerNodeID {
						continue
					}

					host, _, err := net.SplitHostPort(string(server.Address))
					if err != nil {
						host = string(server.Address)
					}

					targetAPI = net.JoinHostPort(host, strconv.Itoa(config.ParsedConfig.Port))
					break
				}
			}
		}
	}

	if targetAPI == "" {
		return nil, 0, fmt.Errorf("backup_runner_node_not_found")
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return nil, 0, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	runURL := fmt.Sprintf("https://%s/api/cluster/backups/jobs/%d/run", targetAPI, jobID)
	body, statusCode, err := utils.HTTPPostJSONRead(runURL, map[string]any{}, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	var parsed internal.APIResponse[any]
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, statusCode, fmt.Errorf("invalid_runner_response")
	}

	return body, statusCode, nil
}

func BackupEvents(rS *replication.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 200
		if q := c.Query("limit"); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil {
				limit = parsed
			}
		}

		jobID := uint(0)
		if q := c.Query("jobId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				jobID = uint(parsed)
			}
		}

		events, err := rS.ListLocalBackupEvents(limit, jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]replication.ReplicationEventInfo]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

// @Summary Get Backup Events (Paginated)
// @Description Retrieve backup replication events with pagination support for remote tables
// @Tags Cluster
// @Accept json
// @Produce json
// @Param hash query string true "Auth hash"
// @Param page query int false "Page number (default 1)"
// @Param size query int false "Page size (default 25)"
// @Param sort[0][field] query string false "Field to sort by"
// @Param sort[0][dir] query string false "Sort direction (asc or desc)"
// @Param jobId query int false "Filter by job ID"
// @Param search query string false "Search term"
// @Success 200 {object} internal.APIResponse[replicationServiceInterfaces.BackupEventsResponse] "Paginated backup events"
// @Failure 500 {string} string "Internal server error"
// @Router /cluster/backups/events/remote [get]
func BackupEventsRemote(rS *replication.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pageStr := c.DefaultQuery("page", "1")
		sizeStr := c.DefaultQuery("size", "25")
		page, _ := strconv.Atoi(pageStr)
		size, _ := strconv.Atoi(sizeStr)

		sortField := c.Query("sort[0][field]")
		sortDir := c.Query("sort[0][dir]")

		jobID := uint(0)
		if q := c.Query("jobId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				jobID = uint(parsed)
			}
		}

		search := c.Query("search")

		events, err := rS.ListLocalBackupEventsPaginated(page, size, sortField, sortDir, jobID, search)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*replicationServiceInterfaces.BackupEventsResponse]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

func PullBackupDataset(cS *cluster.Service, rS *replication.Service, zS *zfsService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req backupPullRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		// Check if we need to forward to a different node
		localNodeID := ""
		if detail := cS.Detail(); detail != nil {
			localNodeID = strings.TrimSpace(detail.NodeID)
		}

		runnerNodeID := strings.TrimSpace(req.RunnerNodeID)
		if runnerNodeID != "" && localNodeID != "" && runnerNodeID != localNodeID {
			// Forward to the specified runner node
			body, statusCode, err := forwardBackupPullToRunner(cS, req, runnerNodeID)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_pull_remote_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		target, err := cS.GetBackupTargetByID(req.TargetID)
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Hour)
		defer cancel()

		plan, err := rS.PullDatasetFromNode(
			ctx,
			req.SourceDataset,
			req.DestinationDataset,
			target.Endpoint,
			req.Snapshot,
			req.Force,
			req.WithIntermediates,
		)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_pull_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		// If rollback is requested and we have a target snapshot, roll back to it
		if req.Rollback && plan.TargetSnapshot != "" {
			// plan.TargetSnapshot is in source dataset format "srcDataset@snapName"
			// We need to construct the local snapshot name: "dstDataset@snapName"
			parts := strings.SplitN(plan.TargetSnapshot, "@", 2)
			if len(parts) != 2 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_rollback_failed",
					Error:   "invalid_snapshot_name",
					Data:    plan,
				})
				return
			}
			localSnapshotName := req.DestinationDataset + "@" + parts[1]

			err := zS.RollbackSnapshotByName(ctx, localSnapshotName, true)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_rollback_failed",
					Error:   err.Error(),
					Data:    plan,
				})
				return
			}
			plan.Mode = "rollback"
		}

		c.JSON(http.StatusOK, internal.APIResponse[*replication.Plan]{
			Status:  "success",
			Message: "backup_pull_completed",
			Data:    plan,
		})
	}
}

func forwardBackupPullToRunner(cS *cluster.Service, req backupPullRequest, runnerNodeID string) ([]byte, int, error) {
	nodes, err := cS.Nodes()
	if err != nil {
		return nil, 0, fmt.Errorf("list_cluster_nodes_failed: %w", err)
	}

	var targetAPI string
	for _, node := range nodes {
		if strings.TrimSpace(node.NodeUUID) == runnerNodeID {
			targetAPI = strings.TrimSpace(node.API)
			break
		}
	}

	if targetAPI == "" {
		if cS.Raft != nil {
			fut := cS.Raft.GetConfiguration()
			if err := fut.Error(); err == nil {
				for _, server := range fut.Configuration().Servers {
					if string(server.ID) != runnerNodeID {
						continue
					}

					host, _, err := net.SplitHostPort(string(server.Address))
					if err != nil {
						host = string(server.Address)
					}

					targetAPI = net.JoinHostPort(host, strconv.Itoa(config.ParsedConfig.Port))
					break
				}
			}
		}
	}

	if targetAPI == "" {
		return nil, 0, fmt.Errorf("backup_runner_node_not_found")
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return nil, 0, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	// Clear the runner node ID to prevent infinite forwarding loops
	forwardReq := map[string]any{
		"targetId":           req.TargetID,
		"runnerNodeId":       "", // Clear to execute locally on the target node
		"sourceDataset":      req.SourceDataset,
		"destinationDataset": req.DestinationDataset,
		"snapshot":           req.Snapshot,
		"force":              req.Force,
		"withIntermediates":  req.WithIntermediates,
	}

	pullURL := fmt.Sprintf("https://%s/api/cluster/backups/pull", targetAPI)
	body, statusCode, err := utils.HTTPPostJSONRead(pullURL, forwardReq, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	return body, statusCode, nil
}
