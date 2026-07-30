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
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

type backupTargetZelta interface {
	InspectTargetCandidate(ctx context.Context, target *clusterModels.BackupTarget) (zelta.BackupTargetValidationResult, error)
	ValidateTargetCandidateReadiness(ctx context.Context, target *clusterModels.BackupTarget) error
	ValidateTargetReadiness(ctx context.Context, target *clusterModels.BackupTarget) error
	ProvisionBackupTargetRoot(ctx context.Context, target *clusterModels.BackupTarget) error
	MaterializeBackupTargetSSHKey(target *clusterModels.BackupTarget) error
	AcquireBackupTargetSSHKey(target *clusterModels.BackupTarget) (func(), error)
	RemoveSSHKey(targetID uint)
}

func BackupTargets(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}
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

func writeBackupTargetCandidateValidationError(c *gin.Context, err error) {
	statusCode := http.StatusBadRequest
	message := "target_validation_failed"
	errorText := "target_validation_failed"
	if err != nil {
		errorText = err.Error()
	}
	if strings.Contains(strings.ToLower(errorText), "stage_backup_target_ssh_key_failed") {
		statusCode = http.StatusInternalServerError
		message = "save_ssh_key_failed"
	}
	c.JSON(statusCode, internal.APIResponse[any]{
		Status: "error", Message: message, Error: errorText, Data: nil,
	})
}

func writeBackupTargetKeyMaterializationError(c *gin.Context, err error) {
	errorText := "backup_target_ssh_key_materialize_failed"
	if err != nil {
		errorText = err.Error()
	}
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status: "error", Message: "save_ssh_key_failed", Error: errorText, Data: nil,
	})
}

// Connectivity-requiring admission remains leader-local and proves only
// leader reachability. Metadata/disable updates are observational; runner
// readiness is established independently by placement or explicit validation.
func CreateBackupTarget(cS *cluster.Service, zS backupTargetZelta) gin.HandlerFunc {
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

		candidate, err := cS.BuildBackupTargetCreateCandidate(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_create_failed", Error: err.Error(), Data: nil,
			})
			return
		}
		validateCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		inspection, err := zS.InspectTargetCandidate(validateCtx, candidate)
		if err != nil {
			writeBackupTargetCandidateValidationError(c, err)
			return
		}

		var committed *clusterModels.BackupTarget
		if inspection.RootProvisioningRequired {
			operation, prepareErr := cS.PrepareBackupTargetProvisionCreate(
				candidate,
				"backup-target-provision:"+uuid.NewString(),
				cS.Raft == nil,
			)
			if prepareErr != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "backup_target_create_failed", Error: prepareErr.Error(), Data: nil,
				})
				return
			}
			proposed, decodeErr := clusterModels.DecodeBackupTargetProvisionTarget(operation)
			if decodeErr != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "backup_target_create_failed", Error: decodeErr.Error(), Data: nil,
				})
				return
			}
			if provisionErr := zS.ProvisionBackupTargetRoot(validateCtx, &proposed); provisionErr != nil {
				if !zelta.BackupTargetProvisionFailureIsAmbiguous(provisionErr) {
					if failErr := cS.FailBackupTargetProvision(operation, provisionErr.Error(), cS.Raft == nil); failErr != nil {
						provisionErr = errors.Join(provisionErr, failErr)
					} else {
						zS.RemoveSSHKey(operation.TargetID)
					}
				}
				writeBackupTargetCandidateValidationError(c, provisionErr)
				return
			}
			if completeErr := cS.CompleteBackupTargetProvision(operation, cS.Raft == nil); completeErr != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "backup_target_create_failed", Error: completeErr.Error(), Data: nil,
				})
				return
			}
			committed = &proposed
		} else {
			committed, err = cS.ProposeBackupTargetCreateCandidate(candidate, cS.Raft == nil)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "backup_target_create_failed", Error: err.Error(), Data: nil,
				})
				return
			}
		}
		if err := zS.MaterializeBackupTargetSSHKey(committed); err != nil {
			writeBackupTargetKeyMaterializationError(c, err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_created",
			Data:    nil,
		})
	}
}

