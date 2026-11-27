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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	authModels "github.com/alchemillahq/sylve/internal/db/models"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

type SambaPrincipalIDsRequest struct {
	UserIDs       []uint   `json:"userIds"`
	GroupIDs      []uint   `json:"groupIds"`
	DirectorySIDs []string `json:"directorySids"`
}

type SambaPermissionsRequest struct {
	Read  SambaPrincipalIDsRequest `json:"read"`
	Write SambaPrincipalIDsRequest `json:"write"`
}

type SambaGuestRequest struct {
	Enabled   bool `json:"enabled"`
	Writeable bool `json:"writeable"`
}

type SetSambaShareEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type CreateSambaShareRequest struct {
	Name               string                  `json:"name"`
	Enabled            *bool                   `json:"enabled"`
	Dataset            string                  `json:"dataset"`
	Permissions        SambaPermissionsRequest `json:"permissions"`
	Guest              SambaGuestRequest       `json:"guest"`
	CreateMask         string                  `json:"createMask"`
	DirectoryMask      string                  `json:"directoryMask"`
	TimeMachine        *bool                   `json:"timeMachine"`
	TimeMachineMaxSize *uint64                 `json:"timeMachineMaxSize"`
	AuditEnabled       *bool                   `json:"auditEnabled"`
	AuditRetentionDays *uint32                 `json:"auditRetentionDays"`
	AuditedOperations  []string                `json:"auditedOperations"`
	ExtraShareConfig   string                  `json:"extraShareConfig"`
	ApplyToExisting    bool                    `json:"applyToExisting"`
}

type UpdateSambaShareRequest struct {
	Name               string                  `json:"name"`
	Enabled            *bool                   `json:"enabled"`
	Dataset            string                  `json:"dataset"`
	Permissions        SambaPermissionsRequest `json:"permissions"`
	Guest              SambaGuestRequest       `json:"guest"`
	CreateMask         string                  `json:"createMask"`
	DirectoryMask      string                  `json:"directoryMask"`
	TimeMachine        *bool                   `json:"timeMachine"`
	TimeMachineMaxSize *uint64                 `json:"timeMachineMaxSize"`
	AuditEnabled       *bool                   `json:"auditEnabled"`
	AuditRetentionDays *uint32                 `json:"auditRetentionDays"`
	AuditedOperations  []string                `json:"auditedOperations"`
	ExtraShareConfig   *string                 `json:"extraShareConfig"`
	ApplyToExisting    bool                    `json:"applyToExisting"`
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
	Users     []SambaPrincipalUserResponse  `json:"users"`
	Groups    []SambaPrincipalGroupResponse `json:"groups"`
	Directory []directoryModels.Principal   `json:"directory"`
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
	ID                 int                       `json:"id"`
	Name               string                    `json:"name"`
	Dataset            string                    `json:"dataset"`
	Enabled            bool                      `json:"enabled"`
	Permissions        SambaPermissionsResponse  `json:"permissions"`
	Guest              SambaGuestResponse        `json:"guest"`
	CreateMask         string                    `json:"createMask"`
	DirectoryMask      string                    `json:"directoryMask"`
	TimeMachine        bool                      `json:"timeMachine"`
	TimeMachineMaxSize uint64                    `json:"timeMachineMaxSize"`
	AuditEnabled       bool                      `json:"auditEnabled"`
	AuditRetentionDays uint32                    `json:"auditRetentionDays"`
	AuditedOperations  []string                  `json:"auditedOperations"`
	ExtraShareConfig   string                    `json:"extraShareConfig"`
	CreatedAt          string                    `json:"createdAt"`
	UpdatedAt          string                    `json:"updatedAt"`
	DatasetName        string                    `json:"datasetName"`
	Mountpoint         string                    `json:"mountpoint"`
	State              string                    `json:"state,omitempty"`
	DatasetBlock       string                    `json:"datasetBlock,omitempty"`
	PermissionsJob     *samba.SharePermissionJob `json:"permissionsJob,omitempty"`
}

