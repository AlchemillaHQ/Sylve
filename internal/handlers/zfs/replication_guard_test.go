// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestReplicationCreateFilesystemGuardRestoresBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationPolicy{})
	svc := &zfsService.Service{DB: db}
	router := gin.New()
	called := false
	router.POST("/create",
		ReplicationDatasetMutationGuard(svc, ReplicationGuardCreateFilesystem),
		func(c *gin.Context) {
			var req CreateFilesystemRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				t.Fatalf("downstream body decode failed: %v", err)
			}
			called = req.Name == "child" && req.Parent == "tank/data" && req.Properties["parent"] == "tank/data"
			c.Status(http.StatusNoContent)
		},
	)
	body, _ := json.Marshal(CreateFilesystemRequest{
		Name: "child", Parent: "tank/data", Properties: map[string]string{"parent": "tank/data"},
	})
	req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("guard did not preserve request body: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReplicationCreateFilesystemGuardRejectsParentMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &zfsService.Service{}
	router := gin.New()
	called := false
	router.POST("/create",
		ReplicationDatasetMutationGuard(svc, ReplicationGuardCreateFilesystem),
		func(c *gin.Context) { called = true },
	)
	body, _ := json.Marshal(CreateFilesystemRequest{
		Name: "child", Parent: "tank/safe", Properties: map[string]string{"parent": "tank/protected"},
	})
	req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("mismatched parents were not rejected: status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}
