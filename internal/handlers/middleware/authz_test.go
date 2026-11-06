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

func newAuthzTestService(t *testing.T) *authSvc.Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{})

	return &authSvc.Service{DB: db}
}

func performAuthzRequest(t *testing.T, service *authSvc.Service, authType string, userID uint) int {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authType != "" {
			c.Set("AuthType", authType)
		}
		if userID != 0 {
			c.Set("UserID", userID)
		}
		c.Next()
	})
	r.Use(RequireLocalAdmin(service))
	r.GET("/secure", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	rec := testutil.PerformRequest(t, r, http.MethodGet, "/secure", nil, nil)

	return rec.Code
}

func TestRequireLocalAdmin(t *testing.T) {
	tests := []struct {
		name     string
		username string
		admin    bool
		authType string
		want     int
	}{
		{"sylve admin", "admin", true, "sylve", http.StatusOK},
		{"passkey admin", "admin", true, authSvc.AuthTypeSylvePasskey, http.StatusOK},
		{"pam admin", "root", true, "pam", http.StatusOK},
		{"pam non-admin", "pamuser", false, "pam", http.StatusForbidden},
		{"sylve non-admin", "user", false, "sylve", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newAuthzTestService(t)
			if err := service.DB.Create(&models.User{ID: 1, Username: tt.username, Admin: tt.admin}).Error; err != nil {
				t.Fatalf("failed_to_seed_user: %v", err)
			}
			if status := performAuthzRequest(t, service, tt.authType, 1); status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

func TestRequireLocalAdminTrustsTheAdminAMemberActsFor(t *testing.T) {
	service := newAuthzTestService(t)
	gin.SetMode(gin.TestMode)

	request := func(caller string) int {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("AuthType", "sylve")
			c.Set("AuthScope", "cluster")
			c.Set("ClusterCaller", caller)
			c.Next()
		})
		router.Use(RequireLocalAdmin(service))
		router.GET("/secure", func(c *gin.Context) { c.Status(http.StatusOK) })
		return testutil.PerformRequest(t, router, http.MethodGet, "/secure", nil, nil).Code
	}

	if status := request(authSvc.ClusterCallerUser); status != http.StatusOK {
		t.Fatalf("proxied admin status=%d", status)
	}
	if status := request(authSvc.ClusterCallerNode); status != http.StatusForbidden {
		t.Fatalf("member node status=%d", status)
	}
}

func TestRequireLocalAdminForWritesAllowsReadsOnly(t *testing.T) {
	service := newAuthzTestService(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("AuthType", "sylve")
		c.Set("UserID", uint(1))
		c.Next()
	})
	router.Use(RequireLocalAdminForWrites(service))
	router.Any("/entries", func(c *gin.Context) { c.Status(http.StatusOK) })

	if status := testutil.PerformRequest(t, router, http.MethodGet, "/entries", nil, nil).Code; status != http.StatusOK {
		t.Fatalf("read status=%d", status)
	}
	if status := testutil.PerformRequest(t, router, http.MethodPost, "/entries", nil, nil).Code; status != http.StatusUnauthorized {
		t.Fatalf("write status=%d", status)
	}
}
