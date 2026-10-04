// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

// ValidateBackupJobSafetyInternal evaluates only this node's durable guest
// inventory. Routing places it behind the internal-cluster JWT middleware.
func ValidateBackupTargetInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_validation_service_unavailable",
				Error: "backup_target_validation_service_unavailable",
			})
			return
		}
		if _, err := cS.ResolveCurrentRaftMember(c.GetString("IssuerNodeID")); err != nil {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{Status: "error", Message: "cluster_member_required", Error: err.Error()})
			return
		}
		var request cluster.BackupTargetValidationRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: err.Error(),
			})
			return
		}
		result, err := cS.ValidateBackupTargetConnectivityLocal(c.Request.Context(), request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_target_validation_failed", Error: err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[clusterModels.BackupTargetNodeReadinessUpdate]{
			Status: "success", Message: "backup_target_validation_completed", Data: result,
		})
	}
}

func ValidateBackupJobSafetyInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_runner_validation_service_unavailable",
				Error: "backup_runner_validation_service_unavailable",
			})
			return
		}

		var request cluster.BackupJobSafetyValidationRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: err.Error(),
			})
			return
		}

		result, err := cS.ValidateBackupJobSafetyLocal(c.Request.Context(), request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "backup_runner_validation_failed", Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.BackupJobSafetyValidationResult]{
			Status:  "success",
			Message: "backup_runner_validation_completed",
			Data:    result,
		})
	}
}

func InstallBackupTargetHostKeyInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS == nil || cS.DB == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{Status: "error", Message: "backup_target_host_key_state_unavailable", Error: "backup_target_host_key_state_unavailable"})
			return
		}
		if _, err := cS.ResolveCurrentRaftMember(c.GetString("IssuerNodeID")); err != nil {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{Status: "error", Message: "cluster_member_required", Error: err.Error()})
			return
		}
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			writeBackupTargetMutationError(c, "backup_target_host_key_install_failed", raft.ErrNotLeader)
			return
		}
		var change clusterModels.BackupTargetSSHHostTrustChange
		if err := c.ShouldBindJSON(&change); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}
		if err := cS.InstallBackupTargetHostKey(c.Request.Context(), change); err != nil {
			writeBackupTargetMutationError(c, "backup_target_host_key_install_failed", err)
			return
		}
		var index uint64
		if cS.Raft != nil {
			index = cS.Raft.AppliedIndex()
		}
		fingerprint, _ := remoteexec.SSHHostKeyFingerprint(change.PublicKey)
		c.JSON(http.StatusOK, internal.APIResponse[cluster.BackupTargetHostKeyInstallResult]{
			Status: "success", Message: "backup_target_host_key_installed",
			Data: cluster.BackupTargetHostKeyInstallResult{Revision: change.ExpectedRevision + 1, AppliedIndex: index, Fingerprint: fingerprint},
		})
	}
}
