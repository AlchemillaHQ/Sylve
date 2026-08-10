// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

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

func jailRouteContractSources(t *testing.T) ([]byte, string) {
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

	return routesSource, handlerDir
}

func TestRegisteredJailRoutesCanBeMounted(t *testing.T) {
	routesSource, _ := jailRouteContractSources(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	pattern := regexp.MustCompile(`(?m)^\s*jail\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	matches := pattern.FindAllStringSubmatch(string(routesSource), -1)

	for _, match := range matches {
		router.Handle(match[1], "/jail"+match[2], func(*gin.Context) {})
	}
	if len(matches) != 43 {
		t.Errorf("unexpected Jail route total: registered=%d, want 43", len(matches))
	}
}

func TestRegisteredJailRoutesMatchSourceAnnotations(t *testing.T) {
	routesSource, handlerDir := jailRouteContractSources(t)
	pathParameter := regexp.MustCompile(`:([A-Za-z0-9_]+)`)
	registered := make(map[string]struct{})
	routePattern := regexp.MustCompile(`(?m)^\s*jail\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := pathParameter.ReplaceAllString("/jail"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/jail\S*) \[(get|post|put|patch|delete)\]$`)
	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list Jail handler sources: %v", err)
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
				t.Errorf("duplicate Jail @Router annotation: %s", key)
			}
			annotated[key] = struct{}{}
		}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered Jail route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("Jail source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 43 || len(annotated) != 43 {
		t.Errorf("unexpected Jail route totals: registered=%d annotated=%d, want 43 each", len(registered), len(annotated))
	}
}

func TestJailRouteMiddlewareAndSwaggerAuthorizationContract(t *testing.T) {
	routesSource, handlerDir := jailRouteContractSources(t)
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

	annotationPattern := regexp.MustCompile(`^// @Router (/jail\S*) \[(get|post|put|patch|delete)\]$`)
	bodyRoutes := map[string]struct{}{
		"POST /jail":                                  {},
		"POST /jail/bootstraps":                       {},
		"POST /jail/{ctid}/migrations":                {},
		"POST /jail/{ctid}/snapshots":                 {},
		"POST /jail/{ctid}/templates":                 {},
		"POST /jail/templates/{templateId}/jails":     {},
		"PATCH /jail/{ctid}/description":              {},
		"PATCH /jail/{ctid}/name":                     {},
		"PUT /jail/{ctid}/hardware/ram":               {},
		"PUT /jail/{ctid}/hardware/cpu":               {},
		"PUT /jail/{ctid}/hardware/resource-limits":   {},
		"PUT /jail/{ctid}/network/inheritance":        {},
		"POST /jail/{ctid}/networks":                  {},
		"PATCH /jail/{ctid}/networks/{networkId}":     {},
		"PUT /jail/{ctid}/options/wol":                {},
		"PUT /jail/{ctid}/options/boot-order":         {},
		"PUT /jail/{ctid}/options/fstab":              {},
		"PUT /jail/{ctid}/options/resolv-conf":        {},
		"PUT /jail/{ctid}/options/devfs-rules":        {},
		"PUT /jail/{ctid}/options/additional-options": {},
		"PUT /jail/{ctid}/options/allowed-options":    {},
		"PUT /jail/{ctid}/options/metadata":           {},
		"PUT /jail/{ctid}/options/lifecycle-hooks":    {},
	}
	documentedBodyRoutes := make(map[string]struct{}, len(bodyRoutes))
	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list Jail handler sources: %v", err)
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
			route := method + " " + match[1]
			if !strings.Contains(comment, "// @Security BearerAuth") {
				t.Errorf("%s %s is missing BearerAuth documentation", method, match[1])
			}
			if !strings.Contains(comment, `// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`) {
				t.Errorf("%s %s is missing its 401 response", method, match[1])
			}
			adminOnly := method != http.MethodGet || match[1] == "/jail/{ctid}/console"
			if adminOnly && !strings.Contains(comment, `// @Failure 403 {object} internal.APIResponse[any] "Forbidden"`) {
				t.Errorf("%s %s is missing its administrator-only 403 response", method, match[1])
			}
			if _, hasBody := bodyRoutes[route]; hasBody {
				documentedBodyRoutes[route] = struct{}{}
				if !strings.Contains(comment, "// @Failure 413 ") {
					t.Errorf("%s is missing its bounded-body 413 response", route)
				}
			}
			block = nil
		}
	}
	for route := range bodyRoutes {
		if _, documented := documentedBodyRoutes[route]; !documented {
			t.Errorf("body-bearing Jail route was not found in source annotations: %s", route)
		}
	}
}
