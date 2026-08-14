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
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type vmStorageService interface {
	StorageDetach(req libvirtServiceInterfaces.StorageDetachRequest, ctx context.Context) error
	StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) (*vmModels.Storage, error)
	StorageUpdate(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) (*vmModels.Storage, error)
}

func vmStorageErrorCodes(err error) map[string]struct{} {
	codes := make(map[string]struct{})
	if err == nil {
		return codes
	}

	for _, part := range strings.Split(strings.ToLower(err.Error()), ":") {
		if code := strings.TrimSpace(part); code != "" {
			codes[code] = struct{}{}
		}
	}
	return codes
}

func vmStorageErrorHasCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

func vmStorageErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func vmStorageErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	codes := vmStorageErrorCodes(err)
	switch {
	case vmStorageErrorHasCode(codes,
		"libvirt_not_initialized", "libvirt_connection_unavailable", "gzfs_not_initialized",
		"system_service_not_initialized", "db_not_initialized", "failed_to_check_vm_shutoff",
		"failed_to_lookup_domain_by_name", "failed_to_get_domain_state"):
		return http.StatusServiceUnavailable
	case vmStorageErrorHasCode(codes,
		"invalid_request", "invalid_rid", "invalid_storage_id", "invalid_storage_name",
		"invalid_storage_type", "invalid_storage_emulation", "invalid_storage_attach_type",
		"invalid_attach_type_for_filesystem_storage", "invalid_attach_type_for_image_storage",
		"invalid_pool", "invalid_size", "invalid_boot_order", "invalid_record_size",
		"invalid_volblock_size", "invalid_raw_path", "raw_path_must_be_regular_file",
		"filesystem_dataset_guid_required", "zvol_dataset_guid_required",
		"download_uuid_required", "invalid_filesystem_target_name", "empty_storage_update"):
		return http.StatusBadRequest
	case vmStorageErrorHasCode(codes, "replication_lease_not_owned"):
		return http.StatusForbidden
	case vmStorageErrorHasCode(codes,
		"vm_not_found", "storage_not_found", "pool_not_found", "raw_path_does_not_exist",
		"zvol_dataset_not_found", "filesystem_dataset_not_found", "download_not_found",
		"target_zvol_dataset_not_found", "zvol_dataset_not_found_in_pool"):
		return http.StatusNotFound
	case vmStorageErrorHasCode(codes,
		"replication_storage_topology_change_requires_policy_disabled",
		"domain_state_not_shutoff", "boot_order_index_already_in_use",
		"storage_dataset_already_exists", "zvol_dataset_already_attached",
		"filesystem_target_already_in_use", "insufficient_space_in_pool",
		"filesystem_dataset_mountpoint_not_usable",
		"shrinking_storage_not_supported", "new_size_must_be_greater_than_or_equal_to_current_volsize",
		"size_edit_not_supported_for_disk_image_storage", "size_edit_not_supported_for_filesystem_storage",
		"size_edit_not_supported_for_storage_type", "no_free_indices_available",
		"no_free_indices_available_for_cloud_init_iso"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeVMStorageError(c *gin.Context, fallbackMessage string, err error) {
	status := vmStorageErrorStatus(err)
	message := fallbackMessage
	if code := vmStorageErrorCode(err); status != http.StatusInternalServerError && code != "" {
		message = code
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func bindVMStoragePath(c *gin.Context) (uint, uint, bool) {
	rid, err := utils.ParamUint(c, "rid")
	if err != nil {
		writeVMStorageError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
		return 0, 0, false
	}

	storageID, err := utils.ParamUint(c, "storageId")
	if err != nil {
		writeVMStorageError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
		return 0, 0, false
	}

	return rid, storageID, true
}

// @Summary Detach storage from a virtual machine
// @Description Detach a storage device from a shut-off virtual machine without deleting its underlying dataset or file
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param storageId path int true "Storage ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/storage/{storageId} [delete]
func StorageDetach(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, storageID, ok := bindVMStoragePath(c)
		if !ok {
			return
		}

		req := libvirtServiceInterfaces.StorageDetachRequest{RID: rid, StorageID: storageID}
		if err := libvirtService.StorageDetach(req, c.Request.Context()); err != nil {
			writeVMStorageError(c, "storage_detach_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_detached",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Attach storage to a virtual machine
// @Description Create or import and attach a storage device to a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body libvirtServiceInterfaces.StorageAttachRequest true "Storage attachment"
// @Success 201 {object} internal.APIResponse[vmModels.Storage] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/storage [post]
func StorageAttach(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			writeVMStorageError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}

		var req libvirtServiceInterfaces.StorageAttachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMStorageError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}
		req.RID = rid

		storage, err := libvirtService.StorageAttach(req, c.Request.Context())
		if err != nil {
			writeVMStorageError(c, "storage_attach_failed", err)
			return
		}
		if storage == nil {
			writeVMStorageError(c, "storage_attach_failed", errors.New("storage_attach_returned_empty_result"))
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[vmModels.Storage]{
			Status:  "success",
			Message: "storage_attached",
			Data:    *storage,
			Error:   "",
		})
	}
}

// @Summary Update virtual machine storage
// @Description Partially update an attached storage device on a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param storageId path int true "Storage ID" minimum(1)
// @Param request body libvirtServiceInterfaces.StorageUpdateRequest true "Storage changes"
// @Success 200 {object} internal.APIResponse[vmModels.Storage] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/storage/{storageId} [patch]
func StorageUpdate(libvirtService vmStorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, storageID, ok := bindVMStoragePath(c)
		if !ok {
			return
		}

		var req libvirtServiceInterfaces.StorageUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMStorageError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}
		if req.Name == nil && req.Size == nil && req.Emulation == nil && req.BootOrder == nil &&
			req.Enable == nil && req.FilesystemTarget == nil && req.ReadOnly == nil {
			writeVMStorageError(c, "invalid_request", errors.New("empty_storage_update"))
			return
		}
		req.RID = rid
		req.ID = storageID

		storage, err := libvirtService.StorageUpdate(req, c.Request.Context())
		if err != nil {
			writeVMStorageError(c, "storage_update_failed", err)
			return
		}
		if storage == nil {
			writeVMStorageError(c, "storage_update_failed", errors.New("storage_update_returned_empty_result"))
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[vmModels.Storage]{
			Status:  "success",
			Message: "storage_updated",
			Data:    *storage,
			Error:   "",
		})
	}
}
