// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package notificationsHandlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func notificationRoutesSource(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve notification contract test path")
	}
	dir := filepath.Dir(filename)
	routes, err := os.ReadFile(filepath.Join(dir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	return string(routes)
}

func TestNotificationMiddlewareOrder(t *testing.T) {
	routes := notificationRoutesSource(t)
	assertNotificationOrder(t, routes,
		`notifications.Use(middleware.EnsureAuthenticated(authService))`,
		`notifications.Use(middleware.RequireLocalAdminForWrites(authService))`,
		`notifications.Use(EnsureCorrectHost(db, authService))`,
		`notifications.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
	)
	assertNotificationOrder(t, routes,
		`notificationSettings.Use(middleware.EnsureAuthenticated(authService))`,
		`notificationSettings.Use(middleware.RequireLocalAdmin(authService))`,
		`notificationSettings.Use(EnsureCorrectHost(db, authService))`,
		`notificationSettings.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))`,
		`notificationSettings.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
	)
}

func assertNotificationOrder(t *testing.T, source string, snippets ...string) {
	t.Helper()
	previous := -1
	for _, snippet := range snippets {
		index := strings.Index(source, snippet)
		if index < 0 || index <= previous {
			t.Fatalf("notification middleware is missing or out of order: %s", snippet)
		}
		previous = index
	}
}
