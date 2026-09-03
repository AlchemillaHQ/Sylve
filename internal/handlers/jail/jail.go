// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type JailEditDescRequest struct {
	Description *string `json:"description" binding:"required"`
}

type JailEditNameRequest struct {
	Name string `json:"name" binding:"required"`
}

type JailCreateResponse struct {
	CTID uint   `json:"ctId"`
	Name string `json:"name"`
}

type jailListService interface {
	GetJails() ([]jailModels.Jail, error)
}

type jailDetailService interface {
	GetJailByCTID(ctID uint) (*jailModels.Jail, error)
}

type jailSimpleListService interface {
	GetJailsSimple() ([]jailServiceInterfaces.SimpleList, error)
}

type jailSimpleDetailService interface {
	GetSimpleJailByCTID(ctID uint) (jailServiceInterfaces.SimpleList, error)
}

type jailCreateService interface {
	CreateJail(ctx context.Context, data jailServiceInterfaces.CreateJailRequest) error
}

type jailDescriptionService interface {
	UpdateDescription(ctID uint, description string) error
}

type jailNameService interface {
	UpdateName(ctID uint, name string) error
}

type jailDeletionService interface {
	CanMutateProtectedJail(ctID uint) (bool, error)
	DeleteJailWithWarnings(
		ctx context.Context,
		ctID uint,
		deleteMacs bool,
		deleteRootFS bool,
	) (jailServiceInterfaces.DeleteJailResult, error)
}

var jailCreateConflictCodes = map[string]struct{}{
	"bootstrap_not_completed":               {},
	"ipv4_already_used":                     {},
	"ipv6_already_used":                     {},
	"jail_base_fs_with_ctid_already_exists": {},
	"jail_create_stale_artifacts_detected":  {},
	"jail_with_ctid_already_exists":         {},
	"mac_already_used":                      {},
}

var jailCreateBadRequestCodes = map[string]struct{}{
	"base_and_bootstrap_name_are_mutually_exclusive": {},
	"base_is_not_a_directory":                        {},
	"base_path_does_not_exist":                       {},
	"bootstrap_dataset_does_not_exist":               {},
	"bootstrap_mount_does_not_exist":                 {},
	"bootstrap_not_found":                            {},
	"bootstrap_version_newer_than_host":              {},
	"devfs_management_disabled":                      {},
	"download_is_not_base_or_rootfs":                 {},
	"download_uuid_or_bootstrap_name_required":       {},
	"download_uuid_required":                         {},
	"failed_to_find_download":                        {},
	"invalid_ct_id":                                  {},
	"invalid_description":                            {},
	"invalid_hostname":                               {},
	"invalid_ipv4_gateway":                           {},
	"invalid_ipv6_gateway":                           {},
	"invalid_jail_allowed_options":                   {},
	"invalid_jail_metadata":                          {},
	"invalid_jail_type":                              {},
	"invalid_lifecycle_hooks":                        {},
	"invalid_cores":                                  {},
	"invalid_memory":                                 {},
	"invalid_vm_name":                                {},
	"lifecycle_hook_script_required":                 {},
	"linux_jails_cannot_use_dhcp_or_slaac":           {},
	"memory_limit_exceeds_host_capacity":             {},
	"memory_limit_too_low":                           {},
	"pool_not_found":                                 {},
	"resource_limits_require_cores_and_memory":       {},
	"reserved_jail_option_marker":                    {},
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
	switch {
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "guest_identity_registry_initializing"),
		strings.Contains(errText, "guest_identity_cluster_formation_in_progress"):
		return http.StatusServiceUnavailable, "guest_identity_registry_initializing"
	case strings.Contains(errText, "cluster_consensus_unavailable"),
		strings.Contains(errText, "guest_identity_release_failed"):
		return http.StatusServiceUnavailable, "cluster_consensus_unavailable"
	case strings.Contains(errText, "guest_identity_inventory_unavailable"):
		return http.StatusServiceUnavailable, "guest_identity_inventory_unavailable"
	case strings.Contains(errText, "guest_identity_inventory_scan_failed"):
		return http.StatusInternalServerError, "guest_identity_inventory_scan_failed"
	case strings.Contains(errText, "guest_identity_inventory_conflict"):
		return http.StatusConflict, "guest_identity_inventory_conflict"
	case strings.Contains(errText, "guest_identity_claim_conflict"):
		return http.StatusConflict, "guest_identity_claim_conflict"
	case strings.Contains(errText, "guest_id_already_in_use"):
		return http.StatusConflict, "guest_id_already_in_use"
	case strings.Contains(errText, "bootstrap_mountpoint_not_usable"):
		return http.StatusConflict, "bootstrap_mountpoint_not_usable"
	case strings.Contains(errText, "bootstrap_record_mismatch"):
		return http.StatusConflict, "bootstrap_record_mismatch"
	case strings.Contains(errText, "jail_dataset_mountpoint_not_usable"):
		return http.StatusConflict, "jail_dataset_mountpoint_not_usable"
	}
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
		"failed_to_get_usable_pools",
		"failed_to_determine_host_freebsd_version",
		"failed_to_get_jails_path",
		"failed_to_get_host_memory",
		"host_cpu_unavailable",
		"jails_path_not_found",
		"system_service_not_initialized",
		"zfs_client_not_initialized":
		return http.StatusServiceUnavailable, "jail_create_dependency_not_ready"
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

