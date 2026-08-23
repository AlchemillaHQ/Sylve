// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/gin-gonic/gin"
)

type JailRootMountPointResponse struct {
	MountPoint string `json:"mountPoint"`
}

type jailRootMountPointService interface {
	GetJailBaseMountPoint(ctID uint) (string, error)
}

func classifyJailRootMountPointError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_resolve_jail_root_mountpoint"
	}
	if isJailNotFoundError(err) {
		return http.StatusNotFound, "jail_not_found"
	}

	errText := strings.ToLower(err.Error())
	if strings.Contains(errText, "gzfs_not_initialized") {
		return http.StatusServiceUnavailable, "gzfs_not_initialized"
	}

	for _, code := range []string{
		"jail_base_storage_not_found",
		"jail_base_storage_ambiguous",
		"jail_base_pool_not_found",
		"jail_base_pool_invalid",
		"jail_dataset_mountpoint_not_usable",
	} {
		if strings.Contains(errText, code) {
			return http.StatusConflict, code
		}
	}

	return http.StatusInternalServerError, "failed_to_resolve_jail_root_mountpoint"
}

// @Summary Resolve a jail root mountpoint
// @Description Resolve and validate the host-side mountpoint for a jail's root ZFS dataset
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Success 200 {object} internal.APIResponse[JailRootMountPointResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/root-mountpoint [get]
func GetJailRootMountPoint(jailService jailRootMountPointService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}

		mountPoint, err := jailService.GetJailBaseMountPoint(ctID)
		if err != nil {
			status, message := classifyJailRootMountPointError(err)
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[JailRootMountPointResponse]{
			Status:  "success",
			Message: "jail_root_mountpoint_resolved",
			Data: JailRootMountPointResponse{
				MountPoint: mountPoint,
			},
			Error: "",
		})
	}
}
