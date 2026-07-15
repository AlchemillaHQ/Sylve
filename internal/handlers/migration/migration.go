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
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	goLibvirt "github.com/digitalocean/go-libvirt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MigrateGuestRequest struct {
	TargetNodeUUID string `json:"targetNodeUuid"`
}

type migrateGuestOptions struct {
	paramName, idName, invalidFormat    string
	guestType, auditType, queuedMessage string
	parseSignedID                       bool
}

func MigrateVM(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService *lifecycle.Service,
) gin.HandlerFunc {
	return migrateGuest(migrationService, lifecycleService, migrateGuestOptions{
		paramName: "rid", idName: "Virtual Machine ID", invalidFormat: "invalid_rid_format",
		guestType: taskModels.GuestTypeVM, auditType: "vm_migrate", queuedMessage: "vm_migration_queued",
	})
}

func MigrateJail(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService *lifecycle.Service,
) gin.HandlerFunc {
	return migrateGuest(migrationService, lifecycleService, migrateGuestOptions{
		paramName: "ctId", idName: "Jail CT ID", invalidFormat: "invalid_ctid_format",
		guestType: taskModels.GuestTypeJail, auditType: "jail_migrate", queuedMessage: "jail_migration_queued",
		parseSignedID: true,
	})
}

func migrateGuest(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService *lifecycle.Service,
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

		guestID, err := parseMigrationGuestID(id, options.parseSignedID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status: "error", Message: options.invalidFormat, Error: options.idName + " must be a valid integer",
			})
			return
		}

		var req MigrateGuestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_request_body", Error: err.Error()})
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
			c.JSON(500, internal.APIResponse[any]{Status: "error", Message: "validation_error", Error: err.Error()})
			return
		}
		if !validation.Allowed {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "migration_not_allowed",
				Error:   strings.Join(validation.Reasons, "; "),
				Data:    validation,
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
			fmt.Sprintf(`{"targetNodeUuid":"%s"}`, req.TargetNodeUUID),
		)
		if err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "migration_request_failed", Error: err.Error(),
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", options.auditType)

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: options.queuedMessage,
			Data:    map[string]any{"taskId": task.ID, "guestId": task.GuestID, "outcome": outcome},
		})
	}
}

func parseMigrationGuestID(id string, signed bool) (uint, error) {
	if signed {
		value, err := strconv.Atoi(id)
		return uint(value), err
	}
	value, err := strconv.ParseUint(id, 10, 0)
	return uint(value), err
}

func CancelMigration(migrationService migrationIface.MigrationServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskIDStr := c.Param("taskId")
		taskID, err := strconv.ParseUint(taskIDStr, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_task_id", Error: err.Error()})
			return
		}

		if err := migrationService.CancelMigration(c.Request.Context(), uint(taskID)); err != nil {
			status := http.StatusInternalServerError
			msg := "cancel_migration_failed"
			if strings.Contains(err.Error(), "not_allowed") || strings.Contains(err.Error(), "not_a_migration") {
				status = http.StatusBadRequest
				msg = err.Error()
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: msg, Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "migration_cancellation_requested",
		})
	}
}

func ValidateMigration(migrationService migrationIface.MigrationServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		guestType := c.Query("guestType")
		guestIDStr := c.Query("guestId")
		targetNodeUUID := c.Query("targetNodeUuid")

		if guestType == "" || guestIDStr == "" || targetNodeUUID == "" {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: "guestType, guestId, and targetNodeUuid query params are required"})
			return
		}

		guestID, err := strconv.ParseUint(guestIDStr, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_guest_id", Error: err.Error()})
			return
		}

		result, err := migrationService.ValidateMigration(c.Request.Context(), migrationIface.MigrateRequest{
			GuestType:      guestType,
			GuestID:        uint(guestID),
			TargetNodeUUID: targetNodeUUID,
		})
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Status: "error", Message: "validation_error", Error: err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[any]{Status: "success", Message: "validation_complete", Data: result})
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
	Start                  func(uint) error
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

		if err := ops.Start(req.GuestID); err != nil {
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
		ops.Start = func(guestID uint) error { return libvirtService.PerformAction(guestID, "start") }
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
			active, err := jailService.IsJailActive(guestID)
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
		ops.Start = func(guestID uint) error { return jailService.JailAction(int(guestID), "start") }
	}
	return targetMigrationImportHandler(ops)
}
