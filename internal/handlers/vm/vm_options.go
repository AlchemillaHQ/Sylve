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
	"fmt"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ModifyWakeOnLanRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type ModifyBootOrderRequest struct {
	StartAtBoot *bool `json:"startAtBoot" binding:"required"`
	BootOrder   *int  `json:"bootOrder" binding:"required"`
}

type ModifyClockRequest struct {
	TimeOffset string `json:"timeOffset" binding:"required"`
}

type ModifySerialConsoleRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type ModifyShutdownWaitTimeRequest struct {
	WaitTime *int `json:"waitTime" binding:"required"`
}

type ModifyCloudInitDataRequest struct {
	Data          *string `json:"data" binding:"required"`
	Metadata      *string `json:"metadata" binding:"required"`
	NetworkConfig *string `json:"networkConfig" binding:"required"`
}

type ModifyBootROMRequest struct {
	BootROM string `json:"bootRom" binding:"required"`
}

type ModifyExtraBhyveOptionsRequest struct {
	ExtraBhyveOptions *[]string `json:"extraBhyveOptions" binding:"required"`
}

type ModifyIgnoreUMSRsRequest struct {
	IgnoreUMSRs *bool `json:"ignoreUMSRs" binding:"required"`
}

type ModifyQemuGuestAgentRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type ModifyTPMRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type vmOptionsService interface {
	ModifyWakeOnLan(rid uint, enabled bool) error
	ModifyBootOrder(rid uint, startAtBoot bool, bootOrder int) error
	ModifyClock(rid uint, timeOffset string) error
	ModifySerial(rid uint, enabled bool) error
	ModifyShutdownWaitTime(rid uint, waitTime int) error
	ModifyCloudInitData(rid uint, data string, metadata string, networkConfig string) error
	ModifyBootROM(rid uint, bootROM string) error
	ModifyExtraBhyveOptions(rid uint, options []string) error
	ModifyIgnoreUMSRs(rid uint, ignore bool) error
	ModifyQemuGuestAgent(rid uint, enabled bool) error
	ModifyTPMEmulation(rid uint, enabled bool) error
}

func vmOptionErrorCodeSet(err error) map[string]struct{} {
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

func vmOptionHasErrorCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

func vmOptionHasErrorCodePrefix(codes map[string]struct{}, prefix string) bool {
	for code := range codes {
		if strings.HasPrefix(code, prefix) {
			return true
		}
	}
	return false
}

func classifyVMOptionError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "internal_server_error"
	}

	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge, "request_body_too_large"
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound, "vm_not_found"
	}

	codes := vmOptionErrorCodeSet(err)
	switch {
	case vmOptionHasErrorCode(codes,
		"invalid_request", "invalid_rid", "invalid_vm_rid", "rid_must_be_positive",
		"invalid_time_offset", "invalid_boot_rom", "uefi_firmware_not_available_on_arm64",
		"uboot_only_available_on_arm64", "start_order_must_be_greater_than_or_equal_to_0",
		"shutdown_wait_time_out_of_range", "both_data_and_metadata_must_be_provided",
		"invalid_yaml_in_cloud_init_data_or_metadata", "invalid_yaml_in_cloud_init_network_config",
		"too_many_extra_bhyve_options", "extra_bhyve_option_too_long",
		"extra_bhyve_options_too_large", "invalid_extra_bhyve_option"):
		return http.StatusBadRequest, firstVMOptionErrorCode(err)
	case vmOptionHasErrorCode(codes, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case vmOptionHasErrorCode(codes, "vm_not_found"):
		return http.StatusNotFound, "vm_not_found"
	case vmOptionHasErrorCode(codes,
		"domain_state_not_shutoff", "qemu_guest_agent_disabled", "qga_requires_running_vm",
		"filesystem_dataset_mountpoint_not_usable"):
		return http.StatusConflict, firstVMOptionErrorCode(err)
	case vmOptionHasErrorCode(codes,
		"libvirt_not_initialized", "libvirt_connection_unavailable",
		"failed_to_check_vm_shutoff", "failed_to_lookup_domain_by_name",
		"failed_to_get_domain_state", "failed_to_get_domain_xml_desc",
		"failed_to_run_qga_command", "failed_to_lookup_domain_for_qga",
		"failed_to_connect_qga_socket", "failed_to_set_qga_deadline",
		"failed_to_send_qga_command", "failed_to_decode_qga_response",
		"failed_to_unmarshal_qga_return", "invalid_qga_response",
		"invalid_qga_response_from_libvirt") || vmOptionHasErrorCodePrefix(codes, "qga_error_"):
		return http.StatusServiceUnavailable, firstVMOptionErrorCode(err)
	default:
		return http.StatusInternalServerError, "internal_server_error"
	}
}

func firstVMOptionErrorCode(err error) string {
	if err == nil {
		return "internal_server_error"
	}
	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if index := strings.IndexByte(code, ':'); index >= 0 {
		code = code[:index]
	}
	if code == "" {
		return "internal_server_error"
	}
	return code
}

func writeVMOptionError(c *gin.Context, err error) {
	status, message := classifyVMOptionError(err)
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func writeVMOptionSuccess(c *gin.Context, message string) {
	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: message,
		Data:    nil,
		Error:   "",
	})
}

