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
	"testing"
)

func diskRoutesSource(t *testing.T) []byte {
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
	return source
}

func TestDiskGroupUsesWriteAuthorizationAndBodyLimit(t *testing.T) {
	routesSource := diskRoutesSource(t)
	for description, pattern := range map[string]string{
		"local-admin write authorization": `disk\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`,
		"request body limit":              `disk\.Use\(middleware\.LimitRequestBody\(diskServicePkg\.MaxRequestBodyBytes\)\)`,
	} {
		if !regexp.MustCompile(pattern).Match(routesSource) {
			t.Errorf("disk group is missing %s", description)
		}
	}
}
