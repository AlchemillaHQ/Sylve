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
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	migrationService "github.com/alchemillahq/sylve/internal/services/migration"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	goLibvirt "github.com/digitalocean/go-libvirt"
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
			code == "target_check_unsupported",
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

type targetMigrationImportRequest struct {
	GuestID            uint     `json:"guestId"`
	OperationToken     string   `json:"operationToken"`
	StartGuest         *bool    `json:"startGuest"`
	SourceDatasetRoots []string `json:"sourceDatasetRoots"`
}

type targetMigrationRuntimeState string

const (
	targetMigrationRuntimeInactive targetMigrationRuntimeState = "inactive"
	targetMigrationRuntimeActive   targetMigrationRuntimeState = "active"
	targetMigrationRuntimeUnsafe   targetMigrationRuntimeState = "unsafe"
)

type targetMigrationImportOperations struct {
	GuestType              string
	UnavailableReason      string
	ImportedMessage        string
	ImportedStoppedMessage string
	AlreadyActiveMessage   string
	Authorize              func(context.Context, uint, string) error
	ValidateRoots          func(context.Context, uint, []string) ([]string, error)
	RuntimeState           func(uint) (targetMigrationRuntimeState, error)
	Import                 func(context.Context, uint, []string) ([]string, error)
	SetIntentionalStop     func(uint, bool) error
	Start                  func(context.Context, uint) error
}

type targetMigrationGuestLockEntry struct {
	mu   sync.Mutex
	refs uint
}

type targetMigrationGuestLockSet struct {
	mu    sync.Mutex
	locks map[uint]*targetMigrationGuestLockEntry
}

func (s *targetMigrationGuestLockSet) acquire(guestID uint) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[uint]*targetMigrationGuestLockEntry)
	}
	entry := s.locks[guestID]
	if entry == nil {
		entry = &targetMigrationGuestLockEntry{}
		s.locks[guestID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.locks[guestID] == entry {
			delete(s.locks, guestID)
		}
		s.mu.Unlock()
	}
}

