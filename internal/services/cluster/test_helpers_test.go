// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newClusterServiceTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if len(migrateModels) > 0 {
		if err := db.AutoMigrate(migrateModels...); err != nil {
			t.Fatalf("failed to migrate test tables: %v", err)
		}
	}

	return db
}

type clusterRaftTestNode struct {
	id        string
	addr      raft.ServerAddress
	transport *raft.InmemTransport
	raft      *raft.Raft
	service   *Service
}

func newClusterRaftTestNode(t *testing.T, id string, migrateModels ...any) *clusterRaftTestNode {
	t.Helper()

	db := newClusterServiceTestDB(t, migrateModels...)
	fsm := clusterModels.NewFSMDispatcher(db)
	clusterModels.RegisterDefaultHandlers(fsm)

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(id)
	cfg.Logger = hclog.NewNullLogger()
	cfg.HeartbeatTimeout = 200 * time.Millisecond
	cfg.ElectionTimeout = 200 * time.Millisecond
	cfg.LeaderLeaseTimeout = 100 * time.Millisecond
	cfg.CommitTimeout = 25 * time.Millisecond

	addr, transport := raft.NewInmemTransport(raft.ServerAddress(id))
	r, err := raft.NewRaft(
		cfg,
		fsm,
		raft.NewInmemStore(),
		raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(),
		transport,
	)
	if err != nil {
		t.Fatalf("failed to create raft node %s: %v", id, err)
	}

	return &clusterRaftTestNode{
		id:        id,
		addr:      addr,
		transport: transport,
		raft:      r,
		service: &Service{
			DB:   db,
			Raft: r,
		},
	}
}

func connectClusterRaftTestNodes(nodes []*clusterRaftTestNode) {
	for _, src := range nodes {
		for _, dst := range nodes {
			if src.id == dst.id {
				continue
			}
			src.transport.Connect(dst.addr, dst.transport)
		}
	}
}

func waitForClusterCondition(t *testing.T, timeout time.Duration, description string, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	if fn() {
		return
	}

	t.Fatalf("timed out waiting for %s", description)
}

func findClusterRaftLeader(nodes []*clusterRaftTestNode) *clusterRaftTestNode {
	for _, node := range nodes {
		if node.raft != nil && node.raft.State() == raft.Leader {
			return node
		}
	}
	return nil
}

func waitForClusterRaftLeader(t *testing.T, nodes []*clusterRaftTestNode, timeout time.Duration) *clusterRaftTestNode {
	t.Helper()

	var leader *clusterRaftTestNode
	waitForClusterCondition(t, timeout, "leader election", func() bool {
		leader = findClusterRaftLeader(nodes)
		return leader != nil
	})

	return leader
}

func addClusterRaftVoterWithRetry(t *testing.T, nodes []*clusterRaftTestNode, joiningNode *clusterRaftTestNode) {
	t.Helper()

	deadline := time.Now().Add(8 * time.Second)

	for time.Now().Before(deadline) {
		leader := findClusterRaftLeader(nodes)
		if leader == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		err := leader.raft.AddVoter(raft.ServerID(joiningNode.id), joiningNode.addr, 0, 5*time.Second).Error()
		if err == nil {
			return
		}

		msg := err.Error()
		if strings.Contains(msg, "not leader") || strings.Contains(msg, "leadership lost") {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if strings.Contains(msg, "already a voter") || strings.Contains(msg, "already in configuration") {
			return
		}

		t.Fatalf("AddVoter(%s) failed: %v", joiningNode.id, err)
	}

	t.Fatalf("timed out adding voter %s", joiningNode.id)
}

func waitForClusterRaftVoterCount(t *testing.T, nodes []*clusterRaftTestNode, expected int, timeout time.Duration) {
	t.Helper()

	waitForClusterCondition(t, timeout, "raft voter count convergence", func() bool {
		leader := findClusterRaftLeader(nodes)
		if leader == nil {
			return false
		}

		future := leader.raft.GetConfiguration()
		if err := future.Error(); err != nil {
			return false
		}

		servers := future.Configuration().Servers
		if len(servers) != expected {
			return false
		}

		for _, server := range servers {
			if server.Suffrage != raft.Voter {
				return false
			}
		}

		return true
	})
}

func setupClusterRaftTestNodes(t *testing.T, nodeCount int, migrateModels ...any) []*clusterRaftTestNode {
	t.Helper()

	if nodeCount < 1 {
		t.Fatal("nodeCount must be at least 1")
	}

	nodes := make([]*clusterRaftTestNode, 0, nodeCount)
	for i := 1; i <= nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		nodes = append(nodes, newClusterRaftTestNode(t, nodeID, migrateModels...))
	}

	connectClusterRaftTestNodes(nodes)

	bootstrap := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      raft.ServerID(nodes[0].id),
				Address: nodes[0].addr,
			},
		},
	}

	if err := nodes[0].raft.BootstrapCluster(bootstrap).Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
		t.Fatalf("failed to bootstrap raft cluster: %v", err)
	}

	waitForClusterRaftLeader(t, nodes, 8*time.Second)

	for i := 1; i < len(nodes); i++ {
		addClusterRaftVoterWithRetry(t, nodes, nodes[i])
	}

	waitForClusterRaftVoterCount(t, nodes, nodeCount, 8*time.Second)
	return nodes
}

func cleanupClusterRaftTestNodes(t *testing.T, nodes []*clusterRaftTestNode) {
	t.Helper()

	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.raft != nil && node.raft.State() != raft.Shutdown {
			if err := node.raft.Shutdown().Error(); err != nil {
				t.Logf("raft shutdown warning for %s: %v", node.id, err)
			}
		}
		if node.transport != nil {
			_ = node.transport.Close()
		}
		if node.service != nil && node.service.DB != nil {
			if sqlDB, err := node.service.DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	}
}
