// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestIntegrationRaftConcurrentProposals(t *testing.T) {
	concurrency := 10
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			title := fmt.Sprintf("note-%d", idx)
			if err := leader.service.ProposeNoteCreate(title, "content", false); err != nil {
				t.Errorf("concurrent create %d failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	waitForClusterCondition(t, 8*time.Second, "all concurrent notes replicated", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != concurrency {
				return false
			}
		}
		return true
	})

	notes, err := notesForNode(leader)
	if err != nil {
		t.Fatalf("failed to read notes from leader: %v", err)
	}
	if len(notes) != concurrency {
		t.Fatalf("expected %d notes after concurrent creates, got %d", concurrency, len(notes))
	}
}

func TestIntegrationRaftConcurrentCreateDeleteRace(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterNote{})

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	if err := leader.service.ProposeNoteCreate("race-target", "original", false); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "seed note replicated", func() bool {
		for _, node := range nodes {
			notes, err := notesForNode(node)
			if err != nil || len(notes) != 1 {
				return false
			}
		}
		return true
	})

	var seedNotes []clusterModels.ClusterNote
	leader.service.DB.Order("id ASC").Find(&seedNotes)
	if len(seedNotes) != 1 {
		t.Fatalf("expected 1 seed note, got %d", len(seedNotes))
	}
	noteID := int(seedNotes[0].ID)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = leader.service.ProposeNoteUpdate(noteID, "updated-by-racer", "content", false)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = leader.service.ProposeNoteDelete(noteID, false)
	}()

	wg.Wait()

	waitForClusterCondition(t, 8*time.Second, "concurrent op resolved", func() bool {
		for _, node := range nodes {
			var current []clusterModels.ClusterNote
			node.service.DB.Order("id ASC").Find(&current)
			if len(current) > 1 {
				return false
			}
		}
		return true
	})

	for _, node := range nodes {
		var current []clusterModels.ClusterNote
		node.service.DB.Order("id ASC").Find(&current)
		if len(current) > 1 {
			t.Fatalf("node %s: unexpected note count %d after race", node.id, len(current))
		}
		if len(current) == 1 && current[0].ID != uint(noteID) {
			t.Logf("node %s: surviving note has different ID %d (ok — raft total order)", node.id, current[0].ID)
		}
	}
}
