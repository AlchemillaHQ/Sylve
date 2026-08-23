// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	mdnsService "github.com/alchemillahq/sylve/internal/services/mdns"

	"github.com/gin-gonic/gin"

	_ "github.com/alchemillahq/sylve/internal/db/models/mdns"
)

type MdnsRecordRequest struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Port       int               `json:"port"`
	Txt        map[string]string `json:"txt"`
	Interfaces string            `json:"interfaces"`
}

func mdnsRecordServiceErrorStatus(err error) int {
	switch {
	case errors.Is(err, mdnsService.ErrInvalidRecord):
		return http.StatusBadRequest
	case errors.Is(err, mdnsService.ErrRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, mdnsService.ErrRecordConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func mdnsRecordID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_record_id",
			Error:   "record ID must be a positive integer",
			Data:    nil,
		})
		return 0, false
	}

	return uint(id), true
}

// @Summary List mDNS records
// @Description List all mDNS records (managed and user-created)
// @Tags mDNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]mdnsInterfaces.MdnsRecordWithManaged] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /mdns/records [get]
func GetRecords(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := mdnsService.GetRecords()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_mdns_records",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_records_retrieved",
			Error:   "",
			Data:    records,
		})
	}
}

// @Summary Create an mDNS record
// @Description Create a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body MdnsRecordRequest true "mDNS Record"
// @Success 201 {object} internal.APIResponse[mdnsModels.MdnsRecord] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /mdns/records [post]
func CreateRecord(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MdnsRecordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		record, err := mdnsService.CreateRecord(req.Name, req.Type, req.Port, req.Txt, req.Interfaces)
		if err != nil {
			c.JSON(mdnsRecordServiceErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_mdns_record",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_record_created",
			Error:   "",
			Data:    record,
		})
	}
}

// @Summary Update an mDNS record
// @Description Update a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Record ID"
// @Param request body MdnsRecordRequest true "mDNS Record"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /mdns/records/{id} [put]
func UpdateRecord(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := mdnsRecordID(c)
		if !ok {
			return
		}

		var req MdnsRecordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := mdnsService.UpdateRecord(id, req.Name, req.Type, req.Port, req.Txt, req.Interfaces); err != nil {
			c.JSON(mdnsRecordServiceErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_mdns_record",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_record_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete an mDNS record
// @Description Delete a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Record ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /mdns/records/{id} [delete]
func DeleteRecord(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := mdnsRecordID(c)
		if !ok {
			return
		}

		if err := mdnsService.DeleteRecord(id); err != nil {
			c.JSON(mdnsRecordServiceErrorStatus(err), internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_mdns_record",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_record_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
