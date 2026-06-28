// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

// @Summary List Sysctl Tunables (Remote/Paginated)
// @Description List sysctl tunables with server-side pagination, sorting and search
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[system.TunablesResponse] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/tunables/remote [get]
func TunablesRemote(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("size", "25"))

		sortField := c.Query("sort[0][field]")
		sortDir := c.Query("sort[0][dir]")
		search := c.Query("search")

		res, err := systemService.ListTunablesPaginated(page, size, sortField, sortDir, search)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_tunables_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*system.TunablesResponse]{
			Status:  "success",
			Message: "tunables_listed",
			Data:    res,
		})
	}
}

type setTunableRequest struct {
	Name  string `json:"name" binding:"required"`
	Value string `json:"value"`
}

// @Summary Set Sysctl Tunable
// @Description Apply a writable sysctl tunable and persist it for re-application on boot
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tunable body setTunableRequest true "Tunable name and value"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/tunables [put]
func SetTunable(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setTunableRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := systemService.SetTunable(req.Name, req.Value); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "set_tunable_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "tunable_set",
			Error:   "",
			Data:    nil,
		})
	}
}