func requireExactMigrationTargetCutover(
	ctx context.Context,
	db *gorm.DB,
	localNodeID string,
	guestType string,
	guestID uint,
	operationToken string,
) error {
	localNodeID = strings.TrimSpace(localNodeID)
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	operationToken = strings.TrimSpace(operationToken)
	if db == nil {
		return fmt.Errorf("migration_target_guard_database_unavailable")
	}
	if localNodeID == "" {
		return fmt.Errorf("migration_target_node_id_unavailable")
	}
	if guestID == 0 || operationToken == "" ||
		(guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail) {
		return fmt.Errorf("migration_target_guard_input_invalid")
	}

	var operation clusterModels.ReplicationGuestOperation
	result := db.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", guestType, guestID).
		Limit(1).
		Find(&operation)
	if result.Error != nil {
		return fmt.Errorf("migration_target_guard_lookup_failed: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("migration_target_cutover_guard_not_applied")
	}
	if operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
		operation.State != clusterModels.ReplicationGuestOperationCutover ||
		strings.TrimSpace(operation.Token) != operationToken ||
		strings.TrimSpace(operation.TargetNodeID) != localNodeID ||
		strings.TrimSpace(operation.OwnerNodeID) == "" || operation.TaskID == 0 {
		return fmt.Errorf("migration_target_cutover_guard_mismatch")
	}
	return nil
}

func targetMigrationImportHandler(ops targetMigrationImportOperations) gin.HandlerFunc {
	var guestLocks targetMigrationGuestLockSet

	return func(c *gin.Context) {
		var req targetMigrationImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
			return
		}
		req.OperationToken = strings.TrimSpace(req.OperationToken)
		if req.GuestID == 0 || req.OperationToken == "" || req.StartGuest == nil || len(req.SourceDatasetRoots) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error", "message": "guest_id_operation_token_start_state_and_dataset_roots_required",
			})
			return
		}
		if ops.UnavailableReason != "" || ops.Authorize == nil || ops.ValidateRoots == nil ||
			ops.RuntimeState == nil || ops.Import == nil || ops.SetIntentionalStop == nil || ops.Start == nil {
			reason := strings.TrimSpace(ops.UnavailableReason)
			if reason == "" {
				reason = "migration_target_import_not_configured"
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": reason})
			return
		}

		releaseGuestLock := guestLocks.acquire(req.GuestID)
		defer releaseGuestLock()

		if err := ops.Authorize(c.Request.Context(), req.GuestID, req.OperationToken); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"status": "error", "message": "migration_target_cutover_guard_rejected", "error": err.Error(),
			})
			return
		}
		roots, err := ops.ValidateRoots(c.Request.Context(), req.GuestID, req.SourceDatasetRoots)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"status": "error", "message": "migration_target_dataset_manifest_rejected", "error": err.Error(),
			})
			return
		}

		runtimeState, err := ops.RuntimeState(req.GuestID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "message": "migration_target_active_check_failed", "error": err.Error(),
			})
			return
		}
		if runtimeState == targetMigrationRuntimeUnsafe {
			c.JSON(http.StatusConflict, gin.H{
				"status": "error", "message": "migration_target_runtime_state_not_safe_for_import",
			})
			return
		}
		if runtimeState == targetMigrationRuntimeActive {
			if !*req.StartGuest {
				c.JSON(http.StatusConflict, gin.H{
					"status": "error", "message": "migration_target_runtime_state_not_safe_for_import",
				})
				return
			}
			c.JSON(http.StatusOK, targetMigrationSuccessReceipt(req, roots, ops.AlreadyActiveMessage, nil))
			return
		}

		warnings, err := ops.Import(c.Request.Context(), req.GuestID, roots)
		if err != nil {
			logger.L.Warn().Err(err).
				Str("guest_type", ops.GuestType).
				Uint("guest_id", req.GuestID).
				Msg("intra_cluster_guest_import_failed")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "message": err.Error(), "warnings": warnings,
			})
			return
		}

		// Import can take long enough for local Raft state to advance. Re-read
		// the exact token-scoped guard immediately before starting the runtime;
		// a successful import must not become authority to start later.
		if err := ops.Authorize(c.Request.Context(), req.GuestID, req.OperationToken); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"status": "error", "message": "migration_target_cutover_guard_rejected", "error": err.Error(),
				"warnings": warnings,
			})
			return
		}
		if err := ops.SetIntentionalStop(req.GuestID, !*req.StartGuest); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "message": "migration_target_runtime_intent_update_failed", "error": err.Error(),
				"warnings": warnings,
			})
			return
		}
		if !*req.StartGuest {
			stateAfterImport, stateErr := ops.RuntimeState(req.GuestID)
			if stateErr != nil || stateAfterImport != targetMigrationRuntimeInactive {
				message := "migration_target_stopped_state_unverified"
				if stateErr != nil {
					message = fmt.Sprintf("%s: %v", message, stateErr)
				}
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error", "message": message, "warnings": warnings,
				})
				return
			}
			c.JSON(http.StatusOK, targetMigrationSuccessReceipt(req, roots, ops.ImportedStoppedMessage, warnings))
			return
		}
		if err := ops.Authorize(c.Request.Context(), req.GuestID, req.OperationToken); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"status": "error", "message": "migration_target_cutover_guard_rejected", "error": err.Error(),
				"warnings": warnings,
			})
			return
		}

		if err := ops.Start(c.Request.Context(), req.GuestID); err != nil {
			// Starting a guest and delivering the HTTP response are not atomic.
			// If the exact guest is active under the same still-applied cutover
			// guard, a retry is complete rather than a second destructive import.
			stateAfterError, activeErr := ops.RuntimeState(req.GuestID)
			if activeErr == nil && stateAfterError == targetMigrationRuntimeActive &&
				ops.Authorize(c.Request.Context(), req.GuestID, req.OperationToken) == nil {
				c.JSON(http.StatusOK, targetMigrationSuccessReceipt(req, roots, ops.AlreadyActiveMessage, warnings))
				return
			}
			logger.L.Warn().Err(err).
				Str("guest_type", ops.GuestType).
				Uint("guest_id", req.GuestID).
				Msg("intra_cluster_guest_start_failed")
			message := err.Error()
			if activeErr != nil {
				message = fmt.Sprintf("%s; active_recheck_failed: %v", message, activeErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "message": message, "warnings": warnings,
			})
			return
		}

		stateAfterStart, stateErr := ops.RuntimeState(req.GuestID)
		if stateErr != nil || stateAfterStart != targetMigrationRuntimeActive {
			message := "migration_target_start_unverified"
			if stateErr != nil {
				message = fmt.Sprintf("%s: %v", message, stateErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "message": message, "warnings": warnings,
			})
			return
		}

		c.JSON(http.StatusOK, targetMigrationSuccessReceipt(req, roots, ops.ImportedMessage, warnings))
	}
}

