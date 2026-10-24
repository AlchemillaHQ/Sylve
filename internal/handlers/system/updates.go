// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/hostupdates"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type hostUpdateService interface {
	Get(context.Context) (hostupdates.View, error)
	GetRelease(context.Context, bool) (hostupdates.ReleaseView, error)
	Start(context.Context, string, hostupdates.Request, string) (hostupdates.Job, error)
	Job(context.Context, uint) (hostupdates.Job, error)
	Logs(context.Context, uint, int64) (string, int64, error)
	SetMonitoring(context.Context, hostupdates.Monitoring) (models.HostUpdateState, error)
	SetReleaseChannel(context.Context, string) (models.HostUpdateState, error)
}

func RegisterHostUpdateRoutes(group *gin.RouterGroup, service *hostupdates.Service) {
	var api hostUpdateService
	if service != nil {
		api = service
	}
	group.GET("", GetHostUpdates(api))
	group.GET("/sylve/release", GetSylveRelease(api))
	group.POST("/actions/:action", StartHostUpdate(api))
	group.GET("/jobs/:id", GetHostUpdateJob(api, false))
	group.GET("/jobs/:id/logs", GetHostUpdateJob(api, true))
	group.PUT("/monitoring", SetHostUpdateMonitoring(api))
	group.PUT("/sylve/channel", SetSylveReleaseChannel(api))
}

func updateResponse(c *gin.Context, value any, err error, status int) {
	if err == nil {
		c.JSON(status, internal.APIResponse[any]{Status: "success", Message: "host_updates", Data: value})
		return
	}
	code, _, _ := strings.Cut(err.Error(), ":")
	code = strings.TrimSpace(code)
	status = http.StatusInternalServerError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case code == "host_update_service_unavailable" || strings.HasPrefix(code, "pkg_"):
		status = http.StatusServiceUnavailable
	case errors.Is(err, gorm.ErrRecordNotFound) || strings.HasSuffix(code, "_not_found"):
		status = http.StatusNotFound
	case strings.HasPrefix(code, "invalid_") || code == "too_many_packages":
		status = http.StatusBadRequest
	case strings.HasPrefix(code, "host_") || strings.HasPrefix(code, "sylve_") || strings.HasPrefix(code, "release_") || strings.HasPrefix(code, "package_") || strings.HasPrefix(code, "standalone_") || strings.HasPrefix(code, "boot_environment_") || code == "no_package_changes":
		status = http.StatusConflict
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: code, Error: pkgmanager.Redact(err.Error())})
}

func GetHostUpdates(service hostUpdateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		value, err := service.Get(c.Request.Context())
		updateResponse(c, value, err, http.StatusOK)
	}
}

func GetSylveRelease(service hostUpdateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		refresh, err := strconv.ParseBool(c.DefaultQuery("refresh", "false"))
		if err != nil {
			updateResponse(c, nil, errors.New("invalid_host_update_request"), 0)
			return
		}
		value, err := service.GetRelease(c.Request.Context(), refresh)
		updateResponse(c, value, err, http.StatusOK)
	}
}

func StartHostUpdate(service hostUpdateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		var request hostupdates.Request
		if err := c.ShouldBindJSON(&request); err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{Status: "error", Message: "host_update_request_too_large"})
				return
			}
			updateResponse(c, nil, errors.New("invalid_host_update_request"), 0)
			return
		}
		job, err := service.Start(c.Request.Context(), c.Param("action"), request, c.GetString("Username"))
		if err == nil {
			c.Set("AuditAsyncJobID", job.ID)
			c.Set("AuditAsyncJobType", "host_update")
			c.Header("Location", fmt.Sprintf("/api/system/updates/jobs/%d", job.ID))
		}
		updateResponse(c, job, err, http.StatusAccepted)
	}
}

func GetHostUpdateJob(service hostUpdateService, logs bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		id, err := utils.ParamUint(c, "id")
		if err != nil {
			updateResponse(c, nil, err, 0)
			return
		}
		if logs {
			offset, err := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
			if err != nil || offset < 0 {
				updateResponse(c, nil, errors.New("invalid_log_offset"), 0)
				return
			}
			text, next, err := service.Logs(c.Request.Context(), id, offset)
			updateResponse(c, map[string]any{"text": text, "next": next}, err, http.StatusOK)
			return
		}
		job, err := service.Job(c.Request.Context(), id)
		updateResponse(c, job, err, http.StatusOK)
	}
}

func SetHostUpdateMonitoring(service hostUpdateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		var request hostupdates.Monitoring
		if c.ShouldBindJSON(&request) != nil {
			updateResponse(c, nil, errors.New("invalid_host_update_request"), 0)
			return
		}
		state, err := service.SetMonitoring(c.Request.Context(), request)
		updateResponse(c, state, err, http.StatusOK)
	}
}

func SetSylveReleaseChannel(service hostUpdateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			updateResponse(c, nil, errors.New("host_update_service_unavailable"), 0)
			return
		}
		var request struct {
			Channel string `json:"channel"`
		}
		if c.ShouldBindJSON(&request) != nil {
			updateResponse(c, nil, errors.New("invalid_host_update_request"), 0)
			return
		}
		state, err := service.SetReleaseChannel(c.Request.Context(), request.Channel)
		updateResponse(c, state, err, http.StatusOK)
	}
}
