// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sylve/internal"
	infoModels "sylve/internal/db/models/info"
	"sylve/internal/services/info"

	"github.com/gin-gonic/gin"
)

type GetNotesResponse struct {
	Status string            `json:"status"`
	Data   []infoModels.Note `json:"data"`
}

type PostNoteResponse struct {
	Status string `json:"status"`
}

type NoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func validateNoteRequest(note NoteRequest) error {
	return Validate.StructPartial(note, "Title", "Content")
}

func getNoteID(c *gin.Context) (int, error) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func NotesHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet:
			handleGetNotes(c, infoService)
		case http.MethodPost:
			handlePostNotes(c, infoService)
		case http.MethodDelete:
			handleDeleteNoteByID(c, infoService)
		case http.MethodPut:
			handleUpdateNoteByID(c, infoService)
		default:
			c.JSON(http.StatusMethodNotAllowed, internal.ErrorResponse{
				Status:  "error",
				Message: "method_not_allowed",
				Error:   "method_not_allowed",
			})
		}
	}
}

// @Summary Get Notes
// @Description Get all notes
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} GetNotesResponse
// @Failure 400 {object} internal.ErrorResponse
// @Router /info/notes [get]
func handleGetNotes(c *gin.Context, infoService *info.Service) {
	notes, err := infoService.GetNotes()
	if err != nil {
		c.JSON(http.StatusOK, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_get_notes",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, GetNotesResponse{
		Status: "success",
		Data:   notes,
	})
}

// @Summary Create Note
// @Description Add a new note
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.SuccessResponse
// @Failure 400 {object} internal.ErrorResponse
// @Router /info/notes [post]
func handlePostNotes(c *gin.Context, infoService *info.Service) {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var newNote NoteRequest
	if err := decoder.Decode(&newNote); err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   err.Error(),
		})
		return
	}

	if err := validateNoteRequest(newNote); err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   err.Error(),
		})
	}

	err := infoService.AddNote(newNote.Title, newNote.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_add_note",
			Error:   err.Error(),
		})
		return
	}
	c.JSON(http.StatusCreated, internal.SuccessResponse{
		Status:  "success",
		Message: "note_added",
	})
}

// @Summary Delete Note
// @Description Delete a note by ID
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Note ID"
// @Success 200 {object} internal.SuccessResponse
// @Failure 400 {object} internal.ErrorResponse
// @Router /info/notes/{id} [delete]
func handleDeleteNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := getNoteID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   err.Error(),
		})
		return
	}

	err = infoService.DeleteNoteByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_delete_note",
			Error:   err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, internal.SuccessResponse{
		Status:  "success",
		Message: "note_deleted",
	})
}

// @Summary Update Note
// @Description Update a note by ID
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Note ID"
// @Success 200 {object} internal.SuccessResponse
// @Failure 400 {object} internal.ErrorResponse
// @Router /info/notes/{id} [put]
func handleUpdateNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := getNoteID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   err.Error(),
		})
		return
	}

	var updateData NoteRequest
	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   err.Error(),
		})
		return
	}

	if err := validateNoteRequest(updateData); err != nil {
		c.JSON(http.StatusBadRequest, internal.ErrorResponse{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   err.Error(),
		})
		return
	}

	err = infoService.UpdateNoteByID(id, updateData.Title, updateData.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.ErrorResponse{
			Status:  "error",
			Message: "unable_to_update_note",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, internal.SuccessResponse{
		Status: "success",
	})
}
