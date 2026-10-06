// SPDX-License-Identifier: BSD-2-Clause

package iscsiHandlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/gin-gonic/gin"
)

type ISCSIConfigRequest struct {
	ExtraTargetConfig *string `json:"extraTargetConfig" extensions:"x-nullable"`
}

type ISCSIConfigValidation struct {
	ReasonCode string `json:"reasonCode"`
	LineNumber *int   `json:"lineNumber" extensions:"x-nullable"`
}

// @Summary Get extra target configuration and observable status
// @Tags iSCSI
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[iscsiModels.ISCSIConfig]
// @Failure 403 {object} internal.APIResponse[any]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /iscsi/config [get]
func GetConfig(service *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		config, err := service.GetConfig()
		if err != nil {
			writeISCSIConfigError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*iscsiModels.ISCSIConfig]{
			Status: "success", Message: "iscsi_configuration_retrieved", Data: config,
		})
	}
}

// @Summary Save bounded native extra target configuration
// @Tags iSCSI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ISCSIConfigRequest true "Omit or null for no change; empty string clears; an explicit string retries"
// @Success 200 {object} internal.APIResponse[iscsiModels.ISCSIConfig]
// @Success 202 {object} internal.APIResponse[any] "Saved; runtime checks pending"
// @Failure 400 {object} internal.APIResponse[ISCSIConfigValidation]
// @Failure 403 {object} internal.APIResponse[any]
// @Failure 409 {object} internal.APIResponse[ISCSIConfigValidation]
// @Failure 413 {object} internal.APIResponse[ISCSIConfigValidation]
// @Failure 500 {object} internal.APIResponse[any]
// @Router /iscsi/config [put]
func SetConfig(service *iscsi.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		request, err := readISCSIConfigRequest(c.Request.Body)
		if err != nil {
			status, reason := http.StatusBadRequest, "invalid_request"
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				status, reason = http.StatusRequestEntityTooLarge, "iscsi_request_too_large"
			}
			c.JSON(status, internal.APIResponse[ISCSIConfigValidation]{
				Status: "error", Message: reason, Error: reason, Data: ISCSIConfigValidation{ReasonCode: reason},
			})
			return
		}
		config, err := service.SetExtraTargetConfig(request.ExtraTargetConfig)
		if err != nil {
			writeISCSIConfigError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*iscsiModels.ISCSIConfig]{
			Status: "success", Message: "iscsi_configuration_updated", Data: config,
		})
	}
}

func readISCSIConfigRequest(body io.Reader) (*ISCSIConfigRequest, error) {
	decoder := json.NewDecoder(body)
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return nil, errors.New("invalid_request")
	}
	request := &ISCSIConfigRequest{}
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key != "extraTargetConfig" || seen {
			return nil, errors.New("invalid_request")
		}
		seen = true
		if err := decoder.Decode(&request.ExtraTargetConfig); err != nil {
			return nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("invalid_request")
	}
	return request, nil
}

func writeISCSIConfigError(c *gin.Context, err error) {
	if errors.Is(err, iscsi.ErrApplyFailed) {
		writeISCSIMutationError(c, err)
		return
	}
	status, reason := http.StatusInternalServerError, "failed_to_read_or_save_iscsi_configuration"
	var line *int
	var detail *iscsi.ExtraConfigError
	switch {
	case errors.Is(err, iscsi.ErrExtraConfigTooLarge):
		status, reason = http.StatusRequestEntityTooLarge, "extra_config_too_large"
	case errors.Is(err, iscsi.ErrInvalidExtraConfig):
		status, reason = http.StatusBadRequest, "invalid_extra_config_syntax"
	case errors.Is(err, iscsi.ErrConflict):
		status, reason = http.StatusConflict, "invalid_managed_target_configuration"
		if err.Error() == "iscsi_listener_normalization_requires_stop" {
			reason = "iscsi_listener_normalization_requires_stop"
		}
	}
	if errors.As(err, &detail) {
		reason, line = detail.ReasonCode, detail.LineNumber
	}
	c.JSON(status, internal.APIResponse[ISCSIConfigValidation]{
		Status: "error", Message: reason, Error: reason, Data: ISCSIConfigValidation{ReasonCode: reason, LineNumber: line},
	})
}
