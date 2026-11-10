// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"slices"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/gin-gonic/gin"
)

func usesSwitch(name string) bool {
	return name != "" && !strings.EqualFold(name, "none") && !strings.EqualFold(name, "inherit")
}

func hooksSet(hooks jailServiceInterfaces.Hooks) bool {
	for _, phase := range []jailServiceInterfaces.HookPhase{hooks.Prestart, hooks.Start, hooks.Poststart, hooks.Prestop, hooks.Stop, hooks.Poststop} {
		if phase.Enabled || strings.TrimSpace(phase.Script) != "" {
			return true
		}
	}
	return false
}

// The create dialog's defaults for each type. Options beyond them need host admin.
var defaultAllowedOptions = map[jailModels.JailType][]string{
	jailModels.JailTypeFreeBSD: {
		"allow.chflags", "allow.set_hostname", "allow.raw_sockets", "allow.socket_af", "allow.suser",
		"allow.sysvipc", "allow.reserved_ports", "allow.mount.devfs",
	},
	jailModels.JailTypeLinux: {
		"allow.chflags", "allow.set_hostname", "allow.raw_sockets", "allow.socket_af", "allow.suser",
		"allow.sysvipc", "allow.mount.tmpfs", "allow.mount.linprocfs", "allow.mount.linsysfs",
		"allow.mount.devfs",
	},
}

func optionsSet(jailType jailModels.JailType, fstab, devfsRuleset, additionalOptions string, allowedOptions []string) bool {
	extraAllowed := slices.ContainsFunc(allowedOptions, func(option string) bool {
		return !slices.Contains(defaultAllowedOptions[jailType], option)
	})
	return strings.TrimSpace(fstab) != "" || strings.TrimSpace(devfsRuleset) != "" ||
		strings.TrimSpace(additionalOptions) != "" || extraAllowed
}

func allowJailCreate(c *gin.Context, authService *auth.Service, req jailServiceInterfaces.CreateJailRequest) bool {
	var ctid uint
	if req.CTID != nil {
		ctid = *req.CTID
	}
	if !middleware.Allowed(c, authService, auth.GuestPath("jail", ctid), auth.GuestAllocate) ||
		!middleware.Allowed(c, authService, auth.StoragePath(req.Pool), auth.DatastoreAllocateSpace) {
		return false
	}
	if usesSwitch(req.SwitchName) && !middleware.Allowed(c, authService, auth.SwitchPath(req.SwitchName), auth.NetworkUse) {
		return false
	}
	if req.ZFSSource != nil || strings.EqualFold(req.SwitchName, "inherit") || hooksSet(req.Hooks) ||
		optionsSet(req.Type, req.Fstab, req.DevFSRuleset, req.AdditionalOptions, req.AllowedOptions) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

// Templates have no path of their own yet, so cloning one needs Guest.Clone on /jail.
func allowJailTemplateCreate(c *gin.Context, authService *auth.Service, service jailTemplateService, template *jailModels.JailTemplate, req jail.CreateFromTemplateRequest) bool {
	if !middleware.Allowed(c, authService, "/jail", auth.GuestClone) {
		return false
	}
	ctids := []uint{req.CTID}
	if req.Mode == "multiple" {
		ctids = ctids[:0]
		for i := range min(max(req.Count, 0), 200) {
			ctids = append(ctids, req.StartCTID+uint(i))
		}
	}
	for _, ctid := range ctids {
		if !middleware.Allowed(c, authService, auth.GuestPath("jail", ctid), auth.GuestAllocate) {
			return false
		}
	}
	pool := req.Pool
	if pool == "" {
		pool = template.Pool
	}
	if !middleware.Allowed(c, authService, auth.StoragePath(pool), auth.DatastoreAllocateSpace) {
		return false
	}
	for _, network := range template.Networks {
		name, err := service.SwitchName(network.SwitchType, network.SwitchID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "template_create_preflight_failed", Error: err.Error(),
			})
			return false
		}
		if !middleware.Allowed(c, authService, auth.SwitchPath(name), auth.NetworkUse) {
			return false
		}
	}
	hooks := false
	for _, hook := range template.Hooks {
		hooks = hooks || hook.Enabled || strings.TrimSpace(hook.Script) != ""
	}
	if hooks || template.InheritIPv4 || template.InheritIPv6 ||
		optionsSet(template.Type, template.Fstab, template.DevFSRuleset, template.AdditionalOptions, template.AllowedOptions) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

func allowJailNetworkChange(c *gin.Context, authService *auth.Service, ctid uint, switchName string) bool {
	if !middleware.Allowed(c, authService, auth.GuestPath("jail", ctid), auth.GuestConfigNetwork) {
		return false
	}
	return !usesSwitch(switchName) || middleware.Allowed(c, authService, auth.SwitchPath(switchName), auth.NetworkUse)
}
