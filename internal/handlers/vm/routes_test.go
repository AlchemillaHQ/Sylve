// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func vmRouteContractSources(t *testing.T) ([]byte, string) {
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

	return routesSource, handlerDir
}

func TestRegisteredVMRoutesCanBeMounted(t *testing.T) {
	routesSource, _ := vmRouteContractSources(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	pattern := regexp.MustCompile(`(?m)^\s*vm\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)

	for _, match := range pattern.FindAllStringSubmatch(string(routesSource), -1) {
		router.Handle(match[1], "/vm"+match[2], func(*gin.Context) {})
	}
}

func TestRegisteredVMRoutesMatchSourceAnnotations(t *testing.T) {
	routesSource, handlerDir := vmRouteContractSources(t)
	pathParameter := regexp.MustCompile(`:([A-Za-z0-9_]+)`)
	registered := make(map[string]struct{})
	routePattern := regexp.MustCompile(`(?m)^\s*vm\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := pathParameter.ReplaceAllString("/vm"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/vm\S*) \[(get|post|put|patch|delete)\]$`)
	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list VM handler sources: %v", err)
	}
	handlerSources = append(handlerSources, filepath.Join(handlerDir, "..", "migration", "migration.go"))

	for _, path := range handlerSources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range annotationPattern.FindAllStringSubmatch(string(source), -1) {
			key := strings.ToUpper(match[2]) + " " + match[1]
			if _, duplicate := annotated[key]; duplicate {
				t.Errorf("duplicate VM @Router annotation: %s", key)
			}
			annotated[key] = struct{}{}
		}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered VM route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("VM source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 47 || len(annotated) != 47 {
		t.Errorf("unexpected VM route totals: registered=%d annotated=%d, want 47 each", len(registered), len(annotated))
	}
}

func TestVMRouteMiddlewareAndSwaggerAuthorizationContract(t *testing.T) {
	routesSource, handlerDir := vmRouteContractSources(t)
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

	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/vm\S*) \[(get|post|put|patch|delete)\]$`)
	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list VM handler sources: %v", err)
	}
	handlerSources = append(handlerSources, filepath.Join(handlerDir, "..", "migration", "migration.go"))

	for _, path := range handlerSources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		var block []string
		for _, line := range strings.Split(string(contents), "\n") {
			if strings.HasPrefix(line, "// @Summary ") {
				block = []string{line}
				continue
			}
			if block == nil {
				continue
			}
			block = append(block, line)
			match := annotationPattern.FindStringSubmatch(line)
			if match == nil {
				continue
			}

			comment := strings.Join(block, "\n")
			method := strings.ToUpper(match[2])
			if !strings.Contains(comment, "// @Security BearerAuth") {
				t.Errorf("%s %s is missing BearerAuth documentation", method, match[1])
			}
			if !strings.Contains(comment, `// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`) {
				t.Errorf("%s %s is missing its 401 response", method, match[1])
			}
			adminOnly := method != http.MethodGet || match[1] == "/vm/{rid}/console"
			if adminOnly && !strings.Contains(comment, `// @Failure 403 {object} internal.APIResponse[any] "Forbidden"`) {
				t.Errorf("%s %s is missing its administrator-only 403 response", method, match[1])
			}
			block = nil
		}
	}
}
