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
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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
	SambaAction     string `json:"sambaAction" binding:"omitempty,oneof=keep upsert remove"`
}

func writeUserBindingError(c *gin.Context, err error, request any) {
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

func positiveUserIDParam(c *gin.Context) (uint, bool) {
	raw := strings.TrimSpace(c.Param("userId"))
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
		writeAuthCodeError(c, http.StatusBadRequest, "invalid_user_id")
		return 0, false
	}
	return uint(parsed), true
}

func classifyUserServiceError(err error) (int, string) {
	var operationError *auth.UserOperationError
	if errors.As(err, &operationError) {
		switch operationError.Kind {
		case auth.UserOperationValidation:
			return http.StatusBadRequest, operationError.Code
		case auth.UserOperationNotFound:
			return http.StatusNotFound, operationError.Code
		case auth.UserOperationConflict:
			return http.StatusConflict, operationError.Code
		case auth.UserOperationDependency:
			return http.StatusServiceUnavailable, operationError.Code
		case auth.UserOperationPartial, auth.UserOperationInternal:
			return http.StatusInternalServerError, operationError.Code
		}
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound, "user_not_found"
	}

	errorText := err.Error()
	validationCodes := []string{
		"invalid_email_format",
		"invalid_password_length",
		"invalid_user_source",
		"invalid_username_format",
		"invalid_username_length",
		"invalid_uid",
		"invalid_shell",
		"invalid_home_directory",
		"invalid_home_directory_permissions",
		"invalid_samba_action",
	}
	for _, code := range validationCodes {
		if strings.Contains(errorText, code) {
			return http.StatusBadRequest, code
		}
	}

	if strings.Contains(errorText, "a_local_user_with_this_username_already_exists") ||
		strings.Contains(errorText, "a_pam_user_with_this_username_already_exists") ||
		strings.Contains(errorText, "user_source_conflict") {
		return http.StatusConflict, "user_source_conflict"
	}
	if strings.Contains(errorText, "username_already_exists") ||
		strings.Contains(strings.ToLower(errorText), "unique constraint") {
		return http.StatusConflict, "username_already_exists"
	}

	protectedCodes := []string{
		"cannot_change_admin_username",
		"cannot_delete_admin_user",
		"cannot_delete_root_user",
		"cannot_demote_admin_user",
		"cannot_lock_admin_user",
	}
	for _, code := range protectedCodes {
		if strings.Contains(errorText, code) {
			return http.StatusConflict, code
		}
	}

	if strings.Contains(errorText, "user_not_found") ||
		strings.Contains(errorText, "failed_to_get_user") {
		return http.StatusNotFound, "user_not_found"
	}

	return http.StatusInternalServerError, "internal_server_error"
}

func writeUserServiceError(c *gin.Context, failureMessage string, err error) {
	status, code := classifyUserServiceError(err)
	if status >= http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", failureMessage).Msg("user_operation_failed")
		c.JSON(status, internal.APIResponse[any]{
			Status:  "error",
			Message: failureMessage,
			Error:   code,
			Data:    nil,
		})
		return
	}

	writeAuthCodeError(c, status, code)
}

// @Summary List users
// @Description List public user records, optionally filtered by account source
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Param source query string false "Account source" Enums(local,pam)
// @Success 200 {object} internal.APIResponse[[]PublicUser] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/users [get]
func ListUsersHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		source := strings.TrimSpace(c.Query("source"))
		if source != "" && source != "local" && source != "pam" {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_user_source")
			return
		}

		users, err := authService.ListUsersBySource(source)
		if err != nil {
			writeUserServiceError(c, "failed_to_list_users", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]PublicUser]{
			Status:  "success",
			Message: "users_listed_successfully",
			Error:   "",
			Data:    publicUsersFromModels(users),
		})
	}
}

// @Summary Create local user
// @Description Create a new local Sylve user
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateUserRequest true "Local user"
// @Success 201 {object} internal.APIResponse[UserMutationResult] "User Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/users [post]
func CreateUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeUserBindingError(c, err, CreateUserRequest{})
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
			writeUserServiceError(c, "failed_to_create_user", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[UserMutationResult]{
			Status:  "success",
			Message: "user_created_successfully",
			Error:   "",
			Data: UserMutationResult{
				ID:       model.ID,
				Username: model.Username,
			},
		})
	}
}

// @Summary Create PAM User
// @Description Create one managed Unix/PAM account and its corresponding Sylve user record, synchronizing the required password and optional Samba credential
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreatePamUserRequest true "Create PAM User Request"
// @Success 201 {object} internal.APIResponse[UserMutationResult] "PAM User Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/pam [post]
func CreatePamUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreatePamUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeUserBindingError(c, err, CreatePamUserRequest{})
			return
		}

		admin := false
		if req.Admin != nil {
			admin = *req.Admin
		}

		user := &models.User{
			Username:        req.Username,
			FullName:        req.FullName,
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
			PrimaryGroupID:  req.PrimaryGroupID,
		}

		opts := auth.CreateUserOpts{
			NewPrimaryGroup: req.NewPrimaryGroup,
			AuxGroupIDs:     req.AuxGroupIDs,
			CreateSamba:     req.CreateSamba,
		}

		err := authService.CreatePamUser(user, opts)
		if err != nil {
			writeUserServiceError(c, "failed_to_create_pam_user", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[UserMutationResult]{
			Status:  "success",
			Message: "pam_user_created_successfully",
			Error:   "",
			Data: UserMutationResult{
				ID:       user.ID,
				Username: user.Username,
			},
		})
	}
}