func targetMigrationSuccessReceipt(
	req targetMigrationImportRequest,
	roots []string,
	message string,
	warnings []string,
) gin.H {
	if warnings == nil {
		warnings = []string{}
	}
	return gin.H{
		"status":             "success",
		"message":            message,
		"warnings":           warnings,
		"guestId":            req.GuestID,
		"operationToken":     req.OperationToken,
		"startGuest":         req.StartGuest,
		"sourceDatasetRoots": append([]string(nil), roots...),
	}
}

func migratedVMRuntimeState(libvirtService *libvirt.Service, guestID uint) (targetMigrationRuntimeState, error) {
	state, err := libvirtService.GetDomainState(int(guestID))
	if err != nil {
		if libvirtServiceInterfaces.IsDomainNotFoundError(err) {
			return targetMigrationRuntimeInactive, nil
		}
		return targetMigrationRuntimeUnsafe, err
	}
	return classifyMigratedVMRuntimeState(state), nil
}

func classifyMigratedVMRuntimeState(state goLibvirt.DomainState) targetMigrationRuntimeState {
	switch state {
	case goLibvirt.DomainRunning:
		return targetMigrationRuntimeActive
	case goLibvirt.DomainShutoff:
		return targetMigrationRuntimeInactive
	default:
		return targetMigrationRuntimeUnsafe
	}
}

func IntraClusterImportVM(
	zeltaService *zelta.Service,
	libvirtService *libvirt.Service,
) gin.HandlerFunc {
	ops := targetMigrationImportOperations{
		GuestType:              clusterModels.ReplicationGuestTypeVM,
		ImportedMessage:        "vm_imported_and_started",
		ImportedStoppedMessage: "vm_imported_and_left_stopped",
		AlreadyActiveMessage:   "vm_already_imported_and_active",
	}
	if zeltaService == nil {
		ops.UnavailableReason = "zelta_not_configured"
	} else if zeltaService.Cluster == nil {
		ops.UnavailableReason = "cluster_not_configured"
	} else {
		ops.Authorize = func(ctx context.Context, guestID uint, operationToken string) error {
			return requireExactMigrationTargetCutover(
				ctx,
				zeltaService.DB,
				zeltaService.Cluster.LocalNodeID(),
				clusterModels.ReplicationGuestTypeVM,
				guestID,
				operationToken,
			)
		}
		ops.ValidateRoots = zeltaService.ValidateMigratedVMRoots
		ops.Import = zeltaService.ImportMigratedVMWithRoots
	}
	if libvirtService == nil {
		if ops.UnavailableReason == "" {
			ops.UnavailableReason = "libvirt_not_configured"
		}
	} else {
		ops.RuntimeState = func(guestID uint) (targetMigrationRuntimeState, error) {
			return migratedVMRuntimeState(libvirtService, guestID)
		}
		ops.SetIntentionalStop = func(guestID uint, stopped bool) error {
			result := libvirtService.DB.Model(&vmModels.VM{}).
				Where("rid = ?", guestID).
				Update("intentionally_stopped", stopped)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("migrated_vm_record_not_found")
			}
			return libvirtService.WriteVMJson(guestID)
		}
		ops.Start = func(ctx context.Context, guestID uint) error {
			return libvirtService.PerformActionContext(ctx, guestID, "start")
		}
	}
	return targetMigrationImportHandler(ops)
}

type CheckVMTargetSwitch struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Bridge string `json:"bridge"`
}

type CheckVMTargetRequest struct {
	RID        uint                  `json:"rid"`
	MediaUUIDs []string              `json:"mediaUuids"`
	VNCPort    int                   `json:"vncPort"`
	Switches   []CheckVMTargetSwitch `json:"switches"`
	FsDatasets []string              `json:"fsDatasets"`
}

