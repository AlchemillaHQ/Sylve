// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"net/http"

	"sylve/internal"
	"sylve/internal/utils"

	"github.com/gin-gonic/gin"
)

type HealthResponse struct {
	Hostname string `json:"hostname"`
	Message  string `json:"message"`
}

// @Summary Get basic health status
// @Description Get basic health status
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} HealthResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /health/basic [get]
func BasicHealthCheckHandler(c *gin.Context) {
	h, err := utils.GetSystemHostname()
	if err != nil {
		utils.SendJSONResponse(c, http.StatusInternalServerError, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_get_hostname",
			Error:   err.Error(),
		})
		return
	}

	utils.SendJSONResponse(c, http.StatusOK, HealthResponse{Hostname: h, Message: "ok"})
}

// @Summary Get HTTP health status
// @Description Get HTTP health status
// @Tags Health
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} HealthResponse
// @Failure 500 {object} internal.ErrorResponse
// @Router /health/http [get]
func HTTPHealthCheckHandler(c *gin.Context) {
	h, err := utils.GetSystemHostname()
	if err != nil {
		utils.SendJSONResponse(c, http.StatusInternalServerError, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_get_hostname",
			Error:   err.Error(),
		})
		return
	}

	utils.SendJSONResponse(c, http.StatusOK, gin.H{"status": "ok", "hostname": h})
}
