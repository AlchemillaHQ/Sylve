// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func bindFirewallTrafficJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "firewall_traffic_request_too_large",
				Error:   "firewall_traffic_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_firewall_traffic_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func firewallTrafficRulePathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_id",
			Error:   "invalid_firewall_traffic_rule_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func firewallTrafficRuleErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidFirewallTrafficRule):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrFirewallTrafficRuleNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrHiddenFirewallRuleMutation), errors.Is(err, network.ErrFirewallTrafficRuleConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeFirewallTrafficRuleError(c *gin.Context, message string, fallbackCode string, err error) {
	status := firewallTrafficRuleErrorStatus(err)
	code := fallbackCode
	if status != http.StatusInternalServerError {
		code = network.FirewallTrafficRuleErrorCode(err)
	} else {
		logger.L.Error().Err(err).Str("operation", message).Msg("firewall_traffic_rule_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   code,
		Data:    nil,
	})
}

// @Summary List Firewall Traffic Rules
// @Description List all user-visible firewall traffic rules in evaluation order
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkModels.FirewallTrafficRule] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic [get]
func ListFirewallTrafficRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rules, err := svc.GetFirewallTrafficRules()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_firewall_traffic_rules")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_traffic_rules",
				Error:   "firewall_traffic_rule_list_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.FirewallTrafficRule]{
			Status:  "success",
			Message: "firewall_traffic_rules_listed",
			Error:   "",
			Data:    rules,
		})
	}
}

// @Summary List Firewall Traffic Rule Counters
// @Description List cumulative packet and byte counters for user-visible firewall traffic rules
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkServiceInterfaces.FirewallTrafficRuleCounter] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic/counters [get]
func ListFirewallTrafficRuleCounters(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		counters, err := svc.GetFirewallTrafficRuleCounters()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_firewall_traffic_rule_counters")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_traffic_rule_counters",
				Error:   "firewall_traffic_rule_counter_list_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkServiceInterfaces.FirewallTrafficRuleCounter]{
			Status:  "success",
			Message: "firewall_traffic_rule_counters_listed",
			Error:   "",
			Data:    counters,
		})
	}
}

// @Summary Create Firewall Traffic Rule
// @Description Create and apply a firewall traffic rule
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.UpsertFirewallTrafficRuleRequest true "Create Firewall Traffic Rule Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic [post]
func CreateFirewallTrafficRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.UpsertFirewallTrafficRuleRequest
		if !bindFirewallTrafficJSON(c, &req) {
			return
		}

		id, err := svc.CreateFirewallTrafficRule(&req)
		if err != nil {
			writeFirewallTrafficRuleError(c, "failed_to_create_firewall_traffic_rule", "firewall_traffic_rule_create_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "firewall_traffic_rule_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Update Firewall Traffic Rule
// @Description Replace and apply an existing user-managed firewall traffic rule
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Traffic Rule ID" minimum(1)
// @Param request body networkServiceInterfaces.UpsertFirewallTrafficRuleRequest true "Update Firewall Traffic Rule Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic/{id} [put]
func EditFirewallTrafficRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := firewallTrafficRulePathID(c)
		if !ok {
			return
		}

		var req networkServiceInterfaces.UpsertFirewallTrafficRuleRequest
		if !bindFirewallTrafficJSON(c, &req) {
			return
		}

		if err := svc.EditFirewallTrafficRule(id, &req); err != nil {
			writeFirewallTrafficRuleError(c, "failed_to_edit_firewall_traffic_rule", "firewall_traffic_rule_update_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_traffic_rule_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Firewall Traffic Rule
// @Description Delete and apply one user-managed firewall traffic rule by ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "Traffic Rule ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic/{id} [delete]
func DeleteFirewallTrafficRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := firewallTrafficRulePathID(c)
		if !ok {
			return
		}

		if err := svc.DeleteFirewallTrafficRule(id); err != nil {
			writeFirewallTrafficRuleError(c, "failed_to_delete_firewall_traffic_rule", "firewall_traffic_rule_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_traffic_rule_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Reorder Firewall Traffic Rules
// @Description Replace the evaluation order of all user-visible firewall traffic rules and apply it
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body []networkServiceInterfaces.FirewallReorderRequest true "Complete Traffic Rule Reorder Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic/reorder [put]
func ReorderFirewallTrafficRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req []networkServiceInterfaces.FirewallReorderRequest
		if !bindFirewallTrafficJSON(c, &req) {
			return
		}

		if err := svc.ReorderFirewallTrafficRules(req); err != nil {
			writeFirewallTrafficRuleError(c, "failed_to_reorder_firewall_traffic_rules", "firewall_traffic_rule_reorder_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_traffic_rules_reordered",
			Error:   "",
			Data:    nil,
		})
	}
}

func ListFirewallNATRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rules, err := svc.GetFirewallNATRules()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_nat_rules",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.FirewallNATRule]{
			Status:  "success",
			Message: "firewall_nat_rules_listed",
			Error:   "",
			Data:    rules,
		})
	}
}

