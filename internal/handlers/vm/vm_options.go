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
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type ModifyWakeOnLanRequest struct {
	Enabled *bool `json:"enabled"`
}

type ModifyBootOrderRequest struct {
	StartAtBoot *bool `json:"startAtBoot"`
	BootOrder   *int  `json:"bootOrder"`
}

type ModifyClockRequest struct {
	TimeOffset string `json:"timeOffset"`
}

type ModifySerialConsoleRequest struct {
	Enabled *bool `json:"enabled"`
}

type ModifyShutdownWaitTimeRequest struct {
	WaitTime *int `json:"waitTime"`
}

type ModifyCloudInitDataRequest struct {
	Data     string `json:"data"`
	Metadata string `json:"metadata"`
}

type ModifyIgnoreUMSRsRequest struct {
	IgnoreUMSRs *bool `json:"ignoreUMSRs"`
}

type ModifyTPMRequest struct {
	Enabled *bool `json:"enabled"`
}

// @Summary Modify Wake-on-LAN of a Virtual Machine
// @Description Modify the Wake-on-LAN configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyWakeOnLanRequest true "Modify Wake-on-LAN Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/wol/:rid [put]
func ModifyWakeOnLan(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyWakeOnLanRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		enabled := false
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		if err := libvirtService.ModifyWakeOnLan(uint(ridInt), enabled); err != nil {
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
			Message: "wol_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Boot Order of a Virtual Machine
// @Description Modify the Boot Order configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyBootOrderRequest true "Modify Boot Order Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/boot-order/:rid [put]
func ModifyBootOrder(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyBootOrderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		startAtBoot := false
		if req.StartAtBoot != nil {
			startAtBoot = *req.StartAtBoot
		}

		bootOrder := 0
		if req.BootOrder != nil {
			bootOrder = *req.BootOrder
		}

		if err := libvirtService.ModifyBootOrder(uint(ridInt), startAtBoot, bootOrder); err != nil {
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
			Message: "boot_order_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Clock of a Virtual Machine
// @Description Modify the Clock configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyClockRequest true "Modify Clock Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/clock/:rid [put]
func ModifyClock(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyClockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		if req.TimeOffset == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "time_offset_not_provided",
			})
			return
		}

		if err := libvirtService.ModifyClock(uint(ridInt), req.TimeOffset); err != nil {
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
			Message: "clock_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Serial Console Access of a Virtual Machine
// @Description Modify the Serial Console Access configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifySerialConsoleRequest true "Modify Serial Console Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/serial-console/:rid [put]
func ModifySerialConsole(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifySerialConsoleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		enabled := false
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		if err := libvirtService.ModifySerial(uint(ridInt), enabled); err != nil {
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
			Message: "serial_console_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Shutdown Wait Time of a Virtual Machine
// @Description Modify the Shutdown Wait Time configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyShutdownWaitTimeRequest true "Modify Shutdown Wait Time Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/shutdown-wait-time/:rid [put]
func ModifyShutdownWaitTime(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyShutdownWaitTimeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		var waitTime int
		if req.WaitTime != nil {
			waitTime = *req.WaitTime
		} else {
			waitTime = 0
		}

		if err := libvirtService.ModifyShutdownWaitTime(uint(ridInt), waitTime); err != nil {
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
			Message: "shutdown_wait_time_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Cloud-Init Data of a Virtual Machine
// @Description Modify the Cloud-Init Data and Metadata of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyCloudInitDataRequest true "Modify Cloud-Init Data Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/cloud-init/:rid [put]
func ModifyCloudInitData(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyCloudInitDataRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		if err := libvirtService.ModifyCloudInitData(uint(ridInt), req.Data, req.Metadata); err != nil {
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
			Message: "cloud_init_data_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Ignore UMSRs of a Virtual Machine
// @Description Modify the Ignore UMSRs configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyIgnoreUMSRsRequest true "Modify Ignore UMSRs Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/ignore-umsrs/:rid [put]
func ModifyIgnoreUMSRs(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "rid_not_provided",
			})
			return
		}

		ridInt, err := strconv.Atoi(rid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyIgnoreUMSRsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		ignoreUMSRs := false
		if req.IgnoreUMSRs != nil {
			ignoreUMSRs = *req.IgnoreUMSRs
		}

		if err := libvirtService.ModifyIgnoreUMSRs(uint(ridInt), ignoreUMSRs); err != nil {
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
			Message: "ignore_umsrs_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify TPM of a Virtual Machine
// @Description Modify the TPM configuration of a virtual machine
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyTPMRequest true "Modify TPM Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/tpm/:rid [put]
func ModifyTPM(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_rid_format",
			})
			return
		}

		var req ModifyTPMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		enabled := false
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		if err := libvirtService.ModifyTPMEmulation(uint(rid), enabled); err != nil {
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
			Message: "tpm_modified",
			Data:    nil,
			Error:   "",
		})
	}
}
