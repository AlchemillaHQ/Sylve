// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func bindDHCPLeaseJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "dhcp_lease_request_too_large",
				Error:   "dhcp_lease_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_dhcp_lease_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func dhcpLeasePathID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_dhcp_lease_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(parsed), true
}

func dhcpLeaseErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidDHCPLease):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrDHCPLeaseNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrDHCPLeaseConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeDHCPLeaseError(c *gin.Context, message string, err error) {
	status := dhcpLeaseErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("dhcp_lease_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.DHCPLeaseErrorCode(err),
		Data:    nil,
	})
}

// @Summary List DHCP leases
// @Description Retrieve active runtime leases and configured static DHCP leases
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.Leases] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease [get]
func GetDHCPLeases(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		leases, err := svc.GetLeases()
		if err != nil {
			logger.L.Error().Err(err).Msg("dhcp_leases_retrieval_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_leases",
				Error:   "dhcp_leases_retrieval_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[networkServiceInterfaces.Leases]{
			Status:  "success",
			Message: "dhcp_leases_retrieved",
			Error:   "",
			Data:    leases,
		})
	}
}

// @Summary Create a static DHCP lease
// @Description Create and apply a new static DHCP lease
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.CreateStaticMapRequest true "Create Static DHCP Lease Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease [post]
func CreateDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.CreateStaticMapRequest
		if !bindDHCPLeaseJSON(c, &req) {
			return
		}

		id, err := svc.CreateStaticMap(&req)
		if err != nil {
			writeDHCPLeaseError(c, "failed_to_create_dhcp_lease", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "dhcp_lease_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Update a static DHCP lease
// @Description Replace and apply an existing static DHCP lease by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Lease ID" minimum(1)
// @Param request body networkServiceInterfaces.ModifyStaticMapRequest true "Update Static DHCP Lease Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease/{id} [put]
func UpdateDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dhcpLeasePathID(c)
		if !ok {
			return
		}

		var req networkServiceInterfaces.ModifyStaticMapRequest
		if !bindDHCPLeaseJSON(c, &req) {
			return
		}

		if err := svc.ModifyStaticMap(id, &req); err != nil {
			writeDHCPLeaseError(c, "failed_to_update_dhcp_lease", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_lease_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a static DHCP lease
// @Description Delete and apply a static DHCP lease by ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Lease ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease/{id} [delete]
func DeleteDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dhcpLeasePathID(c)
		if !ok {
			return
		}

		if err := svc.DeleteStaticMap(id); err != nil {
			writeDHCPLeaseError(c, "failed_to_delete_dhcp_lease", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_lease_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a dynamic DHCP lease
// @Description Delete an active DHCP lease matching the exact identifier and IP pair
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.DeleteDynamicLeaseRequest true "Delete Dynamic DHCP Lease Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/lease/dynamic [delete]
func DeleteDynamicDHCPLease(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.DeleteDynamicLeaseRequest
		if !bindDHCPLeaseJSON(c, &req) {
			return
		}

		if err := svc.DeleteDynamicLease(&req); err != nil {
			writeDHCPLeaseError(c, "failed_to_delete_dynamic_dhcp_lease", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dynamic_dhcp_lease_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
