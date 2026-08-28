// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

func TestIntegrationRaftRemovePeerReportsDependenciesAndDrains(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-key"}).Error; err != nil {
			t.Fatalf("seed cluster record: %v", err)
		}
	}

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var removed *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			removed = node
			break
		}
	}
	if removed == nil {
		t.Fatal("removed peer not found")
	}

	now := time.Now().UTC()
	seed := []any{
		&clusterModels.ClusterNode{NodeUUID: removed.id, GuestIDs: []uint{41}},
		&clusterModels.BackupTarget{
			ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
		},
		&clusterModels.BackupJob{
			ID: 11, Name: "job", TargetID: 1, RunnerNodeID: removed.id,
			Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
			CronExpr: "0 * * * *", Enabled: true,
		},
		&clusterModels.ReplicationPolicy{
			ID: 21, Name: "policy", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 41,
			SourceNodeID: removed.id, ActiveNodeID: removed.id, OwnerEpoch: 1,
			CronExpr: "0 * * * *", Enabled: true,
		},
		&clusterModels.ReplicationLease{
			ID: 31, PolicyID: 21, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 41,
			OwnerNodeID: removed.id, OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour),
		},
		&clusterModels.BackupJobOperation{
			JobID: 11, Token: "backup-token", Operation: clusterModels.BackupJobOperationBackup,
			State: clusterModels.BackupJobOperationQueued, HolderNodeID: removed.id,
			Revision: 1, AcquiredAt: now, UpdatedAt: now,
		},
		&clusterModels.ReplicationRunOperation{
			PolicyID: 21, Token: "replication-token", State: clusterModels.ReplicationRunOperationRunning,
			HolderNodeID: removed.id, Revision: 1, AcquiredAt: now, UpdatedAt: now,
		},
	}
	for _, value := range seed {
		if err := leader.service.DB.Create(value).Error; err != nil {
			t.Fatalf("seed %T: %v", value, err)
		}
	}

	leader.service.joinVersionForNode = func(context.Context, raft.Server, string) (string, error) {
		return cmd.Version, nil
	}
	request := RemoveMembershipRequest{
		LeaveID:   uuid.NewString(),
		NodeID:    removed.id,
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}
	err := leader.service.RemoveMembership(context.Background(), request, removed.id)
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("RemovePeer error = %v, want PeerRemovalBlockedError", err)
	}
	if blocked.Conflict.NodeID != removed.id {
		t.Fatalf("blocked node = %q, want %q", blocked.Conflict.NodeID, removed.id)
	}
	wantKinds := map[string]bool{
		PeerRemovalDependencyBackupJob:            true,
		PeerRemovalDependencyReplicationPolicy:    true,
		PeerRemovalDependencyReplicationLease:     true,
		PeerRemovalDependencyBackupOperation:      true,
		PeerRemovalDependencyReplicationOperation: true,
	}
	if len(blocked.Conflict.Dependencies) != len(wantKinds) {
		t.Fatalf("dependencies = %#v", blocked.Conflict.Dependencies)
	}
	for _, dependency := range blocked.Conflict.Dependencies {
		if !wantKinds[dependency.Kind] {
			t.Fatalf("unexpected dependency %#v", dependency)
		}
		delete(wantKinds, dependency.Kind)
	}
	if len(wantKinds) != 0 {
		t.Fatalf("missing dependency kinds: %#v", wantKinds)
	}

	var voterCount int
	configuration := leader.raft.GetConfiguration()
	if configuration.Error() != nil {
		t.Fatalf("get blocked configuration: %v", configuration.Error())
	}
	for _, server := range configuration.Configuration().Servers {
		if server.Suffrage == raft.Voter {
			voterCount++
		}
	}
	if voterCount != 3 {
		t.Fatalf("voter count after blocked removal = %d, want 3", voterCount)
	}

	for _, value := range []any{
		&clusterModels.ReplicationRunOperation{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.BackupJob{},
		&clusterModels.ClusterNode{},
	} {
		if err := leader.service.DB.Where("1 = 1").Delete(value).Error; err != nil {
			t.Fatalf("clear %T: %v", value, err)
		}
	}

	if err := leader.service.RemoveMembership(context.Background(), request, removed.id); err != nil {
		t.Fatalf("RemoveMembership after drain: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestRemoveMembershipReportsAllSubmittedGuests(t *testing.T) {
	service := &Service{mutationGate: newOpenTestMutationGate(t)}
	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{NodeID: "node-2", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 10, Name: "vm"},
		{NodeID: "node-2", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 20, Name: "jail"},
	})
	err := service.RemoveMembership(context.Background(), RemoveMembershipRequest{
		LeaveID: uuid.NewString(), NodeID: "node-2", Inventory: report,
	}, "node-2")
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || len(blocked.Conflict.Dependencies) != 2 {
		t.Fatalf("error=%v conflict=%+v", err, blocked)
	}
}

