// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// @Summary Get All Cluster Nodes
// @Description Get all nodes in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ClusterNode] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/nodes [get]
func Nodes(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		nodes, err := cS.Nodes()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_nodes_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]clusterModels.ClusterNode]{
			Status:  "success",
			Message: "nodes_listed",
			Error:   "",
			Data:    nodes,
		})
	}
}

// @Summary Get Cluster Resources (per node)
// @Description Fetch jails & VMs for each cluster node (live)
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterServiceInterfaces.NodeResources] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/resources [get]
func Resources(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, err := cS.ResourcesContext(c.Request.Context())
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_resources_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]clusterServiceInterfaces.NodeResources]{
			Status:  "success",
			Message: "resources_listed",
			Error:   "",
			Data:    res,
		})
	}
}
