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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/alchemillahq/sylve/internal/services/samba"

	"github.com/gin-gonic/gin"
)

type SambaConfigRequest struct {
	UnixCharset        string  `json:"unixCharset"`
	Workgroup          string  `json:"workgroup"`
	ServerString       string  `json:"serverString"`
	Interfaces         string  `json:"interfaces"`
	BindInterfacesOnly bool    `json:"bindInterfacesOnly"`
	AppleExtensions    bool    `json:"appleExtensions"`
	AdvertiseMdns      bool    `json:"advertiseMdns"`
	ExtraGlobalConfig  *string `json:"extraGlobalConfig"`
}

func sambaConfigServiceErrorStatus(err error) int {
	if errors.Is(err, samba.ErrInvalidGlobalConfig) {
		return http.StatusBadRequest
	}
	if errors.Is(err, samba.ErrSambaNotInitialized) {
		return http.StatusNotFound
	}
	if errors.Is(err, directoryservices.ErrBusy) || errors.Is(err, directoryservices.ErrConflict) {
		return http.StatusConflict
	}

	return http.StatusInternalServerError
}

func sambaConfigErrorMessage(err error, fallback string) string {
	for _, known := range []error{samba.ErrSambaNotInitialized, samba.ErrSambaApplyFailed} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return fallback
}

// @Summary Get Samba global configuration
// @Description Retrieve the Samba global configuration settings. Returns 404 with message samba_not_initialized when the Samba service has never been enabled on this node.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[sambaModels.SambaSettings] "Success"
// @Failure 404 {object} internal.APIResponse[any] "Samba not initialized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /samba/config [get]
func GetGlobalConfig(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := smbService.GetGlobalConfig()
		if err != nil {
			c.JSON(sambaConfigServiceErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: sambaConfigErrorMessage(err, "failed_to_get_samba_config"),
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
// @Description Validate and save the Samba global configuration, then rewrite smb4.conf and restart Samba, which disconnects connected clients. Samba is only restarted when the Samba service is enabled in Sylve. The complete configuration is checked with testparm before anything is saved, and a rejection returns 400; unknown interface names and a server string ending in a backslash are also rejected. Omitted booleans are saved as false. If the change was saved but Samba could not apply it, the response is 500 with message samba_apply_failed.
// @Tags Samba
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SambaConfigRequest true "Samba global configuration"
// @Success 200 {object} internal.APIResponse[sambaModels.SambaSettings] "Saved settings"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Directory operation in progress"
// @Failure 404 {object} internal.APIResponse[any] "Samba not initialized"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error, or saved but not applied (samba_apply_failed)"
// @Router /samba/config [put]
func SetGlobalConfig(smbService *samba.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SambaConfigRequest
		if err := strictJSONBind(c, &req); err != nil {
			sambaRequestBindError(c, err)
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
			req.AdvertiseMdns,
			req.ExtraGlobalConfig)

		if err != nil {
			c.JSON(sambaConfigServiceErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: sambaConfigErrorMessage(err, "failed_to_set_samba_config"),
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		response := internal.APIResponse[*sambaModels.SambaSettings]{
			Status:  "success",
			Message: "samba_global_config_updated",
		}
		if settings, err := smbService.GetGlobalConfig(); err != nil {
			logger.L.Warn().Err(err).Msg("could not read back the saved Samba settings")
		} else {
			response.Data = &settings
		}
		c.JSON(http.StatusOK, response)
	}
}
