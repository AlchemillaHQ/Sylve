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
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

type backupJobRequest struct {
	Name             string `json:"name" binding:"required,min=2"`
	TargetID         uint   `json:"targetId" binding:"required"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode" binding:"required"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	DestSuffix       string `json:"destSuffix"`
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	CronExpr         string `json:"cronExpr"`
	Enabled          *bool  `json:"enabled"`
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

func CreateBackupTarget(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.BackupTargetReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = 22
		}

		sshKeyPath := ""
		if strings.TrimSpace(req.SSHKey) != "" {
			tmpID := uint(time.Now().UnixNano() % 1000000)
			path, err := zelta.SaveSSHKey(tmpID, req.SSHKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status:  "error",
					Message: "save_ssh_key_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			sshKeyPath = path
		}

		testTarget := &clusterModels.BackupTarget{
			SSHHost:    strings.TrimSpace(req.SSHHost),
			SSHPort:    sshPort,
			SSHKeyPath: sshKeyPath,
			BackupRoot: strings.TrimSpace(req.BackupRoot),
		}

		validateCtx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		if err := zS.ValidateTarget(validateCtx, testTarget); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "target_validation_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		req.SSHKeyPath = sshKeyPath

		err := cS.ProposeBackupTargetCreate(req, cS.Raft == nil)

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

func UpdateBackupTarget(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
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

		var req clusterServiceInterfaces.BackupTargetReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = 22
		}

		existing, err := cS.GetBackupTargetByID(uint(id64))
		if err != nil {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "backup_target_not_found",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		sshKeyPath := existing.SSHKeyPath
		if strings.TrimSpace(req.SSHKey) != "" {
			path, err := zelta.SaveSSHKey(uint(id64), req.SSHKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status:  "error",
					Message: "save_ssh_key_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			sshKeyPath = path
		}

		testTarget := &clusterModels.BackupTarget{
			SSHHost:    strings.TrimSpace(req.SSHHost),
			SSHPort:    sshPort,
			SSHKeyPath: sshKeyPath,
			BackupRoot: strings.TrimSpace(req.BackupRoot),
		}

		validateCtx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		if err := zS.ValidateTarget(validateCtx, testTarget); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "target_validation_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		sshKeyData := strings.TrimSpace(req.SSHKey)
		if sshKeyData == "" {
			sshKeyData = existing.SSHKey
		}

		req.SSHKeyPath = sshKeyPath
		req.SSHKey = sshKeyData
		req.ID = uint(id64)

		err = cS.ProposeBackupTargetUpdate(req, cS.Raft == nil)
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

		zelta.RemoveSSHKey(uint(id64))

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_deleted",
			Data:    nil,
		})
	}
}

func ValidateBackupTarget(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		if err := zS.ValidateTarget(ctx, target); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "target_validation_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "target_validated",
			Data:    nil,
		})
	}
}

func BackupTargetDatasets(zS *zelta.Service) gin.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()

		datasets, err := zS.ListRemoteTargetDatasets(ctx, uint(id64))
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_target_datasets_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zelta.BackupTargetDatasetInfo]{
			Status:  "success",
			Message: "target_datasets_listed",
			Data:    datasets,
		})
	}
}

func BackupTargetDatasetSnapshots(zS *zelta.Service) gin.HandlerFunc {
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

		dataset := strings.TrimSpace(c.Query("dataset"))
		if dataset == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "remote_dataset_required",
				Error:   "dataset query parameter is required",
				Data:    nil,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()

		snapshots, err := zS.ListRemoteTargetDatasetSnapshots(ctx, uint(id64), dataset)
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

func BackupTargetDatasetJailMetadata(zS *zelta.Service) gin.HandlerFunc {
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

		dataset := strings.TrimSpace(c.Query("dataset"))
		if dataset == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "remote_dataset_required",
				Error:   "dataset query parameter is required",
				Data:    nil,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()

		meta, err := zS.GetRemoteTargetJailMetadata(ctx, uint(id64), dataset)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "read_jail_metadata_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupJailMetadataInfo]{
			Status:  "success",
			Message: "jail_metadata_read",
			Data:    meta,
		})
	}
}

func RestoreBackupTargetDataset(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
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

		var req struct {
			RemoteDataset      string `json:"remoteDataset"`
			Snapshot           string `json:"snapshot"`
			DestinationDataset string `json:"destinationDataset"`
			RestoreNodeID      string `json:"restoreNodeId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if strings.TrimSpace(req.RemoteDataset) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "remote_dataset_required",
				Error:   "remoteDataset is required",
				Data:    nil,
			})
			return
		}
		if strings.TrimSpace(req.Snapshot) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "snapshot_required",
				Error:   "snapshot is required",
				Data:    nil,
			})
			return
		}
		if strings.TrimSpace(req.DestinationDataset) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "destination_dataset_required",
				Error:   "destinationDataset is required",
				Data:    nil,
			})
			return
		}

		localNodeID := ""
		if detail := cS.Detail(); detail != nil {
			localNodeID = strings.TrimSpace(detail.NodeID)
		}

		restoreNodeID := strings.TrimSpace(req.RestoreNodeID)
		if restoreNodeID == "" {
			restoreNodeID = localNodeID
		}

		guestID := extractGuestIDFromDatasetPath(req.DestinationDataset)
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

		if restoreNodeID != "" && localNodeID != "" && restoreNodeID != localNodeID {
			body, statusCode, err := forwardBackupTargetRestoreToNode(cS, uint(id64), restoreNodeID, map[string]any{
				"remoteDataset":      strings.TrimSpace(req.RemoteDataset),
				"snapshot":           strings.TrimSpace(req.Snapshot),
				"destinationDataset": strings.TrimSpace(req.DestinationDataset),
				"restoreNodeId":      restoreNodeID,
			})
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "restore_remote_node_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		if err := zS.EnqueueRestoreFromTarget(c.Request.Context(), uint(id64), req.RemoteDataset, req.Snapshot, req.DestinationDataset); err != nil {
			status := http.StatusBadRequest
			msg := "restore_enqueue_failed"
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "restore_job_started",
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
			Name:             req.Name,
			TargetID:         req.TargetID,
			RunnerNodeID:     req.RunnerNodeID,
			Mode:             req.Mode,
			SourceDataset:    req.SourceDataset,
			JailRootDataset:  req.JailRootDataset,
			DestSuffix:       req.DestSuffix,
			PruneKeepLast:    req.PruneKeepLast,
			PruneTarget:      req.PruneTarget,
			StopBeforeBackup: req.StopBeforeBackup,
			CronExpr:         req.CronExpr,
			Enabled:          req.Enabled,
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
			Name:             req.Name,
			TargetID:         req.TargetID,
			RunnerNodeID:     req.RunnerNodeID,
			Mode:             req.Mode,
			SourceDataset:    req.SourceDataset,
			JailRootDataset:  req.JailRootDataset,
			DestSuffix:       req.DestSuffix,
			PruneKeepLast:    req.PruneKeepLast,
			PruneTarget:      req.PruneTarget,
			StopBeforeBackup: req.StopBeforeBackup,
			CronExpr:         req.CronExpr,
			Enabled:          req.Enabled,
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

func RunBackupJobNow(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
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

		// Backward compat for legacy jobs without runner pinning
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_job_run_started",
			Data:    nil,
		})
	}
}