// @Summary Delete user
// @Description Delete a user by its positive database ID; PAM-backed users also lose their managed Unix account, home directory, and associated integrations
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Param userId path uint true "User ID"
// @Success 200 {object} internal.APIResponse[UserMutationResult] "User Deleted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/{userId} [delete]
func DeleteUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := positiveUserIDParam(c)
		if !ok {
			return
		}

		user, err := authService.GetUserByID(userID)
		if err != nil {
			writeUserServiceError(c, "failed_to_delete_user", err)
			return
		}

		if err := authService.DeleteUser(userID); err != nil {
			writeUserServiceError(c, "failed_to_delete_user", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[UserMutationResult]{
			Status:  "success",
			Message: "user_deleted_successfully",
			Error:   "",
			Data: UserMutationResult{
				ID:       user.ID,
				Username: user.Username,
			},
		})
	}
}

// @Summary Update user
// @Description Replace the editable representation of a user identified by its positive database ID; a PAM password change synchronizes Unix and Sylve credentials and Samba intent is explicit
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param userId path uint true "User ID"
// @Param request body EditUserRequest true "User settings"
// @Success 200 {object} internal.APIResponse[UserMutationResult] "User Updated"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/{userId} [put]
func EditUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := positiveUserIDParam(c)
		if !ok {
			return
		}

		var req EditUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeUserBindingError(c, err, EditUserRequest{})
			return
		}

		var admin bool
		if req.Admin != nil {
			admin = *req.Admin
		} else {
			admin = false
		}

		err := authService.EditUser(userID, auth.EditUserOpts{
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
			SambaAction:     req.SambaAction,
		})

		if err != nil {
			writeUserServiceError(c, "failed_to_edit_user", err)
			return
		}

		user, err := authService.GetUserByID(userID)
		if err != nil {
			writeUserServiceError(c, "failed_to_edit_user", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[UserMutationResult]{
			Status:  "success",
			Message: "user_edited_successfully",
			Error:   "",
			Data: UserMutationResult{
				ID:       user.ID,
				Username: user.Username,
			},
		})
	}
}

type NextUIDResult struct {
	NextUID int `json:"nextUID"`
}

// @Summary Get next Unix UID
// @Description Return the next available Unix UID in the managed account range
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[NextUIDResult] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/uid/next [get]
func GetNextUIDHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		nextUID, err := authService.GetNextUID()
		if err != nil {
			writeUserServiceError(c, "failed_to_get_next_uid", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[NextUIDResult]{
			Status:  "success",
			Message: "next_uid_retrieved",
			Error:   "",
			Data:    NextUIDResult{NextUID: nextUID},
		})
	}
}

type UserCapabilities struct {
	DoasAvailable bool `json:"doasAvailable"`
}

// @Summary Get user-management capabilities
// @Description Return optional account-management integrations available on this node
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[UserCapabilities] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Router /auth/users/capabilities [get]
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

type ImportUserRequest struct {
	Username string `json:"username" binding:"required,min=3,max=128"`
	Password string `json:"password" binding:"omitempty,min=8,max=128"`
	Admin    *bool  `json:"admin" binding:"required"`
}

type CreatePamUserRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=128"`
	FullName        string `json:"fullName"`
	Password        string `json:"password" binding:"required,min=8,max=128"`
	Email           string `json:"email"`
	Admin           *bool  `json:"admin" binding:"required"`
	UID             int    `json:"uid" binding:"required,gte=1000,lte=65533"`
	Shell           string `json:"shell"`
	HomeDirectory   string `json:"homeDirectory"`
	HomeDirPerms    uint   `json:"homeDirPerms" binding:"required,gte=1,lte=511"`
	SSHPublicKey    string `json:"sshPublicKey"`
	DisablePassword bool   `json:"disablePassword"`
	Locked          bool   `json:"locked"`
	DoasEnabled     bool   `json:"doasEnabled"`
	NewPrimaryGroup bool   `json:"newPrimaryGroup"`
	PrimaryGroupID  *uint  `json:"primaryGroupId"`
	AuxGroupIDs     []uint `json:"auxGroupIds"`
	CreateSamba     bool   `json:"createSamba"`
}

// @Summary Import Unix user
// @Description Adopt an existing Unix account as a managed PAM-backed Sylve user without changing its Unix password or group membership
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ImportUserRequest true "Import user settings"
// @Success 201 {object} internal.APIResponse[UserMutationResult] "User Imported"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/import [post]
func ImportUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ImportUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeUserBindingError(c, err, ImportUserRequest{})
			return
		}

		admin := false
		if req.Admin != nil {
			admin = *req.Admin
		}

		user, err := authService.ImportUser(req.Username, req.Password, admin)

		if err != nil {
			writeUserServiceError(c, "failed_to_import_user", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[UserMutationResult]{
			Status:  "success",
			Message: "user_imported_successfully",
			Error:   "",
			Data: UserMutationResult{
				ID:       user.ID,
				Username: user.Username,
			},
		})
	}
}

// @Summary List importable Unix users
// @Description List eligible Unix accounts that do not yet have a Sylve user record
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]auth.ImportableUnixUser] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/importable [get]
func ListImportableUsersHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := authService.ListImportableUnixUsers()

		if err != nil {
			writeUserServiceError(c, "failed_to_list_importable_users", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]auth.ImportableUnixUser]{
			Status:  "success",
			Message: "importable_users_listed",
			Error:   "",
			Data:    users,
		})
	}
}
