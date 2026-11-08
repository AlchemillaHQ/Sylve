// SPDX-License-Identifier: BSD-2-Clause

package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Error codes and phase names reach the UI as raw strings, so the UI's message
// maps and phase labels have to follow the backend.
func TestUpdateAndPackageCodesHaveUIText(t *testing.T) {
	read := func(patterns ...string) string {
		var text strings.Builder
		for _, pattern := range patterns {
			files, err := filepath.Glob(filepath.Join("..", "..", pattern))
			if err != nil || len(files) == 0 {
				t.Fatalf("no files match %s: %v", pattern, err)
			}
			for _, file := range files {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				text.Write(data)
			}
		}
		return text.String()
	}
	found := func(pattern, text string) []string {
		var values []string
		for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
			values = append(values, match[1])
		}
		slices.Sort(values)
		return slices.Compact(values)
	}
	labelled := func(text, key string) bool {
		return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:`).MatchString(text)
	}
	errorCode := `(?:Errorf|errors\.New)\("([a-z][a-z0-9_]+)`
	hostMessages := read("web/src/lib/types/system/updates.ts", "web/src/lib/types/jail/packages.ts")
	jailMessages := read("web/src/lib/types/jail/packages.ts")
	for _, code := range found(errorCode, read("pkg/pkgmanager/*.go", "pkg/sylveupdate/*.go", "internal/services/hostupdates/*.go", "internal/handlers/system/updates.go")) {
		// Wrapped into package_audit_failed before it leaves pkgmanager.
		if code != "audit_diagnostics_present" && !labelled(hostMessages, code) {
			t.Errorf("host update error %s has no UI message", code)
		}
	}
	for _, code := range found(errorCode, read("pkg/pkgmanager/*.go", "internal/services/jail/packages*.go", "internal/handlers/jail/packages.go")) {
		// pkgmanager's host and Sylve checks never run for a jail target.
		if code == "audit_diagnostics_present" || strings.HasPrefix(code, "host_") || strings.HasPrefix(code, "sylve_") || strings.HasPrefix(code, "standalone_") {
			continue
		}
		if !labelled(jailMessages, code) {
			t.Errorf("jail package error %s has no UI message", code)
		}
	}
	hostLabels := read("web/src/lib/components/custom/System/Updates/Operation.svelte")
	for _, phase := range found(`(?:s\.phase\(op, |op\.Phase[^"\n]*= )"([a-z_]+)"`, read("internal/services/hostupdates/*.go")) {
		if !labelled(hostLabels, phase) {
			t.Errorf("host update phase %s has no UI label", phase)
		}
	}
	jailLabels := read("web/src/lib/components/custom/Jail/Packages/Operation.svelte")
	for _, phase := range found(`packagePhase\(&op, "([a-z_]+)"\)`, read("internal/services/jail/packages*.go")) {
		if !labelled(jailLabels, phase) {
			t.Errorf("jail package phase %s has no UI label", phase)
		}
	}
}
