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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

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
			RestoreNetwork     *bool  `json:"restoreNetwork"`
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
		restoreNetwork := true
		if req.RestoreNetwork != nil {
			restoreNetwork = *req.RestoreNetwork
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
				"restoreNetwork":     restoreNetwork,
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

		if err := zS.EnqueueRestoreFromTarget(
			c.Request.Context(),
			uint(id64),
			req.RemoteDataset,
			req.Snapshot,
			req.DestinationDataset,
			restoreNetwork,
		); err != nil {
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
