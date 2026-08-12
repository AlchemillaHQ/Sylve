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
	"testing"
)

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
