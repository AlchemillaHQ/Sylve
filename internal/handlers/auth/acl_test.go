// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

func TestGrantAPI(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{Username: "admin", Source: "local"}
	alice := models.User{Username: "alice", Source: "local"}
	for _, record := range []any{&admin, &alice, &models.Migrations{Name: "rbac_admin_grants"}} {
		if err := svc.DB.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	actor := &admin
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("UserID", actor.ID) })
	router.GET("/auth/grants", ListGrantsHandler(svc))
	router.POST("/auth/grants", SetGrantHandler(svc))
	router.DELETE("/auth/grants", DeleteGrantHandler(svc))
	router.GET("/auth/permissions", PermissionsHandler(svc))

	grant := clusterModels.ACLGrant{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser", Propagate: true}
	if w := performJSON(t, router, http.MethodPost, "/auth/grants", grant); w.Code != http.StatusOK {
		t.Fatalf("set grant: %d %s", w.Code, w.Body)
	}
	bad := grant
	bad.Path = "/vm/100/extra"
	if w := performJSON(t, router, http.MethodPost, "/auth/grants", bad); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid grant: %d %s", w.Code, w.Body)
	}

	listed := func() []clusterModels.ACLGrant {
		t.Helper()
		var response internal.APIResponse[[]clusterModels.ACLGrant]
		w := performJSON(t, router, http.MethodGet, "/auth/grants", nil)
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK {
			t.Fatalf("list grants: %d %v", w.Code, err)
		}
		return response.Data
	}
	if grants := listed(); len(grants) != 1 || grants[0].Subject != "alice@sylve" || !grants[0].Propagate {
		t.Fatalf("grants = %+v", grants)
	}

	actor = &alice
	var permissions internal.APIResponse[PermissionsResponse]
	w := performJSON(t, router, http.MethodGet, "/auth/permissions", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &permissions); err != nil || w.Code != http.StatusOK {
		t.Fatalf("permissions: %d %v", w.Code, err)
	}
	if permissions.Data.Superuser || !slices.Contains(permissions.Data.Paths["/vm/100"].Privileges, authService.GuestPowerMgmt) ||
		len(permissions.Data.Paths["/"].Privileges) != 0 {
		t.Fatalf("permissions = %+v", permissions.Data)
	}

	actor = &admin
	if w := performJSON(t, router, http.MethodDelete, "/auth/grants", grant); w.Code != http.StatusOK {
		t.Fatalf("delete grant: %d %s", w.Code, w.Body)
	}
	if grants := listed(); len(grants) != 0 {
		t.Fatalf("grants after delete = %+v", grants)
	}
}

func TestGrantChangesStayWithinWhatTheGranterHolds(t *testing.T) {
	svc := newTestAuthService(t)
	if err := svc.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	dave := models.User{Username: "dave", Source: "local"}
	records := []any{&dave, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLRole{Name: "Delegator", Privileges: []string{authService.PermissionsModify, authService.GuestAudit}},
		&clusterModels.ACLRole{Name: "Viewer", Privileges: []string{authService.GuestAudit}},
		&clusterModels.ACLGrant{Path: "/vm", SubjectType: clusterModels.ACLSubjectUser, Subject: "dave@sylve", Role: "Delegator", Propagate: true},
		&clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "erin@sylve", Role: "Viewer", Propagate: true},
	}
	for _, record := range records {
		if err := svc.DB.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("UserID", dave.ID) })
	router.POST("/auth/grants", SetGrantHandler(svc))
	router.DELETE("/auth/grants", DeleteGrantHandler(svc))

	grant := func(path, subject, role string) clusterModels.ACLGrant {
		return clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: subject, Role: role, Propagate: true}
	}
	for _, tt := range []struct {
		name   string
		method string
		grant  clusterModels.ACLGrant
		want   int
	}{
		{"a role within what dave holds", http.MethodPost, grant("/vm/127", "erin@sylve", "Viewer"), http.StatusOK},
		{"taking access away", http.MethodPost, grant("/vm/127", "erin@sylve", authService.RoleNoAccess), http.StatusOK},
		{"a role with privileges dave lacks", http.MethodPost, grant("/vm/127", "erin@sylve", "GuestUser"), http.StatusForbidden},
		{"Administrator on / to himself", http.MethodPost, grant("/", "dave@sylve", authService.RoleAdministrator), http.StatusForbidden},
		{"a path outside /vm", http.MethodPost, grant("/storage/tank", "erin@sylve", "Viewer"), http.StatusForbidden},
		{"removing a grant under /vm", http.MethodDelete, grant("/vm/127", "erin@sylve", "Viewer"), http.StatusOK},
		{"removing a grant on /", http.MethodDelete, grant("/", "erin@sylve", "Viewer"), http.StatusForbidden},
	} {
		if w := performJSON(t, router, tt.method, "/auth/grants", tt.grant); w.Code != tt.want {
			t.Errorf("%s: %d %s, want %d", tt.name, w.Code, w.Body, tt.want)
		}
	}
}
