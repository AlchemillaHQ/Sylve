// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/network"

	"github.com/gin-gonic/gin"
)

type CreateStandardSwitchRequest struct {
	Name         string   `json:"name" binding:"required"`
	MTU          *int     `json:"mtu"`
	VLAN         *int     `json:"vlan"`
	Network4     *uint    `json:"network4"`
	Gateway4     *uint    `json:"gateway4"`
	Network6     *uint    `json:"network6"`
	Gateway6     *uint    `json:"gateway6"`
	DisableIPv6  *bool    `json:"disableIPv6"`
	SLAAC        *bool    `json:"slaac"`
	Private      *bool    `json:"private" binding:"required"`
	DefaultRoute *bool    `json:"defaultRoute"`
	DHCP         *bool    `json:"dhcp"`
	Ports        []string `json:"ports" binding:"required"`
}

type UpdateStandardSwitchRequest struct {
	ID           uint     `json:"id" binding:"required"`
	MTU          *int     `json:"mtu"`
	VLAN         *int     `json:"vlan"`
	Network4     *uint    `json:"network4"`
	Gateway4     *uint    `json:"gateway4"`
	Network6     *uint    `json:"network6"`
	Gateway6     *uint    `json:"gateway6"`
	DisableIPv6  *bool    `json:"disableIPv6"`
	SLAAC        *bool    `json:"slaac"`
	Private      *bool    `json:"private" binding:"required"`
	Ports        []string `json:"ports" binding:"required"`
	DHCP         *bool    `json:"dhcp"`
	DefaultRoute *bool    `json:"defaultRoute"`
}

// @Summary Create a new Standard Switch
// @Description Create a new standard switch
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateStandardSwitchRequest true "Create Standard Switch Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch [post]
func CreateStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateStandardSwitchRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		mtu := 0
		vlan := 0

		if request.VLAN != nil {
			if *request.VLAN < 0 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_vlan",
					Error:   "vlan_must_be_positive_or_zero",
					Data:    nil,
				})
				return
			}
			vlan = *request.VLAN
		}

		if request.MTU != nil {
			if *request.MTU < 0 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_mtu",
					Error:   "mtu_must_be_positive_or_zero",
					Data:    nil,
				})
				return
			}
			mtu = *request.MTU
		}

		if request.Private == nil {
			request.Private = new(bool)
			*request.Private = false
		}

		if request.DHCP == nil {
			request.DHCP = new(bool)
			*request.DHCP = false
		}

		if request.DisableIPv6 == nil {
			request.DisableIPv6 = new(bool)
			*request.DisableIPv6 = false
		}

		if request.SLAAC == nil {
			request.SLAAC = new(bool)
			*request.SLAAC = false
		}

		if *request.DHCP == true {
			request.Network4 = new(uint)
			*request.Network4 = 0

			request.Gateway4 = new(uint)
			*request.Gateway4 = 0
		}

		if *request.DisableIPv6 == true {
			request.Network6 = new(uint)
			*request.Network6 = 0

			request.Gateway6 = new(uint)
			*request.Gateway6 = 0

			request.SLAAC = new(bool)
			*request.SLAAC = false
		}

		if *request.SLAAC == true {
			request.Network6 = new(uint)
			*request.Network6 = 0

			request.Gateway6 = new(uint)
			*request.Gateway6 = 0

			if request.DisableIPv6 == nil {
				request.DisableIPv6 = new(bool)
			}

			*request.DisableIPv6 = false
		}

		var network4, gateway4, network6, gateway6 uint

		if request.Network4 != nil {
			network4 = *request.Network4
		} else {
			network4 = 0
		}

		if request.Gateway4 != nil {
			gateway4 = *request.Gateway4
		} else {
			gateway4 = 0
		}

		if request.Network6 != nil {
			network6 = *request.Network6
		} else {
			network6 = 0
		}

		if request.Gateway6 != nil {
			gateway6 = *request.Gateway6
		} else {
			gateway6 = 0
		}

		defaultRoute := false

		if request.DefaultRoute != nil {
			defaultRoute = *request.DefaultRoute
		}

		err := networkService.NewStandardSwitch(request.Name,
			mtu,
			vlan,
			network4,
			network6,
			gateway4,
			gateway6,
			request.Ports,
			*request.Private,
			*request.DHCP,
			*request.DisableIPv6,
			*request.SLAAC,
			defaultRoute,
		)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_switch",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "switch_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a Standard Switch
// @Description Delete a standard switch by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Switch ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/{id} [delete]
func DeleteStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_switch_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = networkService.DeleteStandardSwitch(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_switch",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "switch_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update a Standard Switch
// @Description Update a standard switch by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Switch ID"
// @Param request body UpdateStandardSwitchRequest true "Update Standard Switch Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch [put]
func UpdateStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request UpdateStandardSwitchRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		mtu := 0
		vlan := 0

		if request.MTU != nil {
			if *request.MTU < 0 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_mtu",
					Error:   "mtu_must_be_positive_or_zero",
					Data:    nil,
				})
				return
			}
			mtu = *request.MTU
		}

		if request.VLAN != nil {
			if *request.VLAN < 0 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_vlan",
					Error:   "vlan_must_be_positive_or_zero",
					Data:    nil,
				})
				return
			}
			vlan = *request.VLAN
		}

		if request.DHCP == nil {
			request.DHCP = new(bool)
			*request.DHCP = false
		}

		if request.DisableIPv6 == nil {
			request.DisableIPv6 = new(bool)
			*request.DisableIPv6 = false
		}

		if request.SLAAC == nil {
			request.SLAAC = new(bool)
			*request.SLAAC = false
		}

		if *request.SLAAC == true {
			request.Network6 = new(uint)
			*request.Network6 = 0

			request.Gateway6 = new(uint)
			*request.Gateway6 = 0

			if request.DisableIPv6 == nil {
				request.DisableIPv6 = new(bool)
			}

			*request.DisableIPv6 = false
		}

		if *request.DisableIPv6 == true {
			request.Network6 = new(uint)
			*request.Network6 = 0

			request.Gateway6 = new(uint)
			*request.Gateway6 = 0

			if request.SLAAC == nil {
				request.SLAAC = new(bool)
			}

			*request.SLAAC = false
		}

		var network4, gateway4, network6, gateway6 uint

		if request.Network4 != nil {
			network4 = *request.Network4
		} else {
			network4 = 0
		}

		if request.Gateway4 != nil {
			gateway4 = *request.Gateway4
		} else {
			gateway4 = 0
		}

		if request.Network6 != nil {
			network6 = *request.Network6
		} else {
			network6 = 0
		}

		if request.Gateway6 != nil {
			gateway6 = *request.Gateway6
		} else {
			gateway6 = 0
		}

		defaultRoute := false

		if request.DefaultRoute != nil {
			defaultRoute = *request.DefaultRoute
		}

		err := networkService.EditStandardSwitch(
			request.ID,
			mtu,
			vlan,
			network4,
			network6,
			gateway4,
			gateway6,
			request.Ports,
			*request.Private,
			*request.DHCP,
			*request.DisableIPv6,
			*request.SLAAC,
			defaultRoute)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_switch",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "switch_updated",
			Error:   "",
			Data:    nil,
		})
	}
}
