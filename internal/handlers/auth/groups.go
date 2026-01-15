// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/auth"

	"github.com/gin-gonic/gin"
)

type CreateGroupRequest struct {
	Name    string   `json:"name" binding:"required"`
	Members []string `json:"members" binding:"required"`
}

type AddUsersToGroupRequest struct {
	Usernames []string `json:"usernames" binding:"required"`
	Group     string   `json:"group" binding:"required"`
}

// @Summary List Groups
// @Description List all groups in the system
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]models.Group] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups [get]
func ListGroupsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := authService.ListGroups()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_groups",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]models.Group]{
			Status:  "success",
			Message: "groups_listed_successfully",
			Error:   "",
			Data:    groups,
		})
	}
}

// @Summary Create Group
// @Description Create a new group with specified members
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateGroupRequest true "Group creation request"
// @Success 201 {object} internal.APIResponse[any] "Group created successfully"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups [post]
func CreateGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := authService.CreateGroup(req.Name, req.Members); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_group",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "group_created_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Group
// @Description Delete a group by ID
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Group ID"
// @Success 204 {object} internal.APIResponse[any] "Group deleted successfully"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups/:id [delete]
func DeleteGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "group_id_required",
				Error:   "group ID is required",
				Data:    nil,
			})
			return
		}

		idInt, err := strconv.Atoi(id)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_group_id",
				Error:   "invalid group ID format",
				Data:    nil,
			})
			return
		}

		if err := authService.DeleteGroup(uint(idInt)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_group",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "group_deleted_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Add Users to Group
// @Description Add users to a specified group
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddUsersToGroupRequest true "Add users to group request"
// @Success 200 {object} internal.APIResponse[any] "User added to group successfully"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups/users [post]
func AddUsersToGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AddUsersToGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := authService.AddUsersToGroup(req.Usernames, req.Group); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_add_user_to_group",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "user_added_to_group_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update Group Members
// @Description Update the members of a specified group
// @Tags Groups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddUsersToGroupRequest true "Update group members request"
// @Success 200 {object} internal.APIResponse[any] "Group members updated successfully"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/groups/members [put]
func UpdateGroupMembersHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AddUsersToGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := authService.SyncGroupMembers(req.Usernames, req.Group); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_group_members",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "group_members_updated_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}
