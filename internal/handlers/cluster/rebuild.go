// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// @Summary Rebuild the cluster from an older Raft format
// @Description Answers another member upgrading from an older Raft format: how up to date this node is, its vote, and, from the founder, a join key. The message and the answer carry a MAC, with the old cluster key, of the TLS session.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param request body cluster.RebuildMessage true "The member's message"
// @Success 200 {object} internal.APIResponse[cluster.RebuildReply] "Answer"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "No valid MAC"
// @Failure 403 {object} internal.APIResponse[any] "Not a member of the old cluster"
// @Failure 404 {object} internal.APIResponse[any] "No rebuild running"
// @Router /cluster/rebuild [post]
func Rebuild(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var message cluster.RebuildMessage
		if err := c.ShouldBindJSON(&message); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		if c.Request.TLS == nil {
			abortForward(c, http.StatusBadRequest, "rebuild_requires_tls", nil)
			return
		}
		exporter, err := c.Request.TLS.ExportKeyingMaterial(cluster.RebuildExporterLabel, nil, 32)
		if err != nil {
			abortForward(c, http.StatusBadRequest, "rebuild_requires_tls", nil)
			return
		}
		reply, proof, err := cS.AnswerRebuild(c.Request.Context(), exporter, c.GetHeader(cluster.RebuildProofHeader), message)
		if proof != "" {
			c.Header(cluster.RebuildProofHeader, proof)
		}
		switch {
		case errors.Is(err, cluster.ErrRebuildNotRunning):
			abortForward(c, http.StatusNotFound, err.Error(), nil)
		case errors.Is(err, cluster.ErrRebuildProofInvalid):
			abortForward(c, http.StatusUnauthorized, err.Error(), nil)
		case errors.Is(err, cluster.ErrRebuildNodeUnknown):
			abortForward(c, http.StatusForbidden, err.Error(), nil)
		case err != nil:
			abortForward(c, http.StatusServiceUnavailable, "rebuild_answer_failed", nil)
		default:
			c.JSON(http.StatusOK, internal.APIResponse[cluster.RebuildReply]{
				Status: "success", Message: "rebuild_answered", Data: reply,
			})
		}
	}
}
