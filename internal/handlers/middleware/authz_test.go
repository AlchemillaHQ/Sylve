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

func TestRequireLocalAdminAllowsSylveAdmin(t *testing.T) {
	service := newAuthzTestService(t)
	if err := service.DB.Create(&models.User{
		ID:       1,
		Username: "admin",
		Admin:    true,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	status := performAuthzRequest(t, service, "sylve", 1)
	if status != http.StatusOK {
		t.Fatalf("expected_status_200_got: %d", status)
	}
}

func TestRequireLocalAdminAllowsPasskeyAdmin(t *testing.T) {
	service := newAuthzTestService(t)
	if err := service.DB.Create(&models.User{
		ID:       1,
		Username: "admin",
		Admin:    true,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	status := performAuthzRequest(t, service, authSvc.AuthTypeSylvePasskey, 1)
	if status != http.StatusOK {
		t.Fatalf("expected_status_200_got: %d", status)
	}
}

func TestRequireLocalAdminAllowsPamAdmin(t *testing.T) {
	service := newAuthzTestService(t)
	if err := service.DB.Create(&models.User{
		ID:       1,
		Username: "root",
		Admin:    true,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	status := performAuthzRequest(t, service, "pam", 1)
	if status != http.StatusOK {
		t.Fatalf("expected_status_200_got: %d", status)
	}
}

func TestRequireLocalAdminRejectsPamNonAdmin(t *testing.T) {
	service := newAuthzTestService(t)
	if err := service.DB.Create(&models.User{
		ID:       1,
		Username: "pamuser",
		Admin:    false,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	status := performAuthzRequest(t, service, "pam", 1)
	if status != http.StatusForbidden {
		t.Fatalf("expected_status_403_got: %d", status)
	}
}

func TestRequireLocalAdminRejectsNonAdmin(t *testing.T) {
	service := newAuthzTestService(t)
	if err := service.DB.Create(&models.User{
		ID:       1,
		Username: "user",
		Admin:    false,
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	status := performAuthzRequest(t, service, "sylve", 1)
	if status != http.StatusForbidden {
		t.Fatalf("expected_status_403_got: %d", status)
	}
}

func TestRequireLocalAdminUsesSignedForwardedClaim(t *testing.T) {
	service := newAuthzTestService(t)
	gin.SetMode(gin.TestMode)

	request := func(admin bool) int {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("AuthType", "sylve")
			c.Set("UserID", uint(99))
			c.Set("AuthScope", "cluster")
			c.Set("ClusterTokenUse", authSvc.ClusterTokenUseUserProxy)
			c.Set("ProxyAdmin", admin)
			c.Next()
		})
		router.Use(RequireLocalAdmin(service))
		router.GET("/secure", func(c *gin.Context) { c.Status(http.StatusOK) })
		return testutil.PerformRequest(t, router, http.MethodGet, "/secure", nil, nil).Code
	}

	if status := request(true); status != http.StatusOK {
		t.Fatalf("signed forwarded admin status=%d", status)
	}
	if status := request(false); status != http.StatusForbidden {
		t.Fatalf("forwarded non-admin status=%d", status)
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
