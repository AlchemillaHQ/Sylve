// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"github.com/gin-gonic/gin"
)

type CreateSnapshotRequest struct {
	GUID      string `json:"guid" binding:"required"`
	Name      string `json:"name" binding:"required"`
	Recursive bool   `json:"recursive"`
}

type CreateFilesystemRequest struct {
	Name       string            `json:"name" binding:"required"`
	Parent     string            `json:"parent" binding:"required"`
	Properties map[string]string `json:"properties"`
}

type EditFilesystemRequest struct {
	Properties map[string]string `json:"properties"`
	Inherit    []string          `json:"inherit"`
}

type CreateVolumeRequest struct {
	Name       string            `json:"name" binding:"required"`
	Parent     string            `json:"parent" binding:"required"`
	Properties map[string]string `json:"properties"`
}

type RollbackSnapshotRequest struct {
	Name              string   `json:"name"`
	DestroyMoreRecent bool     `json:"destroyMoreRecent"`
	ExpectedNewer     []string `json:"expectedNewer"`
}

type FlashVolumeRequest struct {
	UUID string `json:"uuid" binding:"required"`
}

func snapshotCreationErrorResponse(err error) (int, string) {
	switch {
	case errors.Is(err, zfs.ErrReservedSnapshotNamespace):
		return http.StatusBadRequest, "snapshot_namespace_reserved"
	case errors.Is(err, zfs.ErrSnapshotCreationBlocked):
		return http.StatusConflict, "snapshot_creation_blocked"
	}

	status := zfsErrorStatus(err)
	switch status {
	case http.StatusBadRequest:
		return status, "invalid_request"
	case http.StatusNotFound:
		return status, "dataset_not_found"
	case http.StatusConflict:
		return status, "conflict"
	default:
		return status, "internal_server_error"
	}
}

// @Summary Get all ZFS datasets
// @Description Get all ZFS datasets
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param type query string false "Dataset type" Enums(ALL,SNAPSHOT,FILESYSTEM,VOLUME) default(ALL)
// @Success 200 {object} internal.APIResponse[[]gzfs.Dataset] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets [get]
func GetDatasets(zfsService *zfs.Service, authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		qt := c.Query("type")

		var t gzfs.DatasetType

		if qt == "" {
			t = gzfs.DatasetTypeAll
		} else if qt == string(gzfs.DatasetTypeSnapshot) {
			t = gzfs.DatasetTypeSnapshot
		} else if qt == string(gzfs.DatasetTypeFilesystem) {
			t = gzfs.DatasetTypeFilesystem
		} else if qt == string(gzfs.DatasetTypeVolume) {
			t = gzfs.DatasetTypeVolume
		} else if qt == string(gzfs.DatasetTypeAll) {
			t = gzfs.DatasetTypeAll
		} else {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_dataset_type",
				Error:   "type must be one of: ALL, SNAPSHOT, FILESYSTEM, VOLUME",
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		datasets, err := zfsService.GetDatasets(ctx, t)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		datasets, ok := middleware.Visible(c, authService, datasets, auth.DatastoreAudit, func(dataset *gzfs.Dataset) string {
			pool, _, _ := strings.Cut(dataset.Name, "/")
			return "/storage/" + pool
		})
		if !ok {
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]*gzfs.Dataset]{
			Status:  "success",
			Message: "datasets",
			Error:   "",
			Data:    datasets,
		})
	}
}

// @Summary List ZFS snapshots
// @Description List snapshots on usable pools with their creation time and space usage
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]zfsServiceInterfaces.SnapshotSummary] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/snapshots [get]
func GetSnapshots(zfsService *zfs.Service, authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		snapshots, err := zfsService.GetSnapshotSummaries(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshots, ok := middleware.Visible(c, authService, snapshots, auth.DatastoreAudit, func(snapshot zfsServiceInterfaces.SnapshotSummary) string {
			return "/storage/" + snapshot.Pool
		})
		if !ok {
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zfsServiceInterfaces.SnapshotSummary]{
			Status:  "success",
			Message: "snapshots",
			Error:   "",
			Data:    snapshots,
		})
	}
}

