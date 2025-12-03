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
	"github.com/alchemillahq/sylve/internal/services/jail"

	"github.com/gin-gonic/gin"
)

type JailInheritNetworkRequest struct {
	CTID uint  `json:"ctId" binding:"required"`
	IPv4 *bool `json:"ipv4"`
	IPv6 *bool `json:"ipv6"`
}

type SetInheritanceRequest struct {
	IPv4 *bool `json:"ipv4"`
	IPv6 *bool `json:"ipv6"`
}

type AddNetworkRequest struct {
	CTID       uint   `json:"ctId" binding:"required"`
	SwitchName string `json:"switchName" binding:"required"`
	MacID      *uint  `json:"macId"`
	IP4        *uint  `json:"ip4"`
	IP4GW      *uint  `json:"ip4gw"`
	IP6        *uint  `json:"ip6"`
	IP6GW      *uint  `json:"ip6gw"`
	DHCP       *bool  `json:"dhcp"`
	SLAAC      *bool  `json:"slaac"`
}

// @Summary Update Jail to Inherit Hosts Network
// @Description Update the network settings of a jail to inherit from the host
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JailInheritNetworkRequest true "Inherit Network Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/network/inheritance [post]
func InheritJailNetwork(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JailInheritNetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		ipv4 := false
		ipv6 := false

		if req.IPv4 != nil {
			ipv4 = *req.IPv4
		}

		if req.IPv6 != nil {
			ipv6 = *req.IPv6
		}

		if ipv4 == false && ipv6 == false {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "atleast_one_of_ipv4_or_ipv6_must_be_specified",
			})
			return
		}

		err := jailService.InheritNetwork(req.CTID, ipv4, ipv6)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_inherit_network",
				Data:    nil,
				Error:   "failed_to_inherit_network: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_network_inherited",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Update Jail to Disinherit Hosts Network
// @Description Update the network settings of a jail to disinherit from the host
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctId path uint true "Container ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/network/disinherit/{ctId} [delete]
func DisinheritJailNetwork(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId := c.Param("ctId")
		if ctId == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ct_id",
				Data:    nil,
				Error:   "CT ID is required",
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

		err = jailService.DisinheritNetwork(uint(ctIdUint))
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_disinherit_network",
				Data:    nil,
				Error:   "failed_to_disinherit_network: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_network_disinherited",
			Data:    nil,
			Error:   "",
		})
	}
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
		var req AddNetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		macId := uint(0)
		ipv4 := uint(0)
		ipv4gw := uint(0)
		ipv6 := uint(0)
		ipv6gw := uint(0)
		dhcp := false
		slaac := false

		if req.IP4 != nil {
			ipv4 = *req.IP4
		}

		if req.IP4GW != nil {
			ipv4gw = *req.IP4GW
		}

		if req.IP6 != nil {
			ipv6 = *req.IP6
		}

		if req.IP6GW != nil {
			ipv6gw = *req.IP6GW
		}

		if req.DHCP != nil {
			dhcp = *req.DHCP
		}

		if req.SLAAC != nil {
			slaac = *req.SLAAC
		}

		if req.MacID != nil {
			macId = *req.MacID
		}

		err := jailService.AddNetwork(req.CTID, req.SwitchName, macId, ipv4, ipv4gw, ipv6, ipv6gw, dhcp, slaac)
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
