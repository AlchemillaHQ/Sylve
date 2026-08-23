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

func bindFirewallNATJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "firewall_nat_request_too_large",
				Error:   "firewall_nat_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_firewall_nat_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func firewallNATRulePathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_id",
			Error:   "invalid_firewall_nat_rule_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func firewallNATRuleErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidFirewallNATRule):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrFirewallNATRuleNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrHiddenFirewallRuleMutation), errors.Is(err, network.ErrFirewallNATRuleConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeFirewallNATRuleError(c *gin.Context, message string, fallbackCode string, err error) {
	status := firewallNATRuleErrorStatus(err)
	code := fallbackCode
	if status != http.StatusInternalServerError {
		code = network.FirewallNATRuleErrorCode(err)
	} else {
		logger.L.Error().Err(err).Str("operation", message).Msg("firewall_nat_rule_request_failed")
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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

// @Summary Bulk Delete Firewall Traffic Rules
// @Description Delete and apply a validated collection of user-managed firewall traffic rules as one operation
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body networkServiceInterfaces.BulkDeleteFirewallTrafficRulesRequest true "Bulk Delete Firewall Traffic Rules Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/traffic [delete]
func BulkDeleteFirewallTrafficRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.BulkDeleteFirewallTrafficRulesRequest
		if !bindFirewallTrafficJSON(c, &req) {
			return
		}

		if err := svc.DeleteFirewallTrafficRules(req.IDs); err != nil {
			writeFirewallTrafficRuleError(c, "failed_to_delete_firewall_traffic_rules", "firewall_traffic_rules_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_traffic_rules_deleted",
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
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
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

// @Summary List Firewall NAT Rules
// @Description List all user-visible firewall NAT rules in evaluation order
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[[]networkModels.FirewallNATRule] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat [get]
func ListFirewallNATRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rules, err := svc.GetFirewallNATRules()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_firewall_nat_rules")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_nat_rules",
				Error:   "firewall_nat_rule_list_failed",
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

// @Summary List Firewall NAT Rule Counters
// @Description List cumulative packet and byte counters for user-visible firewall NAT rules
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[[]networkServiceInterfaces.FirewallNATRuleCounter] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat/counters [get]
func ListFirewallNATRuleCounters(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		counters, err := svc.GetFirewallNATRuleCounters()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_firewall_nat_rule_counters")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_nat_rule_counters",
				Error:   "firewall_nat_rule_counter_list_failed",
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

func writeFirewallLiveHitsQueryError(c *gin.Context, message string, code string) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   code,
		Data:    nil,
	})
}

func isFirewallLiveHitAction(action string) bool {
	switch action {
	case "pass", "block", "scrub", "nat", "binat", "rdr":
		return true
	default:
		return false
	}
}

// @Summary List Live Firewall Logs
// @Description List live firewall rule hits after a cursor. Cursor 0 starts at the current stream position without replaying retained history.
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param cursor query int false "Return hits after this cursor; 0 initializes the live stream" minimum(0) default(0)
// @Param limit query int false "Maximum hits to return; values above 2000 are capped" minimum(1) maximum(2000) default(200)
// @Param ruleType query string false "Rule type" Enums(traffic,nat)
// @Param ruleId query int false "Rule ID" minimum(1)
// @Param action query string false "PF action" Enums(pass,block,scrub,nat,binat,rdr)
// @Param direction query string false "Packet direction" Enums(in,out)
// @Param interface query string false "PF interface name"
// @Param query query string false "Text search over the rule name, ID, and rendered log line"
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.FirewallLiveHitsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/logs/live [get]
func ListFirewallLiveHits(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		cursor := int64(0)
		if rawCursor := strings.TrimSpace(c.Query("cursor")); rawCursor != "" {
			parsed, err := strconv.ParseInt(rawCursor, 10, 64)
			if err != nil || parsed < 0 {
				writeFirewallLiveHitsQueryError(c, "invalid_cursor", "invalid_firewall_live_hits_cursor")
				return
			}
			cursor = parsed
		}

		limit := 0
		if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed <= 0 {
				writeFirewallLiveHitsQueryError(c, "invalid_limit", "invalid_firewall_live_hits_limit")
				return
			}
			limit = parsed
		}

		filter := &networkServiceInterfaces.FirewallLiveHitsFilter{}
		if ruleType := strings.ToLower(strings.TrimSpace(c.Query("ruleType"))); ruleType != "" {
			if ruleType != "traffic" && ruleType != "nat" {
				writeFirewallLiveHitsQueryError(c, "invalid_rule_type", "invalid_firewall_live_hits_rule_type")
				return
			}
			filter.RuleType = ruleType
		}

		if rawRuleID := strings.TrimSpace(c.Query("ruleId")); rawRuleID != "" {
			parsed, err := strconv.ParseUint(rawRuleID, 10, strconv.IntSize)
			if err != nil || parsed == 0 {
				writeFirewallLiveHitsQueryError(c, "invalid_rule_id", "invalid_firewall_live_hits_rule_id")
				return
			}
			id := uint(parsed)
			filter.RuleID = &id
		}

		if action := strings.ToLower(strings.TrimSpace(c.Query("action"))); action != "" {
			if !isFirewallLiveHitAction(action) {
				writeFirewallLiveHitsQueryError(c, "invalid_action", "invalid_firewall_live_hits_action")
				return
			}
			filter.Action = action
		}
		if direction := strings.ToLower(strings.TrimSpace(c.Query("direction"))); direction != "" {
			if direction != "in" && direction != "out" {
				writeFirewallLiveHitsQueryError(c, "invalid_direction", "invalid_firewall_live_hits_direction")
				return
			}
			filter.Direction = direction
		}
		filter.Interface = strings.TrimSpace(c.Query("interface"))
		filter.Query = strings.TrimSpace(c.Query("query"))

		hits, err := svc.GetFirewallLiveHits(cursor, limit, filter)
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_firewall_live_hits")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_firewall_live_hits",
				Error:   "firewall_live_hits_list_failed",
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

// @Summary Create Firewall NAT Rule
// @Description Create and apply a firewall NAT rule
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body networkServiceInterfaces.UpsertFirewallNATRuleRequest true "Create Firewall NAT Rule Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat [post]
func CreateFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.UpsertFirewallNATRuleRequest
		if !bindFirewallNATJSON(c, &req) {
			return
		}

		id, err := svc.CreateFirewallNATRule(&req)
		if err != nil {
			writeFirewallNATRuleError(c, "failed_to_create_firewall_nat_rule", "firewall_nat_rule_create_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "firewall_nat_rule_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Update Firewall NAT Rule
// @Description Replace and apply an existing user-managed firewall NAT rule
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param id path int true "NAT Rule ID" minimum(1)
// @Param request body networkServiceInterfaces.UpsertFirewallNATRuleRequest true "Update Firewall NAT Rule Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat/{id} [put]
func EditFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := firewallNATRulePathID(c)
		if !ok {
			return
		}

		var req networkServiceInterfaces.UpsertFirewallNATRuleRequest
		if !bindFirewallNATJSON(c, &req) {
			return
		}

		if err := svc.EditFirewallNATRule(id, &req); err != nil {
			writeFirewallNATRuleError(c, "failed_to_edit_firewall_nat_rule", "firewall_nat_rule_update_failed", err)
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

// @Summary Delete Firewall NAT Rule
// @Description Delete and apply one user-managed firewall NAT rule by ID
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param id path int true "NAT Rule ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat/{id} [delete]
func DeleteFirewallNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := firewallNATRulePathID(c)
		if !ok {
			return
		}

		if err := svc.DeleteFirewallNATRule(id); err != nil {
			writeFirewallNATRuleError(c, "failed_to_delete_firewall_nat_rule", "firewall_nat_rule_delete_failed", err)
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

// @Summary Bulk Delete Firewall NAT Rules
// @Description Delete and apply a validated collection of user-managed firewall NAT rules as one operation
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body networkServiceInterfaces.BulkDeleteFirewallNATRulesRequest true "Bulk Delete Firewall NAT Rules Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat [delete]
func BulkDeleteFirewallNATRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.BulkDeleteFirewallNATRulesRequest
		if !bindFirewallNATJSON(c, &req) {
			return
		}

		if err := svc.DeleteFirewallNATRules(req.IDs); err != nil {
			writeFirewallNATRuleError(c, "failed_to_delete_firewall_nat_rules", "firewall_nat_rules_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "firewall_nat_rules_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Reorder Firewall NAT Rules
// @Description Replace the evaluation order of all user-visible firewall NAT rules and apply it
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body []networkServiceInterfaces.FirewallReorderRequest true "Complete NAT Rule Reorder Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat/reorder [put]
func ReorderFirewallNATRules(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req []networkServiceInterfaces.FirewallReorderRequest
		if !bindFirewallNATJSON(c, &req) {
			return
		}

		if err := svc.ReorderFirewallNATRules(req); err != nil {
			writeFirewallNATRuleError(c, "failed_to_reorder_firewall_nat_rules", "firewall_nat_rule_reorder_failed", err)
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

func bindFirewallAdvancedJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "firewall_advanced_request_too_large",
				Error:   "firewall_advanced_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_firewall_advanced_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func writeFirewallAdvancedError(c *gin.Context, message string, fallbackCode string, err error) {
	if errors.Is(err, network.ErrInvalidFirewallAdvancedSettings) {
		var details *networkServiceInterfaces.FirewallAdvancedValidationDetails
		if detail := network.FirewallAdvancedValidationDetail(err); detail != "" {
			details = &networkServiceInterfaces.FirewallAdvancedValidationDetails{Detail: detail}
		}
		c.JSON(http.StatusBadRequest, internal.APIResponse[*networkServiceInterfaces.FirewallAdvancedValidationDetails]{
			Status:  "error",
			Message: message,
			Error:   "invalid_firewall_advanced_settings",
			Data:    details,
		})
		return
	}

	logger.L.Error().Err(err).Str("operation", message).Msg("firewall_advanced_request_failed")
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   fallbackCode,
		Data:    nil,
	})
}

// @Summary Get Firewall Advanced Settings
// @Description Retrieve the locally managed advanced PF rule sections
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[networkModels.FirewallAdvancedSettings] "Success"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/advanced [get]
func GetFirewallAdvancedSettings(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := svc.GetFirewallAdvancedSettings()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_get_firewall_advanced_settings")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_firewall_advanced_settings",
				Error:   "firewall_advanced_settings_get_failed",
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

// @Summary Update Firewall Advanced Settings
// @Description Replace the locally managed advanced PF rule sections and apply the resulting configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body networkServiceInterfaces.FirewallAdvancedRequest true "Firewall Advanced Settings Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/advanced [put]
func UpdateFirewallAdvancedSettings(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.FirewallAdvancedRequest
		if !bindFirewallAdvancedJSON(c, &req) {
			return
		}

		if err := svc.UpdateFirewallAdvancedSettings(&req); err != nil {
			writeFirewallAdvancedError(c, "failed_to_update_firewall_advanced_settings", "firewall_advanced_settings_update_failed", err)
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

// @Summary Preview Firewall Configuration
// @Description Validate and render a candidate advanced PF configuration without persisting or applying it
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Param request body networkServiceInterfaces.FirewallAdvancedRequest true "Firewall Advanced Settings Preview Request"
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.RenderedConfigResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/advanced/preview [post]
func PreviewRenderedConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.FirewallAdvancedRequest
		if !bindFirewallAdvancedJSON(c, &req) {
			return
		}

		rendered, err := svc.PreviewRenderedConfig(&req)
		if err != nil {
			writeFirewallAdvancedError(c, "preview_failed", "firewall_advanced_preview_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkServiceInterfaces.RenderedConfigResponse]{
			Status:  "success",
			Message: "preview_rendered",
			Error:   "",
			Data:    rendered,
		})
	}
}

// @Summary Get Rendered Firewall Configuration
// @Description Retrieve the currently rendered PF configuration files from disk
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.RenderedConfigResponse] "Success"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/advanced/rendered [get]
func GetRenderedConfig(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rendered, err := svc.GetRenderedConfigOnDisk()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_get_rendered_firewall_config")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_rendered_config",
				Error:   "firewall_rendered_config_get_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkServiceInterfaces.RenderedConfigResponse]{
			Status:  "success",
			Message: "rendered_config_retrieved",
			Error:   "",
			Data:    rendered,
		})
	}
}