func forwardBackupJobRunToRunner(cS *cluster.Service, jobID uint, runnerNodeID string) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, runnerNodeID)
	if err != nil {
		return nil, 0, err
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

	return body, statusCode, nil
}

func forwardBackupTargetRestoreToNode(cS *cluster.Service, targetID uint, restoreNodeID string, payload map[string]any) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, restoreNodeID)
	if err != nil {
		return nil, 0, err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(0, hostname, "", "")
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

					targetAPI = net.JoinHostPort(host, strconv.Itoa(config.ParsedConfig.Port))
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

func extractGuestIDFromDatasetPath(dataset string) uint {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return 0
	}

	parts := strings.Split(strings.Trim(dataset, "/"), "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "jails" {
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
			return uint(guestID)
		}
	}

	return 0
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

func BackupEvents(zS *zelta.Service) gin.HandlerFunc {
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

		events, err := zS.ListLocalBackupEvents(limit, jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

func BackupEventByID(zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_event_id",
				Error:   "invalid_event_id",
				Data:    nil,
			})
			return
		}

		event, err := zS.GetLocalBackupEvent(uint(id64))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_event_not_found",
					Error:   "backup_event_not_found",
					Data:    nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "get_backup_event_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_event_fetched",
			Data:    event,
		})
	}
}

func BackupEventsRemote(zS *zelta.Service) gin.HandlerFunc {
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

		events, err := zS.ListLocalBackupEventsPaginated(page, size, sortField, sortDir, jobID, search)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupEventsResponse]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
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

func RestoreBackupJob(cS *cluster.Service, zS *zelta.Service) gin.HandlerFunc {
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

		var req struct {
			Snapshot string `json:"snapshot"`
		}
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

		// Check runner node routing (same as RunBackupJobNow)
		localNodeID := ""
		if detail := cS.Detail(); detail != nil {
			localNodeID = strings.TrimSpace(detail.NodeID)
		}

		runnerNodeID := strings.TrimSpace(job.RunnerNodeID)
		if runnerNodeID != "" && localNodeID != "" && runnerNodeID != localNodeID {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "restore_must_run_on_runner_node",
				Error:   fmt.Sprintf("this job is assigned to node %s, restore must be triggered from that node", runnerNodeID),
				Data:    nil,
			})
			return
		}

		if runnerNodeID == "" && cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if job.Mode == clusterModels.BackupJobModeJail {
			guestID := extractGuestIDFromDatasetPath(job.JailRootDataset)
			if guestID == 0 {
				guestID = extractGuestIDFromDatasetPath(job.SourceDataset)
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

		if err := zS.EnqueueRestoreJob(c.Request.Context(), job.ID, req.Snapshot); err != nil {
			status := http.StatusBadRequest
			msg := "restore_enqueue_failed"
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "restore_job_started",
			Data:    nil,
		})
	}
}
