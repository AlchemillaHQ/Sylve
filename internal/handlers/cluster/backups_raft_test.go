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
	"strconv"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func setupHandlerRaftCluster(t *testing.T) (*cluster.Service, func()) {
	t.Helper()

	localNodeID, err := utils.GetSystemUUID()
	if err != nil {
		t.Fatalf("GetSystemUUID: %v", err)
	}

	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
		&clusterModels.ClusterNode{},
		&clusterModels.Cluster{},
	)
	fsm := clusterModels.NewFSMDispatcher(db)
	clusterModels.RegisterDefaultHandlers(fsm)

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(localNodeID)
	cfg.Logger = hclog.NewNullLogger()
	cfg.HeartbeatTimeout = 200 * time.Millisecond
	cfg.ElectionTimeout = 200 * time.Millisecond
	cfg.LeaderLeaseTimeout = 100 * time.Millisecond
	cfg.CommitTimeout = 25 * time.Millisecond

	_, transport := raft.NewInmemTransport(raft.ServerAddress(localNodeID))
	r, err := raft.NewRaft(cfg, fsm, raft.NewInmemStore(), raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatalf("raft.NewRaft: %v", err)
	}

	bootstrap := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(localNodeID), Address: raft.ServerAddress(localNodeID)},
		},
	}
	if err := r.BootstrapCluster(bootstrap).Error(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.State() == raft.Leader {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.State() != raft.Leader {
		r.Shutdown()
		t.Fatal("raft did not become leader")
	}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: localNodeID, Hostname: "node1", API: "node1:8181",
		Status: "online",
	})

	cS := &cluster.Service{DB: db, Raft: r}
	return cS, func() {
		r.Shutdown()
		transport.Close()
	}
}

func newBackupJobCrudRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster/backups/jobs", CreateBackupJob(cS))
	r.PUT("/cluster/backups/jobs/:id", UpdateBackupJob(cS))
	r.DELETE("/cluster/backups/jobs/:id", DeleteBackupJob(cS))
	return r
}

func TestCreateBackupJobHandlerHappyPath(t *testing.T) {
	cS, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()

	target := clusterModels.BackupTarget{
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "/backup",
	}
	if err := cS.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	r := newBackupJobCrudRouter(cS)
	body := `{"name":"raft-created-job","targetId":1,"mode":"dataset","sourceDataset":"tank/data","cronExpr":"0 0 * * *"}`
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/jobs", []byte(body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "backup_job_created" {
		t.Fatalf("expected backup_job_created, got %q", resp.Message)
	}

	var count int64
	cS.DB.Model(&clusterModels.BackupJob{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 job created, got %d", count)
	}
}

func TestDeleteBackupJobHandlerHappyPath(t *testing.T) {
	cS, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()

	target := clusterModels.BackupTarget{
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "/backup",
	}
	if err := cS.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	r := newBackupJobCrudRouter(cS)
	createBody := `{"name":"deletable-job","targetId":1,"mode":"dataset","sourceDataset":"tank/data","cronExpr":"0 0 * * *"}`
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/jobs", []byte(createBody))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create for delete test: %d: %s", rr.Code, rr.Body.String())
	}

	var jobs []clusterModels.BackupJob
	cS.DB.Find(&jobs)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	jobID := jobs[0].ID

	rr = performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/jobs/"+toStr(int(jobID)), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var count int64
	cS.DB.Model(&clusterModels.BackupJob{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 jobs after delete, got %d", count)
	}
}

func TestUpdateBackupJobHandlerHappyPath(t *testing.T) {
	cS, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()

	target := clusterModels.BackupTarget{
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "/backup",
	}
	if err := cS.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	r := newBackupJobCrudRouter(cS)
	createBody := `{"name":"updatable-job","targetId":1,"mode":"dataset","sourceDataset":"tank/data","cronExpr":"0 0 * * *"}`
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/jobs", []byte(createBody))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create for update test: %d: %s", rr.Code, rr.Body.String())
	}

	var jobs []clusterModels.BackupJob
	cS.DB.Find(&jobs)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	jobID := jobs[0].ID

	updateBody := `{"name":"updated-job","targetId":1,"mode":"dataset","sourceDataset":"tank/data","cronExpr":"0 12 * * *","enabled":false}`
	rr = performJSONRequest(t, r, http.MethodPut, "/cluster/backups/jobs/"+toStr(int(jobID)),
		[]byte(updateBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var updated clusterModels.BackupJob
	cS.DB.First(&updated, jobID)
	if updated.Name != "updated-job" {
		t.Fatalf("expected name updated-job, got %q", updated.Name)
	}
	if updated.Enabled {
		t.Fatalf("expected enabled=false")
	}
}

func toStr(id int) string { return strconv.Itoa(id) }
