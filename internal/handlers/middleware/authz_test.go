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
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAuthzTestService(t *testing.T) *authSvc.Service {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed_to_open_db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.Group{}); err != nil {
		t.Fatalf("failed_to_migrate_db: %v", err)
	}

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

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

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

func TestRequireLocalAdminRejectsPamAuth(t *testing.T) {
	service := newAuthzTestService(t)
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
