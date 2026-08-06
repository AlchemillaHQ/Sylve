// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func diskRoutesSource(t *testing.T) ([]byte, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	source, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	return source, handlerDir
}

func TestRegisteredDiskRoutesCanBeMounted(t *testing.T) {
	routesSource, _ := diskRoutesSource(t)
	router := gin.New()
	pattern := regexp.MustCompile(`(?m)^\s*disk\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range pattern.FindAllStringSubmatch(string(routesSource), -1) {
		router.Handle(match[1], "/disk"+match[2], func(*gin.Context) {})
	}
}

func TestRegisteredDiskRoutesMatchSourceAnnotations(t *testing.T) {
	routesSource, handlerDir := diskRoutesSource(t)
	routePattern := regexp.MustCompile(`(?m)^\s*disk\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	registered := make(map[string]struct{})
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString("/disk"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|put|patch|delete)\]$`)
	files, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list disk handler files: %v", err)
	}
	for _, path := range files {
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
				t.Errorf("duplicate @Router annotation %s", key)
			}
			annotated[key] = struct{}{}
		}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 12 || len(annotated) != 12 {
		t.Errorf("unexpected route totals: registered=%d annotated=%d, want 12 each", len(registered), len(annotated))
	}
}

func TestDiskGroupUsesWriteAuthorizationAndBodyLimit(t *testing.T) {
	routesSource, _ := diskRoutesSource(t)
	for description, pattern := range map[string]string{
		"local-admin write authorization": `disk\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`,
		"request body limit":              `disk\.Use\(middleware\.LimitRequestBody\(diskServicePkg\.MaxRequestBodyBytes\)\)`,
	} {
		if !regexp.MustCompile(pattern).Match(routesSource) {
			t.Errorf("disk group is missing %s", description)
		}
	}
}
