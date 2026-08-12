// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func jailRoutesSource(t *testing.T) []byte {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Jail route contract test path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	return routesSource
}

func TestJailRouteMiddlewareOrderAndConsoleAuthorization(t *testing.T) {
	routesSource := jailRoutesSource(t)
	source := string(routesSource)

	authIndex := strings.Index(source, "jail.Use(middleware.EnsureAuthenticated(authService))")
	adminIndex := strings.Index(source, "jail.Use(middleware.RequireLocalAdminForWrites(authService))")
	hostIndex := strings.Index(source, "jail.Use(EnsureCorrectHost(db, authService))")
	limitIndex := strings.Index(source, "jail.Use(middleware.LimitRequestBody(jailServicePkg.MaxRequestBodyBytes))")
	loggerIndex := strings.Index(source, "jail.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	if authIndex < 0 || adminIndex < 0 || hostIndex < 0 || limitIndex < 0 || loggerIndex < 0 ||
		!(authIndex < adminIndex && adminIndex < hostIndex && hostIndex < limitIndex && limitIndex < loggerIndex) {
		t.Error("Jail middleware must be ordered as authentication, write authorization, selected-node routing, body limit, and request logging")
	}
	consoleAdmin := regexp.MustCompile(`jail\.GET\("/:ctid/console",\s*middleware\.RequireLocalAdmin\(authService\),`)
	if !consoleAdmin.MatchString(source) {
		t.Error("Jail console route is missing its route-specific administrator check")
	}
}
