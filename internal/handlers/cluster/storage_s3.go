package clusterHandlers

import (
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type CreateS3StorageRequest struct {
	Name      string `json:"name" binding:"required,min=3"`
	Endpoint  string `json:"endpoint" binding:"required"`
	Region    string `json:"region" binding:"required"`
	Bucket    string `json:"bucket" binding:"required"`
	AccessKey string `json:"accessKey" binding:"required"`
	SecretKey string `json:"secretKey" binding:"required"`
}

// @Summary Create an S3 Storage
// @Description Create a new S3 storage configuration in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateS3StorageRequest true "Create S3 Storage Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/storage/s3 [post]
func CreateS3Storage(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft == nil {
			var req CreateS3StorageRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_request",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			if err := cS.ProposeS3Config(
				req.Name, req.Endpoint, req.Region, req.Bucket, req.AccessKey, req.SecretKey, true,
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

		var req CreateS3StorageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := cS.ProposeS3Config(
			req.Name, req.Endpoint, req.Region, req.Bucket, req.AccessKey, req.SecretKey,
			false,
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

// @Summary Delete an S3 Storage
// @Description Delete an S3 storage configuration from the cluster by ID
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Storage ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/storage/s3/{id} [delete]
func DeleteS3Storage(cS *cluster.Service) gin.HandlerFunc {
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
			if err := cS.ProposeS3ConfigDelete(uint(id), true); err != nil {
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

		if err := cS.ProposeS3ConfigDelete(uint(id), false); err != nil {
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
