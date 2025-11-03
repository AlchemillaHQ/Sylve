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

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=128"`
	Password string `json:"password" binding:"required,min=3,max=128"`
	AuthType string `json:"authType"`
	Remember bool   `json:"remember"`
}

type SuccessfulLogin struct {
	Token         string               `json:"token"`
	ClusterToken  string               `json:"clusterToken"`
	Hostname      string               `json:"hostname"`
	NodeID        string               `json:"nodeId"`
	BasicSettings models.BasicSettings `json:"basicSettings"`
}

// @Summary Login
// @Description Create a new JWT token
// @Tags Authentication
// @Param request body LoginRequest true "Login request body"
// @Accept json
// @Produce json
// @Success 200 {object} SuccessfulLogin "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/login [post]
func LoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {

		var r LoginRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			validationErrors := utils.MapValidationErrors(err, LoginRequest{})

			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    validationErrors,
			})
			return
		}

		userId, token, err := authService.CreateJWT(r.Username, r.Password, r.AuthType, r.Remember)

		if err != nil {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credentials",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		clusterToken, _ := authService.CreateClusterJWT(userId, r.Username, r.AuthType, "")
		hostname, err := utils.GetSystemHostname()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		nodeId, err := utils.GetSystemUUID()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		basicSettings, err := authService.GetBasicSettings()
		if err != nil && err.Error() != "basic_settings_not_found" {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "login_successful",
			Error:   "",
			Data: SuccessfulLogin{
				Token:         token,
				ClusterToken:  clusterToken,
				Hostname:      hostname,
				NodeID:        nodeId,
				BasicSettings: basicSettings,
			},
		})
	}
}

// @Summary Logout
// @Description Revoke a JWT token
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/logout [post]
func LogoutHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := utils.GetTokenFromHeader(c.Request.Header)

		if err != nil {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "no_token_provided",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := authService.RevokeJWT(token); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "logout_successful",
			Error:   "",
			Data:    nil,
		})
	}
}
