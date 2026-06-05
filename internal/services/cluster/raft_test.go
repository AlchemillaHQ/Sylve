// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestHasExistingRaftState(t *testing.T) {
	dir := t.TempDir()
	if hasExistingRaftState(dir) {
		t.Fatal("expected false for empty dir")
	}

	// create a raft-log.db file
	if err := os.WriteFile(filepath.Join(dir, "raft-log.db"), []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !hasExistingRaftState(dir) {
		t.Fatal("expected true with raft-log.db present")
	}

	// clean and test snapshots dir
	os.Remove(filepath.Join(dir, "raft-log.db"))
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	if !hasExistingRaftState(dir) {
		t.Fatal("expected true with snapshots dir present")
	}
}

func TestCleanRaftDir(t *testing.T) {
	dir := t.TempDir()

	// override data path for the test
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	// create fake raft state
	raftDir := filepath.Join(dir, "raft")
	if err := os.MkdirAll(raftDir, 0755); err != nil {
		t.Fatalf("mkdir raft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(raftDir, "raft-log.db"), []byte("data"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(raftDir, "raft-stable.db"), []byte("data"), 0644); err != nil {
		t.Fatalf("write stable: %v", err)
	}

	db := newClusterServiceTestDB(t)
	s := &Service{DB: db}

	if err := s.CleanRaftDir(); err != nil {
		t.Fatalf("CleanRaftDir: %v", err)
	}

	// verify directory is empty
	entries, _ := os.ReadDir(raftDir)
	if len(entries) != 0 {
		t.Fatalf("expected empty raft dir, got %d entries", len(entries))
	}

	// cleaning non-existent dir should not error
	if err := s.CleanRaftDir(); err != nil {
		t.Fatalf("CleanRaftDir on empty dir: %v", err)
	}
}

func TestClearClusterNode(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-to-clear", Hostname: "test", API: "10.0.0.1:8184",
		Status: "online",
	})

	if err := s.ClearClusterNode("node-to-clear"); err != nil {
		t.Fatalf("ClearClusterNode: %v", err)
	}

	var count int64
	db.Model(&clusterModels.ClusterNode{}).Where("node_uuid = ?", "node-to-clear").Count(&count)
	if count != 0 {
		t.Fatalf("expected node cleared, got %d", count)
	}
}

func TestRemovePeer(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	var follower *clusterRaftTestNode
	for _, n := range nodes {
		if n.id != leader.id {
			follower = n
			break
		}
	}

	t.Run("remove existing peer", func(t *testing.T) {
		err := leader.service.RemovePeer(raft.ServerID(follower.id))
		if err != nil {
			t.Fatalf("RemovePeer: %v", err)
		}

		waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
	})

	t.Run("remove non-existent peer returns error", func(t *testing.T) {
		err := leader.service.RemovePeer("non-existent-id")
		if err != nil {
			t.Fatalf("remove non-existent should not error: %v", err)
		}
	})

	t.Run("remove as follower returns error", func(t *testing.T) {
		newLeader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
		for _, n := range nodes {
			if n.id != newLeader.id {
				err := n.service.RemovePeer("any-id")
				if err == nil {
					t.Fatal("expected error when removing peer as follower")
				}
				break
			}
		}
	})
}
