package iscsiHandlers

import (
	"net/http"
	"strconv"

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

type UpdateISCSIInitiatorRequest struct {
	ID uint `json:"id"`
	ISCSIInitiatorRequest
}

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

func CreateInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ISCSIInitiatorRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.CreateInitiator(req.Nickname, req.TargetAddress, req.TargetName, req.InitiatorName, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.TgtCHAPName, req.TgtCHAPSecret); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_created"})
	}
}

func UpdateInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req UpdateISCSIInitiatorRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_request", Error: err.Error()})
			return
		}
		if err := svc.UpdateInitiator(req.ID, req.Nickname, req.TargetAddress, req.TargetName, req.InitiatorName, req.AuthMethod, req.CHAPName, req.CHAPSecret, req.TgtCHAPName, req.TgtCHAPSecret); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_updated"})
	}
}

func DeleteInitiator(svc *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "invalid_id", Error: err.Error()})
			return
		}
		if err := svc.DeleteInitiator(uint(id)); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{Status: "error", Message: err.Error(), Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{Status: "success", Message: "initiator_deleted"})
	}
}

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
