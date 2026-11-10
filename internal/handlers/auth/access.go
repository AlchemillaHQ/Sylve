// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"net/http"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

// These keep User.Modify and Permissions.Modify from turning into more than they say.

// A change to another user's account. Taking over root@pam or admin would make anyone a
// superuser, and a PAM account is a host account.
func allowUserChange(c *gin.Context, authService *auth.Service, userID uint) bool {
	if !middleware.Allowed(c, authService, "/access", auth.UserModify) {
		return false
	}
	user, err := authService.GetUserByID(userID)
	if err != nil {
		return true // the handler reports the missing user
	}
	if auth.PrincipalOf(user).Superuser && !middleware.RequireSuperuser(c, authService) {
		return false
	}
	return user.Source != "pam" || middleware.RequireHostAdmin(c, authService)
}

// Setting another user's password or adding a passkey to their account hands over everything they
// hold, so it needs all of it too.
func allowCredentialChange(c *gin.Context, authService *auth.Service, userID uint) bool {
	if userID == c.GetUint("UserID") || c.GetString("ClusterCaller") == auth.ClusterCallerNode || !authService.RBACActive() {
		return true
	}
	user, err := authService.GetUserByID(userID)
	if err != nil {
		return true // the handler reports the missing user
	}
	editor, err := middleware.RequestPrincipal(c, authService)
	if err != nil {
		writeAuthCodeError(c, http.StatusUnauthorized, "invalid_credentials")
		return false
	}
	path, privilege, err := authService.Covers(editor, auth.PrincipalOf(user))
	if err != nil {
		writeAuthCodeError(c, http.StatusInternalServerError, "permission_check_failed")
		return false
	}
	return path == "" || middleware.Allowed(c, authService, path, privilege)
}

// No one hands out privileges they don't hold there themselves.
func allowGrantChange(c *gin.Context, authService *auth.Service, grant clusterModels.ACLGrant, setting bool) bool {
	if !middleware.Allowed(c, authService, grant.Path, auth.PermissionsModify) {
		return false
	}
	if !setting {
		return true
	}
	privileges, err := authService.RolePrivileges(grant.Role)
	if err != nil {
		writeAuthCodeError(c, http.StatusInternalServerError, "failed_to_load_roles")
		return false
	}
	return middleware.Allowed(c, authService, grant.Path, privileges...)
}
