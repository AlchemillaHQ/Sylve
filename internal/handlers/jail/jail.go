// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/jail"

	"github.com/gin-gonic/gin"
)

type JailEditDescRequest struct {
	ID          uint   `json:"id" binding:"required"`
	Description string `json:"description"`
}

type JailEditNameRequest struct {
	ID   uint   `json:"id" binding:"required"`
	Name string `json:"name" binding:"required"`
}

var jailCreateConflictCodes = map[string]struct{}{
	"ipv4_already_used":                     {},
	"ipv6_already_used":                     {},
	"jail_base_fs_with_ctid_already_exists": {},
	"jail_create_stale_artifacts_detected":  {},
	"jail_with_ctid_already_exists":         {},
	"mac_already_used":                      {},
}

var jailCreateBadRequestCodes = map[string]struct{}{
	"base_is_not_a_directory":                        {},
	"base_path_does_not_exist":                       {},
	"download_is_not_base_or_rootfs":                 {},
	"download_uuid_required":                         {},
	"failed_to_find_download":                        {},
	"invalid_ct_id":                                  {},
	"invalid_description":                            {},
	"invalid_hostname":                               {},
	"invalid_ipv4_gateway":                           {},
	"invalid_ipv6_gateway":                           {},
	"invalid_jail_allowed_options":                   {},
	"invalid_jail_type":                              {},
	"invalid_vm_name":                                {},
	"linux_jails_cannot_use_dhcp_or_slaac":           {},
	"pool_not_found":                                 {},
	"standard_switch_not_found":                      {},
	"start_order_must_be_greater_than_or_equal_to_0": {},
	"switch_name_required":                           {},
}

var jailCreateAliasCodes = map[string]string{
	"failed_to_find_base": "base_path_does_not_exist",
}

func isSnakeCaseErrorCode(value string) bool {
	if value == "" || !strings.Contains(value, "_") {
		return false
	}

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}

		return false
	}

	return true
}

func extractCreateJailErrorCode(message string) string {
	parts := strings.Split(strings.ToLower(message), ":")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}

		token := part
		if idx := strings.IndexAny(token, " \t\r\n,.;()[]{}"); idx >= 0 {
			token = token[:idx]
		}
		token = strings.TrimSpace(token)

		if isSnakeCaseErrorCode(token) {
			return token
		}
	}

	return ""
}

func classifyCreateJailError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_create_jail"
	}

	errText := strings.ToLower(err.Error())
	if strings.Contains(errText, "jail_with_ctid_") && strings.Contains(errText, "already_exists") {
		return http.StatusConflict, "jail_with_ctid_already_exists"
	}

	code := extractCreateJailErrorCode(errText)
	if alias, ok := jailCreateAliasCodes[code]; ok {
		code = alias
	}

	switch code {
	case "failed_to_begin_tx",
		"failed_to_commit_tx",
		"failed_to_check_existing_jail":
		return http.StatusInternalServerError, "jail_create_database_failure"
	case "failed_to_create_jail_dataset",
		"failed_to_create_jail",
		"failed_to_create_network",
		"failed_to_create_jail_config",
		"failed_to_copy_base",
		"failed_to_create_jail_directory",
		"failed_to_write_jail_config_file",
		"failed_to_write_log_file",
		"failed_to_write_fstab_file",
		"failed_to_prepare_resolv_conf_path",
		"failed_to_write_resolv_conf_file",
		"failed_to_create_sylve_directory":
		return http.StatusInternalServerError, "jail_create_runtime_failure"
	case "failed_to_list_usable_pools_for_jail_create_precheck",
		"failed_to_check_jail_root_dataset_for_create_precheck",
		"system_service_not_initialized",
		"zfs_client_not_initialized":
		return http.StatusInternalServerError, "jail_create_dependency_not_ready"
	}

	if _, ok := jailCreateConflictCodes[code]; ok {
		return http.StatusConflict, code
	}

	if _, ok := jailCreateBadRequestCodes[code]; ok {
		return http.StatusBadRequest, code
	}

	if strings.HasPrefix(code, "invalid_") {
		return http.StatusBadRequest, code
	}

	if code != "" {
		return http.StatusInternalServerError, code
	}

	return http.StatusInternalServerError, "failed_to_create_jail"
}

