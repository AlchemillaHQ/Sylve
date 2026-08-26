// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"

	"github.com/gin-gonic/gin"
)

type AddPassthroughDeviceRequest struct {
	Domain   string `json:"domain" binding:"required,max=3"`
	DeviceID string `json:"deviceID" binding:"required,max=16"`
}

type RemovePassthroughDeviceResponse struct {
	RebootRequired bool `json:"rebootRequired"`
}

var (
	listPCIDevicesOperation = pciconf.GetPCIDevices
	listPPTDevicesOperation = func(service *system.Service) ([]models.PassedThroughIDs, error) {
		return service.GetPPTDevices()
	}
	addPPTDeviceOperation = func(service *system.Service, domain, deviceID string) (*models.PassedThroughIDs, error) {
		return service.AddPPTDevice(domain, deviceID)
	}
	preparePPTDeviceOperation = func(service *system.Service, domain, deviceID string) error {
		return service.PreparePPTDevice(domain, deviceID)
	}
	importPPTDeviceOperation = func(service *system.Service, domain, deviceID string) (*models.PassedThroughIDs, bool, error) {
		return service.ImportPPTDevice(domain, deviceID)
	}
	removePPTDeviceOperation = func(service *system.Service, id uint) (bool, error) {
		return service.RemovePPTDevice(id)
	}
)

func bindPassthroughJSON(c *gin.Context, request *AddPassthroughDeviceRequest) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "passthrough_request_too_large",
				Error:   "passthrough_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_passthrough_request",
			Error:   "invalid_passthrough_request",
			Data:    nil,
		})
		return false
	}

	return true
}

func passthroughErrorStatus(err error) int {
	switch {
	case errors.Is(err, system.ErrInvalidPassthroughDevice),
		errors.Is(err, system.ErrUnsupportedPassthroughDomain):
		return http.StatusBadRequest
	case errors.Is(err, system.ErrPassthroughDeviceNotFound):
		return http.StatusNotFound
	case errors.Is(err, system.ErrPassthroughDeviceAlreadyAdded),
		errors.Is(err, system.ErrPassthroughDeviceNeedsImport),
		errors.Is(err, system.ErrPassthroughDeviceNotAttached),
		errors.Is(err, system.ErrPassthroughDeviceInUse):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writePassthroughError(c *gin.Context, message string, err error) {
	status := passthroughErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("passthrough_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   system.PassthroughErrorCode(err),
		Data:    nil,
	})
}

// @Summary List PCI devices
// @Description List PCI devices detected on the selected system
// @Tags System
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]pciconf.PCIDevice] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/pci-devices [get]
func ListDevices() gin.HandlerFunc {
	return func(c *gin.Context) {
		devices, err := listPCIDevicesOperation()

		if err != nil {
			logger.L.Error().Err(err).Msg("list_pci_devices_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "pci_devices_list_failed",
				Error:   "pci_devices_list_failed",
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

// @Summary List managed passthrough devices
// @Description List PCI passthrough mappings managed on the selected system
// @Tags System
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]models.PassedThroughIDs] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices [get]
func ListPPTDevices(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		passedThroughIDs, err := listPPTDevicesOperation(systemService)

		if err != nil {
			logger.L.Error().Err(err).Msg("list_passthrough_devices_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "passthrough_devices_list_failed",
				Error:   "passthrough_devices_list_failed",
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

// @Summary Enable PCI passthrough
// @Description Attach a domain-zero PCI device to ppt and persist its managed mapping
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "PCI domain and bus/device/function address"
// @Success 201 {object} internal.APIResponse[models.PassedThroughIDs] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "PCI Device Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices [post]
func AddPPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if !bindPassthroughJSON(c, &request) {
			return
		}

		device, err := addPPTDeviceOperation(systemService, request.Domain, request.DeviceID)
		if err != nil {
			writePassthroughError(c, "device_add_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[models.PassedThroughIDs]{
			Status:  "success",
			Message: "device_added",
			Error:   "",
			Data:    *device,
		})
	}
}

// @Summary Prepare PCI passthrough
// @Description Add a domain-zero PCI device to loader.conf for passthrough after reboot
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "PCI domain and bus/device/function address"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "PCI Device Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/prepare [post]
func PreparePPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if !bindPassthroughJSON(c, &request) {
			return
		}

		if err := preparePPTDeviceOperation(systemService, request.Domain, request.DeviceID); err != nil {
			writePassthroughError(c, "device_prepare_failed", err)
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

// @Summary Import a PCI passthrough device
// @Description Persist a domain-zero PCI device that is already attached to ppt
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddPassthroughDeviceRequest true "PCI domain and bus/device/function address"
// @Success 200 {object} internal.APIResponse[models.PassedThroughIDs] "Already Managed"
// @Success 201 {object} internal.APIResponse[models.PassedThroughIDs] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "PCI Device Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/import [post]
func ImportPPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddPassthroughDeviceRequest

		if !bindPassthroughJSON(c, &request) {
			return
		}

		device, created, err := importPPTDeviceOperation(systemService, request.Domain, request.DeviceID)
		if err != nil {
			writePassthroughError(c, "device_import_failed", err)
			return
		}

		status := http.StatusOK
		message := "device_already_managed"
		if created {
			status = http.StatusCreated
			message = "device_imported"
		}

		c.JSON(status, internal.APIResponse[models.PassedThroughIDs]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data:    *device,
		})
	}
}

// @Summary Disable PCI passthrough
// @Description Remove a managed passthrough mapping and restore its host driver when possible
// @Tags System
// @Produce json
// @Security BearerAuth
// @Param id path int true "Passthrough mapping ID" minimum(1)
// @Success 200 {object} internal.APIResponse[RemovePassthroughDeviceResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Passthrough Mapping Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Mapping In Use"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/ppt-devices/{id} [delete]
func RemovePPTDevice(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		mappingID, err := strconv.ParseUint(c.Param("id"), 10, 32)
		if err != nil || mappingID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_passthrough_mapping_id",
				Error:   "invalid_passthrough_mapping_id",
				Data:    nil,
			})
			return
		}

		rebootRequired, err := removePPTDeviceOperation(systemService, uint(mappingID))
		if err != nil {
			writePassthroughError(c, "device_remove_failed", err)
			return
		}

		message := "device_removed"
		if rebootRequired {
			message = "device_removed_reboot_required"
		}

		c.JSON(http.StatusOK, internal.APIResponse[RemovePassthroughDeviceResponse]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data: RemovePassthroughDeviceResponse{
				RebootRequired: rebootRequired,
			},
		})
	}
}
