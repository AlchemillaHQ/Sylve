// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package taskHandlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func taskContractSources(t *testing.T) (string, string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve task contract test path")
	}
	dir := filepath.Dir(filename)

	read := func(path string) string {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(contents)
	}

	return read(filepath.Join(dir, "..", "routes.go")),
		read(filepath.Join(dir, "lifecycle.go")),
		read(filepath.Join(dir, "..", "migration", "migration.go"))
}

func TestTaskRouteAndSwaggerContract(t *testing.T) {
	routes, lifecycleHandlers, migrationHandlers := taskContractSources(t)
	expected := []struct {
		registration, annotation string
		handlers                 string
	}{
		{`lifecycleTasks.GET("/active",`, "// @Router /tasks/lifecycle/active [get]", lifecycleHandlers},
		{`lifecycleTasks.GET("/active/:guestType/:guestId",`, "// @Router /tasks/lifecycle/active/{guestType}/{guestId} [get]", lifecycleHandlers},
		{`lifecycleTasks.GET("/recent",`, "// @Router /tasks/lifecycle/recent [get]", lifecycleHandlers},
		{`migrationTasks.GET("/validate",`, "// @Router /tasks/migration/validate [get]", migrationHandlers},
		{`migrationTasks.POST("/:taskId/cancel",`, "// @Router /tasks/migration/{taskId}/cancel [post]", migrationHandlers},
	}

	for _, route := range expected {
		if strings.Count(routes, route.registration) != 1 {
			t.Errorf("route registration count for %q = %d, want 1", route.registration, strings.Count(routes, route.registration))
		}
		if strings.Count(route.handlers, route.annotation) != 1 {
			t.Errorf("Swagger annotation count for %q = %d, want 1", route.annotation, strings.Count(route.handlers, route.annotation))
		}
	}

	if strings.Contains(routes, `migrationTasks.POST("/cancel/:taskId"`) {
		t.Fatal("retired migration cancellation route remains registered")
	}
	if strings.Count(lifecycleHandlers, "// @Security BearerAuth") != 3 ||
		strings.Count(migrationHandlers, "// @Router /tasks/") != 2 {
		t.Fatal("task routes must each have one authenticated source Swagger block")
	}
}

func TestTaskMiddlewareOrder(t *testing.T) {
	routes, _, _ := taskContractSources(t)
	snippets := []string{
		`tasks.Use(middleware.EnsureAuthenticated(authService))`,
		`tasks.Use(middleware.RequireLocalAdminForWrites(authService))`,
		`tasks.Use(EnsureCorrectHost(db, authService))`,
		`tasks.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
	}
	previous := -1
	for _, snippet := range snippets {
		index := strings.Index(routes, snippet)
		if index < 0 || index <= previous {
			t.Fatalf("task middleware is missing or out of order: %s", snippet)
		}
		previous = index
	}
}
