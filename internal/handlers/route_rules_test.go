// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/bootstrap"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authServicePkg "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/certificates"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/container"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	diskServicePkg "github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
	infoService "github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	jailServicePkg "github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/mdns"
	"github.com/alchemillahq/sylve/internal/services/migration"
	networkServicePkg "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/nfs"
	notificationsService "github.com/alchemillahq/sylve/internal/services/notifications"
	sambaServicePkg "github.com/alchemillahq/sylve/internal/services/samba"
	systemServicePkg "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesServicePkg "github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestEveryAPIRouteHasARule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// The container and directory-services routes are only registered when those services exist.
	RegisterRoutes(r, internal.Development, true,
		&authServicePkg.Service{}, &infoService.Service{}, &zfsService.Service{}, &diskServicePkg.Service{},
		&networkServicePkg.Service{}, &bootstrap.Service{}, &notificationsService.Service{}, &utilitiesServicePkg.Service{},
		&systemServicePkg.Service{}, &libvirt.Service{}, &sambaServicePkg.Service{Directory: &directoryservices.Service{}},
		&mdns.Service{}, &dynamicdns.Service{}, &certificates.Service{}, &iscsi.Service{}, &nfs.Service{},
		&jailServicePkg.Service{}, &lifecycle.Service{Container: &container.Service{}}, &cluster.Service{},
		&zelta.Service{}, &migration.Service{}, testutil.NewSQLiteTestDB(t))

	missing, stale := middleware.RouteRuleCoverage(r.Routes())
	for _, route := range missing {
		t.Errorf("no rule for %s", route)
	}
	for _, rule := range stale {
		t.Errorf("rule for a route that doesn't exist: %s", rule)
	}
}
