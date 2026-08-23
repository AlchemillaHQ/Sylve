// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// GuestIdentityInventoryInternal returns this node's durable VM and jail
// registrations. Routing places it behind the internal-cluster JWT middleware.
func GuestIdentityInventoryInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		snapshot, err := cS.LocalGuestIdentityInventory(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "guest_identity_inventory_scan_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[cluster.GuestIdentityInventorySnapshot]{
			Status:  "success",
			Message: "guest_identity_inventory_listed",
			Error:   "",
			Data:    snapshot,
		})
	}
}
