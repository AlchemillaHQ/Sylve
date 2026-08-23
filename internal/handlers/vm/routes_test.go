// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func vmRoutesSource(t *testing.T) []byte {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve VM route contract test path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	return routesSource
}

func TestVMRouteMiddlewareOrderAndConsoleAuthorization(t *testing.T) {
	routesSource := vmRoutesSource(t)
	source := string(routesSource)

	authIndex := strings.Index(source, "vm.Use(middleware.EnsureAuthenticated(authService))")
	adminIndex := strings.Index(source, "vm.Use(middleware.RequireLocalAdminForWrites(authService))")
	hostIndex := strings.Index(source, "vm.Use(EnsureCorrectHost(db, authService))")
	limitIndex := strings.Index(source, "vm.Use(middleware.LimitRequestBody(libvirt.MaxRequestBodyBytes))")
	loggerIndex := strings.Index(source, "vm.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	if authIndex < 0 || adminIndex < 0 || hostIndex < 0 || limitIndex < 0 || loggerIndex < 0 ||
		!(authIndex < adminIndex && adminIndex < hostIndex && hostIndex < limitIndex && limitIndex < loggerIndex) {
		t.Error("VM middleware must be ordered as authentication, write authorization, selected-node routing, body limit, and request logging")
	}
	if !strings.Contains(source, `vm.GET("/:rid/console", middleware.RequireLocalAdmin(authService),`) {
		t.Error("VM console route is missing its route-specific administrator check")
	}
}
