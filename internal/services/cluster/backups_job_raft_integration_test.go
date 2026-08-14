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

func TestIntegrationRaftBackupJobDeleteAndReservationAreAtomicallyOrdered(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
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

func TestIntegrationRaftRunningJobIDsReadsOperationOnNonRunnerLeader(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupEvent{},
	)
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
