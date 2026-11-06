// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type authForwardStub struct {
	serviceInterfaces.AuthServiceInterface
}

func (authForwardStub) ProxiedUser(userID uint, authType string) (serviceInterfaces.ProxiedUser, error) {
	return serviceInterfaces.ProxiedUser{Username: "admin", AuthType: authType}, nil
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
	t.Run("authoritative raft address ignores stale node API", func(t *testing.T) {
		db2 := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})
		s2 := cluster.NewClusterService(db2, nil, nil).(*cluster.Service)
		db2.Create(&clusterModels.ClusterNode{
			NodeUUID: "leader-1", API: "203.0.113.99:9999",
		})
		base := resolveLeaderAPI(s2, "leader-1", "10.0.0.1:8180")
		if base != "https://10.0.0.1:8184" {
			t.Fatalf("expected https://10.0.0.1:8184, got %q", base)
		}
	})

	t.Run("raft fallback", func(t *testing.T) {
		db3 := newClusterHandlerTestDB(t)
		s3 := cluster.NewClusterService(db3, nil, nil).(*cluster.Service)
		base := resolveLeaderAPI(s3, "", "192.168.1.1:8180")
		if base != "https://192.168.1.1:8184" {
			t.Fatalf("expected fallback to raft addr, got %q", base)
		}
	})

	t.Run("empty when nothing resolves", func(t *testing.T) {
		db4 := newClusterHandlerTestDB(t)
		s4 := cluster.NewClusterService(db4, nil, nil).(*cluster.Service)
		base := resolveLeaderAPI(s4, "", "bad")
		if base != "" {
			t.Fatalf("expected empty, got %q", base)
		}
	})
}

func withRaft(s *cluster.Service, r *raft.Raft) *cluster.Service {
	s.SetRaft(r)
	return s
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
			// A new leader applies its startup entries asynchronously; wait so
			// tests never see AppliedIndex advance on its own.
			if err := r.Barrier(2 * time.Second).Error(); err != nil {
				t.Fatalf("raft barrier: %v", err)
			}
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for leader election")
	return nil
}