func shareResponses(c *gin.Context, smbService *samba.Service, shares []sambaModels.SambaShare) []SambaShareResponse {
	states, err := smbService.ShareStates(c.Request.Context(), shares)
	if err != nil {
		logger.L.Warn().Err(err).Msg("failed to resolve Samba share datasets")
	}
	mapped := make([]SambaShareResponse, 0, len(shares))
	for _, share := range shares {
		response := mapShareResponse(share)
		if state, ok := states[share.ID]; ok {
			response.DatasetName = state.DatasetName
			response.Mountpoint = state.Mountpoint
			response.State = state.State
			response.DatasetBlock = state.Block
		}
		response.PermissionsJob = smbService.PermissionJob(uint(share.ID))
		mapped = append(mapped, response)
	}
	return mapped
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

func sambaRequestBindError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	message := "invalid_request"
	errorDetail := err.Error()
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		status = http.StatusRequestEntityTooLarge
		message = "samba_request_too_large"
		errorDetail = "Samba request body is too large"
	}
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errorDetail,
		Data:    nil,
	})
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

func nonNilDirectory(principals []directoryModels.Principal) []directoryModels.Principal {
	if principals == nil {
		return []directoryModels.Principal{}
	}
	return principals
}

func directoryPermissionRequest(permissions SambaPermissionsRequest) *directoryservices.SharePermissionInput {
	if permissions.Read.DirectorySIDs == nil && permissions.Write.DirectorySIDs == nil {
		return nil
	}
	return &directoryservices.SharePermissionInput{ReadSIDs: permissions.Read.DirectorySIDs, WriteSIDs: permissions.Write.DirectorySIDs}
}

