// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/gin-gonic/gin"
)

func newTestAuthServiceForGroups(t *testing.T) *authService.Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Group{},
		&models.Token{},
		&models.SystemSecrets{},
		&models.BasicSettings{},
		&models.WebAuthnCredential{},
		&models.WebAuthnChallenge{},
		&models.PAMIdentity{},
	)

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "", nil
	}))

	return &authService.Service{DB: db}
}

func setupGroupRouter(svc *authService.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/groups", ListGroupsHandler(svc))
	r.POST("/auth/groups", CreateGroupHandler(svc))
	r.DELETE("/auth/groups/:id", DeleteGroupHandler(svc))
	r.POST("/auth/groups/users", AddUsersToGroupHandler(svc))
	r.PUT("/auth/groups/users", UpdateGroupMembersHandler(svc))
	return r
}

func performGroupJSON(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("failed to encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestListGroupsHandlerEmpty(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	router := setupGroupRouter(svc)

	w := performGroupJSON(t, router, "GET", "/auth/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp internal.APIResponse[[]models.Group]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status 'success', got: %s", resp.Status)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 groups, got: %d", len(resp.Data))
	}
}

func TestListGroupsHandlerWithGroups(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	svc.DB.Create(&models.Group{Name: "devs"})
	svc.DB.Create(&models.Group{Name: "ops"})
	router := setupGroupRouter(svc)

	w := performGroupJSON(t, router, "GET", "/auth/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp internal.APIResponse[[]models.Group]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 groups, got: %d", len(resp.Data))
	}
}

func TestCreateGroupHandlerMissingName(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	router := setupGroupRouter(svc)

	body := map[string]any{
		"members": []string{"alice"},
	}
	w := performGroupJSON(t, router, "POST", "/auth/groups", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateGroupHandlerMissingMembers(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	router := setupGroupRouter(svc)

	body := map[string]any{
		"name": "devs",
	}
	w := performGroupJSON(t, router, "POST", "/auth/groups", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteGroupHandlerMissingID(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	router := setupGroupRouter(svc)

	w := performGroupJSON(t, router, "DELETE", "/auth/groups/", nil)
	if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 404 or 400, got %d", w.Code)
	}
}

func TestDeleteGroupHandlerInvalidID(t *testing.T) {
	svc := newTestAuthServiceForGroups(t)
	router := setupGroupRouter(svc)

	w := performGroupJSON(t, router, "DELETE", "/auth/groups/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
