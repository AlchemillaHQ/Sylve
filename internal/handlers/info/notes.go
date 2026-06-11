// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

func NotesHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet:
			handleGetNotes(c, infoService)
		case http.MethodPost:
			if strings.HasSuffix(c.Request.URL.Path, "bulk-delete") {
				handleBulkDeleteNotes(c, infoService)
				return
			} else {
				handlePostNotes(c, infoService)
			}
		case http.MethodDelete:
			handleDeleteNoteByID(c, infoService)
		case http.MethodPut:
			handleUpdateNoteByID(c, infoService)
		default:
			c.JSON(http.StatusMethodNotAllowed, internal.APIResponse[any]{
				Status:  "error",
				Message: "method_not_allowed",
				Error:   "",
				Data:    nil,
			})
		}
	}
}

// @Summary Get All Notes
// @Description Get all notes stored in the database
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]infoModels.Note] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes [get]
func handleGetNotes(c *gin.Context, infoService *info.Service) {
	notes, err := infoService.GetNotes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "notes_fetch_failed",
			Error:   err.Error(),
			Data:    nil,
		})
		return
	}

	c.JSON(http.StatusOK, internal.APIResponse[[]infoModels.Note]{
		Status:  "success",
		Message: "notes_fetched",
		Error:   "",
		Data:    notes,
	})
}

// @Summary Create a new note
// @Description Add a new note to the database
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[infoModels.Note] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes [post]
func handlePostNotes(c *gin.Context, infoService *info.Service) {
	var req internal.NoteRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		validationErrors := utils.MapValidationErrors(err, internal.NoteRequest{})

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   "validation_error",
			Data:    validationErrors,
		})
		return
	}

	note, err := infoService.AddNote(req.Title, req.Content)

	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "note_creation_failed",
			Error:   err.Error(),
			Data:    nil,
		})

		return
	}

	c.JSON(http.StatusCreated, internal.APIResponse[infoModels.Note]{
		Status:  "success",
		Message: "note_created",
		Error:   "",
		Data:    note,
	})
}

// @Summary Delete a note by ID
// @Description Delete a note from the database by its ID
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note ID"
// @Failure 404 {object} internal.APIResponse[any] "Note not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/:id [delete]
func handleDeleteNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := utils.GetIdFromParam(c)

	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   err.Error(),
			Data:    nil,
		})
		return
	}

	_, err = infoService.GetNoteByID(id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "note_fetch_failed",
			Error:   err.Error(),
			Data:    nil,
		})

		return
	}

	err = infoService.DeleteNoteByID(id)
	if err != nil {
		if err.Error() == "record not found" {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "note_not_found",
				Error:   "",
				Data:    nil,
			})

			return
		}

		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "note_delete_failed",
			Error:   err.Error(),
			Data:    nil,
		})

		return
	}

	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: "note_deleted",
		Error:   "",
		Data:    nil,
	})
}

// @Summary Update a note by ID
// @Description Update a note in the database by its ID
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note ID"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/:id [put]
func handleUpdateNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := utils.GetIdFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   err.Error(),
			Data:    nil,
		})
		return
	}

	var req internal.NoteRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		var validationErrors []string

		if ve, ok := err.(validator.ValidationErrors); ok {
			for _, fieldErr := range ve {
				validationErrors = append(validationErrors, fieldErr.Field()+" failed validation: "+fieldErr.Tag())
			}
		} else {
			validationErrors = append(validationErrors, err.Error())
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   "validation_error",
			Data:    validationErrors,
		})
		return
	}

	_, err = infoService.GetNoteByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "note_fetch_failed",
			Error:   err.Error(),
			Data:    nil,
		})

		return
	}

	err = infoService.UpdateNoteByID(id, req.Title, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "note_update_failed",
			Error:   err.Error(),
			Data:    nil,
		})
		return
	}

	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: "note_updated",
		Error:   "",
		Data:    nil,
	})
}

// @Summary Bulk delete notes
// @Description Delete multiple notes from the database by their IDs
// @Tags Info
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note IDs"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/bulk-delete [post]
func handleBulkDeleteNotes(c *gin.Context, infoService *info.Service) {
	var req internal.BulkDeleteRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request_payload",
			Error:   "validation_error",
			Data:    nil,
		})
		return
	}

	err := infoService.BulkDeleteNotes(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
			Status:  "error",
			Message: "bulk_delete_failed",
			Error:   err.Error(),
			Data:    nil,
		})
		return
	}

	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: "notes_bulk_deleted",
		Error:   "",
		Data:    nil,
	})
}
