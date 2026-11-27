// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/gin-gonic/gin"
)

func sambaViewError(c *gin.Context, message string, err error) {
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
}

// @Summary Get Samba status
// @Description Report whether the Samba service is enabled in Sylve and running, its version, and its current sessions, share connections and number of open files as reported by smbstatus. When smbstatus fails while Samba runs, error explains why and the lists are empty.
// @Tags Samba
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[samba.SambaStatus] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/status [get]
func GetStatus(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := smbService.Status()
		if err != nil {
			sambaViewError(c, "failed_to_get_samba_status", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[samba.SambaStatus]{Status: "success", Message: "samba_status_retrieved", Data: status})
	}
}

// @Summary List Samba share principals
// @Description List the PAM users and groups that can be given access to shares. sambaAccount tells whether a user has a Samba password and can sign in over SMB; it is null when the Samba account list could not be read.
// @Tags Samba
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[samba.SambaPrincipals] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/principals [get]
func GetPrincipals(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principals, err := smbService.Principals()
		if err != nil {
			sambaViewError(c, "failed_to_list_samba_principals", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[samba.SambaPrincipals]{Status: "success", Message: "samba_principals_retrieved", Data: principals})
	}
}

// @Summary List datasets for Samba shares
// @Description List every ZFS filesystem with its mount state, the share already using it (sharedBy), and why it cannot be shared (block: root, system_path, boot_environment, sylve_data or contains_protected_data). Datasets with a block are refused when creating a share or moving one to them.
// @Tags Samba
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]samba.ShareDataset] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/datasets [get]
func GetShareDatasets(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		datasets, err := smbService.ShareDatasets(c.Request.Context())
		if err != nil {
			sambaViewError(c, "failed_to_list_samba_datasets", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]samba.ShareDataset]{Status: "success", Message: "samba_datasets_retrieved", Data: datasets})
	}
}

// @Summary Stop applying share access to existing files
// @Description Cancel the background job started by applyToExisting. Items already processed keep their new permissions.
// @Tags Samba
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Success 200 {object} internal.APIResponse[any] "Cancelled"
// @Failure 400 {object} internal.APIResponse[any] "Invalid share ID"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "No job is running for this share"
// @Router /samba/shares/{id}/permissions-job [delete]
func CancelSharePermissionJob(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseShareID(c)
		if !ok {
			return
		}
		if !smbService.CancelPermissionJob(id) {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{Status: "error", Message: "permissions_job_not_running", Error: "permissions_job_not_running"})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "permissions_job_cancelled"})
	}
}
