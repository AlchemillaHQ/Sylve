// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

var aclValidationErrors = []string{
	"invalid_acl_path", "invalid_acl_subject", "unknown_role", "invalid_role_name", "builtin_role",
	"role_privileges_required", "unknown_privilege", "unknown_group", "invalid_group_name", "invalid_group_member",
}

func writeACLError(c *gin.Context, operation string, err error) {
	code, status := err.Error(), http.StatusInternalServerError
	switch {
	case slices.Contains(aclValidationErrors, code) || strings.HasPrefix(code, "unknown_privilege") || strings.HasPrefix(code, "invalid_group_member"):
		status = http.StatusBadRequest
	case code == "acl_writes_unavailable" || errors.Is(err, cluster.ErrClusterProtocolOutdated):
		status = http.StatusServiceUnavailable
		if code != "acl_writes_unavailable" {
			code = cluster.ProposalErrorCode(err)
		}
	default:
		logger.L.Error().Err(err).Str("operation", operation).Msg("acl_operation_failed")
		code = cluster.ProposalErrorCode(err)
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: operation, Error: code})
}

// @Summary List grants
// @Description Every grant in the cluster: who holds which role on which path
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ACLGrant] "Grants"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/grants [get]
func ListGrantsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		grants, err := authService.ListGrants()
		if err != nil {
			writeACLError(c, "failed_to_list_grants", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ACLGrant]{Status: "success", Message: "grants_listed", Data: grants})
	}
}

// @Summary Set grant
// @Description Give a user ("name@pam" or "name@sylve") or a group a role on a path, or change whether it propagates
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body clusterModels.ACLGrant true "Grant"
// @Success 200 {object} internal.APIResponse[any] "Grant Set"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/grants [post]
func SetGrantHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var grant clusterModels.ACLGrant
		if err := c.ShouldBindJSON(&grant); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		if !allowGrantChange(c, authService, grant, true) {
			return
		}
		if err := authService.SetGrant(c.Request.Context(), grant); err != nil {
			writeACLError(c, "failed_to_set_grant", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "grant_set"})
	}
}

// @Summary Delete grant
// @Description Remove a role from a user or group on a path
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body clusterModels.ACLGrant true "Grant"
// @Success 200 {object} internal.APIResponse[any] "Grant Deleted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/grants [delete]
func DeleteGrantHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var grant clusterModels.ACLGrant
		if err := c.ShouldBindJSON(&grant); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		if !allowGrantChange(c, authService, grant, false) {
			return
		}
		if err := authService.DeleteGrant(c.Request.Context(), grant); err != nil {
			writeACLError(c, "failed_to_delete_grant", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "grant_deleted"})
	}
}

// @Summary List roles
// @Description Built-in and custom roles with their privileges
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]auth.RoleInfo] "Roles"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/roles [get]
func ListRolesHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		roles, err := authService.ListRoles()
		if err != nil {
			writeACLError(c, "failed_to_list_roles", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]auth.RoleInfo]{Status: "success", Message: "roles_listed", Data: roles})
	}
}

type setRoleRequest struct {
	Privileges []string `json:"privileges" binding:"required"`
}

// @Summary Set custom role
// @Description Create a custom role or replace its privileges; built-in roles can't be changed
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Role name"
// @Param request body setRoleRequest true "Privileges"
// @Success 200 {object} internal.APIResponse[any] "Role Set"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/roles/{name} [put]
func SetRoleHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setRoleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := authService.SetRole(c.Request.Context(), c.Param("name"), req.Privileges); err != nil {
			writeACLError(c, "failed_to_set_role", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "role_set"})
	}
}

// @Summary Delete custom role
// @Description Delete a custom role and every grant of it
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Role name"
// @Success 200 {object} internal.APIResponse[any] "Role Deleted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/roles/{name} [delete]
func DeleteRoleHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := authService.DeleteRole(c.Request.Context(), c.Param("name")); err != nil {
			writeACLError(c, "failed_to_delete_role", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "role_deleted"})
	}
}

// @Summary List permission groups
// @Description Cluster-wide groups for grants, with their user@realm members; separate from Unix groups
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ACLGroup] "Groups"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/acl-groups [get]
func ListACLGroupsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := authService.ListACLGroups()
		if err != nil {
			writeACLError(c, "failed_to_list_groups", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.ACLGroup]{Status: "success", Message: "groups_listed", Data: groups})
	}
}

type setACLGroupRequest struct {
	Members []string `json:"members"`
}

// @Summary Set permission group
// @Description Create a permission group or replace its members (user@realm)
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Group name"
// @Param request body setACLGroupRequest true "Members"
// @Success 200 {object} internal.APIResponse[any] "Group Set"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/acl-groups/{name} [put]
func SetACLGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setACLGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := authService.SetACLGroup(c.Request.Context(), c.Param("name"), req.Members); err != nil {
			writeACLError(c, "failed_to_set_group", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "group_set"})
	}
}

// @Summary Delete permission group
// @Description Delete a permission group and every grant to it
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Group name"
// @Success 200 {object} internal.APIResponse[any] "Group Deleted"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/acl-groups/{name} [delete]
func DeleteACLGroupHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := authService.DeleteACLGroup(c.Request.Context(), c.Param("name")); err != nil {
			writeACLError(c, "failed_to_delete_group", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "group_deleted"})
	}
}

type PermissionsResponse struct {
	Superuser bool                           `json:"superuser"`
	Paths     map[string]auth.PathPrivileges `json:"paths"`
	// Nodes maps hostnames, which the UI shows, to the IDs that /nodes/<id> grants use.
	Nodes map[string]string `json:"nodes"`
}

// @Summary Own permissions
// @Description The signed-in user's privileges on "/" and on every path they have a grant on, and on the paths below each one
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[PermissionsResponse] "Permissions"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/permissions [get]
func PermissionsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := middleware.RequestPrincipal(c, authService)
		if err != nil {
			writeAuthCodeError(c, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		paths, err := authService.Permissions(principal)
		if err != nil {
			writeACLError(c, "failed_to_list_permissions", err)
			return
		}
		nodes, err := authService.NodeIDs()
		if err != nil {
			writeACLError(c, "failed_to_list_permissions", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[PermissionsResponse]{
			Status: "success", Message: "permissions_listed",
			Data: PermissionsResponse{Superuser: principal.Superuser, Paths: paths, Nodes: nodes},
		})
	}
}
