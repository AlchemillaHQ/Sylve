// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/libvirt"

	"github.com/gin-gonic/gin"
)

type ModifyHardwareRequest struct {
	CPUSockets    int    `json:"cpuSockets" binding:"required"`
	CPUCores      int    `json:"cpuCores" binding:"required"`
	CPUThreads    int    `json:"cpuThreads" binding:"required"`
	RAM           int    `json:"ram" binding:"required"`
	CPUPinning    []int  `json:"cpuPinning" binding:"required"`
	VNCPort       int    `json:"vncPort" binding:"required"`
	VNCResolution string `json:"vncResolution" binding:"required"`
	VNCPassword   string `json:"vncPassword" binding:"required"`
	VNCWait       *bool  `json:"vncWait" binding:"required"`
	PCIDevices    []int  `json:"pciDevices" binding:"required"`
}

type ModifyRAMRequest struct {
	RAM int `json:"ram" binding:"required"`
}

type ModifyVNCRequest struct {
	VNCEnabled    *bool  `json:"vncEnabled" binding:"required"`
	VNCPort       int    `json:"vncPort" binding:"required"`
	VNCResolution string `json:"vncResolution" binding:"required"`
	VNCPassword   string `json:"vncPassword" binding:"required"`
	VNCWait       *bool  `json:"vncWait" binding:"required"`
}

type ModifyPassthroughRequest struct {
	PCIDevices []int `json:"pciDevices" binding:"required"`
}

// @Summary Modify CPU of a Virtual Machine
// @Description Modify the CPU configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body libvirtServiceInterfaces.ModifyCPURequest true "Modify CPU Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /hardware/cpu/:vmid [put]
func ModifyCPU(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.ModifyCPURequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		// vmID, exists := c.Params.Get("vmid")
		// if !exists {
		// 	c.JSON(400, internal.APIResponse[any]{
		// 		Status:  "error",
		// 		Message: "invalid_request",
		// 		Data:    nil,
		// 		Error:   "vmid_not_provided",
		// 	})
		// 	return
		// }

		// vmIdInt, err := strconv.Atoi(vmID)
		// if err != nil {
		// 	c.JSON(400, internal.APIResponse[any]{
		// 		Status:  "error",
		// 		Message: "invalid_request",
		// 		Data:    nil,
		// 		Error:   "invalid_vmid_format",
		// 	})
		// 	return
		// }

		// if err := libvirtService.ModifyCPU(vmIdInt,
		// 	req.CPUSockets,
		// 	req.CPUCores,
		// 	req.CPUThreads,
		// 	req.CPUPinning); err != nil {
		// 	c.JSON(500, internal.APIResponse[any]{
		// 		Status:  "error",
		// 		Message: "internal_server_error",
		// 		Data:    nil,
		// 		Error:   err.Error(),
		// 	})
		// 	return
		// }

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "cpu_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify RAM of a Virtual Machine
// @Description Modify the RAM configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyRAMRequest true "Modify RAM Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /hardware/ram/:vmid [put]
func ModifyRAM(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ModifyRAMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		vmID, exists := c.Params.Get("vmid")
		if !exists {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "vmid_not_provided",
			})
			return
		}

		vmIdInt, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_vmid_format",
			})
			return
		}

		if err := libvirtService.ModifyRAM(vmIdInt, req.RAM); err != nil {
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
			Message: "ram_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify VNC of a Virtual Machine
// @Description Modify the VNC configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyVNCRequest true "Modify VNC Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /hardware/vnc/:vmid [put]
func ModifyVNC(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ModifyVNCRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		vmID, exists := c.Params.Get("vmid")
		if !exists {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "vmid_not_provided",
			})
			return
		}

		vmIdInt, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_vmid_format",
			})
			return
		}

		vncWait := false

		if req.VNCWait != nil {
			vncWait = *req.VNCWait
		}

		vncEnabled := false

		if req.VNCEnabled != nil {
			vncEnabled = *req.VNCEnabled
		}

		if err := libvirtService.ModifyVNC(vmIdInt,
			vncEnabled,
			req.VNCPort,
			req.VNCResolution,
			req.VNCPassword,
			vncWait); err != nil {
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
			Message: "vnc_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify PCI Devices of a Virtual Machine
// @Description Modify the PCI Passthrough devices of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyPassthroughRequest true "Modify PCI Devices Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /hardware/ppt/:vmid [put]
func ModifyPassthroughDevices(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ModifyPassthroughRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		vmID, exists := c.Params.Get("vmid")
		if !exists {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "vmid_not_provided",
			})
			return
		}

		vmIdInt, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_vmid_format",
			})
			return
		}

		if err := libvirtService.ModifyPassthrough(vmIdInt, req.PCIDevices); err != nil {
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
			Message: "pci_devices_modified",
			Data:    nil,
			Error:   "",
		})
	}
}
