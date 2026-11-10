// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"fmt"
	"slices"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/auth"
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
func Nodes(cS *cluster.Service, authService *auth.Service) gin.HandlerFunc {
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

		can, ok := middleware.Checker(c, authService)
		if !ok {
			return
		}
		for i := range nodes {
			if node := &nodes[i]; !can("/nodes/"+node.NodeUUID, auth.SysAudit) {
				node.CPU, node.Memory, node.Disk = 0, 0, 0
				node.CPUUsage, node.MemoryUsage, node.DiskUsage = 0, 0, 0
				node.GuestIDs = nil
			}
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
func Resources(cS *cluster.Service, authService *auth.Service) gin.HandlerFunc {
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

		can, ok := middleware.Checker(c, authService)
		if !ok {
			return
		}
		hidden := func(kind string, id uint) bool { return !can(fmt.Sprintf("/%s/%d", kind, id), auth.GuestAudit) }
		for i := range res {
			node := &res[i]
			node.VMs = slices.DeleteFunc(node.VMs, func(vm libvirtServiceInterfaces.SimpleList) bool { return hidden("vm", vm.RID) })
			node.Jails = slices.DeleteFunc(node.Jails, func(jail jailServiceInterfaces.SimpleList) bool { return hidden("jail", jail.CTID) })
			node.Containers = slices.DeleteFunc(node.Containers, func(item containerServiceInterfaces.SimpleList) bool {
				return hidden("container", item.OCID)
			})
			// Templates have no path of their own yet.
			if !can("/vm", auth.GuestAudit) {
				node.VMTemplates = node.VMTemplates[:0]
			}
			if !can("/jail", auth.GuestAudit) {
				node.JailTemplates = node.JailTemplates[:0]
			}
		}

		c.JSON(200, internal.APIResponse[[]clusterServiceInterfaces.NodeResources]{
			Status:  "success",
			Message: "resources_listed",
			Error:   "",
			Data:    res,
		})
	}
}

// @Summary Next free guest ID
// @Description One past the highest VM, jail or container ID claimed in the cluster or used on this node, from 100 up
// @Tags Cluster
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[uint] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/guest-ids/next [get]
func NextGuestID(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		next, err := cS.NextGuestID(c.Request.Context())
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Status: "error", Message: "next_guest_id_failed", Error: err.Error()})
			return
		}
		c.JSON(200, internal.APIResponse[uint]{Status: "success", Message: "next_guest_id", Data: next})
	}
}
