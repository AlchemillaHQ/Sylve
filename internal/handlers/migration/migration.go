// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	migrationService "github.com/alchemillahq/sylve/internal/services/migration"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MigrateGuestRequest struct {
	TargetNodeUUID string `json:"targetNodeUuid"`
}

type MigrationTaskResponse struct {
	TaskID  uint   `json:"taskId"`
	GuestID uint   `json:"guestId"`
	Outcome string `json:"outcome"`
}

type migrationLifecycleRequestService interface {
	RequestActionWithPayload(
		ctx context.Context,
		guestType string,
		guestID uint,
		action string,
		source string,
		requestedBy string,
		payload string,
	) (*taskModels.GuestLifecycleTask, string, error)
}

type migrateGuestOptions struct {
	paramName, idName, invalidFormat    string
	guestType, auditType, queuedMessage string
}

// @Summary Queue a Virtual Machine migration
// @Description Validate and queue an asynchronous migration of a virtual machine to another cluster node
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body MigrateGuestRequest true "Migration target"
// @Success 202 {object} internal.APIResponse[MigrationTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/migrations [post]
func MigrateVM(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService migrationLifecycleRequestService,
) gin.HandlerFunc {
	return migrateGuest(migrationService, lifecycleService, migrateGuestOptions{
		paramName: "rid", idName: "Virtual Machine ID", invalidFormat: "invalid_rid_format",
		guestType: taskModels.GuestTypeVM, auditType: "vm_migrate", queuedMessage: "vm_migration_queued",
	})
}

// @Summary Queue a Jail migration
// @Description Validate and queue an asynchronous migration of a jail to another cluster node
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body MigrateGuestRequest true "Migration target"
// @Success 202 {object} internal.APIResponse[MigrationTaskResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/migrations [post]
func MigrateJail(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService migrationLifecycleRequestService,
) gin.HandlerFunc {
	return migrateGuest(migrationService, lifecycleService, migrateGuestOptions{
		paramName: "ctid", idName: "Jail CTID", invalidFormat: "invalid_ctid_format",
		guestType: taskModels.GuestTypeJail, auditType: "jail_migrate", queuedMessage: "jail_migration_queued",
	})
}

func migrateGuest(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService migrationLifecycleRequestService,
	options migrateGuestOptions,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param(options.paramName)
		if id == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status: "error", Message: "invalid_request", Error: options.idName + " is required",
			})
			return
		}

		guestID, err := parseMigrationGuestID(id)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: options.invalidFormat, Error: options.idName + " must be a positive integer",
			})
			return
		}

		var req MigrateGuestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			status := http.StatusBadRequest
			message := "invalid_request_body"
			detail := err.Error()
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				status = http.StatusRequestEntityTooLarge
				message = "request_body_too_large"
				detail = "request_body_too_large"
			}
			c.JSON(status, internal.APIResponse[any]{
				Status: "error", Message: message, Error: detail, Data: nil,
			})
			return
		}

		req.TargetNodeUUID = strings.TrimSpace(req.TargetNodeUUID)
		if req.TargetNodeUUID == "" {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "target_node_uuid_required", Error: "Target node UUID is required"})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))

		validation, err := migrationService.ValidateMigration(c.Request.Context(), migrationIface.MigrateRequest{
			GuestType:      options.guestType,
			GuestID:        guestID,
			TargetNodeUUID: req.TargetNodeUUID,
		})
		if err != nil {
			status, message := classifyMigrationValidationError(err)
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
			return
		}
		if validation == nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "migration_validation_failed", Error: "Migration validation returned no result",
			})
			return
		}
		if !validation.Allowed {
			status, message := classifyMigrationValidationResult(validation, options.guestType)
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   strings.Join(validation.Reasons, "; "),
				Data:    validation,
			})
			return
		}

		payload, err := json.Marshal(req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "migration_payload_encode_failed", Error: err.Error(),
			})
			return
		}

		task, outcome, err := lifecycleService.RequestActionWithPayload(
			c.Request.Context(),
			options.guestType,
			guestID,
			"migrate",
			taskModels.LifecycleTaskSourceUser,
			username,
			string(payload),
		)
		if err != nil {
			status, message := classifyMigrationRequestError(err)
			c.JSON(status, internal.APIResponse[any]{
				Status: "error", Message: message, Error: err.Error(),
			})
			return
		}
		if task == nil || task.ID == 0 {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "migration_request_failed", Error: "Lifecycle service returned no migration task",
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", options.auditType)

		c.JSON(http.StatusAccepted, internal.APIResponse[MigrationTaskResponse]{
			Status:  "success",
			Message: options.queuedMessage,
			Data: MigrationTaskResponse{
				TaskID: task.ID, GuestID: task.GuestID, Outcome: outcome,
			},
			Error: "",
		})
	}
}

