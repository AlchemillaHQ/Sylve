// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicDNSHandlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
	"github.com/gin-gonic/gin"
)

type dynamicDNSService interface {
	ListEntries(context.Context) ([]dynamicdns.EntryView, error)
	CreateEntry(context.Context, dynamicdns.EntryInput) (*dynamicdns.EntryView, error)
	UpdateEntry(context.Context, uint, dynamicdns.EntryInput) (*dynamicdns.EntryView, error)
	DeleteEntry(context.Context, uint) error
	SyncEntry(context.Context, uint) (*dynamicdns.EntryView, error)
}

func dynamicDNSErrorStatus(err error) int {
	switch {
	case errors.Is(err, dynamicdns.ErrInvalidEntry):
		return http.StatusBadRequest
	case errors.Is(err, dynamicdns.ErrEntryNotFound):
		return http.StatusNotFound
	case errors.Is(err, dynamicdns.ErrEntryInUse):
		return http.StatusConflict
	case errors.Is(err, dynamicdns.ErrEntryConflict):
		return http.StatusConflict
	case errors.Is(err, dynamicdns.ErrProviderUnavailable):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func bindDynamicDNSEntryInput(c *gin.Context, input *dynamicdns.EntryInput) bool {
	if err := c.ShouldBindJSON(input); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "dynamic_dns_request_too_large",
				Error:   "dynamic DNS request body is too large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_dynamic_dns_entry",
			Error:   err.Error(),
			Data:    nil,
		})
		return false
	}
	return true
}

// @Summary List Dynamic DNS entries
// @Description List all configured Dynamic DNS entries
// @Tags Dynamic DNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]dynamicdns.EntryView] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /dynamic-dns/entries [get]
func ListEntries(service dynamicDNSService) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := service.ListEntries(c.Request.Context())
		if err != nil {
			c.JSON(dynamicDNSErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_listing_dynamic_dns_entries",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]dynamicdns.EntryView]{
			Status:  "success",
			Message: "dynamic_dns_entries_listed",
			Error:   "",
			Data:    entries,
		})
	}
}

// @Summary Create a Dynamic DNS entry
// @Description Create and validate a Dynamic DNS entry
// @Tags Dynamic DNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body dynamicdns.EntryInput true "Dynamic DNS entry"
// @Success 201 {object} internal.APIResponse[dynamicdns.EntryView] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Router /dynamic-dns/entries [post]
func CreateEntry(service dynamicDNSService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input dynamicdns.EntryInput
		if !bindDynamicDNSEntryInput(c, &input) {
			return
		}

		entry, err := service.CreateEntry(c.Request.Context(), input)
		if err != nil {
			c.JSON(dynamicDNSErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_creating_dynamic_dns_entry",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[*dynamicdns.EntryView]{
			Status:  "success",
			Message: "dynamic_dns_entry_created",
			Error:   "",
			Data:    entry,
		})
	}
}

// @Summary Update a Dynamic DNS entry
// @Description Update and validate an existing Dynamic DNS entry
// @Tags Dynamic DNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Dynamic DNS entry ID" minimum(1)
// @Param request body dynamicdns.EntryInput true "Dynamic DNS entry"
// @Success 200 {object} internal.APIResponse[dynamicdns.EntryView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Router /dynamic-dns/entries/{id} [put]
func UpdateEntry(service dynamicDNSService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dynamicDNSEntryID(c)
		if !ok {
			return
		}

		var input dynamicdns.EntryInput
		if !bindDynamicDNSEntryInput(c, &input) {
			return
		}

		entry, err := service.UpdateEntry(c.Request.Context(), id, input)
		if err != nil {
			c.JSON(dynamicDNSErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_updating_dynamic_dns_entry",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*dynamicdns.EntryView]{
			Status:  "success",
			Message: "dynamic_dns_entry_updated",
			Error:   "",
			Data:    entry,
		})
	}
}

// @Summary Delete a Dynamic DNS entry
// @Description Delete an existing Dynamic DNS entry
// @Tags Dynamic DNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Dynamic DNS entry ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /dynamic-dns/entries/{id} [delete]
func DeleteEntry(service dynamicDNSService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dynamicDNSEntryID(c)
		if !ok {
			return
		}

		if err := service.DeleteEntry(c.Request.Context(), id); err != nil {
			c.JSON(dynamicDNSErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_deleting_dynamic_dns_entry",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "dynamic_dns_entry_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Synchronize a Dynamic DNS entry
// @Description Resolve and publish the current addresses for a Dynamic DNS entry
// @Tags Dynamic DNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Dynamic DNS entry ID" minimum(1)
// @Success 200 {object} internal.APIResponse[dynamicdns.EntryView] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /dynamic-dns/entries/{id}/sync [post]
func SyncEntry(service dynamicDNSService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := dynamicDNSEntryID(c)
		if !ok {
			return
		}

		entry, err := service.SyncEntry(c.Request.Context(), id)
		if err != nil {
			c.JSON(dynamicDNSErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "error_syncing_dynamic_dns_entry",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*dynamicdns.EntryView]{
			Status:  "success",
			Message: "dynamic_dns_entry_synced",
			Error:   "",
			Data:    entry,
		})
	}
}

func dynamicDNSEntryID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_dynamic_dns_entry_id",
			Error:   "invalid_entry_id",
			Data:    nil,
		})
		return 0, false
	}
	return uint(id), true
}