func UpdateBackupTarget(cS *cluster.Service, zS backupTargetZelta) gin.HandlerFunc {
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

		plan, err := cS.BuildBackupTargetUpdatePlan(existing, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_update_failed", Error: err.Error(), Data: nil,
			})
			return
		}
		validateCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		if plan.Kind == clusterModels.BackupTargetUpdateKindEnable ||
			plan.Kind == clusterModels.BackupTargetUpdateKindRotateKey {
			if err := zS.ValidateTargetCandidateReadiness(validateCtx, plan.Candidate); err != nil {
				writeBackupTargetCandidateValidationError(c, err)
				return
			}
			// The immutable version can be staged safely before Raft: until the
			// command commits no target references a replacement fingerprint. A
			// lease closes the gap against local reconciliation during apply.
			releaseKey, acquireErr := zS.AcquireBackupTargetSSHKey(plan.Candidate)
			if acquireErr != nil {
				writeBackupTargetKeyMaterializationError(c, acquireErr)
				return
			}
			defer releaseKey()
		}

		if err = cS.ProposeBackupTargetUpdatePlan(plan, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_update_failed", Error: err.Error(), Data: nil,
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

func DeleteBackupTarget(cS *cluster.Service, zS backupTargetZelta) gin.HandlerFunc {
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

		zS.RemoveSSHKey(uint(id64))

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_deleted",
			Data:    nil,
		})
	}
}

func ValidateBackupTarget(cS *cluster.Service, zS backupTargetZelta) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_target_id", Error: "invalid_target_id", Data: nil,
			})
			return
		}
		nodeID := strings.TrimSpace(c.Query("nodeId"))
		if cS.Raft != nil && nodeID == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "validation_node_id_required",
				Error: "nodeId query parameter is required in a cluster", Data: nil,
			})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 70*time.Second)
		defer cancel()
		_, validationErr := cS.ValidateBackupTargetOnNode(
			ctx, uint(id64), nodeID, zS.ValidateTargetReadiness,
		)
		if validationErr != nil {
			statusCode := http.StatusInternalServerError
			message := "target_validation_unavailable"
			var rejected *cluster.BackupTargetValidationRejectedError
			errorText := strings.ToLower(validationErr.Error())
			switch {
			case strings.Contains(errorText, "backup_target_not_found"):
				statusCode = http.StatusNotFound
				message = "backup_target_not_found"
			case errors.As(validationErr, &rejected):
				statusCode = http.StatusBadRequest
				message = "target_validation_failed"
			case strings.Contains(errorText, "not_raft_member"),
				strings.Contains(errorText, "not_raft_voter"),
				strings.Contains(errorText, "node_id_required"):
				statusCode = http.StatusBadRequest
				message = "validation_node_invalid"
			case strings.Contains(errorText, "request_failed"),
				strings.Contains(errorText, "leader_barrier_failed"),
				strings.Contains(errorText, "raft_catchup_failed"),
				strings.Contains(errorText, "context deadline"):
				statusCode = http.StatusServiceUnavailable
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status: "error", Message: message, Error: validationErr.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "target_validated", Data: nil,
		})
	}
}

func BackupTargetReadiness(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_target_id", Error: "invalid_target_id", Data: nil,
			})
			return
		}
		readiness, err := cS.BackupTargetReadiness(uint(id64))
		if err != nil {
			statusCode := http.StatusInternalServerError
			message := "backup_target_readiness_failed"
			if strings.Contains(strings.ToLower(err.Error()), "record not found") {
				statusCode = http.StatusNotFound
				message = "backup_target_not_found"
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status: "error", Message: message, Error: err.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupTargetNodeReadinessStatus]{
			Status: "success", Message: "backup_target_readiness_listed", Data: readiness,
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

func BackupTargetDatasetVMMetadata(zS *zelta.Service) gin.HandlerFunc {
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

		meta, err := zS.GetRemoteTargetVMMetadata(ctx, uint(id64), dataset)
		if err != nil {
			c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
				Status:  "error",
				Message: "read_vm_metadata_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupVMMetadataInfo]{
			Status:  "success",
			Message: "vm_metadata_read",
			Data:    meta,
		})
	}
}

