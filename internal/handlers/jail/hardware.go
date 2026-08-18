// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type JailUpdateMemoryRequest struct {
	Memory int64 `json:"memory" binding:"required"`
}

type JailUpdateCPURequest struct {
	Cores int64 `json:"cores" binding:"required"`
}

type JailUpdateResourceLimitsRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type jailHardwareService interface {
	UpdateMemory(ctID uint, memoryBytes int64) (jailServiceInterfaces.JailHardwareResult, error)
	UpdateCPU(ctID uint, cores int64) (jailServiceInterfaces.JailHardwareResult, error)
	UpdateResourceLimits(ctID uint, enabled bool) (jailServiceInterfaces.JailHardwareResult, error)
}

func jailHardwareErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	if idx := strings.IndexByte(code, '\n'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func jailHardwareErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	switch jailHardwareErrorCode(err) {
	case "invalid_request_data", "invalid_ct_id", "invalid_memory", "memory_limit_too_low",
		"memory_limit_exceeds_host_capacity", "invalid_cores":
		return http.StatusBadRequest
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "jail_not_found":
		return http.StatusNotFound
	case "restore_in_progress", "resource_limits_disabled", "jail_hardware_state_invalid",
		"jail_config_not_found", "hook_script_not_found", "jail_hardware_hook_conflict",
		"jail_hardware_config_conflict", "jail_runtime_state_changed",
		"jail_dataset_mountpoint_not_usable":
		return http.StatusConflict
	case "jail_service_not_initialized", "host_cpu_unavailable", "failed_to_get_host_memory",
		"failed_to_get_jail_state":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeJailHardwareError(c *gin.Context, fallbackMessage string, err error) {
	status := jailHardwareErrorStatus(err)
	message := fallbackMessage
	if status != http.StatusInternalServerError {
		if code := jailHardwareErrorCode(err); code != "" {
			message = code
		}
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func positiveJailHardwareParam(c *gin.Context, name string) (uint, bool) {
	value, err := utils.ParamUint(c, name)
	if err != nil || value == 0 {
		writeJailHardwareError(c, "invalid_ct_id", errors.New("invalid_ct_id"))
		return 0, false
	}
	return value, true
}

// @Summary Update jail RAM limit
// @Description Update the persisted RAM limit for a jail and apply it immediately when the jail is running
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body JailUpdateMemoryRequest true "RAM limit request"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.JailHardwareResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/hardware/ram [put]
func UpdateJailMemory(jailService jailHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailHardwareParam(c, "ctid")
		if !ok {
			return
		}

		var req JailUpdateMemoryRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		result, err := jailService.UpdateMemory(ctID, req.Memory)
		if err != nil {
			writeJailHardwareError(c, "failed_to_update_memory", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.JailHardwareResult]{
			Status:  "success",
			Message: "jail_memory_updated",
			Data:    result,
			Error:   "",
		})
	}
}

// @Summary Update jail CPU limit
// @Description Update the persisted CPU-core limit for a jail and apply it immediately when the jail is running
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body JailUpdateCPURequest true "CPU limit request"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.JailHardwareResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/hardware/cpu [put]
func UpdateJailCPU(jailService jailHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailHardwareParam(c, "ctid")
		if !ok {
			return
		}

		var req JailUpdateCPURequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		result, err := jailService.UpdateCPU(ctID, req.Cores)
		if err != nil {
			writeJailHardwareError(c, "failed_to_update_cpu", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.JailHardwareResult]{
			Status:  "success",
			Message: "jail_cpu_updated",
			Data:    result,
			Error:   "",
		})
	}
}

// @Summary Set jail resource limits
// @Description Enable or disable persisted RAM and CPU limits for a jail and reconcile the live runtime when it is running
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body JailUpdateResourceLimitsRequest true "Resource-limit state request"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.JailHardwareResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/hardware/resource-limits [put]
func UpdateResourceLimits(jailService jailHardwareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailHardwareParam(c, "ctid")
		if !ok {
			return
		}

		var req JailUpdateResourceLimitsRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		result, err := jailService.UpdateResourceLimits(ctID, *req.Enabled)
		if err != nil {
			writeJailHardwareError(c, "failed_to_update_resource_limits", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.JailHardwareResult]{
			Status:  "success",
			Message: "jail_resource_limits_updated",
			Data:    result,
			Error:   "",
		})
	}
}
