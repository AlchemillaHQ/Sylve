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
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type NoteRequest struct {
	Title   string `json:"title" binding:"required,min=3"`
	Content string `json:"content" binding:"required,min=3"`
}

// @Summary Get All Cluster Notes
// @Description Get all notes stored in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ClusterNote] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes [get]
func Notes(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		notes, err := cS.ListNotes()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_notes_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]clusterModels.ClusterNote]{
			Status:  "success",
			Message: "notes_listed",
			Error:   "",
			Data:    notes,
		})
	}
}

// @Summary Create a New Cluster Note
// @Description Create a new note in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body NoteRequest true "Create Note Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes [post]
func CreateNote(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req NoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err := cS.ProposeNoteCreate(req.Title, req.Content, cS.Raft == nil)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "note_create_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "note_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update a Cluster Note
// @Description Update an existing note in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Note ID"
// @Param request body NoteRequest true "Update Note Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/{id} [put]
func UpdateNote(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

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

		var req NoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		err = cS.ProposeNoteUpdate(id, req.Title, req.Content, cS.Raft == nil)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "note_update_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "note_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete a Cluster Note
// @Description Delete a note from the cluster by ID
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Note ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/{id} [delete]
func DeleteNote(cS *cluster.Service) gin.HandlerFunc {
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
			err := cS.ProposeNoteDelete(id, true)
			if err != nil {
				c.JSON(500, internal.APIResponse[any]{
					Status:  "error",
					Message: "note_delete_failed",
					Error:   err.Error(),
					Data:    nil,
				})
			}

			c.JSON(200, internal.APIResponse[any]{
				Status:  "success",
				Message: "note_deleted",
				Error:   "",
				Data:    nil,
			})
			return
		}

		if cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if err := cS.ProposeNoteDelete(id, false); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "note_delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "note_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
