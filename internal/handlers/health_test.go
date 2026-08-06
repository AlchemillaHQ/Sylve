// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func performBasicHealthRequest(t *testing.T, service *system.Service) int {
	t.Helper()

	router := gin.New()
	router.GET("/api/health/basic", BasicHealthCheckHandler(service))
	response := testutil.PerformRequest(t, router, http.MethodGet, "/api/health/basic", nil, nil)
	return response.Code
}

func TestBasicHealthTreatsMissingSettingsAsUninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})

	status := performBasicHealthRequest(t, &system.Service{DB: database})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
}

func TestBasicHealthReportsSettingsDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t)

	status := performBasicHealthRequest(t, &system.Service{DB: database})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}
