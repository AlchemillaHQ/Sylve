// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	authModels "github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

type SambaPrincipalIDsRequest struct {
	UserIDs  []uint `json:"userIds"`
	GroupIDs []uint `json:"groupIds"`
}

type SambaPermissionsRequest struct {
	Read  SambaPrincipalIDsRequest `json:"read"`
	Write SambaPrincipalIDsRequest `json:"write"`
}

type SambaGuestRequest struct {
	Enabled   bool `json:"enabled"`
	Writeable bool `json:"writeable"`
}

type CreateSambaShareRequest struct {
	Name               string                  `json:"name"`
	Dataset            string                  `json:"dataset"`
	Permissions        SambaPermissionsRequest `json:"permissions"`
	Guest              SambaGuestRequest       `json:"guest"`
	CreateMask         string                  `json:"createMask"`
	DirectoryMask      string                  `json:"directoryMask"`
	TimeMachine        *bool                   `json:"timeMachine"`
	TimeMachineMaxSize *uint64                 `json:"timeMachineMaxSize"`
}

type UpdateSambaShareRequest struct {
	ID                 uint                    `json:"id"`
	Name               string                  `json:"name"`
	Dataset            string                  `json:"dataset"`
	Permissions        SambaPermissionsRequest `json:"permissions"`
	Guest              SambaGuestRequest       `json:"guest"`
	CreateMask         string                  `json:"createMask"`
	DirectoryMask      string                  `json:"directoryMask"`
	TimeMachine        *bool                   `json:"timeMachine"`
	TimeMachineMaxSize *uint64                 `json:"timeMachineMaxSize"`
}

type SambaPrincipalUserResponse struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

type SambaPrincipalGroupResponse struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type SambaPrincipalSetResponse struct {
	Users  []SambaPrincipalUserResponse  `json:"users"`
	Groups []SambaPrincipalGroupResponse `json:"groups"`
}

type SambaPermissionsResponse struct {
	Read  SambaPrincipalSetResponse `json:"read"`
	Write SambaPrincipalSetResponse `json:"write"`
}

type SambaGuestResponse struct {
	Enabled   bool `json:"enabled"`
	Writeable bool `json:"writeable"`
}

type SambaShareResponse struct {
	ID                 int                      `json:"id"`
	Name               string                   `json:"name"`
	Dataset            string                   `json:"dataset"`
	Permissions        SambaPermissionsResponse `json:"permissions"`
	Guest              SambaGuestResponse       `json:"guest"`
	CreateMask         string                   `json:"createMask"`
	DirectoryMask      string                   `json:"directoryMask"`
	TimeMachine        bool                     `json:"timeMachine"`
	TimeMachineMaxSize uint64                   `json:"timeMachineMaxSize"`
	CreatedAt          string                   `json:"createdAt"`
	UpdatedAt          string                   `json:"updatedAt"`
}

func strictJSONBind(c *gin.Context, dst any) error {
	raw, err := c.GetRawData()
	if err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return err
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid_json_payload")
		}
		return err
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))
	return nil
}

func mapUsers(users []authModels.User) []SambaPrincipalUserResponse {
	mapped := make([]SambaPrincipalUserResponse, 0, len(users))
	for _, user := range users {
		mapped = append(mapped, SambaPrincipalUserResponse{ID: user.ID, Username: user.Username})
	}
	return mapped
}

func mapGroups(groups []authModels.Group) []SambaPrincipalGroupResponse {
	mapped := make([]SambaPrincipalGroupResponse, 0, len(groups))
	for _, group := range groups {
		mapped = append(mapped, SambaPrincipalGroupResponse{ID: group.ID, Name: group.Name})
	}
	return mapped
}

