// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

// @Summary Get DHCP Leases
// @Description Retrieve both active (file-based) and static (DB-based) DHCP leases
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.Leases] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease [get]
func GetDHCPLeases(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		leases, err := svc.GetLeases()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_leases",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[networkServiceInterfaces.Leases]{
			Status:  "success",
			Message: "dhcp_leases_retrieved",
			Error:   "",
			Data:    leases,
		})
	}
}

// @Summary Create DHCP Lease
// @Description Create a new static DHCP lease
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body networkServiceInterfaces.CreateStaticMapRequest true "Request Body"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease [post]
func CreateDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.CreateStaticMapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.CreateStaticMap(&req); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_dhcp_lease",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_lease_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update DHCP Lease
// @Description Update an existing static DHCP lease by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body networkServiceInterfaces.ModifyStaticMapRequest true "Request Body"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease [put]
func UpdateDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.ModifyStaticMapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.ModifyStaticMap(&req); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_dhcp_lease",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_lease_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete DHCP Lease
// @Description Delete a static DHCP lease by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Lease ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease/{id} [delete]
func DeleteDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idParam := c.Param("id")
		if idParam == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "lease_id_required",
				Error:   "lease_id_required",
				Data:    nil,
			})
			return
		}

		id, err := strconv.Atoi(idParam)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_lease_id",
				Error:   "invalid_lease_id",
				Data:    nil,
			})
			return
		}

		if err := svc.DeleteStaticMap(uint(id)); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_dhcp_lease",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_lease_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
