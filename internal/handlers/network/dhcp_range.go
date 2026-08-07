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
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func bindDHCPRangeJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "dhcp_range_request_too_large",
				Error:   "dhcp_range_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_dhcp_range_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func dhcpRangePathID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_dhcp_range_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(parsed), true
}

func dhcpRangeErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidDHCPRange):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrDHCPRangeNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrDHCPRangeConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeDHCPRangeError(c *gin.Context, message string, err error) {
	status := dhcpRangeErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("dhcp_range_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.DHCPRangeErrorCode(err),
		Data:    nil,
	})
}

// @Summary List DHCP ranges
// @Description Retrieve all configured DHCP ranges
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkModels.DHCPRange] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range [get]
func GetDHCPRanges(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ranges, err := svc.GetRanges()
		if err != nil {
			logger.L.Error().Err(err).Msg("dhcp_ranges_retrieval_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_ranges",
				Error:   "dhcp_ranges_retrieval_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.DHCPRange]{
			Status:  "success",
			Message: "dhcp_ranges_retrieved",
			Error:   "",
			Data:    ranges,
		})
	}
}

// @Summary Create a DHCP range
// @Description Create and apply a DHCP range for a configured switch
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.CreateDHCPRangeRequest true "Create DHCP Range Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range [post]
func CreateDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.CreateDHCPRangeRequest
		if !bindDHCPRangeJSON(c, &req) {
			return
		}

		id, err := svc.CreateRange(&req)
		if err != nil {
			writeDHCPRangeError(c, "failed_to_create_dhcp_range", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "dhcp_range_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Update a DHCP range
// @Description Replace and apply an existing DHCP range; its address family is immutable
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Range ID" minimum(1)
// @Param request body networkServiceInterfaces.ModifyDHCPRangeRequest true "Update DHCP Range Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range/{id} [put]
func ModifyDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dhcpRangePathID(c)
		if !ok {
			return
		}

		var req networkServiceInterfaces.ModifyDHCPRangeRequest
		if !bindDHCPRangeJSON(c, &req) {
			return
		}

		if err := svc.ModifyRange(id, &req); err != nil {
			writeDHCPRangeError(c, "failed_to_modify_dhcp_range", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_range_modified",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a DHCP range
// @Description Delete and apply a DHCP range and its associated static leases
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Range ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range/{id} [delete]
func DeleteDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dhcpRangePathID(c)
		if !ok {
			return
		}

		if err := svc.DeleteRange(id); err != nil {
			writeDHCPRangeError(c, "failed_to_delete_dhcp_range", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_range_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
