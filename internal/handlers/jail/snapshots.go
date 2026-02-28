// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type CreateJailSnapshotRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

func ListJailSnapshots(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshots, err := jailService.ListJailSnapshots(ctID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_jail_snapshots",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]jailModels.JailSnapshot]{
			Status:  "success",
			Message: "jail_snapshots_listed",
			Error:   "",
			Data:    snapshots,
		})
	}
}

func CreateJailSnapshot(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req CreateJailSnapshotRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		created, err := jailService.CreateJailSnapshot(
			c.Request.Context(),
			ctID,
			req.Name,
			req.Description,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_jail_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[jailModels.JailSnapshot]{
			Status:  "success",
			Message: "jail_snapshot_created",
			Error:   "",
			Data:    *created,
		})
	}
}

func RollbackJailSnapshot(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshotID, err := utils.ParamUint(c, "snapshotId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := jailService.RollbackJailSnapshot(c.Request.Context(), ctID, snapshotID, true); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_rollback_jail_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_snapshot_rolled_back",
			Error:   "",
			Data:    nil,
		})
	}
}

func DeleteJailSnapshot(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, err := utils.ParamUint(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		snapshotID, err := utils.ParamUint(c, "snapshotId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := jailService.DeleteJailSnapshot(c.Request.Context(), ctID, snapshotID); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_jail_snapshot",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "jail_snapshot_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
