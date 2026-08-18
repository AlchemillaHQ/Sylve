// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"errors"
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
	BindInterfacesOnly bool   `json:"bindInterfacesOnly"`
	AppleExtensions    bool   `json:"appleExtensions"`
	AdvertiseMdns      bool   `json:"advertiseMdns"`
}

func sambaConfigServiceErrorStatus(err error) int {
	if errors.Is(err, samba.ErrInvalidGlobalConfig) {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

// @Summary Get Samba global configuration
// @Description Retrieve the Samba global configuration settings
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[sambaModels.SambaSettings] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
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

// @Summary Update Samba global configuration
// @Description Update the Samba global configuration settings
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SambaConfigRequest true "Samba global configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/config [put]
func SetGlobalConfig(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SambaConfigRequest
		if err := strictJSONBind(c, &req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err := smbService.SetGlobalConfig(
			ctx,
			req.UnixCharset,
			req.Workgroup,
			req.ServerString,
			req.Interfaces,
			req.BindInterfacesOnly,
			req.AppleExtensions,
			req.AdvertiseMdns)

		if err != nil {
			c.JSON(sambaConfigServiceErrorStatus(err), internal.APIResponse[any]{
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
