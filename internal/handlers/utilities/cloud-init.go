// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func cloudInitTemplateBindError(err error) (int, string) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge, "request_too_large"
	}
	return http.StatusBadRequest, "invalid_request"
}

func cloudInitTemplateMutationError(err error, fallback string) (int, string) {
	switch {
	case errors.Is(err, utilities.ErrCloudInitTemplateInvalid):
		return http.StatusBadRequest, "invalid_cloud_init_template"
	case errors.Is(err, utilities.ErrCloudInitTemplateNotFound):
		return http.StatusNotFound, "cloud_init_template_not_found"
	case errors.Is(err, utilities.ErrCloudInitTemplateConflict):
		return http.StatusConflict, "cloud_init_template_name_conflict"
	default:
		return http.StatusInternalServerError, fallback
	}
}

func writeCloudInitTemplateError(c *gin.Context, status int, message string) {
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   message,
		Data:    nil,
	})
}

// @Summary List Cloud-Init Templates
// @Description List all Cloud-Init templates available on the selected node
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]utilitiesModels.CloudInitTemplate] "Templates listed"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates [get]
func ListCloudInitTemplates(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		templates, err := utilitiesService.ListTemplates()
		if err != nil {
			logger.L.Error().Err(err).Msg("Failed to list Cloud-Init templates")
			writeCloudInitTemplateError(c, http.StatusInternalServerError, "failed_to_list_templates")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]utilitiesModels.CloudInitTemplate]{
			Status:  "success",
			Message: "templates_listed",
			Error:   "",
			Data:    templates,
		})
	}
}

// @Summary Create Cloud-Init Template
// @Description Create a Cloud-Init template with a case-insensitively unique name; networkConfig must be present and may be empty
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body utilitiesServiceInterfaces.CloudInitTemplateRequest true "Complete Cloud-Init template document"
// @Success 201 {object} internal.APIResponse[utilitiesModels.CloudInitTemplate] "Template created"
// @Header 201 {string} Location "/api/utilities/cloud-init/templates/{templateId}"
// @Failure 400 {object} internal.APIResponse[any] "Invalid or incomplete template"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access required"
// @Failure 409 {object} internal.APIResponse[any] "Template name already exists"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates [post]
func AddCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req utilitiesServiceInterfaces.CloudInitTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			status, message := cloudInitTemplateBindError(err)
			writeCloudInitTemplateError(c, status, message)
			return
		}

		template, err := utilitiesService.AddTemplate(req)
		if err != nil {
			status, message := cloudInitTemplateMutationError(err, "failed_to_add_template")
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Msg("Failed to create Cloud-Init template")
			}
			writeCloudInitTemplateError(c, status, message)
			return
		}

		c.Header("Location", "/api/utilities/cloud-init/templates/"+strconv.FormatUint(uint64(template.ID), 10))
		c.JSON(http.StatusCreated, internal.APIResponse[utilitiesModels.CloudInitTemplate]{
			Status:  "success",
			Message: "template_added",
			Error:   "",
			Data:    *template,
		})
	}
}

// @Summary Replace Cloud-Init Template
// @Description Completely replace a Cloud-Init template; networkConfig must be present and may be explicitly empty
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param templateId path uint true "Template ID" minimum(1)
// @Param request body utilitiesServiceInterfaces.CloudInitTemplateRequest true "Complete replacement document"
// @Success 200 {object} internal.APIResponse[utilitiesModels.CloudInitTemplate] "Template replaced"
// @Failure 400 {object} internal.APIResponse[any] "Invalid ID or incomplete template"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access required"
// @Failure 404 {object} internal.APIResponse[any] "Template not found"
// @Failure 409 {object} internal.APIResponse[any] "Template name already exists"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates/{templateId} [put]
func EditCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.ParamUint(c, "templateId")
		if err != nil || id == 0 {
			writeCloudInitTemplateError(c, http.StatusBadRequest, "invalid_template_id")
			return
		}

		var req utilitiesServiceInterfaces.CloudInitTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			status, message := cloudInitTemplateBindError(err)
			writeCloudInitTemplateError(c, status, message)
			return
		}

		template, err := utilitiesService.EditTemplate(id, req)
		if err != nil {
			status, message := cloudInitTemplateMutationError(err, "failed_to_edit_template")
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Uint("template_id", id).Msg("Failed to replace Cloud-Init template")
			}
			writeCloudInitTemplateError(c, status, message)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesModels.CloudInitTemplate]{
			Status:  "success",
			Message: "template_edited",
			Error:   "",
			Data:    *template,
		})
	}
}

// @Summary Delete Cloud-Init Template
// @Description Delete one Cloud-Init template by ID and return its identity
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Param templateId path uint true "Template ID" minimum(1)
// @Success 200 {object} internal.APIResponse[utilitiesServiceInterfaces.CloudInitTemplateIdentity] "Template deleted"
// @Failure 400 {object} internal.APIResponse[any] "Invalid template ID"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access required"
// @Failure 404 {object} internal.APIResponse[any] "Template not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates/{templateId} [delete]
func DeleteCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.ParamUint(c, "templateId")
		if err != nil || id == 0 {
			writeCloudInitTemplateError(c, http.StatusBadRequest, "invalid_template_id")
			return
		}

		identity, err := utilitiesService.DeleteTemplate(id)
		if err != nil {
			status, message := cloudInitTemplateMutationError(err, "failed_to_delete_template")
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Uint("template_id", id).Msg("Failed to delete Cloud-Init template")
			}
			writeCloudInitTemplateError(c, status, message)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesServiceInterfaces.CloudInitTemplateIdentity]{
			Status:  "success",
			Message: "template_deleted",
			Error:   "",
			Data:    identity,
		})
	}
}
