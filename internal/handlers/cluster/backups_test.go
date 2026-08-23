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
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newBackupsRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/backups/jobs", BackupJobs(cS))
	r.GET("/cluster/backups/targets/:id/running-jobs", BackupTargetRunningJobIDs(cS))
	r.POST("/cluster/backups/jobs", CreateBackupJob(cS))
	r.DELETE("/cluster/backups/jobs/:id", DeleteBackupJob(cS))
	return r
}

func TestUpdateBackupJobStateInternalPreservesLegacyEncryption(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{})
	service := &cluster.Service{DB: db}
	job := clusterModels.BackupJob{
		ID: 71, Name: "forwarded-state", TargetID: 9,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 0 * * *", Enabled: true, Encrypted: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/intra-cluster/backup-job-state", UpdateBackupJobStateInternal(service))

	legacy := []byte(`{"jobId":71,"lastStatus":"failed","lastError":"legacy follower"}`)
	rr := performJSONRequest(t, router, http.MethodPost, "/intra-cluster/backup-job-state", legacy)
	if rr.Code != http.StatusOK {
		t.Fatalf("legacy update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload legacy update: %v", err)
	}
	if !job.Encrypted {
		t.Fatal("legacy forwarded update cleared encrypted state")
	}

	current := []byte(`{"version":1,"jobId":71,"lastStatus":"success","encrypted":false}`)
	rr = performJSONRequest(t, router, http.MethodPost, "/intra-cluster/backup-job-state", current)
	if rr.Code != http.StatusOK {
		t.Fatalf("current update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload current update: %v", err)
	}
	if job.Encrypted {
		t.Fatal("forwarded encrypted=false was not applied")
	}
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

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs?targetId=1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != job.ID {
		t.Fatalf("expected target-filtered job %d, got %+v", job.ID, resp.Data)
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

func TestBackupJobsHandlerRejectsInvalidTargetFilter(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{})
	if err := db.Create(&clusterModels.BackupJob{
		ID: 100, Name: "must-not-leak", TargetID: 1,
		Mode: clusterModels.BackupJobModeDataset, CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	router := newBackupsRouter(&cluster.Service{DB: db})

	tests := map[string]string{
		"empty":       "?targetId=",
		"malformed":   "?targetId=not-a-number",
		"zero":        "?targetId=0",
		"unsafe":      "?targetId=9007199254740992",
		"overflow":    "?targetId=18446744073709551616",
		"repeated":    "?targetId=1&targetId=2",
		"empty-first": "?targetId=&targetId=1",
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			rr := performJSONRequest(
				t,
				router,
				http.MethodGet,
				"/cluster/backups/jobs"+query,
				nil,
			)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
			var response handlerAPIResponse[any]
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if response.Message != "invalid_target_filter" {
				t.Fatalf("message = %q, want invalid_target_filter", response.Message)
			}
		})
	}
}

