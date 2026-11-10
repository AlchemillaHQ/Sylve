// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type backupForwardTestNode struct {
	id        string
	address   raft.ServerAddress
	transport *raft.InmemTransport
	raft      *raft.Raft
	service   *clusterService.Service
}

type backupForwardAuthCall struct {
	userID   uint
	authType string
}

type backupForwardAuthStub struct {
	serviceInterfaces.AuthServiceInterface
	mu    sync.Mutex
	calls []backupForwardAuthCall
}

func (s *backupForwardAuthStub) ProxiedUser(userID uint, authType string) (serviceInterfaces.ProxiedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, backupForwardAuthCall{userID: userID, authType: authType})
	return serviceInterfaces.ProxiedUser{Username: "admin", AuthType: authType}, nil
}

type backupRestoreHandlerStub struct {
	mu               sync.Mutex
	registeredKeys   []string
	enqueuedJobs     []uint
	enqueuedSnapshot []string
	enqueuedKeys     []string
	registerErr      error
	enqueueErr       error
}

func (s *backupRestoreHandlerStub) RegisterRestoreEncryptionKey(key, format string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registeredKeys = append(s.registeredKeys, key+"\x00"+format)
	return s.registerErr
}

func (s *backupRestoreHandlerStub) EnqueueRestoreJob(_ context.Context, jobID uint, snapshot, passphrase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueuedJobs = append(s.enqueuedJobs, jobID)
	s.enqueuedSnapshot = append(s.enqueuedSnapshot, snapshot)
	s.enqueuedKeys = append(s.enqueuedKeys, passphrase)
	return s.enqueueErr
}

type backupRunHandlerStub struct {
	mu       sync.Mutex
	enqueued []uint
	err      error
}

