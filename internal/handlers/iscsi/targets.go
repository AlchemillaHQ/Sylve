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

type ISCSITargetPortalRequest struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type ISCSITargetLUNRequest struct {
	LUNNumber int    `json:"lunNumber"`
	ZVol      string `json:"zvol"`
}

// @Summary List iSCSI targets
// @Description Retrieve all configured iSCSI targets with their portals and LUNs
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]iscsiModels.ISCSITarget] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets [get]
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

// @Summary Create an iSCSI target
// @Description Create an iSCSI target and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ISCSITargetRequest true "iSCSI target settings"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets [post]
func CreateTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ISCSITargetRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.CreateTarget(req.TargetName, req.Alias, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.MutualCHAPName, req.MutualCHAPSecret); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[any]{Status: "success", Message: "target_created"})
	}
}

// @Summary Update an iSCSI target
// @Description Update an existing iSCSI target and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Param request body ISCSITargetRequest true "iSCSI target settings"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId} [put]
func UpdateTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}

		var req ISCSITargetRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.UpdateTarget(id, req.TargetName, req.Alias, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.MutualCHAPName, req.MutualCHAPSecret); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "target_updated"})
	}
}

// @Summary Delete an iSCSI target
// @Description Delete an iSCSI target and its portals and LUNs, then regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId} [delete]
func DeleteTarget(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}
		if err := svc.DeleteTarget(id); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "target_deleted"})
	}
}

// @Summary Add an iSCSI target portal
// @Description Add a portal to an existing iSCSI target and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Param request body ISCSITargetPortalRequest true "iSCSI target portal settings"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId}/portals [post]
func AddPortal(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}
		var req ISCSITargetPortalRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.AddPortal(targetID, req.Address, req.Port); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[any]{Status: "success", Message: "portal_added"})
	}
}

// @Summary Remove an iSCSI target portal
// @Description Remove an existing iSCSI target portal and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Param portalId path int true "Portal ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId}/portals/{portalId} [delete]
func RemovePortal(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}
		portalID, ok := iscsiPathID(c, "portalId", "invalid_portal_id")
		if !ok {
			return
		}
		if err := svc.RemovePortal(targetID, portalID); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "portal_removed"})
	}
}

// @Summary Add an iSCSI target LUN
// @Description Add a ZFS volume-backed LUN to an existing iSCSI target and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Param request body ISCSITargetLUNRequest true "iSCSI target LUN settings"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId}/luns [post]
func AddLUN(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}
		var req ISCSITargetLUNRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.AddLUN(targetID, req.LUNNumber, req.ZVol); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[any]{Status: "success", Message: "lun_added"})
	}
}

// @Summary Remove an iSCSI target LUN
// @Description Remove an existing iSCSI target LUN and regenerate the target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param targetId path int true "Target ID" minimum(1)
// @Param lunId path int true "LUN ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/targets/{targetId}/luns/{lunId} [delete]
func RemoveLUN(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID, ok := iscsiPathID(c, "targetId", "invalid_target_id")
		if !ok {
			return
		}
		lunID, ok := iscsiPathID(c, "lunId", "invalid_lun_id")
		if !ok {
			return
		}
		if err := svc.RemoveLUN(targetID, lunID); err != nil {
			writeISCSIMutationError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "lun_removed"})
	}
}

// @Summary List iSCSI target sessions
// @Description Retrieve the active connection count for each iSCSI target
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[map[string]int] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/target-sessions [get]
func GetTargetSessions(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessions, err := svc.GetTargetSessions()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: "failed_to_get_target_sessions", Error: err.Error()})
			return
		}
		if sessions == nil {
			sessions = make(map[string]int)
		}
		c.JSON(http.StatusOK, internal.APIResponse[map[string]int]{Status: "success", Message: "target_sessions_retrieved", Data: sessions})
	}
}