func mapShareResponse(share sambaModels.SambaShare) SambaShareResponse {
	guestWriteable := share.GuestOk && !share.ReadOnly

	return SambaShareResponse{
		ID:      share.ID,
		Name:    share.Name,
		Dataset: share.Dataset,
		Permissions: SambaPermissionsResponse{
			Read: SambaPrincipalSetResponse{
				Users:  mapUsers(share.ReadOnlyUsers),
				Groups: mapGroups(share.ReadOnlyGroups),
			},
			Write: SambaPrincipalSetResponse{
				Users:  mapUsers(share.WriteableUsers),
				Groups: mapGroups(share.WriteableGroups),
			},
		},
		Guest: SambaGuestResponse{
			Enabled:   share.GuestOk,
			Writeable: guestWriteable,
		},
		CreateMask:         share.CreateMask,
		DirectoryMask:      share.DirectoryMask,
		TimeMachine:        share.TimeMachine,
		TimeMachineMaxSize: share.TimeMachineMaxSize,
		CreatedAt:          share.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:          share.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func sambaShareServiceErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	msg := err.Error()

	switch {
	case msg == "share_with_name_exists", msg == "share_with_dataset_exists":
		return http.StatusConflict
	case strings.HasPrefix(msg, "share_not_found"):
		return http.StatusNotFound
	case msg == "guest_only_share_cannot_have_principals",
		msg == "no_principals_selected_and_guests_not_allowed",
		msg == "dataset_not_found",
		msg == "dataset_not_mounted",
		strings.HasPrefix(msg, "user_not_found:"),
		strings.HasPrefix(msg, "group_not_found:"),
		strings.Contains(msg, "dataset_not_filesystem:"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// @Summary Get Samba Shares
// @Description Retrieve all Samba shares
// @Tags Samba
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[[]SambaShareResponse] "Success"
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

		mapped := make([]SambaShareResponse, 0, len(shares))
		for _, share := range shares {
			mapped = append(mapped, mapShareResponse(share))
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]SambaShareResponse]{
			Status:  "success",
			Message: "shares_retrieved",
			Error:   "",
			Data:    mapped,
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
		if err := strictJSONBind(c, &request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		timeMachine := false
		if request.TimeMachine != nil {
			timeMachine = *request.TimeMachine
		}

		var timeMachineMaxSize uint64
		if request.TimeMachineMaxSize != nil {
			timeMachineMaxSize = *request.TimeMachineMaxSize
		}

		ctx := c.Request.Context()
		if err := smbService.CreateShare(
			ctx,
			request.Name,
			request.Dataset,
			request.Permissions.Read.UserIDs,
			request.Permissions.Write.UserIDs,
			request.Permissions.Read.GroupIDs,
			request.Permissions.Write.GroupIDs,
			request.Guest.Enabled,
			request.Guest.Writeable,
			request.CreateMask,
			request.DirectoryMask,
			timeMachine,
			timeMachineMaxSize,
		); err != nil {
			c.JSON(sambaShareServiceErrorStatus(err), internal.APIResponse[any]{
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
		if err := strictJSONBind(c, &request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		timeMachine := false
		if request.TimeMachine != nil {
			timeMachine = *request.TimeMachine
		}

		var timeMachineMaxSize uint64
		if request.TimeMachineMaxSize != nil {
			timeMachineMaxSize = *request.TimeMachineMaxSize
		}

		ctx := c.Request.Context()
		if err := smbService.UpdateShare(
			ctx,
			request.ID,
			request.Name,
			request.Dataset,
			request.Permissions.Read.UserIDs,
			request.Permissions.Write.UserIDs,
			request.Permissions.Read.GroupIDs,
			request.Permissions.Write.GroupIDs,
			request.Guest.Enabled,
			request.Guest.Writeable,
			request.CreateMask,
			request.DirectoryMask,
			timeMachine,
			timeMachineMaxSize,
		); err != nil {
			c.JSON(sambaShareServiceErrorStatus(err), internal.APIResponse[any]{
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
			c.JSON(sambaShareServiceErrorStatus(err), internal.APIResponse[any]{
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
