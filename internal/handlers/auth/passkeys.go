// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

type PasskeyChallengeResponse struct {
	RequestID string `json:"requestId"`
	PublicKey any    `json:"publicKey"`
}

type BeginPasskeyRegistrationRequest struct {
	UserID uint `json:"userId" binding:"required"`
}

type FinishPasskeyRegistrationRequest struct {
	RequestID  string          `json:"requestId" binding:"required"`
	Credential json.RawMessage `json:"credential" binding:"required"`
	Label      string          `json:"label"`
}

type FinishPasskeyLoginRequest struct {
	RequestID  string          `json:"requestId" binding:"required"`
	Credential json.RawMessage `json:"credential" binding:"required"`
	Remember   bool            `json:"remember"`
}

func remoteAddrIP(remoteAddr string) net.IP {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return nil
	}

	host, _, err := net.SplitHostPort(trimmed)
	if err != nil {
		host = trimmed
	}

	return net.ParseIP(strings.TrimSpace(host))
}

func isTrustedForwardingSource(c *gin.Context) bool {
	ip := remoteAddrIP(c.Request.RemoteAddr)
	if ip == nil {
		return false
	}

	if ip.IsLoopback() {
		return true
	}

	if config.ParsedConfig != nil {
		for _, proxy := range config.ParsedConfig.TrustedProxies {
			trimmed := strings.TrimSpace(proxy)
			if trimmed == "" {
				continue
			}
			if _, cidr, err := net.ParseCIDR(trimmed); err == nil {
				if cidr.Contains(ip) {
					return true
				}
			} else if parsed := net.ParseIP(trimmed); parsed != nil && parsed.Equal(ip) {
				return true
			}
		}
	}

	return false
}

func firstForwardedHeaderValue(value string) string {
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func isSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}

	if !isTrustedForwardingSource(c) {
		return false
	}

	forwardedProto := firstForwardedHeaderValue(c.GetHeader("X-Forwarded-Proto"))
	return strings.EqualFold(forwardedProto, "https")
}

func getPasskeyRelyingParty(c *gin.Context) (rpID string, origin string, err error) {
	if !isSecureRequest(c) {
		return "", "", http.ErrNotSupported
	}

	host := strings.TrimSpace(c.Request.Host)
	if isTrustedForwardingSource(c) {
		forwardedHost := firstForwardedHeaderValue(c.GetHeader("X-Forwarded-Host"))
		if forwardedHost != "" {
			host = forwardedHost
		}
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", http.ErrNoLocation
	}

	if strings.Contains(host, "://") {
		if parsed, parseErr := url.Parse(host); parseErr == nil && parsed.Host != "" {
			host = parsed.Host
		}
	}

	originURL := &url.URL{
		Scheme: "https",
		Host:   host,
	}

	if originURL.Hostname() == "" {
		return "", "", http.ErrNoLocation
	}

	rpID = strings.ToLower(strings.TrimSpace(originURL.Hostname()))
	originHost := rpID
	if port := originURL.Port(); port != "" {
		originHost = net.JoinHostPort(rpID, port)
	}
	originURL.Host = originHost

	return rpID, originURL.String(), nil
}

// @Summary Begin passkey login
// @Description Start a discoverable WebAuthn login ceremony
// @Tags Authentication
// @Produce json
// @Success 200 {object} internal.APIResponse[PasskeyChallengeResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/passkeys/login/begin [post]
func BeginPasskeyLoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "passkey_requires_https")
			return
		}

		requestID, publicKey, err := authService.BeginPasskeyLogin(rpID, origin)
		if err != nil {
			writeAuthCodeError(c, http.StatusInternalServerError, "failed_to_begin_passkey_login")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[PasskeyChallengeResponse]{
			Status:  "success",
			Message: "passkey_login_started",
			Error:   "",
			Data: PasskeyChallengeResponse{
				RequestID: requestID,
				PublicKey: publicKey,
			},
		})
	}
}

func writePasskeyLoginError(c *gin.Context, err error) {
	switch err.Error() {
	case "invalid_credentials", "user_not_found", "credential_not_found", "blank_user_handle", "invalid_user_handle":
		writeAuthCodeError(c, http.StatusUnauthorized, "invalid_credentials")
	case "only_admin_allowed", "account_locked":
		writeAuthCodeError(c, http.StatusForbidden, err.Error())
	case "challenge_not_found", "challenge_used", "challenge_expired", "credential_required":
		writeAuthCodeError(c, http.StatusBadRequest, "invalid_passkey_request")
	default:
		writeAuthCodeError(c, http.StatusInternalServerError, "internal_server_error")
	}
}

func writePasskeyBindingError(c *gin.Context, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeAuthCodeError(c, http.StatusRequestEntityTooLarge, "request_body_too_large")
		return
	}
	writeAuthCodeError(c, http.StatusBadRequest, "invalid_request_payload")
}