func parseMigrationGuestID(id string) (uint, error) {
	value, err := strconv.ParseUint(id, 10, 0)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("invalid_guest_id")
	}
	return uint(value), nil
}

func migrationReasonCode(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if index := strings.Index(reason, ":"); index >= 0 {
		reason = reason[:index]
	}
	return strings.TrimSpace(reason)
}

func classifyMigrationValidationError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "migration_validation_failed"
	}

	errorText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errorText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case errors.Is(err, migrationService.ErrTargetNodeOffline),
		errors.Is(err, migrationService.ErrSSHUnreachable),
		strings.Contains(errorText, "target_ssh_identity_unavailable"),
		strings.Contains(errorText, "cluster_ssh_key_unavailable"):
		return http.StatusServiceUnavailable, "migration_target_unavailable"
	default:
		return http.StatusInternalServerError, "migration_validation_failed"
	}
}

func classifyMigrationValidationResult(
	result *migrationIface.ValidateResult,
	guestType string,
) (int, string) {
	if result == nil {
		return http.StatusInternalServerError, "migration_validation_failed"
	}

	status := http.StatusConflict
	message := "migration_conflict"
	for _, reason := range result.Reasons {
		code := migrationReasonCode(reason)
		switch {
		case code == "replication_policy_lookup_failed",
			code == "active_task_lookup_failed",
			code == "replication_event_lookup_failed",
			code == "jail_lookup_failed",
			code == "jail_storage_lookup_failed",
			code == "jail_network_lookup_failed",
			(strings.HasPrefix(code, "network_") && strings.HasSuffix(code, "_lookup_failed")):
			return http.StatusInternalServerError, "migration_validation_failed"
		case code == "target_node_offline",
			code == "local_node_id_unavailable",
			code == "target_ssh_identity_unavailable",
			code == "cluster_ssh_key_unavailable",
			code == "target_check_failed",
			code == "target_guest_record_check_failed",
			strings.HasPrefix(code, "target_pool_check_failed_"),
			strings.HasPrefix(code, "target_guest_check_failed_"),
			(strings.HasPrefix(code, "network_") && strings.Contains(code, "_bridge_check_failed_")),
			strings.HasPrefix(code, "target_identity_inventory_"):
			status = http.StatusServiceUnavailable
			message = "migration_target_unavailable"
		case code == "replication_lease_not_owned", code == "standby_mode_edit_not_allowed":
			if status != http.StatusServiceUnavailable {
				status = http.StatusForbidden
				message = "replication_lease_not_owned"
			}
		case code == "vm_not_found", code == "jail_not_found":
			if status != http.StatusServiceUnavailable && status != http.StatusForbidden {
				status = http.StatusNotFound
				if strings.EqualFold(guestType, taskModels.GuestTypeVM) {
					message = "vm_not_found"
				} else {
					message = "jail_not_found"
				}
			}
		case code == "target_is_source_node",
			code == "target_node_is_source",
			code == "target_node_not_found",
			code == "unsupported_guest_type",
			code == "invalid_guest_identity":
			if status == http.StatusConflict {
				status = http.StatusBadRequest
				message = "migration_not_allowed"
			}
		}
	}

	return status, message
}

