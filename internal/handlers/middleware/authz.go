// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

const principalKey = "Principal"

func RequireLocalSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("AuthScope") != "local" || c.GetUint("UserID") == 0 {
			abortAuthentication(c, http.StatusForbidden, "local_session_required")
			return
		}
		c.Next()
	}
}

var localNodeID = sync.OnceValue(func() string {
	id, _ := utils.GetSystemUUID()
	return id
})

func resolveRulePath(c *gin.Context, template string) string {
	parts := strings.Split(template, "/")
	for i, part := range parts {
		switch {
		case part == "{node}":
			parts[i] = localNodeID()
		case strings.HasPrefix(part, ":"):
			parts[i] = c.Param(part[1:])
		}
	}
	return strings.Join(parts, "/")
}

func RequestPrincipal(c *gin.Context, service *authService.Service) (authService.Principal, error) {
	if principal, ok := c.Get(principalKey); ok {
		return principal.(authService.Principal), nil
	}
	user, err := service.GetUserByID(c.GetUint("UserID"))
	if err != nil {
		return authService.Principal{}, err
	}
	principal := authService.PrincipalOf(user)
	c.Set(principalKey, principal)
	return principal, nil
}

func deny(c *gin.Context, code string, data any) {
	c.AbortWithStatusJSON(http.StatusForbidden, internal.APIResponse[any]{Status: "error", Message: code, Error: code, Data: data})
}

// Allowed refuses the request unless the privileges a handler needs beyond its route's are held.
func Allowed(c *gin.Context, service *authService.Service, path string, privileges ...string) bool {
	if c.GetString("ClusterCaller") == authService.ClusterCallerNode || !service.RBACActive() {
		return true
	}
	principal, err := RequestPrincipal(c, service)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_credentials")
		return false
	}
	allowed, err := service.Can(principal, path, privileges...)
	if err != nil {
		abortAuthentication(c, http.StatusInternalServerError, "permission_check_failed")
		return false
	}
	if !allowed {
		deny(c, "permission_denied", gin.H{"path": path, "privileges": privileges})
		return false
	}
	return true
}

// Checker is Allowed for handlers that filter what they return: it answers instead of refusing.
// It returns false after aborting the request when the check itself fails.
func Checker(c *gin.Context, service *authService.Service) (func(path string, privileges ...string) bool, bool) {
	if c.GetString("ClusterCaller") == authService.ClusterCallerNode || !service.RBACActive() {
		return func(string, ...string) bool { return true }, true
	}
	principal, err := RequestPrincipal(c, service)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_credentials")
		return nil, false
	}
	can, err := service.Checker(principal)
	if err != nil {
		abortAuthentication(c, http.StatusInternalServerError, "permission_check_failed")
		return nil, false
	}
	return func(path string, privileges ...string) bool {
		allowed, err := can(path, privileges...)
		return err == nil && allowed
	}, true
}

// RequireHostAdmin needs Sys.Modify on this node: restricted settings amount to host root.
func RequireHostAdmin(c *gin.Context, service *authService.Service) bool {
	return Allowed(c, service, NodePath(), authService.SysModify)
}

func Visible[T any](c *gin.Context, service *authService.Service, items []T, privilege string, path func(T) string) ([]T, bool) {
	can, ok := Checker(c, service)
	if !ok {
		return nil, false
	}
	return slices.DeleteFunc(items, func(item T) bool { return !can(path(item), privilege) }), true
}

func NodePath() string {
	return "/nodes/" + localNodeID()
}

// Node callers are limited by clusterRequestAllowed instead.
func authorize(c *gin.Context, service *authService.Service) bool {
	if c.GetString("ClusterCaller") == authService.ClusterCallerNode {
		return true
	}
	rule, ok := routeRuleFor(c.Request.Method, c.FullPath())
	if !ok || rule.kind == ruleNodeOnly {
		deny(c, "permission_denied", nil)
		return false
	}
	switch rule.kind {
	case rulePrivilege:
		return Allowed(c, service, resolveRulePath(c, rule.path), rule.privs...)
	case ruleSuperuser:
		return RequireSuperuser(c, service)
	}
	return true
}

func RequireSuperuser(c *gin.Context, service *authService.Service) bool {
	if c.GetString("ClusterCaller") == authService.ClusterCallerNode || !service.RBACActive() {
		return true
	}
	principal, err := RequestPrincipal(c, service)
	if err != nil {
		abortAuthentication(c, http.StatusUnauthorized, "invalid_credentials")
		return false
	}
	if !principal.Superuser {
		deny(c, "superuser_required", nil)
		return false
	}
	return true
}
