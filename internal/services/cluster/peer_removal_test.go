// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	hub "github.com/alchemillahq/sylve/internal/events"
	"github.com/hashicorp/raft"
)

func raftVoterCount(t *testing.T, node *clusterRaftTestNode) int {
	t.Helper()
	future := node.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
	voters := 0
	for _, server := range future.Configuration().Servers {
		if server.Suffrage == raft.Voter {
			voters++
		}
	}
	return voters
}

func TestIntegrationRaftRemoveMemberReportsDependencies(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	removed := leaveTestFollowers(nodes, leader)[0]

	now := time.Now().UTC()
	seed := []any{
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
	err := leader.service.RemoveMember(context.Background(), removed.id, true)
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || blocked.Conflict.NodeID != removed.id {
		t.Fatalf("remove error = %v, want the node's dependencies", err)
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
	}
	if voters := raftVoterCount(t, leader); voters != 3 {
		t.Fatalf("voters after a blocked removal = %d", voters)
	}
	for _, value := range []any{
		&clusterModels.ReplicationRunOperation{}, &clusterModels.BackupJobOperation{},
		&clusterModels.ReplicationLease{}, &clusterModels.ReplicationPolicy{}, &clusterModels.BackupJob{},
	} {
		if err := leader.service.DB.Where("1 = 1").Delete(value).Error; err != nil {
			t.Fatalf("clear %T: %v", value, err)
		}
	}

	seedGuestIdentityJoinTestClaim(t, leader, removed.id, clusterModels.ReplicationGuestTypeVM, 41)
	if err := leader.service.apply("cluster_ssh_identity", "upsert", clusterModels.ClusterSSHIdentity{
		NodeUUID: removed.id, SSHHost: removed.id, PublicKey: "key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.RemoveMember(context.Background(), removed.id, false); !errors.As(err, &blocked) ||
		len(blocked.Conflict.Dependencies) != 1 || blocked.Conflict.Dependencies[0].Kind != PeerRemovalDependencyGuest {
		t.Fatalf("unconfirmed removal of a node with guests = %v", err)
	}

	events, unsubscribe := hub.SSE.Subscribe()
	defer unsubscribe()
	if err := leader.service.RemoveMember(context.Background(), removed.id, true); err != nil {
		t.Fatalf("remove keeping guests: %v", err)
	}
	waitForClusterEvent(t, events, "left-panel-refresh")
	waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{leader}, 2, 8*time.Second)
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 41, "")
	var identities int64
	if err := leader.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Where("node_uuid = ?", removed.id).Count(&identities).Error; err != nil || identities != 0 {
		t.Fatalf("removed node's SSH identities = %d, %v", identities, err)
	}
	if err := leader.service.RemoveMember(context.Background(), removed.id, true); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("second removal = %v", err)
	}
	if err := leader.service.RemoveMember(context.Background(), leader.id, true); !errors.Is(err, ErrRemoveSelf) {
		t.Fatalf("self removal = %v", err)
	}
}

func TestIntegrationRaftRemovedPeerCannotStartRuntimeWork(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	removed := leaveTestFollowers(nodes, leader)[0]
	if err := leader.service.RemoveMember(context.Background(), removed.id, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{leader}, 2, 8*time.Second)
	now := time.Now().UTC()
	checks := map[string]func() error{
		"backup acquire": func() error {
			return leader.service.AcquireBackupJobOperation(clusterModels.BackupJobOperationAcquire{
				JobID: 1, Token: "backup-acquire", Operation: clusterModels.BackupJobOperationBackup,
				HolderNodeID: removed.id, AcquiredAt: now,
			})
		},
		"backup start": func() error {
			return leader.service.TransitionBackupJobOperation("start", clusterModels.BackupJobOperationTransition{
				JobID: 1, Token: "backup-start", Operation: clusterModels.BackupJobOperationBackup,
				HolderNodeID: removed.id, OccurredAt: now,
			})
		},
		"backup schedule claim": func() error {
			return leader.service.ApplyBackupJobScheduleDecision(clusterModels.BackupJobScheduleDecision{
				JobID: 1, ClaimToken: "backup-schedule", HolderNodeID: removed.id, DecidedAt: now,
			})
		},
		"restore acquire": func() error {
			return leader.service.AcquireBackupTargetRestoreOperation(clusterModels.BackupTargetRestoreOperationAcquire{
				Token: "restore-acquire", TargetID: 1, HolderNodeID: removed.id,
				DestinationDataset: "tank/restore", RequestPayload: `{}`, AcquiredAt: now,
			})
		},
		"restore start": func() error {
			return leader.service.TransitionBackupTargetRestoreOperation("start", clusterModels.BackupTargetRestoreOperationTransition{
				Token: "restore-start", TargetID: 1, HolderNodeID: removed.id,
				DestinationDataset: "tank/restore", RequestPayload: `{}`, OccurredAt: now,
			})
		},
		"replication schedule claim": func() error {
			return leader.service.ApplyReplicationPolicyScheduleDecision(clusterModels.ReplicationPolicyScheduleDecision{
				PolicyID: 1, ClaimToken: "replication-schedule", HolderNodeID: removed.id, DecidedAt: now,
			})
		},
		"replication start": func() error {
			return leader.service.StartReplicationRun(clusterModels.ReplicationRunOperationTransition{
				PolicyID: 1, Token: "replication-start", HolderNodeID: removed.id, OccurredAt: now,
			})
		},
		"replication lease": func() error {
			return leader.service.UpsertReplicationLease(clusterModels.ReplicationLease{
				PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 1,
				OwnerNodeID: removed.id, OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour),
			})
		},
		"guest operation": func() error {
			return leader.service.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
				GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 1,
				Operation: clusterModels.ReplicationGuestOperationMigration,
				Token:     "migration", OwnerNodeID: removed.id, TargetNodeID: leader.id,
				TaskID: 1, AcquiredAt: now,
			})
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

func waitForClusterEvent(t *testing.T, events <-chan hub.Event, eventType string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s event", eventType)
		}
	}
}

func TestRemoveMemberRequiresALeader(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t), NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)})
	if err := service.RemoveMember(context.Background(), "node-2", true); !errors.Is(err, raft.ErrNotLeader) {
		t.Fatalf("error = %v, want not leader", err)
	}
}

func TestIntegrationRaftRemoveMemberDoesNotBypassQuorum(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 2)
	target := leaveTestFollowers(nodes, leader)[0]
	leader.transport.DisconnectAll()
	target.transport.DisconnectAll()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if err := leader.service.RemoveMember(ctx, target.id, true); err == nil {
		t.Fatal("a leader without quorum removed a member")
	}
	if voters := raftVoterCount(t, leader); voters != 2 {
		t.Fatalf("voters = %d, want 2", voters)
	}
}
