// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type bulkDeleteNotesQuery struct {
	IDs []int `form:"ids" binding:"required,min=1,dive,gt=0"`
}

func invalidNoteIDError(err error) string {
	if err != nil {
		return err.Error()
	}

	return "note ID must be a positive integer"
}

func NotesHandler(infoService *info.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet:
			handleGetNotes(c, infoService)
		case http.MethodPost:
			handlePostNotes(c, infoService)
		case http.MethodDelete:
			if c.Param("id") == "" {
				handleBulkDeleteNotes(c, infoService)
				return
			}
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
// @Param request body internal.NoteRequest true "Note"
// @Success 201 {object} internal.APIResponse[infoModels.Note] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
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
// @Produce json
// @Security BearerAuth
// @Param id path int true "Note ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note ID"
// @Failure 404 {object} internal.APIResponse[any] "Note not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/{id} [delete]
func handleDeleteNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := utils.GetIdFromParam(c)

	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   invalidNoteIDError(err),
			Data:    nil,
		})
		return
	}

	err = infoService.DeleteNoteByID(id)
	if err != nil {
		if errors.Is(err, info.ErrNoteNotFound) {
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
// @Param id path int true "Note ID" minimum(1)
// @Param request body internal.NoteRequest true "Note"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note ID"
// @Failure 404 {object} internal.APIResponse[any] "Note not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes/{id} [put]
func handleUpdateNoteByID(c *gin.Context, infoService *info.Service) {
	id, err := utils.GetIdFromParam(c)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_note_id",
			Error:   invalidNoteIDError(err),
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

	err = infoService.UpdateNoteByID(id, req.Title, req.Content)
	if err != nil {
		if errors.Is(err, info.ErrNoteNotFound) {
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
// @Produce json
// @Security BearerAuth
// @Param ids query []int true "Note IDs" collectionFormat(multi)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Invalid note IDs"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /info/notes [delete]
func handleBulkDeleteNotes(c *gin.Context, infoService *info.Service) {
	var query bulkDeleteNotesQuery

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_note_ids",
			Error:   "validation_error",
			Data:    nil,
		})
		return
	}

	err := infoService.BulkDeleteNotes(query.IDs)
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
