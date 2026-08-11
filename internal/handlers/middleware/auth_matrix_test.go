// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type authProfile struct {
	AuthType        string
	UserID          uint
	AuthScope       string
	ClusterTokenUse string
}

func TestEnsureAuthenticatedTrustBoundary(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Token{},
		&models.SystemSecrets{},
		&clusterModels.Cluster{},
	)
	if err := database.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "local-secret"}).Error; err != nil {
		t.Fatalf("seed JWT secret: %v", err)
	}
	if err := database.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("seed cluster key: %v", err)
	}
	password, err := utils.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "admin", Password: password, Admin: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	service := &authSvc.Service{DB: database}
	_, localToken, err := service.CreateJWT(user.Username, "correct horse battery staple", "sylve", false)
	if err != nil {
		t.Fatalf("create local token: %v", err)
	}
	proxyToken, err := service.CreateUserProxyJWT(user.ID, user.Username, "sylve")
	if err != nil {
		t.Fatalf("create proxy token: %v", err)
	}
	readToken, err := service.CreateUserProxyJWT(0, "status", "")
	if err != nil {
		t.Fatalf("create service-read token: %v", err)
	}
	controlToken, err := service.CreateInternalClusterJWT("control")
	if err != nil {
		t.Fatalf("create control token: %v", err)
	}
	localHash := utils.SHA256(localToken, 1)
	fileAuth, err := json.Marshal(map[string]string{"hostname": "origin"})
	if err != nil {
		t.Fatal(err)
	}
	consoleAuth, err := json.Marshal(map[string]string{"hash": localHash, "hostname": "origin"})
	if err != nil {
		t.Fatal(err)
	}
	fileAuthHex := hex.EncodeToString(fileAuth)
	consoleAuthHex := hex.EncodeToString(consoleAuth)

	tests := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{
			name: "local bearer", method: http.MethodGet, path: "/api/vm",
			headers: map[string]string{"Authorization": "Bearer " + localToken}, want: http.StatusOK,
		},
		{
			name: "file explorer hash", method: http.MethodGet,
			path: "/api/system/file-explorer/download?hash=" + localHash + "&auth=" + fileAuthHex,
			want: http.StatusOK,
		},
		{
			name: "host terminal hash", method: http.MethodGet,
			path: "/api/info/terminal?auth=" + consoleAuthHex, want: http.StatusOK,
		},
		{
			name: "VNC hash", method: http.MethodGet,
			path: "/api/vnc/5900?auth=" + consoleAuthHex, want: http.StatusOK,
		},
		{
			name: "VM console hash", method: http.MethodGet,
			path: "/api/vm/1/console?auth=" + consoleAuthHex, want: http.StatusOK,
		},
		{
			name: "jail console hash", method: http.MethodGet,
			path: "/api/jail/1/console?auth=" + consoleAuthHex, want: http.StatusOK,
		},
		{
			name: "malformed cluster header overrides local bearer", method: http.MethodGet, path: "/api/vm",
			headers: map[string]string{
				authSvc.ClusterTokenHeader: "not-bearer", "Authorization": "Bearer " + localToken,
			},
			want: http.StatusUnauthorized,
		},
		{
			name: "positive proxy ordinary route", method: http.MethodGet, path: "/api/vm",
			headers: map[string]string{authSvc.ClusterTokenHeader: "Bearer " + proxyToken}, want: http.StatusOK,
		},
		{
			name: "service read exact GET", method: http.MethodGet, path: "/api/info/node",
			headers: map[string]string{authSvc.ClusterTokenHeader: "Bearer " + readToken}, want: http.StatusOK,
		},
		{
			name: "service read near miss", method: http.MethodGet, path: "/api/info/node/extra",
			headers: map[string]string{authSvc.ClusterTokenHeader: "Bearer " + readToken}, want: http.StatusForbidden,
		},
		{
			name: "internal control below prefix", method: http.MethodPost, path: "/api/intra-cluster/sync",
			headers: map[string]string{authSvc.ClusterTokenHeader: "Bearer " + controlToken}, want: http.StatusOK,
		},
		{
			name: "internal control ordinary route", method: http.MethodGet, path: "/api/info/node",
			headers: map[string]string{authSvc.ClusterTokenHeader: "Bearer " + controlToken}, want: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(EnsureAuthenticated(service))
			routePath := strings.SplitN(test.path, "?", 2)[0]
			router.Handle(test.method, routePath, func(c *gin.Context) { c.Status(http.StatusOK) })

			response := testutil.PerformRequest(t, router, test.method, test.path, nil, test.headers)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

var (
	anonUser = authProfile{}

	sylveAdmin = authProfile{
		AuthType: "sylve", UserID: 1, AuthScope: "local",
	}
	sylveNonAdmin = authProfile{
		AuthType: "sylve", UserID: 2, AuthScope: "local",
	}
	clusterInternal = authProfile{
		AuthType:        authSvc.ClusterInternalAuthType,
		UserID:          0,
		AuthScope:       "cluster",
		ClusterTokenUse: authSvc.ClusterTokenUseInternalControl,
	}
)

func injectAuthProfile(p authProfile) gin.HandlerFunc {
	return func(c *gin.Context) {
		if p.AuthType != "" {
			c.Set("AuthType", p.AuthType)
		}
		if p.UserID != 0 {
			c.Set("UserID", p.UserID)
		}
		if p.AuthScope != "" {
			c.Set("AuthScope", p.AuthScope)
		}
		if p.ClusterTokenUse != "" {
			c.Set("ClusterTokenUse", p.ClusterTokenUse)
		}
		c.Next()
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{})
	authService := &authSvc.Service{DB: db}

	if err := db.Create(&models.User{ID: 1, Username: "admin", Admin: true}).Error; err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}
	if err := db.Create(&models.User{ID: 2, Username: "user", Admin: false}).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	mockEnsureAuth := func(p authProfile) gin.HandlerFunc {
		return func(c *gin.Context) {
			if p.AuthType == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no_token_provided"})
				return
			}
			if p.UserID != 0 {
				user, err := authService.GetUserByID(p.UserID)
				if err != nil || user == nil {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
					return
				}
			}
			injectAuthProfile(p)(c)
		}
	}

	adminMW := RequireLocalAdmin(authService)
	clusterScopeMW := RequireClusterScope()

	type testCase struct {
		name       string
		profile    authProfile
		middleware []gin.HandlerFunc
		want       int
	}

	cases := []testCase{
		{"info: unauthenticated → 401", anonUser, nil, http.StatusUnauthorized},
		{"info: authenticated → 200", sylveNonAdmin, nil, http.StatusOK},

		{"cluster: unauthenticated → 401", anonUser, nil, http.StatusUnauthorized},
		{"cluster: authenticated → 200", sylveNonAdmin, nil, http.StatusOK},

		{"admin: unauthenticated → 401", anonUser, []gin.HandlerFunc{adminMW}, http.StatusUnauthorized},
		{"admin: non-admin → 403", sylveNonAdmin, []gin.HandlerFunc{adminMW}, http.StatusForbidden},
		{"admin: admin → 200", sylveAdmin, []gin.HandlerFunc{adminMW}, http.StatusOK},

		{"intra-cluster: unauthenticated → 401", anonUser, []gin.HandlerFunc{clusterScopeMW}, http.StatusUnauthorized},
		{"intra-cluster: user → 403", sylveNonAdmin, []gin.HandlerFunc{clusterScopeMW}, http.StatusForbidden},
		{"intra-cluster: admin → 403", sylveAdmin, []gin.HandlerFunc{clusterScopeMW}, http.StatusForbidden},
		{"intra-cluster: cluster token → 200", clusterInternal, []gin.HandlerFunc{clusterScopeMW}, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(mockEnsureAuth(tc.profile))
			for _, m := range tc.middleware {
				r.Use(m)
			}
			r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

			rec := testutil.PerformRequest(t, r, http.MethodGet, "/ok", nil, nil)
			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, rec.Code)
			}
		})
	}
}
