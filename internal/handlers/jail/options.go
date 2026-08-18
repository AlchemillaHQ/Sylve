// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ModifyBootOrderRequest struct {
	StartAtBoot *bool `json:"startAtBoot" binding:"required"`
	BootOrder   *int  `json:"bootOrder" binding:"required"`
}

type ModifyWakeOnLanRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type ModifyFstabRequest struct {
	Fstab *string `json:"fstab" binding:"required"`
}

type ModifyResolvConfRequest struct {
	ResolvConf *string `json:"resolvConf" binding:"required"`
}

type ModifyDevFSRulesRequest struct {
	DevFSRules *string `json:"devFSRules" binding:"required"`
}

type ModifyAdditionalOptionsRequest struct {
	AdditionalOptions *string `json:"additionalOptions" binding:"required"`
}

type ModifyAllowedOptionsRequest struct {
	AllowedOptions *[]string `json:"allowedOptions" binding:"required"`
}

type ModifyMetadataRequest struct {
	Metadata *string `json:"metadata" binding:"required"`
	Env      *string `json:"env" binding:"required"`
}

type ModifyLifecycleHookPhaseRequest struct {
	Enabled *bool   `json:"enabled" binding:"required"`
	Script  *string `json:"script" binding:"required"`
}

type ModifyLifecycleHooksPayload struct {
	Prestart  *ModifyLifecycleHookPhaseRequest `json:"prestart" binding:"required"`
	Start     *ModifyLifecycleHookPhaseRequest `json:"start" binding:"required"`
	Poststart *ModifyLifecycleHookPhaseRequest `json:"poststart" binding:"required"`
	Prestop   *ModifyLifecycleHookPhaseRequest `json:"prestop" binding:"required"`
	Stop      *ModifyLifecycleHookPhaseRequest `json:"stop" binding:"required"`
	Poststop  *ModifyLifecycleHookPhaseRequest `json:"poststop" binding:"required"`
}

type ModifyLifecycleHooksRequest struct {
	Hooks *ModifyLifecycleHooksPayload `json:"hooks" binding:"required"`
}

type jailOptionsService interface {
	ModifyBootOrder(ctID uint, startAtBoot bool, bootOrder int) error
	ModifyWakeOnLan(ctID uint, enabled bool) error
	ModifyFstab(ctID uint, fstab string) error
	ModifyResolvConf(ctID uint, resolvConf string) error
	ModifyDevfsRuleset(ctID uint, rules string) error
	ModifyAdditionalOptions(ctID uint, options string) error
	ModifyAllowedOptions(ctID uint, options []string) error
	ModifyMetadata(ctID uint, metadata string, env string) error
	ModifyLifecycleHooks(ctID uint, hooks jailServiceInterfaces.Hooks) error
}

func jailOptionErrorCodes(err error) map[string]struct{} {
	codes := make(map[string]struct{})
	if err == nil {
		return codes
	}
	for _, part := range strings.FieldsFunc(strings.ToLower(err.Error()), func(r rune) bool {
		return r == ':' || r == '\n'
	}) {
		if code := strings.TrimSpace(part); code != "" {
			codes[code] = struct{}{}
		}
	}
	return codes
}

func jailOptionHasErrorCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

func firstJailOptionErrorCode(err error) string {
	if err == nil {
		return "internal_server_error"
	}
	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if index := strings.IndexAny(code, ":\n"); index >= 0 {
		code = code[:index]
	}
	if code == "" {
		return "internal_server_error"
	}
	return code
}

func classifyJailOptionError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "internal_server_error"
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound, "jail_not_found"
	}

	codes := jailOptionErrorCodes(err)
	switch {
	case jailOptionHasErrorCode(codes,
		"invalid_request", "invalid_ct_id", "ctid_must_be_positive",
		"start_order_must_be_greater_than_or_equal_to_0", "invalid_jail_allowed_options",
		"invalid_jail_metadata", "invalid_lifecycle_hooks", "lifecycle_hook_script_required",
		"reserved_jail_option_marker"):
		return http.StatusBadRequest, firstJailOptionErrorCode(err)
	case jailOptionHasErrorCode(codes, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case jailOptionHasErrorCode(codes, "jail_not_found"):
		return http.StatusNotFound, "jail_not_found"
	case jailOptionHasErrorCode(codes,
		"restore_in_progress", "jail_config_not_found",
		"jail_option_config_conflict", "jail_lifecycle_hook_conflict",
		"jail_dataset_mountpoint_not_usable"):
		return http.StatusConflict, firstJailOptionErrorCode(err)
	case jailOptionHasErrorCode(codes, "devfs_management_disabled", "devfs_service_unavailable"):
		return http.StatusServiceUnavailable, firstJailOptionErrorCode(err)
	default:
		return http.StatusInternalServerError, "internal_server_error"
	}
}

func writeJailOptionError(c *gin.Context, err error) {
	status, message := classifyJailOptionError(err)
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Data:    nil,
		Error:   err.Error(),
	})
}

func writeJailOptionSuccess(c *gin.Context, message string) {
	c.JSON(http.StatusOK, internal.APIResponse[any]{
		Status:  "success",
		Message: message,
		Data:    nil,
		Error:   "",
	})
}

func bindJailOptionCTID(c *gin.Context) (uint, bool) {
	ctID, err := utils.ParamUint(c, "ctid")
	if err != nil {
		writeJailOptionError(c, fmt.Errorf("invalid_request: %w", err))
		return 0, false
	}
	if ctID == 0 {
		writeJailOptionError(c, errors.New("ctid_must_be_positive"))
		return 0, false
	}
	return ctID, true
}

