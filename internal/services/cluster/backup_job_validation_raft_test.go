// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
)

func TestBackupJobPlacementFenceRaceIsDeterministicAcrossRaftMembers(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
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

func TestBackupJobRunnerResolutionRejectsNonVoter(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1)
	nonVoter := newClusterRaftTestNode(t, "node-non-voter")
	nodes = append(nodes, nonVoter)
	connectClusterRaftTestNodes(nodes)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes[:1], 8*time.Second)
	leader.service.NodeID = leader.id
	if err := leader.raft.AddNonvoter(raft.ServerID(nonVoter.id), nonVoter.addr, 0, 5*time.Second).Error(); err != nil {
		t.Fatalf("add non-voter: %v", err)
	}
	_, _, err := leader.service.backupJobRunnerVoter("node-non-voter")
	if err == nil || !strings.Contains(err.Error(), "backup_runner_not_raft_voter") {
		t.Fatalf("non-voter resolution error = %v", err)
	}
}

func TestRemoteBackupJobValidationRejectsSpoofedRunnerIdentity(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		2,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	runner := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	leader.service.AuthService = clusterAuthStub{}
	if err := leader.service.DB.Create(&clusterModels.BackupTarget{
		ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	sim.serveMux.HandleFunc("/api/intra-cluster/backup-job-safety-validation", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(internal.APIResponse[BackupJobSafetyValidationResult]{
			Status: "success",
			Data: BackupJobSafetyValidationResult{
				NodeID: "spoofed-node", RaftAppliedIndex: ^uint64(0), Valid: true,
				Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
				Classification: BackupJobSourceClassificationDataset, FriendlySource: "tank/data",
			},
		})
	})
	leader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != runner.id {
			return "", fmt.Errorf("unexpected runner")
		}
		return sim.Addr(), nil
	}
	enabled := true
	err := leader.service.ProposeBackupJobCreateContext(context.Background(), clusterServiceInterfaces.BackupJobReq{
		Name: "spoofed", TargetID: 1, RunnerNodeID: runner.id,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data", Enabled: &enabled,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "backup_runner_identity_mismatch") {
		t.Fatalf("spoofed identity error = %v", err)
	}

	var count int64
	if err := leader.service.DB.Model(&clusterModels.BackupJob{}).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("job count = %d, want 0", count)
	}
}
