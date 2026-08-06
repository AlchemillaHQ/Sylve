// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
)

func zfsErrorStatus(err error) int {
	switch {
	case errors.Is(err, zfs.ErrInvalidRequest),
		errors.Is(err, zfs.ErrReservedSnapshotNamespace):
		return http.StatusBadRequest
	case errors.Is(err, zfs.ErrPoolNotFound),
		errors.Is(err, zfs.ErrDatasetNotFound),
		errors.Is(err, zfs.ErrSnapshotJobNotFound),
		errors.Is(err, zfs.ErrSourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, zfs.ErrConflict),
		errors.Is(err, zfs.ErrCannotDeletePoolRootDataset),
		errors.Is(err, zfs.ErrSnapshotCreationBlocked):
		return http.StatusConflict
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
