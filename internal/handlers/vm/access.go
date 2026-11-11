// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"slices"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

func usesSwitch(name string) bool {
	return name != "" && !strings.EqualFold(name, "none")
}

func hasValue(values []string) bool {
	return slices.ContainsFunc(values, func(value string) bool { return strings.TrimSpace(value) != "" })
}

func allowVMCreate(c *gin.Context, authService *auth.Service, req libvirtServiceInterfaces.CreateVMRequest) bool {
	var rid uint
	if req.RID != nil {
		rid = *req.RID
	}
	if !middleware.Allowed(c, authService, auth.GuestPath("vm", rid), auth.GuestAllocate) {
		return false
	}
	if req.StorageType != "" && req.StorageType != libvirtServiceInterfaces.StorageTypeNone &&
		!middleware.Allowed(c, authService, auth.StoragePath(req.StoragePool), auth.DatastoreAllocateSpace) {
		return false
	}
	if usesSwitch(req.SwitchName) && !middleware.Allowed(c, authService, auth.SwitchPath(req.SwitchName), auth.NetworkUse) {
		return false
	}
	if len(req.PCIDevices) > 0 || hasValue(req.ExtraBhyveOptions) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

// Templates have no path of their own yet, so cloning one needs Guest.Clone on /vm.
func allowVMTemplateCreate(c *gin.Context, authService *auth.Service, template *vmModels.VMTemplate, req libvirtServiceInterfaces.CreateFromTemplateRequest) bool {
	if !middleware.Allowed(c, authService, "/vm", auth.GuestClone) {
		return false
	}
	rids := []uint{req.RID}
	if req.Mode == "multiple" {
		rids = rids[:0]
		for i := range min(max(req.Count, 0), 200) {
			rids = append(rids, req.StartRID+uint(i))
		}
	}
	for _, rid := range rids {
		if !middleware.Allowed(c, authService, auth.GuestPath("vm", rid), auth.GuestAllocate) {
			return false
		}
	}
	for _, storage := range template.Storages {
		pool := storage.Pool
		for _, assigned := range req.StoragePools {
			if assigned.SourceStorageID == storage.SourceStorageID {
				pool = assigned.Pool
			}
		}
		if !middleware.Allowed(c, authService, auth.StoragePath(pool), auth.DatastoreAllocateSpace) {
			return false
		}
	}
	for _, network := range template.Networks {
		if usesSwitch(network.SwitchName) && !middleware.Allowed(c, authService, auth.SwitchPath(network.SwitchName), auth.NetworkUse) {
			return false
		}
	}
	if hasValue(template.ExtraBhyveOptions) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

func allowStorageAttach(c *gin.Context, authService *auth.Service, req libvirtServiceInterfaces.StorageAttachRequest) bool {
	if !middleware.Allowed(c, authService, auth.GuestPath("vm", req.RID), auth.GuestConfigDisk) {
		return false
	}
	usesPool := req.StorageType == libvirtServiceInterfaces.StorageTypeRaw || req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL
	if usesPool && req.Pool != nil && !middleware.Allowed(c, authService, auth.StoragePath(*req.Pool), auth.DatastoreAllocateSpace) {
		return false
	}
	if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem ||
		(usesPool && req.AttachType == libvirtServiceInterfaces.StorageAttachTypeImport) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

// Only directory shares have a target and a read-only switch.
func allowStorageUpdate(c *gin.Context, authService *auth.Service, req libvirtServiceInterfaces.StorageUpdateRequest) bool {
	if !middleware.Allowed(c, authService, auth.GuestPath("vm", req.RID), auth.GuestConfigDisk) {
		return false
	}
	if req.FilesystemTarget != nil || req.ReadOnly != nil {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

func allowNetworkChange(c *gin.Context, authService *auth.Service, rid uint, switchName string) bool {
	if !middleware.Allowed(c, authService, auth.GuestPath("vm", rid), auth.GuestConfigNetwork) {
		return false
	}
	return !usesSwitch(switchName) || middleware.Allowed(c, authService, auth.SwitchPath(switchName), auth.NetworkUse)
}

func hideVNCPassword(can func(string, ...string) bool, vm *vmModels.VM) {
	path := auth.GuestPath("vm", vm.RID)
	if !can(path, auth.GuestConsole) && !can(path, auth.GuestConfigHWType) {
		vm.VNCPassword = ""
	}
}
