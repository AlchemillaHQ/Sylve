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
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestRaftBackupJobCRUDTwoNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupJob{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	job := clusterModels.BackupJob{
		ID: 1, Name: "raft-job", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 2 * * *", Enabled: true,
	}
	createRaw, _ := json.Marshal(job)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create job via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job create replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 1 {
				return false
			}
			var j clusterModels.BackupJob
			n.service.DB.First(&j, 1)
			if j.Name != "raft-job" || j.Mode != clusterModels.BackupJobModeDataset {
				return false
			}
		}
		return true
	})

	// update
	job.Name = "raft-job-updated"
	job.PruneKeepLast = 30
	job.PruneTarget = true
	job.Enabled = false
	updateRaw, _ := json.Marshal(job)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "update", Data: updateRaw,
	}); err != nil {
		t.Fatalf("update job via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job update replicated", func() bool {
		for _, n := range nodes {
			var j clusterModels.BackupJob
			if err := n.service.DB.First(&j, 1).Error; err != nil {
				return false
			}
			if j.Name != "raft-job-updated" {
				return false
			}
			if j.PruneKeepLast != 30 || !j.PruneTarget {
				return false
			}
			if j.Enabled {
				return false
			}
		}
		return true
	})

	// delete
	deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "delete", Data: deleteRaw,
	}); err != nil {
		t.Fatalf("delete job via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job delete replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 0 {
				return false
			}
		}
		return true
	})
}

func TestRaftBackupJobDeleteBlockedWhenRunning(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupJob{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// create job
	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 1, Name: "running-job", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "* * * * *", Enabled: true,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "job created", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	// seed a running event on both nodes
	for _, n := range nodes {
		n.service.DB.Create(&clusterModels.BackupEvent{
			JobID: uintPtr(1), Status: "running",
		})
	}

	// try to delete — should fail
	deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
	err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "delete", Data: deleteRaw,
	})
	if err == nil {
		t.Fatal("expected error deleting job with running event")
	}
	if !strings.Contains(err.Error(), "backup_job_running") {
		t.Fatalf("expected backup_job_running error, got: %v", err)
	}

	// job still present
	for _, n := range nodes {
		var count int64
		n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
		if count != 1 {
			t.Fatalf("expected job still present on node %s, got %d", n.id, count)
		}
	}
}

func TestRaftBackupJobStateUpdateReplication(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// seed job
	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 1, Name: "state-job", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "* * * * *", Enabled: true,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	nextRun := now.Add(24 * time.Hour)

	stateRaw, _ := json.Marshal(map[string]any{
		"jobId":      1,
		"lastRunAt":  now,
		"lastStatus": "success",
		"lastError":  "",
		"nextRunAt":  nextRun,
		"encrypted":  true,
	})

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_state", Action: "update", Data: stateRaw,
	}); err != nil {
		t.Fatalf("update state via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "state update replicated", func() bool {
		for _, n := range nodes {
			var j clusterModels.BackupJob
			if err := n.service.DB.First(&j, 1).Error; err != nil {
				return false
			}
			if j.LastStatus != "success" || !j.Encrypted {
				return false
			}
		}
		return true
	})

	// test invalid status
	invalidRaw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "unknown"})
	err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_state", Action: "update", Data: invalidRaw,
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid_last_status") {
		t.Fatalf("expected invalid_last_status, got: %v", err)
	}
}

func TestRaftBackupJobFriendlySourceReplication(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// seed jobs
	for _, id := range []uint{1, 2, 3} {
		createRaw, _ := json.Marshal(clusterModels.BackupJob{
			ID: id, Name: "fs-job-" + strings.Trim(string([]byte("0123456789")[id%10]), ""),
			TargetID: 10, Mode: clusterModels.BackupJobModeDataset,
			SourceDataset: "tank/data", CronExpr: "* * * * *", Enabled: true,
			FriendlySrc: "old-name",
		})
		if err := leader.service.applyRaftCommand(clusterModels.Command{
			Type: "backup_job", Action: "create", Data: createRaw,
		}); err != nil {
			t.Fatalf("create job %d: %v", id, err)
		}
	}

	waitForClusterCondition(t, 8*time.Second, "initial jobs replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.BackupJob{}).Count(&count)
			if count != 3 {
				return false
			}
		}
		return true
	})

	// update friendly source on jobs 1 and 2
	fsRaw, _ := json.Marshal(map[string]any{
		"jobIds":      []uint{1, 2},
		"friendlySrc": "new-name",
	})

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_friendly_source", Action: "update", Data: fsRaw,
	}); err != nil {
		t.Fatalf("update friendly source via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "friendly source replicated", func() bool {
		for _, n := range nodes {
			var j1, j2, j3 clusterModels.BackupJob
			if err := n.service.DB.First(&j1, 1).Error; err != nil {
				return false
			}
			n.service.DB.First(&j2, 2)
			n.service.DB.First(&j3, 3)
			if j1.FriendlySrc != "new-name" || j2.FriendlySrc != "new-name" {
				return false
			}
			if j3.FriendlySrc != "old-name" {
				return false
			}
		}
		return true
	})
}
