// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"fmt"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"

	"github.com/gin-gonic/gin"
)

type JailEditDescRequest struct {
	ID          uint   `json:"id" binding:"required"`
	Description string `json:"description"`
}

// @Summary List all Jails
// @Description Retrieve a list of all jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.Jail] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail [get]
func ListJails(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJails()

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_jails: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailModels.Jail]{
			Status:  "success",
			Message: "jail_listed",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary List all Jails (Simple)
// @Description Retrieve a simple list of all jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.SimpleList] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/simple [get]
func ListJailsSimple(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJailsSimple()

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_jails_simple: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "jail_listed_simple",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary Create a new Jail
// @Description Create a new jail with the provided configuration
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body jailServiceInterfaces.CreateJailRequest true "Create Jail Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail [post]
func CreateJail(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.CreateJailRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		err := jailService.CreateJail(req)

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_create: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_created",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Delete a Jail
// @Description Delete a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "CTID of the Jail"
// @Param deletemacs query bool true "Delete or Keep"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid} [delete]
func DeleteJail(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctid, berr := c.Params.Get("ctid")
		if !berr || ctid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Data:    nil,
				Error:   "invalid_ctid: ",
			})
			return
		}

		var ctidInt int
		ctidInt, err := strconv.Atoi(ctid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid_format",
				Data:    nil,
				Error:   fmt.Sprintf("invalid_ctid_format: %s", err.Error()),
			})
			return
		}

		deleteMacsStr := c.Query("deletemacs")
		if deleteMacsStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deletemacs_param",
				Error:   "missing 'deletemacs' query parameter",
				Data:    nil,
			})
			return
		}

		deleteMacs, err := strconv.ParseBool(deleteMacsStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deletemacs_param",
				Error:   "invalid 'deletemacs' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		deleteRootFsStr := c.Query("deleterootfs")
		if deleteRootFsStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deleterootfs_param",
				Error:   "missing 'deleterootfs' query parameter",
				Data:    nil,
			})
			return
		}

		deleteRootFs, err := strconv.ParseBool(deleteRootFsStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deleterootfs_param",
				Error:   "invalid 'deleterootfs' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		err = jailService.DeleteJail(uint(ctidInt), deleteMacs, deleteRootFs)

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_jail",
				Data:    nil,
				Error:   "failed_to_delete_jail: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_deleted",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Edit a Jail's description
// @Description Update the description of a jail by its ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JailEditDescRequest true "Edit Jail Description Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/description [put]
func UpdateJailDescription(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JailEditDescRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		err := jailService.UpdateDescription(req.ID, req.Description)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_description",
				Data:    nil,
				Error:   "failed_to_update_description: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_description_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Update Resource Limits
// @Description Enable or disable a Jail's resource limits
// @Tags jail
// @Accept json
// @Produce json
// @Param ctId path int true "Container ID"
// @Param enabled query bool true "Enable or Disable"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /jail/resource-limits/{ctId} [put]
func UpdateResourceLimits(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId, err := strconv.ParseUint(c.Param("ctId"), 10, 32)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Error:   "invalid_ctid: " + err.Error(),
				Data:    nil,
			})
			return
		}

		enabledStr := c.Query("enabled")
		if enabledStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_enabled_param",
				Error:   "missing 'enabled' query parameter",
				Data:    nil,
			})
			return
		}

		enabled, err := strconv.ParseBool(enabledStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_enabled_param",
				Error:   "invalid 'enabled' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		if err := jailService.UpdateResourceLimits(uint(ctId), enabled); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_resource_limits",
				Data:    nil,
				Error:   "failed_to_update_resource_limits: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_resource_limits_updated",
			Data:    nil,
			Error:   "",
		})
	}
}
