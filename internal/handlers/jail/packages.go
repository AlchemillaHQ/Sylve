// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type jailPackageService interface {
	GetPackageDetails(context.Context, uint, string) (jailTypes.PackageDetails, error)
	GetPackages(context.Context, uint) (jailTypes.PackageView, error)
	StartPackageOperation(context.Context, uint, string, jailTypes.PackageRequest, string, string) (jailTypes.PackageJob, error)
	GetPackageJob(context.Context, uint, uint) (jailTypes.PackageJob, error)
	PackageLogs(context.Context, uint, uint, int64) (jailTypes.PackageLog, error)
	GetPackageMonitoring(context.Context, uint) (jailTypes.PackageMonitoring, error)
	SetPackageMonitoring(context.Context, uint, jailTypes.PackageMonitoring) (jailTypes.PackageMonitoring, error)
}

func packageResponse(c *gin.Context, value any, err error, status int) {
	if err == nil {
		c.JSON(status, internal.APIResponse[any]{Status: "success", Message: "jail_packages", Data: value})
		return
	}
	code, _, _ := strings.Cut(err.Error(), ":")
	code = strings.TrimSpace(code)
	status = http.StatusInternalServerError
	switch {
	case code == "package_management_unsupported_jail_type":
		status = http.StatusUnprocessableEntity
	case code == "replication_lease_not_owned":
		status = http.StatusForbidden
	case code == "package_service_unavailable" || strings.HasPrefix(code, "pkg_"):
		status = http.StatusServiceUnavailable
	case strings.HasPrefix(code, "invalid_") || code == "package_selection_required" || code == "too_many_packages" || code == "duplicate_package_selection":
		status = http.StatusBadRequest
	case errors.Is(err, gorm.ErrRecordNotFound) || strings.HasSuffix(code, "_not_found"):
		status = http.StatusNotFound
	case strings.HasPrefix(code, "package_") || strings.HasPrefix(code, "jail_") || strings.HasPrefix(code, "replication_") || strings.HasPrefix(code, "migration_") || strings.HasPrefix(code, "no_eligible_") || code == "lifecycle_task_in_progress" || code == "application_repositories_unavailable":
		status = http.StatusConflict
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: code, Error: pkgmanager.Redact(err.Error())})
}

func GetJailPackages(service jailPackageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		view, err := service.GetPackages(c.Request.Context(), ctID)
		packageResponse(c, view, err, http.StatusOK)
	}
}

func GetJailPackageDetails(service jailPackageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		value, err := service.GetPackageDetails(c.Request.Context(), ctID, c.Param("name"))
		packageResponse(c, value, err, http.StatusOK)
	}
}

func StartJailPackageOperation(service jailPackageService, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		var request jailTypes.PackageRequest
		if err = c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_package_request"})
			return
		}
		job, err := service.StartPackageOperation(c.Request.Context(), ctID, action, request, "user", c.GetString("Username"))
		if err == nil {
			c.Set("AuditAsyncJobID", job.TaskID)
			c.Set("AuditAsyncJobType", "jail_"+action)
			c.Header("Location", fmt.Sprintf("/api/jail/%d/packages/jobs/%d", ctID, job.TaskID))
		}
		packageResponse(c, job, err, http.StatusAccepted)
	}
}

func GetJailPackageJob(service jailPackageService, logs bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		taskID, err := utils.ParamUint(c, "taskId")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		if logs {
			offset, err := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
			if err != nil || offset < 0 {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_log_offset"})
				return
			}
			value, err := service.PackageLogs(c.Request.Context(), ctID, taskID, offset)
			packageResponse(c, value, err, http.StatusOK)
			return
		}
		value, err := service.GetPackageJob(c.Request.Context(), ctID, taskID)
		packageResponse(c, value, err, http.StatusOK)
	}
}

func JailPackageMonitoring(service jailPackageService, write bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "ctid")
		if err != nil {
			packageResponse(c, nil, err, 0)
			return
		}
		if write {
			var input struct {
				Enabled       bool   `json:"enabled"`
				IntervalHours int    `json:"intervalHours"`
				CronExpr      string `json:"cronExpr"`
			}
			if err = c.ShouldBindJSON(&input); err != nil {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_package_request"})
				return
			}
			value, err := service.SetPackageMonitoring(c.Request.Context(), ctID, jailTypes.PackageMonitoring{Enabled: input.Enabled, IntervalHours: input.IntervalHours, CronExpr: input.CronExpr})
			packageResponse(c, value, err, http.StatusOK)
			return
		}
		value, err := service.GetPackageMonitoring(c.Request.Context(), ctID)
		packageResponse(c, value, err, http.StatusOK)
	}
}
