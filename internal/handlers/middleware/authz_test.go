// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestAuthorizeChecksTheRouteRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &authSvc.Service{DB: testutil.NewSQLiteTestDB(t, &models.Migrations{})}
	const intra = "/api/intra-cluster/raft/apply"
	tests := []struct {
		name, scope, caller, method, route, request string
		want                                        int
	}{
		{"session on a guest route", "local", "", http.MethodGet, "/api/vm/:rid", "/api/vm/100", http.StatusOK},
		{"forwarded user on a guest route", "cluster", authSvc.ClusterCallerUser, http.MethodGet, "/api/vm/:rid", "/api/vm/100", http.StatusOK},
		{"session on a route without a rule", "local", "", http.MethodGet, "/api/vm/:rid/unknown", "/api/vm/100/unknown", http.StatusForbidden},
		{"session on an intra-cluster route", "local", "", http.MethodPost, intra, intra, http.StatusForbidden},
		{"forwarded user on an intra-cluster route", "cluster", authSvc.ClusterCallerUser, http.MethodPost, intra, intra, http.StatusForbidden},
		{"member node on an intra-cluster route", "cluster", authSvc.ClusterCallerNode, http.MethodPost, intra, intra, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Handle(tt.method, tt.route, func(c *gin.Context) {
				c.Set("AuthScope", tt.scope)
				c.Set("ClusterCaller", tt.caller)
				if authorize(c, service) {
					c.Status(http.StatusOK)
				}
			})
			if status := testutil.PerformRequest(t, router, tt.method, tt.request, nil, nil).Code; status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

func TestAuthorizeWithGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{}, &models.Migrations{},
		&clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	service := &authSvc.Service{DB: db}
	alice := models.User{Username: "alice", Source: "local"}
	root := models.User{Username: "root", Source: "pam"}
	for _, record := range []any{&alice, &root, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLGrant{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser", Propagate: true},
		&clusterModels.ACLGrant{Path: "/vm/200", SubjectType: clusterModels.ACLSubjectGroup, Subject: "ops", Role: "GuestUser", Propagate: true},
		&clusterModels.ACLGroup{Name: "ops", Members: []string{"alice@sylve"}},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	session := func(user models.User) func(*gin.Context) {
		return func(c *gin.Context) {
			c.Set("AuthScope", "local")
			c.Set("UserID", user.ID)
		}
	}
	forwarded := func(realm string) func(*gin.Context) {
		return func(c *gin.Context) {
			c.Set("AuthScope", "cluster")
			c.Set("ClusterCaller", authSvc.ClusterCallerUser)
			c.Set(principalKey, authSvc.ForwardedPrincipal(authSvc.ProxiedUser{Username: "alice", AuthType: "sylve", Realm: realm}))
		}
	}
	tests := []struct {
		name                   string
		as                     func(*gin.Context)
		method, route, request string
		want                   int
	}{
		{"own guest", session(alice), http.MethodGet, "/api/vm/:rid", "/api/vm/100", http.StatusOK},
		{"other guest", session(alice), http.MethodGet, "/api/vm/:rid", "/api/vm/101", http.StatusForbidden},
		{"privilege the role has", session(alice), http.MethodPost, "/api/vm/:rid/actions/:action", "/api/vm/100/actions/start", http.StatusOK},
		{"privilege the role lacks", session(alice), http.MethodPut, "/api/vm/:rid/hardware/cpu", "/api/vm/100/hardware/cpu", http.StatusForbidden},
		{"host terminal", session(alice), http.MethodGet, "/api/info/terminal", "/api/info/terminal", http.StatusForbidden},
		{"superuser-only route", session(alice), http.MethodPost, "/api/cluster", "/api/cluster", http.StatusForbidden},
		{"superuser", session(root), http.MethodPost, "/api/cluster", "/api/cluster", http.StatusOK},
		{"forwarded with the realm", forwarded(authSvc.RealmSylve), http.MethodGet, "/api/vm/:rid", "/api/vm/100", http.StatusOK},
		{"forwarded from another realm", forwarded(authSvc.RealmPAM), http.MethodGet, "/api/vm/:rid", "/api/vm/100", http.StatusForbidden},
		{"forwarded group member", forwarded(authSvc.RealmSylve), http.MethodGet, "/api/vm/:rid", "/api/vm/200", http.StatusOK},
		{"forwarded from another realm, not a member", forwarded(authSvc.RealmPAM), http.MethodGet, "/api/vm/:rid", "/api/vm/200", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Handle(tt.method, tt.route, func(c *gin.Context) {
				tt.as(c)
				if authorize(c, service) {
					c.Status(http.StatusOK)
				}
			})
			if status := testutil.PerformRequest(t, router, tt.method, tt.request, nil, nil).Code; status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

func TestVisibleKeepsWhatTheUserMayAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Migrations{},
		&clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	service := &authSvc.Service{DB: db}
	alice := models.User{Username: "alice", Source: "local"}
	for _, record := range []any{&alice, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLGrant{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser", Propagate: true},
		&clusterModels.ACLGrant{Path: "/storage/tank", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "DatastoreUser", Propagate: true},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	visible := func(caller string, privilege string, paths ...string) []string {
		var got []string
		router := gin.New()
		router.GET("/", func(c *gin.Context) {
			c.Set("UserID", alice.ID)
			c.Set("ClusterCaller", caller)
			kept, ok := Visible(c, service, paths, privilege, func(path string) string { return path })
			if !ok {
				t.Fatal("check failed")
			}
			got = kept
		})
		testutil.PerformRequest(t, router, http.MethodGet, "/", nil, nil)
		return got
	}

	if got := visible("", authSvc.GuestAudit, "/vm/100", "/vm/101", "/jail/100"); !slices.Equal(got, []string{"/vm/100"}) {
		t.Fatalf("guests = %v, want only /vm/100", got)
	}
	if got := visible("", authSvc.DatastoreAudit, "/storage/tank", "/storage/zroot"); !slices.Equal(got, []string{"/storage/tank"}) {
		t.Fatalf("pools = %v, want only tank", got)
	}
	if got := visible(authSvc.ClusterCallerNode, authSvc.GuestAudit, "/vm/100", "/vm/101"); len(got) != 2 {
		t.Fatalf("a node caller saw %v, want everything", got)
	}
}

func TestRouteRules(t *testing.T) {
	tests := []struct {
		reason, method, path string
		want                 routeRule
	}{
		{"host reads need audit", http.MethodGet, "/api/network/route", need(pathNode, authSvc.SysAudit)},
		{"host changes need modify", http.MethodPost, "/api/network/route", need(pathNode, authSvc.SysModify)},
		{"iSCSI config holds CHAP secrets", http.MethodGet, "/api/iscsi/config", need(pathNode, authSvc.SysModify)},
		{"the archive holds private keys", http.MethodGet, "/api/certificates/:id/archive", need(pathNode, authSvc.SysModify)},
		{"WireGuard keys are hidden from auditors", http.MethodGet, "/api/network/wireguard/server", inHandler},
		{"registry passwords never leave", http.MethodGet, "/api/container/registry-credentials", need(pathNode, authSvc.SysAudit)},
		{"transport secrets are blanked for auditors", http.MethodGet, "/api/notifications/transports", inHandler},
		{"raw bhyve options run on the host", http.MethodPut, "/api/vm/:rid/options/extra-bhyve-options", hostAdmin},
		{"jail hooks run on the host", http.MethodPut, "/api/jail/:ctid/options/lifecycle-hooks", hostAdmin},
		{"users get shells and doas", http.MethodPost, "/api/auth/users", need(pathAccess, authSvc.UserModify)},
		{"the user list is for pickers here and on host pages", http.MethodGet, "/api/auth/users", inHandler},
		{"Unix groups are host configuration", http.MethodPost, "/api/auth/groups", need(pathNode, authSvc.SysModify)},
		{"a role reaches every grant naming it", http.MethodPut, "/api/auth/roles/:name", need(pathRoot, authSvc.PermissionsModify)},
		{"a group's members get all its grants", http.MethodPut, "/api/auth/acl-groups/:name", need(pathRoot, authSvc.PermissionsModify)},
		{"a grant is checked on its own path", http.MethodPost, "/api/auth/grants", inHandler},
		{"the target node checks forwarded requests", http.MethodDelete, "/api/nodes/:node/*path", anyUser},
	}
	for _, tt := range tests {
		if rule, ok := routeRuleFor(tt.method, tt.path); !ok || !reflect.DeepEqual(rule, tt.want) {
			t.Errorf("%s %s (%s): rule = %+v, %v; want %+v", tt.method, tt.path, tt.reason, rule, ok, tt.want)
		}
	}
}

func TestGuestRoutesHaveNoDefaultRule(t *testing.T) {
	missing, _ := RouteRuleCoverage(gin.RoutesInfo{
		{Method: http.MethodGet, Path: "/api/vm/:rid/unknown"},
		{Method: http.MethodGet, Path: "/api/network/unknown"},
	})
	if want := []string{"GET /api/vm/:rid/unknown"}; !slices.Equal(missing, want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}
