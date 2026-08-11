// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package eventsHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

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
