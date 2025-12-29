// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

type CreateSambaShareRequest struct {
	Name            string   `json:"name"`
	Dataset         string   `json:"dataset"`
	ReadOnlyGroups  []string `json:"readOnlyGroups"`
	WriteableGroups []string `json:"writeableGroups"`
	CreateMask      string   `json:"createMask"`
	DirectoryMask   string   `json:"directoryMask"`
	GuestOk         *bool    `json:"guestOk"`
	ReadOnly        *bool    `json:"readOnly"`
}

type UpdateSambaShareRequest struct {
	ID              uint     `json:"id"`
	Name            string   `json:"name"`
	Dataset         string   `json:"dataset"`
	ReadOnlyGroups  []string `json:"readOnlyGroups"`
	WriteableGroups []string `json:"writeableGroups"`
	CreateMask      string   `json:"createMask"`
	DirectoryMask   string   `json:"directoryMask"`
	GuestOk         *bool    `json:"guestOk"`
	ReadOnly        *bool    `json:"readOnly"`
}

// @Summary Get Samba Shares
// @Description Retrieve all Samba shares
// @Tags Samba
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[[]sambaModels.SambaShare] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/shares [get]
func GetShares(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		shares, err := smbService.GetShares()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_shares",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]sambaModels.SambaShare]{
			Status:  "success",
			Message: "shares_retrieved",
			Error:   "",
			Data:    shares,
		})
	}
}

// @Summary Create Samba Share
// @Description Create a new Samba share with specified settings
// @Tags Samba
// @Accept json
// @Produce json
// @Param request body CreateSambaShareRequest true "Create Samba Share Request"
// @Success 200 {string} string "Samba share created successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /samba/shares [post]
func CreateShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateSambaShareRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		guestOk := false

		if request.GuestOk != nil {
			guestOk = *request.GuestOk
		}

		readOnly := false

		if request.ReadOnly != nil {
			readOnly = *request.ReadOnly
		}

		ctx := c.Request.Context()
		if err := smbService.CreateShare(
			ctx,
			request.Name,
			request.Dataset,
			request.ReadOnlyGroups,
			request.WriteableGroups,
			request.CreateMask,
			request.DirectoryMask,
			guestOk,
			readOnly,
		); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_share",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "Samba share created successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update Samba Share
// @Description Update an existing Samba share with specified settings
// @Tags Samba
// @Accept json
// @Produce json
// @Param request body UpdateSambaShareRequest true "Update Samba Share Request"
// @Success 200 {string} string "Samba share updated successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /samba/shares [put]
func UpdateShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request UpdateSambaShareRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		guestOk := false

		if request.GuestOk != nil {
			guestOk = *request.GuestOk
		}

		readOnly := false

		if request.ReadOnly != nil {
			readOnly = *request.ReadOnly
		}

		ctx := c.Request.Context()
		if err := smbService.UpdateShare(
			ctx,
			request.ID,
			request.Name,
			request.Dataset,
			request.ReadOnlyGroups,
			request.WriteableGroups,
			request.CreateMask,
			request.DirectoryMask,
			guestOk,
			readOnly,
		); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_share",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "Samba share updated successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Samba Share
// @Description Delete a Samba share by ID
// @Tags Samba
// @Accept json
// @Produce json
// @Param id path uint true "Share ID"
// @Success 200 {string} string "Samba share deleted successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /samba/shares/{id} [delete]
func DeleteShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		idInt, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_share_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		if err := smbService.DeleteShare(ctx, uint(idInt)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_share",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "Samba share deleted successfully",
			Error:   "",
			Data:    nil,
		})
	}
}
