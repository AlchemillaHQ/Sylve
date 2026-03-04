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
	"time"

	"github.com/alchemillahq/sylve/internal"
	hub "github.com/alchemillahq/sylve/internal/events"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

type CreateSSETokenResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expiresIn"`
}

func CreateSSEToken(authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		if !ok {
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
			120,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_sse_token",
				Error:   err.Error(),
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
				ExpiresIn: 120,
			},
		})
	}
}

func StreamSSE(authService *authService.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		sseToken := c.Query("sse_token")
		if sseToken == "" {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_sse_token",
				Error:   "missing_sse_token",
				Data:    nil,
			})
			return
		}

		if _, err := authService.ValidateScopedJWT(sseToken, "sse"); err != nil {
			c.JSON(http.StatusUnauthorized, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_sse_token",
				Error:   err.Error(),
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
		c.Header("Cache-Control", "no-cache")
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

		session := time.NewTimer(110 * time.Second)
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

				_, _ = c.Writer.Write([]byte("event: left-panel-refresh\n"))
				_, _ = c.Writer.Write([]byte("data: "))
				_, _ = c.Writer.Write(data)
				_, _ = c.Writer.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}
