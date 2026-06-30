// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"

	"github.com/gin-gonic/gin"
)

type MdnsRecordRequest struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Port       int               `json:"port"`
	Txt        map[string]string `json:"txt"`
	Interfaces string            `json:"interfaces"`
}

// @Summary List mDNS Records
// @Description List all mDNS records (managed and user-created)
// @Tags mDNS
// @Accept json
// @Produce json
// @Success 200 {object} internal.APIResponse[[]mdnsInterfaces.MdnsRecordWithManaged] "mDNS records"
// @Failure 500 {string} string "Internal server error"
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

// @Summary Create mDNS Record
// @Description Create a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Param request body MdnsRecordRequest true "mDNS Record"
// @Success 200 {object} internal.APIResponse[mdnsModels.MdnsRecord] "Created record"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_mdns_record",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "mdns_record_created",
			Error:   "",
			Data:    record,
		})
	}
}

// @Summary Update mDNS Record
// @Description Update a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Param id path int true "Record ID"
// @Param request body MdnsRecordRequest true "mDNS Record"
// @Success 200 {string} string "Record updated"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /mdns/records/{id} [put]
func UpdateRecord(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_record_id",
				Error:   err.Error(),
				Data:    nil,
			})
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

		if err := mdnsService.UpdateRecord(uint(id), req.Name, req.Type, req.Port, req.Txt, req.Interfaces); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
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

// @Summary Delete mDNS Record
// @Description Delete a user-defined mDNS record
// @Tags mDNS
// @Accept json
// @Produce json
// @Param id path int true "Record ID"
// @Success 200 {string} string "Record deleted"
// @Failure 400 {string} string "Invalid record ID"
// @Failure 500 {string} string "Internal server error"
// @Router /mdns/records/{id} [delete]
func DeleteRecord(mdnsService mdnsInterfaces.MdnsServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_record_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := mdnsService.DeleteRecord(uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
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
