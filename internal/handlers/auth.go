// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"net/http"
	"sylve/internal"
	"sylve/internal/services/auth"
	"sylve/internal/utils"

	"github.com/gin-gonic/gin"
)

type RequestLogin struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
	AuthType string `json:"authType"`
	Remember bool   `json:"remember"`
}

type ResponseLogin struct {
	Status   string `json:"status"`
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
}

// @Summary User login
// @Description Authenticate a user and return a JWT token
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body RequestLogin true "Login request payload"
// @Success 200 {object} ResponseLogin
// @Failure 400 {object} internal.ErrorResponse "Invalid request"
// @Failure 401 {object} internal.ErrorResponse "Invalid credentials"
// @Failure 500 {object} internal.ErrorResponse "Internal server error"
// @Router /auth/login [post]
func LoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var r RequestLogin

		if err := c.ShouldBindJSON(&r); err != nil {
			utils.SendJSONResponse(c, http.StatusBadRequest, internal.ErrorResponse{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
			})
			return
		}

		if err := Validate.Struct(r); err != nil {
			utils.SendJSONResponse(c, http.StatusBadRequest, internal.ErrorResponse{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
			})
			return
		}

		token, err := authService.CreateJWT(r.Username, r.Password, r.AuthType, r.Remember)
		if err != nil {
			utils.SendJSONResponse(c, http.StatusUnauthorized, internal.ErrorResponse{
				Status:  "error",
				Message: "invalid_credentials",
				Error:   err.Error(),
			})
			return
		}

		hostname, err := utils.GetSystemHostname()
		if err != nil {
			utils.SendJSONResponse(c, http.StatusInternalServerError, internal.ErrorResponse{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
			})
			return
		}

		utils.SendJSONResponse(c, http.StatusOK, ResponseLogin{"success", token, hostname})
	}
}

// @Summary User logout
// @Description Revoke a JWT token
// @Tags Authentication
// @Security BearerAuth
// @Success 200 {object} internal.SuccessResponse
// @Failure 401 {object} internal.ErrorResponse "Unauthorized"
// @Router /auth/logout [get]
// @Router /auth/logout [post]
func LogoutHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := utils.GetTokenFromHeader(c.Request.Header)

		if err != nil {
			utils.SendJSONResponse(c, http.StatusUnauthorized, internal.ErrorResponse{
				Status:  "error",
				Message: "unauthorized",
				Error:   "no_token_provided",
			})
			return
		}

		if err := authService.RevokeJWT(token); err != nil {
			utils.SendJSONResponse(c, http.StatusInternalServerError, internal.ErrorResponse{
				Status:  "error",
				Message: "unable_to_revoke_token",
				Error:   err.Error(),
			})
			return
		}

		utils.SendJSONResponse(c, http.StatusOK, internal.SuccessResponse{
			Status:  "success",
			Message: "token_revoked",
		})
	}
}
