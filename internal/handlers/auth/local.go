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
	"github.com/alchemillahq/sylve/pkg/system"

	"github.com/gin-gonic/gin"
)

type CreateUserRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=128"`
	FullName        string `json:"fullName"`
	Password        string `json:"password"`
	Email           string `json:"email"`
	Admin           *bool  `json:"admin" binding:"required"`
	UID             int    `json:"uid"`
	Shell           string `json:"shell"`
	HomeDirectory   string `json:"homeDirectory"`
	HomeDirPerms    uint   `json:"homeDirPerms"`
	SSHPublicKey    string `json:"sshPublicKey"`
	DisablePassword bool   `json:"disablePassword"`
	Locked          bool   `json:"locked"`
	DoasEnabled     bool   `json:"doasEnabled"`
	NewPrimaryGroup bool   `json:"newPrimaryGroup"`
	PrimaryGroupID  *uint  `json:"primaryGroupId"`
	AuxGroupIDs     []uint `json:"auxGroupIds"`
}

type EditUserRequest struct {
	ID              uint   `json:"id" binding:"required"`
	FullName        string `json:"fullName"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	Email           string `json:"email"`
	Admin           *bool  `json:"admin" binding:"required"`
	UID             int    `json:"uid"`
	Shell           string `json:"shell"`
	HomeDirectory   string `json:"homeDirectory"`
	HomeDirPerms    uint   `json:"homeDirPerms"`
	SSHPublicKey    string `json:"sshPublicKey"`
	DisablePassword bool   `json:"disablePassword"`
	Locked          bool   `json:"locked"`
	DoasEnabled     bool   `json:"doasEnabled"`
	NewPrimaryGroup bool   `json:"newPrimaryGroup"`
	PrimaryGroupID  *uint  `json:"primaryGroupId"`
	AuxGroupIDs     []uint `json:"auxGroupIds"`
}

// @Summary List Users
// @Description List all users in the system
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]models.User] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/users [get]
func ListUsersHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := authService.ListUsers()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_users",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]models.User]{
			Status:  "success",
			Message: "users_listed_successfully",
			Error:   "",
			Data:    users,
		})
	}
}

// @Summary Create User
// @Description Create a new local (sylve) user in the system
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateUserRequest true "Create User Request"
// @Success 201 {object} internal.APIResponse[models.User] "User Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/users [post]
func CreateUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		admin := false
		if req.Admin != nil {
			admin = *req.Admin
		}

		var model models.User
		model.Username = req.Username
		model.FullName = req.FullName
		model.Password = req.Password
		model.Email = req.Email
		model.Admin = admin
		model.UID = req.UID
		model.Shell = req.Shell
		model.HomeDirectory = req.HomeDirectory
		model.HomeDirPerms = req.HomeDirPerms
		model.SSHPublicKey = req.SSHPublicKey
		model.DisablePassword = req.DisablePassword
		model.Locked = req.Locked
		model.DoasEnabled = req.DoasEnabled
		model.PrimaryGroupID = req.PrimaryGroupID

		opts := auth.CreateUserOpts{
			NewPrimaryGroup: req.NewPrimaryGroup,
			AuxGroupIDs:     req.AuxGroupIDs,
		}

		err := authService.CreateUser(&model, opts)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_user",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "user_created_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete User
// @Description Delete a local (sylve) user from the system
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "User ID"
// @Success 204 {object} internal.APIResponse[any] "User Deleted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/users/{id} [delete]
func DeleteUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		if id == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_user_id",
				Error:   "user_id_is_required",
				Data:    nil,
			})
			return
		}

		idInt, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_user_id",
				Error:   "invalid_user_id_format",
				Data:    nil,
			})
			return
		}

		err = authService.DeleteUser(uint(idInt))

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_user",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "user_deleted_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

func EditUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req EditUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "invalid_request: " + err.Error(),
				Data:    nil,
			})
			return
		}

		var admin bool
		if req.Admin != nil {
			admin = *req.Admin
		} else {
			admin = false
		}

		err := authService.EditUser(req.ID, auth.EditUserOpts{
			FullName:        req.FullName,
			Username:        req.Username,
			Password:        req.Password,
			Email:           req.Email,
			Admin:           admin,
			UID:             req.UID,
			Shell:           req.Shell,
			HomeDirectory:   req.HomeDirectory,
			HomeDirPerms:    req.HomeDirPerms,
			SSHPublicKey:    req.SSHPublicKey,
			DisablePassword: req.DisablePassword,
			Locked:          req.Locked,
			DoasEnabled:     req.DoasEnabled,
			NewPrimaryGroup: req.NewPrimaryGroup,
			PrimaryGroupID:  req.PrimaryGroupID,
			AuxGroupIDs:     req.AuxGroupIDs,
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_edit_user",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "user_edited_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

func GetNextUIDHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		nextUID, err := authService.GetNextUID()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_next_uid",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[map[string]int]{
			Status:  "success",
			Message: "next_uid_retrieved",
			Error:   "",
			Data:    map[string]int{"nextUID": nextUID},
		})
	}
}

type UserCapabilities struct {
	DoasAvailable bool `json:"doasAvailable"`
}

func UserCapabilitiesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, internal.APIResponse[UserCapabilities]{
			Status:  "success",
			Message: "capabilities_retrieved",
			Error:   "",
			Data: UserCapabilities{
				DoasAvailable: system.DoasAvailable(),
			},
		})
	}
}
