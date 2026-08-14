// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/gin-gonic/gin"
)

var setTunableOperation = func(systemService *system.Service, name, value string) error {
	return systemService.SetTunable(name, value)
}

// @Summary List Sysctl Tunables
// @Description List sysctl tunables with server-side pagination, sorting, search, and configured-only filtering
// @Tags System
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number" default(1) minimum(1)
// @Param size query int false "Page size" default(25) minimum(1) maximum(100)
// @Param search query string false "Search tunable names"
// @Param configuredOnly query bool false "Only include tunables configured through Sylve"
// @Param sort[0][field] query string false "Sort field" Enums(name,value,type,writable)
// @Param sort[0][dir] query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} internal.APIResponse[system.TunablesResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/tunables/remote [get]
func TunablesRemote(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("size", "25"))

		sortField := c.Query("sort[0][field]")
		sortDir := c.Query("sort[0][dir]")
		search := c.Query("search")
		configuredOnly, err := strconv.ParseBool(c.DefaultQuery("configuredOnly", "false"))
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_configured_only_param",
				Error:   "invalid_configured_only_param",
				Data:    nil,
			})
			return
		}

		res, err := systemService.ListTunablesPaginated(page, size, sortField, sortDir, search, configuredOnly)
		if err != nil {
			logger.L.Error().Err(err).Msg("list_tunables_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_tunables_failed",
				Error:   "list_tunables_failed",
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

type SetTunableRequest struct {
	Name  string  `json:"name" binding:"required"`
	Value *string `json:"value" binding:"required"`
}

func bindTunableJSON(c *gin.Context, request *SetTunableRequest) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "tunable_request_too_large",
				Error:   "tunable_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_tunable_request",
			Error:   "invalid_tunable_request",
			Data:    nil,
		})
		return false
	}

	return true
}

func tunableErrorStatus(err error) int {
	switch {
	case errors.Is(err, system.ErrTunableNameRequired),
		errors.Is(err, system.ErrTunableNotWritable),
		errors.Is(err, system.ErrInvalidTunableValue):
		return http.StatusBadRequest
	case errors.Is(err, system.ErrTunableNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeTunableError(c *gin.Context, name string, err error) {
	status := tunableErrorStatus(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Err(err).Str("tunable", name).Msg("set_tunable_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: "set_tunable_failed",
		Error:   system.TunableErrorCode(err),
		Data:    map[string]string{"resource": name},
	})
}

// @Summary Set Sysctl Tunable
// @Description Apply a writable sysctl tunable and persist it for re-application on boot
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tunable body SetTunableRequest true "Tunable name and value"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/tunables [put]
func SetTunable(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SetTunableRequest
		if !bindTunableJSON(c, &req) {
			return
		}

		if err := setTunableOperation(systemService, req.Name, *req.Value); err != nil {
			writeTunableError(c, req.Name, err)
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
