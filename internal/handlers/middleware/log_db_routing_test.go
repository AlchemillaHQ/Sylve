// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestRequestLoggerMiddlewareWritesAuditToTelemetryDB(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})

	router := gin.New()
	router.Use(RequestLoggerMiddleware(telemetryDB, nil))
	router.POST("/api/auth/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"token": "invalid-jwt"}})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var mainCount int64
	if err := mainDB.Model(&infoModels.AuditRecord{}).Count(&mainCount).Error; err != nil {
		t.Fatalf("failed counting main db audit rows: %v", err)
	}
	if mainCount != 0 {
		t.Fatalf("expected 0 main db audit rows, got %d", mainCount)
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.AuditRecord{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry db audit rows: %v", err)
	}
	if telemetryCount != 1 {
		t.Fatalf("expected 1 telemetry db audit row, got %d", telemetryCount)
	}
}