// @Summary Delete a ZFS snapshot
// @Description Delete a ZFS snapshot
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Snapshot GUID"
// @Param name query string false "Full snapshot name; required when the GUID has multiple copies"
// @Param recursive query bool false "Recursively delete descendant snapshots" default(false)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/{guid} [delete]
func DeleteSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		recursive, err := parseOptionalBoolQuery(c, "recursive")
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}
		name, err := snapshotNameQuery(c)
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}

		ctx := c.Request.Context()
		err = zfsService.DeleteSnapshot(ctx, guid, name, recursive)

		if err != nil {
			writeZFSServiceError(c, err, "snapshot_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Create a ZFS snapshot
// @Description Create a ZFS snapshot
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsHandlers.CreateSnapshotRequest true "Create Snapshot Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot [post]
func CreateSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateSnapshotRequest

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.CreateSnapshot(ctx, request.GUID, request.Name, request.Recursive)

		if err != nil {
			status, message := snapshotCreationErrorResponse(err)
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Rollback to a ZFS snapshot
// @Description Rollback to a ZFS snapshot. Refused with 409 for datasets Sylve manages (under <pool>/sylve; roll those back from their VM or jail), for operating-system file systems, and while the file system is used by a running VM or a container. When expectedNewer is set, it must list the GUIDs of every snapshot newer than the target, or the rollback is refused with 409.
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Snapshot GUID"
// @Param request body zfsHandlers.RollbackSnapshotRequest true "Rollback Snapshot Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/{guid}/rollback [post]
func RollbackSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request RollbackSnapshotRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.RollbackSnapshot(ctx, guid, request.Name, request.DestroyMoreRecent, request.ExpectedNewer)
		if err != nil {
			writeZFSServiceError(c, err, "snapshot_rollback_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "rolled_back_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get all periodic ZFS snapshot jobs
// @Description Get all periodic ZFS snapshots jobs
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]zfsModels.PeriodicSnapshot] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic [get]
func GetPeriodicSnapshots(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		snapshots, err := zfsService.GetPeriodicSnapshots()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zfsModels.PeriodicSnapshot]{
			Status:  "success",
			Message: "periodic_snapshots",
			Error:   "",
			Data:    snapshots,
		})
	}
}

