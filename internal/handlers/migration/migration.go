// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

type MigrateGuestRequest struct {
	TargetNodeUUID string `json:"targetNodeUuid"`
}

func MigrateVM(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService *lifecycle.Service,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: "Virtual Machine ID is required"})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_rid_format", Error: "Virtual Machine ID must be a valid integer"})
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
			GuestType:      taskModels.GuestTypeVM,
			GuestID:        uint(ridInt),
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
			taskModels.GuestTypeVM,
			uint(ridInt),
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
		c.Set("AuditAsyncJobType", "vm_migrate")

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_migration_queued",
			Data:    map[string]any{"taskId": task.ID, "guestId": task.GuestID, "outcome": outcome},
		})
	}
}

func MigrateJail(
	migrationService migrationIface.MigrationServiceInterface,
	lifecycleService *lifecycle.Service,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID := c.Param("ctId")
		if ctID == "" {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: "Jail CT ID is required"})
			return
		}

		ctIDInt, err := strconv.Atoi(ctID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{Status: "error", Message: "invalid_ctid_format", Error: "Jail CT ID must be a valid integer"})
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
			GuestType:      taskModels.GuestTypeJail,
			GuestID:        uint(ctIDInt),
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
			taskModels.GuestTypeJail,
			uint(ctIDInt),
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
		c.Set("AuditAsyncJobType", "jail_migrate")

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_migration_queued",
			Data:    map[string]any{"taskId": task.ID, "guestId": task.GuestID, "outcome": outcome},
		})
	}
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

		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "migration_cancelled"})
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

func IntraClusterImportVM(
	zeltaService *zelta.Service,
	libvirtService *libvirt.Service,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GuestID uint `json:"guestId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"status": "error", "message": err.Error()})
			return
		}

		if zeltaService == nil {
			c.JSON(500, gin.H{"status": "error", "message": "zelta_not_configured"})
			return
		}
		if libvirtService == nil {
			c.JSON(500, gin.H{"status": "error", "message": "libvirt_not_configured"})
			return
		}

		warnings, err := zeltaService.ImportMigratedVM(c.Request.Context(), req.GuestID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("rid", req.GuestID).Msg("intra_cluster_vm_import_failed")
			c.JSON(500, gin.H{
				"status":   "error",
				"message":  err.Error(),
				"warnings": warnings,
			})
			return
		}

		if err := libvirtService.PerformAction(req.GuestID, "start"); err != nil {
			logger.L.Warn().Err(err).Uint("rid", req.GuestID).Msg("intra_cluster_vm_start_failed")
			c.JSON(500, gin.H{
				"status":   "error",
				"message":  err.Error(),
				"warnings": warnings,
			})
			return
		}

		c.JSON(200, gin.H{
			"status":   "success",
			"message":  "vm_imported_and_started",
			"warnings": warnings,
		})
	}
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
				Where("vnc_enabled = ? AND vnc_port = ? AND rid <> ?", true, req.VNCPort, req.RID).
				Count(&count).Error; err == nil && count > 0 {
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
	return func(c *gin.Context) {
		var req struct {
			GuestID uint `json:"guestId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"status": "error", "message": err.Error()})
			return
		}

		if zeltaService == nil {
			c.JSON(500, gin.H{"status": "error", "message": "zelta_not_configured"})
			return
		}
		if jailService == nil {
			c.JSON(500, gin.H{"status": "error", "message": "jail_not_configured"})
			return
		}

		warnings, err := zeltaService.ImportMigratedJail(c.Request.Context(), req.GuestID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("ct_id", req.GuestID).Msg("intra_cluster_jail_import_failed")
			c.JSON(500, gin.H{
				"status":   "error",
				"message":  err.Error(),
				"warnings": warnings,
			})
			return
		}

		if err := jailService.JailAction(int(req.GuestID), "start"); err != nil {
			logger.L.Warn().Err(err).Uint("ct_id", req.GuestID).Msg("intra_cluster_jail_start_failed")
			c.JSON(500, gin.H{
				"status":   "error",
				"message":  err.Error(),
				"warnings": warnings,
			})
			return
		}

		c.JSON(200, gin.H{
			"status":   "success",
			"message":  "jail_imported_and_started",
			"warnings": warnings,
		})
	}
}
