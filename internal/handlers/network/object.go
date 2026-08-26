// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"

	"github.com/gin-gonic/gin"
)

type CreateOrEditNetworkObjectRequest struct {
	Name   string   `json:"name" binding:"required,max=128"`
	Type   string   `json:"type" binding:"required"`
	Values []string `json:"values" binding:"required,min=1,max=1024,dive,required,max=2048"`
}

type BulkDeleteNetworkObjectsRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1,max=1024,unique,dive,gt=0"`
}

func bindNetworkObjectJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "network_object_request_too_large",
				Error:   "network_object_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_network_object_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func networkObjectPathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_id",
			Error:   "invalid_network_object_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func networkObjectErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrNetworkObjectUpstream):
		return http.StatusBadGateway
	case errors.Is(err, network.ErrInvalidNetworkObject):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrNetworkObjectNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrNetworkObjectConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeNetworkObjectError(c *gin.Context, message string, err error) {
	status := networkObjectErrorStatus(err)
	if status == http.StatusInternalServerError || status == http.StatusBadGateway {
		logger.L.Error().Err(err).Str("operation", message).Msg("network_object_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.NetworkObjectErrorCode(err),
		Data:    nil,
	})
}

// @Summary List Network Objects
// @Description List all configured network objects and their current usage state
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[[]networkModels.Object] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/object [get]
func ListNetworkObjects(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		objects, err := svc.GetObjects()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_get_network_objects")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_objects",
				Error:   "network_object_list_failed",
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
// @Description Create and validate a network object with the specified type and values
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body CreateOrEditNetworkObjectRequest true "Create Network Object Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Router /network/object [post]
func CreateNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateOrEditNetworkObjectRequest
		if !bindNetworkObjectJSON(c, &request) {
			return
		}

		id, err := svc.CreateObject(request.Name, request.Type, request.Values)
		if err != nil {
			writeNetworkObjectError(c, "failed_to_create_object", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "object_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Delete Network Object
// @Description Delete one network object by its positive integer ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param id path int true "Object ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/object/{id} [delete]
func DeleteNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := networkObjectPathID(c)
		if !ok {
			return
		}

		if err := svc.DeleteObject(id); err != nil {
			writeNetworkObjectError(c, "failed_to_delete_object", err)
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

// @Summary Update Network Object
// @Description Replace an existing network object's name, type, and values
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param id path int true "Object ID" minimum(1)
// @Param request body CreateOrEditNetworkObjectRequest true "Update Network Object Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Router /network/object/{id} [put]
func EditNetworkObject(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := networkObjectPathID(c)
		if !ok {
			return
		}

		var request CreateOrEditNetworkObjectRequest
		if !bindNetworkObjectJSON(c, &request) {
			return
		}

		if err := svc.EditObject(id, request.Name, request.Type, request.Values); err != nil {
			writeNetworkObjectError(c, "failed_to_edit_object", err)
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

// @Summary Bulk Delete Network Objects
// @Description Delete a validated collection of unused network objects as one operation
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body BulkDeleteNetworkObjectsRequest true "Bulk Delete Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/object [delete]
func BulkDeleteNetworkObjects(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request BulkDeleteNetworkObjectsRequest
		if !bindNetworkObjectJSON(c, &request) {
			return
		}

		if err := svc.BulkDeleteObjects(request.IDs); err != nil {
			writeNetworkObjectError(c, "failed_to_bulk_delete_objects", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "objects_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
