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
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=128"`
	Password string `json:"password" binding:"required,min=3,max=128"`
	AuthType string `json:"authType" binding:"required,oneof=sylve pam"`
	Remember bool   `json:"remember"`
}

type SuccessfulLogin struct {
	Token         string               `json:"token"`
	Hostname      string               `json:"hostname"`
	NodeID        string               `json:"nodeId"`
	BasicSettings models.BasicSettings `json:"basicSettings"`
}

type LoginConfig struct {
	PAMEnabled bool `json:"pamEnabled"`
}

func setSensitiveAuthResponseHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func writeAuthCodeError(c *gin.Context, status int, code string) {
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

func writeLoginHostnameError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "internal_server_error"
	errorText := message

	if errors.Is(err, utils.ErrSystemHostnameNotConfigured) {
		status = http.StatusServiceUnavailable
		message = utils.ErrSystemHostnameNotConfigured.Error()
		errorText = message
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errorText,
		Data:    nil,
	})
}

func writeLoginServiceError(c *gin.Context, err error) {
	var rateLimitErr *auth.LoginRateLimitError
	if errors.As(err, &rateLimitErr) {
		retryAfter := int(math.Ceil(rateLimitErr.RetryAfter.Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		writeAuthCodeError(c, http.StatusTooManyRequests, "too_many_attempts")
		return
	}

	switch err.Error() {
	case "invalid_credentials":
		writeAuthCodeError(c, http.StatusUnauthorized, "invalid_credentials")
	case "invalid_auth_type":
		writeAuthCodeError(c, http.StatusBadRequest, "invalid_auth_type")
	case "pam_auth_disabled":
		writeAuthCodeError(c, http.StatusForbidden, "pam_auth_disabled")
	case "only_admin_allowed", "account_locked", "password_auth_disabled", "user_not_registered_in_sylve":
		writeAuthCodeError(c, http.StatusForbidden, err.Error())
	case "pam_auth_error":
		writeAuthCodeError(c, http.StatusServiceUnavailable, "authentication_service_unavailable")
	default:
		writeAuthCodeError(c, http.StatusInternalServerError, "internal_server_error")
	}
}

func completeLogin(
	c *gin.Context,
	authService *auth.Service,
	userID uint,
	token string,
) {
	completed := false
	defer func() {
		if completed || token == "" {
			return
		}
		if err := authService.RevokeJWT(token); err != nil {
			logger.L.Error().Err(err).Uint("user_id", userID).Msg("failed_to_revoke_incomplete_login_token")
		}
	}()

	hostname, err := utils.GetSystemHostname()
	if err != nil {
		writeLoginHostnameError(c, err)
		return
	}

	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		writeAuthCodeError(c, http.StatusInternalServerError, "internal_server_error")
		return
	}

	basicSettings, err := authService.GetBasicSettings()
	if err != nil && err.Error() != "basic_settings_not_found" {
		writeAuthCodeError(c, http.StatusInternalServerError, "internal_server_error")
		return
	}

	c.JSON(http.StatusOK, internal.APIResponse[SuccessfulLogin]{
		Status:  "success",
		Message: "login_successful",
		Error:   "",
		Data: SuccessfulLogin{
			Token:         token,
			Hostname:      hostname,
			NodeID:        nodeID,
			BasicSettings: basicSettings,
		},
	})
	completed = true
}

// @Summary Get Login Configuration
// @Description Retrieve the public authentication configuration used by the login page
// @Tags Authentication
// @Produce json
// @Success 200 {object} internal.APIResponse[LoginConfig] "Success"
// @Router /auth/login/config [get]
func LoginConfigHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, internal.APIResponse[LoginConfig]{
			Status:  "success",
			Message: "login_config_retrieved",
			Error:   "",
			Data: LoginConfig{
				PAMEnabled: config.IsPAMEnabled(),
			},
		})
	}
}

// @Summary Login
// @Description Authenticate an administrator and create a local session
// @Tags Authentication
// @Param request body LoginRequest true "Login request body"
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[SuccessfulLogin] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 429 {object} internal.APIResponse[any] "Too Many Requests"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/login [post]
func LoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		var r LoginRequest

		if err := c.ShouldBindJSON(&r); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeAuthCodeError(c, http.StatusRequestEntityTooLarge, "request_body_too_large")
				return
			}

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
			writeLoginServiceError(c, err)
			return
		}

		completeLogin(c, authService, userId, token)
	}
}

// @Summary Logout
// @Description Revoke a JWT token
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/logout [post]
func LogoutHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		if c.GetString("AuthScope") != "local" {
			writeAuthCodeError(c, http.StatusForbidden, "local_session_required")
			return
		}

		token := strings.TrimSpace(c.GetString("Token"))
		if token == "" {
			writeAuthCodeError(c, http.StatusUnauthorized, "no_token_provided")
			return
		}

		if err := authService.RevokeJWT(token); err != nil {
			writeAuthCodeError(c, http.StatusInternalServerError, "internal_server_error")
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
