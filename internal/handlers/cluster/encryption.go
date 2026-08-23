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
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type discoverEncryptionKeyRequest struct {
	UUID      string `json:"uuid" binding:"required"`
	KeyData   string `json:"keyData" binding:"required"`
	KeyFormat string `json:"keyFormat"`
}

func DiscoverEncryptionKeyInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req discoverEncryptionKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		uuid := strings.TrimSpace(req.UUID)
		if uuid == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "encryption_key_uuid_required",
				Data:    nil,
			})
			return
		}

		keyFormat := strings.TrimSpace(req.KeyFormat)
		if keyFormat == "" {
			keyFormat = "passphrase"
		}

		if err := cS.ProposeEncryptionKeyUpsert(uuid, req.KeyData, keyFormat, cS.Raft == nil); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "upsert_encryption_key_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "encryption_key_discovered",
			Data:    nil,
		})
	}
}
