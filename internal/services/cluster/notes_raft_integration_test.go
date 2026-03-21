// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func notesForNode(node *clusterRaftTestNode) ([]clusterModels.ClusterNote, error) {
	var notes []clusterModels.ClusterNote
	err := node.service.DB.Order("id ASC").Find(&notes).Error
	return notes, err
}

func TestRaftNotesReplicationTwoNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterNote{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeNoteCreate("first", "content", false); err != nil {
		t.Fatalf("leader failed to create note through raft: %v", err)
	}

	noteID := 0
	waitForClusterCondition(t, 8*time.Second, "note create replication to 2 nodes", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 1 || notes[0].Title != "first" || notes[0].Content != "content" {
				return false
			}
			if noteID == 0 {
				noteID = int(notes[0].ID)
			}
			if int(notes[0].ID) != noteID {
				return false
			}
		}
		return noteID > 0
	})

	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeNoteUpdate(noteID, "first-updated", "content-updated", false); err != nil {
		t.Fatalf("leader failed to update note through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "note update replication to 2 nodes", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 1 {
				return false
			}
			if notes[0].Title != "first-updated" || notes[0].Content != "content-updated" {
				return false
			}
		}
		return true
	})

	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeNoteDelete(noteID, false); err != nil {
		t.Fatalf("leader failed to delete note through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "note delete replication to 2 nodes", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 0 {
				return false
			}
		}
		return true
	})
}

func TestRaftNotesReplicationThreeNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeNoteCreate("three-node", "replicated", false); err != nil {
		t.Fatalf("leader failed to create note through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "note replication to 3 nodes", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 1 {
				return false
			}
			if notes[0].Title != "three-node" || notes[0].Content != "replicated" {
				return false
			}
		}
		return true
	})
}

func TestRaftNotesThreeNodeFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	initialLeader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := initialLeader.service.ProposeNoteCreate("before-failover", "first-write", false); err != nil {
		t.Fatalf("initial leader failed to create note: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "initial replication before failover", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 1 {
				return false
			}
			if notes[0].Title != "before-failover" {
				return false
			}
		}
		return true
	})

	survivors := make([]*clusterRaftTestNode, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.id != initialLeader.id {
			survivors = append(survivors, node)
		}
	}

	for _, node := range survivors {
		node.transport.Disconnect(initialLeader.addr)
	}
	initialLeader.transport.DisconnectAll()
	if err := initialLeader.raft.Shutdown().Error(); err != nil {
		t.Fatalf("failed to shutdown initial leader: %v", err)
	}

	newLeader := waitForClusterRaftLeader(t, survivors, 12*time.Second)
	if err := newLeader.service.ProposeNoteCreate("after-failover", "second-write", false); err != nil {
		t.Fatalf("new leader failed to create note after failover: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "post-failover replication on surviving quorum", func() bool {
		for _, node := range survivors {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 2 {
				return false
			}
			if notes[0].Title != "before-failover" {
				return false
			}
			if notes[1].Title != "after-failover" {
				return false
			}
		}
		return true
	})
}
