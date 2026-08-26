// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func writeNoteMutationError(c *gin.Context, operation string, err error) {
	status := http.StatusInternalServerError
	message := operation
	detail := operation

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
		message = "note_not_found"
		detail = "note_not_found"
	case errors.Is(err, raft.ErrNotLeader), errors.Is(err, raft.ErrLeadershipLost):
		status = http.StatusConflict
		message = "cluster_leadership_changed"
		detail = "cluster_leadership_changed"
	case errors.Is(err, raft.ErrRaftShutdown), errors.Is(err, raft.ErrEnqueueTimeout):
		status = http.StatusServiceUnavailable
		message = "cluster_consensus_unavailable"
		detail = "cluster_consensus_unavailable"
	default:
		logger.L.Error().Err(err).Str("operation", operation).Msg("cluster_note_mutation_failed")
	}

	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   detail,
		Data:    nil,
	})
}

func validNoteBulkIDs(ids []int) bool {
	if len(ids) == 0 {
		return false
	}

	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

// @Summary Get All Cluster Notes
// @Description Get all notes stored in the cluster
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]clusterModels.ClusterNote] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /cluster/notes [get]
func Notes(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		notes, err := cS.ListNotes()
		if err != nil {
			logger.L.Error().Err(err).Msg("cluster_note_list_failed")
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_notes_failed",
				Error:   "list_notes_failed",
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
// @Description Create a new note with a 3-128 character title and content of at least 3 characters
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body internal.NoteRequest true "Create Note Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Cluster leadership changed"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/notes [post]
func CreateNote(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req internal.NoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		err := cS.ProposeNoteCreate(req.Title, req.Content, cS.Raft == nil)
		if err != nil {
			writeNoteMutationError(c, "note_create_failed", err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "note_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update a Cluster Note
// @Description Update an existing note by positive ID
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Positive note ID" minimum(1)
// @Param request body internal.NoteRequest true "Update Note Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Note Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Cluster leadership changed"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/notes/{id} [put]
func UpdateNote(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		id, err := utils.GetIdFromParam(c)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "id must be a positive integer",
				Data:    nil,
			})
			return
		}

		var req internal.NoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}

		err = cS.ProposeNoteUpdate(id, req.Title, req.Content, cS.Raft == nil)
		if err != nil {
			writeNoteMutationError(c, "note_update_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
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
// @Param id path int true "Positive note ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Note Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Cluster leadership changed"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/notes/{id} [delete]
func DeleteNote(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.GetIdFromParam(c)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_id",
				Error:   "id must be a positive integer",
				Data:    nil,
			})
			return
		}

		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		if err := cS.ProposeNoteDelete(id, cS.Raft == nil); err != nil {
			writeNoteMutationError(c, "note_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "note_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Bulk Delete Cluster Notes
// @Description Delete the exact requested non-empty set of unique positive note IDs
// @Tags Cluster
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body internal.BulkDeleteRequest true "Bulk Delete Notes Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "One or more notes not found"
// @Failure 409 {object} internal.APIResponse[any] "Cluster leadership changed"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Cluster consensus unavailable"
// @Router /cluster/notes/bulk-delete [post]
func BulkDeleteNotes(cS *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cS.Raft != nil && cS.Raft.State() != raft.Leader {
			forwardToLeader(c, cS)
			return
		}

		var req internal.BulkDeleteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeClusterJSONBindError(c, err, "invalid_request")
			return
		}
		if !validNoteBulkIDs(req.IDs) {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_ids",
				Error:   "ids must be a non-empty list of unique positive integers",
				Data:    nil,
			})
			return
		}

		err := cS.ProposeNoteBulkDelete(req.IDs, cS.Raft == nil)
		if err != nil {
			writeNoteMutationError(c, "bulk_note_delete_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "notes_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
