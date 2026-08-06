// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificateHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRegisteredCertificateRoutesMatchSourceAnnotations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	registered := make(map[string]struct{})
	routePattern := regexp.MustCompile(`(?m)^\s*certificateGroup\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := "/certificates" + match[2]
		path = regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString(path, `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|put|patch|delete)\]$`)
	files, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list certificate handler files: %v", err)
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
	if len(registered) != 10 || len(annotated) != 10 {
		t.Errorf("unexpected route totals: registered=%d annotated=%d, want 10 each", len(registered), len(annotated))
	}
}

func TestCertificateArchiveRouteRequiresLocalAdmin(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	routesSource, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	archiveRoute := regexp.MustCompile(`certificateGroup\.GET\("/:id/archive",\s*middleware\.RequireLocalAdmin\(authService\),\s*certificateHandlers\.Download\(certificateService\)\)`)
	if !archiveRoute.Match(routesSource) {
		t.Fatal("certificate archive GET route is not protected by explicit local-admin middleware")
	}
}
