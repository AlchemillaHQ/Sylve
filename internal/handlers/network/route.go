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

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func bindStaticRouteJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "static_route_request_too_large",
				Error:   "static_route_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_static_route_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func staticRoutePathID(c *gin.Context, errorCode string) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_id",
			Error:   errorCode,
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}

func staticRouteErrorStatus(err error) int {
	switch {
	case errors.Is(err, network.ErrInvalidStaticRoute):
		return http.StatusBadRequest
	case errors.Is(err, network.ErrStaticRouteNotFound), errors.Is(err, network.ErrFirewallNATRuleNotFound):
		return http.StatusNotFound
	case errors.Is(err, network.ErrStaticRouteConflict), errors.Is(err, network.ErrStaticRouteSuggestionUnavailable):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeStaticRouteError(c *gin.Context, message string, err error) {
	status := staticRouteErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("operation", message).Msg("static_route_request_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   network.StaticRouteErrorCode(err),
		Data:    nil,
	})
}

// @Summary List static routes
// @Description List all configured static routes
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkModels.StaticRoute] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/route [get]
func ListStaticRoutes(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		routes, err := svc.GetStaticRoutes()
		if err != nil {
			logger.L.Error().Err(err).Msg("failed_to_list_static_routes")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_static_routes",
				Error:   "static_route_list_failed",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.StaticRoute]{
			Status:  "success",
			Message: "static_routes_listed",
			Error:   "",
			Data:    routes,
		})
	}
}

// @Summary Create static route
// @Description Create and apply a static route
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body networkServiceInterfaces.UpsertStaticRouteRequest true "Static Route Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/route [post]
func CreateStaticRoute(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.UpsertStaticRouteRequest
		if !bindStaticRouteJSON(c, &req) {
			return
		}

		id, err := svc.CreateStaticRoute(&req)
		if err != nil {
			writeStaticRouteError(c, "failed_to_create_static_route", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "static_route_created",
			Error:   "",
			Data:    id,
		})
	}
}

// @Summary Update static route
// @Description Replace and apply a configured static route
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Static Route ID"
// @Param request body networkServiceInterfaces.UpsertStaticRouteRequest true "Static Route Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/route/{id} [put]
func EditStaticRoute(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := staticRoutePathID(c, "invalid_static_route_id")
		if !ok {
			return
		}

		var req networkServiceInterfaces.UpsertStaticRouteRequest
		if !bindStaticRouteJSON(c, &req) {
			return
		}

		if err := svc.EditStaticRoute(id, &req); err != nil {
			writeStaticRouteError(c, "failed_to_edit_static_route", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "static_route_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete static route
// @Description Delete a configured static route and remove it from the host routing table
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "Static Route ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/route/{id} [delete]
func DeleteStaticRoute(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := staticRoutePathID(c, "invalid_static_route_id")
		if !ok {
			return
		}

		if err := svc.DeleteStaticRoute(id); err != nil {
			writeStaticRouteError(c, "failed_to_delete_static_route", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "static_route_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Suggest routes from NAT rule
// @Description Derive static return-route suggestions from a policy-routed SNAT or BINAT rule
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param id path int true "Firewall NAT Rule ID"
// @Success 200 {object} internal.APIResponse[[]networkServiceInterfaces.StaticRouteSuggestion] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/firewall/nat/{id}/route-suggestions [get]
func SuggestStaticRoutesFromNATRule(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := staticRoutePathID(c, "invalid_firewall_nat_rule_id")
		if !ok {
			return
		}

		suggestions, err := svc.SuggestStaticRoutesFromNATRule(id)
		if err != nil {
			writeStaticRouteError(c, "failed_to_suggest_static_routes_from_nat_rule", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkServiceInterfaces.StaticRouteSuggestion]{
			Status:  "success",
			Message: "static_routes_suggested_from_nat_rule",
			Error:   "",
			Data:    suggestions,
		})
	}
}
