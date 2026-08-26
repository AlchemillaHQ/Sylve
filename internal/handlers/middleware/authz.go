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
	"strings"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

func RequireLocalAdmin(service *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !localAdminAllowed(c, service) {
			return
		}
		c.Next()
	}
}

func RequireLocalSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("AuthScope") != "local" || c.GetUint("UserID") == 0 {
			abortAuthentication(c, http.StatusForbidden, "local_session_required")
			return
		}
		c.Next()
	}
}

func RequireLocalAdminForWrites(service *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if !localAdminAllowed(c, service) {
			return
		}
		c.Next()
	}
}

func localAdminAllowed(c *gin.Context, service *authService.Service) bool {
	authType := strings.TrimSpace(c.GetString("AuthType"))
	if authType != "sylve" && authType != authService.AuthTypeSylvePasskey && authType != "pam" {
		abortAdminRequired(c)
		return false
	}

	userID := c.GetUint("UserID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_credentials",
			Error:   "invalid_credentials",
			Data:    nil,
		})
		return false
	}

	scope := strings.TrimSpace(c.GetString("AuthScope"))
	if scope == "cluster" {
		if strings.TrimSpace(c.GetString("ClusterTokenUse")) != authService.ClusterTokenUseUserProxy || !c.GetBool("ProxyAdmin") {
			abortAdminRequired(c)
			return false
		}
		return true
	}

	user, err := service.GetUserByID(userID)
	if err != nil || user == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_credentials",
			Error:   "invalid_credentials",
			Data:    nil,
		})
		return false
	}

	if !user.Admin {
		abortAdminRequired(c)
		return false
	}
	return true
}

func abortAdminRequired(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, internal.APIResponse[any]{
		Status:  "error",
		Message: "only_admin_allowed",
		Error:   "only_admin_allowed",
		Data:    nil,
	})
}
