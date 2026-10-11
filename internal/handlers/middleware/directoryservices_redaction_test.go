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
	"strings"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestDirectoryServicesAuditDoesNotRecordCredentials(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/api/directory-services/active-directory"},
		{http.MethodPost, "/api/directory-services/active-directory/join"},
		{http.MethodPost, "/api/directory-services/active-directory/leave"},
		{http.MethodPost, "/api/directory-services/kerberos/keytabs"},
		{http.MethodPost, "/api/directory-services/recovery/export"},
		{http.MethodPost, "/api/directory-services/recovery/restore"},
		{http.MethodPost, "/api/directory-services/recovery/rollback"},
	} {
		for _, body := range []string{`{"joinUsername":"joinuser","joinPassword":"private-password","password":"private-password","passphrase":"private-password","file":"private-keytab"}`, `{"joinPassword":"private-password`} {
			db := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("UserID", uint(1)); c.Set("Username", "admin"); c.Set("AuthType", "sylve") })
			router.Use(RequestLoggerMiddleware(db, nil))
			router.Handle(route.method, route.path, func(c *gin.Context) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "private-password", "data": "private-keytab"})
			})
			testutil.PerformJSONRequest(t, router, route.method, route.path, []byte(body))
			var record infoModels.AuditRecord
			if err := db.First(&record).Error; err != nil {
				t.Fatal(err)
			}
			if strings.Contains(record.Action, "private-password") || strings.Contains(record.Action, "private-keytab") || !strings.Contains(record.Action, "[REDACTED]") {
				t.Fatalf("directory credential was recorded: %s", record.Action)
			}
		}
	}
}
