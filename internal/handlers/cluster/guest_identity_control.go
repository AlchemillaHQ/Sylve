// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

func writeGuestIdentityControlError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "guest_identity_control_failed"
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	switch {
	case errors.Is(err, clusterModels.ErrGuestIdentityAlreadyInUse),
		errors.Is(err, clusterModels.ErrGuestIdentityClaimConflict),
		errors.Is(err, clusterModels.ErrGuestIdentityInventoryConflict),
		errors.Is(err, clusterModels.ErrGuestIdentityStillRegistered),
		strings.Contains(errorText, "guest_operation_in_progress"):
		status = http.StatusConflict
		message = "guest_identity_conflict"
	case errors.Is(err, clusterModels.ErrGuestIdentityReclaimUnsafe) &&
		strings.Contains(errorText, "confirmation_must_equal_guest_id"):
		status = http.StatusBadRequest
		message = "guest_identity_reclaim_confirmation_required"
	case errors.Is(err, clusterModels.ErrGuestIdentityReclaimUnsafe):
		status = http.StatusServiceUnavailable
		message = "guest_identity_reclaim_unsafe"
	case errors.Is(err, clusterModels.ErrGuestIdentityRegistryInitializing):
		status = http.StatusServiceUnavailable
		message = clusterModels.ErrGuestIdentityRegistryInitializing.Error()
	case errors.Is(err, raft.ErrNotLeader), errors.Is(err, raft.ErrLeadershipLost):
		status = http.StatusConflict
		message = "cluster_leadership_changed"
	case errors.Is(err, raft.ErrRaftShutdown), errors.Is(err, raft.ErrEnqueueTimeout),
		strings.Contains(errorText, "cluster_consensus_unavailable"):
		status = http.StatusServiceUnavailable
		message = "cluster_consensus_unavailable"
	case strings.Contains(errorText, "owner_not_issuer"), strings.Contains(errorText, "issuer_not_voter"):
		status = http.StatusForbidden
		message = "guest_identity_control_forbidden"
	case strings.Contains(errorText, "invalid"), strings.Contains(errorText, "required"):
		status = http.StatusBadRequest
		message = "guest_identity_control_invalid"
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   errorText,
		Data:    nil,
	})
}

func GuestIdentityControlInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request cluster.GuestIdentityControlRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}
		response, err := cS.HandleGuestIdentityControl(
			c.Request.Context(),
			c.GetString("IssuerNodeID"),
			request,
		)
		if err != nil {
			writeGuestIdentityControlError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.GuestIdentityControlResponse]{
			Status:  "success",
			Message: "guest_identity_control_applied",
			Error:   "",
			Data:    response,
		})
	}
}
