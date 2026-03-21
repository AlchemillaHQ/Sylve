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
