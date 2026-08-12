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

func taskRoutesSource(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve task contract test path")
	}
	dir := filepath.Dir(filename)

	contents, err := os.ReadFile(filepath.Join(dir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	return string(contents)
}

func TestTaskMiddlewareOrder(t *testing.T) {
	routes := taskRoutesSource(t)
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
