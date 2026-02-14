// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type ModifyBootOrderRequest struct {
	StartAtBoot *bool `json:"startAtBoot"`
	BootOrder   *int  `json:"bootOrder"`
}

type ModifyFstabRequest struct {
	Fstab *string `json:"fstab"`
}

type ModifyDevFSRulesRequest struct {
	DevFSRules *string `json:"devFSRules"`
}

type ModifyAdditionalOptionsRequest struct {
	AdditionalOptions *string `json:"additionalOptions"`
}

type ModifyAllowedOptionsRequest struct {
	AllowedOptions *[]string `json:"allowedOptions"`
}

type ModifyMetadataRequest struct {
	Metadata *string `json:"metadata"`
	Env      *string `json:"env"`
}

type ModifyLifecycleHooksRequest struct {
	Hooks *jailServiceInterfaces.Hooks `json:"hooks"`
}

// @Summary Modify Boot Order of a Jail
// @Description Modify the Boot Order configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyBootOrderRequest true "Modify Boot Order Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/boot-order/:rid [put]
func ModifyBootOrder(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyBootOrderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		startAtBoot := false
		if req.StartAtBoot != nil {
			startAtBoot = *req.StartAtBoot
		}

		bootOrder := 0
		if req.BootOrder != nil {
			bootOrder = *req.BootOrder
		}

		if err := jailService.ModifyBootOrder(rid, startAtBoot, bootOrder); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "boot_order_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Fstab of a Jail
// @Description Modify the Fstab configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyFstabRequest true "Modify Fstab Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/fstab/:rid [put]
func ModifyFstab(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyFstabRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		fstab := ""
		if req.Fstab != nil {
			fstab = *req.Fstab
		}

		if err := jailService.ModifyFstab(rid, fstab); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "fstab_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify DevFS rules of a Jail
// @Description Modify the DevFS rules configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyDevFSRulesRequest true "Modify DevFS Rules Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/devfs-rules/:rid [put]
func ModifyDevFSRules(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyDevFSRulesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		devFSRules := ""
		if req.DevFSRules != nil {
			devFSRules = *req.DevFSRules
		}

		if err := jailService.ModifyDevfsRuleset(rid, devFSRules); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "devfs_rules_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Additional Options of a Jail
// @Description Modify the Additional Options configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyAdditionalOptionsRequest true "Modify Additional Options Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/additional-options/:rid [put]
func ModifyAdditionalOptions(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyAdditionalOptionsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		additionalOptions := ""
		if req.AdditionalOptions != nil {
			additionalOptions = *req.AdditionalOptions
		}

		if err := jailService.ModifyAdditionalOptions(rid, additionalOptions); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "additional_options_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Allowed Options of a Jail
// @Description Modify allowed options configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyAllowedOptionsRequest true "Modify Allowed Options Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/allowed-options/:rid [put]
func ModifyAllowedOptions(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyAllowedOptionsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		allowedOptions := []string{}
		if req.AllowedOptions != nil {
			allowedOptions = *req.AllowedOptions
		}

		if err := jailService.ModifyAllowedOptions(rid, allowedOptions); err != nil {
			statusCode := 500
			message := "internal_server_error"
			if strings.Contains(err.Error(), "invalid_jail_allowed_options") {
				statusCode = 400
				message = "invalid_request"
			}

			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "allowed_options_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Metadata of a Jail
// @Description Modify the Metadata configuration of a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyMetadataRequest true "Modify Metadata Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/metadata/:rid [put]
func ModifyMetadata(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyMetadataRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		meta := ""
		if req.Metadata != nil {
			meta = *req.Metadata
		}

		env := ""
		if req.Env != nil {
			env = *req.Env
		}

		if err := jailService.ModifyMetadata(rid, meta, env); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "metadata_modified",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Modify Lifecycle Hooks of a Jail
// @Description Modify jail exec.* lifecycle hooks in one request
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ModifyLifecycleHooksRequest true "Modify Lifecycle Hooks Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /options/lifecycle-hooks/:rid [put]
func ModifyLifecycleHooks(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, "rid")
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		var req ModifyLifecycleHooksRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "invalid_request: " + err.Error(),
			})
			return
		}

		hooks := jailServiceInterfaces.Hooks{}
		if req.Hooks != nil {
			hooks = *req.Hooks
		}

		if err := jailService.ModifyLifecycleHooks(rid, hooks); err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "lifecycle_hooks_modified",
			Data:    nil,
			Error:   "",
		})
	}
}