func classifyUpdateJailNameError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_update_jail_name"
	}

	errText := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errText, "jail_not_found"):
		return http.StatusNotFound, "jail_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	}

	code := extractCreateJailErrorCode(errText)
	switch code {
	case "invalid_jail_id", "invalid_vm_name":
		return http.StatusBadRequest, code
	case "jail_name_already_in_use":
		return http.StatusConflict, code
	case "replication_lease_check_failed":
		return http.StatusInternalServerError, code
	}

	if strings.HasPrefix(code, "invalid_") {
		return http.StatusBadRequest, code
	}
	if code != "" {
		return http.StatusInternalServerError, code
	}

	return http.StatusInternalServerError, "failed_to_update_jail_name"
}

// @Summary List all Jails
// @Description Retrieve a list of all jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.Jail] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail [get]
func ListJails(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJails()

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_jails: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailModels.Jail]{
			Status:  "success",
			Message: "jail_listed",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary Get a Jail by an Identifier
// @Description Retrieve a jail by its CTID or ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param identifier path string true "Jail CTID or ID"
// @Param type query string false "Type of identifier (ctid or id)"  Enums(ctid, id) default(ctid)
// @Success 200 {object} internal.APIResponse[jailModels.Jail] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/:id [get]
func GetJailByIdentifier(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id",
				Data:    nil,
				Error:   "Jail ID is required",
			})
			return
		}

		var t string = c.DefaultQuery("type", "ctid")
		if t != "ctid" && t != "id" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_type_param",
				Data:    nil,
				Error:   "Type parameter must be either 'ctid' or 'id'",
			})
			return
		}

		identifier, err := strconv.Atoi(id)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id_format",
				Data:    nil,
				Error:   "Jail ID must be a valid integer",
			})
			return
		}

		var jail *jailModels.Jail
		if t == "ctid" {
			jail, err = jailService.GetJailByCTID(uint(identifier))
		} else {
			jail, err = jailService.GetJail(uint(identifier))
		}

		if err != nil || jail.ID == 0 {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail",
				Data:    nil,
				Error:   "failed_to_get_jail: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[jailModels.Jail]{
			Status:  "success",
			Message: "jail_retrieved_by_identifier",
			Data:    *jail,
			Error:   "",
		})
	}
}

// @Summary List all Jails (Simple)
// @Description Retrieve a simple list of all jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.SimpleList] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/simple [get]
func ListJailsSimple(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJailsSimple()

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_jails_simple: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]jailServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "jail_listed_simple",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary Get a Jail by CTID or ID (Simple)
// @Description Retrieve a simple jail by its CTID or ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param identifier path string true "Jail CTID or ID"
// @Param type query string false "Type of identifier (ctid or id)"  Enums(ctid, id) default(ctid)
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.SimpleList] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/simple/:id [get]
func GetSimpleJailByIdentifier(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id",
				Data:    nil,
				Error:   "Jail ID is required",
			})
			return
		}

		var t string = c.DefaultQuery("type", "ctid")
		if t != "ctid" && t != "id" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_type_param",
				Data:    nil,
				Error:   "Type parameter must be either 'ctid' or 'id'",
			})
			return
		}

		identifier, err := strconv.Atoi(id)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_jail_id_format",
				Data:    nil,
				Error:   "Jail ID must be a valid integer",
			})
			return
		}

		jail, err := jailService.GetSimpleJail(identifier, t == "ctid")

		if err != nil || jail.ID == 0 {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail_simple",
				Data:    nil,
				Error:   "failed_to_get_jail_simple: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[jailServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "jail_retrieved_by_identifier_simple",
			Data:    jail,
			Error:   "",
		})
	}
}

