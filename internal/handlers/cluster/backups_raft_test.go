// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
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
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupTargetNodeReadiness{},
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
	cS.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error { return nil })
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
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "tank/backup", Enabled: true,
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
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "tank/backup", Enabled: true,
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
		Name: "test-target", SSHHost: "localhost", SSHPort: 22, BackupRoot: "tank/backup", Enabled: true,
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

func TestUpdateBackupJobHandlerMissingJobReturnsNotFound(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{})
	router := newBackupJobCrudRouter(&cluster.Service{DB: database})
	body := `{"name":"missing-job","targetId":1,"mode":"dataset","sourceDataset":"tank/data","cronExpr":"0 0 * * *","enabled":true}`

	response := performJSONRequest(
		t,
		router,
		http.MethodPut,
		"/cluster/backups/jobs/999",
		[]byte(body),
	)
	if response.Code != http.StatusNotFound ||
		!strings.Contains(response.Body.String(), "backup_job_not_found") {
		t.Fatalf("response=%d body=%s, want missing-job 404", response.Code, response.Body.String())
	}
}

func TestUpdateBackupJobHandlerReportsStrictRunnerPlacementFailures(t *testing.T) {
	tests := []struct {
		name        string
		clustered   bool
		duplicate   bool
		wantStatus  int
		wantMessage string
	}{
		{
			name: "inventory unavailable", clustered: true,
			wantStatus: http.StatusServiceUnavailable, wantMessage: "backup_job_runner_inventory_unavailable",
		},
		{
			name: "duplicate registration", duplicate: true,
			wantStatus: http.StatusConflict, wantMessage: "backup_job_update_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(
				t,
				&clusterModels.BackupTarget{}, &clusterModels.BackupTargetNodeReadiness{},
				&clusterModels.BackupJob{}, &clusterModels.BackupJobRunnerRebind{},
				&clusterModels.BackupJobRunnerRebindItem{}, &clusterModels.Cluster{},
				&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{},
				&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
				&jailModels.Jail{},
			)
			if err := database.Create(&clusterModels.Cluster{Enabled: test.clustered}).Error; err != nil {
				t.Fatalf("seed cluster state: %v", err)
			}
			target := clusterModels.BackupTarget{
				ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
			}
			if err := database.Create(&target).Error; err != nil {
				t.Fatalf("seed target: %v", err)
			}
			job := clusterModels.BackupJob{
				ID: 82, Name: "stale-runner", TargetID: target.ID, RunnerNodeID: "node-old",
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/812",
				Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
			}
			if err := database.Create(&job).Error; err != nil {
				t.Fatalf("seed job: %v", err)
			}
			vm := vmModels.VM{RID: 812, Name: "vm-812"}
			if err := database.Create(&vm).Error; err != nil {
				t.Fatalf("seed VM: %v", err)
			}
			dataset := vmModels.VMStorageDataset{
				Pool: "fast", Name: "fast/sylve/virtual-machines/812/disk0", GUID: "vm-812-guid",
			}
			if err := database.Create(&dataset).Error; err != nil {
				t.Fatalf("seed VM dataset: %v", err)
			}
			if err := database.Create(&vmModels.Storage{
				VMID: vm.ID, Type: vmModels.VMStorageTypeZVol,
				Pool: "fast", Enable: true, DatasetID: &dataset.ID,
			}).Error; err != nil {
				t.Fatalf("seed VM storage: %v", err)
			}
			if test.duplicate {
				if err := database.Create(&jailModels.Jail{CTID: 812, Name: "duplicate-jail"}).Error; err != nil {
					t.Fatalf("seed duplicate jail: %v", err)
				}
			}

			service := &cluster.Service{DB: database, NodeID: "node-current"}
			service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error {
				return nil
			})
			router := newBackupJobCrudRouter(service)
			body := `{"name":"stale-runner","targetId":1,"runnerNodeId":"node-current","mode":"vm","sourceDataset":"fast/sylve/virtual-machines/812","recursive":true,"cronExpr":"0 0 * * *","enabled":true}`
			response := performJSONRequest(
				t,
				router,
				http.MethodPut,
				"/cluster/backups/jobs/82",
				[]byte(body),
			)
			if response.Code != test.wantStatus ||
				!strings.Contains(response.Body.String(), test.wantMessage) {
				t.Fatalf(
					"response=%d body=%s, want status=%d message=%s",
					response.Code,
					response.Body.String(),
					test.wantStatus,
					test.wantMessage,
				)
			}
			var unchanged clusterModels.BackupJob
			if err := database.First(&unchanged, job.ID).Error; err != nil {
				t.Fatalf("reload job: %v", err)
			}
			if unchanged.RunnerNodeID != "node-old" {
				t.Fatalf("failed placement changed runner: %+v", unchanged)
			}
		})
	}
}

func TestValidateBackupTargetRequiresExplicitVoterInCluster(t *testing.T) {
	cS, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()
	target := clusterModels.BackupTarget{
		ID: 91, Name: "target-validate-node", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := cS.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	zStub := &backupTargetZeltaStub{}
	router := newBackupTargetRouter(cS, zStub)
	path := "/cluster/backups/targets/91/validate"
	rr := performJSONRequest(t, router, http.MethodPost, path, nil)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "validation_node_id_required") {
		t.Fatalf("missing node status=%d body=%s", rr.Code, rr.Body.String())
	}
	configuration := cS.Raft.GetConfiguration()
	if err := configuration.Error(); err != nil || len(configuration.Configuration().Servers) != 1 {
		t.Fatalf("configuration err=%v servers=%+v", err, configuration.Configuration().Servers)
	}
	nodeID := string(configuration.Configuration().Servers[0].ID)
	rr = performJSONRequest(t, router, http.MethodPost, path+"?nodeId="+nodeID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("selected node status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(zStub.validateCalls) != 1 {
		t.Fatalf("validate calls=%d, want 1", len(zStub.validateCalls))
	}
}

func toStr(id int) string { return strconv.Itoa(id) }
