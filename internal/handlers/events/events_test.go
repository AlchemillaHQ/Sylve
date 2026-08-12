// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package eventsHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestEventsStreamRouteAndSwaggerContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve events test path")
	}
	dir := filepath.Dir(filename)

	read := func(path string) string {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(contents)
	}

	handlerSource := read(filepath.Join(dir, "events.go"))
	routesSource := read(filepath.Join(dir, "..", "routes.go"))
	start := strings.Index(handlerSource, "// @Summary Subscribe to server-sent events")
	end := strings.Index(handlerSource, "func StreamSSE()")
	if start < 0 || end <= start {
		t.Fatal("SSE source Swagger block is missing")
	}
	streamBlock := handlerSource[start:end]

	for _, required := range []string{
		"// @Produce text/event-stream",
		`// @Param sse_token query string true`,
		"// @Success 200",
		"// @Failure 401",
		"// @Failure 500",
		"// @Router /events/stream [get]",
	} {
		if !strings.Contains(streamBlock, required) {
			t.Errorf("SSE source Swagger block is missing %q", required)
		}
	}
	if strings.Contains(streamBlock, "// @Security BearerAuth") {
		t.Fatal("SSE stream must not advertise ordinary Bearer authentication")
	}
	if strings.Count(routesSource, `events.GET("/stream",`) != 1 {
		t.Fatal("SSE stream route must be registered exactly once")
	}
}

func TestCreateSSETokenCreatesLocalScopedCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.SystemSecrets{})
	if err := db.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "test-secret"}).Error; err != nil {
		t.Fatalf("create JWT secret: %v", err)
	}
	service := &authService.Service{DB: db}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("AuthScope", "local")
	ctx.Set("UserID", uint(7))
	ctx.Set("Username", "admin")
	ctx.Set("AuthType", "sylve")

	CreateSSEToken(service)(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q want=no-store", recorder.Header().Get("Cache-Control"))
	}
	var response internal.APIResponse[CreateSSETokenResponse]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Token == "" || response.Data.ExpiresIn != 600 {
		t.Fatalf("unexpected SSE token response: %+v", response.Data)
	}
	claims, err := service.ValidateScopedJWT(response.Data.Token, "sse")
	if err != nil {
		t.Fatalf("validate scoped token: %v", err)
	}
	if claims.UserID != 7 || claims.Username != "admin" || claims.AuthType != "sylve" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.ExpiresAt.IsZero() || time.Until(claims.ExpiresAt) > 10*time.Minute {
		t.Fatalf("unexpected scoped expiry: %v", claims.ExpiresAt)
	}
}

func TestCreateSSETokenRejectsClusterScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("AuthScope", "cluster")

	CreateSSEToken(nil)(ctx)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestStreamSSEClosesAtAuthenticatedTokenExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.SSEExpiresAtContextKey, time.Now().Add(50*time.Millisecond))
		c.Next()
	})
	router.GET("/events/stream", StreamSSE())

	started := time.Now()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events/stream", nil))
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stream ignored authenticated expiry: %v", elapsed)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, "event: reconnect") ||
		!strings.Contains(body, "token_rotation") {
		t.Fatalf("stream did not request reconnect at expiry: %s", body)
	}
}