func mapShareResponse(share sambaModels.SambaShare) SambaShareResponse {
	guestWriteable := share.GuestOk && !share.ReadOnly

	return SambaShareResponse{
		ID:      share.ID,
		Name:    share.Name,
		Dataset: share.Dataset,
		Enabled: share.Enabled,
		Permissions: SambaPermissionsResponse{
			Read: SambaPrincipalSetResponse{
				Users:     mapUsers(share.ReadOnlyUsers),
				Groups:    mapGroups(share.ReadOnlyGroups),
				Directory: nonNilDirectory(share.DirectoryPermissions.Read),
			},
			Write: SambaPrincipalSetResponse{
				Users:     mapUsers(share.WriteableUsers),
				Groups:    mapGroups(share.WriteableGroups),
				Directory: nonNilDirectory(share.DirectoryPermissions.Write),
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
		AuditEnabled:       share.AuditEnabled,
		AuditRetentionDays: sambaModels.AuditRetentionDaysValue(share.AuditRetentionDays),
		AuditedOperations:  share.AuditedOperations,
		ExtraShareConfig:   share.ExtraShareConfig,
		CreatedAt:          share.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:          share.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func sambaShareServiceErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, samba.ErrInvalidShareConfig) || errors.Is(err, directoryservices.ErrInvalid) ||
		errors.Is(err, samba.ErrDatasetNotShareable) {
		return http.StatusBadRequest
	}
	if errors.Is(err, directoryservices.ErrUnavailable) {
		return http.StatusBadGateway
	}
	if errors.Is(err, directoryservices.ErrBusy) || errors.Is(err, directoryservices.ErrConflict) {
		return http.StatusConflict
	}

	msg := err.Error()

	switch {
	case msg == "share_with_name_exists", msg == "share_with_dataset_exists":
		return http.StatusConflict
	case strings.HasPrefix(msg, "share_not_found"):
		return http.StatusNotFound
	case msg == "guest_only_share_cannot_have_principals",
		msg == "no_principals_selected_and_guests_not_allowed",
		msg == "invalid_share_name",
		msg == "share_name_reserved",
		msg == "share_name_too_long",
		msg == "invalid_create_mask",
		msg == "invalid_directory_mask",
		msg == "dataset_not_found",
		msg == "dataset_not_mounted",
		strings.HasPrefix(msg, "invalid_audit_operation:"),
		strings.HasPrefix(msg, "user_not_found:"),
		strings.HasPrefix(msg, "user_not_pam:"),
		strings.HasPrefix(msg, "group_not_found:"),
		strings.Contains(msg, "dataset_not_filesystem:"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// @Summary Get Samba shares
// @Description Retrieve all configured Samba shares with their dataset name and mountpoint, their state (active, disabled, dataset_missing or dataset_unmounted), datasetBlock when the share uses a dataset that could no longer be shared, and the progress of any job applying access to existing files (permissionsJob).
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
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

		mapped := shareResponses(c, smbService, shares)

		c.JSON(http.StatusOK, internal.APIResponse[[]SambaShareResponse]{
			Status:  "success",
			Message: "shares_retrieved",
			Error:   "",
			Data:    mapped,
		})
	}
}

// @Summary Create a Samba share
// @Description Create a Samba share on a mounted ZFS filesystem. Share names must be 1 to 80 characters without control characters, backslashes, slashes, colons, asterisks, question marks, double quotes, angle brackets, pipes, square brackets or commas, must not be global, homes, printers, print$ or ipc$, and must be unique ignoring case. System datasets, boot environments, Sylve data and datasets containing them are refused with dataset_not_shareable and a reason. The share's ACL entries are set on the dataset's top folder and Samba reloads its configuration without disconnecting clients. With applyToExisting, a background job also applies the access to the files and folders already in the dataset, without crossing into other datasets; its progress appears in permissionsJob. If the change was saved but Samba could not apply it, the response is 500 with message samba_apply_failed, with the saved share as data.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateSambaShareRequest true "Samba share settings"
// @Success 201 {object} internal.APIResponse[SambaShareResponse] "Created share"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/shares [post]
func CreateShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CreateSambaShareRequest
		if err := strictJSONBind(c, &request); err != nil {
			sambaRequestBindError(c, err)
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

		auditEnabled := false
		if request.AuditEnabled != nil {
			auditEnabled = *request.AuditEnabled
		}
		auditRetentionDays := sambaModels.DefaultAuditRetentionDays
		if request.AuditRetentionDays != nil {
			auditRetentionDays = *request.AuditRetentionDays
		}

		enabled := true
		if request.Enabled != nil {
			enabled = *request.Enabled
		}

		if request.AuditedOperations == nil {
			request.AuditedOperations = []string{}
		}

		ctx := c.Request.Context()
		id, err := smbService.CreateShare(
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
			auditEnabled,
			auditRetentionDays,
			request.AuditedOperations,
			enabled,
			request.ExtraShareConfig,
			directoryPermissionRequest(request.Permissions),
		)
		if request.ApplyToExisting {
			startShareAccessJob(c, smbService, id, nil, err)
		}
		if err != nil {
			sambaShareFailure(c, smbService, "failed_to_create_share", id, err)
			return
		}
		sambaShareSaved(c, smbService, http.StatusCreated, "share_created", id)
	}
}

func sambaShareFailure(c *gin.Context, smbService *samba.Service, message string, id uint, err error) {
	var data any
	if errors.Is(err, samba.ErrSambaApplyFailed) {
		message = samba.ErrSambaApplyFailed.Error()
		if id != 0 {
			if share, loadErr := smbService.GetShare(id); loadErr == nil {
				data = shareResponses(c, smbService, []sambaModels.SambaShare{share})[0]
			}
		}
	}
	c.JSON(sambaShareServiceErrorStatus(err), internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    data,
	})
}

func sambaShareSaved(c *gin.Context, smbService *samba.Service, status int, message string, id uint) {
	response := internal.APIResponse[*SambaShareResponse]{Status: "success", Message: message}
	if share, err := smbService.GetShare(id); err != nil {
		logger.L.Warn().Err(err).Uint("share_id", id).Msg("could not read back the saved Samba share")
	} else {
		mapped := shareResponses(c, smbService, []sambaModels.SambaShare{share})[0]
		response.Data = &mapped
	}
	c.JSON(status, response)
}

func parseShareID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_share_id", Error: err.Error()})
		return 0, false
	}
	return uint(id), true
}

func startShareAccessJob(c *gin.Context, smbService *samba.Service, id uint, previous *sambaModels.SambaShare, err error) {
	if id == 0 || (err != nil && !errors.Is(err, samba.ErrSambaApplyFailed)) {
		return
	}
	if jobErr := smbService.ApplyShareAccessToExisting(c.Request.Context(), id, previous); jobErr != nil {
		logger.L.Warn().Err(jobErr).Uint("share_id", id).Msg("could not apply Samba share access to existing files")
	}
}