func isJailNotFoundError(err error) bool {
	return err != nil && (errors.Is(err, gorm.ErrRecordNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "jail_not_found"))
}

func parseJailCTID(c *gin.Context, parameter string) (uint, bool) {
	raw := strings.TrimSpace(c.Param(parameter))
	ctID, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || ctID == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_ctid",
			Data:    nil,
			Error:   "Jail CTID must be a positive integer",
		})
		return 0, false
	}

	return uint(ctID), true
}

func parseRequiredJailBoolQuery(c *gin.Context, name string) (bool, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "missing_" + name + "_param",
			Data:    nil,
			Error:   "missing '" + name + "' query parameter",
		})
		return false, false
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_" + name + "_param",
			Data:    nil,
			Error:   "invalid '" + name + "' value: " + err.Error(),
		})
		return false, false
	}

	return value, true
}

func classifyUpdateJailDescriptionError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_update_jail_description"
	}

	errText := strings.ToLower(err.Error())
	switch {
	case isJailNotFoundError(err):
		return http.StatusNotFound, "jail_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "invalid_ct_id"),
		strings.Contains(errText, "invalid_description"):
		return http.StatusBadRequest, extractCreateJailErrorCode(errText)
	case strings.Contains(errText, "replication_lease_check_failed"):
		return http.StatusInternalServerError, "replication_lease_check_failed"
	case strings.Contains(errText, "jail_dataset_mountpoint_not_usable"):
		return http.StatusConflict, "jail_dataset_mountpoint_not_usable"
	case strings.Contains(errText, "failed_to_sync_jail_metadata"):
		return http.StatusInternalServerError, "failed_to_sync_jail_metadata"
	default:
		return http.StatusInternalServerError, "failed_to_update_jail_description"
	}
}

func classifyUpdateJailNameError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_update_jail_name"
	}

	errText := strings.ToLower(err.Error())

	switch {
	case isJailNotFoundError(err):
		return http.StatusNotFound, "jail_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "jail_dataset_mountpoint_not_usable"):
		return http.StatusConflict, "jail_dataset_mountpoint_not_usable"
	}

	code := extractCreateJailErrorCode(errText)
	switch code {
	case "invalid_ct_id", "invalid_vm_name":
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

func classifyDeleteJailError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_delete_jail"
	}

	errText := strings.ToLower(err.Error())
	switch {
	case isJailNotFoundError(err):
		return http.StatusNotFound, "jail_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "guest_identity_registry_initializing"),
		strings.Contains(errText, "guest_identity_cluster_formation_in_progress"):
		return http.StatusServiceUnavailable, "guest_identity_registry_initializing"
	case strings.Contains(errText, "cluster_consensus_unavailable"),
		strings.Contains(errText, "guest_identity_release_pending"):
		return http.StatusServiceUnavailable, "cluster_consensus_unavailable"
	case strings.Contains(errText, "guest_identity_claim_conflict"):
		return http.StatusConflict, "guest_identity_claim_conflict"
	case strings.Contains(errText, "guest_delete_requires_replication_policy_removed"):
		return http.StatusConflict, "guest_delete_requires_replication_policy_removed"
	case strings.Contains(errText, "replication_storage_topology_change_requires_policy_disabled"):
		return http.StatusConflict, "replication_storage_topology_change_requires_policy_disabled"
	case strings.Contains(errText, "jail_failed_to_stop_before_delete"),
		strings.Contains(errText, "jail_became_active_before_delete"):
		return http.StatusConflict, "jail_delete_state_conflict"
	case strings.Contains(errText, "jail_delete_runtime_not_initialized"),
		strings.Contains(errText, "jail_service_not_initialized"),
		strings.Contains(errText, "zfs_client_not_initialized"):
		return http.StatusServiceUnavailable, "jail_delete_dependency_not_ready"
	default:
		return http.StatusInternalServerError, "failed_to_delete_jail"
	}
}