func TestBackupJobsHandlerFiltersByGuest(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	target := clusterModels.BackupTarget{
		Name: "vm-filter-target", SSHHost: "localhost", BackupRoot: "/backup",
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	jobs := []clusterModels.BackupJob{
		{ID: 101, Name: "vm-12", TargetID: target.ID, Mode: "vm", SourceDataset: "zroot/sylve/virtual-machines/12", CronExpr: "0 0 * * *"},
		{ID: 102, Name: "vm-112", TargetID: target.ID, Mode: "vm", SourceDataset: "zroot/sylve/virtual-machines/112", CronExpr: "0 0 * * *"},
		{ID: 103, Name: "dataset-12", TargetID: target.ID, Mode: "dataset", SourceDataset: "zroot/sylve/virtual-machines/12", CronExpr: "0 0 * * *"},
		{ID: 104, Name: "jail-12", TargetID: target.ID, Mode: "jail", JailRootDataset: "zroot/sylve/jails/12", CronExpr: "0 0 * * *"},
		{ID: 105, Name: "jail-112", TargetID: target.ID, Mode: "jail", JailRootDataset: "zroot/sylve/jails/112", CronExpr: "0 0 * * *"},
		{ID: 106, Name: "dataset-jail-12", TargetID: target.ID, Mode: "dataset", SourceDataset: "zroot/sylve/jails/12", CronExpr: "0 0 * * *"},
		{ID: 107, Name: "legacy-jail-12", TargetID: target.ID, Mode: "jail", SourceDataset: "zroot/sylve/jails/12", CronExpr: "0 0 * * *"},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("failed to seed jobs: %v", err)
	}

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs?guestType=vm&guestId=12", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp handlerAPIResponse[[]clusterModels.BackupJob]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != 101 {
		t.Fatalf("expected only VM 12 job, got %+v", resp.Data)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/jobs?guestType=jail&guestId=12", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp = handlerAPIResponse[[]clusterModels.BackupJob]{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 2 || resp.Data[0].ID != 104 || resp.Data[1].ID != 107 {
		t.Fatalf("expected jail 12 jobs including the legacy source fallback, got %+v", resp.Data)
	}

	for _, path := range []string{
		"/cluster/backups/jobs?guestType=vm",
		"/cluster/backups/jobs?guestType=vm&guestId=0",
		"/cluster/backups/jobs?guestType=dataset&guestId=12",
		"/cluster/backups/jobs?guestType=jail&guestId=not-a-number",
	} {
		rr = performJSONRequest(t, r, http.MethodGet, path, nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestBackupTargetRunningJobIDsUsesDurableRemoteRunnerOperations(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{}, &clusterModels.BackupEvent{})
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)
	if err := db.Create(&[]clusterModels.BackupTarget{
		{ID: 1, Name: "target-one", SSHHost: "user@one", BackupRoot: "tank/one"},
		{ID: 2, Name: "target-two", SSHHost: "user@two", BackupRoot: "tank/two"},
	}).Error; err != nil {
		t.Fatalf("seed targets: %v", err)
	}

	jobs := []clusterModels.BackupJob{
		{ID: 201, Name: "remote-runner-active", TargetID: 1, RunnerNodeID: "runner-a", Mode: "dataset", CronExpr: "0 0 * * *"},
		{ID: 202, Name: "ingress-local-event-only", TargetID: 1, RunnerNodeID: "ingress-b", Mode: "dataset", CronExpr: "0 0 * * *"},
		{ID: 203, Name: "other-target", TargetID: 2, RunnerNodeID: "runner-c", Mode: "dataset", CronExpr: "0 0 * * *"},
		{ID: 204, Name: "remote-restore-finishing", TargetID: 1, RunnerNodeID: "runner-d", Mode: "dataset", CronExpr: "0 0 * * *"},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("seed jobs: %v", err)
	}
	now := time.Now().UTC()
	operations := []clusterModels.BackupJobOperation{
		{JobID: 201, Token: "backup:runner-a:active", Operation: clusterModels.BackupJobOperationBackup, State: clusterModels.BackupJobOperationRunning, HolderNodeID: "runner-a", Revision: 2, AcquiredAt: now, UpdatedAt: now},
		{JobID: 203, Token: "backup:runner-c:other", Operation: clusterModels.BackupJobOperationBackup, State: clusterModels.BackupJobOperationQueued, HolderNodeID: "runner-c", Revision: 1, AcquiredAt: now, UpdatedAt: now},
		{JobID: 204, Token: "restore:runner-d:finishing", Operation: clusterModels.BackupJobOperationRestore, State: clusterModels.BackupJobOperationFinishing, HolderNodeID: "runner-d", Revision: 3, AcquiredAt: now, UpdatedAt: now},
	}
	if err := db.Create(&operations).Error; err != nil {
		t.Fatalf("seed durable operations: %v", err)
	}
	if err := db.Create(&clusterModels.BackupEvent{
		JobID: uintPtr(202), Status: "running", StartedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed ingress-local event: %v", err)
	}

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets/1/running-jobs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response handlerAPIResponse[[]uint]
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 2 || response.Data[0] != 201 || response.Data[1] != 204 {
		t.Fatalf("expected remote backup 201 and finishing restore 204, got %v", response.Data)
	}
}

func TestBackupTargetRunningJobIDsFailsWhenDurableStatusIsUnavailable(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	if err := db.Create(&clusterModels.BackupTarget{
		ID: 1, Name: "target-one", SSHHost: "user@one", BackupRoot: "tank/one",
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Migrator().DropTable(&clusterModels.BackupJobOperation{}); err != nil {
		t.Fatalf("drop operation table: %v", err)
	}
	cS := &cluster.Service{DB: db}
	r := newBackupsRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets/1/running-jobs", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status failure to return 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestBackupTargetRunningJobIDsReturnsNotFoundForMissingTarget(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	r := newBackupsRouter(&cluster.Service{DB: db})

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets/99/running-jobs", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
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

func TestBackupJobCreateRejectsOversizedBody(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	r := gin.New()
	r.Use(middleware.LimitRequestBody(64))
	r.POST("/cluster/backups/jobs", CreateBackupJob(&cluster.Service{DB: db}))

	body := []byte(`{"name":"` + strings.Repeat("x", 128) + `","targetId":1,"mode":"dataset"}`)
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/jobs", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
	var response handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "request_body_too_large" || response.Error != "request_body_too_large" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestDeleteBackupJobHandlerValidation(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
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

	rr = performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/jobs/99", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing job, got %d: %s", rr.Code, rr.Body.String())
	}

	target := clusterModels.BackupTarget{
		ID: 1, Name: "delete-job-target", SSHHost: "user@target", BackupRoot: "tank/target",
	}
	job := clusterModels.BackupJob{
		ID: 7, Name: "running-job", TargetID: target.ID,
		Mode: clusterModels.BackupJobModeDataset, CronExpr: "0 0 * * *",
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&clusterModels.BackupJobOperation{
		JobID: job.ID, Token: "backup:runner:delete-test", Operation: clusterModels.BackupJobOperationBackup,
		State: clusterModels.BackupJobOperationRunning, HolderNodeID: "runner", Revision: 1,
		AcquiredAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	rr = performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/jobs/7", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for running job, got %d: %s", rr.Code, rr.Body.String())
	}
}

func timePtr(t time.Time) *time.Time { return &t }
