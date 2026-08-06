// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRegisteredMdnsRoutesMatchSourceAnnotations(t *testing.T) {
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
	routePattern := regexp.MustCompile(`(?m)^\s*mdnsGroup\.(GET|POST|PUT|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := "/mdns" + match[2]
		path = regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString(path, `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|put|delete)\]$`)
	files, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list mDNS handler files: %v", err)
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
	if len(registered) != 6 || len(annotated) != 6 {
		t.Errorf("unexpected route totals: registered=%d annotated=%d, want 6 each", len(registered), len(annotated))
	}
}
