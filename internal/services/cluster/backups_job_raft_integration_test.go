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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestRaftBackupJobCRUDTwoNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	job := clusterModels.BackupJob{
		ID: 1, Name: "raft-job", TargetID: 10,
		Mode:          clusterModels.BackupJobModeDataset,
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

func TestRaftBackupJobDeleteBlockedWhenReserved(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// create job
	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 1, Name: "running-job", TargetID: 10,
		Mode:          clusterModels.BackupJobModeDataset,
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

	acquireRaw, _ := json.Marshal(clusterModels.BackupJobOperationAcquire{
		JobID: 1, Token: "backup:node-a:running", Operation: clusterModels.BackupJobOperationBackup,
		HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(),
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_operation", Action: "acquire", Data: acquireRaw,
	}); err != nil {
		t.Fatalf("acquire operation: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "operation replicated", func() bool {
		for _, node := range nodes {
			var count int64
			node.service.DB.Model(&clusterModels.BackupJobOperation{}).Where("job_id = ?", 1).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
	err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "delete", Data: deleteRaw,
	})
	if err == nil || !strings.Contains(err.Error(), "backup_job_running") {
		t.Fatalf("expected backup_job_running error, got: %v", err)
	}

	for _, n := range nodes {
		var jobCount, operationCount int64
		n.service.DB.Model(&clusterModels.BackupJob{}).Count(&jobCount)
		n.service.DB.Model(&clusterModels.BackupJobOperation{}).Count(&operationCount)
		if jobCount != 1 || operationCount != 1 {
			t.Fatalf("node %s state mismatch: jobs=%d operations=%d", n.id, jobCount, operationCount)
		}
	}
}

func TestRaftBackupJobDeleteIgnoresFollowerLocalRunningEvent(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	createRaw, _ := json.Marshal(clusterModels.BackupJob{
		ID: 7, Name: "local-event-job", TargetID: 10,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "* * * * *", Enabled: true,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "job created", func() bool {
		for _, node := range nodes {
			var count int64
			node.service.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", 7).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	var follower *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			follower = node
			break
		}
	}
	if follower == nil {
		t.Fatal("follower not found")
	}
	if err := follower.service.DB.Create(&clusterModels.BackupEvent{
		JobID: uintPtr(7), Status: "running", StartedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed follower-local event: %v", err)
	}

	deleteRaw, _ := json.Marshal(map[string]any{"id": 7})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "delete", Data: deleteRaw,
	}); err != nil {
		t.Fatalf("delete with follower-local telemetry: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "job deleted identically", func() bool {
		for _, node := range nodes {
			var count int64
			node.service.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", 7).Count(&count)
			if count != 0 {
				return false
			}
		}
		return true
	})

	var eventCount int64
	if err := follower.service.DB.Model(&clusterModels.BackupEvent{}).
		Where("job_id = ?", 7).Count(&eventCount).Error; err != nil || eventCount != 1 {
		t.Fatalf("follower-local event history changed: count=%d err=%v", eventCount, err)
	}
}

func TestRaftBackupJobDeleteAndReservationAreAtomicallyOrdered(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	for iteration := uint(1); iteration <= 12; iteration++ {
		jobID := 100 + iteration
		createRaw, _ := json.Marshal(clusterModels.BackupJob{
			ID: jobID, Name: "delete-acquire-race", TargetID: 10,
			Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
			CronExpr: "* * * * *", Enabled: true,
		})
		if err := leader.service.applyRaftCommand(clusterModels.Command{
			Type: "backup_job", Action: "create", Data: createRaw,
		}); err != nil {
			t.Fatalf("iteration %d create: %v", iteration, err)
		}

		operationToken := "backup:node-a:race-" + strconv.FormatUint(uint64(iteration), 10)
		acquireRaw, _ := json.Marshal(clusterModels.BackupJobOperationAcquire{
			JobID: jobID, Token: operationToken,
			Operation:    clusterModels.BackupJobOperationBackup,
			HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(),
		})
		deleteRaw, _ := json.Marshal(map[string]any{"id": jobID})
		var acquireErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			acquireErr = leader.service.applyRaftCommand(clusterModels.Command{
				Type: "backup_job_operation", Action: "acquire", Data: acquireRaw,
			})
		}()
		go func() {
			defer wg.Done()
			deleteErr = leader.service.applyRaftCommand(clusterModels.Command{
				Type: "backup_job", Action: "delete", Data: deleteRaw,
			})
		}()
		wg.Wait()
		if (acquireErr == nil) == (deleteErr == nil) {
			t.Fatalf("iteration %d expected exactly one winner: acquire=%v delete=%v", iteration, acquireErr, deleteErr)
		}

		waitForClusterCondition(t, 8*time.Second, "delete/acquire race convergence", func() bool {
			expectedJobs := int64(0)
			expectedOperations := int64(0)
			if acquireErr == nil {
				expectedJobs = 1
				expectedOperations = 1
			}
			for _, node := range nodes {
				var jobCount, operationCount int64
				node.service.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", jobID).Count(&jobCount)
				node.service.DB.Model(&clusterModels.BackupJobOperation{}).Where("job_id = ?", jobID).Count(&operationCount)
				if jobCount != expectedJobs || operationCount != expectedOperations {
					return false
				}
			}
			return true
		})

		if acquireErr == nil {
			transitionRaw, _ := json.Marshal(clusterModels.BackupJobOperationTransition{
				JobID: jobID, Token: operationToken,
				Operation:    clusterModels.BackupJobOperationBackup,
				HolderNodeID: "node-a", OccurredAt: time.Now().UTC(),
			})
			if err := leader.service.applyRaftCommand(clusterModels.Command{
				Type: "backup_job_operation", Action: "abort", Data: transitionRaw,
			}); err != nil {
				t.Fatalf("iteration %d cleanup operation: %v", iteration, err)
			}
			if err := leader.service.applyRaftCommand(clusterModels.Command{
				Type: "backup_job", Action: "delete", Data: deleteRaw,
			}); err != nil {
				t.Fatalf("iteration %d cleanup job: %v", iteration, err)
			}
		}
	}
}

func TestRunningJobIDsForTargetReadsReplicatedOperationOnNonRunnerLeader(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	runnerNodeID := ""
	for _, node := range nodes {
		if node.id != leader.id {
			runnerNodeID = node.id
			break
		}
	}
	if runnerNodeID == "" {
		t.Fatal("remote runner not found")
	}
	targetRaw, _ := json.Marshal(clusterModels.BackupTargetToReplicationPayload(clusterModels.BackupTarget{
		ID: 9, Name: "target", SSHHost: "root@backup", SSHKey: "key", BackupRoot: "tank/backups", Enabled: true,
	}))
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_target", Action: "create", Data: targetRaw,
	}); err != nil {
		t.Fatalf("create target: %v", err)
	}

	for _, job := range []clusterModels.BackupJob{
		{
			ID: 41, Name: "remote-runner", TargetID: 9, RunnerNodeID: runnerNodeID,
			Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/remote",
			CronExpr: "0 0 * * *", Enabled: true,
		},
		{
			ID: 42, Name: "leader-event-only", TargetID: 9, RunnerNodeID: leader.id,
			Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/local",
			CronExpr: "0 0 * * *", Enabled: true,
		},
	} {
		raw, _ := json.Marshal(job)
		if err := leader.service.applyRaftCommand(clusterModels.Command{
			Type: "backup_job", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create job %d: %v", job.ID, err)
		}
	}

	acquireRaw, _ := json.Marshal(clusterModels.BackupJobOperationAcquire{
		JobID: 41, Token: "backup:" + runnerNodeID + ":active",
		Operation: clusterModels.BackupJobOperationBackup, HolderNodeID: runnerNodeID,
		AcquiredAt: time.Now().UTC(),
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_operation", Action: "acquire", Data: acquireRaw,
	}); err != nil {
		t.Fatalf("acquire remote operation: %v", err)
	}

	// This event exists only on the API ingress node and must not create a false
	// positive now that durable operations are the status source.
	if err := leader.service.DB.Create(&clusterModels.BackupEvent{
		JobID: uintPtr(42), Status: "running", StartedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed leader-local event: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "remote operation replicated", func() bool {
		for _, node := range nodes {
			var count int64
			node.service.DB.Model(&clusterModels.BackupJobOperation{}).
				Where("job_id = ?", 41).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	ids, err := leader.service.RunningJobIDsForTarget(9)
	if err != nil {
		t.Fatalf("read durable status on non-runner leader: %v", err)
	}
	if len(ids) != 1 || ids[0] != 41 {
		t.Fatalf("expected remote operation 41 only, got %v", ids)
	}
	for _, node := range nodes {
		if node == leader {
			continue
		}
		if _, err := node.service.RunningJobIDsForTarget(9); err == nil || !strings.Contains(err.Error(), "not_leader") {
			t.Fatalf("follower %s was allowed to serve potentially stale status: %v", node.id, err)
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
		Mode:          clusterModels.BackupJobModeDataset,
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

	legacyRaw, _ := json.Marshal(map[string]any{
		"jobId": 1, "lastStatus": "failed", "lastError": "legacy follower",
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_state", Action: "update", Data: legacyRaw,
	}); err != nil {
		t.Fatalf("legacy state update via raft: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "legacy state preserves encryption", func() bool {
		for _, n := range nodes {
			var job clusterModels.BackupJob
			if err := n.service.DB.First(&job, 1).Error; err != nil || !job.Encrypted || job.LastStatus != "failed" {
				return false
			}
		}
		return true
	})

	unencryptedRaw, _ := json.Marshal(map[string]any{
		"version": 1, "jobId": 1, "lastStatus": "success", "encrypted": false,
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job_state", Action: "update", Data: unencryptedRaw,
	}); err != nil {
		t.Fatalf("unencrypted state update via raft: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "unencrypted state replicated", func() bool {
		for _, n := range nodes {
			var job clusterModels.BackupJob
			if err := n.service.DB.First(&job, 1).Error; err != nil || job.Encrypted {
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
