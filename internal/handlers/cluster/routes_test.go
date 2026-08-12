// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package clusterHandlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func clusterRoutesSource(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve cluster contract test path")
	}
	dir := filepath.Dir(filename)

	contents, err := os.ReadFile(filepath.Join(dir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	return string(contents)
}

func TestClusterMiddlewareOrder(t *testing.T) {
	routes := clusterRoutesSource(t)
	for _, snippets := range [][]string{
		{
			`clusterAdmission.Use(middleware.AuthenticateClusterKey(authService))`,
			`clusterAdmission.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))`,
			`clusterAdmission.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
		},
		{
			`clusterLocal.Use(middleware.EnsureAuthenticated(authService))`,
			`clusterLocal.Use(middleware.RequireLocalSession())`,
			`clusterLocal.Use(middleware.RequireLocalAdmin(authService))`,
			`clusterLocal.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))`,
			`clusterLocal.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
		},
		{
			`cluster.Use(middleware.EnsureAuthenticated(authService))`,
			`cluster.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))`,
			`cluster.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`,
		},
	} {
		previous := -1
		for _, snippet := range snippets {
			index := strings.Index(routes, snippet)
			if index < 0 || index <= previous {
				t.Fatalf("cluster middleware is missing or out of order: %s", snippet)
			}
			previous = index
		}
	}
}
