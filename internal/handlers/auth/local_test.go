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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
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

	// Prevent real system command execution during tests.
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "", nil
	}))

	return authService.NewAuthService(db).(*authService.Service)
}

func setupRouter(svc *authService.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/users", ListUsersHandler(svc))
	r.POST("/auth/users", CreateUserHandler(svc))
	r.POST("/auth/users/pam", CreatePamUserHandler(svc))
	r.PUT("/auth/users/:userId", EditUserHandler(svc))
	r.DELETE("/auth/users/:userId", DeleteUserHandler(svc))
	r.GET("/auth/users/uid/next", GetNextUIDHandler(svc))
	r.GET("/auth/users/capabilities", UserCapabilitiesHandler())
	r.POST("/auth/users/import", ImportUserHandler(svc))
	r.GET("/auth/users/importable", ListImportableUsersHandler(svc))
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

	var resp internal.APIResponse[[]PublicUser]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 users, got: %d", len(resp.Data))
	}
}

func TestListUsersHandlerUsesPublicRepresentation(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.Create(&models.User{
		Username: "user1",
		Password: "hashed",
		TOTP:     "private-seed",
		Admin:    true,
		Source:   "local",
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-seed") || strings.Contains(w.Body.String(), `"totp"`) {
		t.Fatalf("public user response exposed database-only authentication state: %s", w.Body.String())
	}

	var resp internal.APIResponse[[]PublicUser]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || !resp.Data[0].PasskeyEligible {
		t.Fatalf("expected a password-backed public user, got: %+v", resp.Data)
	}
}

func TestListUsersHandlerRejectsUnknownSource(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users?source=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Error != "invalid_user_source" {
		t.Fatalf("expected invalid_user_source, got: %s", resp.Error)
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

func TestCreateUserHandlerDoesNotReturnSubmittedPassword(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	const submittedPassword = "leaky"
	w := performJSON(t, router, "POST", "/auth/users", map[string]any{
		"username": "testuser",
		"password": submittedPassword,
		"admin":    false,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), submittedPassword) {
		t.Fatalf("response exposed submitted password: %s", w.Body.String())
	}
}

func TestEditUserHandlerInvalidID(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "testuser",
		"admin":    false,
	}
	w := performJSON(t, router, "PUT", "/auth/users/0", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditUserHandlerMissingAdmin(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "testuser",
	}
	w := performJSON(t, router, "PUT", "/auth/users/1", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditUserHandlerChangesAdminPassword(t *testing.T) {
	svc := newTestAuthService(t)
	admin := models.User{
		Username: "admin",
		Email:    "admin@sylve.local",
		Password: "old-hash",
		Admin:    true,
		Source:   "local",
	}
	if err := svc.DB.Create(&admin).Error; err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}
	router := setupRouter(svc)

	const newPassword = "new-admin-password"
	w := performJSON(t, router, http.MethodPut, "/auth/users/1", map[string]any{
		"username": "admin",
		"email":    "admin@sylve.local",
		"password": newPassword,
		"admin":    true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated models.User
	if err := svc.DB.First(&updated, admin.ID).Error; err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if updated.Password == "old-hash" || updated.Password == newPassword {
		t.Fatal("admin password was not safely updated")
	}
}

func TestEditUserHandlerNonExistentUser(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "testuser",
		"admin":    false,
	}
	w := performJSON(t, router, "PUT", "/auth/users/999", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Message != "user_not_found" {
		t.Fatalf("expected user_not_found, got: %s", resp.Message)
	}
}

func TestEditUserHandlerNewPrimaryGroupField(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "testuser", Password: "hashed"})
	router := setupRouter(svc)

	body := map[string]any{
		"username":        "testuser",
		"admin":           false,
		"newPrimaryGroup": true,
	}
	w := performJSON(t, router, "PUT", "/auth/users/1", body)

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
		"username":    "testuser",
		"admin":       false,
		"auxGroupIds": []uint{1},
	}
	w := performJSON(t, router, "PUT", "/auth/users/1", body)
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
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
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
		"auxGroupIds": [1, 2],
		"sambaAction": "upsert"
	}`

	var req EditUserRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("failed to unmarshal EditUserRequest: %v", err)
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
	if req.SambaAction != authService.SambaActionUpsert {
		t.Fatalf("expected SambaAction=upsert, got %q", req.SambaAction)
	}
}

func TestCreateUserHandlerSuccess(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	body := map[string]any{
		"username": "newuser",
		"password": "password123",
		"admin":    false,
	}
	w := performJSON(t, router, "POST", "/auth/users", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp internal.APIResponse[UserMutationResult]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "success" || resp.Data.ID == 0 || resp.Data.Username != "newuser" {
		t.Fatalf("unexpected create result: %+v", resp)
	}
}

func TestCreateUserHandlerDuplicateWithPAM(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "pamuser", Password: "hashed", Source: "pam"})
	router := setupRouter(svc)

	body := map[string]any{
		"username": "pamuser",
		"password": "password123",
		"admin":    false,
	}
	w := performJSON(t, router, "POST", "/auth/users", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Error != "user_source_conflict" {
		t.Fatalf("expected user_source_conflict, got: %s", resp.Error)
	}
}

func TestEditUserHandlerSuccess(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "editme", Password: "hashed", Source: "local"})
	router := setupRouter(svc)

	body := map[string]any{
		"username": "editme",
		"fullName": "New Name",
		"admin":    false,
	}
	w := performJSON(t, router, "PUT", "/auth/users/1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Status != "success" {
		t.Fatalf("expected status 'success', got: %s", resp.Status)
	}
}

func TestListUsersHandlerFilterBySource(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "local1", Password: "hashed", Source: "local"})
	svc.DB.Create(&models.User{Username: "local2", Password: "hashed", Source: "local"})
	svc.DB.Create(&models.User{Username: "pam1", Password: "hashed", Source: "pam"})
	router := setupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/users?source=local", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp1 internal.APIResponse[[]PublicUser]
	if err := json.Unmarshal(w.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp1.Data) != 2 {
		t.Fatalf("expected 2 local users, got: %d", len(resp1.Data))
	}

	w = performJSON(t, router, "GET", "/auth/users?source=pam", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp2 internal.APIResponse[[]PublicUser]
	if err := json.Unmarshal(w.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp2.Data) != 1 {
		t.Fatalf("expected 1 PAM user, got: %d", len(resp2.Data))
	}
}

func TestDeleteUserHandlerSuccess(t *testing.T) {
	svc := newTestAuthService(t)
	svc.DB.Create(&models.User{Username: "delete_me", Password: "hashed", Source: "local"})
	router := setupRouter(svc)

	w := performJSON(t, router, "DELETE", "/auth/users/1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp internal.APIResponse[UserMutationResult]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "success" || resp.Data.ID != 1 || resp.Data.Username != "delete_me" {
		t.Fatalf("unexpected delete result: %+v", resp)
	}

	var count int64
	svc.DB.Model(&models.User{}).Where("username = ?", "delete_me").Count(&count)
	if count != 0 {
		t.Fatalf("expected user to be deleted, got: %d rows", count)
	}
}

func TestCreatePamUserHandlerRequiresPassword(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupRouter(svc)

	w := performJSON(t, router, http.MethodPost, "/auth/users/pam", map[string]any{
		"username":     "pamuser",
		"admin":        false,
		"uid":          1001,
		"homeDirPerms": 493,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Password") {
		t.Fatalf("binding response exposed request internals: %s", w.Body.String())
	}
}

func TestImportUserHandlerMapsSourceConflict(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.Create(&models.User{
		Username: "existing",
		Password: "hashed",
		Source:   "local",
	}).Error; err != nil {
		t.Fatalf("seed local user: %v", err)
	}
	router := setupRouter(svc)

	w := performJSON(t, router, http.MethodPost, "/auth/users/import", map[string]any{
		"username": "existing",
		"admin":    false,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	response := decodeResponse(t, w)
	if response.Error != "user_source_conflict" {
		t.Fatalf("expected user_source_conflict, got %q", response.Error)
	}
}

func TestListImportableUsersHandlerReturnsUnixCandidateDTO(t *testing.T) {
	svc := newTestAuthService(t)
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		if command == "/usr/sbin/pw" {
			return "alice:*:1001:1001::0:0:Alice Example:/home/alice:/bin/sh\n", nil
		}
		return "", nil
	}))
	router := setupRouter(svc)

	w := performJSON(t, router, http.MethodGet, "/auth/users/importable", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response internal.APIResponse[[]authService.ImportableUnixUser]
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].GID != 1001 {
		t.Fatalf("unexpected importable users: %+v", response.Data)
	}
	if strings.Contains(w.Body.String(), `"id"`) || strings.Contains(w.Body.String(), `"source"`) {
		t.Fatalf("response fabricated database-user fields: %s", w.Body.String())
	}
}

func TestGetNextUIDHandlerReportsDiscoveryFailure(t *testing.T) {
	svc := newTestAuthService(t)
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "", errors.New("pw unavailable")
	}))
	router := setupRouter(svc)

	w := performJSON(t, router, http.MethodGet, "/auth/users/uid/next", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	response := decodeResponse(t, w)
	if response.Error != "unix_user_discovery_failed" {
		t.Fatalf("expected unix_user_discovery_failed, got %q", response.Error)
	}
	if strings.Contains(w.Body.String(), "pw unavailable") {
		t.Fatalf("response exposed dependency details: %s", w.Body.String())
	}
}