func restoreFromTargetEnqueueError(err error) (int, string) {
	if err == nil {
		return http.StatusBadRequest, "restore_enqueue_failed"
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "backup_job_already_running"):
		return http.StatusConflict, "backup_job_already_running"
	case strings.Contains(message, "restore_destination_reserved"),
		strings.Contains(message, "restore_destination_already_running"):
		return http.StatusConflict, "restore_destination_already_running"
	case strings.Contains(message, "guest_id_already_in_use"),
		strings.Contains(message, "guest_identity_inventory_conflict"),
		strings.Contains(message, "restore_destination_guest_dataset_exists"):
		return http.StatusConflict, "restore_guest_destination_conflict"
	case strings.Contains(message, "guest_identity_inventory_unavailable"):
		return http.StatusServiceUnavailable, "restore_guest_identity_unavailable"
	case strings.Contains(message, "leader_not_available"),
		strings.Contains(message, "not_leader"),
		strings.Contains(message, "raft_"),
		strings.Contains(message, "replication_control_"),
		strings.Contains(message, "request timed out"),
		strings.Contains(message, "local_node_id_unavailable"):
		return http.StatusServiceUnavailable, "restore_reservation_unavailable"
	case strings.Contains(message, "async_audit_"),
		strings.Contains(message, "restore_event_"):
		return http.StatusInternalServerError, "restore_observability_unavailable"
	case strings.Contains(message, "guest_identity_inventory_scan_failed"),
		strings.Contains(message, "restore_destination_dataset_check_failed"):
		return http.StatusInternalServerError, "restore_precheck_failed"
	case strings.Contains(message, "restore_guest_destination_kind_mismatch"),
		strings.Contains(message, "restore_guest_destination_must_be_canonical_root"),
		strings.Contains(message, "invalid_guest_id"):
		return http.StatusBadRequest, "restore_guest_destination_invalid"
	default:
		return http.StatusBadRequest, "restore_enqueue_failed"
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
			RemoteDataset       string `json:"remoteDataset"`
			Snapshot            string `json:"snapshot"`
			DestinationDataset  string `json:"destinationDataset"`
			RestoreNodeID       string `json:"restoreNodeId"`
			RestoreNetwork      *bool  `json:"restoreNetwork"`
			OperationID         string `json:"operationId"`
			EncryptionKey       string `json:"encryptionKey"`
			EncryptionKeyFormat string `json:"encryptionKeyFormat"`
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
		operationID := strings.TrimSpace(req.OperationID)
		if operationID == "" {
			operationID = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}

		if restoreNodeID != "" && localNodeID != "" && restoreNodeID != localNodeID {
			response, err := forwardBackupTargetRestoreToNode(c, cS, uint(id64), restoreNodeID, map[string]any{
				"remoteDataset":       strings.TrimSpace(req.RemoteDataset),
				"snapshot":            strings.TrimSpace(req.Snapshot),
				"destinationDataset":  strings.TrimSpace(req.DestinationDataset),
				"restoreNodeId":       restoreNodeID,
				"restoreNetwork":      restoreNetwork,
				"operationId":         operationID,
				"encryptionKey":       req.EncryptionKey,
				"encryptionKeyFormat": req.EncryptionKeyFormat,
			})
			if err != nil {
				writeClusterForwardError(c, "restore_remote_node_forward_failed", err)
				return
			}

			writeClusterForwardResponse(c, response)
			return
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

		if err := zS.EnqueueRestoreFromTarget(
			c.Request.Context(),
			uint(id64),
			req.RemoteDataset,
			req.Snapshot,
			req.DestinationDataset,
			restoreNetwork,
			operationID,
		); err != nil {
			status, msg := restoreFromTargetEnqueueError(err)
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
