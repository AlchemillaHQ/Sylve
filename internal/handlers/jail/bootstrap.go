// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
)

type jailBootstrapService interface {
	ListBootstraps(ctx context.Context, pool string) ([]jailServiceInterfaces.BootstrapEntry, error)
	CreateBootstrap(
		ctx context.Context,
		req jailServiceInterfaces.BootstrapRequest,
	) (jailServiceInterfaces.BootstrapCreateResult, error)
	DeleteBootstrap(
		ctx context.Context,
		pool string,
		name string,
	) (jailServiceInterfaces.BootstrapDeleteResult, error)
}

func bootstrapErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

func bootstrapErrorStatus(err error) int {
	switch bootstrapErrorCode(err) {
	case "invalid_bootstrap_pool",
		"invalid_bootstrap_name",
		"unsupported_bootstrap_type",
		"unsupported_bootstrap_version",
		"bootstrap_version_newer_than_host":
		return http.StatusBadRequest
	case "pool_not_found":
		return http.StatusNotFound
	case "bootstrap_already_in_progress",
		"bootstrap_cleanup_required",
		"bootstrap_dataset_unmanaged",
		"bootstrap_invalid_status",
		"bootstrap_record_mismatch",
		"bootstrap_mountpoint_not_usable":
		return http.StatusConflict
	case "pkgbase_signing_keys_not_found", "pkg_not_found":
		return http.StatusServiceUnavailable
	case "bootstrap_system_service_unavailable", "bootstrap_zfs_service_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// @Summary List bootstraps
// @Description List all supported pkgbase bootstrap entries for a pool, with their current install status
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pool query string true "Pool name"
// @Success 200 {object} internal.APIResponse[[]jailServiceInterfaces.BootstrapEntry] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/bootstraps [get]
func ListBootstraps(jailService jailBootstrapService) gin.HandlerFunc {
	return func(c *gin.Context) {
		pool := strings.TrimSpace(c.Query("pool"))
		if pool == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_required",
				Error:   "query parameter 'pool' is required",
				Data:    nil,
			})
			return
		}

		entries, err := jailService.ListBootstraps(c.Request.Context(), pool)
		if err != nil {
			code := bootstrapErrorCode(err)
			c.JSON(bootstrapErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: code,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailServiceInterfaces.BootstrapEntry]{
			Status:  "success",
			Message: "bootstraps_listed",
			Data:    entries,
			Error:   "",
		})
	}
}

// @Summary Delete bootstrap
// @Description Idempotently destroy one exact inactive pkgbase bootstrap dataset and remove its state record
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param pool query string true "Pool name"
// @Param name path string true "Canonical bootstrap name (e.g. 15-0-Base)"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.BootstrapDeleteResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/bootstraps/{name} [delete]
func DeleteBootstrap(jailService jailBootstrapService) gin.HandlerFunc {
	return func(c *gin.Context) {
		pool := strings.TrimSpace(c.Query("pool"))
		name := strings.TrimSpace(c.Param("name"))
		if pool == "" || name == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "pool_and_name_required",
				Error:   "query parameter 'pool' and path parameter 'name' are required",
				Data:    nil,
			})
			return
		}

		result, err := jailService.DeleteBootstrap(c.Request.Context(), pool, name)
		if err != nil {
			code := bootstrapErrorCode(err)
			c.JSON(bootstrapErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: code,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		message := "bootstrap_deleted"
		if result.Outcome == "already_absent" {
			message = "bootstrap_already_absent"
		}
		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.BootstrapDeleteResult]{
			Status:  "success",
			Message: message,
			Data:    result,
			Error:   "",
		})
	}
}

// @Summary Create bootstrap
// @Description Queue a pkgbase bootstrap for a pool, version, and type, or return the existing completed member without starting duplicate work
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body jailServiceInterfaces.BootstrapRequest true "Bootstrap Request"
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.BootstrapCreateResult] "Already Completed"
// @Success 202 {object} internal.APIResponse[jailServiceInterfaces.BootstrapCreateResult] "Accepted"
// @Header 200 {string} Location "/api/jail/bootstraps/{name}?pool={pool}"
// @Header 202 {string} Location "/api/jail/bootstraps/{name}?pool={pool}"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/bootstraps [post]
func CreateBootstrap(jailService jailBootstrapService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req jailServiceInterfaces.BootstrapRequest
		if !bindJailJSON(c, &req, "invalid_request") {
			return
		}

		result, err := jailService.CreateBootstrap(c.Request.Context(), req)
		if err != nil {
			code := bootstrapErrorCode(err)
			c.JSON(bootstrapErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: code,
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		status := http.StatusAccepted
		message := "bootstrap_queued"
		if result.Outcome == "already_completed" {
			status = http.StatusOK
			message = "bootstrap_already_completed"
		} else if result.Outcome != "queued" {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_bootstrap_create_outcome",
				Error:   "service returned an unsupported bootstrap create outcome",
				Data:    nil,
			})
			return
		}

		location := "/api/jail/bootstraps/" + url.PathEscape(result.Name) +
			"?pool=" + url.QueryEscape(result.Pool)
		c.Header("Location", location)
		c.JSON(status, internal.APIResponse[jailServiceInterfaces.BootstrapCreateResult]{
			Status:  "success",
			Message: message,
			Data:    result,
			Error:   "",
		})
	}
}
