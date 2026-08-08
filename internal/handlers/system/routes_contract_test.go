// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type systemRouteContract struct {
	registration string
	method       string
	ginPath      string
	swaggerPath  string
	adminOnly    bool
}

var finalSystemRouteContracts = []systemRouteContract{
	{
		registration: `systemJSON.GET("/pci-devices",`,
		method:       http.MethodGet,
		ginPath:      "/system/pci-devices",
		swaggerPath:  "/system/pci-devices",
	},
	{
		registration: `systemJSON.GET("/ppt-devices",`,
		method:       http.MethodGet,
		ginPath:      "/system/ppt-devices",
		swaggerPath:  "/system/ppt-devices",
	},
	{
		registration: `systemJSON.POST("/ppt-devices",`,
		method:       http.MethodPost,
		ginPath:      "/system/ppt-devices",
		swaggerPath:  "/system/ppt-devices",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.POST("/ppt-devices/prepare",`,
		method:       http.MethodPost,
		ginPath:      "/system/ppt-devices/prepare",
		swaggerPath:  "/system/ppt-devices/prepare",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.POST("/ppt-devices/import",`,
		method:       http.MethodPost,
		ginPath:      "/system/ppt-devices/import",
		swaggerPath:  "/system/ppt-devices/import",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.DELETE("/ppt-devices/:id",`,
		method:       http.MethodDelete,
		ginPath:      "/system/ppt-devices/:id",
		swaggerPath:  "/system/ppt-devices/{id}",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.GET("/basic-settings",`,
		method:       http.MethodGet,
		ginPath:      "/system/basic-settings",
		swaggerPath:  "/system/basic-settings",
	},
	{
		registration: `systemJSON.PUT("/basic-settings/pools",`,
		method:       http.MethodPut,
		ginPath:      "/system/basic-settings/pools",
		swaggerPath:  "/system/basic-settings/pools",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.PATCH("/basic-settings/services/:service",`,
		method:       http.MethodPatch,
		ginPath:      "/system/basic-settings/services/:service",
		swaggerPath:  "/system/basic-settings/services/{service}",
		adminOnly:    true,
	},
	{
		registration: `systemJSON.GET("/tunables/remote",`,
		method:       http.MethodGet,
		ginPath:      "/system/tunables/remote",
		swaggerPath:  "/system/tunables/remote",
	},
	{
		registration: `systemJSON.PUT("/tunables",`,
		method:       http.MethodPut,
		ginPath:      "/system/tunables",
		swaggerPath:  "/system/tunables",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerCore.GET("",`,
		method:       http.MethodGet,
		ginPath:      "/system/file-explorer",
		swaggerPath:  "/system/file-explorer",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerCore.POST("",`,
		method:       http.MethodPost,
		ginPath:      "/system/file-explorer",
		swaggerPath:  "/system/file-explorer",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerCore.POST("/delete",`,
		method:       http.MethodPost,
		ginPath:      "/system/file-explorer/delete",
		swaggerPath:  "/system/file-explorer/delete",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerCore.POST("/rename",`,
		method:       http.MethodPost,
		ginPath:      "/system/file-explorer/rename",
		swaggerPath:  "/system/file-explorer/rename",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerCore.POST("/copy-or-move-batch",`,
		method:       http.MethodPost,
		ginPath:      "/system/file-explorer/copy-or-move-batch",
		swaggerPath:  "/system/file-explorer/copy-or-move-batch",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerTransfer.GET("/download",`,
		method:       http.MethodGet,
		ginPath:      "/system/file-explorer/download",
		swaggerPath:  "/system/file-explorer/download",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerTransfer.POST("/upload",`,
		method:       http.MethodPost,
		ginPath:      "/system/file-explorer/upload",
		swaggerPath:  "/system/file-explorer/upload",
		adminOnly:    true,
	},
	{
		registration: `fileExplorerRevert.DELETE("/upload",`,
		method:       http.MethodDelete,
		ginPath:      "/system/file-explorer/upload",
		swaggerPath:  "/system/file-explorer/upload",
		adminOnly:    true,
	},
}

func readSystemHandlerSources(t *testing.T) string {
	t.Helper()

	var source strings.Builder
	for _, path := range []string{
		"device-passthrough.go",
		"settings.go",
		"tunables.go",
		"file-explorer.go",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		source.Write(contents)
		source.WriteString("\n\n")
	}
	return source.String()
}

func swaggerOperationBlock(t *testing.T, source, annotation string) string {
	t.Helper()

	annotationIndex := strings.Index(source, annotation)
	if annotationIndex < 0 {
		t.Fatalf("missing Swagger annotation %q", annotation)
	}

	blockStart := strings.LastIndex(source[:annotationIndex], "\n\n")
	if blockStart < 0 {
		blockStart = 0
	} else {
		blockStart += 2
	}

	return source[blockStart : annotationIndex+len(annotation)]
}

func TestFinalSystemRouteAndSwaggerContract(t *testing.T) {
	routeSource, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	systemStart := strings.Index(string(routeSource), `system := api.Group("/system")`)
	systemEnd := strings.Index(string(routeSource), `vm := api.Group("/vm")`)
	if systemStart < 0 || systemEnd <= systemStart {
		t.Fatal("could not isolate the /system route block")
	}
	systemRoutesSource := string(routeSource[systemStart:systemEnd])
	routeRegistrationPattern := regexp.MustCompile(
		`(?:systemJSON|fileExplorerCore|fileExplorerTransfer|fileExplorerRevert)\.(?:GET|POST|PUT|PATCH|DELETE)\(`,
	)
	if got, want := len(routeRegistrationPattern.FindAllString(systemRoutesSource, -1)), len(finalSystemRouteContracts); got != want {
		t.Fatalf("registered /system routes=%d want=%d", got, want)
	}

	handlerSource := readSystemHandlerSources(t)
	annotationPattern := regexp.MustCompile(`(?m)^// @Router /system\S* \[(?:get|post|put|patch|delete)\]$`)
	if got, want := len(annotationPattern.FindAllString(handlerSource, -1)), len(finalSystemRouteContracts); got != want {
		t.Fatalf("/system Swagger annotations=%d want=%d", got, want)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	noOp := func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	}

	for _, contract := range finalSystemRouteContracts {
		if count := strings.Count(systemRoutesSource, contract.registration); count != 1 {
			t.Errorf("route registration %q count=%d want=1", contract.registration, count)
		}

		annotation := "// @Router " + contract.swaggerPath + " [" + strings.ToLower(contract.method) + "]"
		if count := strings.Count(handlerSource, annotation); count != 1 {
			t.Errorf("Swagger annotation %q count=%d want=1", annotation, count)
			continue
		}

		operationBlock := swaggerOperationBlock(t, handlerSource, annotation)
		if !strings.Contains(operationBlock, "// @Security BearerAuth") {
			t.Errorf("%s %s is missing BearerAuth", contract.method, contract.swaggerPath)
		}
		if !strings.Contains(operationBlock, "// @Failure 401 ") {
			t.Errorf("%s %s is missing 401", contract.method, contract.swaggerPath)
		}
		if contract.adminOnly && !strings.Contains(operationBlock, "// @Failure 403 ") {
			t.Errorf("%s %s is missing 403", contract.method, contract.swaggerPath)
		}

		router.Handle(contract.method, "/api"+contract.ginPath, noOp)
	}
}
