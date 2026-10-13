// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func zfsErrorStatus(err error) int {
	switch {
	case errors.Is(err, zfs.ErrInvalidRequest),
		errors.Is(err, zfs.ErrReservedSnapshotNamespace):
		return http.StatusBadRequest
	case errors.Is(err, zfs.ErrPoolNotFound),
		errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, zfs.ErrDatasetNotFound),
		errors.Is(err, zfs.ErrSnapshotJobNotFound),
		errors.Is(err, zfs.ErrSourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, zfs.ErrConflict),
		errors.Is(err, db.ErrPoolScrubActive),
		errors.Is(err, db.ErrPoolScrubBusy),
		errors.Is(err, gorm.ErrDuplicatedKey),
		errors.Is(err, zfs.ErrCannotDeletePoolRootDataset),
		errors.Is(err, zfs.ErrSnapshotCreationBlocked):
		return http.StatusConflict
	case errors.Is(err, zfs.ErrScrubStatusUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeZFSServiceError(c *gin.Context, err error, message string) {
	c.JSON(zfsErrorStatus(err), internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}
