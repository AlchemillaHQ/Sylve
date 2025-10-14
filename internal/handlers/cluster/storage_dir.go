// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type CreateDirStorageRequest struct {
	Name string `json:"name" binding:"required,min=3"`
	Path string `json:"path" binding:"required"`
}

// @Summary Create a Directory Storage
// @Description Create a new Directory storage configuration in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateDirStorageRequest true "Create Directory Storage Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/storage/directory [post]
func CreateDirStorage(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft == nil {
			var req CreateDirStorageRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_request",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			if err := cS.ProposeDirectoryConfig(
				req.Name, req.Path, true,
			); err != nil {
				c.JSON(500, internal.APIResponse[any]{
					Status:  "error",
					Message: "storage_create_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.JSON(200, internal.APIResponse[any]{
				Status:  "success",
				Message: "storage_created",
				Error:   "",
				Data:    nil,
			})
			return
		}

		if cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req CreateDirStorageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeDirectoryConfig(
			req.Name, req.Path, false,
		); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "storage_create_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a Directory Storage
// @Description Delete a Directory storage configuration from the cluster by ID
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Storage ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/storage/directory/{id} [delete]
func DeleteDirStorage(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "id must be a positive integer",
				Data:    nil,
			})
			return
		}

		if cS.Raft == nil {
			if err := cS.ProposeDirectoryConfigDelete(uint(id), true); err != nil {
				c.JSON(500, internal.APIResponse[any]{
					Status:  "error",
					Message: "storage_delete_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.JSON(200, internal.APIResponse[any]{
				Status:  "success",
				Message: "storage_deleted",
				Error:   "",
				Data:    nil,
			})
			return
		}

		if cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if err := cS.ProposeDirectoryConfigDelete(uint(id), false); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "storage_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "storage_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
