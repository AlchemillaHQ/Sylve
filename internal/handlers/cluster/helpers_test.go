// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type authForwardStub struct {
	serviceInterfaces.AuthServiceInterface
}

func (authForwardStub) CreateClusterJWT(_ uint, _, _, _ string) (string, error) {
	return "test-forward-token", nil
}

func TestMapRaftAddrToAPI(t *testing.T) {
	u, err := mapRaftAddrToAPI("192.168.1.1:8180")
	if err != nil {
		t.Fatalf("mapRaftAddrToAPI: %v", err)
	}
	if u != "https://192.168.1.1:8184" {
		t.Fatalf("expected https://192.168.1.1:8184, got %q", u)
	}

	_, err = mapRaftAddrToAPI("no-port")
	if err == nil {
		t.Fatal("expected error for address without port")
	}

	_, err = mapRaftAddrToAPI("")
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestResolveLeaderAPI(t *testing.T) {
	t.Run("from nodes table", func(t *testing.T) {
		db2 := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})
		s2 := &cluster.Service{DB: db2}
		db2.Create(&clusterModels.ClusterNode{
			NodeUUID: "leader-1", API: "10.0.0.1:8184",
		})
		base := resolveLeaderAPI(s2, "leader-1", "10.0.0.1:8180")
		if base != "https://10.0.0.1:8184" {
			t.Fatalf("expected https://10.0.0.1:8184, got %q", base)
		}
	})

	t.Run("raft fallback", func(t *testing.T) {
		db3 := newClusterHandlerTestDB(t)
		s3 := &cluster.Service{DB: db3}
		base := resolveLeaderAPI(s3, "", "192.168.1.1:8180")
		if base != "https://192.168.1.1:8184" {
			t.Fatalf("expected fallback to raft addr, got %q", base)
		}
	})

	t.Run("empty when nothing resolves", func(t *testing.T) {
		db4 := newClusterHandlerTestDB(t)
		s4 := &cluster.Service{DB: db4}
		base := resolveLeaderAPI(s4, "", "bad")
		if base != "" {
			t.Fatalf("expected empty, got %q", base)
		}
	})
}

func setupSingleRaftForTest(t *testing.T, id string) *raft.Raft {
	t.Helper()
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(id)
	cfg.Logger = hclog.NewNullLogger()
	cfg.HeartbeatTimeout = 200 * time.Millisecond
	cfg.ElectionTimeout = 200 * time.Millisecond
	cfg.LeaderLeaseTimeout = 100 * time.Millisecond
	cfg.CommitTimeout = 25 * time.Millisecond

	store := raft.NewInmemStore()
	snaps := raft.NewInmemSnapshotStore()
	_, transport := raft.NewInmemTransport(raft.ServerAddress(id))
	r, err := raft.NewRaft(cfg, nil, store, store, snaps, transport)
	if err != nil {
		t.Fatalf("raft.NewRaft: %v", err)
	}

	bootstrap := raft.Configuration{
		Servers: []raft.Server{{ID: raft.ServerID(id), Address: raft.ServerAddress(id)}},
	}
	if err := r.BootstrapCluster(bootstrap).Error(); err != nil && err != raft.ErrCantBootstrap {
		t.Fatalf("bootstrap: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if r.State() == raft.Leader {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for leader election")
	return nil
}

func TestForwardToLeader(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})

	// stand up the target server first
	var capturedPath, capturedBody string
	forwardServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		bodyBytes := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(bodyBytes)
		}
		capturedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer forwardServer.Close()

	forwardAddr := strings.TrimPrefix(forwardServer.URL, "https://")

	r := setupSingleRaftForTest(t, "node-1")
	defer func() { _ = r.Shutdown().Error() }()

	s := &cluster.Service{DB: db, Raft: r, AuthService: authForwardStub{}}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-1", API: forwardAddr,
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	reqBody := `{"name":"test","mode":"dataset"}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/cluster/test-endpoint", strings.NewReader(reqBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("UserID", uint(1))
	ctx.Set("Username", "admin")
	ctx.Set("AuthType", "local")

	forwardToLeader(ctx, s)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedPath != "/api/cluster/test-endpoint" {
		t.Fatalf("expected forwarded path /api/cluster/test-endpoint, got %q", capturedPath)
	}
	if capturedBody != reqBody {
		t.Fatalf("expected forwarded body %q, got %q", reqBody, capturedBody)
	}
}

func TestForwardToLeaderBadAPIResolution(t *testing.T) {
	db := newClusterHandlerTestDB(t)

	r := setupSingleRaftForTest(t, "node-nomatch")
	defer func() { _ = r.Shutdown().Error() }()

	// no ClusterNode matching the raft ID, and raft address has no port
	// so resolveLeaderAPI returns empty
	s := &cluster.Service{DB: db, Raft: r}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	forwardToLeader(ctx, s)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}
