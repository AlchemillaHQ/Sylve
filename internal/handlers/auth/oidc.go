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
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

type OIDCStartRequest struct {
	RedirectURI string `json:"redirectUri" binding:"required,max=2048"`
}

type OIDCStartResponse struct {
	URL string `json:"url"`
}

type OIDCCallbackRequest struct {
	Code  string `json:"code" binding:"required,max=4096"`
	State string `json:"state" binding:"required,max=128"`
}

type DiscoverRealmRequest struct {
	Issuer string `json:"issuer" binding:"required,max=2048"`
	CACert string `json:"caCert"`
}

type CreateRealmUserRequest struct {
	Username string `json:"username" binding:"required,max=64"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
	Notes    string `json:"notes"`
}

func setOIDCStateCookie(c *gin.Context, state string, maxAge int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(auth.OIDCStateCookie, state, maxAge, "/api/auth/oidc", "", true, true)
}

func writeOIDCLoginError(c *gin.Context, err error) {
	code := err.Error()
	status := http.StatusUnauthorized
	switch code {
	case "invalid_redirect_uri", "oidc_login_expired":
		status = http.StatusBadRequest
	case "oidc_realm_unavailable", "oidc_acr_not_met", "oidc_domain_not_allowed", "user_not_registered_in_sylve",
		"account_locked", "no_permissions", "admin_required":
		status = http.StatusForbidden
	case "oidc_login_conflict":
		status = http.StatusConflict
	case "oidc_provider_unavailable", "oidc_login_pending", "cluster_write_failed":
		status = http.StatusServiceUnavailable
	case "oidc_exchange_failed", "oidc_id_token_invalid", "oidc_userinfo_failed", "oidc_claims_invalid", "oidc_login_failed":
	default:
		logger.L.Error().Err(err).Msg("oidc_login_failed")
		status, code = http.StatusInternalServerError, "internal_server_error"
	}
	writeAuthCodeError(c, status, code)
}

func writeRealmError(c *gin.Context, operation string, err error) {
	code, status := err.Error(), http.StatusInternalServerError
	switch {
	case auth.IsRealmValidationError(err):
		status = http.StatusBadRequest
	case strings.HasPrefix(code, "oidc_provider_unavailable"):
		status, code = http.StatusBadGateway, "oidc_provider_unavailable"
	case code == "acl_writes_unavailable" || errors.Is(err, cluster.ErrClusterProtocolOutdated):
		status = http.StatusServiceUnavailable
		if code != "acl_writes_unavailable" {
			code = cluster.ProposalErrorCode(err)
		}
	default:
		logger.L.Error().Err(err).Str("operation", operation).Msg("realm_operation_failed")
		code = cluster.ProposalErrorCode(err)
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: operation, Error: code})
}

// @Summary Start OpenID Connect sign-in
// @Description Returns the provider URL to send the browser to, and binds the attempt to this browser with a cookie
// @Tags Authentication
// @Accept json
// @Produce json
// @Param realm path string true "Realm name"
// @Param request body OIDCStartRequest true "This node's callback URL"
// @Success 200 {object} internal.APIResponse[OIDCStartResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/oidc/{realm}/start [post]
func StartOIDCLoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)
		var req OIDCStartRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		url, loginState, err := authService.StartOIDCLogin(c.Request.Context(), c.Param("realm"), req.RedirectURI)
		if err != nil {
			writeOIDCLoginError(c, err)
			return
		}
		setOIDCStateCookie(c, loginState, int(auth.OIDCLoginTTL.Seconds()))
		c.JSON(http.StatusOK, internal.APIResponse[OIDCStartResponse]{
			Status: "success", Message: "oidc_login_started", Data: OIDCStartResponse{URL: url},
		})
	}
}

// @Summary Finish OpenID Connect sign-in
// @Description Exchanges the provider's code for a session, in the browser that started the sign-in
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body OIDCCallbackRequest true "Code and state from the provider"
// @Success 200 {object} internal.APIResponse[SuccessfulLogin] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/oidc/callback [post]
func OIDCCallbackHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)
		var req OIDCCallbackRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		loginState, _ := c.Cookie(auth.OIDCStateCookie)
		setOIDCStateCookie(c, "", -1)
		userID, token, err := authService.FinishOIDCLogin(c.Request.Context(), req.State, loginState, req.Code)
		if err != nil {
			writeOIDCLoginError(c, err)
			return
		}
		completeLogin(c, authService, userID, token)
	}
}

// @Summary List realms
// @Description OpenID Connect realms; client secrets are never returned
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.AuthRealm] "Realms"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Router /auth/realms [get]
func ListRealmsHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		realms, err := authService.ListRealms()
		if err != nil {
			writeRealmError(c, "failed_to_list_realms", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.AuthRealm]{Status: "success", Message: "realms_listed", Data: realms})
	}
}

// @Summary Create or update a realm
// @Description Issuer and client ID are fixed once a realm exists; a missing clientSecret keeps the stored one
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Realm name"
// @Param request body clusterModels.AuthRealmSet true "Realm"
// @Success 200 {object} internal.APIResponse[any] "Realm Set"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/realms/{name} [put]
func SetRealmHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req clusterModels.AuthRealmSet
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		req.Name = c.Param("name")
		if err := authService.SetRealm(c.Request.Context(), req); err != nil {
			writeRealmError(c, "failed_to_set_realm", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "realm_set"})
	}
}

// @Summary Delete a realm
// @Description Deletes the realm with its users and their grants
// @Tags Permissions
// @Produce json
// @Security BearerAuth
// @Param name path string true "Realm name"
// @Success 200 {object} internal.APIResponse[any] "Realm Deleted"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/realms/{name} [delete]
func DeleteRealmHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := authService.DeleteRealm(c.Request.Context(), c.Param("name")); err != nil {
			writeRealmError(c, "failed_to_delete_realm", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "realm_deleted"})
	}
}

// @Summary Discover a provider
// @Description Reads an issuer's discovery document; it can't check a client secret
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DiscoverRealmRequest true "Issuer"
// @Success 200 {object} internal.APIResponse[auth.RealmDiscovery] "Discovery"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Router /auth/realms/discover [post]
func DiscoverRealmHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req DiscoverRealmRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		discovery, err := authService.DiscoverRealm(c.Request.Context(), req.Issuer, req.CACert)
		if err != nil {
			writeRealmError(c, "failed_to_discover_realm", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[auth.RealmDiscovery]{Status: "success", Message: "realm_discovered", Data: discovery})
	}
}

// @Summary Add a realm user
// @Description Adds a user before their first sign-in; the first login whose username claim names them binds them
// @Tags Permissions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Realm name"
// @Param request body CreateRealmUserRequest true "User"
// @Success 200 {object} internal.APIResponse[any] "User Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/realms/{name}/users [post]
func CreateRealmUserHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateRealmUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := authService.CreateRealmUser(c.Request.Context(), clusterModels.OIDCUser{
			Realm: c.Param("name"), Username: req.Username, FullName: req.FullName, Email: req.Email, Notes: req.Notes,
		}); err != nil {
			writeRealmError(c, "failed_to_create_realm_user", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "realm_user_created"})
	}
}
