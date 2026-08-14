// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

type jailTemplateService interface {
	GetJailTemplatesSimple() ([]jailServiceInterfaces.SimpleTemplateList, error)
	GetJailTemplate(templateID uint) (*jailModels.JailTemplate, error)
	PreflightConvertJailToTemplate(ctx context.Context, ctID uint, req jail.ConvertToTemplateRequest) error
	PreflightCreateJailsFromTemplate(ctx context.Context, templateID uint, req jail.CreateFromTemplateRequest) error
	DeleteJailTemplate(ctx context.Context, templateID uint) error
}

type jailTemplateLifecycleService interface {
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

type JailTemplateCaptureTaskResponse struct {
	TaskID     uint   `json:"taskId"`
	SourceCTID uint   `json:"sourceCtid"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
}

type JailTemplateInstantiationTaskResponse struct {
	TaskID     uint   `json:"taskId"`
	TemplateID uint   `json:"templateId"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
}

func jailTemplateErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func jailTemplatePreflightStatusCode(err error) int {
	if err == nil {
		return http.StatusBadRequest
	}

	switch jailTemplateErrorCode(err) {
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "guest_identity_inventory_unavailable", "template_network_service_unavailable":
		return http.StatusServiceUnavailable
	case "jail_not_found",
		"template_not_found",
		"pool_not_found",
		"jail_base_pool_not_found",
		"source_jail_dataset_not_found",
		"template_dataset_not_found",
		"template_network_switch_not_found",
		"switch_not_found":
		return http.StatusNotFound
	case "template_name_already_in_use",
		"guest_id_already_in_use",
		"guest_identity_inventory_conflict",
		"ctid_range_contains_used_values",
		"jail_name_already_in_use",
		"jail_must_be_stopped",
		"jail_has_active_lifecycle_task",
		"insufficient_pool_space",
		"target_dataset_already_exists",
		"jail_template_in_use":
		return http.StatusConflict
	case "guest_identity_inventory_scan_failed", "replication_lease_check_failed":
		return http.StatusInternalServerError
	}
	if strings.HasPrefix(jailTemplateErrorCode(err), "failed_to_") {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}

// @Summary List jail templates
// @Description Retrieve the jail template collection using its simple list representation
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.SimpleTemplateList] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/templates [get]
func ListJailTemplatesSimple(jailService jailTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templates, err := jailService.GetJailTemplatesSimple()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_jail_templates_simple",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailServiceInterfaces.SimpleTemplateList]{
			Status:  "success",
			Message: "jail_templates_listed_simple",
			Data:    templates,
			Error:   "",
		})
	}
}

// @Summary Get a jail template
// @Description Retrieve a jail template by its template ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "Jail Template ID" minimum(1)
// @Success 200 {object} internal.APIResponse[jailModels.JailTemplate] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/templates/{templateId} [get]
func GetJailTemplateByID(jailService jailTemplateService) gin.HandlerFunc {
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

		template, err := jailService.GetJailTemplate(uint(templateID))
		if err != nil {
			if jailTemplateErrorCode(err) == "template_not_found" {
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
				Message: "failed_to_get_jail_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		if template == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail_template",
				Data:    nil,
				Error:   "template_response_missing",
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*jailModels.JailTemplate]{
			Status:  "success",
			Message: "jail_template_retrieved",
			Data:    template,
			Error:   "",
		})
	}
}

// @Summary Capture a jail as a template
// @Description Validate and queue creation of a jail template from a stopped source jail without deleting the source jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Source Jail CTID" minimum(1)
// @Param request body jail.ConvertToTemplateRequest true "Template capture request"
// @Success 202 {object} internal.APIResponse[JailTemplateCaptureTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/templates [post]
func ConvertJailToTemplate(jailService jailTemplateService, lifecycleService jailTemplateLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := strconv.Atoi(c.Param("ctid"))
		if err != nil || ctID <= 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Data:    nil,
				Error:   "ctid must be a positive integer",
			})
			return
		}

		var req jail.ConvertToTemplateRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		if err := jailService.PreflightConvertJailToTemplate(c.Request.Context(), uint(ctID), req); err != nil {
			c.JSON(jailTemplatePreflightStatusCode(err), internal.APIResponse[any]{
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
			taskModels.GuestTypeJailTemplate,
			uint(ctID),
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
		c.Set("AuditAsyncJobType", "jail_template_convert")

		c.JSON(http.StatusAccepted, internal.APIResponse[JailTemplateCaptureTaskResponse]{
			Status:  "success",
			Message: "jail_template_convert_queued",
			Data: JailTemplateCaptureTaskResponse{
				TaskID:     task.ID,
				SourceCTID: uint(ctID),
				Action:     "convert",
				Outcome:    outcome,
			},
			Error: "",
		})
	}
}

// @Summary Create jails from a template
// @Description Validate and queue creation of one or more jails from a jail template
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "Jail Template ID" minimum(1)
// @Param request body jail.CreateFromTemplateRequest true "Jail template instantiation request"
// @Success 202 {object} internal.APIResponse[JailTemplateInstantiationTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/templates/{templateId}/jails [post]
func CreateJailFromTemplate(jailService jailTemplateService, lifecycleService jailTemplateLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("templateId"))
		if err != nil || templateID <= 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		var req jail.CreateFromTemplateRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		if err := jailService.PreflightCreateJailsFromTemplate(c.Request.Context(), uint(templateID), req); err != nil {
			c.JSON(jailTemplatePreflightStatusCode(err), internal.APIResponse[any]{
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
			taskModels.GuestTypeJailTemplate,
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
		c.Set("AuditAsyncJobType", "jail_template_create")

		c.JSON(http.StatusAccepted, internal.APIResponse[JailTemplateInstantiationTaskResponse]{
			Status:  "success",
			Message: "jail_template_create_queued",
			Data: JailTemplateInstantiationTaskResponse{
				TaskID:     task.ID,
				TemplateID: uint(templateID),
				Action:     "create",
				Outcome:    outcome,
			},
			Error: "",
		})
	}
}

// @Summary Delete a jail template
// @Description Delete a jail template and its stored dataset when no jail creation is active
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path int true "Jail Template ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/templates/{templateId} [delete]
func DeleteJailTemplate(jailService jailTemplateService, lifecycleService jailTemplateLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("templateId"))
		if err != nil || templateID <= 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		activeTasks, err := lifecycleService.ListActiveTasks(taskModels.GuestTypeJailTemplate, uint(templateID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_check_jail_template_usage",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		for _, task := range activeTasks {
			if strings.EqualFold(strings.TrimSpace(task.Action), "create") {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "jail_template_in_use",
					Data:    nil,
					Error:   "jail_template_creation_in_progress",
				})
				return
			}
		}

		if err := jailService.DeleteJailTemplate(c.Request.Context(), uint(templateID)); err != nil {
			status := http.StatusInternalServerError
			switch jailTemplateErrorCode(err) {
			case "template_not_found":
				status = http.StatusNotFound
			case "jail_template_in_use":
				status = http.StatusConflict
			}

			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_jail_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_template_deleted",
			Data:    nil,
			Error:   "",
		})
	}
}
