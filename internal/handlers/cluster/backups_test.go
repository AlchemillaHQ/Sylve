// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newBackupsRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/backups/jobs", BackupJobs(cS))
	r.GET("/cluster/backups/targets/:id/running-job-ids", BackupTargetRunningJobIDs(cS))
	r.POST("/cluster/backups/jobs", CreateBackupJob(cS))
	r.DELETE("/cluster/backups/jobs/:id", DeleteBackupJob(cS))
	return r
}

func TestBackupJobsHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.BackupJob]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" || resp.Message != "backup_jobs_listed" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty, got %d jobs", len(resp.Data))
	}

	target := clusterModels.BackupTarget{
		Name: "test-target", SSHHost: "localhost", BackupRoot: "/backup",
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 100, Name: "test-job", TargetID: target.ID, Mode: "dataset", CronExpr: "0 0 * * *",
		NextRunAt: timePtr(time.Now()),
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 job, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs?targetId=99999", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 with non-existent target, got %d", len(resp.Data))
	}
}

func TestBackupTargetRunningJobIDs(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupEvent{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets/1/running-job-ids", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-existent target, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateBackupJobHandlerValidation(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/jobs",
		[]byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteBackupJobHandlerValidation(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/jobs/abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric id, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/jobs/0", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero id, got %d: %s", rr.Code, rr.Body.String())
	}
}

func timePtr(t time.Time) *time.Time { return &t }
