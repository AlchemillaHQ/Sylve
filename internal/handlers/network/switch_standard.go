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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"

	"github.com/gin-gonic/gin"
)

type CreateStandardSwitchRequest struct {
	Name                  string                                `json:"name" binding:"required"`
	MTU                   *int                                  `json:"mtu"`
	VLAN                  *int                                  `json:"vlan"`
	Network4              *uint                                 `json:"network4"`
	Gateway4              *uint                                 `json:"gateway4"`
	Network6              *uint                                 `json:"network6"`
	Gateway6              *uint                                 `json:"gateway6"`
	Network4Manual        *string                               `json:"network4Manual"`
	Gateway4Manual        *string                               `json:"gateway4Manual"`
	Network6Manual        *string                               `json:"network6Manual"`
	Gateway6Manual        *string                               `json:"gateway6Manual"`
	DisableIPv6           *bool                                 `json:"disableIPv6"`
	SLAAC                 *bool                                 `json:"slaac"`
	Private               *bool                                 `json:"private" binding:"required"`
	DefaultRoute          *bool                                 `json:"defaultRoute"`
	DisableBridgeOffloads *bool                                 `json:"disableBridgeOffloads"`
	DHCP                  *bool                                 `json:"dhcp"`
	Ports                 []string                              `json:"ports"`
	BridgeMAC             networkModels.StandardSwitchMACSource `json:"bridgeMac"`
}

type UpdateStandardSwitchRequest struct {
	MTU                   *int                                  `json:"mtu"`
	VLAN                  *int                                  `json:"vlan"`
	Network4              *uint                                 `json:"network4"`
	Gateway4              *uint                                 `json:"gateway4"`
	Network6              *uint                                 `json:"network6"`
	Gateway6              *uint                                 `json:"gateway6"`
	Network4Manual        *string                               `json:"network4Manual"`
	Gateway4Manual        *string                               `json:"gateway4Manual"`
	Network6Manual        *string                               `json:"network6Manual"`
	Gateway6Manual        *string                               `json:"gateway6Manual"`
	DisableIPv6           *bool                                 `json:"disableIPv6"`
	SLAAC                 *bool                                 `json:"slaac"`
	Private               *bool                                 `json:"private" binding:"required"`
	Ports                 []string                              `json:"ports"`
	DHCP                  *bool                                 `json:"dhcp"`
	DefaultRoute          *bool                                 `json:"defaultRoute"`
	DisableBridgeOffloads *bool                                 `json:"disableBridgeOffloads"`
	BridgeMAC             networkModels.StandardSwitchMACSource `json:"bridgeMac"`
}

func bindStandardSwitchJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "standard_switch_request_too_large",
				Error:   "standard_switch_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_standard_switch_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func standardSwitchPathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_standard_switch_id",
			Error:   "invalid_standard_switch_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func standardSwitchErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidStandardSwitch):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrStandardSwitchNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrStandardSwitchConflict), errors.Is(err, network.ErrStandardSwitchInUse):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeStandardSwitchError(c *gin.Context, message string, err error) {
	status := standardSwitchErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("standard_switch_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.StandardSwitchErrorCode(err),
		Data:    nil,
	})
}

func optionalInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func optionalUint(value *uint) uint {
	if value == nil {
		return 0
	}
	return *value
}

func optionalBool(value *bool) bool {
	return value != nil && *value
}

func standardSwitchManualAddresses(
	network4, gateway4, network6, gateway6 *string,
) networkModels.StandardSwitchManualAddresses {
	manual := networkModels.StandardSwitchManualAddresses{}
	if network4 != nil {
		manual.Network4 = *network4
	}
	if gateway4 != nil {
		manual.Gateway4 = *gateway4
	}
	if network6 != nil {
		manual.Network6 = *network6
	}
	if gateway6 != nil {
		manual.Gateway6 = *gateway6
	}
	return manual
}

// @Summary Create a standard switch
// @Description Create and apply a managed standard network switch
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateStandardSwitchRequest true "Create Standard Switch Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/standard [post]
func CreateStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateStandardSwitchRequest
		if !bindStandardSwitchJSON(c, &request) {
			return
		}

		id, err := networkService.NewStandardSwitch(request.Name,
			optionalInt(request.MTU),
			optionalInt(request.VLAN),
			optionalUint(request.Network4),
			optionalUint(request.Network6),
			optionalUint(request.Gateway4),
			optionalUint(request.Gateway6),
			request.Ports,
			request.BridgeMAC,
			optionalBool(request.Private),
			optionalBool(request.DHCP),
			optionalBool(request.DisableIPv6),
			optionalBool(request.SLAAC),
			optionalBool(request.DefaultRoute),
			optionalBool(request.DisableBridgeOffloads),
			standardSwitchManualAddresses(
				request.Network4Manual,
				request.Gateway4Manual,
				request.Network6Manual,
				request.Gateway6Manual,
			),
		)

		if err != nil {
			writeStandardSwitchError(c, "failed_to_create_switch", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "switch_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Delete a standard switch
// @Description Delete a managed standard network switch by ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "Standard Switch ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/standard/{id} [delete]
func DeleteStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := standardSwitchPathID(c)
		if !ok {
			return
		}

		if err := networkService.DeleteStandardSwitch(id); err != nil {
			writeStandardSwitchError(c, "failed_to_delete_switch", err)
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

// @Summary Update a standard switch
// @Description Replace and apply a managed standard network switch by ID
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Standard Switch ID" minimum(1)
// @Param request body UpdateStandardSwitchRequest true "Update Standard Switch Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/standard/{id} [put]
func UpdateStandardSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := standardSwitchPathID(c)
		if !ok {
			return
		}

		var request UpdateStandardSwitchRequest
		if !bindStandardSwitchJSON(c, &request) {
			return
		}

		err := networkService.EditStandardSwitch(
			id,
			optionalInt(request.MTU),
			optionalInt(request.VLAN),
			optionalUint(request.Network4),
			optionalUint(request.Network6),
			optionalUint(request.Gateway4),
			optionalUint(request.Gateway6),
			request.Ports,
			request.BridgeMAC,
			optionalBool(request.Private),
			optionalBool(request.DHCP),
			optionalBool(request.DisableIPv6),
			optionalBool(request.SLAAC),
			optionalBool(request.DefaultRoute),
			optionalBool(request.DisableBridgeOffloads),
			standardSwitchManualAddresses(
				request.Network4Manual,
				request.Gateway4Manual,
				request.Network6Manual,
				request.Gateway6Manual,
			),
		)
		if err != nil {
			writeStandardSwitchError(c, "failed_to_update_switch", err)
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