// @Summary Update a Samba share
// @Description Update a Samba share. Omitted enabled and extraShareConfig keep their current values; other omitted fields are reset. Share names must be 1 to 80 characters without control characters, backslashes, slashes, colons, asterisks, question marks, double quotes, angle brackets, pipes, square brackets or commas, must not be global, homes, printers, print$ or ipc$, and must be unique ignoring case. System datasets, boot environments, Sylve data and datasets containing them are refused with dataset_not_shareable and a reason. Disabling removes the share's ACL entries from the dataset's top folder and stops a running access job. With applyToExisting, the background job applies the new access to existing files and folders and removes users and groups that lost access; guest access that is turned off is only removed from the top folder. If the change was saved but Samba could not apply it, the response is 500 with message samba_apply_failed, with the saved share as data.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Param request body UpdateSambaShareRequest true "Samba share settings"
// @Success 200 {object} internal.APIResponse[SambaShareResponse] "Updated share"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/shares/{id} [put]
func UpdateShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseShareID(c)
		if !ok {
			return
		}

		var request UpdateSambaShareRequest
		if err := strictJSONBind(c, &request); err != nil {
			sambaRequestBindError(c, err)
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

		auditEnabled := false
		if request.AuditEnabled != nil {
			auditEnabled = *request.AuditEnabled
		}
		auditRetentionDays := sambaModels.DefaultAuditRetentionDays
		if request.AuditRetentionDays != nil {
			auditRetentionDays = *request.AuditRetentionDays
		}

		if request.AuditedOperations == nil {
			request.AuditedOperations = []string{}
		}

		var previous *sambaModels.SambaShare
		if request.ApplyToExisting {
			if share, err := smbService.GetShare(id); err == nil {
				previous = &share
			}
		}
		ctx := c.Request.Context()
		err := smbService.UpdateShare(
			ctx,
			id,
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
			auditEnabled,
			auditRetentionDays,
			request.AuditedOperations,
			request.Enabled,
			request.ExtraShareConfig,
			directoryPermissionRequest(request.Permissions),
		)
		if request.ApplyToExisting {
			startShareAccessJob(c, smbService, id, previous, err)
		}
		if err != nil {
			sambaShareFailure(c, smbService, "failed_to_update_share", id, err)
			return
		}
		sambaShareSaved(c, smbService, http.StatusOK, "share_updated", id)
	}
}

// @Summary Enable or disable a Samba share
// @Description Set whether a Samba share is included in the active configuration. Enabling checks the dataset again and refuses datasets that cannot be shared; disabling removes the share's ACL entries from the dataset's top folder and stops a running access job. If the change was saved but Samba could not apply it, the response is 500 with message samba_apply_failed.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Param request body SetSambaShareEnabledRequest true "Enabled state"
// @Success 200 {object} internal.APIResponse[SambaShareResponse] "Updated share"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Another Samba or directory services change is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/shares/{id}/enabled [put]
func SetShareEnabled(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseShareID(c)
		if !ok {
			return
		}

		var request SetSambaShareEnabledRequest
		if err := strictJSONBind(c, &request); err != nil {
			sambaRequestBindError(c, err)
			return
		}

		if err := smbService.SetShareEnabled(c.Request.Context(), id, request.Enabled); err != nil {
			sambaShareFailure(c, smbService, "failed_to_set_share_enabled", id, err)
			return
		}
		sambaShareSaved(c, smbService, http.StatusOK, "share_state_updated", id)
	}
}

// @Summary Delete a Samba share
// @Description Delete a Samba share. The dataset and its files are kept; the share's ACL entries are removed from the dataset's top folder and a running access job is stopped. If the change was saved but Samba could not apply it, the response is 500 with message samba_apply_failed (the share is deleted).
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Another Samba or directory services change is in progress"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/shares/{id} [delete]
func DeleteShare(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseShareID(c)
		if !ok {
			return
		}

		ctx := c.Request.Context()
		if err := smbService.DeleteShare(ctx, id); err != nil {
			sambaShareFailure(c, smbService, "failed_to_delete_share", 0, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "share_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