func TestRestoreBackupJobRejectsInvalidSnapshotBeforeLookup(t *testing.T) {
	service := &clusterService.Service{DB: testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{})}
	restoreService := &backupRestoreHandlerStub{}
	router := newBackupRestoreForwardRouter(service, restoreService, 1, "admin", "local")
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/cluster/backups/jobs/42/restore",
		strings.NewReader(`{"snapshot":"@bad;name","encryptionKey":"secret"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || len(restoreService.registeredKeys) != 0 || len(restoreService.enqueuedJobs) != 0 {
		t.Fatalf(
			"status=%d body=%s keys=%v jobs=%v",
			response.Code, response.Body.String(), restoreService.registeredKeys, restoreService.enqueuedJobs,
		)
	}
}

func (s *backupRunHandlerStub) EnqueueBackupJob(_ context.Context, jobID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued = append(s.enqueued, jobID)
	return s.err
}

func setupBackupForwardTestCluster(t *testing.T, nodeIDs ...string) []*backupForwardTestNode {
	t.Helper()
	if len(nodeIDs) < 2 {
		t.Fatal("backup forwarding fixture requires at least two voters")
	}

	nodes := make([]*backupForwardTestNode, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		database := testutil.NewSQLiteTestDB(t,
			&clusterModels.BackupTarget{},
			&clusterModels.BackupJob{},
			&clusterModels.BackupJobOperation{},
			&clusterModels.ClusterNode{},
		)
		fsm := clusterModels.NewFSMDispatcher(database)
		clusterModels.RegisterDefaultHandlers(fsm)
		cfg := raft.DefaultConfig()
		cfg.LocalID = raft.ServerID(nodeID)
		cfg.Logger = hclog.NewNullLogger()
		cfg.HeartbeatTimeout = 200 * time.Millisecond
		cfg.ElectionTimeout = 200 * time.Millisecond
		cfg.LeaderLeaseTimeout = 100 * time.Millisecond
		cfg.CommitTimeout = 25 * time.Millisecond
		address, transport := raft.NewInmemTransport(raft.ServerAddress(nodeID))
		r, err := raft.NewRaft(
			cfg,
			fsm,
			raft.NewInmemStore(),
			raft.NewInmemStore(),
			raft.NewInmemSnapshotStore(),
			transport,
		)
		if err != nil {
			t.Fatalf("create raft node %s: %v", nodeID, err)
		}
		nodes = append(nodes, &backupForwardTestNode{
			id: nodeID, address: address, transport: transport, raft: r,
			service: withRaft(&clusterService.Service{DB: database, NodeID: nodeID}, r),
		})
	}

	for _, left := range nodes {
		for _, right := range nodes {
			if left == right {
				continue
			}
			left.transport.Connect(right.address, right.transport)
		}
	}
	if err := nodes[0].raft.BootstrapCluster(raft.Configuration{Servers: []raft.Server{{
		ID: raft.ServerID(nodes[0].id), Address: nodes[0].address, Suffrage: raft.Voter,
	}}}).Error(); err != nil {
		t.Fatalf("bootstrap forwarding cluster: %v", err)
	}
	leader := waitBackupForwardLeader(t, nodes, 8*time.Second)
	for _, node := range nodes[1:] {
		if err := leader.raft.AddVoter(raft.ServerID(node.id), node.address, 0, 5*time.Second).Error(); err != nil {
			t.Fatalf("add voter %s: %v", node.id, err)
		}
		leader = waitBackupForwardLeader(t, nodes, 8*time.Second)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		allReady := true
		for _, node := range nodes {
			future := node.raft.GetConfiguration()
			if future.Error() != nil || len(future.Configuration().Servers) != len(nodes) {
				allReady = false
				break
			}
		}
		if allReady {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, node := range nodes {
		future := node.raft.GetConfiguration()
		if err := future.Error(); err != nil || len(future.Configuration().Servers) != len(nodes) {
			t.Fatalf("node %s configuration did not converge: servers=%d err=%v", node.id, len(future.Configuration().Servers), err)
		}
	}

	t.Cleanup(func() {
		for _, node := range nodes {
			_ = node.raft.Shutdown().Error()
			_ = node.transport.Close()
		}
	})
	return nodes
}

func waitBackupForwardLeader(t *testing.T, nodes []*backupForwardTestNode, timeout time.Duration) *backupForwardTestNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.raft.State() == raft.Leader {
				return node
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("backup forwarding cluster did not elect a leader")
	return nil
}

func seedBackupForwardJob(t *testing.T, nodes []*backupForwardTestNode, job clusterModels.BackupJob) {
	t.Helper()
	for _, node := range nodes {
		target := clusterModels.BackupTarget{
			ID: job.TargetID, Name: fmt.Sprintf("target-%d", job.TargetID),
			SSHHost: "root@backup", SSHPort: 22, BackupRoot: "tank/backups", Enabled: true,
		}
		if err := node.service.DB.Create(&target).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&job).Error; err != nil {
			t.Fatalf("seed job on %s: %v", node.id, err)
		}
	}
}

func newBackupRestoreForwardRouter(
	service *clusterService.Service,
	restoreService backupJobRestoreService,
	userID uint,
	username string,
	authType string,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("AuthScope", "local")
		c.Set("UserID", userID)
		c.Set("Username", username)
		c.Set("AuthType", authType)
		c.Next()
	})
	router.POST("/api/cluster/backups/jobs/:id/restore", RestoreBackupJob(service, restoreService))
	return router
}

func newBackupRunForwardRouter(
	service *clusterService.Service,
	runService backupJobRunService,
	userID uint,
	username string,
	authType string,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("AuthScope", "local")
		c.Set("UserID", userID)
		c.Set("Username", username)
		c.Set("AuthType", authType)
		c.Next()
	})
	router.POST("/api/cluster/backups/jobs/:id/run", RunBackupJobNow(service, runService))
	return router
}

type forwardedRequest struct {
	url    string
	header http.Header
	body   []byte
}

func recordForwards(t *testing.T, handler http.Handler) *[]forwardedRequest {
	t.Helper()
	var requests []forwardedRequest
	serveForwards(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, forwardedRequest{url: r.URL.String(), header: r.Header.Clone(), body: body})
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler.ServeHTTP(w, r)
	}))
	return &requests
}

func performBackupForwardRequest(
	t *testing.T,
	router http.Handler,
	path string,
	body []byte,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(forwardRecorder{response}, request)
	return response
}

func TestBackupRestoreForwardsThroughEveryNonRunnerAndOnlyRunnerEnqueues(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "ingress-a", "runner-b", "ingress-c")
	job := clusterModels.BackupJob{
		ID: 61, Name: "forwarded-restore", TargetID: 7, RunnerNodeID: "runner-b",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	seedBackupForwardJob(t, nodes, job)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.ClusterNode{
			NodeUUID: "runner-b", Hostname: "stale-runner", API: "stale.example:9999", Status: "offline",
		}).Error; err != nil {
			t.Fatalf("seed stale node row on %s: %v", node.id, err)
		}
	}

	runnerRestore := &backupRestoreHandlerStub{}
	runnerRouter := newBackupRestoreForwardRouter(nodes[1].service, runnerRestore, 0, "", "")
	forwarded := recordForwards(t, runnerRouter)

	for _, ingressIndex := range []int{0, 2} {
		ingressRestore := &backupRestoreHandlerStub{}
		auth := &backupForwardAuthStub{}
		nodes[ingressIndex].service.AuthService = auth
		router := newBackupRestoreForwardRouter(nodes[ingressIndex].service, ingressRestore, 77, "alice", "sylve")
		requestID := fmt.Sprintf("restore-request-%d", ingressIndex)
		remember := ingressIndex == 0
		response := performBackupForwardRequest(
			t,
			router,
			"/api/cluster/backups/jobs/61/restore",
			[]byte(fmt.Sprintf(
				`{"snapshot":"@bk_j1_c1_test","encryptionKey":"secret","encryptionKeyFormat":"passphrase","rememberEncryptionKey":%t}`,
				remember,
			)),
			map[string]string{"X-Request-ID": requestID, "X-Correlation-ID": "audit-correlation"},
		)
		if response.Code != http.StatusAccepted {
			t.Fatalf("ingress %s response=%d body=%s", nodes[ingressIndex].id, response.Code, response.Body.String())
		}
		if len(ingressRestore.enqueuedJobs) != 0 || len(ingressRestore.registeredKeys) != 0 {
			t.Fatalf("ingress %s executed restore locally: %+v", nodes[ingressIndex].id, ingressRestore)
		}
		if len(auth.calls) != 1 || auth.calls[0] != (backupForwardAuthCall{userID: 77, authType: "sylve"}) {
			t.Fatalf("ingress %s did not preserve audit identity: %+v", nodes[ingressIndex].id, auth.calls)
		}
	}

	if len(runnerRestore.enqueuedJobs) != 2 || runnerRestore.enqueuedJobs[0] != job.ID || runnerRestore.enqueuedJobs[1] != job.ID {
		t.Fatalf("runner enqueue calls = %v, want two exact job calls", runnerRestore.enqueuedJobs)
	}
	if len(runnerRestore.enqueuedSnapshot) != 2 || runnerRestore.enqueuedSnapshot[0] != "@bk_j1_c1_test" {
		t.Fatalf("runner snapshots = %v", runnerRestore.enqueuedSnapshot)
	}
	if len(runnerRestore.registeredKeys) != 1 || runnerRestore.registeredKeys[0] != "secret\x00passphrase" {
		t.Fatalf("runner encryption key registration = %v", runnerRestore.registeredKeys)
	}
	if len(runnerRestore.enqueuedKeys) != 2 || runnerRestore.enqueuedKeys[0] != "" || runnerRestore.enqueuedKeys[1] != "secret" {
		t.Fatalf("passphrases sent with the restores = %q", runnerRestore.enqueuedKeys)
	}
	if len(*forwarded) != 2 {
		t.Fatalf("forwarded request count = %d", len(*forwarded))
	}
	wantProxyUser := serviceInterfaces.ProxiedUser{Username: "admin", AuthType: "sylve"}.Header()
	for index, request := range *forwarded {
		if request.header.Get(authService.ProxyUserHeader) != wantProxyUser ||
			request.header.Get("X-Request-ID") != fmt.Sprintf("restore-request-%d", index*2) ||
			request.header.Get("X-Correlation-ID") != "audit-correlation" {
			t.Fatalf("forwarded headers %d = %+v", index, request.header)
		}
		if request.url != "https://runner-b:8184/api/cluster/backups/jobs/61/restore" {
			t.Fatalf("forwarded URL %d = %q", index, request.url)
		}
		var payload RestoreBackupJobRequest
		if err := json.Unmarshal(request.body, &payload); err != nil || payload.Snapshot != "@bk_j1_c1_test" || payload.EncryptionKey != "secret" {
			t.Fatalf("forwarded body %d = %s err=%v", index, request.body, err)
		}
	}
}

func TestBackupRestoreChecksClaimsNotHealthRows(t *testing.T) {
	tests := []struct {
		name        string
		claimOwner  string
		wantStatus  int
		wantMessage string
		wantEnqueue bool
	}{
		{
			name:        "stale health row is advisory",
			wantStatus:  http.StatusAccepted,
			wantMessage: "restore_job_started",
			wantEnqueue: true,
		},
		{
			name:        "an ID another node holds conflicts",
			claimOwner:  "other-node",
			wantStatus:  http.StatusConflict,
			wantMessage: "restore_guest_id_conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newClusterHandlerTestDB(
				t,
				&clusterModels.BackupTarget{},
				&clusterModels.BackupJob{},
				&clusterModels.Cluster{},
				&clusterModels.ClusterNode{},
				&clusterModels.GuestIdentityClaim{},
				&vmModels.VM{},
				&jailModels.Jail{},
			)
			if err := database.Create(&clusterModels.Cluster{}).Error; err != nil {
				t.Fatalf("seed cluster state: %v", err)
			}
			if test.claimOwner != "" {
				if err := database.Create(&clusterModels.GuestIdentityClaim{
					GuestID: 72, GuestKind: clusterModels.ReplicationGuestTypeVM, OwnerNodeID: test.claimOwner,
				}).Error; err != nil {
					t.Fatalf("seed claim: %v", err)
				}
			}
			if err := database.Create(&clusterModels.ClusterNode{
				NodeUUID: "stale-node",
				GuestIDs: []uint{72},
			}).Error; err != nil {
				t.Fatalf("seed stale health row: %v", err)
			}
			if err := database.Create(&vmModels.VM{RID: 72, Name: "vm-72"}).Error; err != nil {
				t.Fatalf("seed VM: %v", err)
			}
			target := clusterModels.BackupTarget{
				ID: 1, Name: "strict-inventory-target", SSHHost: "root@backup",
				SSHPort: 22, BackupRoot: "tank/backups", Enabled: true,
			}
			if err := database.Create(&target).Error; err != nil {
				t.Fatalf("seed backup target: %v", err)
			}
			job := clusterModels.BackupJob{
				ID: 71, Name: "strict-inventory-restore", TargetID: target.ID,
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "zroot/virtual-machines/72",
				CronExpr: "0 0 * * *", Enabled: true,
			}
			if err := database.Create(&job).Error; err != nil {
				t.Fatalf("seed backup job: %v", err)
			}

			service := newTestClusterService(database, "current-node")
			restoreService := &backupRestoreHandlerStub{}
			router := newBackupRestoreForwardRouter(service, restoreService, 1, "tester", "test")
			response := performBackupForwardRequest(
				t,
				router,
				"/api/cluster/backups/jobs/71/restore",
				[]byte(`{"snapshot":"@bk_j71_c1_test"}`),
				nil,
			)
			if response.Code != test.wantStatus ||
				!strings.Contains(response.Body.String(), test.wantMessage) {
				t.Fatalf(
					"restore response=%d body=%s, want status=%d message=%s",
					response.Code,
					response.Body.String(),
					test.wantStatus,
					test.wantMessage,
				)
			}
			if got := len(restoreService.enqueuedJobs); (got == 1) != test.wantEnqueue {
				t.Fatalf("enqueue calls=%d, wantEnqueue=%t", got, test.wantEnqueue)
			}
		})
	}
}

func TestBackupRestorePreservesRunnerApplicationResponses(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "ingress-a", "runner-b")
	seedBackupForwardJob(t, nodes, clusterModels.BackupJob{
		ID: 62, Name: "response-restore", TargetID: 8, RunnerNodeID: "runner-b",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source",
		CronExpr: "0 0 * * *", Enabled: true,
	})
	nodes[0].service.AuthService = &backupForwardAuthStub{}
	router := newBackupRestoreForwardRouter(nodes[0].service, &backupRestoreHandlerStub{}, 12, "bob", "pam")

	for _, statusCode := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			wantBody := []byte(fmt.Sprintf(`{"status":"error","message":"runner_%d","data":{"jobId":62}}`, statusCode))
			serveForwards(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				_, _ = w.Write(wantBody)
			}))
			response := performBackupForwardRequest(
				t,
				router,
				"/api/cluster/backups/jobs/62/restore",
				[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
				nil,
			)
			if response.Code != statusCode || !bytes.Equal(response.Body.Bytes(), wantBody) {
				t.Fatalf("runner response changed: status=%d body=%s", response.Code, response.Body.Bytes())
			}
		})
	}
}

func TestBackupRestoreForwardingFencesTransportMembershipAndSecondHops(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "ingress-a", "runner-b")
	job := clusterModels.BackupJob{
		ID: 63, Name: "fenced-restore", TargetID: 9, RunnerNodeID: "runner-b",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	seedBackupForwardJob(t, nodes, job)
	nodes[0].service.AuthService = &backupForwardAuthStub{}
	ingressRestore := &backupRestoreHandlerStub{}
	router := newBackupRestoreForwardRouter(nodes[0].service, ingressRestore, 12, "bob", "pam")

	calls := 0
	original := forwardTransport
	forwardTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("runner offline")
	})
	t.Cleanup(func() { forwardTransport = original })
	response := performBackupForwardRequest(
		t,
		router,
		"/api/cluster/backups/jobs/63/restore",
		[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
		nil,
	)
	if response.Code != http.StatusBadGateway || calls != 1 {
		t.Fatalf("offline runner response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	calls = 0
	memberRouter := gin.New()
	memberRouter.Use(func(c *gin.Context) {
		c.Set("AuthScope", "cluster")
		c.Set("ClusterCaller", authService.ClusterCallerUser)
		c.Next()
	})
	memberRouter.POST("/api/cluster/backups/jobs/:id/restore", RestoreBackupJob(nodes[0].service, ingressRestore))
	response = performBackupForwardRequest(
		t,
		memberRouter,
		"/api/cluster/backups/jobs/63/restore",
		[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
		nil,
	)
	if response.Code != http.StatusMisdirectedRequest || calls != 0 {
		t.Fatalf("second hop response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	for _, node := range nodes {
		if err := node.service.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).
			Update("runner_node_id", "removed-node").Error; err != nil {
			t.Fatalf("set removed runner on %s: %v", node.id, err)
		}
	}
	response = performBackupForwardRequest(
		t,
		router,
		"/api/cluster/backups/jobs/63/restore",
		[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
		nil,
	)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "node_not_found") {
		t.Fatalf("removed runner response=%d body=%s", response.Code, response.Body.String())
	}
	if len(ingressRestore.enqueuedJobs) != 0 {
		t.Fatalf("ingress enqueued fenced restore: %v", ingressRestore.enqueuedJobs)
	}
}

func TestRunNowUsesSharedRunnerForwardingProtocol(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "ingress-a", "runner-b")
	seedBackupForwardJob(t, nodes, clusterModels.BackupJob{
		ID: 64, Name: "forwarded-run", TargetID: 10, RunnerNodeID: "runner-b",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source",
		CronExpr: "0 0 * * *", Enabled: true,
	})
	ingressRun := &backupRunHandlerStub{}
	runnerRun := &backupRunHandlerStub{}
	runnerRouter := newBackupRunForwardRouter(nodes[1].service, runnerRun, 0, "", "")
	nodes[0].service.AuthService = &backupForwardAuthStub{}
	recordForwards(t, runnerRouter)

	response := performBackupForwardRequest(
		t,
		newBackupRunForwardRouter(nodes[0].service, ingressRun, 88, "carol", "sylve"),
		"/api/cluster/backups/jobs/64/run",
		[]byte(`{}`),
		map[string]string{"X-Request-ID": "run-request"},
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("run forwarding response=%d body=%s", response.Code, response.Body.String())
	}
	if len(ingressRun.enqueued) != 0 || len(runnerRun.enqueued) != 1 || runnerRun.enqueued[0] != 64 {
		t.Fatalf("run enqueue ingress=%v runner=%v", ingressRun.enqueued, runnerRun.enqueued)
	}
}

func TestRunNowReturnsAcceptedAndBindsAsyncAudit(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	target := clusterModels.BackupTarget{
		ID: 10, Name: "local-target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true,
	}
	job := clusterModels.BackupJob{
		ID: 65, Name: "local-run", TargetID: target.ID,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source", CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := database.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var auditJobID any
	var auditJobType any
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		auditJobID, _ = c.Get("AuditAsyncJobID")
		auditJobType, _ = c.Get("AuditAsyncJobType")
	})
	runService := &backupRunHandlerStub{}
	router.POST(
		"/api/cluster/backups/jobs/:id/run",
		RunBackupJobNow(clusterService.NewClusterService(database, nil, nil).(*clusterService.Service), runService),
	)

	response := performBackupForwardRequest(
		t, router, "/api/cluster/backups/jobs/65/run", []byte(`{}`), nil,
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(runService.enqueued) != 1 || runService.enqueued[0] != job.ID {
		t.Fatalf("enqueued=%v", runService.enqueued)
	}
	if auditJobID != job.ID || auditJobType != "backup_job_run" {
		t.Fatalf("audit binding jobID=%v jobType=%v", auditJobID, auditJobType)
	}
}

func TestBackupTargetRestoreForwardsToTheRestoreNode(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "ingress-a", "restore-b")
	nodes[0].service.AuthService = &backupForwardAuthStub{}
	restoreRouter := gin.New()
	restoreRouter.POST("/api/cluster/backups/targets/:id/restore", func(c *gin.Context) {
		c.JSON(http.StatusAccepted, gin.H{"status": "success"})
	})
	forwarded := recordForwards(t, restoreRouter)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("AuthScope", "local")
		c.Set("UserID", uint(5))
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.POST("/api/cluster/backups/targets/:id/restore", RestoreBackupTargetDataset(nodes[0].service, nil))

	response := performBackupForwardRequest(t, router, "/api/cluster/backups/targets/9/restore",
		[]byte(`{"remoteDataset":"tank/backups/source","snapshot":"@bk_test","destinationDataset":"zroot/restore","restoreNodeId":"restore-b"}`),
		map[string]string{"Idempotency-Key": "restore-operation-1"})
	if response.Code != http.StatusAccepted || len(*forwarded) != 1 {
		t.Fatalf("response=%d body=%s forwarded=%d", response.Code, response.Body.String(), len(*forwarded))
	}
	request := (*forwarded)[0]
	var payload map[string]any
	if err := json.Unmarshal(request.body, &payload); err != nil {
		t.Fatal(err)
	}
	if request.url != "https://restore-b:8184/api/cluster/backups/targets/9/restore" ||
		payload["operationId"] != "restore-operation-1" || payload["restoreNodeId"] != "restore-b" ||
		payload["snapshot"] != "@bk_test" {
		t.Fatalf("forwarded %s with %s", request.url, request.body)
	}
}
