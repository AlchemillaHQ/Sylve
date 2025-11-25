// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/libvirt"

	"github.com/gin-gonic/gin"
)

type NetworkDetachRequest struct {
	RID       uint `json:"rid" binding:"required"`
	NetworkId uint `json:"networkId" binding:"required"`
}

type NetworkAttachRequest struct {
	RID        uint   `json:"rid" binding:"required"`
	SwitchName string `json:"switchName" binding:"required"`
	Emulation  string `json:"emulation" binding:"required"`
	MacId      *uint  `json:"macId"`
}

// @Summary Detach Network from a Virtual Machine
// @Description Detach a network interface from a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/detach [post]
func NetworkDetach(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req NetworkDetachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		if err := libvirtService.NetworkDetach(req.RID, req.NetworkId); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_detached",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Attach Network to a Virtual Machine
// @Description Attach a network interface to a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/attach [post]
func NetworkAttach(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req NetworkAttachRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		macId := uint(0)
		if req.MacId != nil {
			macId = *req.MacId
		}

		if err := libvirtService.NetworkAttach(req.RID, req.SwitchName, req.Emulation, macId); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_attached",
			Data:    nil,
			Error:   "",
		})
	}
}
