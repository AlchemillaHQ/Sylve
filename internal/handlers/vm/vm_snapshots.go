// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type CreateVMSnapshotRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

func ListVMSnapshots(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "id")
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_vm_snapshots",
				Error:   err.Error(),
				Data:    nil,
			})
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

func CreateVMSnapshot(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "id")
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_vm_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[vmModels.VMSnapshot]{
			Status:  "success",
			Message: "vm_snapshot_created",
			Error:   "",
			Data:    *created,
		})
	}
}

func RollbackVMSnapshot(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "id")
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

		if err := libvirtService.RollbackVMSnapshot(c.Request.Context(), rid, snapshotID, true); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_rollback_vm_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_snapshot_rolled_back",
			Error:   "",
			Data:    nil,
		})
	}
}

func DeleteVMSnapshot(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "id")
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_vm_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
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
