// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"fmt"
	"net/http"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
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
	GUID       string            `json:"guid" binding:"required"`
	Properties map[string]string `json:"properties" binding:"required"`
}

type CreateVolumeRequest struct {
	Name       string            `json:"name" binding:"required"`
	Parent     string            `json:"parent" binding:"required"`
	Properties map[string]string `json:"properties"`
}

type RollbackSnapshotRequest struct {
	GUID              string `json:"guid" binding:"required"`
	DestroyMoreRecent bool   `json:"destroyMoreRecent"`
}

type BulkDeleteRequest struct {
	GUIDs []string `json:"guids" binding:"required"`
}

type BulkDeleteByNameRequest struct {
	Names []string `json:"names" binding:"required"`
}

type FlashVolumeRequest struct {
	GUID string `json:"guid" binding:"required"`
	UUID string `json:"uuid" binding:"required"`
}

type DatasetListResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    []*gzfs.Dataset `json:"data"`
}

// @Summary Get all ZFS datasets
// @Description Get all ZFS datasets
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param type query string false "Filter for datasets"
// @Success 200 {object} DatasetListResponse "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets [get]
func GetDatasets(zfsService *zfs.Service) gin.HandlerFunc {
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

		c.JSON(http.StatusOK, internal.APIResponse[[]*gzfs.Dataset]{
			Status:  "success",
			Message: "datasets",
			Error:   "",
			Data:    datasets,
		})
	}
}

// @Summary Delete a ZFS snapshot
// @Description Delete a ZFS snapshot
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Snapshot GUID"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/{guid} [delete]
func DeleteSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		recursive := c.Query("recursive")
		var r bool

		if recursive == "" {
			r = false
		} else if recursive == "true" {
			r = true
		}

		ctx := c.Request.Context()
		err := zfsService.DeleteSnapshot(ctx, guid, r)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Param request body CreateSnapshotRequest true "Create Snapshot Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_snapshot",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Rollback to a ZFS snapshot
// @Description Rollback to a ZFS snapshot
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RollbackSnapshotRequest true "Rollback Snapshot Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/rollback [post]
func RollbackSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		err := zfsService.RollbackSnapshot(ctx, request.GUID, request.DestroyMoreRecent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]zfsModels.PeriodicSnapshot] "OK"
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

type RetentionType string

const (
	RetentionNone   RetentionType = "none"
	RetentionSimple RetentionType = "simple"
	RetentionGFS    RetentionType = "gfs"
)

func validateAndDetectRetention(req zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest) (RetentionType, error) {
	// presence (did the client include the fields?)
	simplePresent := req.KeepLast != nil || req.MaxAgeDays != nil
	gfsPresent := req.KeepHourly != nil || req.KeepDaily != nil ||
		req.KeepWeekly != nil || req.KeepMonthly != nil || req.KeepYearly != nil

	// mutually exclusive
	if simplePresent && gfsPresent {
		return "", fmt.Errorf("retention_conflict: simple and GFS cannot be set together")
	}

	// normalize to values (treat nil as 0)
	keepLast := 0
	maxAgeDays := 0
	if req.KeepLast != nil {
		keepLast = *req.KeepLast
	}
	if req.MaxAgeDays != nil {
		maxAgeDays = *req.MaxAgeDays
	}

	keepHourly, keepDaily, keepWeekly, keepMonthly, keepYearly := 0, 0, 0, 0, 0
	if req.KeepHourly != nil {
		keepHourly = *req.KeepHourly
	}
	if req.KeepDaily != nil {
		keepDaily = *req.KeepDaily
	}
	if req.KeepWeekly != nil {
		keepWeekly = *req.KeepWeekly
	}
	if req.KeepMonthly != nil {
		keepMonthly = *req.KeepMonthly
	}
	if req.KeepYearly != nil {
		keepYearly = *req.KeepYearly
	}

	// non-negative check
	for _, v := range []int{keepLast, maxAgeDays, keepHourly, keepDaily, keepWeekly, keepMonthly, keepYearly} {
		if v < 0 {
			return "", fmt.Errorf("invalid_retention: values must be >= 0")
		}
	}

	// detect type + “all zeros” → none
	if simplePresent {
		if keepLast == 0 && maxAgeDays == 0 {
			return RetentionNone, nil
		}
		return RetentionSimple, nil
	}

	if gfsPresent {
		if keepHourly == 0 && keepDaily == 0 && keepWeekly == 0 && keepMonthly == 0 && keepYearly == 0 {
			return RetentionNone, nil
		}
		return RetentionGFS, nil
	}

	// nothing provided at all → none
	return RetentionNone, nil
}

