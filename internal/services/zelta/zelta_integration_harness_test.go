// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

type zeltaRaftNode struct {
	id        string
	addr      raft.ServerAddress
	transport *raft.InmemTransport
	raft      *raft.Raft
	db        *gorm.DB
	cService  *cluster.Service
}

type ZeltaClusterFixture struct {
	Nodes       []*zeltaRaftNode
	LocalNodeID string
	DB          *gorm.DB
	ClusterSvc  *cluster.Service
	Raft        *raft.Raft
}

func (f *ZeltaClusterFixture) LeaderNode() *zeltaRaftNode {
	for _, n := range f.Nodes {
		if n.raft != nil && n.raft.State() == raft.Leader {
			return n
		}
	}
	return nil
}

func (f *ZeltaClusterFixture) FollowerNode() *zeltaRaftNode {
	for _, n := range f.Nodes {
		if n.raft != nil && n.raft.State() == raft.Follower {
			return n
		}
	}
	return nil
}

func (f *ZeltaClusterFixture) SeedPolicy(policy *clusterModels.ReplicationPolicy) {
	if err := clusterModels.UpsertReplicationPolicyTxn(f.DB, policy, policy.Targets); err != nil {
		panic(fmt.Sprintf("SeedPolicy: %v", err))
	}
}

func (f *ZeltaClusterFixture) SeedLease(lease *clusterModels.ReplicationLease) {
	if err := f.DB.Create(lease).Error; err != nil {
		panic(fmt.Sprintf("SeedLease: %v", err))
	}
}

func (f *ZeltaClusterFixture) SeedClusterNode(node *clusterModels.ClusterNode) {
	if err := f.DB.Create(node).Error; err != nil {
		panic(fmt.Sprintf("SeedClusterNode: %v", err))
	}
}

func (f *ZeltaClusterFixture) SetNodeStatus(nodeUUID, status string) {
	f.DB.Model(&clusterModels.ClusterNode{}).Where("node_uuid = ?", nodeUUID).Update("status", status)
}

func (f *ZeltaClusterFixture) NewZeltaService() *Service {
	return &Service{
		DB:                 f.DB,
		Cluster:            f.ClusterSvc,
		runningReplication: make(map[uint]struct{}),
		runningTransitions: make(map[uint]struct{}),
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		poolDownMisses:     make(map[string]int),
		runningWorkloadOp:  make(map[string]string),
	}
}

func (f *ZeltaClusterFixture) WaitForCondition(timeout time.Duration, description string, fn func() bool) {
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
	panic(fmt.Sprintf("timed out waiting for %s", description))
}

func (f *ZeltaClusterFixture) DisconnectNode(nodeID string) {
	for _, n := range f.Nodes {
		if n.id == nodeID {
			n.transport.DisconnectAll()
			return
		}
	}
}

func newZeltaRaftNode(t *testing.T, id string, models ...any) *zeltaRaftNode {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, models...)
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
	r, err := raft.NewRaft(cfg, fsm, raft.NewInmemStore(), raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatalf("raft.NewRaft(%s): %v", id, err)
	}

	return &zeltaRaftNode{
		id: id, addr: addr, transport: transport, raft: r, db: db,
		cService: &cluster.Service{DB: db, Raft: r},
	}
}

func SetupZeltaClusterFixture(t *testing.T, nodeCount int) *ZeltaClusterFixture {
	t.Helper()

	if nodeCount < 1 || nodeCount > 5 {
		t.Fatalf("nodeCount must be 1-5, got %d", nodeCount)
	}

	cfg := &internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}
	if err := db.SetupQueue(cfg, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("SetupQueue: %v", err)
	}
	db.SetupCache(cfg)

	localNodeID, err := utils.GetSystemUUID()
	if err != nil {
		t.Fatalf("GetSystemUUID: %v", err)
	}
	localNodeID = strings.TrimSpace(localNodeID)

	models := []any{
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationReceipt{},
		&clusterModels.ClusterNode{},
		&clusterModels.Cluster{},
	}

	nodes := make([]*zeltaRaftNode, 0, nodeCount)
	nodeIDs := make([]string, nodeCount)
	nodeIDs[0] = localNodeID
	for i := 1; i < nodeCount; i++ {
		nodeIDs[i] = fmt.Sprintf("node-%d", i+1)
	}

	for i := 0; i < nodeCount; i++ {
		nodes = append(nodes, newZeltaRaftNode(t, nodeIDs[i], models...))
	}

	for i := 0; i < nodeCount; i++ {
		for j := 0; j < nodeCount; j++ {
			if i == j {
				continue
			}
			nodes[i].transport.Connect(nodes[j].addr, nodes[j].transport)
		}
	}

	bootstrap := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(nodeIDs[0]), Address: nodes[0].addr},
		},
	}
	if err := nodes[0].raft.BootstrapCluster(bootstrap).Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
		t.Fatalf("bootstrap: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[0].raft.State() == raft.Leader {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if nodes[0].raft.State() != raft.Leader {
		t.Fatalf("node %s did not become leader", nodeIDs[0])
	}

	for i := 1; i < nodeCount; i++ {
		addVoterRetry(t, nodes, nodes[i], 8*time.Second)
	}

	waitVoterCount(t, nodes, nodeCount, 8*time.Second)

	for i := 0; i < nodeCount; i++ {
		localNode := clusterModels.ClusterNode{
			NodeUUID: nodeIDs[i], Hostname: fmt.Sprintf("node%d", i+1),
			API: fmt.Sprintf("node%d:8181", i+1), Status: "online",
		}
		for j := 0; j < nodeCount; j++ {
			nodes[j].db.Create(&localNode)
		}
	}

	return &ZeltaClusterFixture{
		Nodes:       nodes,
		LocalNodeID: localNodeID,
		DB:          nodes[0].db,
		ClusterSvc:  nodes[0].cService,
		Raft:        nodes[0].raft,
	}
}

func addVoterRetry(t *testing.T, nodes []*zeltaRaftNode, joining *zeltaRaftNode, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var leader *zeltaRaftNode
		for _, n := range nodes {
			if n.raft != nil && n.raft.State() == raft.Leader {
				leader = n
				break
			}
		}
		if leader == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		err := leader.raft.AddVoter(raft.ServerID(joining.id), joining.addr, 0, 5*time.Second).Error()
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
		t.Fatalf("AddVoter(%s): %v", joining.id, err)
	}
	t.Fatalf("timed out adding voter %s", joining.id)
}

func waitVoterCount(t *testing.T, nodes []*zeltaRaftNode, expected int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var leader *zeltaRaftNode
		for _, n := range nodes {
			if n.raft != nil && n.raft.State() == raft.Leader {
				leader = n
				break
			}
		}
		if leader == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		future := leader.raft.GetConfiguration()
		if err := future.Error(); err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		servers := future.Configuration().Servers
		if len(servers) != expected {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ok := true
		for _, s := range servers {
			if s.Suffrage != raft.Voter {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d voters", expected)
}

func (f *ZeltaClusterFixture) Cleanup() {
	for _, n := range f.Nodes {
		if n.raft != nil && n.raft.State() != raft.Shutdown {
			_ = n.raft.Shutdown().Error()
		}
		if n.transport != nil {
			_ = n.transport.Close()
		}
		if n.db != nil {
			if sqlDB, err := n.db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	}
	if db.CacheDB != nil {
		_ = db.CacheDB.Close()
		db.CacheDB = nil
	}
}
