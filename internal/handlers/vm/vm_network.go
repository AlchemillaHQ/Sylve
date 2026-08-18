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

type vmNetworkService interface {
	NetworkDetach(req libvirtServiceInterfaces.NetworkDetachRequest, ctx context.Context) error
	NetworkAttach(req libvirtServiceInterfaces.NetworkAttachRequest, ctx context.Context) (*vmModels.Network, error)
	NetworkUpdate(req libvirtServiceInterfaces.NetworkUpdateRequest, ctx context.Context) (*vmModels.Network, error)
}

func vmNetworkErrorCodes(err error) map[string]struct{} {
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

func vmNetworkErrorHasCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

func vmNetworkErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func vmNetworkErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	codes := vmNetworkErrorCodes(err)
	switch {
	case vmNetworkErrorHasCode(codes,
		"libvirt_not_initialized", "libvirt_connection_unavailable", "db_not_initialized",
		"failed_to_check_vm_shutoff", "failed_to_lookup_domain_by_name",
		"failed_to_lookup_domain", "failed_to_get_domain_state"):
		return http.StatusServiceUnavailable
	case vmNetworkErrorHasCode(codes,
		"invalid_request", "invalid_rid", "invalid_vm_rid", "invalid_network_id",
		"invalid_switch_name", "invalid_emulation_type", "invalid_mac_object_type",
		"invalid_mac_address", "mac_object_has_no_entries", "mac_object_has_multiple_entries",
		"network_mac_address_missing", "empty_network_update"):
		return http.StatusBadRequest
	case vmNetworkErrorHasCode(codes, "replication_lease_not_owned"):
		return http.StatusForbidden
	case vmNetworkErrorHasCode(codes,
		"vm_not_found", "network_not_found", "switch_not_found", "mac_object_not_found"):
		return http.StatusNotFound
	case vmNetworkErrorHasCode(codes,
		"domain_state_not_shutoff", "vm_is_active", "mac_object_already_in_use",
		"mac_address_already_in_use", "filesystem_dataset_mountpoint_not_usable"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeVMNetworkError(c *gin.Context, fallbackMessage string, err error) {
	status := vmNetworkErrorStatus(err)
	message := fallbackMessage
	if code := vmNetworkErrorCode(err); status != http.StatusInternalServerError && code != "" {
		message = code
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func bindVMNetworkPath(c *gin.Context) (uint, uint, bool) {
	rid, err := utils.ParamUint(c, "rid")
	if err != nil {
		writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
		return 0, 0, false
	}
	if rid == 0 {
		writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: rid_must_be_positive"))
		return 0, 0, false
	}

	networkID, err := utils.ParamUint(c, "networkId")
	if err != nil {
		writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
		return 0, 0, false
	}
	if networkID == 0 {
		writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: network_id_must_be_positive"))
		return 0, 0, false
	}

	return rid, networkID, true
}

// @Summary Detach network from a virtual machine
// @Description Detach a network interface from a shut-off virtual machine without deleting its MAC object
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param networkId path int true "Network Attachment ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/networks/{networkId} [delete]
func NetworkDetach(libvirtService vmNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, networkID, ok := bindVMNetworkPath(c)
		if !ok {
			return
		}

		req := libvirtServiceInterfaces.NetworkDetachRequest{RID: rid, NetworkID: networkID}
		if err := libvirtService.NetworkDetach(req, c.Request.Context()); err != nil {
			writeVMNetworkError(c, "network_detach_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_detached",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Attach network to a virtual machine
// @Description Attach a network interface to a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body libvirtServiceInterfaces.NetworkAttachRequest true "Network attachment"
// @Success 201 {object} internal.APIResponse[vmModels.Network] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/networks [post]
func NetworkAttach(libvirtService vmNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}
		if rid == 0 {
			writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: rid_must_be_positive"))
			return
		}

		var req libvirtServiceInterfaces.NetworkAttachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}
		req.RID = rid

		network, err := libvirtService.NetworkAttach(req, c.Request.Context())
		if err != nil {
			writeVMNetworkError(c, "network_attach_failed", err)
			return
		}
		if network == nil {
			writeVMNetworkError(c, "network_attach_failed", errors.New("network_attach_returned_empty_result"))
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[vmModels.Network]{
			Status:  "success",
			Message: "network_attached",
			Data:    *network,
			Error:   "",
		})
	}
}

// @Summary Update virtual machine network
// @Description Partially update a network interface attached to a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param networkId path int true "Network Attachment ID" minimum(1)
// @Param request body libvirtServiceInterfaces.NetworkUpdateRequest true "Network changes"
// @Success 200 {object} internal.APIResponse[vmModels.Network] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/networks/{networkId} [patch]
func NetworkUpdate(libvirtService vmNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, networkID, ok := bindVMNetworkPath(c)
		if !ok {
			return
		}

		var req libvirtServiceInterfaces.NetworkUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMNetworkError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}
		if req.SwitchName == nil && req.Emulation == nil && req.MacID == nil && req.Enable == nil {
			writeVMNetworkError(c, "invalid_request", errors.New("empty_network_update"))
			return
		}
		req.RID = rid
		req.NetworkID = networkID

		network, err := libvirtService.NetworkUpdate(req, c.Request.Context())
		if err != nil {
			writeVMNetworkError(c, "network_update_failed", err)
			return
		}
		if network == nil {
			writeVMNetworkError(c, "network_update_failed", errors.New("network_update_returned_empty_result"))
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[vmModels.Network]{
			Status:  "success",
			Message: "network_updated",
			Data:    *network,
			Error:   "",
		})
	}
}