func classifyMigrationRequestError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "migration_request_failed"
	}

	switch {
	case errors.Is(err, lifecycle.ErrMigrationActive):
		return http.StatusConflict, "migration_in_progress"
	case errors.Is(err, lifecycle.ErrTaskInProgress):
		return http.StatusConflict, "lifecycle_task_in_progress"
	case errors.Is(err, lifecycle.ErrInvalidAction), errors.Is(err, lifecycle.ErrInvalidGuest),
		strings.Contains(strings.ToLower(err.Error()), "invalid_guest_id"):
		return http.StatusBadRequest, "invalid_migration_request"
	default:
		return http.StatusInternalServerError, "migration_request_failed"
	}
}

// @Summary Request migration cancellation
// @Description Request cancellation of a queued or pre-cutover guest migration
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "Migration task ID" minimum(1)
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /tasks/migration/{taskId}/cancel [post]
func CancelMigration(migrationService migrationIface.MigrationServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskIDStr := c.Param("taskId")
		taskID, err := strconv.ParseUint(strings.TrimSpace(taskIDStr), 10, strconv.IntSize)
		if err != nil || taskID == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status: "error", Message: "invalid_task_id", Error: "Migration task ID must be a positive integer",
			})
			return
		}

		if err := migrationService.CancelMigration(c.Request.Context(), uint(taskID)); err != nil {
			status, message := classifyCancelMigrationError(err)
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Uint64("task_id", taskID).Msg("cancel_migration_failed")
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: message})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "migration_cancellation_requested",
		})
	}
}

func classifyCancelMigrationError(err error) (int, string) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound, "migration_task_not_found"
	case errors.Is(err, migrationService.ErrNotMigrationTask):
		return http.StatusBadRequest, "not_a_migration_task"
	case errors.Is(err, migrationService.ErrCancelNotAllowed):
		return http.StatusConflict, "cancel_not_allowed_in_current_phase"
	default:
		return http.StatusInternalServerError, "cancel_migration_failed"
	}
}

// @Summary Validate a guest migration
// @Description Run a side-effect-free migration preflight for one VM or jail and target node UUID
// @Tags Tasks
// @Produce json
// @Security BearerAuth
// @Param guestType query string true "Guest type" Enums(vm,jail)
// @Param guestId query int true "Positive guest ID" minimum(1)
// @Param targetNodeUuid query string true "Target cluster node UUID"
// @Success 200 {object} internal.APIResponse[migrationIface.ValidateResult] "Validation complete"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /tasks/migration/validate [get]
func ValidateMigration(migrationService migrationIface.MigrationServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType := strings.ToLower(strings.TrimSpace(c.Query("guestType")))
		guestIDStr := strings.TrimSpace(c.Query("guestId"))
		targetNodeUUID := strings.TrimSpace(c.Query("targetNodeUuid"))

		if guestType == "" || guestIDStr == "" || targetNodeUUID == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: "guestType, guestId, and targetNodeUuid query params are required"})
			return
		}
		if guestType != taskModels.GuestTypeVM && guestType != taskModels.GuestTypeJail {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_guest_type", Error: "guestType must be vm or jail"})
			return
		}

		guestID, err := parseMigrationGuestID(guestIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_guest_id", Error: "guestId must be a positive integer"})
			return
		}

		result, err := migrationService.ValidateMigration(c.Request.Context(), migrationIface.MigrateRequest{
			GuestType:      guestType,
			GuestID:        guestID,
			TargetNodeUUID: targetNodeUUID,
		})
		if err != nil {
			status, message := classifyMigrationValidationError(err)
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Uint("guest_id", guestID).Str("guest_type", guestType).Msg("migration_validation_failed")
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: message})
			return
		}
		if result == nil {
			logger.L.Error().Uint("guest_id", guestID).Str("guest_type", guestType).Msg("migration_validation_returned_no_result")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: "migration_validation_failed", Error: "migration_validation_failed"})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[migrationIface.ValidateResult]{Status: "success", Message: "validation_complete", Data: *result})
	}
}