// @Summary Create a new Jail
// @Description Create a new jail with the provided configuration
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body jailServiceInterfaces.CreateJailRequest true "Create Jail Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail [post]
func CreateJail(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.CreateJailRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		ctx := c.Request.Context()
		err := jailService.CreateJail(ctx, req)

		if err != nil {
			statusCode, errorCode := classifyCreateJailError(err)

			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   "failed_to_create: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_created",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Delete a Jail
// @Description Delete a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "CTID of the Jail"
// @Param deletemacs query bool true "Delete or Keep"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid} [delete]
func DeleteJail(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctid, berr := c.Params.Get("ctid")
		if !berr || ctid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Data:    nil,
				Error:   "invalid_ctid: ",
			})
			return
		}

		var ctidInt int
		ctidInt, err := strconv.Atoi(ctid)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid_format",
				Data:    nil,
				Error:   fmt.Sprintf("invalid_ctid_format: %s", err.Error()),
			})
			return
		}

		deleteMacsStr := c.Query("deletemacs")
		if deleteMacsStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deletemacs_param",
				Error:   "missing 'deletemacs' query parameter",
				Data:    nil,
			})
			return
		}

		deleteMacs, err := strconv.ParseBool(deleteMacsStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deletemacs_param",
				Error:   "invalid 'deletemacs' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		deleteRootFsStr := c.Query("deleterootfs")
		if deleteRootFsStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deleterootfs_param",
				Error:   "missing 'deleterootfs' query parameter",
				Data:    nil,
			})
			return
		}

		deleteRootFs, err := strconv.ParseBool(deleteRootFsStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deleterootfs_param",
				Error:   "invalid 'deleterootfs' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		allowed, leaseErr := jailService.CanMutateProtectedJail(uint(ctidInt))
		if leaseErr != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_check_failed",
				Data:    nil,
				Error:   leaseErr.Error(),
			})
			return
		}
		if !allowed {
			c.JSON(403, internal.APIResponse[any]{
				Status:  "error",
				Message: "standby_mode_edit_not_allowed",
				Data:    nil,
				Error:   "replication_lease_not_owned",
			})
			return
		}

		ctx := c.Request.Context()
		err = jailService.DeleteJail(ctx, uint(ctidInt), deleteMacs, deleteRootFs)

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_jail",
				Data:    nil,
				Error:   "failed_to_delete_jail: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_deleted",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Edit a Jail's description
// @Description Update the description of a jail by its ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JailEditDescRequest true "Edit Jail Description Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/description [put]
func UpdateJailDescription(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JailEditDescRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		err := jailService.UpdateDescription(req.ID, req.Description)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_description",
				Data:    nil,
				Error:   "failed_to_update_description: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_description_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Edit a Jail's name
// @Description Update the name of a jail by its ID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body JailEditNameRequest true "Edit Jail Name Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /jail/name [put]
func UpdateJailName(jailService *jail.Service, clusterService *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req JailEditNameRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		ctID, err := jailService.UpdateName(req.ID, req.Name)
		if err != nil {
			statusCode, errorCode := classifyUpdateJailNameError(err)
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		if clusterService != nil && ctID > 0 {
			syncErr := clusterService.SyncBackupJobFriendlySourceByGuestClusterWide(cluster.BackupJobFriendlySourceUpdate{
				GuestType:   clusterModels.ReplicationGuestTypeJail,
				GuestID:     ctID,
				FriendlySrc: strings.TrimSpace(req.Name),
			})
			if syncErr != nil {
				logger.L.Warn().
					Err(syncErr).
					Uint("jail_ctid", ctID).
					Msg("failed_to_sync_backup_friendly_source_after_jail_rename")
			}
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_name_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Update Resource Limits
// @Description Enable or disable a Jail's resource limits
// @Tags jail
// @Accept json
// @Produce json
// @Param ctId path int true "Container ID"
// @Param enabled query bool true "Enable or Disable"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /jail/resource-limits/{ctId} [put]
func UpdateResourceLimits(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctId, err := strconv.ParseUint(c.Param("ctId"), 10, 32)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ctid",
				Error:   "invalid_ctid: " + err.Error(),
				Data:    nil,
			})
			return
		}

		enabledStr := c.Query("enabled")
		if enabledStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_enabled_param",
				Error:   "missing 'enabled' query parameter",
				Data:    nil,
			})
			return
		}

		enabled, err := strconv.ParseBool(enabledStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_enabled_param",
				Error:   "invalid 'enabled' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		if err := jailService.UpdateResourceLimits(uint(ctId), enabled); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_resource_limits",
				Data:    nil,
				Error:   "failed_to_update_resource_limits: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_resource_limits_updated",
			Data:    nil,
			Error:   "",
		})
	}
}
