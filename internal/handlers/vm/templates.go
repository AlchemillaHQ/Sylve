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

type vmTemplateLifecycleService interface {
	RequestActionWithPayload(
		ctx context.Context,
		guestType string,
		guestID uint,
		action string,
		source string,
		requestedBy string,
		payload string,
	) (*taskModels.GuestLifecycleTask, string, error)
	ListActiveTasks(guestType string, guestID uint) ([]taskModels.GuestLifecycleTask, error)
}

type VMTemplateCaptureTaskResponse struct {
	TaskID    uint   `json:"taskId"`
	SourceRID uint   `json:"sourceRid"`
	Action    string `json:"action"`
	Outcome   string `json:"outcome"`
}

type VMTemplateInstantiationTaskResponse struct {
	TaskID     uint   `json:"taskId"`
	TemplateID uint   `json:"templateId"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
}

func vmTemplateErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func vmTemplatePreflightStatusCode(err error) int {
	if err == nil {
		return http.StatusBadRequest
	}

	switch vmTemplateErrorCode(err) {
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "guest_identity_inventory_unavailable":
		return http.StatusServiceUnavailable
	case "vm_not_found",
		"template_not_found",
		"pool_not_found",
		"switch_not_found",
		"template_network_switch_not_found",
		"source_storage_dataset_not_found",
		"template_storage_dataset_missing",
		"template_storage_dataset_not_found":
		return http.StatusNotFound
	case "template_name_already_in_use",
		"guest_id_already_in_use",
		"guest_identity_inventory_conflict",
		"rid_range_contains_used_values",
		"vm_name_already_in_use",
		"vm_must_be_shut_off",
		"no_cloneable_storage",
		"template_has_no_cloneable_storage",
		"insufficient_pool_space",
		"target_vm_dataset_already_exists",
		"target_storage_dataset_already_exists",
		"template_storage_dataset_already_exists",
		"no_available_vnc_port":
		return http.StatusConflict
	case "guest_identity_inventory_scan_failed", "replication_lease_check_failed":
		return http.StatusInternalServerError
	}

	if strings.HasPrefix(vmTemplateErrorCode(err), "failed_to_") {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}

// @Summary List VM templates
// @Description Retrieve the VM template collection using its simple list representation
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]libvirtServiceInterfaces.SimpleTemplateList] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/templates [get]
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

// @Summary Get a VM template
// @Description Retrieve a VM template by its template ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "VM Template ID" minimum(1)
// @Success 200 {object} internal.APIResponse[vmModels.VMTemplate] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/templates/{templateId} [get]
func GetVMTemplateByID(libvirtService vmTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("templateId"))
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

// @Summary Capture a VM as a template
// @Description Validate and queue creation of a VM template from a shut-off source VM without deleting the source VM
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Source Virtual Machine RID" minimum(1)
// @Param request body libvirtServiceInterfaces.ConvertToTemplateRequest true "Template capture request"
// @Success 202 {object} internal.APIResponse[VMTemplateCaptureTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/templates [post]
func ConvertVMToTemplate(libvirtService vmTemplateService, lifecycleService vmTemplateLifecycleService) gin.HandlerFunc {
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
					Data:    nil,
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

		if task == nil || task.ID == 0 {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   "lifecycle_task_missing",
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", "vm_template_convert")

		c.JSON(http.StatusAccepted, internal.APIResponse[VMTemplateCaptureTaskResponse]{
			Status:  "success",
			Message: "vm_template_convert_queued",
			Data: VMTemplateCaptureTaskResponse{
				TaskID:    task.ID,
				SourceRID: uint(rid),
				Action:    "convert",
				Outcome:   outcome,
			},
			Error: "",
		})
	}
}

// @Summary Create VMs from a template
// @Description Validate and queue creation of one or more virtual machines from a VM template
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "VM Template ID" minimum(1)
// @Param request body libvirtServiceInterfaces.CreateFromTemplateRequest true "VM template instantiation request"
// @Success 202 {object} internal.APIResponse[VMTemplateInstantiationTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/templates/{templateId}/vms [post]
func CreateVMFromTemplate(libvirtService vmTemplateService, lifecycleService vmTemplateLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("templateId"))
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
					Data:    nil,
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

		if task == nil || task.ID == 0 {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   "lifecycle_task_missing",
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", "vm_template_create")

		c.JSON(http.StatusAccepted, internal.APIResponse[VMTemplateInstantiationTaskResponse]{
			Status:  "success",
			Message: "vm_template_create_queued",
			Data: VMTemplateInstantiationTaskResponse{
				TaskID:     task.ID,
				TemplateID: uint(templateID),
				Action:     "create",
				Outcome:    outcome,
			},
			Error: "",
		})
	}
}

// @Summary Delete a VM template
// @Description Delete a VM template and its stored template datasets when no VM creation is active
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "VM Template ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/templates/{templateId} [delete]
func DeleteVMTemplate(libvirtService vmTemplateService, lifecycleService vmTemplateLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("templateId"))
		if err != nil || templateID <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		activeTasks, err := lifecycleService.ListActiveTasks(taskModels.GuestTypeVMTemplate, uint(templateID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_check_vm_template_usage",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		for _, task := range activeTasks {
			if strings.EqualFold(strings.TrimSpace(task.Action), "create") {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_template_in_use",
					Data:    nil,
					Error:   "vm_template_creation_in_progress",
				})
				return
			}
		}

		if err := libvirtService.DeleteVMTemplate(c.Request.Context(), uint(templateID)); err != nil {
			status := http.StatusInternalServerError
			if vmTemplateErrorCode(err) == "template_not_found" {
				status = http.StatusNotFound
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
