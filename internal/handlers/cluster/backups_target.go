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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
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

type RestoreBackupTargetRequest struct {
	RemoteDataset       string `json:"remoteDataset"`
	Snapshot            string `json:"snapshot"`
	DestinationDataset  string `json:"destinationDataset"`
	RestoreNodeID       string `json:"restoreNodeId"`
	RestoreNetwork      *bool  `json:"restoreNetwork"`
	OperationID         string `json:"operationId"`
	EncryptionKey       string `json:"encryptionKey"`
	EncryptionKeyFormat string `json:"encryptionKeyFormat"`
}

func writeBackupTargetMutationError(c *gin.Context, operation string, err error) {
	status := http.StatusInternalServerError
	message := operation
	detail := operation
	errorText := ""
	if err != nil {
		errorText = strings.ToLower(err.Error())
	}

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), strings.Contains(errorText, "backup_target_not_found"):
		status = http.StatusNotFound
		message = "backup_target_not_found"
		detail = message
	case strings.Contains(errorText, "invalid_"),
		strings.Contains(errorText, "_required"),
		strings.Contains(errorText, "_invalid"),
		strings.Contains(errorText, "_immutable"),
		strings.Contains(errorText, "not_supported"):
		status = http.StatusBadRequest
		if err != nil {
			detail = err.Error()
		}
	case strings.Contains(errorText, "_conflict"),
		strings.Contains(errorText, "must_be_disabled"),
		strings.Contains(errorText, "has_active_operations"),
		strings.Contains(errorText, "provision_pending"),
		strings.Contains(errorText, "update_stale"),
		strings.Contains(errorText, "in_use"),
		errors.Is(err, raft.ErrNotLeader),
		errors.Is(err, raft.ErrLeadershipLost):
		status = http.StatusConflict
		if errors.Is(err, raft.ErrNotLeader) || errors.Is(err, raft.ErrLeadershipLost) {
			message = "cluster_leadership_changed"
			detail = message
		} else if err != nil {
			detail = err.Error()
		}
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, raft.ErrRaftShutdown),
		errors.Is(err, raft.ErrEnqueueTimeout),
		strings.Contains(errorText, "raft_not_initialized"),
		strings.Contains(errorText, "raft_apply_failed"),
		strings.Contains(errorText, "leader_not_available"),
		strings.Contains(errorText, "barrier_failed"):
		status = http.StatusServiceUnavailable
		message = "backup_target_service_unavailable"
		detail = message
	default:
		logger.L.Error().Err(err).Str("operation", operation).Msg("backup_target_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status: "error", Message: message, Error: detail, Data: nil,
	})
}

func writeBackupTargetRemoteReadError(c *gin.Context, operation string, err error) {
	status := http.StatusBadGateway
	message := operation
	detail := operation
	errorText := ""
	if err != nil {
		errorText = strings.ToLower(err.Error())
	}

	switch {
	case strings.Contains(errorText, "remote_dataset_invalid"),
		strings.Contains(errorText, "remote_dataset_required"),
		strings.Contains(errorText, "remote_dataset_outside_backup_root"):
		status = http.StatusBadRequest
		if err != nil {
			detail = err.Error()
		}
	case errors.Is(err, gorm.ErrRecordNotFound), strings.Contains(errorText, "backup_target_not_found"):
		status = http.StatusNotFound
		message = "backup_target_not_found"
		detail = message
	case strings.Contains(errorText, "backup_target_disabled"):
		status = http.StatusConflict
		message = "backup_target_disabled"
		detail = message
	case strings.Contains(errorText, "backup_target_lookup_failed"):
		status = http.StatusInternalServerError
		message = "backup_target_lookup_failed"
		detail = message
		logger.L.Error().Err(err).Str("operation", operation).Msg("backup_target_lookup_failed")
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(errorText, "context deadline"):
		status = http.StatusGatewayTimeout
		message = "backup_target_request_timed_out"
		detail = message
	case strings.Contains(errorText, "backup_target_ssh_key_materialize_failed"):
		status = http.StatusServiceUnavailable
		message = "backup_target_key_unavailable"
		detail = message
	default:
		if err != nil {
			detail = err.Error()
		}
	}

	c.JSON(status, internal.APIResponse[any]{
		Status: "error", Message: message, Error: detail, Data: nil,
	})
}

// @Summary List Backup Targets
// @Description List backup targets managed by the cluster
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.BackupTarget] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/backups/targets [get]
func BackupTargets(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}
		targets, err := cS.ListBackupTargets()
		if err != nil {
			writeBackupTargetMutationError(c, "list_backup_targets_failed", err)
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
		errorText = message
		logger.L.Error().Err(err).Msg("backup_target_key_stage_failed")
	}
	c.JSON(statusCode, internal.APIResponse[any]{
		Status: "error", Message: message, Error: errorText, Data: nil,
	})
}

