// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/network/reconcile"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type switchSyncService interface {
	SyncSwitchNetwork(context.Context) error
	SwitchSyncStatus() reconcile.Status
}

type bootstrapSwitchStatus interface {
	SwitchStatus() reconcile.Status
}

func switchSyncStatus(service switchSyncService, bootstrap bootstrapSwitchStatus) reconcile.SwitchStatus {
	status := reconcile.SwitchStatus{
		Runtime: service.SwitchSyncStatus(), Bootstrap: reconcile.Status{}.Clone(),
	}
	if bootstrap != nil {
		status.Bootstrap = bootstrap.SwitchStatus()
	}
	return status
}

// @Summary Get switch reconciliation status
// @Description Get the last runtime and bootstrap switch results, including exhausted failures. This is not continuous live-state health.
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[reconcile.SwitchStatus]
// @Failure 401 {object} internal.APIResponse[any]
// @Router /network/switch/sync [get]
func GetSwitchSyncStatus(service switchSyncService, bootstrap bootstrapSwitchStatus) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, internal.APIResponse[reconcile.SwitchStatus]{
			Status: "success", Message: "switch_sync_status", Data: switchSyncStatus(service, bootstrap),
		})
	}
}

// @Summary Re-apply stored switch network configuration
// @Description Reconcile standard switches and Host IP configuration with bounded retries. Does not replay bootstrap settings or roll back pending Host IP edits.
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[reconcile.SwitchStatus]
// @Failure 401 {object} internal.APIResponse[any]
// @Failure 403 {object} internal.APIResponse[any]
// @Failure 409 {object} internal.APIResponse[reconcile.SwitchStatus]
// @Failure 500 {object} internal.APIResponse[reconcile.SwitchStatus]
// @Router /network/switch/sync [post]
func SyncSwitches(service switchSyncService, bootstrap bootstrapSwitchStatus) gin.HandlerFunc {
	return func(c *gin.Context) {
		err := service.SyncSwitchNetwork(c.Request.Context())
		response := internal.APIResponse[reconcile.SwitchStatus]{
			Status: "success", Message: "switches_synced", Data: switchSyncStatus(service, bootstrap),
		}
		status := http.StatusOK
		if err != nil {
			status = http.StatusInternalServerError
			response.Status = "error"
			response.Message = "failed_to_sync_switches"
			response.Error = "switch_sync_failed"
			if errors.Is(err, network.ErrSwitchSyncInProgress) {
				status = http.StatusConflict
				response.Error = "switch_sync_in_progress"
			}
		}
		c.JSON(status, response)
	}
}
