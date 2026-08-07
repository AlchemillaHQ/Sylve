// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type networkObjectHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupNetworkObjectHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&models.BasicSettings{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ObjectListSnapshot{},
	)
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	objects := router.Group("/network/object")
	if bodyLimit > 0 {
		objects.Use(middleware.LimitRequestBody(bodyLimit))
	}
	objects.GET("", ListNetworkObjects(svc))
	objects.POST("", CreateNetworkObject(svc))
	objects.DELETE("", BulkDeleteNetworkObjects(svc))
	objects.DELETE("/:id", DeleteNetworkObject(svc))
	objects.PUT("/:id", EditNetworkObject(svc))
	return router, db
}

func decodeNetworkObjectHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) networkObjectHandlerResponse {
	t.Helper()
	var response networkObjectHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func TestCreateNetworkObjectReturnsCreatedID(t *testing.T) {
	router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", []byte(`{
		"name": "web-ports",
		"type": "Port",
		"values": ["80", "443"]
	}`))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	response := decodeNetworkObjectHandlerResponse(t, rr)
	if response.Status != "success" || response.Message != "object_created" {
		t.Fatalf("unexpected create response: %+v", response)
	}
	var id uint
	if err := json.Unmarshal(response.Data, &id); err != nil || id == 0 {
		t.Fatalf("expected positive created ID, data=%s err=%v", response.Data, err)
	}
}

func TestNetworkObjectHandlersMapStableClientErrors(t *testing.T) {
	t.Run("invalid object", func(t *testing.T) {
		router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", []byte(`{
			"name": "bad-type",
			"type": "Unknown",
			"values": ["value"]
		}`))
		response := decodeNetworkObjectHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_network_object_type" {
			t.Fatalf("expected stable 400 invalid-type response, status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
		body := []byte(`{"name":"duplicate","type":"Port","values":["443"]}`)
		if rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", body); rr.Code != http.StatusCreated {
			t.Fatalf("seed create failed: status=%d body=%s", rr.Code, rr.Body.String())
		}
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", body)
		response := decodeNetworkObjectHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "network_object_name_conflict" {
			t.Fatalf("expected stable 409 duplicate response, status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("missing object", func(t *testing.T) {
		router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/object/999999", nil)
		response := decodeNetworkObjectHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "network_object_not_found" {
			t.Fatalf("expected stable 404 response, status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("upstream list failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()

		router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
		body, err := json.Marshal(map[string]any{
			"name":   "unavailable-list",
			"type":   "List",
			"values": []string{server.URL},
		})
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", body)
		response := decodeNetworkObjectHandlerResponse(t, rr)
		if rr.Code != http.StatusBadGateway || response.Error != "network_object_source_unavailable" {
			t.Fatalf("expected stable 502 response, status=%d response=%+v", rr.Code, response)
		}
		if strings.Contains(rr.Body.String(), "503") || strings.Contains(rr.Body.String(), "unavailable-list") {
			t.Fatalf("response leaked upstream/runtime detail: %s", rr.Body.String())
		}
	})
}

func TestNetworkObjectHandlersRejectInvalidIDs(t *testing.T) {
	for _, id := range []string{"0", "-1", "not-a-number"} {
		t.Run(id, func(t *testing.T) {
			router, _ := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
			rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/object/"+id, nil)
			response := decodeNetworkObjectHandlerResponse(t, rr)
			if rr.Code != http.StatusBadRequest || response.Error != "invalid_network_object_id" {
				t.Fatalf("expected stable 400 invalid-ID response, status=%d response=%+v", rr.Code, response)
			}
		})
	}
}

func TestNetworkObjectHandlersRejectOversizedJSON(t *testing.T) {
	const limit = 128
	router, _ := setupNetworkObjectHandlerRouter(t, limit)
	body := []byte(`{"name":"` + strings.Repeat("a", limit) + `","type":"Port","values":["443"]}`)
	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/object", body)
	response := decodeNetworkObjectHandlerResponse(t, rr)
	if rr.Code != http.StatusRequestEntityTooLarge || response.Error != "network_object_request_too_large" {
		t.Fatalf("expected stable 413 response, status=%d response=%+v", rr.Code, response)
	}
}

func TestBulkDeleteNetworkObjectsUsesCollectionDelete(t *testing.T) {
	router, db := setupNetworkObjectHandlerRouter(t, networkService.MaxRequestBodyBytes)
	objects := []networkModels.Object{
		{Name: "bulk-one", Type: "Port", Entries: []networkModels.ObjectEntry{{Value: "80"}}},
		{Name: "bulk-two", Type: "Port", Entries: []networkModels.ObjectEntry{{Value: "443"}}},
	}
	if err := db.Create(&objects).Error; err != nil {
		t.Fatalf("seed objects: %v", err)
	}
	body, err := json.Marshal(map[string]any{"ids": []uint{objects[0].ID, objects[1].ID}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/object", body)
	response := decodeNetworkObjectHandlerResponse(t, rr)
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "objects_deleted" {
		t.Fatalf("unexpected bulk-delete response: status=%d response=%+v", rr.Code, response)
	}
	var count int64
	if err := db.Model(&networkModels.Object{}).Count(&count).Error; err != nil {
		t.Fatalf("count objects: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected collection DELETE to remove both objects, count=%d", count)
	}
}

func TestRegisteredNetworkObjectRoutesMatchSourceAnnotations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	handlerSource, err := os.ReadFile(filepath.Join(handlerDir, "object.go"))
	if err != nil {
		t.Fatalf("read object.go: %v", err)
	}

	registered := map[string]struct{}{}
	routePattern := regexp.MustCompile(`(?m)^\s*objects\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString("/network/object"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := map[string]struct{}{}
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|put|patch|delete)\]$`)
	for _, match := range annotationPattern.FindAllStringSubmatch(string(handlerSource), -1) {
		annotated[strings.ToUpper(match[2])+" "+match[1]] = struct{}{}
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
	if len(registered) != 5 || len(annotated) != 5 {
		t.Fatalf("unexpected route totals: registered=%d annotated=%d, want 5 each", len(registered), len(annotated))
	}
}

func TestNetworkObjectRoutesUseWriteAuthorizationAndPreLoggerBodyLimit(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	routesSource, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	if !regexp.MustCompile(`objects\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`).Match(routesSource) {
		t.Error("network object routes are missing local-admin write authorization")
	}
	limitIndex := strings.Index(string(routesSource), "network.Use(middleware.LimitRequestBody(networkServicePkg.MaxRequestBodyBytes))")
	loggerIndex := strings.Index(string(routesSource), "network.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	correctHostIndex := strings.Index(string(routesSource), "network.Use(EnsureCorrectHost(db, authService))")
	if correctHostIndex < 0 || limitIndex < 0 || correctHostIndex > limitIndex {
		t.Error("selected-node proxy middleware must run before local network body processing")
	}
	if limitIndex < 0 || loggerIndex < 0 || limitIndex > loggerIndex {
		t.Error("network request-body limit must be installed before request logging")
	}

	diskLimitIndex := strings.Index(string(routesSource), "disk.Use(middleware.LimitRequestBody(diskServicePkg.MaxRequestBodyBytes))")
	diskLoggerIndex := strings.Index(string(routesSource), "disk.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	if diskLimitIndex < 0 || diskLoggerIndex < 0 || diskLimitIndex > diskLoggerIndex {
		t.Error("disk request-body limit must be installed before request logging")
	}
}
