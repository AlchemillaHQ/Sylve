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
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

type vmTemplateService interface {
	GetVMTemplatesSimple() ([]libvirtServiceInterfaces.SimpleTemplateList, error)
	GetVMTemplate(templateID uint) (*vmModels.VMTemplate, error)
	PreflightConvertVMToTemplate(ctx context.Context, rid uint, req libvirtServiceInterfaces.ConvertToTemplateRequest) error
	PreflightCreateVMsFromTemplate(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error
	DeleteVMTemplate(ctx context.Context, templateID uint) error
}

func vmTemplatePreflightStatusCode(err error) int {
	if err == nil {
		return http.StatusBadRequest
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "replication_lease_not_owned"):
		return http.StatusForbidden
	case strings.Contains(msg, "failed_to_"), strings.Contains(msg, "replication_lease_check_failed"):
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

func ListVMTemplatesSimple(libvirtService vmTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templates, err := libvirtService.GetVMTemplatesSimple()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_vm_templates_simple",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]libvirtServiceInterfaces.SimpleTemplateList]{
			Status:  "success",
			Message: "vm_templates_listed_simple",
			Data:    templates,
			Error:   "",
		})
	}
}

func GetVMTemplateByID(libvirtService vmTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("id"))
		if err != nil || templateID <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		template, err := libvirtService.GetVMTemplate(uint(templateID))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "template_not_found") {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "template_not_found",
					Data:    nil,
					Error:   err.Error(),
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*vmModels.VMTemplate]{
			Status:  "success",
			Message: "vm_template_retrieved",
			Data:    template,
			Error:   "",
		})
	}
}

func ConvertVMToTemplate(libvirtService vmTemplateService, lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := strconv.Atoi(c.Param("rid"))
		if err != nil || rid <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid",
				Data:    nil,
				Error:   "rid must be a positive integer",
			})
			return
		}

		var req libvirtServiceInterfaces.ConvertToTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		if err := libvirtService.PreflightConvertVMToTemplate(c.Request.Context(), uint(rid), req); err != nil {
			c.JSON(vmTemplatePreflightStatusCode(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "template_convert_preflight_failed",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		payload, err := json.Marshal(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))
		task, outcome, err := lifecycleService.RequestActionWithPayload(
			c.Request.Context(),
			taskModels.GuestTypeVMTemplate,
			uint(rid),
			"convert",
			taskModels.LifecycleTaskSourceUser,
			username,
			string(payload),
		)
		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "lifecycle_task_in_progress",
					Data:    gin.H{"task": task},
					Error:   err.Error(),
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_template_convert_queued",
			Data:    gin.H{"task": task, "outcome": outcome},
			Error:   "",
		})
	}
}

func CreateVMFromTemplate(libvirtService vmTemplateService, lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("id"))
		if err != nil || templateID <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		var req libvirtServiceInterfaces.CreateFromTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		if err := libvirtService.PreflightCreateVMsFromTemplate(c.Request.Context(), uint(templateID), req); err != nil {
			c.JSON(vmTemplatePreflightStatusCode(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "template_create_preflight_failed",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		payload, err := json.Marshal(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))
		task, outcome, err := lifecycleService.RequestActionWithPayload(
			c.Request.Context(),
			taskModels.GuestTypeVMTemplate,
			uint(templateID),
			"create",
			taskModels.LifecycleTaskSourceUser,
			username,
			string(payload),
		)
		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "lifecycle_task_in_progress",
					Data:    gin.H{"task": task},
					Error:   err.Error(),
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_template_create_queued",
			Data:    gin.H{"task": task, "outcome": outcome},
			Error:   "",
		})
	}
}

func DeleteVMTemplate(libvirtService vmTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("id"))
		if err != nil || templateID <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		if err := libvirtService.DeleteVMTemplate(c.Request.Context(), uint(templateID)); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(strings.ToLower(err.Error()), "template_not_found") {
				status = http.StatusNotFound
			} else if strings.Contains(strings.ToLower(err.Error()), "replication_lease_not_owned") {
				status = http.StatusForbidden
			}

			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_vm_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_template_deleted",
			Data:    nil,
			Error:   "",
		})
	}
}
