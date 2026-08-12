// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestISCSIGroupUsesWriteAuthorizationAndBodyLimit(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	routesSource, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	for description, pattern := range map[string]string{
		"local-admin write authorization": `iscsiGroup\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`,
		"request body limit":              `iscsiGroup\.Use\(middleware\.LimitRequestBody\(iscsi\.MaxRequestBodyBytes\)\)`,
	} {
		if !regexp.MustCompile(pattern).Match(routesSource) {
			t.Errorf("iSCSI group is missing %s", description)
		}
	}
}
