// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

// @Summary Get DHCP Ranges
// @Description Retrieve the current DHCP ranges
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range [get]
func GetDHCPRanges(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ranges, err := svc.GetRanges()
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_dhcp_ranges",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[[]networkModels.DHCPRange]{
			Status:  "success",
			Message: "dhcp_ranges_retrieved",
			Error:   "",
			Data:    ranges,
		})
	}
}

// @Summary Create DHCP Range
// @Description Create a new DHCP range
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body network.CreateDHCPRangeRequest true "Request Body"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range [post]
func CreateDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.CreateDHCPRangeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.CreateRange(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_create_dhcp_range",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_range_created",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Modify DHCP Range
// @Description Modify an existing DHCP range
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Range ID"
// @Param data body network.ModifyDHCPRangeRequest true "Request Body"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range/{id} [put]
func ModifyDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req networkServiceInterfaces.ModifyDHCPRangeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_body",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		idParam := c.Param("id")
		if idParam == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_dhcp_range_id",
				Error:   "dhcp_range_id is required",
				Data:    nil,
			})
			return
		}

		id := utils.StringToUint64(idParam)
		if id == 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_dhcp_range_id",
				Error:   "dhcp_range_id must be a valid number",
				Data:    nil,
			})
			return
		}

		req.ID = uint(id)

		if err := svc.ModifyRange(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_modify_dhcp_range",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_range_modified",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete DHCP Range
// @Description Delete an existing DHCP range
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "DHCP Range ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /network/dhcp/range/{id} [delete]
func DeleteDHCPRange(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idParam := c.Param("id")
		if idParam == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_dhcp_range_id",
				Error:   "dhcp_range_id is required",
				Data:    nil,
			})
			return
		}

		id := utils.StringToUint64(idParam)
		if id == 0 {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_dhcp_range_id",
				Error:   "dhcp_range_id must be a valid number",
				Data:    nil,
			})
			return
		}

		if err := svc.DeleteRange(uint(id)); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_dhcp_range",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "dhcp_range_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
