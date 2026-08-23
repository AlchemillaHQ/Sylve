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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
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
	username string
	authType string
}

type backupForwardAuthStub struct {
	serviceInterfaces.AuthServiceInterface
	mu    sync.Mutex
	calls []backupForwardAuthCall
}

func (s *backupForwardAuthStub) CreateUserProxyJWT(userID uint, username, authType string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, backupForwardAuthCall{userID: userID, username: username, authType: authType})
	return "signed-user-proxy", nil
}

type backupRestoreHandlerStub struct {
	mu               sync.Mutex
	registeredKeys   []string
	enqueuedJobs     []uint
	enqueuedSnapshot []string
	registerErr      error
	enqueueErr       error
}

func (s *backupRestoreHandlerStub) RegisterRestoreEncryptionKey(key, format string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registeredKeys = append(s.registeredKeys, key+"\x00"+format)
	return s.registerErr
}

func (s *backupRestoreHandlerStub) EnqueueRestoreJob(_ context.Context, jobID uint, snapshot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueuedJobs = append(s.enqueuedJobs, jobID)
	s.enqueuedSnapshot = append(s.enqueuedSnapshot, snapshot)
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
			service: &clusterService.Service{DB: database, Raft: r, NodeID: nodeID},
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

func dispatchBackupForwardToRouter(
	router http.Handler,
	capture func(string, map[string]string, []byte),
) func(context.Context, string, any, map[string]string) (clusterForwardResponse, error) {
	return func(ctx context.Context, targetURL string, payload any, headers map[string]string) (clusterForwardResponse, error) {
		parsed, err := url.Parse(targetURL)
		if err != nil {
			return clusterForwardResponse{}, err
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return clusterForwardResponse{}, err
		}
		if capture != nil {
			copyHeaders := make(map[string]string, len(headers))
			for key, value := range headers {
				copyHeaders[key] = value
			}
			capture(targetURL, copyHeaders, body)
		}
		request := httptest.NewRequest(http.MethodPost, parsed.Path, bytes.NewReader(body)).WithContext(ctx)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return clusterForwardResponse{
			StatusCode: response.Code,
			Header:     response.Header().Clone(),
			Body:       append([]byte(nil), response.Body.Bytes()...),
		}, nil
	}
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
	router.ServeHTTP(response, request)
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
	var capturedHeaders []map[string]string
	var capturedURLs []string
	var capturedBodies [][]byte
	originalForward := backupJobForwardHTTP
	backupJobForwardHTTP = dispatchBackupForwardToRouter(runnerRouter, func(targetURL string, headers map[string]string, body []byte) {
		capturedURLs = append(capturedURLs, targetURL)
		capturedHeaders = append(capturedHeaders, headers)
		capturedBodies = append(capturedBodies, append([]byte(nil), body...))
	})
	t.Cleanup(func() { backupJobForwardHTTP = originalForward })

	for _, ingressIndex := range []int{0, 2} {
		ingressRestore := &backupRestoreHandlerStub{}
		auth := &backupForwardAuthStub{}
		nodes[ingressIndex].service.AuthService = auth
		router := newBackupRestoreForwardRouter(nodes[ingressIndex].service, ingressRestore, 77, "alice", "sylve")
		requestID := fmt.Sprintf("restore-request-%d", ingressIndex)
		response := performBackupForwardRequest(
			t,
			router,
			"/api/cluster/backups/jobs/61/restore",
			[]byte(`{"snapshot":"@bk_j1_c1_test","encryptionKey":"secret","encryptionKeyFormat":"passphrase"}`),
			map[string]string{"X-Request-ID": requestID, "X-Correlation-ID": "audit-correlation"},
		)
		if response.Code != http.StatusAccepted {
			t.Fatalf("ingress %s response=%d body=%s", nodes[ingressIndex].id, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Request-ID") != requestID {
			t.Fatalf("ingress %s lost request ID", nodes[ingressIndex].id)
		}
		if len(ingressRestore.enqueuedJobs) != 0 || len(ingressRestore.registeredKeys) != 0 {
			t.Fatalf("ingress %s executed restore locally: %+v", nodes[ingressIndex].id, ingressRestore)
		}
		if len(auth.calls) != 1 || auth.calls[0] != (backupForwardAuthCall{userID: 77, username: "alice", authType: "sylve"}) {
			t.Fatalf("ingress %s did not preserve audit identity: %+v", nodes[ingressIndex].id, auth.calls)
		}
	}

	if len(runnerRestore.enqueuedJobs) != 2 || runnerRestore.enqueuedJobs[0] != job.ID || runnerRestore.enqueuedJobs[1] != job.ID {
		t.Fatalf("runner enqueue calls = %v, want two exact job calls", runnerRestore.enqueuedJobs)
	}
	if len(runnerRestore.enqueuedSnapshot) != 2 || runnerRestore.enqueuedSnapshot[0] != "@bk_j1_c1_test" {
		t.Fatalf("runner snapshots = %v", runnerRestore.enqueuedSnapshot)
	}
	if len(runnerRestore.registeredKeys) != 2 || runnerRestore.registeredKeys[0] != "secret\x00passphrase" {
		t.Fatalf("runner encryption key registration = %v", runnerRestore.registeredKeys)
	}
	if len(capturedHeaders) != 2 {
		t.Fatalf("forwarded request count = %d", len(capturedHeaders))
	}
	for index, headers := range capturedHeaders {
		if headers[backupJobForwardHopHeader] != "1" ||
			headers[backupJobForwardedTargetHeader] != "runner-b" ||
			headers[backupJobForwardedByHeader] != nodes[index*2].id ||
			headers["X-Cluster-Token"] != "Bearer signed-user-proxy" ||
			headers["X-Request-ID"] != fmt.Sprintf("restore-request-%d", index*2) ||
			headers["X-Correlation-ID"] != "audit-correlation" {
			t.Fatalf("forwarded headers %d = %+v", index, headers)
		}
		if !strings.Contains(capturedURLs[index], "runner-b:8184/api/cluster/backups/jobs/61/restore") {
			t.Fatalf("forwarded URL %d = %q", index, capturedURLs[index])
		}
		var payload RestoreBackupJobRequest
		if err := json.Unmarshal(capturedBodies[index], &payload); err != nil || payload.Snapshot != "@bk_j1_c1_test" || payload.EncryptionKey != "secret" {
			t.Fatalf("forwarded body %d = %s err=%v", index, capturedBodies[index], err)
		}
	}
}

func TestBackupRestoreUsesStrictInventoryInsteadOfHealthRows(t *testing.T) {
	tests := []struct {
		name        string
		clustered   bool
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
			name:        "unavailable voter inventory is distinct",
			clustered:   true,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "restore_guest_identity_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(
				t,
				&clusterModels.BackupTarget{},
				&clusterModels.BackupJob{},
				&clusterModels.Cluster{},
				&clusterModels.ClusterNode{},
				&vmModels.VM{},
				&jailModels.Jail{},
			)
			if err := database.Create(&clusterModels.Cluster{Enabled: test.clustered}).Error; err != nil {
				t.Fatalf("seed cluster state: %v", err)
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

			service := &clusterService.Service{DB: database, NodeID: "current-node"}
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
	originalForward := backupJobForwardHTTP
	t.Cleanup(func() { backupJobForwardHTTP = originalForward })

	for _, statusCode := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			wantBody := []byte(fmt.Sprintf(`{"status":"error","message":"runner_%d","data":{"jobId":62}}`, statusCode))
			backupJobForwardHTTP = func(context.Context, string, any, map[string]string) (clusterForwardResponse, error) {
				return clusterForwardResponse{
					StatusCode: statusCode,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       wantBody,
				}, nil
			}
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

func TestBackupRestoreForwardingFencesTransportMembershipIdentityAndLoops(t *testing.T) {
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
	originalForward := backupJobForwardHTTP
	t.Cleanup(func() { backupJobForwardHTTP = originalForward })

	calls := 0
	backupJobForwardHTTP = func(context.Context, string, any, map[string]string) (clusterForwardResponse, error) {
		calls++
		return clusterForwardResponse{}, errors.New("runner offline")
	}
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
	response = performBackupForwardRequest(
		t,
		router,
		"/api/cluster/backups/jobs/63/restore",
		[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
		map[string]string{backupJobForwardHopHeader: "1"},
	)
	if response.Code != http.StatusLoopDetected || calls != 0 {
		t.Fatalf("forward loop response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	nodes[0].service.NodeID = ""
	response = performBackupForwardRequest(
		t,
		router,
		"/api/cluster/backups/jobs/63/restore",
		[]byte(`{"snapshot":"@bk_j1_c1_test"}`),
		nil,
	)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "local_identity_unavailable") {
		t.Fatalf("missing local identity response=%d body=%s", response.Code, response.Body.String())
	}
	nodes[0].service.NodeID = "ingress-a"

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
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "backup_runner_unavailable") {
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
	originalForward := backupJobForwardHTTP
	backupJobForwardHTTP = dispatchBackupForwardToRouter(runnerRouter, nil)
	t.Cleanup(func() { backupJobForwardHTTP = originalForward })

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
		RunBackupJobNow(&clusterService.Service{DB: database}, runService),
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
