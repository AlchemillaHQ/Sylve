// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

func ListStaticRoutes(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		routes, err := svc.GetStaticRoutes()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_static_routes",
				Error:   err.Error(),
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

func CreateStaticRoute(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.UpsertStaticRouteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		id, err := svc.CreateStaticRoute(&req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_static_route",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[uint]{
			Status:  "success",
			Message: "static_route_created",
			Error:   "",
			Data:    id,
		})
	}
}

func EditStaticRoute(svc *network.Service) gin.HandlerFunc {
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

		var req networkServiceInterfaces.UpsertStaticRouteRequest
		if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   bindErr.Error(),
				Data:    nil,
			})
			return
		}

		if updateErr := svc.EditStaticRoute(uint(id), &req); updateErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_edit_static_route",
				Error:   updateErr.Error(),
				Data:    nil,
			})
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

func DeleteStaticRoute(svc *network.Service) gin.HandlerFunc {
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

		if deleteErr := svc.DeleteStaticRoute(uint(id)); deleteErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_static_route",
				Error:   deleteErr.Error(),
				Data:    nil,
			})
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

func SuggestStaticRoutesFromNATRule(svc *network.Service) gin.HandlerFunc {
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

		suggestions, suggestErr := svc.SuggestStaticRoutesFromNATRule(uint(id))
		if suggestErr != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_suggest_static_routes_from_nat_rule",
				Error:   suggestErr.Error(),
				Data:    nil,
			})
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
