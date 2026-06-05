// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestConcurrentRaftProposals(t *testing.T) {
	concurrency := 10
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	defer cleanupClusterRaftTestNodes(t, nodes)

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

func TestConcurrentCreateDeleteRace(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterNote{})
	defer cleanupClusterRaftTestNodes(t, nodes)

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

func TestConcurrentBackupJobStateUpdates(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 1, Name: "state-race", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "* * * * *", Enabled: true,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job seeded", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	var wg sync.WaitGroup
	readerDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-readerDone:
				return
			default:
				var j clusterModels.BackupJob
				_ = leader.service.DB.First(&j, 1).Error
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	updateCount := 5
	for i := 0; i < updateCount; i++ {
		stateRaw, _ := json.Marshal(map[string]any{
			"jobId":      1,
			"lastStatus": "success",
			"lastRunAt":  time.Now().UTC(),
			"encrypted":  i%2 == 0,
		})
		if err := leader.service.applyRaftCommand(clusterModels.Command{
			Type: "backup_job_state", Action: "update", Data: stateRaw,
		}); err != nil {
			t.Errorf("state update %d failed: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(readerDone)
	wg.Wait()

	waitForClusterCondition(t, 8*time.Second, "state updates converged", func() bool {
		for _, n := range nodes {
			var j clusterModels.BackupJob
			if err := n.service.DB.First(&j, 1).Error; err != nil {
				return false
			}
			if j.LastStatus != "success" {
				return false
			}
		}
		return true
	})
}

func TestConcurrentBackupJobReadWriteRaces(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 1, Name: "rw-race-job", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "* * * * *", Enabled: true,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job seeded", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	var wg sync.WaitGroup
	runDuration := 500 * time.Millisecond
	stop := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					var j clusterModels.BackupJob
					_ = leader.service.DB.First(&j, 1).Error
				}
			}
		}()

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					stateRaw, _ := json.Marshal(map[string]any{
						"jobId":      1,
						"lastStatus": "success",
						"encrypted":  idx%2 == 0,
					})
					_ = leader.service.applyRaftCommand(clusterModels.Command{
						Type: "backup_job_state", Action: "update", Data: stateRaw,
					})
				}
			}
		}(i)
	}

	time.Sleep(runDuration)
	close(stop)
	wg.Wait()

	for _, n := range nodes {
		var j clusterModels.BackupJob
		if err := n.service.DB.First(&j, 1).Error; err != nil {
			t.Fatalf("node %s: job lookup failed: %v", n.id, err)
		}
		if j.Name != "rw-race-job" {
			t.Fatalf("node %s: corrupted job name: %q", n.id, j.Name)
		}
	}
}
