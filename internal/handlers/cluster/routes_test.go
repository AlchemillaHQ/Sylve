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

func clusterContractSources(t *testing.T) (string, string, string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve cluster contract test path")
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
		read(filepath.Join(dir, "cluster.go")),
		read(filepath.Join(dir, "detail.go")),
		read(filepath.Join(dir, "notes.go"))
}

func clusterSwaggerBlock(t *testing.T, source, annotation string) string {
	t.Helper()
	end := strings.Index(source, annotation)
	if end < 0 {
		t.Fatalf("missing Swagger annotation %q", annotation)
	}
	start := strings.LastIndex(source[:end], "// @Summary")
	if start < 0 {
		t.Fatalf("missing Swagger summary before %q", annotation)
	}
	return source[start : end+len(annotation)]
}

func requireSwaggerMarkers(t *testing.T, block string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(block, marker) {
			t.Errorf("Swagger block missing %q:\n%s", marker, block)
		}
	}
}

func TestClusterRouteAndSwaggerContract(t *testing.T) {
	routes, core, detail, notes := clusterContractSources(t)
	expected := []struct {
		registration string
		annotation   string
		source       string
	}{
		{`cluster.GET("",`, "// @Router /cluster [get]", core},
		{`cluster.GET("/nodes",`, "// @Router /cluster/nodes [get]", detail},
		{`cluster.GET("/resources",`, "// @Router /cluster/resources [get]", detail},
		{`clusterLocal.GET("/join-key",`, "// @Router /cluster/join-key [get]", core},
		{`clusterLocal.POST("",`, "// @Router /cluster [post]", core},
		{`clusterLocal.POST("/join",`, "// @Router /cluster/join [post]", core},
		{`clusterAdmission.POST("/accept-join",`, "// @Router /cluster/accept-join [post]", core},
		{`clusterLocal.DELETE("/reset-node",`, "// @Router /cluster/reset-node [delete]", core},
		{`clusterAdmin.POST("/resync-state",`, "// @Router /cluster/resync-state [post]", core},
		{`clusterNotes.GET("",`, "// @Router /cluster/notes [get]", notes},
		{`clusterNotes.POST("",`, "// @Router /cluster/notes [post]", notes},
		{`clusterNotes.PUT("/:id",`, "// @Router /cluster/notes/{id} [put]", notes},
		{`clusterNotes.DELETE("/:id",`, "// @Router /cluster/notes/{id} [delete]", notes},
		{`clusterNotes.POST("/bulk-delete",`, "// @Router /cluster/notes/bulk-delete [post]", notes},
	}

	for _, route := range expected {
		if strings.Count(routes, route.registration) != 1 {
			t.Errorf("route registration count for %q=%d want=1", route.registration, strings.Count(routes, route.registration))
		}
		if strings.Count(route.source, route.annotation) != 1 {
			t.Errorf("Swagger annotation count for %q=%d want=1", route.annotation, strings.Count(route.source, route.annotation))
		}
	}

	for _, annotation := range []string{
		"// @Router /cluster [get]",
		"// @Router /cluster/nodes [get]",
		"// @Router /cluster/resources [get]",
	} {
		source := core
		if strings.Contains(annotation, "/nodes") || strings.Contains(annotation, "/resources") {
			source = detail
		}
		requireSwaggerMarkers(t, clusterSwaggerBlock(t, source, annotation), "// @Failure 401", "// @Failure 403")
	}

	requireSwaggerMarkers(t,
		clusterSwaggerBlock(t, core, "// @Router /cluster/accept-join [post]"),
		"// @Security ClusterKeyAuth", "// @Failure 403", "// @Failure 413",
	)
	resetBlock := clusterSwaggerBlock(t, core, "// @Router /cluster/reset-node [delete]")
	if strings.Contains(resetBlock, "// @Failure 400") {
		t.Fatal("reset-node Swagger block advertises an unreachable 400 response")
	}
	requireSwaggerMarkers(t,
		clusterSwaggerBlock(t, core, "// @Router /cluster/resync-state [post]"),
		"// @Success 200 {object} internal.APIResponse[cluster.ClusterStateResyncResult]",
		"// @Failure 409 {object} internal.APIResponse[cluster.ClusterStateResyncResult]",
	)

	requireSwaggerMarkers(t,
		clusterSwaggerBlock(t, notes, "// @Router /cluster/notes [get]"),
		"// @Failure 401", "// @Failure 403", "// @Failure 500",
	)
	requireSwaggerMarkers(t,
		clusterSwaggerBlock(t, notes, "// @Router /cluster/notes [post]"),
		"// @Success 201", "// @Failure 400", "// @Failure 401", "// @Failure 403",
		"// @Failure 409", "// @Failure 413", "// @Failure 500", "// @Failure 503",
	)
	for _, annotation := range []string{
		"// @Router /cluster/notes/{id} [put]",
		"// @Router /cluster/notes/bulk-delete [post]",
	} {
		requireSwaggerMarkers(t, clusterSwaggerBlock(t, notes, annotation),
			"// @Failure 400", "// @Failure 401", "// @Failure 403", "// @Failure 404",
			"// @Failure 409", "// @Failure 413", "// @Failure 500", "// @Failure 503",
		)
	}
	requireSwaggerMarkers(t,
		clusterSwaggerBlock(t, notes, "// @Router /cluster/notes/{id} [delete]"),
		"// @Failure 400", "// @Failure 401", "// @Failure 403", "// @Failure 404",
		"// @Failure 409", "// @Failure 500", "// @Failure 503",
	)
}

func TestClusterMiddlewareOrder(t *testing.T) {
	routes, _, _, _ := clusterContractSources(t)
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
