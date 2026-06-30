// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"

	"github.com/gin-gonic/gin"
)

type MdnsSettingsRequest struct {
	Interfaces string `json:"interfaces"`
	Hostname   string `json:"hostname"`
}

// @Summary Get mDNS Settings
// @Description Retrieve mDNS service discovery settings
// @Tags mDNS
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[mdnsModels.MdnsSettings] "mDNS settings"
// @Failure 500 {string} string "Internal server error"
// @Router /mdns/config [get]
func GetSettings(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := mdnsService.GetSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_mdns_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_settings_retrieved",
			Error:   "",
			Data:    settings,
		})
	}
}

// @Summary Set mDNS Settings
// @Description Update mDNS service discovery settings
// @Tags mDNS
// @Accept json
// @Produce json
// @Param request body MdnsSettingsRequest true "mDNS Settings"
// @Success 200 {string} string "mDNS settings updated"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /mdns/config [post]
func SetSettings(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MdnsSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := mdnsService.SetSettings(req.Interfaces, req.Hostname); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_set_mdns_settings",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_settings_updated",
			Error:   "",
			Data:    nil,
		})
	}
}
