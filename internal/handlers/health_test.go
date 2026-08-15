// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func performBasicHealthRequest(t *testing.T, service *system.Service) *httptest.ResponseRecorder {
	t.Helper()

	router := gin.New()
	router.GET("/api/health/basic", BasicHealthCheckHandler(service))
	response := testutil.PerformRequest(t, router, http.MethodGet, "/api/health/basic", nil, nil)
	return response
}

func TestBasicHealthTreatsMissingSettingsAsUninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})

	response := performBasicHealthRequest(t, &system.Service{DB: database})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestBasicHealthReportsSettingsDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t)

	response := performBasicHealthRequest(t, &system.Service{DB: database})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestBasicHealthIncludesJailStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})

	response := performBasicHealthRequest(t, &system.Service{DB: database})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	jailed, ok := body.Data["jailed"]
	if !ok {
		t.Fatal("health response is missing jailed")
	}
	if string(jailed) != "false" {
		t.Fatalf("jailed = %s, want false", jailed)
	}
}
