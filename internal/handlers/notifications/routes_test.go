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

func notificationContractSources(t *testing.T) (string, string) {
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
	handlers, err := os.ReadFile(filepath.Join(dir, "notifications.go"))
	if err != nil {
		t.Fatalf("read notifications.go: %v", err)
	}
	return string(routes), string(handlers)
}

func TestNotificationRouteAndSwaggerContract(t *testing.T) {
	routes, handlers := notificationContractSources(t)
	expected := []struct {
		group, method, path, swaggerPath string
	}{
		{"notifications", "GET", "", "/notifications"},
		{"notifications", "GET", "/count", "/notifications/count"},
		{"notifications", "POST", "/:id/dismiss", "/notifications/{id}/dismiss"},
		{"notifications", "POST", "/dismiss-all", "/notifications/dismiss-all"},
		{"notificationSettings", "GET", "/transports", "/notifications/transports"},
		{"notificationSettings", "POST", "/transports", "/notifications/transports"},
		{"notificationSettings", "PUT", "/transports/:id", "/notifications/transports/{id}"},
		{"notificationSettings", "DELETE", "/transports/:id", "/notifications/transports/{id}"},
		{"notificationSettings", "POST", "/transports/:id/test", "/notifications/transports/{id}/test"},
		{"notificationSettings", "GET", "/rules", "/notifications/rules"},
		{"notificationSettings", "POST", "/rules", "/notifications/rules"},
		{"notificationSettings", "PUT", "/rules", "/notifications/rules"},
		{"notificationSettings", "PUT", "/rules/:id", "/notifications/rules/{id}"},
		{"notificationSettings", "DELETE", "/rules/:id", "/notifications/rules/{id}"},
		{"notificationSettings", "POST", "/rules/test", "/notifications/rules/test"},
		{"notificationSettings", "POST", "/rules/bulk-delete", "/notifications/rules/bulk-delete"},
		{"notificationSettings", "POST", "/rules/bulk-update", "/notifications/rules/bulk-update"},
	}

	for _, route := range expected {
		registration := route.group + "." + route.method + "(\"" + route.path + "\""
		if strings.Count(routes, registration) != 1 {
			t.Errorf("route registration count for %s %s=%d want=1", route.method, route.path, strings.Count(routes, registration))
		}
		annotation := "// @Router " + route.swaggerPath + " [" + strings.ToLower(route.method) + "]"
		if strings.Count(handlers, annotation) != 1 {
			t.Errorf("Swagger annotation count for %s %s=%d want=1", route.method, route.swaggerPath, strings.Count(handlers, annotation))
		}
	}

	if strings.Contains(routes, `notificationSettings.PUT("/transports"`) {
		t.Fatal("retired collection transport PUT remains registered")
	}
	if strings.Count(handlers, "// @Router /notifications") != len(expected) ||
		strings.Count(handlers, "// @Security BearerAuth") != len(expected) {
		t.Fatal("notification routes must each have one authenticated Swagger block")
	}
	if strings.Count(handlers, "// @Success 201") != 2 || strings.Count(handlers, "// @Failure 413") != 8 {
		t.Fatal("notification Swagger creation or request-limit outcomes are incomplete")
	}
	if strings.Contains(handlers, "/intra-cluster") {
		t.Fatal("notification source unexpectedly documents intra-cluster routes")
	}
}

func TestNotificationMiddlewareOrder(t *testing.T) {
	routes, _ := notificationContractSources(t)
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
