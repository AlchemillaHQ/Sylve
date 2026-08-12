// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func utilitiesRoutesSource(t *testing.T) []byte {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Utilities route contract test path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	return routesSource
}

func TestUtilitiesMiddlewareOrderAndUploadLogging(t *testing.T) {
	routesSource := utilitiesRoutesSource(t)
	source := string(routesSource)
	authIndex := strings.Index(source, `utilities.Use(middleware.EnsureAuthenticated(authService))`)
	adminIndex := strings.Index(source, `utilities.Use(middleware.RequireLocalAdminForWrites(authService))`)
	hostIndex := strings.Index(source, `utilities.Use(EnsureCorrectHost(db, authService))`)
	jsonIndex := strings.Index(source, `utilitiesJSON := utilities.Group("")`)
	limitIndex := strings.Index(source, `utilitiesJSON.Use(middleware.LimitRequestBody(utilitiesServicePkg.MaxRequestBodyBytes))`)
	loggerIndex := strings.Index(source, `utilitiesJSON.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))`)
	if authIndex < 0 || adminIndex < 0 || hostIndex < 0 || jsonIndex < 0 || limitIndex < 0 || loggerIndex < 0 ||
		!(authIndex < adminIndex && adminIndex < hostIndex && hostIndex < jsonIndex && jsonIndex < limitIndex && limitIndex < loggerIndex) {
		t.Error("Utilities middleware must be ordered as authentication, write authorization, selected-node routing, JSON body limit, and request logging")
	}

	stagePattern := regexp.MustCompile(`(?s)utilities\.POST\(\s*"/downloader-uploads",\s*middleware\.RequestLoggerMiddleware\(telemetryDB, authService\),\s*utilitiesHandlers\.UploadDownloaderFile`)
	if !stagePattern.MatchString(source) {
		t.Error("multipart downloader staging must retain request logging without the Utilities JSON body limit")
	}
}