func writeBackupTargetKeyMaterializationError(c *gin.Context, err error) {
	logger.L.Error().Err(err).Msg("backup_target_key_materialization_failed")
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status: "error", Message: "save_ssh_key_failed", Error: "save_ssh_key_failed", Data: nil,
	})
}

// Connectivity-requiring admission remains leader-local and proves only
// leader reachability. Metadata/disable updates are observational; runner
// readiness is established independently by placement or explicit validation.
//
// @Summary Create a Backup Target
// @Description Validate and create a cluster backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body clusterServiceInterfaces.BackupTargetReq true "Backup Target Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/backups/targets [post]
func CreateBackupTarget(cS *cluster.Service, zS backupTargetZelta) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req clusterServiceInterfaces.BackupTargetReq
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		candidate, err := cS.BuildBackupTargetCreateCandidate(req)
		if err != nil {
			writeBackupTargetMutationError(c, "backup_target_create_failed", err)
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
				writeBackupTargetMutationError(c, "backup_target_create_failed", prepareErr)
				return
			}
			proposed, decodeErr := clusterModels.DecodeBackupTargetProvisionTarget(operation)
			if decodeErr != nil {
				logger.L.Error().Err(decodeErr).Msg("backup_target_provision_decode_failed")
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "backup_target_create_failed", Error: "backup_target_create_failed", Data: nil,
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
				writeBackupTargetMutationError(c, "backup_target_create_failed", completeErr)
				return
			}
			committed = &proposed
		} else {
			committed, err = cS.ProposeBackupTargetCreateCandidate(candidate, cS.Raft == nil)
			if err != nil {
				writeBackupTargetMutationError(c, "backup_target_create_failed", err)
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

// @Summary Update a Backup Target
// @Description Update backup target metadata, lifecycle state, or managed SSH key
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param request body clusterServiceInterfaces.BackupTargetReq true "Backup Target Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/backups/targets/{id} [put]
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
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		existing, err := cS.GetBackupTargetByID(uint(id64))
		if err != nil {
			writeBackupTargetMutationError(c, "backup_target_lookup_failed", err)
			return
		}

		plan, err := cS.BuildBackupTargetUpdatePlan(existing, req)
		if err != nil {
			writeBackupTargetMutationError(c, "backup_target_update_failed", err)
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
			writeBackupTargetMutationError(c, "backup_target_update_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "backup_target_updated",
			Data:    nil,
		})
	}
}

// @Summary Delete a Backup Target
// @Description Delete a disabled backup target that has no active dependencies
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /cluster/backups/targets/{id} [delete]
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
			writeBackupTargetMutationError(c, "backup_target_delete_failed", err)
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

// @Summary Validate a Backup Target
// @Description Validate backup target readiness from a selected cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param nodeId query string false "Validation node ID; required when clustered"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Validation unavailable"
// @Router /cluster/backups/targets/{id}/validate [post]
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
			detail := message
			var rejected *cluster.BackupTargetValidationRejectedError
			errorText := strings.ToLower(validationErr.Error())
			switch {
			case strings.Contains(errorText, "backup_target_not_found"):
				statusCode = http.StatusNotFound
				message = "backup_target_not_found"
				detail = message
			case errors.As(validationErr, &rejected):
				statusCode = http.StatusBadRequest
				message = "target_validation_failed"
				detail = validationErr.Error()
			case strings.Contains(errorText, "not_raft_member"),
				strings.Contains(errorText, "not_raft_voter"),
				strings.Contains(errorText, "node_id_required"):
				statusCode = http.StatusBadRequest
				message = "validation_node_invalid"
				detail = validationErr.Error()
			case strings.Contains(errorText, "request_failed"),
				strings.Contains(errorText, "leader_barrier_failed"),
				strings.Contains(errorText, "raft_catchup_failed"),
				strings.Contains(errorText, "context deadline"):
				statusCode = http.StatusServiceUnavailable
				detail = message
			default:
				logger.L.Error().Err(validationErr).Msg("backup_target_validation_failed")
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status: "error", Message: message, Error: detail, Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "target_validated", Data: nil,
		})
	}
}

// @Summary Get Backup Target Readiness
// @Description Get per-node readiness for a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Success 200 {object} internal.APIResponse[[]clusterModels.BackupTargetNodeReadinessStatus] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/backups/targets/{id}/readiness [get]
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
			writeBackupTargetMutationError(c, "backup_target_readiness_failed", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupTargetNodeReadinessStatus]{
			Status: "success", Message: "backup_target_readiness_listed", Data: readiness,
		})
	}
}

