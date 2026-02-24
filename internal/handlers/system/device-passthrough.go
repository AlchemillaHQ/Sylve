// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"

	"github.com/gin-gonic/gin"
)

type AddPassthroughDeviceRequest struct {
	Domain   string `json:"domain" binding:"required"`
	DeviceID string `json:"deviceId" binding:"required"`
}

// @Summary List PCI Devices
// @Description List all PCI devices on the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]pciconf.PCIDevice] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/pci-devices [get]
func ListDevices() gin.HandlerFunc {
	return func(c *gin.Context) {
		devices, err := pciconf.GetPCIDevices()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]pciconf.PCIDevice]{
			Status:  "success",
			Message: "devices_list",
			Error:   "",
			Data:    devices,
		})
	}
}

// @Summary List Passed Through Devices
// @Description List all passed through devices on the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]models.PassedThroughIDs] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices [get]
func ListPPTDevices(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		passedThroughIDs, err := systemService.GetPPTDevices()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]models.PassedThroughIDs]{
			Status:  "success",
			Message: "passed_through_devices_list",
			Error:   "",
			Data:    passedThroughIDs,
		})
	}
}

// @Summary Add Passed Through Device
// @Description Add a device to the passed through devices db
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "Device ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices [post]
func AddPPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := systemService.AddPPTDevice(request.Domain, request.DeviceID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_added",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Prepare Passed Through Device
// @Description Add a device to loader.conf for passthrough on next boot
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "Device ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/prepare [post]
func PreparePPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := systemService.PreparePPTDevice(request.Domain, request.DeviceID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_prepared",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Import Passed Through Device
// @Description Import an existing ppt device into Sylve passthrough management
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "Device ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/import [post]
func ImportPPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := systemService.ImportPPTDevice(request.Domain, request.DeviceID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_imported",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Remove Passed Through Device
// @Description Remove a device from the passed through devices db
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param deviceId path string true "Device ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/{id} [delete]
func RemovePPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceID := c.Param("id")
		if deviceID == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "device ID cannot be empty",
				Data:    nil,
			})
			return
		}

		if err := systemService.RemovePPTDevice(deviceID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "device_removed",
			Error:   "",
			Data:    nil,
		})
	}
}
