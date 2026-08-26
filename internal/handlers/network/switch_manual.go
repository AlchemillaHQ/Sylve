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

type CreateManualSwitchRequest struct {
	Name   string `json:"name" binding:"required"`
	Bridge string `json:"bridge" binding:"required"`
}

var (
	createManualSwitchOperation = func(service *network.Service, name, bridge string) (*networkModels.ManualSwitch, error) {
		return service.CreateManualSwitch(name, bridge)
	}
	deleteManualSwitchOperation = func(service *network.Service, id uint) error {
		return service.DeleteManualSwitch(id)
	}
)

func bindManualSwitchJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "manual_switch_request_too_large",
				Error:   "manual_switch_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_manual_switch_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func manualSwitchPathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_manual_switch_id",
			Error:   "invalid_manual_switch_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func manualSwitchErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidManualSwitch):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrManualSwitchNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrManualSwitchConflict), errors.Is(err, network.ErrManualSwitchInUse):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeManualSwitchError(c *gin.Context, message string, err error) {
	status := manualSwitchErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("manual_switch_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.ManualSwitchErrorCode(err),
		Data:    nil,
	})
}

// @Summary Create a manual switch
// @Description Register an existing host bridge as a manual network switch
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateManualSwitchRequest true "Create Manual Switch Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/manual [post]
func CreateManualSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateManualSwitchRequest
		if !bindManualSwitchJSON(c, &req) {
			return
		}

		switchModel, err := createManualSwitchOperation(networkService, req.Name, req.Bridge)
		if err != nil {
			writeManualSwitchError(c, "failed_to_create_manual_switch", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "manual_switch_created",
			Error:   "",
			Data:    switchModel.ID,
		})
	}
}

// @Summary Delete a manual switch
// @Description Delete a manual switch registration by ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "Manual Switch ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/switch/manual/{id} [delete]
func DeleteManualSwitch(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := manualSwitchPathID(c)
		if !ok {
			return
		}

		if err := deleteManualSwitchOperation(networkService, id); err != nil {
			writeManualSwitchError(c, "failed_to_delete_manual_switch", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "manual_switch_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
