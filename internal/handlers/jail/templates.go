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
	CanMutateProtectedJail(ctID uint) (bool, error)
	PreflightConvertJailToTemplate(ctx context.Context, ctID uint, req jail.ConvertToTemplateRequest) error
	PreflightCreateJailsFromTemplate(ctx context.Context, templateID uint, req jail.CreateFromTemplateRequest) error
	DeleteJailTemplate(ctx context.Context, templateID uint) error
}

func preflightStatusCode(err error) int {
	if err == nil {
		return http.StatusBadRequest
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "failed_to_") {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}

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

func GetJailTemplateByID(jailService jailTemplateService) gin.HandlerFunc {
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

		template, err := jailService.GetJailTemplate(uint(templateID))
		if err != nil {
			if strings.Contains(err.Error(), "template_not_found") {
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

		c.JSON(http.StatusOK, internal.APIResponse[*jailModels.JailTemplate]{
			Status:  "success",
			Message: "jail_template_retrieved",
			Data:    template,
			Error:   "",
		})
	}
}

func ConvertJailToTemplate(jailService jailTemplateService, lifecycleService *lifecycle.Service) gin.HandlerFunc {
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
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		allowed, leaseErr := jailService.CanMutateProtectedJail(uint(ctID))
		if leaseErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_check_failed",
				Data:    nil,
				Error:   leaseErr.Error(),
			})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "standby_mode_edit_not_allowed",
				Data:    nil,
				Error:   "replication_lease_not_owned",
			})
			return
		}

		if err := jailService.PreflightConvertJailToTemplate(c.Request.Context(), uint(ctID), req); err != nil {
			c.JSON(preflightStatusCode(err), internal.APIResponse[any]{
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
			Message: "jail_template_convert_queued",
			Data:    gin.H{"task": task, "outcome": outcome},
			Error:   "",
		})
	}
}

func CreateJailFromTemplate(jailService jailTemplateService, lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("id"))
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
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		if err := jailService.PreflightCreateJailsFromTemplate(c.Request.Context(), uint(templateID), req); err != nil {
			c.JSON(preflightStatusCode(err), internal.APIResponse[any]{
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
			Message: "jail_template_create_queued",
			Data:    gin.H{"task": task, "outcome": outcome},
			Error:   "",
		})
	}
}

func DeleteJailTemplate(jailService jailTemplateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID, err := strconv.Atoi(c.Param("id"))
		if err != nil || templateID <= 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Data:    nil,
				Error:   "template id must be a positive integer",
			})
			return
		}

		if err := jailService.DeleteJailTemplate(c.Request.Context(), uint(templateID)); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(strings.ToLower(err.Error()), "template_not_found") {
				status = http.StatusNotFound
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
