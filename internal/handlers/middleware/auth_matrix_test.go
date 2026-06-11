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
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type authProfile struct {
	AuthType        string
	UserID          uint
	AuthScope       string
	ClusterTokenUse string
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
		name     string
		profile  authProfile
		middleware []gin.HandlerFunc
		want     int
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
