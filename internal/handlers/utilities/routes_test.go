// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func utilitiesRouteContractSources(t *testing.T) ([]byte, string) {
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
	return routesSource, handlerDir
}

func registeredUtilitiesRoutes(t *testing.T, routesSource []byte) map[string]string {
	t.Helper()

	registered := make(map[string]string)
	routePattern := regexp.MustCompile(`(?m)^\s*(?:utilities|utilitiesJSON)\.(GET|POST|PUT|PATCH|DELETE)\(\s*"([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := "/utilities" + match[2]
		registered[match[1]+" "+regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString(path, `{$1}`)] = path
	}

	const publicRoute = `api.GET("/utilities/downloads/:uuid", EnsurePublicDownloadHost(db), utilitiesHandlers.DownloadFileFromSignedURL(utilitiesService))`
	if !strings.Contains(string(routesSource), publicRoute) {
		t.Fatal("public signed-download route is missing or unexpectedly protected")
	}
	registered["GET /utilities/downloads/{uuid}"] = "/utilities/downloads/:uuid"
	return registered
}

func TestRegisteredUtilitiesRoutesCanBeMounted(t *testing.T) {
	routesSource, _ := utilitiesRouteContractSources(t)
	registered := registeredUtilitiesRoutes(t, routesSource)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	contracts := make([]string, 0, len(registered))
	for contract := range registered {
		contracts = append(contracts, contract)
	}
	sort.Slice(contracts, func(i, j int) bool {
		leftPath := registered[contracts[i]]
		rightPath := registered[contracts[j]]
		leftWildcard := strings.Contains(leftPath, ":")
		rightWildcard := strings.Contains(rightPath, ":")
		if leftWildcard != rightWildcard {
			return !leftWildcard
		}
		return contracts[i] < contracts[j]
	})
	for _, contract := range contracts {
		path := registered[contract]
		method, _, ok := strings.Cut(contract, " ")
		if !ok {
			t.Fatalf("invalid route contract %q", contract)
		}
		router.Handle(method, path, func(*gin.Context) {})
	}
	if len(registered) != 16 {
		t.Errorf("unexpected Utilities route total: registered=%d, want 16", len(registered))
	}
}

func TestRegisteredUtilitiesRoutesMatchSourceAnnotations(t *testing.T) {
	routesSource, handlerDir := utilitiesRouteContractSources(t)
	registered := registeredUtilitiesRoutes(t, routesSource)
	annotated := make(map[string]struct{})
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (/utilities\S*) \[(get|post|put|patch|delete)\]$`)

	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list Utilities handler sources: %v", err)
	}
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
				t.Errorf("duplicate Utilities @Router annotation: %s", key)
			}
			annotated[key] = struct{}{}
		}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered Utilities route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("Utilities source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 16 || len(annotated) != 16 {
		t.Errorf("unexpected Utilities route totals: registered=%d annotated=%d, want 16 each", len(registered), len(annotated))
	}
}

func TestUtilitiesMiddlewareAndSwaggerAuthorizationContract(t *testing.T) {
	routesSource, handlerDir := utilitiesRouteContractSources(t)
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

	bodyRoutes := map[string]struct{}{
		"POST /utilities/downloader-uploads":               {},
		"POST /utilities/downloader-uploads/{id}/complete": {},
		"POST /utilities/downloads":                        {},
		"PATCH /utilities/downloads/{id}":                  {},
		"POST /utilities/downloads/bulk-delete":            {},
		"POST /utilities/downloads/signed-url":             {},
		"POST /utilities/cloud-init/templates":             {},
		"PUT /utilities/cloud-init/templates/{templateId}": {},
	}
	annotationPattern := regexp.MustCompile(`^// @Router (/utilities\S*) \[(get|post|put|patch|delete)\]$`)
	handlerSources, err := filepath.Glob(filepath.Join(handlerDir, "*.go"))
	if err != nil {
		t.Fatalf("list Utilities handler sources: %v", err)
	}
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
			key := method + " " + match[1]
			public := key == "GET /utilities/downloads/{uuid}"
			if public {
				if strings.Contains(comment, "// @Security BearerAuth") {
					t.Errorf("%s must be capability-authorized rather than Bearer-authenticated", key)
				}
			} else {
				if !strings.Contains(comment, "// @Security BearerAuth") {
					t.Errorf("%s is missing BearerAuth documentation", key)
				}
				if !strings.Contains(comment, `// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`) {
					t.Errorf("%s is missing its 401 response", key)
				}
				if method != http.MethodGet && !strings.Contains(comment, "// @Failure 403") {
					t.Errorf("%s is missing its administrator-only 403 response", key)
				}
			}
			if _, hasBody := bodyRoutes[key]; hasBody && !strings.Contains(comment, "// @Failure 413") {
				t.Errorf("%s is missing its bounded-body 413 response", key)
			}
			block = nil
		}
	}
}
