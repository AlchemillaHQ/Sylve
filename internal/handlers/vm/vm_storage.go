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
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"

	"github.com/gin-gonic/gin"
)

type vmStorageService interface {
	RequireVMStorageTopologyMutable(rid uint) error
	RequireVMStorageRecordTopologyMutable(storageID int) error
	StorageDetach(req libvirtServiceInterfaces.StorageDetachRequest) error
	StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) error
	StorageUpdate(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) error
}

func writeVMStorageTopologyGuardError(c *gin.Context, err error) {
	if strings.Contains(err.Error(), "replication_storage_topology_change_requires_policy_disabled") {
		c.JSON(http.StatusConflict, internal.APIResponse[any]{
			Status: "error", Message: "replication_storage_topology_change_requires_policy_disabled", Error: err.Error(),
		})
		return
	}
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status: "error", Message: "replication_topology_check_failed", Error: err.Error(),
	})
}

// @Summary Detach Storage from a Virtual Machine
// @Description Detach a storage volume from a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /storage/detach [post]
func StorageDetach(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.StorageDetachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}
		if err := libvirtService.RequireVMStorageTopologyMutable(req.RID); err != nil {
			writeVMStorageTopologyGuardError(c, err)
			return
		}

		if err := libvirtService.StorageDetach(req); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_detached",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Attach Storage to a Virtual Machine
// @Description Attach a storage volume to a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /storage/attach [post]
func StorageAttach(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.StorageAttachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}
		if err := libvirtService.RequireVMStorageTopologyMutable(req.RID); err != nil {
			writeVMStorageTopologyGuardError(c, err)
			return
		}

		ctx := c.Request.Context()

		if err := libvirtService.StorageAttach(req, ctx); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_attached",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Update Virtual Machine Storage
// @Description Update properties of a virtual machine's storage volume
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /storage/update [post]
func StorageUpdate(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.StorageUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}
		if err := libvirtService.RequireVMStorageRecordTopologyMutable(req.ID); err != nil {
			writeVMStorageTopologyGuardError(c, err)
			return
		}

		ctx := c.Request.Context()

		if err := libvirtService.StorageUpdate(req, ctx); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_updated",
			Data:    nil,
			Error:   "",
		})
	}
}
