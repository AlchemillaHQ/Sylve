// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notificationsHandlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type NotificationListResponse struct {
	Items []models.Notification `json:"items"`
	Total int64                 `json:"total"`
}

type NotificationCountResponse struct {
	Active int64 `json:"active"`
}

type notificationConfigUpdateRequest struct {
	Ntfy struct {
		Enabled   bool    `json:"enabled"`
		BaseURL   string  `json:"baseUrl"`
		Topic     string  `json:"topic"`
		AuthToken *string `json:"authToken"`
	} `json:"ntfy"`
	Email struct {
		Enabled      bool     `json:"enabled"`
		SMTPHost     string   `json:"smtpHost"`
		SMTPPort     int      `json:"smtpPort"`
		SMTPUsername string   `json:"smtpUsername"`
		SMTPFrom     string   `json:"smtpFrom"`
		SMTPUseTLS   bool     `json:"smtpUseTls"`
		Recipients   []string `json:"recipients"`
		SMTPPassword *string  `json:"smtpPassword"`
	} `json:"email"`
}

func List(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope := notifications.ListScope(strings.TrimSpace(strings.ToLower(c.DefaultQuery("scope", string(notifications.ListScopeActive)))))
		if scope != notifications.ListScopeActive && scope != notifications.ListScopeAll {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_scope",
				Error:   "invalid_scope",
				Data:    nil,
			})
			return
		}

		limit := parseInt(c.Query("limit"), 50)
		offset := parseInt(c.Query("offset"), 0)

		items, total, err := service.List(c.Request.Context(), scope, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_notifications",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[NotificationListResponse]{
			Status:  "success",
			Message: "notifications_listed",
			Error:   "",
			Data: NotificationListResponse{
				Items: items,
				Total: total,
			},
		})
	}
}

func Count(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		active, err := service.CountActive(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_count_notifications",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[NotificationCountResponse]{
			Status:  "success",
			Message: "notifications_counted",
			Error:   "",
			Data: NotificationCountResponse{
				Active: active,
			},
		})
	}
}

func Dismiss(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_notification_id",
				Error:   "invalid_notification_id",
				Data:    nil,
			})
			return
		}

		err = service.Dismiss(c.Request.Context(), uint(id))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "notification_not_found",
					Error:   "notification_not_found",
					Data:    nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_dismiss_notification",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "notification_dismissed",
			Error:   "",
			Data:    nil,
		})
	}
}

func GetConfig(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg, err := service.GetTransportConfig(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_load_notification_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.TransportConfigView]{
			Status:  "success",
			Message: "notification_config_loaded",
			Error:   "",
			Data:    cfg,
		})
	}
}

func UpdateConfig(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req notificationConfigUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		updated, err := service.UpdateTransportConfig(c.Request.Context(), notifications.TransportConfigUpdate{
			Ntfy: notifications.NtfyTransportConfigUpdate{
				Enabled:   req.Ntfy.Enabled,
				BaseURL:   req.Ntfy.BaseURL,
				Topic:     req.Ntfy.Topic,
				AuthToken: req.Ntfy.AuthToken,
			},
			Email: notifications.EmailTransportConfigUpdate{
				Enabled:      req.Email.Enabled,
				SMTPHost:     req.Email.SMTPHost,
				SMTPPort:     req.Email.SMTPPort,
				SMTPUsername: req.Email.SMTPUsername,
				SMTPFrom:     req.Email.SMTPFrom,
				SMTPUseTLS:   req.Email.SMTPUseTLS,
				Recipients:   req.Email.Recipients,
				SMTPPassword: req.Email.SMTPPassword,
			},
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_notification_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.TransportConfigView]{
			Status:  "success",
			Message: "notification_config_updated",
			Error:   "",
			Data:    updated,
		})
	}
}

func parseInt(value string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return v
}
