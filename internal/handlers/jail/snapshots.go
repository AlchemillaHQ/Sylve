// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CreateJailSnapshotRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=4096"`
}

type jailSnapshotService interface {
	ListJailSnapshots(ctID uint) ([]jailModels.JailSnapshot, error)
	CreateJailSnapshot(ctx context.Context, ctID uint, name string, description string) (*jailModels.JailSnapshot, error)
	RollbackJailSnapshot(ctx context.Context, ctID uint, snapshotID uint) (jail.JailSnapshotRollbackResult, error)
	DeleteJailSnapshot(ctx context.Context, ctID uint, snapshotID uint) error
}

func jailSnapshotErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func jailSnapshotErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	switch jailSnapshotErrorCode(err) {
	case "invalid_request", "invalid_ct_id", "invalid_snapshot_name", "snapshot_name_required",
		"snapshot_name_too_long", "snapshot_description_too_long":
		return http.StatusBadRequest
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "jail_not_found", "snapshot_not_found":
		return http.StatusNotFound
	case "replication_storage_topology_change_requires_policy_disabled",
		"replication_run_in_progress",
		"jail_base_storage_not_found",
		"jail_base_pool_not_found",
		"jail_snapshot_root_dataset_mismatch",
		"jail_snapshot_dataset_missing",
		"jail_snapshot_root_not_filesystem",
		"snapshot_jail_json_not_found",
		"invalid_snapshot_jail_json",
		"snapshot_jail_identity_mismatch",
		"jail_snapshot_mountpoint_unusable",
		"jail_snapshot_mountpoint_mismatch",
		"failed_to_get_jail_mount_point",
		"snapshot_host_config_missing",
		"snapshot_host_config_invalid",
		"snapshot_jail_conf_not_found",
		"restored_jail_base_storage_not_found",
		"jail_base_storage_dataset_missing",
		"restored_jail_storage_dataset_missing",
		"restored_jail_storage_duplicate",
		"restored_jail_storage_in_use",
		"restored_jail_storage_not_filesystem",
		"failed_to_stop_jail_before_snapshot_rollback",
		"jail_failed_to_reach_inactive_state":
		return http.StatusConflict
	case "gzfs_not_initialized":
		return http.StatusServiceUnavailable
	}

	return http.StatusInternalServerError
}

func jailSnapshotErrorResponse(err error, fallbackMessage string) (int, string) {
	status := jailSnapshotErrorStatus(err)
	message := fallbackMessage
	if status != http.StatusInternalServerError {
		if code := jailSnapshotErrorCode(err); code != "" {
			message = code
		}
	}
	return status, message
}

func writeJailSnapshotError(c *gin.Context, fallbackMessage string, err error) {
	status, message := jailSnapshotErrorResponse(err, fallbackMessage)
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

// @Summary List jail snapshots
// @Description Retrieve the crash-consistent recursive ZFS snapshot records for a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Success 200 {object} internal.APIResponse[[]jailModels.JailSnapshot] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/snapshots [get]
func ListJailSnapshots(jailService jailSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshots, err := jailService.ListJailSnapshots(ctID)
		if err != nil {
			writeJailSnapshotError(c, "failed_to_list_jail_snapshots", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailModels.JailSnapshot]{
			Status:  "success",
			Message: "jail_snapshots_listed",
			Error:   "",
			Data:    snapshots,
		})
	}
}

// @Summary Create a jail snapshot
// @Description Create a crash-consistent recursive ZFS snapshot for a jail without quiescing it
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body CreateJailSnapshotRequest true "Snapshot request"
// @Success 201 {object} internal.APIResponse[jailModels.JailSnapshot] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/snapshots [post]
func CreateJailSnapshot(jailService jailSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req CreateJailSnapshotRequest
		if !bindJailJSON(c, &req, "invalid_request") {
			return
		}

		created, err := jailService.CreateJailSnapshot(
			c.Request.Context(),
			ctID,
			req.Name,
			req.Description,
		)
		if err != nil {
			writeJailSnapshotError(c, "failed_to_create_jail_snapshot", err)
			return
		}
		if created == nil {
			writeJailSnapshotError(c, "failed_to_create_jail_snapshot", errors.New("snapshot_creation_returned_nil"))
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[jailModels.JailSnapshot]{
			Status:  "success",
			Message: "jail_snapshot_created",
			Error:   "",
			Data:    *created,
		})
	}
}

// @Summary Roll back a jail snapshot
// @Description Stop the jail if needed, recursively roll its ZFS dataset tree back while destroying newer snapshots, restore saved configuration, and attempt to restore the prior running state
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param snapshotId path int true "Jail Snapshot ID" minimum(1)
// @Success 200 {object} internal.APIResponse[jail.JailSnapshotRollbackResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/snapshots/{snapshotId}/rollback [post]
func RollbackJailSnapshot(jailService jailSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshotID, err := utils.ParamUint(c, "snapshotId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		result, err := jailService.RollbackJailSnapshot(c.Request.Context(), ctID, snapshotID)
		if err != nil {
			status, message := jailSnapshotErrorResponse(err, "failed_to_rollback_jail_snapshot")
			c.JSON(status, internal.APIResponse[jail.JailSnapshotRollbackResult]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    result,
			})
			return
		}

		message := "jail_snapshot_rolled_back"
		if len(result.Warnings) > 0 {
			message = "jail_snapshot_rolled_back_with_warnings"
		}
		c.JSON(http.StatusOK, internal.APIResponse[jail.JailSnapshotRollbackResult]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Delete a jail snapshot
// @Description Delete a jail snapshot throughout its recursive ZFS dataset tree and remove its metadata while preserving child lineage
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param snapshotId path int true "Jail Snapshot ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/snapshots/{snapshotId} [delete]
func DeleteJailSnapshot(jailService jailSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshotID, err := utils.ParamUint(c, "snapshotId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := jailService.DeleteJailSnapshot(c.Request.Context(), ctID, snapshotID); err != nil {
			writeJailSnapshotError(c, "failed_to_delete_jail_snapshot", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_snapshot_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
