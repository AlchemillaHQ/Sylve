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

type ISCSIInitiatorRequest struct {
	Nickname      string `json:"nickname"`
	TargetAddress string `json:"targetAddress"`
	TargetName    string `json:"targetName"`
	InitiatorName string `json:"initiatorName"`
	AuthMethod    string `json:"authMethod"`
	CHAPName      string `json:"chapName"`
	CHAPSecret    string `json:"chapSecret"`
	TgtCHAPName   string `json:"tgtChapName"`
	TgtCHAPSecret string `json:"tgtChapSecret"`
}

// @Summary List iSCSI initiators
// @Description Retrieve all configured iSCSI initiators
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]iscsiModels.ISCSIInitiator] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/initiators [get]
func GetInitiators(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		initiators, err := svc.GetInitiators()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: "failed_to_get_initiators", Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]iscsiModels.ISCSIInitiator]{Status: "success", Message: "initiators_retrieved", Data: initiators})
	}
}

// @Summary Create an iSCSI initiator
// @Description Create an iSCSI initiator and regenerate the initiator configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ISCSIInitiatorRequest true "iSCSI initiator settings"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/initiators [post]
func CreateInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ISCSIInitiatorRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.CreateInitiator(req.Nickname, req.TargetAddress, req.TargetName, req.InitiatorName, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.TgtCHAPName, req.TgtCHAPSecret); err != nil {
			c.JSON(iscsiErrorStatus(err), internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[any]{Status: "success", Message: "initiator_created"})
	}
}

// @Summary Update an iSCSI initiator
// @Description Update an existing iSCSI initiator and regenerate the initiator configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Initiator ID" minimum(1)
// @Param request body ISCSIInitiatorRequest true "iSCSI initiator settings"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/initiators/{id} [put]
func UpdateInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := iscsiPathID(c, "id", "invalid_initiator_id")
		if !ok {
			return
		}

		var req ISCSIInitiatorRequest
		if !bindISCSIJSON(c, &req) {
			return
		}
		if err := svc.UpdateInitiator(id, req.Nickname, req.TargetAddress, req.TargetName, req.InitiatorName, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.TgtCHAPName, req.TgtCHAPSecret); err != nil {
			c.JSON(iscsiErrorStatus(err), internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_updated"})
	}
}

// @Summary Delete an iSCSI initiator
// @Description Delete an existing iSCSI initiator and regenerate the initiator configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Initiator ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/initiators/{id} [delete]
func DeleteInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := iscsiPathID(c, "id", "invalid_initiator_id")
		if !ok {
			return
		}
		if err := svc.DeleteInitiator(id); err != nil {
			c.JSON(iscsiErrorStatus(err), internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_deleted"})
	}
}

// @Summary Get iSCSI initiator status
// @Description Retrieve the runtime connection state of configured iSCSI initiator targets
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[map[string]string] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/status [get]
func GetStatus(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := svc.GetStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: "failed_to_get_status", Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[map[string]string]{Status: "success", Message: "status_retrieved", Data: status})
	}
}

// @Summary Connect an iSCSI initiator
// @Description Attempt to connect the configured iSCSI initiator by ID
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Initiator ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /iscsi/initiators/{id}/connect [post]
func ConnectInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := iscsiPathID(c, "id", "invalid_initiator_id")
		if !ok {
			return
		}
		if err := svc.ConnectInitiator(id); err != nil {
			c.JSON(iscsiErrorStatus(err), internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_connected"})
	}
}
