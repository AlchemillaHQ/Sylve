// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
)

type ReplicationMutationGuardOperation string

const (
	ReplicationGuardDatasetGUID      ReplicationMutationGuardOperation = "dataset_guid"
	ReplicationGuardPoolGUID         ReplicationMutationGuardOperation = "pool_guid"
	ReplicationGuardBulkGUIDs        ReplicationMutationGuardOperation = "bulk_guids"
	ReplicationGuardBulkNames        ReplicationMutationGuardOperation = "bulk_names"
	ReplicationGuardCreateFilesystem ReplicationMutationGuardOperation = "create_filesystem"
	ReplicationGuardEditFilesystem   ReplicationMutationGuardOperation = "edit_filesystem"
	ReplicationGuardCreateVolume     ReplicationMutationGuardOperation = "create_volume"
	ReplicationGuardEditVolume       ReplicationMutationGuardOperation = "edit_volume"
	ReplicationGuardFlashVolume      ReplicationMutationGuardOperation = "flash_volume"
	ReplicationGuardRollbackSnapshot ReplicationMutationGuardOperation = "rollback_snapshot"
)

func decodeAndRestoreMutationBody(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return fmt.Errorf("request_body_required")
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func abortReplicationMutationGuard(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "replication_dataset_guard_failed"
	if strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
		status = http.StatusConflict
		message = "replication_protected_dataset_mutation_blocked"
	} else if strings.Contains(err.Error(), "replication_dataset_create_parent_mismatch") ||
		strings.Contains(err.Error(), "replication_dataset_guard_name_required") {
		status = http.StatusBadRequest
		message = "invalid_request"
	}
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status: "error", Message: message, Error: err.Error(), Data: nil,
	})
}

func ReplicationDatasetMutationGuard(
	zfsService *zfs.Service,
	operation ReplicationMutationGuardOperation,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if zfsService == nil {
			abortReplicationMutationGuard(c, fmt.Errorf("replication_dataset_guard_unavailable"))
			return
		}
		ctx := c.Request.Context()
		var err error
		switch operation {
		case ReplicationGuardDatasetGUID:
			err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, c.Param("guid"))
		case ReplicationGuardPoolGUID:
			err = zfsService.RequireReplicationPoolMutationAllowed(ctx, c.Param("guid"))
		case ReplicationGuardBulkGUIDs:
			var req BulkDeleteRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, req.GUIDs...)
			}
		case ReplicationGuardBulkNames:
			var req BulkDeleteByNameRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetMutationAllowed(ctx, req.Names...)
			}
		case ReplicationGuardCreateFilesystem:
			var req CreateFilesystemRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				requestParent := normalizedGuardDataset(req.Parent)
				propertyParent := normalizedGuardDataset(req.Properties["parent"])
				if requestParent == "" || propertyParent == "" || requestParent != propertyParent {
					err = fmt.Errorf("replication_dataset_create_parent_mismatch")
				} else {
					err = zfsService.RequireReplicationDatasetCreateAllowed(
						ctx, propertyParent+"/"+normalizedGuardDataset(req.Name),
					)
				}
			}
		case ReplicationGuardEditFilesystem:
			var req EditFilesystemRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, req.GUID)
			}
		case ReplicationGuardCreateVolume:
			var req CreateVolumeRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetCreateAllowed(
					ctx, normalizedGuardDataset(req.Parent)+"/"+normalizedGuardDataset(req.Name),
				)
			}
		case ReplicationGuardEditVolume:
			var req zfsServiceInterfaces.EditVolumeRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, req.GUID)
			}
		case ReplicationGuardFlashVolume:
			var req FlashVolumeRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, req.GUID)
			}
		case ReplicationGuardRollbackSnapshot:
			var req RollbackSnapshotRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, req.GUID)
			}
		default:
			err = fmt.Errorf("replication_dataset_guard_operation_invalid")
		}
		if err != nil {
			abortReplicationMutationGuard(c, err)
			return
		}
		c.Next()
	}
}

func normalizedGuardDataset(value string) string {
	return strings.Trim(strings.TrimSpace(value), "/")
}
