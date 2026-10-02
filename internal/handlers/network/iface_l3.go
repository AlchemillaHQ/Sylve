// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type HostInterfaceL3AddressPayload struct {
	Address string `json:"address" binding:"required"`
}

type HostInterfaceL3SaveRequest struct {
	IPv6Mode         *string                         `json:"ipv6Mode" enums:"inherit,enabled,disabled"`
	MTU              *uint                           `json:"mtu"`
	Metric           *uint                           `json:"metric"`
	Addresses        []HostInterfaceL3AddressPayload `json:"addresses"`
	ExpectedRevision *uint64                         `json:"expectedRevision"`
}

func RequireLocalHostOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(c.GetHeader("X-Current-Hostname")) == "" &&
			strings.TrimSpace(c.Query("auth")) == "" {
			c.Next()
			return
		}
		requested, err := utils.GetCurrentHostnameFromHeader(c.Request.Header, c.Request)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_selected_node",
				Error:   "invalid_selected_node",
				Data:    nil,
			})
			return
		}
		requested = strings.TrimSpace(requested)
		if requested == "" {
			c.Next()
			return
		}

		local, err := utils.GetSystemHostname()
		local = strings.TrimSpace(local)
		if err != nil || local == "" {
			c.AbortWithStatusJSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "local_hostname_unavailable",
				Error:   "local_hostname_unavailable",
				Data:    nil,
			})
			return
		}

		if !strings.EqualFold(requested, local) {
			c.AbortWithStatusJSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "host_interface_l3_local_node_only",
				Error:   "host_interface_l3_local_node_only",
				Data:    nil,
			})
			return
		}

		c.Next()
	}
}

func respondHostInterfaceL3Error(c *gin.Context, err error) {
	code := network.HostInterfaceL3ErrorCode(err)
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, network.ErrInvalidHostInterfaceL3):
		status = http.StatusBadRequest
	case errors.Is(err, network.ErrHostInterfaceL3NotFound):
		status = http.StatusNotFound
	case errors.Is(err, network.ErrHostInterfaceL3Conflict), errors.Is(err, network.ErrHostInterfaceL3Pending):
		status = http.StatusConflict
	}

	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Msg("host_interface_l3_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

// @Summary List Host Interface L3 Configuration
// @Description List host layer-3 configuration rows for interfaces, including rows whose interface is missing, with live state and conflict markers
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.HostInterfaceL3List] "Rows with live state and per-interface eligibility"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/interface/l3 [get]
func ListHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := networkService.GetHostInterfaceL3()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_host_interface_l3")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_host_interface_l3",
				Error:   "host_interface_l3_list_failed",
				Data:    nil,
			})
			return
		}

		if list.Rows == nil {
			list.Rows = make([]networkServiceInterfaces.HostInterfaceL3Entry, 0)
		}
		if list.Targets == nil {
			list.Targets = make([]networkServiceInterfaces.HostInterfaceL3TargetEntry, 0)
		}

		c.JSON(http.StatusOK, internal.APIResponse[networkServiceInterfaces.HostInterfaceL3List]{
			Status:  "success",
			Message: "host_interface_l3_list",
			Error:   "",
			Data:    list,
		})
	}
}

// @Summary Save Host Interface L3 Configuration
// @Description Stage a Host IP change; the response carries the pending id and the 60-second confirmation deadline
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Interface name"
// @Param request body networkHandlers.HostInterfaceL3SaveRequest true "Desired Host IP state"
// @Success 202 {object} internal.APIResponse[networkServiceInterfaces.HostInterfaceL3PendingEntry] "Applied, awaiting confirmation"
// @Failure 400 {object} internal.APIResponse[any] "Invalid request"
// @Failure 409 {object} internal.APIResponse[any] "Revision, pending or interface conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/interface/{name}/l3 [put]
func SaveHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload HostInterfaceL3SaveRequest
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   "invalid_request_body",
				Data:    nil,
			})
			return
		}

		request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{
			IPv6Mode:  payload.IPv6Mode,
			MTU:       payload.MTU,
			Metric:    payload.Metric,
			Addresses: make([]networkServiceInterfaces.HostInterfaceL3AddressInput, 0, len(payload.Addresses)),
		}
		if payload.ExpectedRevision != nil {
			request.ExpectedRevision = *payload.ExpectedRevision
		}
		for _, address := range payload.Addresses {
			request.Addresses = append(request.Addresses, networkServiceInterfaces.HostInterfaceL3AddressInput{
				Address: address.Address,
			})
		}

		entry, err := networkService.SaveHostInterfaceL3(c.Param("name"), request)
		if err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[networkServiceInterfaces.HostInterfaceL3PendingEntry]{
			Status:  "success",
			Message: "host_interface_l3_apply_pending",
			Error:   "",
			Data:    entry,
		})
	}
}