// @Summary List Jails
// @Description Retrieve all configured jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailModels.Jail] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail [get]
func ListJails(jailService jailListService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJails()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_jails",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailModels.Jail]{
			Status:  "success",
			Message: "jail_listed",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary Get a Jail
// @Description Retrieve a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Success 200 {object} internal.APIResponse[jailModels.Jail] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid} [get]
func GetJailByCTID(jailService jailDetailService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		jail, err := jailService.GetJailByCTID(ctID)
		if err != nil {
			if isJailNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "jail_not_found",
					Data:    nil,
					Error:   "jail_not_found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		if jail == nil || jail.ID == 0 {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "jail_not_found",
				Data:    nil,
				Error:   "jail_not_found",
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailModels.Jail]{
			Status:  "success",
			Message: "jail_retrieved",
			Data:    *jail,
			Error:   "",
		})
	}
}

// @Summary List Jails (Simple)
// @Description Retrieve the lightweight projection of all configured jails
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.SimpleList] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/simple [get]
func ListJailsSimple(jailService jailSimpleListService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jails, err := jailService.GetJailsSimple()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_jails_simple",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "jail_listed_simple",
			Data:    jails,
			Error:   "",
		})
	}
}

// @Summary Get a Jail (Simple)
// @Description Retrieve the lightweight projection of a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.SimpleList] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/simple/{ctid} [get]
func GetSimpleJailByCTID(jailService jailSimpleDetailService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		jail, err := jailService.GetSimpleJailByCTID(ctID)
		if err != nil {
			if isJailNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "jail_not_found",
					Data:    nil,
					Error:   "jail_not_found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_jail_simple",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		if jail.ID == 0 {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "jail_not_found",
				Data:    nil,
				Error:   "jail_not_found",
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "jail_retrieved_simple",
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
// @Success 201 {object} internal.APIResponse[JailCreateResponse] "Created"
// @Header 201 {string} Location "/api/jail/{ctid}"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail [post]
func CreateJail(jailService jailCreateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.CreateJailRequest

		if !bindJailJSON(c, &req, "invalid_request_data") {
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

		created := JailCreateResponse{
			CTID: *req.CTID,
			Name: req.Name,
		}
		c.Header("Location", "/api/jail/"+strconv.FormatUint(uint64(created.CTID), 10))
		c.JSON(http.StatusCreated, internal.APIResponse[JailCreateResponse]{
			Status:  "success",
			Message: "jail_created",
			Data:    created,
			Error:   "",
		})
	}
}

// @Summary Delete a Jail
// @Description Delete a jail by its CTID, optionally deleting unused MAC objects and its root filesystem
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param deletemacs query bool true "Delete unused jail MAC objects"
// @Param deleterootfs query bool true "Delete the jail root filesystem"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.DeleteJailResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid} [delete]
func DeleteJail(jailService jailDeletionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		deleteMacs, ok := parseRequiredJailBoolQuery(c, "deletemacs")
		if !ok {
			return
		}

		deleteRootFS, ok := parseRequiredJailBoolQuery(c, "deleterootfs")
		if !ok {
			return
		}

		allowed, leaseErr := jailService.CanMutateProtectedJail(ctID)
		if leaseErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_check_failed",
				Data:    nil,
				Error:   leaseErr.Error(),
			})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_not_owned",
				Data:    nil,
				Error:   "replication_lease_not_owned",
			})
			return
		}

		ctx := c.Request.Context()
		result, err := jailService.DeleteJailWithWarnings(ctx, ctID, deleteMacs, deleteRootFS)

		if err != nil {
			statusCode, errorCode := classifyDeleteJailError(err)
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		message := "jail_deleted"
		if len(result.Warnings) > 0 {
			message = "jail_deleted_with_warnings"
		}
		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.DeleteJailResult]{
			Status:  "success",
			Message: message,
			Data:    result,
			Error:   "",
		})
	}
}

// @Summary Edit a Jail's description
// @Description Update the description of a jail by its CTID; an empty description clears it
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body JailEditDescRequest true "Edit Jail Description Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/description [patch]
func UpdateJailDescription(jailService jailDescriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		var req JailEditDescRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		err := jailService.UpdateDescription(ctID, *req.Description)
		if err != nil {
			statusCode, errorCode := classifyUpdateJailDescriptionError(err)
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_description_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Edit a Jail's name
// @Description Update the name of a jail by its CTID
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body JailEditNameRequest true "Edit Jail Name Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/name [patch]
func UpdateJailName(jailService jailNameService, clusterService *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		var req JailEditNameRequest
		if !bindJailJSON(c, &req, "invalid_request_data") {
			return
		}

		err := jailService.UpdateName(ctID, req.Name)
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

		if clusterService != nil {
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