func TestIntegrationRaftRemovedPeerCannotStartRuntimeWork(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.Cluster{},
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-key"}).Error; err != nil {
			t.Fatalf("seed cluster record: %v", err)
		}
	}

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var removed *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			removed = node
			break
		}
	}
	if removed == nil {
		t.Fatal("removed peer not found")
	}
	leader.service.joinVersionForNode = func(context.Context, raft.Server, string) (string, error) {
		return cmd.Version, nil
	}
	if err := leader.service.RemoveMembership(context.Background(), RemoveMembershipRequest{
		LeaveID:   uuid.NewString(),
		NodeID:    removed.id,
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}, removed.id); err != nil {
		t.Fatalf("RemoveMembership: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
	now := time.Now().UTC()
	checks := map[string]func() error{
		"backup acquire": func() error {
			return leader.service.AcquireBackupJobOperation(clusterModels.BackupJobOperationAcquire{
				JobID: 1, Token: "backup-acquire", Operation: clusterModels.BackupJobOperationBackup,
				HolderNodeID: removed.id, AcquiredAt: now,
			}, false)
		},
		"backup start": func() error {
			return leader.service.TransitionBackupJobOperation("start", clusterModels.BackupJobOperationTransition{
				JobID: 1, Token: "backup-start", Operation: clusterModels.BackupJobOperationBackup,
				HolderNodeID: removed.id, OccurredAt: now,
			}, false)
		},
		"backup schedule claim": func() error {
			return leader.service.ApplyBackupJobScheduleDecision(clusterModels.BackupJobScheduleDecision{
				JobID: 1, ClaimToken: "backup-schedule", HolderNodeID: removed.id, DecidedAt: now,
			}, false)
		},
		"restore acquire": func() error {
			return leader.service.AcquireBackupTargetRestoreOperation(clusterModels.BackupTargetRestoreOperationAcquire{
				Token: "restore-acquire", TargetID: 1, HolderNodeID: removed.id,
				DestinationDataset: "tank/restore", RequestPayload: `{}`, AcquiredAt: now,
			}, false)
		},
		"restore start": func() error {
			return leader.service.TransitionBackupTargetRestoreOperation("start", clusterModels.BackupTargetRestoreOperationTransition{
				Token: "restore-start", TargetID: 1, HolderNodeID: removed.id,
				DestinationDataset: "tank/restore", RequestPayload: `{}`, OccurredAt: now,
			}, false)
		},
		"replication schedule claim": func() error {
			return leader.service.ApplyReplicationPolicyScheduleDecision(clusterModels.ReplicationPolicyScheduleDecision{
				PolicyID: 1, ClaimToken: "replication-schedule", HolderNodeID: removed.id, DecidedAt: now,
			}, false)
		},
		"replication start": func() error {
			return leader.service.StartReplicationRun(clusterModels.ReplicationRunOperationTransition{
				PolicyID: 1, Token: "replication-start", HolderNodeID: removed.id, OccurredAt: now,
			}, false)
		},
		"replication lease": func() error {
			return leader.service.UpsertReplicationLease(clusterModels.ReplicationLease{
				PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 1,
				OwnerNodeID: removed.id, OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour),
			}, false)
		},
		"guest operation": func() error {
			return leader.service.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
				GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 1,
				Operation: clusterModels.ReplicationGuestOperationMigration,
				Token:     "migration", OwnerNodeID: removed.id, TargetNodeID: leader.id,
				TaskID: 1, AcquiredAt: now,
			}, false)
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			err := check()
			if err == nil || !strings.Contains(err.Error(), "backup_runner_not_raft_member") {
				t.Fatalf("error = %v, want removed-voter rejection", err)
			}
		})
	}
}

func TestIntegrationRaftForceRemovePeerRemovesExternallyFencedMember(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var target *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			target = node
			break
		}
	}
	if target == nil {
		t.Fatal("target unavailable")
	}
	if _, err := leader.service.ForceRemovePeer(context.Background(), ForceRemovePeerRequest{
		NodeID: target.id,
	}); err == nil || !strings.Contains(err.Error(), "cluster_force_target_fence_ack_required") {
		t.Fatalf("missing acknowledgement error=%v", err)
	}
	result, err := leader.service.ForceRemovePeer(context.Background(), ForceRemovePeerRequest{
		NodeID:                 target.id,
		TargetExternallyFenced: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.MembershipRemoved || result.CleanupAcknowledged {
		t.Fatalf("result=%+v", result)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestForceRemovePeerRequiresConsensus(t *testing.T) {
	service := &Service{NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)}
	_, err := service.ForceRemovePeer(context.Background(), ForceRemovePeerRequest{
		NodeID:                 "node-2",
		TargetExternallyFenced: true,
	})
	var consensusErr *ClusterConsensusError
	if !errors.As(err, &consensusErr) {
		t.Fatalf("error = %v, want ClusterConsensusError", err)
	}
}

func TestIntegrationRaftForceRemovePeerDoesNotBypassQuorum(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var target *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			target = node
			break
		}
	}
	if target == nil {
		t.Fatal("target unavailable")
	}
	leader.transport.DisconnectAll()
	target.transport.DisconnectAll()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	_, err := leader.service.ForceRemovePeer(ctx, ForceRemovePeerRequest{
		NodeID:                 target.id,
		TargetExternallyFenced: true,
	})
	var consensusErr *ClusterConsensusError
	if !errors.As(err, &consensusErr) {
		t.Fatalf("error = %v, want ClusterConsensusError", err)
	}
}