// @Summary Delete Host Interface L3 Configuration
// @Description Stage removal of the Host IP row; confirmation promotes the delete, timeout restores it
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param name path string true "Interface name"
// @Param expectedRevision query integer true "Revision returned by the list endpoint"
// @Success 202 {object} internal.APIResponse[networkServiceInterfaces.HostInterfaceL3PendingEntry] "Removed, awaiting confirmation"
// @Failure 404 {object} internal.APIResponse[any] "No configuration for this interface"
// @Failure 409 {object} internal.APIResponse[any] "Revision, pending or identity conflict"
// @Router /network/interface/{name}/l3 [delete]
func DeleteHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var expectedRevision uint64
		if raw := strings.TrimSpace(c.Query("expectedRevision")); raw != "" {
			parsed, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_expected_revision",
					Error:   "invalid_expected_revision",
					Data:    nil,
				})
				return
			}
			expectedRevision = parsed
		}

		entry, err := networkService.DeleteHostInterfaceL3(c.Param("name"), expectedRevision)
		if err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[networkServiceInterfaces.HostInterfaceL3PendingEntry]{
			Status:  "success",
			Message: "host_interface_l3_delete_pending",
			Error:   "",
			Data:    entry,
		})
	}
}

// @Summary Reapply Host Interface L3 Configuration
// @Description Re-run the reconciler for one interface; foreign state is never removed
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param name path string true "Interface name"
// @Success 200 {object} internal.APIResponse[any] "Reapplied"
// @Failure 409 {object} internal.APIResponse[any] "Conflict or pending operation"
// @Router /network/interface/{name}/l3/reapply [post]
func ReapplyHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := networkService.ReapplyHostInterfaceL3(c.Param("name")); err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "host_interface_l3_reapplied",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary List Pending Host Interface L3 Operations
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkServiceInterfaces.HostInterfaceL3PendingEntry] "Pending operations awaiting confirmation"
// @Router /network/interface/l3/pending [get]
func GetHostInterfaceL3Pending(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := networkService.GetHostInterfaceL3Pending()
		if err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		if entries == nil {
			entries = make([]networkServiceInterfaces.HostInterfaceL3PendingEntry, 0)
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkServiceInterfaces.HostInterfaceL3PendingEntry]{
			Status:  "success",
			Message: "host_interface_l3_pending_list",
			Error:   "",
			Data:    entries,
		})
	}
}

// @Summary Confirm A Pending Host Interface L3 Operation
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path string true "Pending operation id"
// @Success 200 {object} internal.APIResponse[any] "Confirmed"
// @Failure 404 {object} internal.APIResponse[any] "Unknown pending operation"
// @Failure 409 {object} internal.APIResponse[any] "Expired or conflicting operation"
// @Router /network/host-ip/pending/{id}/confirm [post]
func ConfirmHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := networkService.ConfirmHostInterfaceL3(c.Param("id")); err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "host_interface_l3_confirmed",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Revert A Pending Host Interface L3 Operation
// @Description Restore an applied operation; discard a prepared operation without changing ambiguous runtime state
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path string true "Pending operation id"
// @Success 200 {object} internal.APIResponse[any] "Reverted"
// @Failure 404 {object} internal.APIResponse[any] "Unknown pending operation"
// @Router /network/host-ip/pending/{id}/revert [post]
func RevertHostInterfaceL3(networkService *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := networkService.RevertHostInterfaceL3(c.Param("id")); err != nil {
			respondHostInterfaceL3Error(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "host_interface_l3_reverted",
			Error:   "",
			Data:    nil,
		})
	}
}
