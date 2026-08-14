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
	"github.com/alchemillahq/sylve/internal/logger"
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

type NotificationDismissAllResponse struct {
	Dismissed int64 `json:"dismissed"`
}

// @Summary List notifications
// @Description List active or all notifications using bounded offset pagination
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param scope query string false "Notification scope" Enums(active,all) default(active)
// @Param limit query int false "Maximum notifications to return" minimum(1) maximum(500) default(50)
// @Param offset query int false "Number of notifications to skip" minimum(0) default(0)
// @Success 200 {object} internal.APIResponse[NotificationListResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications [get]
func List(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope := notifications.ListScope(strings.TrimSpace(strings.ToLower(c.DefaultQuery("scope", string(notifications.ListScopeActive)))))
		if scope != notifications.ListScopeActive && scope != notifications.ListScopeAll {
			writeNotificationError(c, http.StatusBadRequest, "invalid_scope", "invalid_scope")
			return
		}

		limit, ok := parseNotificationQueryInt(c, "limit", notifications.DefaultListLimit, 1, notifications.MaxListLimit)
		if !ok {
			return
		}
		offset, ok := parseNotificationQueryInt(c, "offset", 0, 0, 0)
		if !ok {
			return
		}

		items, total, err := service.List(c.Request.Context(), scope, limit, offset)
		if err != nil {
			writeNotificationServiceError(c, "list", "failed_to_list_notifications", err)
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

// @Summary Count active notifications
// @Description Return the number of notifications that have not been dismissed
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[NotificationCountResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/count [get]
func Count(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		active, err := service.CountActive(c.Request.Context())
		if err != nil {
			writeNotificationServiceError(c, "count", "failed_to_count_notifications", err)
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

// @Summary Dismiss a notification
// @Description Dismiss one global notification and persist suppression where supported
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/{id}/dismiss [post]
func Dismiss(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_notification_id", "invalid_notification_id")
			return
		}

		err = service.Dismiss(c.Request.Context(), uint(id))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeNotificationError(c, http.StatusNotFound, "notification_not_found", "notification_not_found")
				return
			}
			writeNotificationServiceError(c, "dismiss", "failed_to_dismiss_notification", err)
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

// @Summary Dismiss all notifications
// @Description Dismiss every currently active global notification
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[NotificationDismissAllResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/dismiss-all [post]
func DismissAll(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		dismissed, err := service.DismissAll(c.Request.Context())
		if err != nil {
			writeNotificationServiceError(c, "dismiss_all", "failed_to_dismiss_notifications", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[NotificationDismissAllResponse]{
			Status:  "success",
			Message: "notifications_dismissed",
			Error:   "",
			Data: NotificationDismissAllResponse{
				Dismissed: dismissed,
			},
		})
	}
}

// @Summary List notification transports
// @Description List administrator-visible notification transport configuration, including configured Discord webhook URLs
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[notifications.TransportConfigView] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/transports [get]
func GetTransports(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg, err := service.GetTransportConfig(c.Request.Context())
		if err != nil {
			writeNotificationServiceError(c, "get_transports", "failed_to_load_notification_config", err)
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

// @Summary Create a notification transport
// @Description Create one ntfy, SMTP, or Discord notification transport
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body notifications.TransportInput true "Notification transport"
// @Success 201 {object} internal.APIResponse[notifications.TransportConfigView] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/transports [post]
func CreateTransport(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req notifications.TransportInput
		if !bindNotificationJSON(c, &req) {
			return
		}

		updated, err := service.CreateTransport(c.Request.Context(), req)
		if err != nil {
			writeNotificationServiceError(c, "create_transport", "failed_to_create_notification_transport", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[notifications.TransportConfigView]{
			Status:  "success",
			Message: "notification_transport_created",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Update a notification transport
// @Description Replace the mutable configuration of one notification transport; omitted credentials remain unchanged and explicit empty credentials are cleared
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification transport ID" minimum(1)
// @Param request body notifications.TransportInput true "Notification transport"
// @Success 200 {object} internal.APIResponse[notifications.TransportConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/transports/{id} [put]
func UpdateTransport(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_transport_id", "invalid_transport_id")
			return
		}

		var req notifications.TransportInput
		if !bindNotificationJSON(c, &req) {
			return
		}

		updated, err := service.UpdateTransport(c.Request.Context(), uint(id), req)
		if err != nil {
			writeNotificationServiceError(c, "update_transport", "failed_to_update_notification_transport", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.TransportConfigView]{
			Status:  "success",
			Message: "notification_transport_updated",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Delete a notification transport
// @Description Delete one notification transport without affecting other configured transports
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification transport ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/transports/{id} [delete]
func DeleteTransport(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_transport_id", "invalid_transport_id")
			return
		}

		if err := service.DeleteTransport(c.Request.Context(), uint(id)); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeNotificationError(c, http.StatusNotFound, "transport_not_found", "transport_not_found")
				return
			}

			writeNotificationServiceError(c, "delete_transport", "failed_to_delete_transport", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "transport_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary List notification rules
// @Description List notification rule templates and concrete target rules, including inactive retained targets
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules [get]
func GetRules(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg, err := service.GetRuleConfig(c.Request.Context())
		if err != nil {
			writeNotificationServiceError(c, "get_rules", "failed_to_load_notification_rules", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rules_loaded",
			Error:   "",
			Data:    cfg,
		})
	}
}

// @Summary Create a notification rule
// @Description Create a concrete notification rule from a template and active target
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body notifications.RuleCreateInput true "Notification rule"
// @Success 201 {object} internal.APIResponse[notifications.RuleConfigView] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules [post]
func CreateRule(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req notifications.RuleCreateInput
		if !bindNotificationJSON(c, &req) {
			return
		}

		updated, err := service.CreateRule(c.Request.Context(), req)
		if err != nil {
			writeNotificationServiceError(c, "create_rule", "failed_to_create_notification_rule", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rule_created",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Update a notification rule
// @Description Update channel toggles and optional template-specific configuration for one notification rule
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification rule ID" minimum(1)
// @Param request body notifications.RuleUpdateInput true "Notification rule update"
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules/{id} [put]
func UpdateRule(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_notification_rule_id", "invalid_notification_rule_id")
			return
		}

		var req notifications.RuleUpdateInput
		if !bindNotificationJSON(c, &req) {
			return
		}

		updated, err := service.UpdateRule(c.Request.Context(), uint(id), req)
		if err != nil {
			writeNotificationServiceError(c, "update_rule", "failed_to_update_notification_rule", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rule_updated",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Delete a notification rule
// @Description Delete one notification rule; synchronization recreates missing rules for active auto-managed targets
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification rule ID" minimum(1)
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules/{id} [delete]
func DeleteRule(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_notification_rule_id", "invalid_notification_rule_id")
			return
		}

		updated, err := service.DeleteRule(c.Request.Context(), uint(id))
		if err != nil {
			writeNotificationServiceError(c, "delete_rule", "failed_to_delete_notification_rule", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rule_deleted",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Delete notification rules in bulk
// @Description Delete an exact set of notification rules atomically; missing active auto-managed rules are recreated by synchronization
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body internal.BulkDeleteRequest true "Notification rule IDs"
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules/bulk-delete [post]
func BulkDeleteRules(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internal.BulkDeleteRequest
		if !bindNotificationJSON(c, &req) {
			return
		}

		ids, ok := notificationRuleIDs(c, req.IDs)
		if !ok {
			return
		}

		updated, err := service.BulkDeleteRules(c.Request.Context(), ids)
		if err != nil {
			writeNotificationServiceError(c, "bulk_delete_rules", "failed_to_bulk_delete_notification_rules", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rules_bulk_deleted",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Update notification rules in bulk
// @Description Apply selected channel toggles to an exact set of notification rules atomically
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body internal.BulkUpdateRulesRequest true "Notification rule IDs and channel toggles"
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules/bulk-update [post]
func BulkUpdateRules(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internal.BulkUpdateRulesRequest
		if !bindNotificationJSON(c, &req) {
			return
		}

		ids, ok := notificationRuleIDs(c, req.IDs)
		if !ok {
			return
		}

		updated, err := service.BulkUpdateRules(c.Request.Context(), ids, req.UIEnabled, req.NtfyEnabled, req.EmailEnabled, req.DiscordEnabled)
		if err != nil {
			writeNotificationServiceError(c, "bulk_update_rules", "failed_to_bulk_update_notification_rules", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rules_bulk_updated",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Update notification rules
// @Description Apply the legacy full-bulk notification rule update contract
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body notifications.RuleConfigUpdate true "Notification rule updates"
// @Success 200 {object} internal.APIResponse[notifications.RuleConfigView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules [put]
func UpdateRules(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req notifications.RuleConfigUpdate
		if !bindNotificationJSON(c, &req) {
			return
		}

		updated, err := service.UpdateRuleConfig(c.Request.Context(), req)
		if err != nil {
			writeNotificationServiceError(c, "update_rules", "failed_to_update_notification_rules", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[notifications.RuleConfigView]{
			Status:  "success",
			Message: "notification_rules_updated",
			Error:   "",
			Data:    updated,
		})
	}
}

// @Summary Test a notification transport
// @Description Send a test notification through one configured transport, even when the transport is disabled
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Notification transport ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/transports/{id}/test [post]
func TestTransport(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_transport_id", "invalid_transport_id")
			return
		}

		err = service.TestTransport(c.Request.Context(), uint(id))
		if err != nil {
			writeNotificationServiceError(c, "test_transport", "failed_to_test_transport", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "transport_test_sent",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Test a notification rule
// @Description Emit a representative event for one template and active target through the normal notification pipeline
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body notifications.TestRuleInput true "Notification rule test"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /notifications/rules/test [post]
func TestRule(service *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req notifications.TestRuleInput
		if !bindNotificationJSON(c, &req) {
			return
		}

		err := service.TestRule(c.Request.Context(), req)
		if err != nil {
			writeNotificationServiceError(c, "test_rule", "failed_to_test_notification_rule", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "test_notification_rule_sent",
			Error:   "",
			Data:    nil,
		})
	}
}

func bindNotificationJSON[T any](c *gin.Context, target *T) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeNotificationError(c, http.StatusRequestEntityTooLarge, "request_body_too_large", "request_body_too_large")
			return false
		}
		writeNotificationError(c, http.StatusBadRequest, "invalid_request_body", "invalid_request_body")
		return false
	}
	return true
}

func parseNotificationQueryInt(c *gin.Context, name string, fallback, minimum, maximum int) (int, bool) {
	raw, exists := c.GetQuery(name)
	if !exists {
		return fallback, true
	}

	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || maximum > 0 && value > maximum {
		code := "invalid_notification_" + name
		writeNotificationError(c, http.StatusBadRequest, code, code)
		return 0, false
	}
	return value, true
}

func notificationRuleIDs(c *gin.Context, rawIDs []int) ([]uint, bool) {
	if len(rawIDs) == 0 {
		writeNotificationError(c, http.StatusBadRequest, "invalid_notification_rule_ids", "invalid_notification_rule_ids")
		return nil, false
	}

	ids := make([]uint, 0, len(rawIDs))
	seen := make(map[int]struct{}, len(rawIDs))
	for _, id := range rawIDs {
		if id <= 0 {
			writeNotificationError(c, http.StatusBadRequest, "invalid_notification_rule_ids", "invalid_notification_rule_ids")
			return nil, false
		}
		if _, exists := seen[id]; exists {
			writeNotificationError(c, http.StatusBadRequest, "duplicate_notification_rule_id", "duplicate_notification_rule_id")
			return nil, false
		}
		seen[id] = struct{}{}
		ids = append(ids, uint(id))
	}
	return ids, true
}

func writeNotificationError(c *gin.Context, status int, message, detail string) {
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   detail,
		Data:    nil,
	})
}

func writeNotificationServiceError(c *gin.Context, operation, message string, err error) {
	status := notificationServiceErrorStatus(err)
	detail := notificationServiceErrorCode(err)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if strings.Contains(operation, "transport") {
			detail = "transport_not_found"
		} else {
			detail = "notification_rule_not_found"
		}
	}
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", operation).Msg("notification_request_failed")
		detail = "internal_server_error"
	}
	writeNotificationError(c, status, message, detail)
}

func notificationServiceErrorStatus(err error) int {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}

	code := notificationServiceErrorCode(err)
	switch code {
	case "notification_rule_not_found", "notification_rule_template_not_found", "notification_rule_target_not_found":
		return http.StatusNotFound
	case "notification_rule_already_exists", "notification_rule_no_targets":
		return http.StatusConflict
	case "notification_rule_kind_mismatch", "smtp_auth_not_supported", "smtp_starttls_not_supported":
		return http.StatusBadRequest
	}

	if strings.HasPrefix(code, "invalid_") ||
		strings.HasPrefix(code, "duplicate_") ||
		strings.HasSuffix(code, "_required") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func notificationServiceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := strings.TrimSpace(err.Error())
	if prefix, _, ok := strings.Cut(code, ":"); ok {
		code = strings.TrimSpace(prefix)
	}
	return code
}
