// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestBackupJobRunnerVoterFromConfiguration(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-local", Address: "node-local:7000", Suffrage: raft.Voter},
		{ID: "node-voter", Address: "node-voter:7000", Suffrage: raft.Voter},
		{ID: "node-non-voter", Address: "node-non-voter:7000", Suffrage: raft.Nonvoter},
	}}

	server, local, err := backupJobRunnerVoterFromConfiguration(configuration, "node-local", "node-voter")
	if err != nil || local || server.ID != "node-voter" {
		t.Fatalf("remote voter = %+v local=%v err=%v", server, local, err)
	}
	_, local, err = backupJobRunnerVoterFromConfiguration(configuration, "node-local", "node-local")
	if err != nil || !local {
		t.Fatalf("local voter local=%v err=%v", local, err)
	}
	_, _, err = backupJobRunnerVoterFromConfiguration(configuration, "node-local", "node-non-voter")
	if err == nil || !strings.Contains(err.Error(), "backup_runner_not_raft_voter") {
		t.Fatalf("non-voter error = %v", err)
	}
	_, _, err = backupJobRunnerVoterFromConfiguration(configuration, "node-local", "removed-node")
	if err == nil || !strings.Contains(err.Error(), "backup_runner_not_raft_member") {
		t.Fatalf("removed member error = %v", err)
	}
}

func TestIntegrationRaftBackupJobPlacementFenceRaceIsDeterministicAcrossMembers(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	expected, err := clusterModels.LoadBackupJobPlacementFence(
		leader.service.DB,
		clusterModels.BackupJobModeVM,
		808,
		"node-runner",
	)
	if err != nil {
		t.Fatalf("load expected fence: %v", err)
	}

	policyData, err := json.Marshal(clusterModels.ReplicationPolicyPayload{
		Policy: clusterModels.ReplicationPolicy{
			ID: 1, Name: "race-policy", GuestType: clusterModels.BackupJobModeVM, GuestID: 808,
			SourceNodeID: "node-runner", ActiveNodeID: "node-runner", OwnerEpoch: 1,
			CronExpr: "0 * * * *", Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: policyData,
	}); err != nil {
		t.Fatalf("commit policy: %v", err)
	}

	jobData, err := json.Marshal(clusterModels.BackupJobCommandPayload{
		Job: clusterModels.BackupJob{
			ID: 1, Name: "raced-job", TargetID: 1, RunnerNodeID: "node-runner",
			Mode:          clusterModels.BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/808", Recursive: true,
			CronExpr: "0 0 * * *", Enabled: true,
		},
		PlacementFence: &expected,
	})
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	err = leader.service.applyRaftCommand(clusterModels.Command{
		Type: "backup_job", Action: "create", Data: jobData,
	})
	if err == nil || !strings.Contains(err.Error(), "backup_job_placement_changed") {
		t.Fatalf("raced apply error = %v", err)
	}

	committedIndex := leader.raft.AppliedIndex()
	waitForClusterCondition(t, 5*time.Second, "raced command application", func() bool {
		for _, node := range nodes {
			if node.raft.AppliedIndex() < committedIndex {
				return false
			}
			var jobs, policies int64
			if node.service.DB.Model(&clusterModels.BackupJob{}).Count(&jobs).Error != nil ||
				node.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&policies).Error != nil ||
				jobs != 0 || policies != 1 {
				return false
			}
		}
		return true
	})
}