func bindJailOptionJSON(c *gin.Context, request any) bool {
	return bindJailJSON(c, request, "invalid_request")
}

// @Summary Replace jail automatic-start configuration
// @Description Replace whether a jail starts at boot and its relative start order; explicit false and zero values are preserved
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyBootOrderRequest true "Automatic-start configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/boot-order [put]
func ModifyBootOrder(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyBootOrderRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyBootOrder(ctID, *req.StartAtBoot, *req.BootOrder); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "boot_order_modified")
	}
}

// @Summary Replace jail Wake-on-LAN configuration
// @Description Enable or disable Wake-on-LAN for all MAC addresses attached to a jail
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyWakeOnLanRequest true "Wake-on-LAN configuration"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/wol [put]
func ModifyWakeOnLan(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyWakeOnLanRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyWakeOnLan(ctID, *req.Enabled); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "wol_modified")
	}
}

// @Summary Replace jail fstab configuration
// @Description Replace the jail fstab document; an explicit empty string removes the managed fstab file and jail.conf wiring
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyFstabRequest true "Fstab document"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/fstab [put]
func ModifyFstab(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyFstabRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyFstab(ctID, *req.Fstab); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "fstab_modified")
	}
}

// @Summary Replace jail resolv.conf configuration
// @Description Replace /etc/resolv.conf inside a jail; an explicit empty string removes the managed file
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyResolvConfRequest true "resolv.conf document"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/resolv-conf [put]
func ModifyResolvConf(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyResolvConfRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyResolvConf(ctID, *req.ResolvConf); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "resolv_conf_modified")
	}
}

// @Summary Replace jail DevFS rules
// @Description Replace the jail-specific DevFS rules; an explicit empty string removes the custom rules and restores the default ruleset
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyDevFSRulesRequest true "DevFS rules"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/options/devfs-rules [put]
func ModifyDevFSRules(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyDevFSRulesRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyDevfsRuleset(ctID, *req.DevFSRules); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "devfs_rules_modified")
	}
}

// @Summary Replace additional jail.conf options
// @Description Replace intentionally raw user-defined jail.conf options; an explicit empty string removes the managed block
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyAdditionalOptionsRequest true "Additional jail.conf options"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/additional-options [put]
func ModifyAdditionalOptions(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyAdditionalOptionsRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyAdditionalOptions(ctID, *req.AdditionalOptions); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "additional_options_modified")
	}
}

// @Summary Replace allowed jail options
// @Description Replace the complete normalized allow.* option list; an explicit empty array clears every managed allowed option
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyAllowedOptionsRequest true "Allowed options"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/options/allowed-options [put]
func ModifyAllowedOptions(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyAllowedOptionsRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyAllowedOptions(ctID, *req.AllowedOptions); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "allowed_options_modified")
	}
}

// @Summary Replace jail metadata
// @Description Replace the jail meta and env values after safely escaping them for jail.conf; explicit empty strings clear either value
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyMetadataRequest true "Metadata values"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/metadata [put]
func ModifyMetadata(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyMetadataRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		if err := service.ModifyMetadata(ctID, *req.Metadata, *req.Env); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "metadata_modified")
	}
}

func lifecycleHooksFromRequest(req *ModifyLifecycleHooksPayload) (jailServiceInterfaces.Hooks, error) {
	if req == nil || req.Prestart == nil || req.Start == nil || req.Poststart == nil ||
		req.Prestop == nil || req.Stop == nil || req.Poststop == nil {
		return jailServiceInterfaces.Hooks{}, errors.New("invalid_lifecycle_hooks")
	}
	toPhase := func(phase *ModifyLifecycleHookPhaseRequest) (jailServiceInterfaces.HookPhase, error) {
		if phase.Enabled == nil || phase.Script == nil {
			return jailServiceInterfaces.HookPhase{}, errors.New("invalid_lifecycle_hooks")
		}
		return jailServiceInterfaces.HookPhase{Enabled: *phase.Enabled, Script: *phase.Script}, nil
	}

	prestart, err := toPhase(req.Prestart)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}
	start, err := toPhase(req.Start)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}
	poststart, err := toPhase(req.Poststart)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}
	prestop, err := toPhase(req.Prestop)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}
	stop, err := toPhase(req.Stop)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}
	poststop, err := toPhase(req.Poststop)
	if err != nil {
		return jailServiceInterfaces.Hooks{}, err
	}

	return jailServiceInterfaces.Hooks{
		Prestart: prestart, Start: start, Poststart: poststart,
		Prestop: prestop, Stop: stop, Poststop: poststop,
	}, nil
}

// @Summary Replace jail lifecycle hooks
// @Description Replace all six jail exec.* lifecycle-hook phases; every phase, enabled flag, and script must be supplied, and disabled empty scripts explicitly clear a phase
// @Tags Jail
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param request body ModifyLifecycleHooksRequest true "Lifecycle hooks"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /jail/{ctid}/options/lifecycle-hooks [put]
func ModifyLifecycleHooks(service jailOptionsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := bindJailOptionCTID(c)
		if !ok {
			return
		}
		var req ModifyLifecycleHooksRequest
		if !bindJailOptionJSON(c, &req) {
			return
		}
		hooks, err := lifecycleHooksFromRequest(req.Hooks)
		if err != nil {
			writeJailOptionError(c, err)
			return
		}
		if err := service.ModifyLifecycleHooks(ctID, hooks); err != nil {
			writeJailOptionError(c, err)
			return
		}
		writeJailOptionSuccess(c, "lifecycle_hooks_modified")
	}
}
