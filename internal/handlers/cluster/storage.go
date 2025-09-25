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
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// @Summary Get Cluster Storages
// @Description Get all storage backends configured in the cluster (S3, etc.)
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[clusterServiceInterfaces.Storages] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/storage [get]
func Storages(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		storages, err := cS.ListStorages()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_storages_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[clusterServiceInterfaces.Storages]{
			Status:  "success",
			Message: "storages_listed",
			Error:   "",
			Data:    storages,
		})
	}
}