func ListFirewallNATRuleCounters(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		counters, err := svc.GetFirewallNATRuleCounters()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_nat_rule_counters",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkServiceInterfaces.FirewallNATRuleCounter]{
			Status:  "success",
			Message: "firewall_nat_rule_counters_listed",
			Error:   "",
			Data:    counters,
		})
	}
}

func ListFirewallLiveHits(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cursor := int64(0)
		if rawCursor := c.Query("cursor"); strings.TrimSpace(rawCursor) != "" {
			parsed, err := strconv.ParseInt(rawCursor, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_cursor",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			cursor = parsed
		}

		limit := 0
		if rawLimit := c.Query("limit"); strings.TrimSpace(rawLimit) != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_limit",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			limit = parsed
		}

		filter := &networkServiceInterfaces.FirewallLiveHitsFilter{}
		if ruleType := strings.ToLower(strings.TrimSpace(c.Query("ruleType"))); ruleType != "" {
			if ruleType != "traffic" && ruleType != "nat" {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_rule_type",
					Error:   "ruleType must be one of: traffic, nat",
					Data:    nil,
				})
				return
			}
			filter.RuleType = ruleType
		}

		if rawRuleID := strings.TrimSpace(c.Query("ruleId")); rawRuleID != "" {
			parsed, err := strconv.ParseUint(rawRuleID, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_rule_id",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}
			id := uint(parsed)
			filter.RuleID = &id
		}

		if action := strings.ToLower(strings.TrimSpace(c.Query("action"))); action != "" {
			filter.Action = action
		}
		if direction := strings.ToLower(strings.TrimSpace(c.Query("direction"))); direction != "" {
			if direction != "in" && direction != "out" {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_direction",
					Error:   "direction must be one of: in, out",
					Data:    nil,
				})
				return
			}
			filter.Direction = direction
		}
		if iface := strings.TrimSpace(c.Query("interface")); iface == "" {
			filter.Interface = strings.TrimSpace(c.Query("iface"))
		} else {
			filter.Interface = iface
		}
		if query := strings.TrimSpace(c.Query("query")); query == "" {
			filter.Query = strings.TrimSpace(c.Query("q"))
		} else {
			filter.Query = query
		}

		hits, err := svc.GetFirewallLiveHits(cursor, limit, filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_live_hits",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkServiceInterfaces.FirewallLiveHitsResponse]{
			Status:  "success",
			Message: "firewall_live_hits_listed",
			Error:   "",
			Data:    hits,
		})
	}
}

func CreateFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.UpsertFirewallNATRuleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		id, err := svc.CreateFirewallNATRule(&req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_firewall_nat_rule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[uint]{
			Status:  "success",
			Message: "firewall_nat_rule_created",
			Error:   "",
			Data:    id,
		})
	}
}

func EditFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req networkServiceInterfaces.UpsertFirewallNATRuleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.EditFirewallNATRule(uint(id), &req); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "hidden_firewall_rule_managed_by_wireguard") {
				status = http.StatusBadRequest
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_edit_firewall_nat_rule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_nat_rule_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

func DeleteFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.DeleteFirewallNATRule(uint(id)); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "hidden_firewall_rule_managed_by_wireguard") {
				status = http.StatusBadRequest
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_firewall_nat_rule",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_nat_rule_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

func ReorderFirewallNATRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req []networkServiceInterfaces.FirewallReorderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.ReorderFirewallNATRules(req); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "hidden_firewall_rule_managed_by_wireguard") {
				status = http.StatusBadRequest
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_reorder_firewall_nat_rules",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_nat_rules_reordered",
			Error:   "",
			Data:    nil,
		})
	}
}

func GetFirewallAdvancedSettings(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := svc.GetFirewallAdvancedSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_firewall_advanced_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkModels.FirewallAdvancedSettings]{
			Status:  "success",
			Message: "firewall_advanced_settings_retrieved",
			Error:   "",
			Data:    settings,
		})
	}
}

func UpdateFirewallAdvancedSettings(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.FirewallAdvancedRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.UpdateFirewallAdvancedSettings(&req); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_firewall_advanced_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_advanced_settings_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

func PreviewRenderedConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.FirewallAdvancedRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		rendered, err := svc.PreviewRenderedConfig(&req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "preview_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "preview_rendered",
			Error:   "",
			Data:    rendered,
		})
	}
}

func GetRenderedConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rendered, err := svc.GetRenderedConfigOnDisk()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_rendered_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "rendered_config_retrieved",
			Error:   "",
			Data:    rendered,
		})
	}
}
