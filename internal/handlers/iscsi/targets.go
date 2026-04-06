// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiHandlers

import (
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/gin-gonic/gin"
)

type ISCSITargetRequest struct {
	TargetName       string `json:"targetName"`
	Alias            string `json:"alias"`
	AuthMethod       string `json:"authMethod"`
	CHAPName         string `json:"chapName"`
	CHAPSecret       string `json:"chapSecret"`
	MutualCHAPName   string `json:"mutualChapName"`
	MutualCHAPSecret string `json:"mutualChapSecret"`
}

type UpdateISCSITargetRequest struct {
	ID uint `json:"id"`
	ISCSITargetRequest
}

type ISCSITargetPortalRequest struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type ISCSITargetLUNRequest struct {
	LUNNumber int    `json:"lunNumber"`
	ZVol      string `json:"zvol"`
}

func GetTargets(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targets, err := svc.GetTargets()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: "failed_to_get_targets", Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]iscsiModels.ISCSITarget]{Status: "success", Message: "targets_retrieved", Data: targets})
	}
}

func CreateTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ISCSITargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.CreateTarget(req.TargetName, req.Alias, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.MutualCHAPName, req.MutualCHAPSecret); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "target_created"})
	}
}

func UpdateTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req UpdateISCSITargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.UpdateTarget(req.ID, req.TargetName, req.Alias, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.MutualCHAPName, req.MutualCHAPSecret); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "target_updated"})
	}
}

func DeleteTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		if err := svc.DeleteTarget(uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "target_deleted"})
	}
}

func AddPortal(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		targetID, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		var req ISCSITargetPortalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.AddPortal(uint(targetID), req.Address, req.Port); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "portal_added"})
	}
}

func RemovePortal(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("portalId")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		if err := svc.RemovePortal(uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "portal_removed"})
	}
}

func AddLUN(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		targetID, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		var req ISCSITargetLUNRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.AddLUN(uint(targetID), req.LUNNumber, req.ZVol); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "lun_added"})
	}
}

func RemoveLUN(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("lunId")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		if err := svc.RemoveLUN(uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "lun_removed"})
	}
}