func classifyPasskeyManagementError(err error) (int, string) {
	switch err.Error() {
	case "invalid_user_id", "invalid_credential_id", "passkey_label_required", "passkey_label_too_long",
		"challenge_not_found", "challenge_expired", "credential_required", "invalid_passkey_registration":
		return http.StatusBadRequest, err.Error()
	case "passkey_registration_not_allowed":
		return http.StatusForbidden, err.Error()
	case "user_not_found", "credential_not_found":
		return http.StatusNotFound, err.Error()
	case "challenge_used", "credential_already_registered", "passkey_limit_reached":
		return http.StatusConflict, err.Error()
	default:
		return http.StatusInternalServerError, "internal_server_error"
	}
}

func writePasskeyManagementError(c *gin.Context, operation string, err error) {
	status, code := classifyPasskeyManagementError(err)
	if status >= http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", operation).Msg("passkey_operation_failed")
	}
	writeAuthCodeError(c, status, code)
}

// @Summary Finish passkey login
// @Description Verify a WebAuthn assertion and create a local administrator session
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body FinishPasskeyLoginRequest true "Passkey login completion request"
// @Success 200 {object} internal.APIResponse[SuccessfulLogin] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/passkeys/login/finish [post]
func FinishPasskeyLoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		var req FinishPasskeyLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeAuthCodeError(c, http.StatusRequestEntityTooLarge, "request_body_too_large")
				return
			}
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_request_payload")
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "passkey_requires_https")
			return
		}

		user, token, err := authService.FinishPasskeyLogin(req.RequestID, req.Credential, req.Remember, rpID, origin)
		if err != nil {
			writePasskeyLoginError(c, err)
			return
		}

		completeLogin(c, authService, user.ID, token)
	}
}

// @Summary Begin passkey registration
// @Description Start a WebAuthn registration ceremony for an eligible administrator
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body BeginPasskeyRegistrationRequest true "Passkey registration request"
// @Success 200 {object} internal.APIResponse[PasskeyChallengeResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/passkeys/register/begin [post]
func BeginPasskeyRegistrationHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		var req BeginPasskeyRegistrationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writePasskeyBindingError(c, err)
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "passkey_requires_https")
			return
		}

		requestID, publicKey, err := authService.BeginPasskeyRegistration(req.UserID, rpID, origin)
		if err != nil {
			writePasskeyManagementError(c, "failed_to_begin_passkey_registration", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[PasskeyChallengeResponse]{
			Status:  "success",
			Message: "passkey_registration_started",
			Error:   "",
			Data: PasskeyChallengeResponse{
				RequestID: requestID,
				PublicKey: publicKey,
			},
		})
	}
}

// @Summary Finish passkey registration
// @Description Verify a WebAuthn attestation and save the credential bound to its registration challenge
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body FinishPasskeyRegistrationRequest true "Passkey registration completion request"
// @Success 200 {object} internal.APIResponse[auth.PasskeyCredentialInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/passkeys/register/finish [post]
func FinishPasskeyRegistrationHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		setSensitiveAuthResponseHeaders(c)

		var req FinishPasskeyRegistrationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writePasskeyBindingError(c, err)
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			writeAuthCodeError(c, http.StatusBadRequest, "passkey_requires_https")
			return
		}

		passkey, err := authService.FinishPasskeyRegistration(req.RequestID, req.Credential, req.Label, rpID, origin)
		if err != nil {
			writePasskeyManagementError(c, "failed_to_finish_passkey_registration", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[auth.PasskeyCredentialInfo]{
			Status:  "success",
			Message: "passkey_registered_successfully",
			Error:   "",
			Data:    passkey,
		})
	}
}

// @Summary List user passkeys
// @Description List safe passkey metadata for a user, including users no longer eligible for registration; returns an empty list when none are registered
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Param userId path int true "Positive User ID" minimum(1)
// @Success 200 {object} internal.APIResponse[[]auth.PasskeyCredentialInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/{userId}/passkeys [get]
func ListUserPasskeysHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := positiveUserIDParam(c)
		if !ok {
			return
		}

		passkeys, err := authService.ListUserPasskeys(userID)
		if err != nil {
			writePasskeyManagementError(c, "failed_to_list_passkeys", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]auth.PasskeyCredentialInfo]{
			Status:  "success",
			Message: "passkeys_listed_successfully",
			Error:   "",
			Data:    passkeys,
		})
	}
}

// @Summary Delete user passkey
// @Description Delete one passkey credential owned by a user
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Param userId path int true "Positive User ID" minimum(1)
// @Param credentialId path string true "URL-encoded passkey credential ID"
// @Success 200 {object} internal.APIResponse[auth.PasskeyCredentialInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /auth/users/{userId}/passkeys/{credentialId} [delete]
func DeleteUserPasskeyHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := positiveUserIDParam(c)
		if !ok {
			return
		}

		credentialID := strings.TrimSpace(c.Param("credentialId"))
		if credentialID == "" {
			writeAuthCodeError(c, http.StatusBadRequest, "invalid_credential_id")
			return
		}

		passkey, err := authService.DeleteUserPasskey(userID, credentialID)
		if err != nil {
			writePasskeyManagementError(c, "failed_to_delete_passkey", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[auth.PasskeyCredentialInfo]{
			Status:  "success",
			Message: "passkey_deleted_successfully",
			Error:   "",
			Data:    passkey,
		})
	}
}
