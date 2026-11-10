// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

func TestUserChangesNeedMoreThanUserModify(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	erin := models.User{Username: "erin", Source: "local"}
	admin := models.User{Username: "admin", Source: "local"}
	frank := models.User{Username: "frank", Source: "local"}
	paul := models.User{Username: "paul", Source: "pam"}
	records := []any{&erin, &admin, &frank, &paul, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLRole{Name: "UserManager", Privileges: []string{authService.UserModify}},
		&clusterModels.ACLGrant{Path: "/access", SubjectType: clusterModels.ACLSubjectUser, Subject: "erin@sylve", Role: "UserManager", Propagate: true},
	}
	for _, record := range records {
		if err := svc.DB.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("UserID", erin.ID) })
	router.POST("/auth/users/pam", CreatePamUserHandler(svc))
	router.POST("/auth/users/import", ImportUserHandler(svc))
	router.PUT("/auth/users/:userId", EditUserHandler(svc))
	router.DELETE("/auth/users/:userId", DeleteUserHandler(svc))
	router.POST("/auth/passkeys/register/begin", BeginPasskeyRegistrationHandler(svc))

	user := func(u models.User) string { return fmt.Sprintf("/auth/users/%d", u.ID) }
	for _, tt := range []struct {
		name, method, path string
		body               any
		want               string
	}{
		{"edit admin", http.MethodPut, user(admin), map[string]any{"username": "admin", "password": "takeover123"}, "superuser_required"},
		{"delete admin", http.MethodDelete, user(admin), nil, "superuser_required"},
		{"a passkey on admin", http.MethodPost, "/auth/passkeys/register/begin", map[string]any{"userId": admin.ID}, "superuser_required"},
		{"edit a PAM user", http.MethodPut, user(paul), map[string]any{"username": "paul", "doasEnabled": true}, "permission_denied"},
		{"delete a PAM user", http.MethodDelete, user(paul), nil, "permission_denied"},
		{"create a PAM user", http.MethodPost, "/auth/users/pam", map[string]any{"username": "mallory", "password": "password123"}, "permission_denied"},
		{"import a host account", http.MethodPost, "/auth/users/import", map[string]any{"username": "mallory"}, "permission_denied"},
	} {
		w := performJSON(t, router, tt.method, tt.path, tt.body)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), tt.want) {
			t.Errorf("%s: %d %s, want 403 %s", tt.name, w.Code, w.Body, tt.want)
		}
	}

	if w := performJSON(t, router, http.MethodPut, user(frank), map[string]any{"username": "frank", "fullName": "Frank"}); w.Code != http.StatusOK {
		t.Errorf("edit a Sylve user: %d %s", w.Code, w.Body)
	}
}

func TestUserListNeedsAuditOnAccessOrThisNode(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	auditor := models.User{Username: "auditor", Source: "local"}
	hostAdmin := models.User{Username: "hostadmin", Source: "local"}
	nobody := models.User{Username: "nobody", Source: "local"}
	records := []any{&auditor, &hostAdmin, &nobody, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLGrant{Path: "/access", SubjectType: clusterModels.ACLSubjectUser, Subject: "auditor@sylve", Role: "Auditor", Propagate: true},
		&clusterModels.ACLGrant{Path: middleware.NodePath(), SubjectType: clusterModels.ACLSubjectUser, Subject: "hostadmin@sylve", Role: "SysAdmin", Propagate: true},
	}
	for _, record := range records {
		if err := svc.DB.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		user models.User
		want int
	}{{auditor, http.StatusOK}, {hostAdmin, http.StatusOK}, {nobody, http.StatusForbidden}} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("UserID", tt.user.ID) })
		router.GET("/auth/users", ListUsersHandler(svc))
		if w := performJSON(t, router, http.MethodGet, "/auth/users", nil); w.Code != tt.want {
			t.Errorf("%s: %d %s, want %d", tt.user.Username, w.Code, w.Body, tt.want)
		}
	}
}

func TestCredentialChangesNeedEverythingTheTargetHolds(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	erin := models.User{Username: "erin", Source: "local"}
	frank := models.User{Username: "frank", Source: "local"}
	gina := models.User{Username: "gina", Source: "local"}
	records := []any{&erin, &frank, &gina, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLRole{Name: "UserManager", Privileges: []string{authService.UserModify}},
		&clusterModels.ACLGrant{Path: "/access", SubjectType: clusterModels.ACLSubjectUser, Subject: "erin@sylve", Role: "UserManager", Propagate: true},
		&clusterModels.ACLGrant{Path: "/vm", SubjectType: clusterModels.ACLSubjectUser, Subject: "gina@sylve", Role: "GuestUser", Propagate: true},
	}
	for _, record := range records {
		if err := svc.DB.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("UserID", erin.ID) })
	router.PUT("/auth/users/:userId", EditUserHandler(svc))
	router.POST("/auth/passkeys/register/begin", BeginPasskeyRegistrationHandler(svc))

	for _, tt := range []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"a password for a user with no grants", http.MethodPut, fmt.Sprintf("/auth/users/%d", frank.ID),
			map[string]any{"username": "frank", "password": "password123"}, http.StatusOK},
		{"a password for a user holding more", http.MethodPut, fmt.Sprintf("/auth/users/%d", gina.ID),
			map[string]any{"username": "gina", "password": "password123"}, http.StatusForbidden},
		{"a passkey on a user holding more", http.MethodPost, "/auth/passkeys/register/begin",
			map[string]any{"userId": gina.ID}, http.StatusForbidden},
		{"her name, not her password", http.MethodPut, fmt.Sprintf("/auth/users/%d", gina.ID),
			map[string]any{"username": "gina", "fullName": "Gina"}, http.StatusOK},
	} {
		w := performJSON(t, router, tt.method, tt.path, tt.body)
		if w.Code != tt.want || (tt.want == http.StatusForbidden && !strings.Contains(w.Body.String(), `"path":"/vm"`)) {
			t.Errorf("%s: %d %s, want %d", tt.name, w.Code, w.Body, tt.want)
		}
	}
}
