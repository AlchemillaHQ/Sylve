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
	"net/http"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/replication"
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
	Mode               string `json:"mode" binding:"required"`
	SourceDataset      string `json:"sourceDataset"`
	JailRootDataset    string `json:"jailRootDataset"`
	DestinationDataset string `json:"destinationDataset" binding:"required"`
	CronExpr           string `json:"cronExpr" binding:"required"`
	Force              bool   `json:"force"`
	WithIntermediates  bool   `json:"withIntermediates"`
	Enabled            bool   `json:"enabled"`
}

type backupPullRequest struct {
	TargetID           uint   `json:"targetId" binding:"required"`
	SourceDataset      string `json:"sourceDataset" binding:"required"`
	DestinationDataset string `json:"destinationDataset" binding:"required"`
	Snapshot           string `json:"snapshot"`
	Force              bool   `json:"force"`
	WithIntermediates  bool   `json:"withIntermediates"`
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		err := cS.ProposeBackupJobCreate(cluster.BackupJobInput{
			Name:               req.Name,
			TargetID:           req.TargetID,
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		err = cS.ProposeBackupJobUpdate(uint(id64), cluster.BackupJobInput{
			Name:               req.Name,
			TargetID:           req.TargetID,
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
		defer cancel()

		if err := rS.RunBackupJobByID(ctx, uint(id64)); err != nil {
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

func PullBackupDataset(cS *cluster.Service, rS *replication.Service) gin.HandlerFunc {
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

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
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

		c.JSON(http.StatusOK, internal.APIResponse[*replication.Plan]{
			Status:  "success",
			Message: "backup_pull_completed",
			Data:    plan,
		})
	}
}
