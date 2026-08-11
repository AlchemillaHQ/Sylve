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
	"strings"
	"testing"

	internalDB "github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestAsyncWorkerCompletionBeforeRequestLoggerReturnCannotBeOverwritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("UserID", uint(7))
		c.Set("Username", "restore-user")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(RequestLoggerMiddleware(auditDB, nil))
	router.POST("/api/cluster/backups/targets/:id/restore", func(c *gin.Context) {
		ref, err := internalDB.PrepareAsyncAuditRecord(
			auditDB,
			c.Request.Context(),
			"backup_target_restore",
			81,
			"target-restore:node-a:fast-worker",
		)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		if err := internalDB.FinalizeAsyncAuditOperation(
			auditDB,
			ref,
			"success",
			"",
			map[string]any{"eventId": 55, "status": "success"},
		); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"status": "success", "message": "restore_job_started"})
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/cluster/backups/targets/81/restore",
		bytes.NewBufferString(`{"snapshot":"@test"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}

	var record infoModels.AuditRecord
	if err := auditDB.First(&record).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if record.Status != "success" || record.AsyncOperationID != "target-restore:node-a:fast-worker" {
		t.Fatalf("terminal audit was overwritten by middleware: %+v", record)
	}
	if !strings.Contains(record.Action, `"eventId":55`) || strings.Contains(record.Action, "restore_job_started") {
		t.Fatalf("worker outcome was not authoritative: %s", record.Action)
	}
}

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

func TestRequestLoggerMiddlewareSkipsRoutineSSETokenIssuance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	router := gin.New()
	router.Use(RequestLoggerMiddleware(auditDB, nil))
	router.POST("/api/auth/sse-tokens", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/sse-tokens", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var count int64
	if err := auditDB.Model(&infoModels.AuditRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count audit records: %v", err)
	}
	if count != 0 {
		t.Fatalf("audit record count=%d want=0", count)
	}
}
