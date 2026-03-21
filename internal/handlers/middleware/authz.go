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
		authType := strings.TrimSpace(c.GetString("AuthType"))
		if authType != "sylve" && authType != authService.AuthTypeSylvePasskey {
			c.AbortWithStatusJSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "only_admin_allowed",
				Error:   "only_admin_allowed",
				Data:    nil,
			})
			return
		}

		userID := c.GetUint("UserID")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credentials",
				Error:   "invalid_credentials",
				Data:    nil,
			})
			return
		}

		user, err := service.GetUserByID(userID)
		if err != nil || user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credentials",
				Error:   "invalid_credentials",
				Data:    nil,
			})
			return
		}

		if !user.Admin {
			c.AbortWithStatusJSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "only_admin_allowed",
				Error:   "only_admin_allowed",
				Data:    nil,
			})
			return
		}

		c.Next()
	}
}