// @Summary Create a periodic ZFS snapshot job
// @Description Create a periodic ZFS snapshot job
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest true "Create Periodic Snapshot Job Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
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

		_, err := validateAndDetectRetention(request)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_retention",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err = zfsService.AddPeriodicSnapshot(ctx, request)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
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
// @Param request body ModifyPeriodicSnapshotRetentionRequest true "Modify Periodic Snapshot Retention Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic [patch]
func ModifyPeriodicSnapshotRetention(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		err := zfsService.ModifyPeriodicSnapshotRetention(request)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Delete a periodic ZFS snapshot
// @Description Delete a periodic ZFS snapshot
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Periodic Snapshot GUID"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic/{guid} [delete]
func DeletePeriodicSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		err := zfsService.DeletePeriodicSnapshot(guid)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Create a ZFS filesystem
// @Description Create a ZFS filesystem
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateFilesystemRequest true "Create Filesystem Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
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
		err := zfsService.CreateFilesystem(ctx, request.Name, request.Properties)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
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
// @Param request body EditFilesystemRequest true "Edit Filesystem Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/filesystem [patch]
func EditFilesystem(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		err := zfsService.EditFilesystem(ctx, request.GUID, request.Properties)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Description Delete a ZFS filesystem
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Filesystem GUID"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/filesystem/{guid} [delete]
func DeleteFilesystem(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")

		ctx := c.Request.Context()
		err := zfsService.DeleteFilesystem(ctx, guid)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Param request body CreateVolumeRequest true "Create Volume Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "created_volume",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Edit a ZFS volume
// @Description Edit a ZFS volume
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body EditVolumeRequest true "Edit Volume Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume [patch]
func EditVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		err := zfsService.EditVolume(ctx, request)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Description Delete a ZFS volume
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param guid path string true "Volume GUID"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume/{guid} [delete]
func DeleteVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		guid := c.Param("guid")
		ctx := c.Request.Context()
		err := zfsService.DeleteVolume(ctx, guid)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Description Bulk delete ZFS datasets
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body BulkDeleteRequest true "Bulk Delete Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/bulk-delete [post]
func BulkDeleteDataset(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var guids BulkDeleteRequest

		if err := c.ShouldBindJSON(&guids); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := zfsService.BulkDeleteDataset(ctx, guids.GUIDs)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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

// @Summary Bulk Delete Datasets By Name
// @Description Bulk delete ZFS datasets by their names
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.BulkDeleteDatasetsByNameRequest true "Bulk Delete Datasets By Name Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/bulk-delete-by-name [post]
func BulkDeleteDatasetsByName(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request BulkDeleteByNameRequest

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
		err := zfsService.BulkDeleteDatasetByNames(ctx, request.Names)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Description Flash a ZFS volume with a UUID pointing to a disk iso/img
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body FlashVolumeRequest true "Flash Volume Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/volume/flash [post]
func FlashVolume(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		err := zfsService.FlashVolume(ctx, request.GUID, request.UUID)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
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
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body zfsServiceInterfaces.PaginatedDatasetsRequest true "Paginated Datasets Request"
// @Success 200 {object} zfsServiceInterfaces.PaginatedDatasetsResponse "OK"
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
		}

		if len(request.Sort) > 0 {
			s := request.Sort[0]

			if _, ok := allowedSortFields[s.Field]; !ok {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_sort_field",
					Error:   "sort field must be one of: name, used, referenced",
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
