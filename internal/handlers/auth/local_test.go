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
	"github.com/gin-gonic/gin"
)

func newTestAuthService(t *testing.T) *authService.Service {
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
	return &authService.Service{DB: db}
}

func setupRouter(svc *authService.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/users", ListUsersHandler(svc))
	r.POST("/auth/users", CreateUserHandler(svc))
	r.PUT("/auth/users", EditUserHandler(svc))
	r.DELETE("/auth/users/:id", DeleteUserHandler(svc))
	r.GET("/auth/users/uid/next", GetNextUIDHandler(svc))
	r.GET("/auth/users/capabilities", UserCapabilitiesHandler())
	return r
}

func performJSON(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
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

type apiResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Data    any    `json:"data"`
}

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

func TestListUsersHandlerEmpty(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp := decodeResponse(t, w)
	if resp.Status != "success" {
		t.Fatalf("expected status 'success', got: %s", resp.Status)
	}
}

func TestListUsersHandlerWithUsers(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "user1", Password: "hashed"})
	svc.DB.Create(&models.User{Username: "user2", Password: "hashed"})
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp internal.APIResponse[[]models.User]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 users, got: %d", len(resp.Data))
	}
}

func TestCreateUserHandlerMissingUsername(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"password": "password123",
		"admin":    false,
	}
	w := performJSON(t, router, "POST", "/auth/users", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateUserHandlerMissingAdmin(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "testuser",
		"password": "password123",
	}
	w := performJSON(t, router, "POST", "/auth/users", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateUserHandlerShortUsername(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "ab",
		"password": "password123",
		"admin":    false,
	}
	w := performJSON(t, router, "POST", "/auth/users", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short username, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditUserHandlerMissingID(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "testuser",
		"admin":    false,
	}
	w := performJSON(t, router, "PUT", "/auth/users", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditUserHandlerMissingAdmin(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"id":       1,
		"username": "testuser",
	}
	w := performJSON(t, router, "PUT", "/auth/users", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditUserHandlerNonExistentUser(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"id":       999,
		"username": "testuser",
		"admin":    false,
	}
	w := performJSON(t, router, "PUT", "/auth/users", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Message != "failed_to_edit_user" {
		t.Fatalf("expected failed_to_edit_user, got: %s", resp.Message)
	}
}

func TestEditUserHandlerNewPrimaryGroupField(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "testuser", Password: "hashed"})
	router := setupRouter(svc)

	body := map[string]any{
		"id":              1,
		"username":        "testuser",
		"admin":           false,
		"newPrimaryGroup": true,
	}
	w := performJSON(t, router, "PUT", "/auth/users", body)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("newPrimaryGroup should be accepted in request body, got 400: %s", w.Body.String())
	}
}

func TestEditUserHandlerAuxGroupIDs(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "testuser", Password: "hashed"})
	svc.DB.Create(&models.Group{Name: "dev_group"})
	router := setupRouter(svc)

	body := map[string]any{
		"id":          1,
		"username":    "testuser",
		"admin":       false,
		"auxGroupIds": []uint{1},
	}
	w := performJSON(t, router, "PUT", "/auth/users", body)
	if w.Code == http.StatusBadRequest {
		t.Fatalf("auxGroupIds should be accepted, got 400: %s", w.Body.String())
	}
}

func TestDeleteUserHandlerMissingID(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "DELETE", "/auth/users/", nil)
	if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 404 or 400, got %d", w.Code)
	}
}

func TestDeleteUserHandlerInvalidID(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "DELETE", "/auth/users/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteUserHandlerNonExistentUser(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "DELETE", "/auth/users/999", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUserCapabilitiesHandler(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users/capabilities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			DoasAvailable bool `json:"doasAvailable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status 'success', got: %s", resp.Status)
	}
}

func TestCreateUserRequestFields(t *testing.T) {
	raw := `{
		"username": "testuser",
		"fullName": "Test User",
		"password": "password123",
		"email": "test@example.com",
		"admin": true,
		"uid": 1001,
		"shell": "/bin/sh",
		"homeDirectory": "/home/test",
		"homeDirPerms": 493,
		"sshPublicKey": "ssh-rsa AAAA...",
		"disablePassword": false,
		"locked": false,
		"doasEnabled": true,
		"newPrimaryGroup": true,
		"primaryGroupId": 1,
		"auxGroupIds": [1, 2]
	}`

	var req CreateUserRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("failed to unmarshal CreateUserRequest: %v", err)
	}
	if req.Username != "testuser" {
		t.Fatalf("expected username 'testuser', got: %s", req.Username)
	}
	if !req.NewPrimaryGroup {
		t.Fatalf("expected NewPrimaryGroup=true")
	}
	if len(req.AuxGroupIDs) != 2 {
		t.Fatalf("expected 2 aux group IDs, got: %d", len(req.AuxGroupIDs))
	}
}

func TestEditUserRequestFields(t *testing.T) {
	raw := `{
		"id": 1,
		"username": "testuser",
		"fullName": "Test User",
		"password": "password123",
		"email": "test@example.com",
		"admin": true,
		"uid": 1001,
		"shell": "/bin/sh",
		"homeDirectory": "/home/test",
		"homeDirPerms": 493,
		"sshPublicKey": "ssh-rsa AAAA...",
		"disablePassword": false,
		"locked": false,
		"doasEnabled": true,
		"newPrimaryGroup": true,
		"primaryGroupId": 1,
		"auxGroupIds": [1, 2]
	}`

	var req EditUserRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("failed to unmarshal EditUserRequest: %v", err)
	}
	if req.ID != 1 {
		t.Fatalf("expected ID 1, got: %d", req.ID)
	}
	if !req.NewPrimaryGroup {
		t.Fatalf("expected NewPrimaryGroup=true")
	}
	if len(req.AuxGroupIDs) != 2 {
		t.Fatalf("expected 2 aux group IDs, got: %d", len(req.AuxGroupIDs))
	}
	if req.PrimaryGroupID == nil || *req.PrimaryGroupID != 1 {
		t.Fatalf("expected PrimaryGroupID=1")
	}
}
