// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package containerHandlers

import (
	"reflect"
	"strings"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

type restricted struct {
	hostNetwork bool
	mounts      []containerModels.Mount
	annotations map[string]string
	lowPorts    []uint16
}

// A stack's "./" datasets live under the stack, so they don't reach the host.
func restrictedOf(spec containerServiceInterfaces.Spec) restricted {
	r := restricted{hostNetwork: spec.Network == containerServiceInterfaces.HostNetwork}
	for _, mount := range spec.Mounts {
		if mount.Type == containerModels.MountPath ||
			(mount.Type == containerModels.MountDataset && !strings.HasPrefix(mount.Source, "./")) {
			r.mounts = append(r.mounts, mount)
		}
	}
	if len(spec.Annotations) > 0 {
		r.annotations = spec.Annotations
	}
	for _, port := range spec.Ports {
		if port.HostPort != 0 && port.HostPort < 1024 {
			r.lowPorts = append(r.lowPorts, port.HostPort)
		}
	}
	return r
}

func (r restricted) any() bool {
	return r.hostNetwork || len(r.mounts) > 0 || len(r.annotations) > 0 || len(r.lowPorts) > 0
}

func allowSwitch(c *gin.Context, authService *auth.Service, service containerServiceInterfaces.ContainerServiceInterface, network string) bool {
	if network == "" || network == containerServiceInterfaces.HostNetwork {
		return true
	}
	name, err := service.NetworkSwitchName(c.Request.Context(), network)
	if err != nil {
		failService(c, "failed_to_check_container_network", err)
		return false
	}
	return name == "" || middleware.Allowed(c, authService, auth.SwitchPath(name), auth.NetworkUse)
}

func allowContainerCreate(c *gin.Context, authService *auth.Service, service containerServiceInterfaces.ContainerServiceInterface, req containerServiceInterfaces.CreateRequest) bool {
	if !middleware.Allowed(c, authService, auth.GuestPath("container", req.OCID), auth.GuestAllocate) ||
		!middleware.Allowed(c, authService, auth.StoragePath(req.Pool), auth.DatastoreAllocateSpace) ||
		!allowSwitch(c, authService, service, req.Network) {
		return false
	}
	if restrictedOf(req.Spec).any() {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

// A stack's IDs are picked when it's prepared, so creating one needs the right to create any
// container. Its containers are on the stack's own network.
func allowStackCreate(c *gin.Context, authService *auth.Service, req containerServiceInterfaces.StackRequest) bool {
	if !middleware.Allowed(c, authService, "/container/*", auth.GuestAllocate) ||
		!middleware.Allowed(c, authService, auth.StoragePath(req.Pool), auth.DatastoreAllocateSpace) {
		return false
	}
	for _, container := range req.Containers {
		if restrictedOf(container.Spec).any() {
			return middleware.RequireHostAdmin(c, authService)
		}
	}
	return true
}

func sameList[T any](a, b []T) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}

// An update replaces the whole spec, so it needs the privilege of each part that changed, and host
// admin when what reaches the host changed.
func allowContainerUpdate(c *gin.Context, authService *auth.Service, service containerServiceInterfaces.ContainerServiceInterface, ocid uint, old, spec containerServiceInterfaces.Spec) bool {
	var privileges []string
	if old.Image != spec.Image || old.OS != spec.OS || spec.PullImage {
		privileges = append(privileges, auth.GuestAllocate)
	}
	if old.Network != spec.Network || old.IP != spec.IP || !sameList(old.Ports, spec.Ports) || !sameList(old.ExtraHosts, spec.ExtraHosts) {
		privileges = append(privileges, auth.GuestConfigNetwork)
	}
	if !sameList(old.Mounts, spec.Mounts) {
		privileges = append(privileges, auth.GuestConfigDisk)
	}
	if old.ShmSize != spec.ShmSize {
		privileges = append(privileges, auth.GuestConfigMemory)
	}
	if old.Name != spec.Name || old.Hostname != spec.Hostname || old.WorkDir != spec.WorkDir || old.User != spec.User ||
		old.Restart != spec.Restart || old.RestartRetries != spec.RestartRetries || old.StopTimeout != spec.StopTimeout ||
		!sameList(old.Command, spec.Command) || !sameList(old.Entrypoint, spec.Entrypoint) || !sameList(old.Env, spec.Env) {
		privileges = append(privileges, auth.GuestConfigOptions)
	}
	if len(privileges) > 0 && !middleware.Allowed(c, authService, auth.GuestPath("container", ocid), privileges...) {
		return false
	}
	if old.Network != spec.Network && !allowSwitch(c, authService, service, spec.Network) {
		return false
	}
	if !reflect.DeepEqual(restrictedOf(old), restrictedOf(spec)) {
		return middleware.RequireHostAdmin(c, authService)
	}
	return true
}

func allowOptionsUpdate(c *gin.Context, authService *auth.Service, ocid uint, req containerServiceInterfaces.OptionsRequest) bool {
	var privileges []string
	if req.Title != nil || req.Description != nil || req.StartAtBoot != nil || req.StartOrder != nil {
		privileges = append(privileges, auth.GuestConfigOptions)
	}
	if req.Cores != nil {
		privileges = append(privileges, auth.GuestConfigCPU)
	}
	if req.Memory != nil {
		privileges = append(privileges, auth.GuestConfigMemory)
	}
	return len(privileges) == 0 || middleware.Allowed(c, authService, auth.GuestPath("container", ocid), privileges...)
}

func allowStackRename(c *gin.Context, authService *auth.Service, service containerServiceInterfaces.ContainerServiceInterface, stack string) bool {
	items, err := service.List(c.Request.Context())
	if err != nil {
		failService(c, "failed_to_list_containers", err)
		return false
	}
	for _, item := range items {
		if item.Stack == stack && !middleware.Allowed(c, authService, auth.GuestPath("container", item.OCID), auth.GuestConfigOptions) {
			return false
		}
	}
	return true
}