// @Summary List Backup Target Datasets
// @Description List restorable datasets on a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Success 200 {object} internal.APIResponse[[]zelta.BackupTargetDatasetInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Target Disabled"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Target Failure"
// @Failure 503 {object} internal.APIResponse[any] "Local Key Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Target Timeout"
// @Router /cluster/backups/targets/{id}/datasets [get]
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
			writeBackupTargetRemoteReadError(c, "list_target_datasets_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zelta.BackupTargetDatasetInfo]{
			Status:  "success",
			Message: "target_datasets_listed",
			Data:    datasets,
		})
	}
}

// @Summary List Backup Target Dataset Snapshots
// @Description List restorable snapshots for a dataset on a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param dataset query string true "Remote dataset"
// @Success 200 {object} internal.APIResponse[[]zelta.SnapshotInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Target Disabled"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Target Failure"
// @Failure 503 {object} internal.APIResponse[any] "Local Key Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Target Timeout"
// @Router /cluster/backups/targets/{id}/datasets/snapshots [get]
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

// @Summary Get Backup Jail Metadata
// @Description Read jail metadata for a dataset on a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param dataset query string true "Remote jail dataset"
// @Success 200 {object} internal.APIResponse[zelta.BackupJailMetadataInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Target Disabled"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Target Failure"
// @Failure 503 {object} internal.APIResponse[any] "Local Key Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Target Timeout"
// @Router /cluster/backups/targets/{id}/datasets/jail-metadata [get]
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
			writeBackupTargetRemoteReadError(c, "read_jail_metadata_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupJailMetadataInfo]{
			Status:  "success",
			Message: "jail_metadata_read",
			Data:    meta,
		})
	}
}

// @Summary Get Backup VM Metadata
// @Description Read virtual machine metadata for a dataset on a backup target
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param dataset query string true "Remote virtual machine dataset"
// @Success 200 {object} internal.APIResponse[zelta.BackupVMMetadataInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Backup Target Disabled"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Target Failure"
// @Failure 503 {object} internal.APIResponse[any] "Local Key Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Target Timeout"
// @Router /cluster/backups/targets/{id}/datasets/vm-metadata [get]
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
			writeBackupTargetRemoteReadError(c, "read_vm_metadata_failed", err)
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
	case errors.Is(err, gorm.ErrRecordNotFound), strings.Contains(message, "backup_target_not_found"):
		return http.StatusNotFound, "backup_target_not_found"
	case strings.Contains(message, "backup_target_lookup_failed"):
		return http.StatusInternalServerError, "backup_target_lookup_failed"
	case strings.Contains(message, "backup_target_disabled"):
		return http.StatusConflict, "backup_target_disabled"
	case strings.Contains(message, "backup_target_ssh_key_materialize_failed"):
		return http.StatusServiceUnavailable, "backup_target_key_unavailable"
	case strings.Contains(message, "backup_job_already_running"):
		return http.StatusConflict, "backup_job_already_running"
	case strings.Contains(message, "restore_destination_reserved"),
		strings.Contains(message, "restore_destination_already_running"):
		return http.StatusConflict, "restore_destination_already_running"
	case strings.Contains(message, "guest_id_already_in_use"),
		strings.Contains(message, "guest_identity_claim_conflict"),
		strings.Contains(message, "guest_identity_inventory_conflict"),
		strings.Contains(message, "restore_destination_guest_dataset_exists"):
		return http.StatusConflict, "restore_guest_destination_conflict"
	case strings.Contains(message, "guest_identity_inventory_unavailable"),
		strings.Contains(message, "guest_identity_registry_initializing"),
		strings.Contains(message, "guest_identity_cluster_formation_in_progress"):
		return http.StatusServiceUnavailable, "restore_guest_identity_unavailable"
	case strings.Contains(message, "leader_not_available"),
		strings.Contains(message, "not_leader"),
		strings.Contains(message, "cluster_consensus_unavailable"),
		strings.Contains(message, "guest_identity_release_failed"),
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

// @Summary Restore a Backup Target Dataset
// @Description Queue an idempotent out-of-band restore from a backup target, optionally on another cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Target ID"
// @Param Idempotency-Key header string false "Idempotency key used when operationId is omitted"
// @Param request body RestoreBackupTargetRequest true "Restore Request"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Target or Restore Node Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Restore Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/backups/targets/{id}/restore [post]
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

		var req RestoreBackupTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
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
		req.RemoteDataset, req.Snapshot, req.DestinationDataset, err = zelta.CanonicalRestoreFromTargetInput(
			req.RemoteDataset,
			req.Snapshot,
			req.DestinationDataset,
		)
		if err != nil {
			status, message := restoreFromTargetEnqueueError(err)
			c.JSON(status, internal.APIResponse[any]{
				Status: "error", Message: message, Error: err.Error(), Data: nil,
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
				writeBackupNodeForwardError(
					c,
					"restore_remote_node_forward_failed",
					"restore_node_not_found",
					err,
				)
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
			detail := err.Error()
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Uint("target_id", uint(id64)).Msg("backup_target_restore_enqueue_failed")
				detail = msg
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   detail,
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