func IntraClusterCheckVMTarget(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CheckVMTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_request_body", Error: err.Error()})
			return
		}

		if libvirtService == nil {
			c.JSON(500, internal.APIResponse[any]{Status: "error", Message: "libvirt_not_configured", Error: "libvirt_not_configured"})
			return
		}

		ctx := c.Request.Context()
		db := libvirtService.DB

		missingMedia := make([]string, 0, len(req.MediaUUIDs))
		seenMedia := make(map[string]struct{}, len(req.MediaUUIDs))
		for _, uuid := range req.MediaUUIDs {
			uuid = strings.TrimSpace(uuid)
			if uuid == "" {
				continue
			}
			if _, ok := seenMedia[uuid]; ok {
				continue
			}
			seenMedia[uuid] = struct{}{}

			if _, err := libvirtService.FindISOByUUID(uuid, true); err != nil {
				missingMedia = append(missingMedia, uuid)
			}
		}

		missingSwitches := make([]string, 0, len(req.Switches))
		if db != nil {
			for _, sw := range req.Switches {
				name := strings.TrimSpace(sw.Name)
				bridge := strings.TrimSpace(sw.Bridge)
				if name == "" && bridge == "" {
					continue
				}

				found := false
				if strings.EqualFold(strings.TrimSpace(sw.Type), "manual") {
					var m networkModels.ManualSwitch
					if name != "" && db.Where("name = ?", name).First(&m).Error == nil {
						found = true
					}
					if !found && bridge != "" && db.Where("bridge = ?", bridge).First(&m).Error == nil {
						found = true
					}
				} else {
					var st networkModels.StandardSwitch
					if name != "" && db.Where("name = ?", name).First(&st).Error == nil {
						found = true
					}
					if !found && bridge != "" && db.Where("bridge_name = ?", bridge).First(&st).Error == nil {
						found = true
					}
				}

				if !found {
					label := name
					if label == "" {
						label = bridge
					}
					missingSwitches = append(missingSwitches, label)
				}
			}
		}

		missingFsDatasets := make([]string, 0, len(req.FsDatasets))
		if libvirtService.GZFS != nil && libvirtService.GZFS.ZFS != nil {
			for _, ds := range req.FsDatasets {
				ds = strings.TrimSpace(ds)
				if ds == "" {
					continue
				}
				datasets, err := libvirtService.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false, ds)
				if err != nil || len(datasets) == 0 {
					missingFsDatasets = append(missingFsDatasets, ds)
				}
			}
		}

		vncPortInUse := false
		if db != nil && req.VNCPort > 0 {
			var count int64
			if err := db.Model(&vmModels.VM{}).
				Where("vnc_port = ? AND rid <> ?", req.VNCPort, req.RID).
				Count(&count).Error; err == nil && count > 0 {
				vncPortInUse = true
			}
			if utils.IsTCPPortInUse(req.VNCPort) {
				vncPortInUse = true
			}
		}

		c.JSON(http.StatusOK, internal.APIResponse[map[string]any]{
			Status:  "success",
			Message: "vm_target_check_complete",
			Data: map[string]any{
				"missingMedia":      missingMedia,
				"vncPortInUse":      vncPortInUse,
				"missingSwitches":   missingSwitches,
				"missingFsDatasets": missingFsDatasets,
			},
		})
	}
}

func IntraClusterImportJail(
	zeltaService *zelta.Service,
	jailService *jail.Service,
) gin.HandlerFunc {
	ops := targetMigrationImportOperations{
		GuestType:              clusterModels.ReplicationGuestTypeJail,
		ImportedMessage:        "jail_imported_and_started",
		ImportedStoppedMessage: "jail_imported_and_left_stopped",
		AlreadyActiveMessage:   "jail_already_imported_and_active",
	}
	if zeltaService == nil {
		ops.UnavailableReason = "zelta_not_configured"
	} else if zeltaService.Cluster == nil {
		ops.UnavailableReason = "cluster_not_configured"
	} else {
		ops.Authorize = func(ctx context.Context, guestID uint, operationToken string) error {
			return requireExactMigrationTargetCutover(
				ctx,
				zeltaService.DB,
				zeltaService.Cluster.LocalNodeID(),
				clusterModels.ReplicationGuestTypeJail,
				guestID,
				operationToken,
			)
		}
		ops.ValidateRoots = zeltaService.ValidateMigratedJailRoots
		ops.Import = zeltaService.ImportMigratedJailWithRoots
	}
	if jailService == nil {
		if ops.UnavailableReason == "" {
			ops.UnavailableReason = "jail_not_configured"
		}
	} else {
		ops.RuntimeState = func(guestID uint) (targetMigrationRuntimeState, error) {
			active, err := jailService.IsJailRunning(guestID)
			if err != nil {
				return targetMigrationRuntimeUnsafe, err
			}
			if active {
				return targetMigrationRuntimeActive, nil
			}
			return targetMigrationRuntimeInactive, nil
		}
		ops.SetIntentionalStop = func(guestID uint, stopped bool) error {
			result := jailService.DB.Model(&jailModels.Jail{}).
				Where("ct_id = ?", guestID).
				Update("intentionally_stopped", stopped)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("migrated_jail_record_not_found")
			}
			return jailService.WriteJailJSON(guestID)
		}
		ops.Start = func(ctx context.Context, guestID uint) error {
			return jailService.JailActionContext(ctx, int(guestID), "start")
		}
	}
	return targetMigrationImportHandler(ops)
}
