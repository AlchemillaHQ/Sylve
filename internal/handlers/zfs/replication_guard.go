// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"bytes"
	"encoding/json"
	"errors"
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
	ReplicationGuardBulkTargets      ReplicationMutationGuardOperation = "bulk_targets"
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
	} else if errors.Is(err, zfs.ErrPoolNotFound) {
		status = http.StatusNotFound
		message = "pool_not_found"
	} else if errors.Is(err, zfs.ErrDatasetNotFound) {
		status = http.StatusNotFound
		message = "dataset_not_found"
	} else if errors.Is(err, zfs.ErrInvalidRequest) ||
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
		case ReplicationGuardBulkTargets:
			var targets []zfsServiceInterfaces.DatasetDeletionTarget
			if targets, err = datasetDeletionTargetsQuery(c); err == nil {
				names := make([]string, 0, len(targets))
				for _, target := range targets {
					names = append(names, target.Name)
				}
				err = zfsService.RequireReplicationDatasetMutationAllowed(ctx, names...)
			}
		case ReplicationGuardCreateFilesystem:
			var req CreateFilesystemRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				requestParent := normalizedGuardDataset(req.Parent)
				requestName := normalizedGuardDataset(req.Name)
				if requestParent == "" || requestName == "" {
					err = invalidZFSRequest("filesystem name and parent are required")
				} else {
					err = zfsService.RequireReplicationDatasetCreateAllowed(
						ctx, requestParent+"/"+requestName,
					)
				}
			}
		case ReplicationGuardEditFilesystem:
			err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, c.Param("guid"))
		case ReplicationGuardCreateVolume:
			var req CreateVolumeRequest
			if err = decodeAndRestoreMutationBody(c, &req); err == nil {
				requestParent := normalizedGuardDataset(req.Parent)
				requestName := normalizedGuardDataset(req.Name)
				if requestParent == "" || requestName == "" {
					err = invalidZFSRequest("volume name and parent are required")
				} else {
					err = zfsService.RequireReplicationDatasetCreateAllowed(
						ctx, requestParent+"/"+requestName,
					)
				}
			}
		case ReplicationGuardEditVolume:
			err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, c.Param("guid"))
		case ReplicationGuardFlashVolume:
			err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, c.Param("guid"))
		case ReplicationGuardRollbackSnapshot:
			err = zfsService.RequireReplicationDatasetGUIDMutationAllowed(ctx, c.Param("guid"))
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
