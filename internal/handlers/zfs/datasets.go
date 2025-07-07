// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"net/http"
	"sylve/internal"
	zfsModels "sylve/internal/db/models/zfs"
	zfsServiceInterfaces "sylve/internal/interfaces/services/zfs"
	"sylve/internal/services/zfs"

	"github.com/gin-gonic/gin"
)

type CreateSnapshotRequest struct {
	GUID      string `json:"guid" binding:"required"`
	Name      string `json:"name" binding:"required"`
	Recursive bool   `json:"recursive"`
}

type CreatePeriodicSnapshotJobRequest struct {
	GUID      string `json:"guid" binding:"required"`
	Prefix    string `json:"prefix" binding:"required"`
	Recursive bool   `json:"recursive"`
	Interval  *int   `json:"interval" binding:"required"`
	CronExpr  string `json:"cronExpr"`
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

type EditVolumeRequest struct {
	Name       string            `json:"name" binding:"required"`
	Properties map[string]string `json:"properties" binding:"required"`
}

type RollbackSnapshotRequest struct {
	GUID              string `json:"guid" binding:"required"`
	DestroyMoreRecent bool   `json:"destroyMoreRecent"`
}

type BulkDeleteRequest struct {
	GUIDs []string `json:"guids" binding:"required"`
}

type FlashVolumeRequest struct {
	GUID string `json:"guid" binding:"required"`
	UUID string `json:"uuid" binding:"required"`
}

// @Summary Get all ZFS datasets
// @Description Get all ZFS datasets
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]zfsServiceInterfaces.Dataset] "OK"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets [get]
func GetDatasets(zfsSerice *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		datasets, err := zfsSerice.GetDatasets()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]zfsServiceInterfaces.Dataset]{
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

		err := zfsService.DeleteSnapshot(guid, r)

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

		err := zfsService.CreateSnapshot(request.GUID, request.Name, request.Recursive)

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

		err := zfsService.RollbackSnapshot(request.GUID, request.DestroyMoreRecent)
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

// @Summary Create a periodic ZFS snapshot job
// @Description Create a periodic ZFS snapshot job
// @Tags ZFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreatePeriodicSnapshotJobRequest true "Create Periodic Snapshot Job Request"
// @Success 200 {object} internal.APIResponse[any] "OK"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /zfs/datasets/snapshot/periodic [post]
func CreatePeriodicSnapshot(zfsService *zfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreatePeriodicSnapshotJobRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var interval int
		var cronExpr string

		if request.Interval == nil {
			interval = 0
		} else {
			interval = *request.Interval
		}

		if request.CronExpr == "" {
			cronExpr = ""
		} else {
			cronExpr = request.CronExpr
		}

		err := zfsService.AddPeriodicSnapshot(request.GUID, request.Prefix, request.Recursive, interval, cronExpr)

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

		err := zfsService.CreateFilesystem(request.Name, request.Properties)

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

		err := zfsService.EditFilesystem(request.GUID, request.Properties)

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
		err := zfsService.DeleteFilesystem(guid)

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

		err := zfsService.CreateVolume(request.Name, request.Parent, request.Properties)

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
		var request EditVolumeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := zfsService.EditVolume(request.Name, request.Properties)

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
		err := zfsService.DeleteVolume(guid)

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

		err := zfsService.BulkDeleteDataset(guids.GUIDs)

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

// flash volume handler
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

		err := zfsService.FlashVolume(request.GUID, request.UUID)

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
