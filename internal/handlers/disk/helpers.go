// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func bindDiskJSON(c *gin.Context, destination, validationModel any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "disk_request_too_large",
				Error:   "disk request body is too large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   "validation_error",
			Data:    utils.MapValidationErrors(err, validationModel),
		})
		return false
	}
	return true
}

func diskMutationHTTPStatus(err error) int {
	switch {
	case errors.Is(err, disk.ErrInvalidDiskRequest), errors.Is(err, disk.ErrInvalidPhysicalDisk):
		return http.StatusBadRequest
	case errors.Is(err, disk.ErrDiskResourceNotFound), errors.Is(err, disk.ErrPhysicalDiskNotFound):
		return http.StatusNotFound
	case errors.Is(err, disk.ErrDiskOperationConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeDiskMutationError(c *gin.Context, message string, err error) {
	c.JSON(diskMutationHTTPStatus(err), internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}
