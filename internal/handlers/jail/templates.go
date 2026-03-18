// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/gin-gonic/gin"
)

func ListJailTemplatesSimple(jailService *jail.Service) gin.HandlerFunc {
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

func ConvertJailToTemplate(jailService *jail.Service) gin.HandlerFunc {
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

		if err := jailService.ConvertJailToTemplate(c.Request.Context(), uint(ctID)); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_convert_jail_to_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_converted_to_template",
			Data:    nil,
			Error:   "",
		})
	}
}

func CreateJailFromTemplate(jailService *jail.Service) gin.HandlerFunc {
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

		if err := jailService.CreateJailsFromTemplate(c.Request.Context(), uint(templateID), req); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_jail_from_template",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_created_from_template",
			Data:    nil,
			Error:   "",
		})
	}
}

func DeleteJailTemplate(jailService *jail.Service) gin.HandlerFunc {
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
			c.JSON(500, internal.APIResponse[any]{
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
