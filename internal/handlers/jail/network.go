// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SetInheritanceRequest struct {
	IPv4 *bool `json:"ipv4" binding:"required"`
	IPv6 *bool `json:"ipv6" binding:"required"`
}

type jailNetworkService interface {
	SetInheritance(ctID uint, ipv4 bool, ipv6 bool) (jailServiceInterfaces.JailNetworkInheritanceResult, error)
	AddNetwork(ctID uint, req jailServiceInterfaces.AddJailNetworkRequest) (*jailModels.Network, error)
	EditNetwork(ctID uint, networkID uint, req jailServiceInterfaces.EditJailNetworkRequest) (*jailModels.Network, error)
	DeleteNetwork(ctID uint, networkID uint) error
}

func jailNetworkErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func jailNetworkErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	switch jailNetworkErrorCode(err) {
	case "invalid_request", "invalid_request_data", "invalid_ct_id", "invalid_network_id",
		"network_name_required", "invalid_network_name", "switch_name_required", "invalid_vlan", "invalid_mac",
		"invalid_ip4_cidr_not_assignable", "invalid_ip6_cidr_not_assignable",
		"invalid_ipv4_gateway", "invalid_ipv6_gateway", "network_object_type_mismatch",
		"network_object_requires_single_entry", "conflicting_network_value_sources", "network_mac_required",
		"default_gateway_requires_gateway", "cannot_set_dhcp_slaac_and_default_gateway_together",
		"cannot_set_dhcp_or_slaac_when_linux_jail":
		return http.StatusBadRequest
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "jail_not_found", "network_not_found", "switch_not_found", "network_object_not_found":
		return http.StatusNotFound
	case "restore_in_progress", "jail_network_change_requires_inactive",
		"cannot_add_network_when_inheriting_network", "cannot_edit_network_when_inheriting_network",
		"jail_network_name_exists", "jail_default_gateway_exists", "network_object_already_used",
		"jail_dataset_mountpoint_not_usable":
		return http.StatusConflict
	case "network_service_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeJailNetworkError(c *gin.Context, fallbackMessage string, err error) {
	status := jailNetworkErrorStatus(err)
	message := fallbackMessage
	if status != http.StatusInternalServerError {
		if code := jailNetworkErrorCode(err); code != "" {
			message = code
		}
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

func positiveJailNetworkParam(c *gin.Context, name, message string) (uint, bool) {
	value, err := utils.ParamUint(c, name)
	if err != nil || value == 0 {
		if err == nil {
			err = errors.New(message)
		}
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: message,
			Error:   err.Error(),
			Data:    nil,
		})
		return 0, false
	}
	return value, true
}

// @Summary Set jail network inheritance
// @Description Set IPv4 and IPv6 host-network inheritance for an inactive jail. Enabling either protocol removes the jail's attached network records.
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body SetInheritanceRequest true "Network inheritance request"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.JailNetworkInheritanceResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/network/inheritance [put]
func SetNetworkInheritance(jailService jailNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailNetworkParam(c, "ctid", "invalid_ct_id")
		if !ok {
			return
		}

		var req SetInheritanceRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		result, err := jailService.SetInheritance(ctID, *req.IPv4, *req.IPv6)
		if err != nil {
			writeJailNetworkError(c, "failed_to_set_network_inheritance", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.JailNetworkInheritanceResult]{
			Status:  "success",
			Message: "network_inheritance_set",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Attach a network to a jail
// @Description Create a network attachment for an inactive jail. The jail must not inherit host networking.
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body jailServiceInterfaces.AddJailNetworkRequest true "Network attachment request"
// @Success 201 {object} internal.APIResponse[jailModels.Network] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/networks [post]
func AddNetwork(jailService jailNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailNetworkParam(c, "ctid", "invalid_ct_id")
		if !ok {
			return
		}

		var req jailServiceInterfaces.AddJailNetworkRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		created, err := jailService.AddNetwork(ctID, req)
		if err != nil {
			writeJailNetworkError(c, "failed_to_add_network", err)
			return
		}
		if created == nil {
			writeJailNetworkError(c, "failed_to_add_network", errors.New("network_creation_returned_nil"))
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[jailModels.Network]{
			Status:  "success",
			Message: "network_added_to_jail",
			Error:   "",
			Data:    *created,
		})
	}
}

// @Summary Update a jail network
// @Description Partially update a network attachment belonging to an inactive jail. Omitted fields are preserved.
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param networkId path int true "Network attachment ID" minimum(1)
// @Param request body jailServiceInterfaces.EditJailNetworkRequest true "Network update request"
// @Success 200 {object} internal.APIResponse[jailModels.Network] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/networks/{networkId} [patch]
func EditNetwork(jailService jailNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailNetworkParam(c, "ctid", "invalid_ct_id")
		if !ok {
			return
		}
		networkID, ok := positiveJailNetworkParam(c, "networkId", "invalid_network_id")
		if !ok {
			return
		}

		var req jailServiceInterfaces.EditJailNetworkRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		updated, err := jailService.EditNetwork(ctID, networkID, req)
		if err != nil {
			writeJailNetworkError(c, "failed_to_edit_network", err)
			return
		}
		if updated == nil {
			writeJailNetworkError(c, "failed_to_edit_network", errors.New("network_update_returned_nil"))
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailModels.Network]{
			Status:  "success",
			Message: "network_updated_for_jail",
			Error:   "",
			Data:    *updated,
		})
	}
}

// @Summary Detach a network from a jail
// @Description Delete a network attachment that belongs to an inactive jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param networkId path int true "Network attachment ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/networks/{networkId} [delete]
func DeleteNetwork(jailService jailNetworkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := positiveJailNetworkParam(c, "ctid", "invalid_ct_id")
		if !ok {
			return
		}
		networkID, ok := positiveJailNetworkParam(c, "networkId", "invalid_network_id")
		if !ok {
			return
		}

		if err := jailService.DeleteNetwork(ctID, networkID); err != nil {
			writeJailNetworkError(c, "failed_to_delete_network", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "network_deleted_from_jail",
			Error:   "",
			Data:    nil,
		})
	}
}
