// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package eventsHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	hub "github.com/alchemillahq/sylve/internal/events"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

type CreateSSETokenResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expiresIn"`
}

// @Summary Create SSE token
// @Description Create a short-lived token scoped to the authenticated user's event stream
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[CreateSSETokenResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/sse-tokens [post]
func CreateSSEToken(authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")

		if c.GetString("AuthScope") != "local" {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "local_session_required",
				Error:   "local_session_required",
				Data:    nil,
			})
			return
		}

		userIDAny, hasUserID := c.Get("UserID")
		usernameAny, hasUsername := c.Get("Username")
		authTypeAny, hasAuthType := c.Get("AuthType")

		if !hasUserID || !hasUsername || !hasAuthType {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "unauthorized",
				Error:   "missing_claims",
				Data:    nil,
			})
			return
		}

		userID, ok := userIDAny.(uint)
		if !ok || userID == 0 {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "unauthorized",
				Error:   "invalid_user_id",
				Data:    nil,
			})
			return
		}

		token, err := authService.CreateScopedJWT(
			userID,
			fmt.Sprintf("%v", usernameAny),
			fmt.Sprintf("%v", authTypeAny),
			"sse",
			600,
		)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_sse_token",
				Error:   "failed_to_create_sse_token",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[CreateSSETokenResponse]{
			Status:  "success",
			Message: "sse_token_created",
			Error:   "",
			Data: CreateSSETokenResponse{
				Token:     token,
				ExpiresIn: 600,
			},
		})
	}
}

// @Summary Subscribe to server-sent events
// @Description Open a long-lived event stream using a short-lived SSE query capability
// @Tags Events
// @Produce text/event-stream
// @Param sse_token query string true "Short-lived SSE capability issued by POST /auth/sse-tokens"
// @Success 200 {string} string "Server-sent event stream"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /events/stream [get]
func StreamSSE() gin.HandlerFunc {
	return func(c *gin.Context) {
		expiresAt, ok := c.Get(middleware.SSEExpiresAtContextKey)
		expiry, validExpiry := expiresAt.(time.Time)
		if !ok || !validExpiry || expiry.IsZero() {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "sse_expiry_unavailable",
				Error:   "sse_expiry_unavailable",
				Data:    nil,
			})
			return
		}

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "streaming_not_supported",
				Error:   "streaming_not_supported",
				Data:    nil,
			})
			return
		}

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "private, no-store")
		c.Header("Pragma", "no-cache")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		_, _ = c.Writer.Write([]byte("retry: 3000\n\n"))
		_, _ = c.Writer.Write([]byte("event: connected\ndata: {\"ok\":true}\n\n"))
		flusher.Flush()

		events, unsubscribe := hub.SSE.Subscribe()
		defer unsubscribe()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		session := time.NewTimer(time.Until(expiry))
		defer session.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-heartbeat.C:
				_, _ = c.Writer.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			case <-session.C:
				_, _ = c.Writer.Write([]byte("event: reconnect\ndata: {\"reason\":\"token_rotation\"}\n\n"))
				flusher.Flush()
				return
			case evt, ok := <-events:
				if !ok {
					return
				}

				data, err := json.Marshal(evt)
				if err != nil {
					continue
				}

				eventName := strings.TrimSpace(evt.Type)
				if eventName == "" {
					eventName = "left-panel-refresh"
				}

				_, _ = c.Writer.Write([]byte("event: " + eventName + "\n"))
				_, _ = c.Writer.Write([]byte("data: "))
				_, _ = c.Writer.Write(data)
				_, _ = c.Writer.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}
