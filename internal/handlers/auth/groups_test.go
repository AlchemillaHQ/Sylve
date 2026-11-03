// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	authService "github.com/alchemillahq/sylve/internal/services/auth"

	"github.com/alchemillahq/sylve/internal/db/models"

	"github.com/gin-gonic/gin"
)

func setupGroupRouter(svc *authService.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/groups", ListGroupsHandler(svc))
	r.POST("/auth/groups", CreateGroupHandler(svc))
	r.DELETE("/auth/groups/:groupId", DeleteGroupHandler(svc))
	r.PUT("/auth/groups/:groupId/members", UpdateGroupMembersHandler(svc))
	return r
}

func TestListGroupsHandlerEmpty(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupGroupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp internal.APIResponse[[]PublicGroup]
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
	svc := newTestAuthService(t)
	svc.DB.Create(&models.Group{Name: "devs"})
	svc.DB.Create(&models.Group{Name: "ops"})
	router := setupGroupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp internal.APIResponse[[]PublicGroup]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 groups, got: %d", len(resp.Data))
	}
}

func TestListGroupsHandlerUsesPublicNestedUsers(t *testing.T) {
	svc := newTestAuthService(t)
	user := models.User{Username: "alice", Password: "hashed", TOTP: "private-seed", Source: "local"}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	group := models.Group{Name: "devs"}
	if err := svc.DB.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := svc.DB.Model(&group).Association("Users").Append(&user); err != nil {
		t.Fatalf("associate user: %v", err)
	}
	router := setupGroupRouter(svc)

	w := performJSON(t, router, "GET", "/auth/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-seed") || strings.Contains(w.Body.String(), `"totp"`) {
		t.Fatalf("nested public user exposed database-only authentication state: %s", w.Body.String())
	}

	var resp internal.APIResponse[[]PublicGroup]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Users) != 1 || resp.Data[0].Users[0].Username != "alice" {
		t.Fatalf("unexpected public group response: %+v", resp.Data)
	}
}

func TestCreateGroupHandlerMissingName(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupGroupRouter(svc)

	body := map[string]any{
		"members": []string{"alice"},
	}
	w := performJSON(t, router, "POST", "/auth/groups", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateGroupHandlerMissingMembers(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupGroupRouter(svc)

	body := map[string]any{
		"name": "devs",
	}
	w := performJSON(t, router, "POST", "/auth/groups", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteGroupHandlerInvalidID(t *testing.T) {
	svc := newTestAuthService(t)
	router := setupGroupRouter(svc)

	w := performJSON(t, router, "DELETE", "/auth/groups/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
