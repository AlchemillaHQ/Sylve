package utilitiesHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

// @Summary List Cloud-Init Templates
// @Description List all Cloud-Init templates
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]utilitiesModels.CloudInitTemplate] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates [get]
func ListCloudInitTemplates(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		templates, err := utilitiesService.ListTemplates()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_templates",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Add Cloud-Init Template
// @Description Add a new Cloud-Init template
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body utilitiesServiceInterfaces.AddTemplateRequest true "Add Template Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates [post]
func AddCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req utilitiesServiceInterfaces.AddTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := utilitiesService.AddTemplate(req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_add_template",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "template_added",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit Cloud-Init Template
// @Description Edit an existing Cloud-Init template
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body utilitiesServiceInterfaces.EditTemplateRequest true "Edit Template Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates/:id [put]
func EditCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req utilitiesServiceInterfaces.EditTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		id, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		req.ID = id

		err = utilitiesService.EditTemplate(req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_edit_template",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "template_edited",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Cloud-Init Template
// @Description Delete a Cloud-Init template by ID
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Template ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/cloud-init/templates/:id [delete]
func DeleteCloudInitTemplate(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_template_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = utilitiesService.DeleteTemplate(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_template",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "template_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
