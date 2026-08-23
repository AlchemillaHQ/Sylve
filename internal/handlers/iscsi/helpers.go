// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/gin-gonic/gin"
)

func iscsiErrorStatus(err error) int {
	switch {
	case errors.Is(err, iscsi.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, iscsi.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, iscsi.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeISCSIMutationError(c *gin.Context, err error) {
	if errors.Is(err, iscsi.ErrApplyFailed) {
		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "iscsi_configuration_saved_apply_pending",
			Error:   "",
			Data:    nil,
		})
		return
	}

	c.JSON(iscsiErrorStatus(err), internal.APIResponse[any]{
		Status:  "error",
		Message: err.Error(),
		Error:   err.Error(),
		Data:    nil,
	})
}

func bindISCSIJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "iscsi_request_too_large",
				Error:   "iSCSI request body is too large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   err.Error(),
			Data:    nil,
		})
		return false
	}
	return true
}

func iscsiPathID(c *gin.Context, parameter, message string) (uint, bool) {
	id, err := strconv.ParseUint(c.Param(parameter), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: message,
			Error:   "invalid_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}
