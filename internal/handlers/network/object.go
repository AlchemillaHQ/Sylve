// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/network"

	"github.com/gin-gonic/gin"
)

type CreateOrEditNetworkObjectRequest struct {
	Name   string   `json:"name" binding:"required"`
	Type   string   `json:"type" binding:"required"`
	Values []string `json:"values" binding:"required"`
}

// @Summary List Network Objects
// @Description List all network objects
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkModels.Object] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/object [get]
func ListNetworkObjects(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		objects, err := svc.GetObjects()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_objects",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.Object]{
			Status:  "success",
			Message: "objects_retrieved",
			Error:   "",
			Data:    objects,
		})
	}
}

// @Summary Create Network Object
// @Description Create a new network object with specified type and values
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateOrEditNetworkObjectRequest true "Create Network Object Request"
// @Success 200 {uint} uint "ID of the created network object"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /network/object [post]
func CreateNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateOrEditNetworkObjectRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		id, err := svc.CreateObject(request.Name, request.Type, request.Values)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_object",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[uint]{
			Status:  "success",
			Message: fmt.Sprintf("object_created: %d", id),
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Delete Network Object
// @Description Delete a network object by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Object ID"
// @Success 200 {string} string "Object deleted successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 404 {string} string "Object not found"
// @Failure 500 {string} string "Internal server error"
// @Router /network/object/{id} [delete]
func DeleteNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := c.Params.Get("id")
		if err == false {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "object ID is required",
				Data:    nil,
			})
			return
		}

		idInt, iErr := strconv.Atoi(id)
		if iErr != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "object ID must be an integer",
				Data:    nil,
			})
			return
		}

		if err := svc.DeleteObject(uint(idInt)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_object",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "object_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit Network Object
// @Description Edit an existing network object by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Object ID"
// @Param request body CreateOrEditNetworkObjectRequest true "Edit Network Object Request"
// @Success 200 {string} string "Network object updated successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 404 {string} string "Object not found"
// @Failure 500 {string} string "Internal server error"
// @Router /network/object/{id} [put]
func EditNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := c.Params.Get("id")
		if err == false {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "object ID is required",
				Data:    nil,
			})
			return
		}

		idInt, iErr := strconv.Atoi(id)
		if iErr != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "object ID must be an integer",
				Data:    nil,
			})
			return
		}

		var request CreateOrEditNetworkObjectRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.EditObject(uint(idInt), request.Name, request.Type, request.Values); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_edit_object",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "object_updated",
			Error:   "",
			Data:    nil,
		})
	}
}
