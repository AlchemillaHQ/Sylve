// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ModifyRAMRequest struct {
	RAM int `json:"ram" binding:"required"`
}

type ModifyPassthroughRequest struct {
	PCIDevices []int `json:"pciDevices" binding:"required"`
}

type vmHardwareService interface {
	ModifyCPU(rid uint, req libvirtServiceInterfaces.ModifyCPURequest) error
	ModifyRAM(rid uint, ram int) error
	ModifyVNC(rid uint, req libvirtServiceInterfaces.ModifyVNCRequest) error
	ModifyPassthrough(rid uint, pciDevices []int) error
}

func vmHardwareErrorCodes(err error) map[string]struct{} {
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

func vmHardwareErrorHasCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

func vmHardwareErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func vmHardwareErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	codes := vmHardwareErrorCodes(err)
	switch {
	case vmHardwareErrorHasCode(codes,
		"libvirt_not_initialized", "libvirt_connection_unavailable", "db_not_initialized",
		"replication_lease_check_failed", "failed_to_check_vm_shutoff",
		"failed_to_check_domain_shutoff_status", "failed_to_capture_domain_xml",
		"failed_to_lookup_domain", "failed_to_lookup_domain_by_name",
		"failed_to_get_domain_xml_desc", "failed_to_get_domain_state"):
		return http.StatusServiceUnavailable
	case vmHardwareErrorHasCode(codes,
		"invalid_request", "invalid_rid", "invalid_vm_rid", "rid_must_be_positive",
		"cpu_topology_must_be_positive", "cpu_topology_overflow",
		"cpu_pinning_exceeds_vcpu_count", "invalid_host_socket_count",
		"invalid_host_logical_cores", "socket_index_out_of_range",
		"duplicate_socket_in_request", "empty_core_list_for_socket",
		"core_index_out_of_range", "duplicate_core_within_socket",
		"calculated_core_out_of_range", "duplicate_core_across_sockets",
		"cpu_pinning_exceeds_logical_cores", "socket_capacity_exceeded",
		"memory_must_be_greater_than_128mb", "memory_must_be_at_least_128mb",
		"invalid_vnc_bind_ip",
		"vnc_enabled_required", "vnc_wait_required",
		"vnc_port_must_be_between_1_and_65535", "invalid_vnc_resolution_format",
		"invalid_vnc_resolution_width", "invalid_vnc_resolution_height",
		"vnc_resolution_out_of_range", "vnc_password_cannot_contain_commas",
		"invalid_passthrough_device_id", "duplicate_passthrough_device"):
		return http.StatusBadRequest
	case vmHardwareErrorHasCode(codes, "replication_lease_not_owned"):
		return http.StatusForbidden
	case vmHardwareErrorHasCode(codes,
		"vm_not_found", "passthrough_device_not_found", "passthrough_device_does_not_exist"):
		return http.StatusNotFound
	case vmHardwareErrorHasCode(codes,
		"domain_not_shutoff", "domain_state_not_shutoff", "core_conflict",
		"vnc_port_already_in_use_by_another_vm",
		"vnc_port_already_in_use_by_another_service", "no_free_passthrough_indices",
		"filesystem_dataset_mountpoint_not_usable"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeVMHardwareError(c *gin.Context, fallbackMessage string, err error) {
	status := vmHardwareErrorStatus(err)
	message := fallbackMessage
	if code := vmHardwareErrorCode(err); status != http.StatusInternalServerError && code != "" {
		message = code
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func bindVMHardwareRID(c *gin.Context) (uint, bool) {
	rid, err := utils.ParamUint(c, "rid")
	if err != nil {
		writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
		return 0, false
	}
	if rid == 0 {
		writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: rid_must_be_positive"))
		return 0, false
	}

	return rid, true
}

func writeVMHardwareSuccess(c *gin.Context, message string) {
	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: message,
		Data:    nil,
		Error:   "",
	})
}

func handleVMHardwareMutationResult(c *gin.Context, successMessage, fallbackMessage string, err error) {
	if err == nil {
		writeVMHardwareSuccess(c, successMessage)
		return
	}
	if vmHardwareErrorHasCode(vmHardwareErrorCodes(err), "no_changes_detected") {
		writeVMHardwareSuccess(c, "no_changes_detected")
		return
	}
	writeVMHardwareError(c, fallbackMessage, err)
}

// @Summary Replace virtual machine CPU configuration
// @Description Replace the CPU topology and pinning configuration of a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body libvirtServiceInterfaces.ModifyCPURequest true "CPU configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/hardware/cpu [put]
func ModifyCPU(libvirtService vmHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMHardwareRID(c)
		if !ok {
			return
		}

		var req libvirtServiceInterfaces.ModifyCPURequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}

		handleVMHardwareMutationResult(
			c,
			"cpu_modified",
			"cpu_modification_failed",
			libvirtService.ModifyCPU(rid, req),
		)
	}
}

// @Summary Replace virtual machine RAM configuration
// @Description Replace the memory allocation of a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyRAMRequest true "RAM configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/hardware/ram [put]
func ModifyRAM(libvirtService vmHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMHardwareRID(c)
		if !ok {
			return
		}

		var req ModifyRAMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}

		handleVMHardwareMutationResult(
			c,
			"ram_modified",
			"ram_modification_failed",
			libvirtService.ModifyRAM(rid, req.RAM),
		)
	}
}

// @Summary Replace virtual machine VNC configuration
// @Description Replace the VNC configuration of a shut-off virtual machine; an empty password clears VNC authentication
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body libvirtServiceInterfaces.ModifyVNCRequest true "VNC configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/hardware/vnc [put]
func ModifyVNC(libvirtService vmHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMHardwareRID(c)
		if !ok {
			return
		}

		var req libvirtServiceInterfaces.ModifyVNCRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}

		handleVMHardwareMutationResult(
			c,
			"vnc_modified",
			"vnc_modification_failed",
			libvirtService.ModifyVNC(rid, req),
		)
	}
}

// @Summary Replace virtual machine PCI passthrough configuration
// @Description Replace the PCI passthrough device assignments of a shut-off virtual machine; an empty array clears all assignments
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyPassthroughRequest true "PCI passthrough configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/hardware/pci-devices [put]
func ModifyPassthroughDevices(libvirtService vmHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMHardwareRID(c)
		if !ok {
			return
		}

		var req ModifyPassthroughRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeVMHardwareError(c, "invalid_request", errors.New("invalid_request: "+err.Error()))
			return
		}

		handleVMHardwareMutationResult(
			c,
			"pci_devices_modified",
			"pci_devices_modification_failed",
			libvirtService.ModifyPassthrough(rid, req.PCIDevices),
		)
	}
}
