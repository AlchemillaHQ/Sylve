// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestRequireJailDeletionDetachedBlocksDisabledPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationPolicy{})
	if err := db.Create(&clusterModels.ReplicationPolicy{
		ID: 92, Name: "disabled-jail-policy", GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 812, Enabled: false,
	}).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}

	service := &jail.Service{DB: db}
	router := gin.New()
	called := false
	router.DELETE("/jail/:ctid", RequireJailDeletionDetached(service, "ctid"), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/jail/812", nil))
	if response.Code != http.StatusConflict || called {
		t.Fatalf("disabled-policy jail deletion ran: status=%d called=%v", response.Code, called)
	}
}

func TestRequireJailDeletionDetachedBlocksDisabledBackupJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{})
	if err := db.Create(&clusterModels.BackupJob{
		ID: 93, Name: "disabled-jail-backup", TargetID: 1, Mode: clusterModels.BackupJobModeJail,
		JailRootDataset: "tank/sylve/jails/813", CronExpr: "0 * * * *", Enabled: false,
	}).Error; err != nil {
		t.Fatalf("create disabled backup job: %v", err)
	}

	service := &jail.Service{DB: db}
	router := gin.New()
	called := false
	router.DELETE("/jail/:ctid", RequireJailDeletionDetached(service, "ctid"), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/jail/813", nil))
	if response.Code != http.StatusConflict || called ||
		!strings.Contains(response.Body.String(), "guest_delete_requires_backup_jobs_removed") {
		t.Fatalf("backup-owned jail deletion ran: status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}
