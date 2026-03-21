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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
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

	// Only trust forwarded headers from a local reverse proxy.
	return ip.IsLoopback()
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

func parseUserIDParam(c *gin.Context) (uint, error) {
	rawID := strings.TrimSpace(c.Param("id"))
	if rawID == "" {
		return 0, http.ErrMissingFile
	}

	parsedID, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil {
		return 0, err
	}

	return uint(parsedID), nil
}

func requirePasskeyManagementAccess(c *gin.Context, authService *auth.Service) bool {
	authType := strings.TrimSpace(c.GetString("AuthType"))
	if authType != "sylve" && authType != auth.AuthTypeSylvePasskey {
		c.JSON(http.StatusForbidden, internal.APIResponse[any]{
			Status:  "error",
			Message: "passkey_management_for_sylve_only",
			Error:   "passkey_management_for_sylve_only",
			Data:    nil,
		})
		return false
	}

	currentUserID := c.GetUint("UserID")
	if currentUserID == 0 {
		c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_credentials",
			Error:   "invalid_credentials",
			Data:    nil,
		})
		return false
	}

	currentUser, err := authService.GetUserByID(currentUserID)
	if err != nil || currentUser == nil {
		c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_credentials",
			Error:   "invalid_credentials",
			Data:    nil,
		})
		return false
	}

	if !currentUser.Admin {
		c.JSON(http.StatusForbidden, internal.APIResponse[any]{
			Status:  "error",
			Message: "only_admin_allowed",
			Error:   "only_admin_allowed",
			Data:    nil,
		})
		return false
	}

	return true
}

func BeginPasskeyLoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "passkey_requires_https",
				Error:   "passkey_requires_https",
				Data:    nil,
			})
			return
		}

		requestID, publicKey, err := authService.BeginPasskeyLogin(rpID, origin)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_begin_passkey_login",
				Error:   err.Error(),
				Data:    nil,
			})
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

func FinishPasskeyLoginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req FinishPasskeyLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    nil,
			})
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "passkey_requires_https",
				Error:   "passkey_requires_https",
				Data:    nil,
			})
			return
		}

		user, token, err := authService.FinishPasskeyLogin(req.RequestID, req.Credential, req.Remember, rpID, origin)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "invalid_credentials") || strings.Contains(err.Error(), "only_admin_allowed") {
				status = http.StatusUnauthorized
			}

			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credentials",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		clusterToken, _ := authService.CreateClusterJWT(user.ID, user.Username, auth.AuthTypeSylvePasskey, "")
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

		nodeID, err := utils.GetSystemUUID()
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
				NodeID:        nodeID,
				BasicSettings: basicSettings,
			},
		})
	}
}

func BeginPasskeyRegistrationHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requirePasskeyManagementAccess(c, authService) {
			return
		}

		var req BeginPasskeyRegistrationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    nil,
			})
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "passkey_requires_https",
				Error:   "passkey_requires_https",
				Data:    nil,
			})
			return
		}

		requestID, publicKey, err := authService.BeginPasskeyRegistration(req.UserID, rpID, origin)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_begin_passkey_registration",
				Error:   err.Error(),
				Data:    nil,
			})
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

func FinishPasskeyRegistrationHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requirePasskeyManagementAccess(c, authService) {
			return
		}

		var req FinishPasskeyRegistrationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_payload",
				Error:   "validation_error",
				Data:    nil,
			})
			return
		}

		rpID, origin, err := getPasskeyRelyingParty(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "passkey_requires_https",
				Error:   "passkey_requires_https",
				Data:    nil,
			})
			return
		}

		if err := authService.FinishPasskeyRegistration(req.RequestID, req.Credential, req.Label, rpID, origin); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_finish_passkey_registration",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "passkey_registered_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}

func ListUserPasskeysHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requirePasskeyManagementAccess(c, authService) {
			return
		}

		userID, err := parseUserIDParam(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_user_id",
				Error:   "invalid_user_id_format",
				Data:    nil,
			})
			return
		}

		passkeys, err := authService.ListUserPasskeys(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_passkeys",
				Error:   err.Error(),
				Data:    nil,
			})
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

func DeleteUserPasskeyHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requirePasskeyManagementAccess(c, authService) {
			return
		}

		userID, err := parseUserIDParam(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_user_id",
				Error:   "invalid_user_id_format",
				Data:    nil,
			})
			return
		}

		rawCredentialID := strings.TrimSpace(c.Param("credentialId"))
		if rawCredentialID == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credential_id",
				Error:   "credential_id_required",
				Data:    nil,
			})
			return
		}

		credentialID, decodeErr := url.PathUnescape(rawCredentialID)
		if decodeErr != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_credential_id",
				Error:   "invalid_credential_id_format",
				Data:    nil,
			})
			return
		}

		if err := authService.DeleteUserPasskey(userID, credentialID); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "credential_not_found") {
				status = http.StatusNotFound
			}

			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_passkey",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "passkey_deleted_successfully",
			Error:   "",
			Data:    nil,
		})
	}
}
