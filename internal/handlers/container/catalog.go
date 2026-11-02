// SPDX-License-Identifier: BSD-2-Clause

package containerHandlers

import (
	"net/http"
	"strings"

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/gin-gonic/gin"
)

// @Summary List catalog apps
// @Description List the apps in the daemonless catalogs that run on this host
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]containerServiceInterfaces.CatalogApp] "Success"
// @Router /container/catalog [get]
func ListCatalog(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		apps, err := service.Catalog(c.Request.Context())
		if err != nil {
			fail(c, http.StatusBadGateway, "failed_to_load_container_catalog", err)
			return
		}
		respond(c, http.StatusOK, "container_catalog_listed", apps)
	}
}

// @Summary Get a catalog app's settings
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param source path string true "Catalog"
// @Param id path string true "App ID"
// @Success 200 {object} internal.APIResponse[containerServiceInterfaces.CatalogTemplate] "Success"
// @Router /container/catalog/{source}/{id} [get]
func GetCatalogApp(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		template, err := service.CatalogTemplate(c.Request.Context(), c.Param("source"), c.Param("id"))
		if err != nil {
			fail(c, http.StatusBadGateway, "failed_to_load_catalog_app", err)
			return
		}
		respond(c, http.StatusOK, "catalog_app_retrieved", template)
	}
}

// @Summary Get a catalog app's icon, or the catalog's logo
// @Tags Container
// @Produce image/svg+xml,image/png
// @Security BearerAuth
// @Param source path string true "Catalog"
// @Param id path string false "App ID; without it, the catalog's logo"
// @Router /container/catalog/{source}/{id}/icon [get]
// @Router /container/catalog/{source}/icon [get]
func GetCatalogIcon(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		path, err := service.CatalogIcon(c.Request.Context(), c.Param("source"), c.Param("id"))
		if err != nil {
			fail(c, http.StatusNotFound, "catalog_icon_not_found", err)
			return
		}
		c.Header("Cache-Control", "private, max-age=86400")
		c.File(path)
	}
}

// @Summary Render a catalog app
// @Description Turn a catalog app and its settings into the containers to create: one, or a stack
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param source path string true "Catalog"
// @Param id path string true "App ID"
// @Param request body containerServiceInterfaces.CatalogRenderRequest true "Settings"
// @Success 200 {object} internal.APIResponse[containerServiceInterfaces.CatalogPlan] "Success"
// @Router /container/catalog/{source}/{id}/render [post]
func RenderCatalogApp(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req containerServiceInterfaces.CatalogRenderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		create, err := service.RenderCatalogApp(c.Request.Context(), c.Param("source"), c.Param("id"), req)
		if err != nil {
			fail(c, http.StatusBadRequest, "failed_to_render_catalog_app", err)
			return
		}
		respond(c, http.StatusOK, "catalog_app_rendered", create)
	}
}

type StackResponse struct {
	TaskResponse
	Containers []StackMember `json:"containers"`
}

type StackMember struct {
	OCID uint   `json:"ocId"`
	Name string `json:"name"`
}

// @Summary Create a catalog app's stack
// @Description Queue the creation of the containers of a catalog app that runs as several, on a network of their own, with the free container IDs from ocId on. The task belongs to the first container; when a container cannot be created or started, none are kept.
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body containerServiceInterfaces.StackRequest true "Stack"
// @Success 202 {object} internal.APIResponse[StackResponse] "Accepted"
// @Router /container/stack [post]
func CreateStack(lifecycleService lifecycleRequester) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req containerServiceInterfaces.StackRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		prepared, task, outcome, err := lifecycleService.RequestContainerStack(c.Request.Context(), req,
			taskModels.LifecycleTaskSourceUser, strings.TrimSpace(c.GetString("Username")))
		if err != nil {
			fail(c, http.StatusBadRequest, "container_stack_rejected", err)
			return
		}
		response := StackResponse{TaskResponse: TaskResponse{TaskID: task.ID, OCID: task.GuestID, Action: "create-stack", Outcome: outcome}}
		for _, create := range prepared.Containers {
			response.Containers = append(response.Containers, StackMember{OCID: create.OCID, Name: create.Name})
		}
		respond(c, http.StatusAccepted, "container_stack_create_queued", response)
	}
}
