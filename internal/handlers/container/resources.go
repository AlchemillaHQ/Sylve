// SPDX-License-Identifier: BSD-2-Clause

package containerHandlers

import (
	"net/http"
	"strconv"

	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"

	_ "github.com/alchemillahq/sylve/internal"
	_ "github.com/alchemillahq/sylve/internal/db/models/container"

	"github.com/gin-gonic/gin"
)

// @Summary List used host ports
// @Description List the host ports something listens on or another container publishes
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid query int false "Leave out this container's own ports"
// @Success 200 {object} internal.APIResponse[[]containerServiceInterfaces.UsedPort] "Success"
// @Router /container/ports [get]
func ListUsedPorts(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, _ := strconv.ParseUint(c.Query("ocid"), 10, 32)
		ports, err := service.UsedPorts(c.Request.Context(), uint(ocid))
		if err != nil {
			failService(c, "failed_to_list_used_ports", err)
			return
		}
		respond(c, http.StatusOK, "used_ports_listed", ports)
	}
}

// @Summary List container networks
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]containerModels.Network] "Success"
// @Router /container/networks [get]
func ListNetworks(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		networks, err := service.ListNetworks(c.Request.Context())
		if err != nil {
			failService(c, "failed_to_list_container_networks", err)
			return
		}
		respond(c, http.StatusOK, "container_networks_listed", networks)
	}
}

// @Summary Create a container network
// @Description Create a NAT bridge network, or with a switch, a network attached to that switch
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body containerServiceInterfaces.NetworkRequest true "Network"
// @Success 200 {object} internal.APIResponse[containerModels.Network] "Success"
// @Router /container/networks [post]
func CreateNetwork(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req containerServiceInterfaces.NetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		network, err := service.CreateNetwork(c.Request.Context(), req)
		if err != nil {
			fail(c, http.StatusBadRequest, "failed_to_create_container_network", err)
			return
		}
		respond(c, http.StatusOK, "container_network_created", network)
	}
}

// @Summary Delete a container network
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param id path int true "Network ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/networks/{id} [delete]
func DeleteNetwork(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := service.DeleteNetwork(c.Request.Context(), id); err != nil {
			fail(c, http.StatusConflict, "failed_to_delete_container_network", err)
			return
		}
		respond(c, http.StatusOK, "container_network_deleted", nil)
	}
}

// @Summary List images on a pool
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param pool path string true "Pool"
// @Success 200 {object} internal.APIResponse[[]containerServiceInterfaces.Image] "Success"
// @Router /container/images/{pool} [get]
func ListImages(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		images, err := service.ListImages(c.Request.Context(), c.Param("pool"))
		if err != nil {
			failService(c, "failed_to_list_container_images", err)
			return
		}
		respond(c, http.StatusOK, "container_images_listed", images)
	}
}

type PullImageRequest struct {
	Image string `json:"image" binding:"required"`
	OS    string `json:"os"`
}

// @Summary Pull an image to a pool
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pool path string true "Pool"
// @Param request body PullImageRequest true "Image"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/images/{pool}/pull [post]
func PullImage(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req PullImageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		if err := service.PullImage(c.Request.Context(), c.Param("pool"), req.Image, req.OS); err != nil {
			failService(c, "failed_to_pull_container_image", err)
			return
		}
		respond(c, http.StatusOK, "container_image_pulled", nil)
	}
}

// @Summary Remove an image from a pool
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param pool path string true "Pool"
// @Param id path string true "Image ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/images/{pool}/{id} [delete]
func RemoveImage(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.RemoveImage(c.Request.Context(), c.Param("pool"), c.Param("id")); err != nil {
			fail(c, http.StatusConflict, "failed_to_remove_container_image", err)
			return
		}
		respond(c, http.StatusOK, "container_image_removed", nil)
	}
}

// @Summary Remove unused images from a pool
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param pool path string true "Pool"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/images/{pool}/prune [post]
func PruneImages(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.PruneImages(c.Request.Context(), c.Param("pool")); err != nil {
			failService(c, "failed_to_prune_container_images", err)
			return
		}
		respond(c, http.StatusOK, "container_images_pruned", nil)
	}
}

// @Summary List registry credentials
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]containerModels.RegistryCredential] "Success"
// @Router /container/registry-credentials [get]
func ListRegistryCredentials(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		credentials, err := service.ListRegistryCredentials(c.Request.Context())
		if err != nil {
			failService(c, "failed_to_list_registry_credentials", err)
			return
		}
		respond(c, http.StatusOK, "registry_credentials_listed", credentials)
	}
}

// @Summary Save a registry credential
// @Description Add or replace the login used to pull from a registry
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body containerServiceInterfaces.RegistryCredentialRequest true "Credential"
// @Success 200 {object} internal.APIResponse[containerModels.RegistryCredential] "Success"
// @Router /container/registry-credentials [put]
func SaveRegistryCredential(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req containerServiceInterfaces.RegistryCredentialRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		credential, err := service.SaveRegistryCredential(c.Request.Context(), req)
		if err != nil {
			fail(c, http.StatusBadRequest, "failed_to_save_registry_credential", err)
			return
		}
		respond(c, http.StatusOK, "registry_credential_saved", credential)
	}
}

// @Summary Delete a registry credential
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param id path int true "Credential ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/registry-credentials/{id} [delete]
func DeleteRegistryCredential(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := service.DeleteRegistryCredential(c.Request.Context(), id); err != nil {
			failService(c, "failed_to_delete_registry_credential", err)
			return
		}
		respond(c, http.StatusOK, "registry_credential_deleted", nil)
	}
}
