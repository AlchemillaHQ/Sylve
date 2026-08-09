// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CreateVMSnapshotRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=4096"`
}

type vmSnapshotService interface {
	ListVMSnapshots(rid uint) ([]vmModels.VMSnapshot, error)
	CreateVMSnapshot(ctx context.Context, rid uint, name string, description string) (*vmModels.VMSnapshot, error)
	RollbackVMSnapshot(ctx context.Context, rid uint, snapshotID uint) (libvirt.VMSnapshotRollbackResult, error)
	DeleteVMSnapshot(ctx context.Context, rid uint, snapshotID uint) error
}

func vmSnapshotErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func vmSnapshotErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}

	switch vmSnapshotErrorCode(err) {
	case "invalid_request", "invalid_rid", "invalid_vm_rid", "snapshot_name_required",
		"snapshot_name_too_long", "snapshot_description_too_long":
		return http.StatusBadRequest
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "snapshot_not_found", "vm_not_found":
		return http.StatusNotFound
	case "replication_storage_topology_change_requires_policy_disabled",
		"vm_snapshot_requires_zfs_storage",
		"vm_snapshot_root_dataset_not_found",
		"snapshot_vm_json_not_found",
		"invalid_snapshot_vm_json",
		"snapshot_vm_identity_mismatch",
		"vm_snapshot_dataset_missing",
		"restored_storage_dataset_missing",
		"restored_storage_snapshot_missing",
		"restored_storage_dataset_outside_vm_roots",
		"restored_vm_storage_id_conflict",
		"restored_vm_storage_dataset_in_use",
		"invalid_restored_storage_id",
		"invalid_restored_vm_storage_dataset_name":
		return http.StatusConflict
	case "libvirt_connection_unavailable", "libvirt_not_initialized", "gzfs_not_initialized":
		return http.StatusServiceUnavailable
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func writeVMSnapshotError(c *gin.Context, fallbackMessage string, err error) {
	message := fallbackMessage
	status := vmSnapshotErrorStatus(err)
	code := vmSnapshotErrorCode(err)
	if status != http.StatusInternalServerError && code != "" {
		message = code
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

// @Summary List VM snapshots
// @Description Retrieve the crash-consistent ZFS snapshot records for a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Success 200 {object} internal.APIResponse[[]vmModels.VMSnapshot] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/snapshots [get]
func ListVMSnapshots(libvirtService vmSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshots, err := libvirtService.ListVMSnapshots(rid)
		if err != nil {
			writeVMSnapshotError(c, "failed_to_list_vm_snapshots", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]vmModels.VMSnapshot]{
			Status:  "success",
			Message: "vm_snapshots_listed",
			Error:   "",
			Data:    snapshots,
		})
	}
}

// @Summary Create a VM snapshot
// @Description Create crash-consistent recursive ZFS snapshots for a virtual machine; roots on different pools are captured sequentially and the guest is not quiesced
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body CreateVMSnapshotRequest true "Snapshot request"
// @Success 201 {object} internal.APIResponse[vmModels.VMSnapshot] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/snapshots [post]
func CreateVMSnapshot(libvirtService vmSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req CreateVMSnapshotRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		created, err := libvirtService.CreateVMSnapshot(
			c.Request.Context(),
			rid,
			req.Name,
			req.Description,
		)
		if err != nil {
			writeVMSnapshotError(c, "failed_to_create_vm_snapshot", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[vmModels.VMSnapshot]{
			Status:  "success",
			Message: "vm_snapshot_created",
			Error:   "",
			Data:    *created,
		})
	}
}

// @Summary Roll back a VM snapshot
// @Description Stop the VM if needed, roll every recorded VM ZFS root back to the selected snapshot while destroying newer ZFS snapshots, restore the saved configuration, and attempt to restore the prior running state
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param snapshotId path int true "VM Snapshot ID" minimum(1)
// @Success 200 {object} internal.APIResponse[libvirt.VMSnapshotRollbackResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/snapshots/{snapshotId}/rollback [post]
func RollbackVMSnapshot(libvirtService vmSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
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

		result, err := libvirtService.RollbackVMSnapshot(c.Request.Context(), rid, snapshotID)
		if err != nil {
			writeVMSnapshotError(c, "failed_to_rollback_vm_snapshot", err)
			return
		}

		message := "vm_snapshot_rolled_back"
		if len(result.Warnings) > 0 {
			message = "vm_snapshot_rolled_back_with_warnings"
		}
		c.JSON(http.StatusOK, internal.APIResponse[libvirt.VMSnapshotRollbackResult]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Delete a VM snapshot
// @Description Delete a VM snapshot from every recorded ZFS root and remove its metadata record while preserving lineage for any child records
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param snapshotId path int true "VM Snapshot ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/snapshots/{snapshotId} [delete]
func DeleteVMSnapshot(libvirtService vmSnapshotService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
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

		if err := libvirtService.DeleteVMSnapshot(c.Request.Context(), rid, snapshotID); err != nil {
			writeVMSnapshotError(c, "failed_to_delete_vm_snapshot", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_snapshot_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
