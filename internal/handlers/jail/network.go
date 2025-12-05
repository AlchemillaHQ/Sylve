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

type SetInheritanceRequest struct {
	IPv4 *bool `json:"ipv4"`
	IPv6 *bool `json:"ipv6"`
}

// @Summary Set Network Inheritance
// @Description Set network inheritance for a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SetInheritanceRequest true "Set Inheritance Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/network/inheritance/:ctId [put]
func SetNetworkInheritance(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SetInheritanceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		ctidParam := c.Param("ctId")
		ctid, err := strconv.ParseUint(ctidParam, 10, 32)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ct_id",
				Data:    nil,
				Error:   "Invalid CT ID: " + err.Error(),
			})
			return
		}

		var ipv4, ipv6 bool

		if req.IPv4 != nil {
			ipv4 = *req.IPv4
		}

		if req.IPv6 != nil {
			ipv6 = *req.IPv6
		}

		err = jailService.SetInheritance(uint(ctid), ipv4, ipv6)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_set_network_inheritance",
				Data:    nil,
				Error:   "failed_to_set_network_inheritance: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_inheritance_set",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Add Network Switch to Jail
// @Description Add a network switch to a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddNetworkRequest true "Add Network Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/network [post]
func AddNetwork(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.AddJailNetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		err := jailService.AddNetwork(req)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_add_network",
				Data:    nil,
				Error:   "failed_to_add_network: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_added_to_jail",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Delete Network Switch from Jail
// @Description Delete a network switch from a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctId path uint true "Container ID"
// @Param networkId path uint true "Network ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/network/{ctId}/{networkId} [delete]
func DeleteNetwork(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId := c.Param("ctId")
		networkId := c.Param("networkId")

		if ctId == "" || networkId == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "CT ID and Network ID are required",
			})
			return
		}

		ctIdUint, err := strconv.ParseUint(ctId, 10, 32)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ct_id",
				Data:    nil,
				Error:   "Invalid CT ID: " + err.Error(),
			})
			return
		}

		networkIdUint, err := strconv.ParseUint(networkId, 10, 32)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_network_id",
				Data:    nil,
				Error:   "Invalid Network ID: " + err.Error(),
			})
			return
		}

		err = jailService.DeleteNetwork(uint(ctIdUint), uint(networkIdUint))
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_network",
				Data:    nil,
				Error:   "failed_to_delete_network: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_deleted_from_jail",
			Data:    nil,
			Error:   "",
		})
	}
}