// @Summary Create a periodic ZFS snapshot job
// @Description Create a periodic ZFS snapshot job
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest true "Create Periodic Snapshot Job Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic [post]
func CreatePeriodicSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.AddPeriodicSnapshot(ctx, request)

		if err != nil {
			status, message := snapshotCreationErrorResponse(err)
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_periodic_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Modify retention of a periodic ZFS snapshot job
// @Description Modify retention of a periodic ZFS snapshot job
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Periodic snapshot job ID" minimum(1)
// @Param request body zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest true "Modify Periodic Snapshot Retention Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic/{id} [patch]
func ModifyPeriodicSnapshotRetention(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := positiveUintPath(c, "id")
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}

		var request zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err = zfsService.ModifyPeriodicSnapshotRetention(ctx, id, request)

		if err != nil {
			writeZFSServiceError(c, err, "periodic_snapshot_update_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "modified_periodic_snapshot_retention",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a periodic ZFS snapshot job
// @Description Delete a periodic ZFS snapshot job
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param id path int true "Periodic snapshot job ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic/{id} [delete]
func DeletePeriodicSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := positiveUintPath(c, "id")
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}

		ctx := c.Request.Context()
		err = zfsService.DeletePeriodicSnapshot(ctx, id)

		if err != nil {
			writeZFSServiceError(c, err, "periodic_snapshot_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_periodic_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Bulk delete periodic ZFS snapshot jobs
// @Description Atomically delete an exact, non-empty set of periodic ZFS snapshot jobs by ID
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.BulkDeletePeriodicSnapshotsRequest true "Periodic snapshot job IDs"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "One or more periodic snapshot jobs not found; nothing deleted"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic [delete]
func BulkDeletePeriodicSnapshots(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request zfsServiceInterfaces.BulkDeletePeriodicSnapshotsRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "invalid_periodic_snapshot_job_ids",
				Data:    nil,
			})
			return
		}

		if err := zfsService.BulkDeletePeriodicSnapshots(c.Request.Context(), request.IDs); err != nil {
			writeZFSServiceError(c, err, "periodic_snapshots_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_periodic_snapshots",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Create a ZFS filesystem
// @Description Create a ZFS filesystem
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsHandlers.CreateFilesystemRequest true "Create Filesystem Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/filesystem [post]
func CreateFilesystem(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateFilesystemRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.CreateFilesystem(ctx, request.Name, request.Parent, request.Properties)

		if err != nil {
			writeZFSServiceError(c, err, "filesystem_create_failed")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_filesystem",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit a ZFS filesystem
// @Description Edit a ZFS filesystem
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Filesystem GUID"
// @Param request body zfsHandlers.EditFilesystemRequest true "Edit Filesystem Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/filesystem/{guid} [patch]
func EditFilesystem(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request EditFilesystemRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.EditFilesystem(ctx, guid, request.Properties, request.Inherit)

		if err != nil {
			writeZFSServiceError(c, err, "filesystem_edit_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "edited_filesystem",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a ZFS filesystem
// @Description Delete a ZFS filesystem and everything inside it. Returns 409 when anything inside it is in use, including a volume an iSCSI target shares.
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Filesystem GUID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/filesystem/{guid} [delete]
func DeleteFilesystem(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.DeleteFilesystem(ctx, guid)

		if err != nil {
			writeZFSServiceError(c, err, "filesystem_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_filesystem",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Create a ZFS volume
// @Description Create a ZFS volume
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsHandlers.CreateVolumeRequest true "Create Volume Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume [post]
func CreateVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateVolumeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.CreateVolume(ctx, request.Name, request.Parent, request.Properties)

		if err != nil {
			writeZFSServiceError(c, err, "volume_create_failed")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_volume",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit a ZFS volume
// @Description Edit a ZFS volume. Properties listed in inherit are reset to their inherited value. Shrinking the volume or changing its volume mode is refused with 409 while a running VM uses it or an iSCSI target shares it.
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Volume GUID"
// @Param request body zfsServiceInterfaces.EditVolumeRequest true "Edit Volume Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume/{guid} [patch]
func EditVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request zfsServiceInterfaces.EditVolumeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.EditVolume(ctx, guid, request.Properties, request.Inherit)

		if err != nil {
			writeZFSServiceError(c, err, "volume_edit_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "edited_volume",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a ZFS volume
// @Description Delete a ZFS volume and its snapshots. Returns 409 when a VM uses it or an iSCSI target shares it.
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Volume GUID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume/{guid} [delete]
func DeleteVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		ctx := c.Request.Context()
		err := zfsService.DeleteVolume(ctx, guid)

		if err != nil {
			writeZFSServiceError(c, err, "volume_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_volume",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Bulk delete ZFS datasets
// @Description Delete one or more ZFS datasets using exact name and GUID pairs. Returns 409 when anything being deleted is in use, including a volume an iSCSI target shares.
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param name query []string true "Exact dataset name; pair by index with each guid parameter" collectionFormat(multi)
// @Param guid query []string true "Dataset GUID; pair by index with each name parameter" collectionFormat(multi)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets [delete]
func BulkDeleteDataset(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targets, err := datasetDeletionTargetsQuery(c)
		if err != nil {
			writeZFSServiceError(c, err, "invalid_request")
			return
		}

		ctx := c.Request.Context()
		err = zfsService.BulkDeleteDataset(ctx, targets)

		if err != nil {
			writeZFSServiceError(c, err, "dataset_delete_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "deleted_datasets",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Flash a ZFS volume
// @Description Write a downloaded ISO or disk image onto a ZFS volume, starting at its first byte. Disk images in formats other than raw, such as QCOW2 or VMDK, are converted to raw as they are written, and images with a backing file are refused. The image, or its virtual size for disk images, must fit in the volume. Returns 409 when the volume is in use, shared by an iSCSI target, read only or too small. The write continues if the client disconnects.
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Volume GUID"
// @Param request body zfsHandlers.FlashVolumeRequest true "Flash Volume Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume/{guid}/flash [post]
func FlashVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		var request FlashVolumeRequest

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.FlashVolume(ctx, guid, request.UUID)

		if err != nil {
			writeZFSServiceError(c, err, "volume_flash_failed")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "flashed_volume",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get all ZFS Datasets with Pagination
// @Description Get all ZFS Datasets with Pagination
// @Tags ZFS
// @Produce json
// @Security BearerAuth
// @Param datasetType query string true "Dataset type" Enums(ALL,SNAPSHOT,FILESYSTEM,VOLUME)
// @Param page query int false "Page number" default(1)
// @Param size query int false "Page size" default(25)
// @Param search query string false "Search dataset names"
// @Param nameFilter query string false "Comma-separated filters to exclude. bk_j, svms_, sjs_, ha_ and _template match Sylve snapshot kinds by snapshot name; other values exclude names containing them"
// @Param sort[0][field] query string false "Sort field" Enums(name,used,referenced,creation)
// @Param sort[0][dir] query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} internal.APIResponse[zfsServiceInterfaces.PaginatedDatasetsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/paginated [get]
func GetPaginatedDatasets(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request zfsServiceInterfaces.PaginatedDatasetsRequest
		if err := c.ShouldBindQuery(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		field := c.Query("sort[0][field]")
		dir := c.Query("sort[0][dir]")

		if field != "" {
			request.Sort = []zfsServiceInterfaces.SortParam{
				{Field: field, Dir: dir},
			}
		}

		var allowedSortFields = map[string]struct{}{
			"name":       {},
			"used":       {},
			"referenced": {},
			"creation":   {},
		}

		if len(request.Sort) > 0 {
			s := request.Sort[0]

			if _, ok := allowedSortFields[s.Field]; !ok {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_sort_field",
					Error:   "sort field must be one of: name, used, referenced, creation",
					Data:    nil,
				})
				return
			}

			if s.Dir != "asc" && s.Dir != "desc" {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_sort_dir",
					Error:   "sort dir must be asc or desc",
					Data:    nil,
				})
				return
			}
		}

		switch request.DatasetType {
		case gzfs.DatasetTypeSnapshot, gzfs.DatasetTypeFilesystem, gzfs.DatasetTypeVolume, gzfs.DatasetTypeAll:
		default:
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_dataset_type",
				Error:   "datasetType must be one of: ALL, SNAPSHOT, FILESYSTEM, VOLUME",
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		response, err := zfsService.GetPaginatedDatasets(ctx, &request)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[zfsServiceInterfaces.PaginatedDatasetsResponse]{
			Status:  "success",
			Message: "paginated_datasets",
			Error:   "",
			Data:    *response,
		})
	}
}