func handleVMOptionMutationResult(c *gin.Context, successMessage string, err error) {
	if err == nil {
		writeVMOptionSuccess(c, successMessage)
		return
	}
	if vmOptionHasErrorCode(vmOptionErrorCodeSet(err), "no_changes_detected") {
		writeVMOptionSuccess(c, "no_changes_detected")
		return
	}
	writeVMOptionError(c, err)
}

func bindVMOptionRID(c *gin.Context) (uint, bool) {
	rid, err := utils.ParamUint(c, "rid")
	if err != nil {
		writeVMOptionError(c, fmt.Errorf("invalid_request: %w", err))
		return 0, false
	}
	if rid == 0 {
		writeVMOptionError(c, errors.New("rid_must_be_positive"))
		return 0, false
	}
	return rid, true
}

func bindVMOptionJSON(c *gin.Context, request any) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		writeVMOptionError(c, fmt.Errorf("invalid_request: %w", err))
		return false
	}
	return true
}

// @Summary Replace virtual machine Wake-on-LAN configuration
// @Description Enable or disable Wake-on-LAN for a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyWakeOnLanRequest true "Wake-on-LAN configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/options/wol [put]
func ModifyWakeOnLan(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyWakeOnLanRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(c, "wol_modified", service.ModifyWakeOnLan(rid, *req.Enabled))
	}
}

// @Summary Replace virtual machine automatic-start configuration
// @Description Configure whether a virtual machine starts at boot and its relative start order
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyBootOrderRequest true "Automatic-start configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/options/boot-order [put]
func ModifyBootOrder(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyBootOrderRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"boot_order_modified",
			service.ModifyBootOrder(rid, *req.StartAtBoot, *req.BootOrder),
		)
	}
}

// @Summary Replace virtual machine clock configuration
// @Description Set the virtual machine clock offset to UTC or local time while it is shut off
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyClockRequest true "Clock configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/clock [put]
func ModifyClock(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyClockRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(c, "clock_modified", service.ModifyClock(rid, req.TimeOffset))
	}
}

// @Summary Replace virtual machine serial-console configuration
// @Description Enable or disable serial-console access for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifySerialConsoleRequest true "Serial-console configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/serial-console [put]
func ModifySerialConsole(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifySerialConsoleRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(c, "serial_console_modified", service.ModifySerial(rid, *req.Enabled))
	}
}

// @Summary Replace virtual machine shutdown wait time
// @Description Set how long a graceful virtual machine shutdown may wait before forced termination
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyShutdownWaitTimeRequest true "Shutdown wait-time configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/options/shutdown-wait-time [put]
func ModifyShutdownWaitTime(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyShutdownWaitTimeRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"shutdown_wait_time_modified",
			service.ModifyShutdownWaitTime(rid, *req.WaitTime),
		)
	}
}

// @Summary Replace virtual machine cloud-init configuration
// @Description Replace or explicitly clear cloud-init user data, metadata, and network configuration for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyCloudInitDataRequest true "Cloud-init configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/cloud-init [put]
func ModifyCloudInitData(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyCloudInitDataRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"cloud_init_data_modified",
			service.ModifyCloudInitData(rid, *req.Data, *req.Metadata, *req.NetworkConfig),
		)
	}
}

// @Summary Replace virtual machine boot ROM configuration
// @Description Select the boot ROM mode for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyBootROMRequest true "Boot ROM configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/boot-rom [put]
func ModifyBootROM(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyBootROMRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(c, "boot_rom_modified", service.ModifyBootROM(rid, req.BootROM))
	}
}

// @Summary Replace virtual machine extra bhyve options
// @Description Replace custom bhyve arguments for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyExtraBhyveOptionsRequest true "Extra bhyve options"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/extra-bhyve-options [put]
func ModifyExtraBhyveOptions(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyExtraBhyveOptionsRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"extra_bhyve_options_modified",
			service.ModifyExtraBhyveOptions(rid, *req.ExtraBhyveOptions),
		)
	}
}

// @Summary Replace virtual machine unknown-MSR handling
// @Description Enable or disable ignoring accesses to unimplemented model-specific registers for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyIgnoreUMSRsRequest true "Unknown-MSR configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/ignore-umsrs [put]
func ModifyIgnoreUMSRs(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyIgnoreUMSRsRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"ignore_umsrs_modified",
			service.ModifyIgnoreUMSRs(rid, *req.IgnoreUMSRs),
		)
	}
}

// @Summary Replace virtual machine QEMU Guest Agent configuration
// @Description Enable or disable the QEMU Guest Agent channel for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyQemuGuestAgentRequest true "QEMU Guest Agent configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/qemu-guest-agent [put]
func ModifyQemuGuestAgent(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyQemuGuestAgentRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(
			c,
			"qemu_guest_agent_modified",
			service.ModifyQemuGuestAgent(rid, *req.Enabled),
		)
	}
}

// @Summary Replace virtual machine TPM-emulation configuration
// @Description Enable or disable TPM emulation for a shut-off virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body ModifyTPMRequest true "TPM-emulation configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/options/tpm [put]
func ModifyTPM(service vmOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := bindVMOptionRID(c)
		if !ok {
			return
		}
		var req ModifyTPMRequest
		if !bindVMOptionJSON(c, &req) {
			return
		}
		handleVMOptionMutationResult(c, "tpm_modified", service.ModifyTPMEmulation(rid, *req.Enabled))
	}
}
