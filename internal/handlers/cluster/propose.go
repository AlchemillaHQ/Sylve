// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"io"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func ApplyProposalInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var cmd clusterModels.Command
		if err := c.ShouldBindJSON(&cmd); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		index, err := cS.ApplyPeerProposal(c.Request.Context(), c.GetString("IssuerNodeID"), cmd)
		if err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: cluster.ProposalErrorCode(err), Error: err.Error(), Data: nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[cluster.ProposalResult]{
			Status: "success", Message: "raft_proposal_committed", Error: "", Data: cluster.ProposalResult{Index: index},
		})
	}
}

func LeaderCallInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		result, err := cS.ServeLeaderCall(c.Request.Context(), c.GetString("IssuerNodeID"), c.Param("name"), payload)
		writeCallResult(c, result, err)
	}
}

func NodeCallInternal(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeClusterJSONBindError(c, err, "invalid_request_payload")
			return
		}
		index, _ := strconv.ParseUint(c.GetHeader(cluster.CallIndexHeader), 10, 64)
		result, err := cS.ServeNodeCall(
			c.Request.Context(), c.GetString("IssuerNodeID"), c.GetHeader(cluster.CallNodeHeader), index, c.Param("name"), payload,
		)
		writeCallResult(c, result, err)
	}
}

func writeCallResult(c *gin.Context, result cluster.CallResult, err error) {
	if err != nil {
		c.JSON(http.StatusConflict, internal.APIResponse[cluster.CallResult]{
			Status: "error", Message: cluster.ProposalErrorCode(err), Error: err.Error(), Data: result,
		})
		return
	}
	c.JSON(http.StatusOK, internal.APIResponse[cluster.CallResult]{Status: "success", Message: "call_completed", Data: result})
}
