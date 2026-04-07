// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/gin-gonic/gin"
)

// @Summary List bootstraps
// @Description List all supported pkgbase bootstrap entries for a pool, with their current install status
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pool query string true "Pool name"
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.BootstrapEntry] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/bootstraps [get]
func ListBootstraps(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pool := c.Query("pool")
		if pool == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_required",
				Error:   "query parameter 'pool' is required",
			})
			return
		}

		entries, err := jailService.ListBootstraps(c.Request.Context(), pool)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_bootstraps",
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailServiceInterfaces.BootstrapEntry]{
			Status:  "success",
			Message: "bootstraps_listed",
			Data:    entries,
		})
	}
}

// @Summary Delete bootstrap
// @Description Destroy a completed pkgbase bootstrap dataset and remove its DB record
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pool query string true "Pool name"
// @Param name query string true "Bootstrap name (e.g. 15-0-Base)"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/bootstrap [delete]
func DeleteBootstrap(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pool := c.Query("pool")
		name := c.Query("name")
		if pool == "" || name == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_and_name_required",
				Error:   "query parameters 'pool' and 'name' are required",
			})
			return
		}

		if err := jailService.DeleteBootstrap(c.Request.Context(), pool, name); err != nil {
			msg := err.Error()
			statusCode := http.StatusInternalServerError
			if msg == "bootstrap_in_progress" {
				statusCode = http.StatusConflict
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   msg,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "bootstrap_deleted",
		})
	}
}

// @Summary Create bootstrap
// @Description Start a pkgbase bootstrap for the given pool, version, and type. Returns immediately; bootstrap runs asynchronously.
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body jailServiceInterfaces.BootstrapRequest true "Bootstrap Request"
// @Success 202 {object} internal.APIResponse[any] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/bootstrap [post]
func CreateBootstrap(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.BootstrapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
			})
			return
		}

		if err := jailService.CreateBootstrap(c.Request.Context(), req); err != nil {
			msg := err.Error()
			statusCode := http.StatusInternalServerError
			if msg == "bootstrap_already_in_progress" {
				statusCode = http.StatusConflict
			} else if msg == "pool_not_found" ||
				msg == "unsupported_bootstrap_type: "+req.Type ||
				strings.HasPrefix(msg, "pkgbase_signing_keys_not_found") ||
				msg == "pkg_not_found" {
				statusCode = http.StatusBadRequest
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: msg,
				Error:   msg,
			})
			return
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: "bootstrap_started",
		})
	}
}
