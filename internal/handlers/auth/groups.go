// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type CreateGroupRequest struct {
	Name    string   `json:"name" binding:"required"`
	Members []string `json:"members" binding:"required"`
}

type ReplaceGroupMembersRequest struct {
	Usernames *[]string `json:"usernames" binding:"required"`
}

// GroupMutationResult gives callers and audit presentation a stable group identity.
type GroupMutationResult struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func groupMutationResult(group models.Group) GroupMutationResult {
	return GroupMutationResult{ID: group.ID, Name: group.Name}
}

func writeGroupBindingError(c *gin.Context, err error, request any) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeAuthCodeError(c, http.StatusRequestEntityTooLarge, "request_body_too_large")
		return
	}

	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: "invalid_request_payload",
		Error:   "validation_error",
		Data:    utils.MapValidationErrors(err, request),
	})
}

func positiveGroupIDParam(c *gin.Context) (uint, bool) {
	raw := strings.TrimSpace(c.Param("groupId"))
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
		writeAuthCodeError(c, http.StatusBadRequest, "invalid_group_id")
		return 0, false
	}
	return uint(parsed), true
}

func classifyGroupServiceError(err error) (int, string) {
	var operationError *auth.GroupOperationError
	if errors.As(err, &operationError) {
		switch operationError.Kind {
		case auth.GroupOperationValidation:
			return http.StatusBadRequest, operationError.Code
		case auth.GroupOperationNotFound:
			return http.StatusNotFound, operationError.Code
		case auth.GroupOperationConflict:
			return http.StatusConflict, operationError.Code
		case auth.GroupOperationDependency:
			return http.StatusServiceUnavailable, operationError.Code
		case auth.GroupOperationPartial, auth.GroupOperationInternal:
			return http.StatusInternalServerError, operationError.Code
		}
	}
	return http.StatusInternalServerError, "internal_server_error"
}

func writeGroupServiceError(c *gin.Context, operation string, err error) {
	status, code := classifyGroupServiceError(err)
	if status >= http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", operation).Msg("group_operation_failed")
		c.JSON(status, internal.APIResponse[any]{
			Status:  "error",
			Message: operation,
			Error:   code,
			Data:    nil,
		})
		return
	}
	writeAuthCodeError(c, status, code)
}

// @Summary List groups
// @Description List all managed groups and their public user records
// @Tags Groups
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]PublicGroup] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups [get]
func ListGroupsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := authService.ListGroups()
		if err != nil {
			writeGroupServiceError(c, "failed_to_list_groups", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]PublicGroup]{
			Status:  "success",
			Message: "groups_listed_successfully",
			Error:   "",
			Data:    publicGroupsFromModels(groups),
		})
	}
}

// @Summary Create group
// @Description Create a managed Unix group with PAM-backed members
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateGroupRequest true "Group creation request"
// @Success 201 {object} internal.APIResponse[GroupMutationResult] "Group Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/groups [post]
func CreateGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeGroupBindingError(c, err, CreateGroupRequest{})
			return
		}

		group, err := authService.CreateGroup(req.Name, req.Members)
		if err != nil {
			writeGroupServiceError(c, "failed_to_create_group", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[GroupMutationResult]{
			Status:  "success",
			Message: "group_created_successfully",
			Error:   "",
			Data:    groupMutationResult(group),
		})
	}
}

// @Summary Delete group
// @Description Delete a managed group that is not protected or referenced
// @Tags Groups
// @Produce json
// @Security BearerAuth
// @Param groupId path int true "Positive Group ID"
// @Success 200 {object} internal.APIResponse[GroupMutationResult] "Group Deleted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/groups/{groupId} [delete]
func DeleteGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := positiveGroupIDParam(c)
		if !ok {
			return
		}

		group, err := authService.DeleteGroup(groupID)
		if err != nil {
			writeGroupServiceError(c, "failed_to_delete_group", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[GroupMutationResult]{
			Status:  "success",
			Message: "group_deleted_successfully",
			Error:   "",
			Data:    groupMutationResult(group),
		})
	}
}

// @Summary Replace group members
// @Description Replace all editable members of a managed group; an empty list removes all supplementary members
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param groupId path int true "Positive Group ID"
// @Param request body ReplaceGroupMembersRequest true "Complete group membership"
// @Success 200 {object} internal.APIResponse[GroupMutationResult] "Group Members Replaced"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/groups/{groupId}/members [put]
func UpdateGroupMembersHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := positiveGroupIDParam(c)
		if !ok {
			return
		}

		var req ReplaceGroupMembersRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeGroupBindingError(c, err, ReplaceGroupMembersRequest{})
			return
		}

		usernames := []string{}
		if req.Usernames != nil {
			usernames = *req.Usernames
		}
		group, err := authService.SyncGroupMembers(groupID, usernames)
		if err != nil {
			writeGroupServiceError(c, "failed_to_update_group_members", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[GroupMutationResult]{
			Status:  "success",
			Message: "group_members_updated_successfully",
			Error:   "",
			Data:    groupMutationResult(group),
		})
	}
}
