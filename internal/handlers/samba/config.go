// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

type SambaConfigRequest struct {
	UnixCharset        string `json:"unixCharset"`
	Workgroup          string `json:"workgroup"`
	ServerString       string `json:"serverString"`
	Interfaces         string `json:"interfaces"`
	BindInterfacesOnly *bool  `json:"bindInterfacesOnly"`
}

// @Summary Get Samba Global Configuration
// @Description Retrieve Samba global configuration settings
// @Tags Samba
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[[]sambaModels.SambaSettings] "Samba global configuration"
// @Failure 500 {string} string "Internal server error"
// @Router /samba/config [get]
func GetGlobalConfig(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := smbService.GetGlobalConfig()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_samba_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[sambaModels.SambaSettings]{
			Status:  "success",
			Message: "samba_global_config_retrieved",
			Error:   "",
			Data:    settings,
		})
	}
}

// @Summary Set Samba Global Configuration
// @Description Set Samba global configuration settings
// @Tags Samba
// @Accept json
// @Produce json
// @Param request body SambaConfigRequest true "Samba Global Configuration"
// @Success 200 {string} string "Samba global configuration updated successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /samba/config [post]
func SetGlobalConfig(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SambaConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		bindInterfaces := false
		if req.BindInterfacesOnly != nil {
			bindInterfaces = *req.BindInterfacesOnly
		}

		ctx := c.Request.Context()
		err := smbService.SetGlobalConfig(
			ctx,
			req.UnixCharset,
			req.Workgroup,
			req.ServerString,
			req.Interfaces,
			bindInterfaces)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_set_samba_config",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "samba_global_config_updated",
			Error:   "",
			Data:    nil,
		})
	}
}
