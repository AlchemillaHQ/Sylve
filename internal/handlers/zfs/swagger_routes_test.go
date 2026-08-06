// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRegisteredZFSRoutesMatchSourceAnnotations(t *testing.T) {
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
	routePattern := regexp.MustCompile(`(?m)^\s*(zfs|pools|datasets)\.(GET|POST|PATCH|DELETE)\("([^"]*)"`)
	prefixes := map[string]string{
		"zfs":      "/zfs",
		"pools":    "/zfs/pools",
		"datasets": "/zfs/datasets",
	}
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := prefixes[match[1]] + match[3]
		path = regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString(path, `{$1}`)
		registered[strings.ToUpper(match[2])+" "+path] = struct{}{}
	}

	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|patch|delete)\]$`)
	files, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list ZFS handler files: %v", err)
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
	if len(registered) != 29 || len(annotated) != 29 {
		t.Errorf("unexpected route totals: registered=%d annotated=%d, want 29 each", len(registered), len(annotated))
	}
}

func TestZFSRouterCommentBlocksHaveCoreAnnotations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "*.go"))
	if err != nil {
		t.Fatalf("list ZFS handler files: %v", err)
	}
	required := []string{
		"@Summary ",
		"@Description ",
		"@Tags ZFS",
		"@Produce json",
		"@Security BearerAuth",
		"@Success ",
		"@Failure 500 ",
	}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(source), "\n")
		for i, line := range lines {
			if !strings.HasPrefix(line, "// @Router /zfs/") {
				continue
			}
			start := i
			for start > 0 && strings.HasPrefix(lines[start-1], "//") {
				start--
			}
			block := strings.Join(lines[start:i+1], "\n")
			for _, annotation := range required {
				if !strings.Contains(block, annotation) {
					t.Errorf("%s:%d block for %q is missing %s", filepath.Base(path), i+1, line, annotation)
				}
			}
		}
	}
}
