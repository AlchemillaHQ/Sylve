// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
)

type jailGuestInfoService interface {
	GetJailGuestInfo(context.Context, uint) (jailServiceInterfaces.GuestInfo, error)
}

// @Summary Get jail guest information
// @Description Best-effort OS and live network information for running FreeBSD jails. Available is false when inspection is unsupported or tools are unavailable.
// @Tags Jail
// @Produce json
// @Param ctid path int true "Jail CTID" minimum(1)
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[jailServiceInterfaces.GuestInfo] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/guest-info [get]
func GetJailGuestInfo(service jailGuestInfoService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}
		info, err := service.GetJailGuestInfo(c.Request.Context(), ctID)
		if err != nil {
			if isJailNotFoundError(err) {
				writeJailNotFound(c)
			} else {
				writeJailReadError(c, http.StatusInternalServerError, "failed_to_get_jail_guest_info", err)
			}
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[jailServiceInterfaces.GuestInfo]{
			Status: "success", Message: "jail_guest_info_retrieved", Data: info, Error: "",
		})
	}
}
